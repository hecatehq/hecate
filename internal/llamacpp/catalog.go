package llamacpp

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Catalog is the read-only set of curated GGUF entries Hecate's local-
// models surface offers as one-click installs. It is intentionally
// small in v1: each entry is a specific quantization from a trusted
// converter account on HuggingFace.
//
// Update rules (see catalog_test.go for enforcement):
//
//   - Each ID is a stable lowercase-hyphen slug. Don't rename without
//     a migration plan — existing InstalledModel rows reference these.
//
//   - HuggingFaceURL must be the *direct* GGUF download URL, not the
//     repo page. The exact format is
//     https://huggingface.co/<repo>/resolve/<rev>/<file>.gguf
//
//   - SHA256 is optional during early bring-up but required before a
//     stable release. Empty values are listed by CatalogSHA256Gaps to
//     make the backfill task discoverable.
//
//   - SizeBytes is approximate ("size on HuggingFace at the time we
//     pinned the entry"). The installer doesn't gate on it.
//
//   - Capabilities default to Streaming:true, ToolCalling:"none".
//     Override per-entry when the model is known to behave (the v1
//     entries are conservative — none of these reliably tool-call).
//
// Operators install models outside this set via the paste-URL path
// (POST /hecate/v1/local-models/install with {url}). Those installs
// produce InstalledModel rows that aren't backed by a CatalogEntry.

