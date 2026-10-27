// Package githubapp owns Hecate's repository-scoped GitHub App connection.
// It deliberately exposes no repository mutation or token-export operation.
package githubapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const (
	apiOrigin        = "https://api.github.com"
	maxResponseBytes = 2 << 20
	maxKeyBytes      = 64 << 10
)

var (
	ErrUnconfigured         = errors.New("GitHub App connection is not configured")
	ErrInvalidConfiguration = errors.New("GitHub App configuration is invalid")
	ErrInvalidReference     = errors.New("use a GitHub pull request URL without a query or fragment")
	ErrRepositoryDenied     = errors.New("repository is not enabled for this GitHub connection")
	ErrBusy                 = errors.New("GitHub connection is busy; try again shortly")
	ErrCredential           = errors.New("GitHub App credential could not be used; check the runtime configuration")
	ErrUnavailable          = errors.New("GitHub could not be reached; retry the inspection")
	ErrResponse             = errors.New("GitHub returned an incomplete or unexpected response")
	ErrScope                = errors.New("GitHub did not issue the requested repository-scoped read-only access")
	ErrHeadChanged          = errors.New("pull request changed during inspection; refresh before continuing")
	namePattern             = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	shaPattern              = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)
)

type Config struct {
	ClientID       string
	InstallationID int64
	PrivateKeyFile string
	Repositories   []string
}

func (c Config) Configured() bool {
	return c.ClientID != "" && c.InstallationID > 0 && c.PrivateKeyFile != "" && len(c.Repositories) > 0
}

func (c Config) Validate() error {
	if c.ClientID == "" && c.InstallationID == 0 && c.PrivateKeyFile == "" && len(c.Repositories) == 0 {
		return nil
	}
	if !c.Configured() || !filepath.IsAbs(c.PrivateKeyFile) || !namePattern.MatchString(c.ClientID) || len(c.ClientID) > 128 || len(c.Repositories) > 100 {
		return ErrInvalidConfiguration
	}
	for _, repository := range c.Repositories {
		parts := strings.Split(repository, "/")
		if len(parts) != 2 || !validName(parts[0]) || !validName(parts[1]) {
			return ErrInvalidConfiguration
		}
	}
	return nil
}

type Reference struct {
	Owner, Repository string
	Number            int
}

func ParseReference(raw string) (Reference, error) {
	if len(raw) > 1024 {
		return Reference{}, ErrInvalidReference
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return Reference{}, ErrInvalidReference
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" || !validName(parts[0]) || !validName(parts[1]) {
		return Reference{}, ErrInvalidReference
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n <= 0 || strconv.Itoa(n) != parts[3] {
		return Reference{}, ErrInvalidReference
	}
	return Reference{Owner: parts[0], Repository: parts[1], Number: n}, nil
}

func validName(s string) bool {
	return len(s) <= 100 && namePattern.MatchString(s) && s != "." && s != ".."
}
func (r Reference) FullName() string { return r.Owner + "/" + r.Repository }
func (r Reference) URL() string {
	return "https://github.com/" + r.FullName() + "/pull/" + strconv.Itoa(r.Number)
}

type Check struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	State  string `json:"state"`
}

type Inspection struct {
	URL         string    `json:"url"`
	Repository  string    `json:"repository"`
	Number      int       `json:"number"`
	HeadSHA     string    `json:"head_sha"`
	BaseSHA     string    `json:"base_sha"`
	State       string    `json:"state"`
	Draft       bool      `json:"draft"`
	Merged      bool      `json:"merged"`
	Checks      []Check   `json:"checks"`
	ChecksState string    `json:"checks_state"`
	Complete    bool      `json:"complete"`
	ObservedAt  time.Time `json:"observed_at"`
}

type HTTPError struct {
	Status     int
	RetryAfter int
}

func (e *HTTPError) Error() string {
	switch e.Status {
	case 401:
		return "GitHub rejected the App credentials"
	case 403:
		return "GitHub denied access or rate-limited this connection; check repository permissions and retry later"
	case 404:
		return "GitHub could not find this pull request with the configured App access"
	case 429:
		return "GitHub rate limit reached; retry later"
	default:
		return "GitHub inspection failed; retry later"
	}
}

type Client struct {
	config Config
	http   *http.Client
	now    func() time.Time
	slots  chan struct{}
}

func New(config Config) *Client {
	config.Repositories = append([]string(nil), config.Repositories...)
	return &Client{config: config, http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, now: time.Now, slots: make(chan struct{}, 2)}
}

