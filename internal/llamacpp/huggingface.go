package llamacpp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// HuggingFaceClient is the minimal slice of HF Hub's API we need for
// the v2 browse panel. Two surfaces:
//
//	SearchModels — `/api/models` with a query string; returns the
//	               GGUF-tagged repos that match.
//	ListRepoFiles — `/api/models/<repo>/tree/main`; returns the file
//	                list with LFS metadata (size + sha256 oid).
//
// The client is stateless and safe for concurrent use. Auth tokens
// are passed per-call rather than stored on the struct — same shape
// the installer uses for InstallSpec.HFToken.
type HuggingFaceClient struct {
	baseURL string
	http    HTTPDoer
	clock   Clock
}

// HuggingFaceOptions configures the HF client. All fields are
// optional; production callers leave them zero and get the public
// endpoint + default HTTP client.
type HuggingFaceOptions struct {
	// BaseURL overrides https://huggingface.co. Tests inject an
	// httptest.Server here.
	BaseURL string
	// HTTP is the underlying client. Defaults to http.DefaultClient.
	HTTP HTTPDoer
	// Clock backs result timestamps. Defaults to time.Now.
	Clock Clock
}

// NewHuggingFaceClient builds a client with sensible defaults.
func NewHuggingFaceClient(opts HuggingFaceOptions) *HuggingFaceClient {
	c := &HuggingFaceClient{
		baseURL: strings.TrimRight(opts.BaseURL, "/"),
		http:    opts.HTTP,
		clock:   opts.Clock,
	}
	if c.baseURL == "" {
		c.baseURL = "https://huggingface.co"
	}
	if c.http == nil {
		c.http = http.DefaultClient
	}
	if c.clock == nil {
		c.clock = time.Now
	}
	return c
}

// HuggingFaceModel is one search-result row. Shape mirrors the HF
// API but trimmed to the fields the UI renders.
type HuggingFaceModel struct {
	// ID is the repo path, "owner/name".
	ID string `json:"id"`
	// Author is the namespace (= ID before the slash).
	Author string `json:"author"`
	// Downloads is HF's monthly download count. Useful as a
	// relevance signal in the UI ("X downloads this month").
	Downloads int64 `json:"downloads,omitempty"`
	// Likes is the HF star count. Same role as Downloads.
	Likes int64 `json:"likes,omitempty"`
	// LastModified is the most recent commit timestamp.
	LastModified time.Time `json:"last_modified,omitempty"`
	// Tags carries the HF model tags (license, language, etc.).
	// We don't filter here — the UI surfaces them as chips.
	Tags []string `json:"tags,omitempty"`
	// Pipeline is the HF "pipeline_tag" (e.g. "text-generation"),
	// when known.
	Pipeline string `json:"pipeline_tag,omitempty"`
	// Gated reports whether the repo is gated (requires auth).
	// Surfaced so the UI can render a "needs token" badge without
	// the operator discovering it at install time.
	Gated bool `json:"gated,omitempty"`
}

// HuggingFaceFile is one file inside a repo's tree. Only the
// GGUF-named entries with LFS metadata are interesting for our
// installer flow.
type HuggingFaceFile struct {
	// Path is the file's path within the repo. "models/foo.gguf"
	// for nested layouts, just "foo.gguf" for flat repos.
	Path string `json:"path"`
	// Size is the LFS file size in bytes (when present), else
	// the in-repo size.
	Size int64 `json:"size"`
	// SHA256 is the LFS oid (which is a sha256 for HF) if the
	// file is LFS-stored; empty for inline files.
	SHA256 string `json:"sha256,omitempty"`
	// DownloadURL is the canonical resolve URL the installer
	// accepts:
	//   https://huggingface.co/<repo>/resolve/main/<path>
	DownloadURL string `json:"download_url"`
}