// catalogEntries is the v1 set. Pinned to bartowski's converter
// account — the same converter LM Studio defaults to. All Q4_K_M
// (the "good default" quant for general use).
//
// SHA256 values are pulled from HuggingFace's LFS metadata
// (https://huggingface.co/api/models/<repo>/tree/main → lfs.oid) and
// match the on-disk file at the upstream `resolve/main` path at the
// time of pinning. Mismatches at install time are a hard fail. To
// bump a model to a newer revision, copy the new sha + size from
// HF's tree response and update both fields here.
var catalogEntries = []CatalogEntry{
	{
		ID:                 "llama-3.2-1b-instruct-q4_k_m",
		DisplayName:        "Llama 3.2 1B Instruct (Q4_K_M)",
		Description:        "Smallest Llama 3.2 instruct variant. Good for low-RAM machines and fast iteration.",
		HuggingFaceURL:     "https://huggingface.co/bartowski/Llama-3.2-1B-Instruct-GGUF/resolve/main/Llama-3.2-1B-Instruct-Q4_K_M.gguf",
		SHA256:             "6f85a640a97cf2bf5b8e764087b1e83da0fdb51d7c9fab7d0fece9385611df83",
		SizeBytes:          807_694_464,
		RecommendedContext: 4096,
		Capabilities:       Capabilities{Streaming: true, ToolCalling: "none", MaxContextTokens: 131072},
		License:            "llama-3.2",
	},
	{
		ID:                 "llama-3.2-3b-instruct-q4_k_m",
		DisplayName:        "Llama 3.2 3B Instruct (Q4_K_M)",
		Description:        "Mid-size Llama 3.2 instruct. Solid general-purpose default for laptops with 8 GB+ free RAM.",
		HuggingFaceURL:     "https://huggingface.co/bartowski/Llama-3.2-3B-Instruct-GGUF/resolve/main/Llama-3.2-3B-Instruct-Q4_K_M.gguf",
		SHA256:             "6c1a2b41161032677be168d354123594c0e6e67d2b9227c84f296ad037c728ff",
		SizeBytes:          2_019_377_696,
		RecommendedContext: 8192,
		Capabilities:       Capabilities{Streaming: true, ToolCalling: "none", MaxContextTokens: 131072},
		License:            "llama-3.2",
	},
	{
		ID:                 "qwen2.5-0_5b-instruct-q4_k_m",
		DisplayName:        "Qwen 2.5 0.5B Instruct (Q4_K_M)",
		Description:        "Tiny Qwen 2.5 instruct. Useful for sanity-checking the local runtime — downloads in seconds.",
		HuggingFaceURL:     "https://huggingface.co/bartowski/Qwen2.5-0.5B-Instruct-GGUF/resolve/main/Qwen2.5-0.5B-Instruct-Q4_K_M.gguf",
		SHA256:             "6eb923e7d26e9cea28811e1a8e852009b21242fb157b26149d3b188f3a8c8653",
		SizeBytes:          397_808_192,
		RecommendedContext: 8192,
		Capabilities:       Capabilities{Streaming: true, ToolCalling: "none", MaxContextTokens: 32768},
		License:            "apache-2.0",
	},
	{
		ID:                 "qwen2.5-3b-instruct-q4_k_m",
		DisplayName:        "Qwen 2.5 3B Instruct (Q4_K_M)",
		Description:        "Mid-size Qwen 2.5 instruct. Strong reasoning for its size.",
		HuggingFaceURL:     "https://huggingface.co/bartowski/Qwen2.5-3B-Instruct-GGUF/resolve/main/Qwen2.5-3B-Instruct-Q4_K_M.gguf",
		SHA256:             "9c9f56a391a3abbd5b89d0245bf6106081bcc3173119d4229235dd9d23253f94",
		SizeBytes:          1_929_903_264,
		RecommendedContext: 8192,
		Capabilities:       Capabilities{Streaming: true, ToolCalling: "none", MaxContextTokens: 32768},
		License:            "qwen-research",
	},
	{
		ID:                 "qwen2.5-7b-instruct-q4_k_m",
		DisplayName:        "Qwen 2.5 7B Instruct (Q4_K_M)",
		Description:        "Larger Qwen 2.5 instruct. Best general-purpose model in this catalog for 16 GB+ machines.",
		HuggingFaceURL:     "https://huggingface.co/bartowski/Qwen2.5-7B-Instruct-GGUF/resolve/main/Qwen2.5-7B-Instruct-Q4_K_M.gguf",
		SHA256:             "65b8fcd92af6b4fefa935c625d1ac27ea29dcb6ee14589c55a8f115ceaaa1423",
		SizeBytes:          4_683_074_240,
		RecommendedContext: 8192,
		Capabilities:       Capabilities{Streaming: true, ToolCalling: "none", MaxContextTokens: 131072},
		License:            "apache-2.0",
	},
	{
		ID:                 "mistral-7b-instruct-v0.3-q4_k_m",
		DisplayName:        "Mistral 7B Instruct v0.3 (Q4_K_M)",
		Description:        "Mistral's open-weight instruct model. Function-calling capable upstream but Hecate keeps it on streaming-only for v1.",
		HuggingFaceURL:     "https://huggingface.co/bartowski/Mistral-7B-Instruct-v0.3-GGUF/resolve/main/Mistral-7B-Instruct-v0.3-Q4_K_M.gguf",
		SHA256:             "1270d22c0fbb3d092fb725d4d96c457b7b687a5f5a715abe1e818da303e562b6",
		SizeBytes:          4_372_812_000,
		RecommendedContext: 8192,
		Capabilities:       Capabilities{Streaming: true, ToolCalling: "none", MaxContextTokens: 32768},
		License:            "apache-2.0",
	},
	{
		ID:                 "phi-3-mini-4k-instruct-q4_k_m",
		DisplayName:        "Phi-3 Mini 4K Instruct (Q4_K_M)",
		Description:        "Microsoft's small reasoning model. Strong at chain-of-thought relative to its size.",
		HuggingFaceURL:     "https://huggingface.co/bartowski/Phi-3-mini-4k-instruct-GGUF/resolve/main/Phi-3-mini-4k-instruct-Q4_K_M.gguf",
		SHA256:             "28a89b4ddb5766355f24e362ae4078b4c35b9ca9568df5fc9e6d9aeee4dee834",
		SizeBytes:          2_393_231_360,
		RecommendedContext: 4096,
		Capabilities:       Capabilities{Streaming: true, ToolCalling: "none", MaxContextTokens: 4096},
		License:            "mit",
	},
	{
		ID:                 "gemma-2-2b-it-q4_k_m",
		DisplayName:        "Gemma 2 2B IT (Q4_K_M)",
		Description:        "Google's small instruction-tuned Gemma 2. Well-behaved for general chat.",
		HuggingFaceURL:     "https://huggingface.co/bartowski/gemma-2-2b-it-GGUF/resolve/main/gemma-2-2b-it-Q4_K_M.gguf",
		SHA256:             "e0aee85060f168f0f2d8473d7ea41ce2f3230c1bc1374847505ea599288a7787",
		SizeBytes:          1_708_582_752,
		RecommendedContext: 8192,
		Capabilities:       Capabilities{Streaming: true, ToolCalling: "none", MaxContextTokens: 8192},
		License:            "gemma",
	},
}