func (c *Client) Inspect(ctx context.Context, rawURL string) (_ Inspection, resultErr error) {
	ctx, span := otel.Tracer("github.com/hecatehq/hecate/internal/githubapp").Start(ctx, "github.pull_request.inspect")
	defer func() {
		if resultErr != nil {
			span.SetStatus(codes.Error, "inspection_failed")
		}
		span.End()
	}()
	if err := ctx.Err(); err != nil {
		return Inspection{}, err
	}
	if c == nil || !c.config.Configured() {
		return Inspection{}, ErrUnconfigured
	}
	if err := c.config.Validate(); err != nil {
		return Inspection{}, err
	}
	ref, err := ParseReference(rawURL)
	if err != nil {
		return Inspection{}, err
	}
	allowed := false
	for _, repository := range c.config.Repositories {
		if strings.EqualFold(repository, ref.FullName()) {
			allowed = true
			break
		}
	}
	if !allowed {
		return Inspection{}, ErrRepositoryDenied
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return Inspection{}, ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	jwt, err := c.jwt()
	if err != nil {
		return Inspection{}, err
	}
	var credential struct {
		Token        string            `json:"token"`
		ExpiresAt    time.Time         `json:"expires_at"`
		Permissions  map[string]string `json:"permissions"`
		Repositories []struct {
			FullName string `json:"full_name"`
		} `json:"repositories"`
	}
	permissions := map[string]string{"pull_requests": "read", "checks": "read", "statuses": "read", "metadata": "read"}
	request := map[string]any{"repositories": []string{ref.Repository}, "permissions": permissions}
	if err := c.request(ctx, http.MethodPost, "/app/installations/"+strconv.FormatInt(c.config.InstallationID, 10)+"/access_tokens", jwt, request, &credential); err != nil {
		return Inspection{}, err
	}
	if credential.Token == "" || len(credential.Token) > 32<<10 {
		return Inspection{}, ErrResponse
	}
	// Tokens are single-inspection credentials, never provider/tool environment.
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer stop()
		_ = c.request(cleanup, http.MethodDelete, "/installation/token", credential.Token, nil, nil)
	}()
	if !credential.ExpiresAt.After(c.now().Add(30*time.Second)) || len(credential.Repositories) != 1 || !strings.EqualFold(credential.Repositories[0].FullName, ref.FullName()) {
		return Inspection{}, ErrScope
	}
	for permission, level := range credential.Permissions {
		if permissions[permission] != level {
			return Inspection{}, ErrScope
		}
	}
	for permission, level := range permissions {
		if credential.Permissions[permission] != level {
			return Inspection{}, ErrScope
		}
	}
	prefix := "/repos/" + ref.FullName()
	prPath := prefix + "/pulls/" + strconv.Itoa(ref.Number)
	var pr pullRequest
	if err := c.request(ctx, http.MethodGet, prPath, credential.Token, nil, &pr); err != nil {
		return Inspection{}, err
	}
	if err := pr.validate(ref); err != nil {
		return Inspection{}, err
	}
	result := Inspection{URL: ref.URL(), Repository: ref.FullName(), Number: ref.Number, HeadSHA: pr.Head.SHA, BaseSHA: pr.Base.SHA, State: pr.State, Draft: pr.Draft, Merged: pr.Merged, Checks: []Check{}, Complete: true}
	var checks struct {
		TotalCount int `json:"total_count"`
		Runs       []struct {
			Name, Status, Conclusion string
			HeadSHA                  string `json:"head_sha"`
		} `json:"check_runs"`
	}
	if err := c.request(ctx, http.MethodGet, prefix+"/commits/"+pr.Head.SHA+"/check-runs?per_page=100&filter=latest", credential.Token, nil, &checks); err != nil {
		return Inspection{}, err
	}
	if checks.TotalCount < 0 || len(checks.Runs) > 100 {
		return Inspection{}, ErrResponse
	}
	result.Complete = checks.TotalCount == len(checks.Runs)
	for _, run := range checks.Runs {
		if !strings.EqualFold(run.HeadSHA, pr.Head.SHA) {
			return Inspection{}, ErrResponse
		}
		state := "pending"
		if run.Status == "completed" {
			switch run.Conclusion {
			case "success":
				state = "success"
			case "neutral", "skipped":
				state = "skipped"
			case "failure", "cancelled", "timed_out", "action_required", "startup_failure", "stale":
				state = "failure"
			default:
				state = "unknown"
			}
		} else if run.Status != "queued" && run.Status != "in_progress" && run.Status != "waiting" && run.Status != "pending" && run.Status != "requested" {
			state = "unknown"
		}
		result.Checks = append(result.Checks, Check{Name: displayName(run.Name), Source: "check", State: state})
	}
	var statuses struct {
		SHA        string                            `json:"sha"`
		TotalCount int                               `json:"total_count"`
		Statuses   []struct{ Context, State string } `json:"statuses"`
	}
	if err := c.request(ctx, http.MethodGet, prefix+"/commits/"+pr.Head.SHA+"/status?per_page=100", credential.Token, nil, &statuses); err != nil {
		return Inspection{}, err
	}
	if !strings.EqualFold(statuses.SHA, pr.Head.SHA) || statuses.TotalCount < 0 || len(statuses.Statuses) > 100 {
		return Inspection{}, ErrResponse
	}
	result.Complete = result.Complete && statuses.TotalCount == len(statuses.Statuses)
	for _, status := range statuses.Statuses {
		state := status.State
		switch state {
		case "success", "pending", "failure":
		case "error":
			state = "failure"
		default:
			state = "unknown"
		}
		result.Checks = append(result.Checks, Check{Name: displayName(status.Context), Source: "status", State: state})
	}
	var current pullRequest
	if err := c.request(ctx, http.MethodGet, prPath, credential.Token, nil, &current); err != nil {
		return Inspection{}, err
	}
	if current.validate(ref) != nil || current.Head.SHA != pr.Head.SHA || current.Base.SHA != pr.Base.SHA || current.State != pr.State || current.Draft != pr.Draft || current.Merged != pr.Merged {
		return Inspection{}, ErrHeadChanged
	}
	result.ChecksState = summarize(result.Checks, result.Complete)
	result.ObservedAt = c.now().UTC()
	span.SetAttributes(attribute.String("hecate.github.checks_state", result.ChecksState), attribute.Bool("hecate.github.complete", result.Complete), attribute.Int("hecate.github.check_count", len(result.Checks)))
	return result, nil
}