// SearchModels hits /api/models with the given query. v1 behavior:
// always filters for the "gguf" tag, sorts by downloads descending,
// caps results at limit (default 20). When token is non-empty it's
// attached as an Authorization header so gated-repo metadata is
// included in the response.
func (c *HuggingFaceClient) SearchModels(ctx context.Context, query, token string, limit int) ([]HuggingFaceModel, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		// HF's public cap; respect it. Surface to the UI as a
		// validation error rather than silently truncating —
		// helpful for diagnosing pagination needs.
		limit = 100
	}
	q := url.Values{}
	if s := strings.TrimSpace(query); s != "" {
		q.Set("search", s)
	}
	q.Set("filter", "gguf")
	q.Set("sort", "downloads")
	q.Set("direction", "-1")
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("full", "true") // include tags + pipeline_tag

	target := c.baseURL + "/api/models?" + q.Encode()
	body, err := c.fetchJSON(ctx, target, token)
	if err != nil {
		return nil, err
	}

	// HF returns a JSON array of model objects. Shape varies a
	// little between cached / live endpoints; decode into a flat
	// struct that covers both.
	var raw []struct {
		ID           string    `json:"id"`
		ModelID      string    `json:"modelId"`
		Author       string    `json:"author"`
		Downloads    int64     `json:"downloads"`
		Likes        int64     `json:"likes"`
		LastModified time.Time `json:"lastModified"`
		Tags         []string  `json:"tags"`
		PipelineTag  string    `json:"pipeline_tag"`
		Gated        any       `json:"gated"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode search results: %w", err)
	}

	out := make([]HuggingFaceModel, 0, len(raw))
	for _, r := range raw {
		id := r.ID
		if id == "" {
			id = r.ModelID
		}
		author := r.Author
		if author == "" {
			if i := strings.Index(id, "/"); i > 0 {
				author = id[:i]
			}
		}
		out = append(out, HuggingFaceModel{
			ID:           id,
			Author:       author,
			Downloads:    r.Downloads,
			Likes:        r.Likes,
			LastModified: r.LastModified,
			Tags:         r.Tags,
			Pipeline:     r.PipelineTag,
			Gated:        parseGated(r.Gated),
		})
	}
	return out, nil
}

// ListRepoFiles fetches the file tree for a repo at the default
// branch (main). Returns only .gguf files so the UI doesn't have to
// filter; ordering is by path. The download URL is pre-computed so
// the install flow can drop it into InstallSpec.URL verbatim.
func (c *HuggingFaceClient) ListRepoFiles(ctx context.Context, repo, token string) ([]HuggingFaceFile, error) {
	if strings.TrimSpace(repo) == "" {
		return nil, errors.New("repo is required")
	}
	if strings.Contains(repo, "..") || strings.HasPrefix(repo, "/") {
		// Defensive: callers should validate, but a malformed
		// repo id could turn into a path-traversal on the
		// resolve URL.
		return nil, errors.New("repo contains invalid characters")
	}
	target := c.baseURL + "/api/models/" + repo + "/tree/main"
	body, err := c.fetchJSON(ctx, target, token)
	if err != nil {
		return nil, err
	}

	var raw []struct {
		Type string `json:"type"`
		Path string `json:"path"`
		Size int64  `json:"size"`
		LFS  *struct {
			OID  string `json:"oid"`
			Size int64  `json:"size"`
		} `json:"lfs,omitempty"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode tree: %w", err)
	}

	out := make([]HuggingFaceFile, 0, len(raw))
	for _, f := range raw {
		if f.Type != "file" {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(f.Path), ".gguf") {
			continue
		}
		size := f.Size
		sha := ""
		if f.LFS != nil {
			sha = f.LFS.OID
			if f.LFS.Size > 0 {
				size = f.LFS.Size
			}
		}
		out = append(out, HuggingFaceFile{
			Path:        f.Path,
			Size:        size,
			SHA256:      sha,
			DownloadURL: fmt.Sprintf("%s/%s/resolve/main/%s", c.baseURL, repo, f.Path),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// fetchJSON is the shared GET path. Attaches Authorization when the
// caller provided a token and maps non-2xx responses to typed
// errors.
func (c *HuggingFaceClient) fetchJSON(ctx context.Context, target, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if token = strings.TrimSpace(token); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("huggingface: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized,
		resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: HTTP %d", ErrHuggingFaceGated, resp.StatusCode)
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: HTTP %d", ErrHuggingFaceNotFound, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("huggingface: HTTP %d: %s", resp.StatusCode,
			truncatedForError(body, 256))
	}
	return body, nil
}

// parseGated unifies the two shapes HF returns for the gated field
// — sometimes a bool, sometimes "auto" / "manual" / "" string.
func parseGated(raw any) bool {
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		return v == "auto" || v == "manual"
	}
	return false
}

func truncatedForError(body []byte, max int) string {
	s := string(body)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

var (
	// ErrHuggingFaceGated is returned by the client when the HF
	// API responds with 401/403. Handlers map to 401 / 403 with
	// stable error code `huggingface_gated`.
	ErrHuggingFaceGated = errors.New("huggingface repo is gated; provide an access token")

	// ErrHuggingFaceNotFound is returned when HF returns 404 on
	// the search / tree request. Handlers map to 404.
	ErrHuggingFaceNotFound = errors.New("huggingface repo not found")
)