// Catalog exposes the curated set. It's intentionally a thin wrapper
// rather than a bare slice so future versions can layer caching, async
// refresh from a remote manifest, or feature-flag filtering without
// touching call sites.
type Catalog struct {
	entries []CatalogEntry
	byID    map[string]CatalogEntry
}

// NewCatalog returns the default catalog backed by the compiled-in
// entries. Tests can pass a custom slice via NewCatalogFrom.
func NewCatalog() *Catalog {
	return NewCatalogFrom(catalogEntries)
}

// NewCatalogFrom wraps an arbitrary slice. Used by tests and reserved
// for a future remote-manifest path. The entries are copied so
// callers can't mutate the catalog post-construction.
func NewCatalogFrom(entries []CatalogEntry) *Catalog {
	copied := make([]CatalogEntry, len(entries))
	copy(copied, entries)
	byID := make(map[string]CatalogEntry, len(copied))
	for _, entry := range copied {
		byID[entry.ID] = entry
	}
	return &Catalog{entries: copied, byID: byID}
}

// Entries returns a defensive copy ordered the way the catalog was
// declared. Stable order matters: the UI surfaces these directly.
func (c *Catalog) Entries() []CatalogEntry {
	out := make([]CatalogEntry, len(c.entries))
	copy(out, c.entries)
	return out
}

// Lookup returns the entry with the given ID. ErrCatalogEntryNotFound
// when the ID isn't recognized — used by the install handler to route
// catalog vs paste-URL flows.
func (c *Catalog) Lookup(id string) (CatalogEntry, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return CatalogEntry{}, ErrCatalogIDRequired
	}
	entry, ok := c.byID[id]
	if !ok {
		return CatalogEntry{}, fmt.Errorf("%w: %q", ErrCatalogEntryNotFound, id)
	}
	return entry, nil
}

// ErrCatalogEntryNotFound is returned by Catalog.Lookup when the
// requested ID isn't in the curated set. Wraps the ID via fmt.Errorf
// so error messages stay diagnostic without leaking internals.
var ErrCatalogEntryNotFound = errors.New("catalog entry not found")

// ErrCatalogIDRequired is returned by Catalog.Lookup when the input
// is empty / whitespace. Surface-level sanity check — keeps the
// install handler simple.
var ErrCatalogIDRequired = errors.New("catalog id is required")

// CatalogSHA256Gaps lists IDs that ship without a pinned sha256. The
// installer uses it to gate per-entry warnings; the catalog test uses
// it to track the backfill TODO without failing the build.
//
// Drain this list before any stable release. Filling a sha means
// fetching the file from HuggingFace, computing sha256, and pasting
// the hex digest into the CatalogEntry literal above — the test will
// then refuse a regression that re-empties the field.
func (c *Catalog) CatalogSHA256Gaps() []string {
	var gaps []string
	for _, entry := range c.entries {
		if strings.TrimSpace(entry.SHA256) == "" {
			gaps = append(gaps, entry.ID)
		}
	}
	return gaps
}