type pullRequest struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	Draft  bool   `json:"draft"`
	Merged bool   `json:"merged"`
	Head   struct {
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		SHA  string `json:"sha"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"base"`
}

func (p pullRequest) validate(r Reference) error {
	if p.Number != r.Number || (p.State != "open" && p.State != "closed") || !shaPattern.MatchString(p.Head.SHA) || !shaPattern.MatchString(p.Base.SHA) || !strings.EqualFold(p.Base.Repo.FullName, r.FullName()) {
		return ErrResponse
	}
	return nil
}

func summarize(checks []Check, complete bool) string {
	if !complete {
		return "incomplete"
	}
	if len(checks) == 0 {
		return "not_reported"
	}
	state := "success"
	for _, check := range checks {
		if check.State == "failure" {
			return "failure"
		}
		if check.State == "unknown" {
			state = "unknown"
		} else if check.State == "pending" && state != "unknown" {
			state = "pending"
		}
	}
	return state
}

func displayName(value string) string {
	runes := []rune(value)
	if len(runes) > 200 {
		runes = runes[:200]
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return ' '
		}
		return r
	}, string(runes))
}

func (c *Client) jwt() (string, error) {
	// This is an operator-configured secret file, not agent workspace IO.
	before, err := os.Lstat(c.config.PrivateKeyFile)
	if err != nil || !before.Mode().IsRegular() || before.Size() > maxKeyBytes {
		return "", ErrCredential
	}
	// A special-file replacement between Lstat and open must not block a slot.
	f, err := openCredentialFile(c.config.PrivateKeyFile)
	if err != nil {
		return "", ErrCredential
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxKeyBytes || !os.SameFile(before, info) {
		return "", ErrCredential
	}
	body, err := io.ReadAll(io.LimitReader(f, maxKeyBytes+1))
	if err != nil || len(body) > maxKeyBytes {
		return "", ErrCredential
	}
	defer clear(body)
	block, rest := pem.Decode(body)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return "", ErrCredential
	}
	defer clear(block.Bytes)
	var key *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		var parsed any
		parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		key, _ = parsed.(*rsa.PrivateKey)
	default:
		return "", ErrCredential
	}
	if err != nil || key == nil || key.N.BitLen() < 2048 || key.N.BitLen() > 8192 || key.Validate() != nil {
		return "", ErrCredential
	}
	now := c.now()
	payload, _ := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(5 * time.Minute).Unix(), "iss": c.config.ClientID})
	unsigned := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", ErrCredential
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (c *Client) request(ctx context.Context, method, path, token string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return ErrResponse
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiOrigin+path, body)
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	req.Header.Set("User-Agent", "Hecate-GitHub-App")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		retry := 0
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
			retry = min(seconds, 86400)
		}
		return &HTTPError{Status: response.StatusCode, RetryAfter: retry}
	}
	if output == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return ErrResponse
	}
	if err := json.Unmarshal(data, output); err != nil {
		return ErrResponse
	}
	return nil
}

func ConfigurationHint() string {
	return fmt.Sprintf("Configure %s, %s, %s, and %s on the runtime host.", "HECATE_GITHUB_APP_CLIENT_ID", "HECATE_GITHUB_APP_INSTALLATION_ID", "HECATE_GITHUB_APP_PRIVATE_KEY_FILE", "HECATE_GITHUB_REPOSITORIES")
}