// ParsePasteURL validates a paste-URL install input and returns the
// pieces the installer needs: the resolved filename, a derived
// InstalledModel.ID, and the canonical URL (with whitespace trimmed).
//
// v1 policy: only direct GGUF download URLs are accepted. Repo URLs,
// blob URLs, and tree URLs surface a "give us the .gguf URL" error.
// Gated repos surface a "not supported in v1" error after the
// installer probes them.
func ParsePasteURL(raw string) (canonical, filename, derivedID string, err error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", "", ErrPasteURLEmpty
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", "", "", fmt.Errorf("%w: %v", ErrPasteURLInvalid, err)
	}
	if parsed.Scheme != "https" {
		return "", "", "", fmt.Errorf("%w: scheme must be https", ErrPasteURLInvalid)
	}
	if parsed.Host == "" {
		return "", "", "", fmt.Errorf("%w: missing host", ErrPasteURLInvalid)
	}
	// HuggingFace direct download URLs use /<repo>/resolve/<rev>/<file>
	// — we don't enforce the prefix (operator might mirror) but we do
	// want a .gguf suffix on the file portion so we don't quietly
	// download a tokenizer.json or a model card by mistake.
	path := strings.TrimSpace(parsed.Path)
	if path == "" || path == "/" {
		return "", "", "", fmt.Errorf("%w: URL has no file path", ErrPasteURLNotGGUF)
	}
	slash := strings.LastIndex(path, "/")
	if slash < 0 || slash == len(path)-1 {
		return "", "", "", fmt.Errorf("%w: URL has no filename component", ErrPasteURLNotGGUF)
	}
	filename = path[slash+1:]
	lower := strings.ToLower(filename)
	if !strings.HasSuffix(lower, ".gguf") {
		// Heuristic for the most common operator mistake — pasting
		// the repo page URL instead of the direct file URL.
		if strings.Contains(parsed.Path, "/resolve/") {
			return "", "", "", fmt.Errorf("%w: URL doesn't end in .gguf", ErrPasteURLNotGGUF)
		}
		return "", "", "", ErrPasteURLNotDirect
	}
	// Derive a slug from the filename. Strip the .gguf, lowercase,
	// replace runs of non-alphanumerics with a single hyphen, trim
	// trailing hyphens.
	base := strings.TrimSuffix(filename, ".gguf")
	base = strings.TrimSuffix(base, ".GGUF")
	derivedID = slugify(base)
	if derivedID == "" {
		return "", "", "", fmt.Errorf("%w: filename has no slug-able content", ErrPasteURLNotGGUF)
	}
	return trimmed, filename, derivedID, nil
}

// slugify reduces an arbitrary string to a lowercase hyphen slug. Used
// to derive InstalledModel.ID from a paste-URL filename. Kept private
// because the rules are an implementation detail of ParsePasteURL.
func slugify(in string) string {
	var b strings.Builder
	b.Grow(len(in))
	prevHyphen := true
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			prevHyphen = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			prevHyphen = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	out := b.String()
	out = strings.TrimRight(out, "-")
	return out
}

var (
	// ErrPasteURLEmpty surfaces when the operator submits {"url":""}.
	ErrPasteURLEmpty = errors.New("paste url is empty")

	// ErrPasteURLInvalid wraps net/url's parse error or our own
	// scheme / host checks.
	ErrPasteURLInvalid = errors.New("paste url is invalid")

	// ErrPasteURLNotGGUF triggers when the URL has a path but
	// doesn't end in .gguf. The most common operator mistake.
	ErrPasteURLNotGGUF = errors.New("paste url does not point at a .gguf file")

	// ErrPasteURLNotDirect triggers when the URL ends in .gguf
	// (or seems file-shaped) but is missing the /resolve/ segment
	// HuggingFace direct-download URLs use. The handler maps this
	// to a 400 with copy-the-direct-URL guidance.
	ErrPasteURLNotDirect = errors.New("paste url is not a direct download url")
)
