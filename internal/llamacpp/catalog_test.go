package llamacpp

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

// TestCatalogEntriesWellFormed catches the most expensive class of
// catalog bug: an entry that's obviously broken (bad URL, dangling
// slug, zero context size) before the operator ever clicks Install.
// It does *not* check sha256 — those are tracked through
// TestCatalogSHA256Gaps, which is informational, not a hard failure.
func TestCatalogEntriesWellFormed(t *testing.T) {
	t.Parallel()

	c := NewCatalog()
	entries := c.Entries()
	if len(entries) == 0 {
		t.Fatal("catalog has no entries")
	}

	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		t.Run(entry.ID, func(t *testing.T) {
			if strings.TrimSpace(entry.ID) == "" {
				t.Fatal("entry has empty ID")
			}
			if _, dup := seen[entry.ID]; dup {
				t.Fatalf("duplicate catalog ID %q", entry.ID)
			}
			seen[entry.ID] = struct{}{}

			// ID style: lowercase, hyphen/underscore/dot-separated.
			// Reject uppercase here — slugs leak into /v1/models so
			// case matters.
			for _, r := range entry.ID {
				switch {
				case r >= 'a' && r <= 'z':
				case r >= '0' && r <= '9':
				case r == '-' || r == '_' || r == '.':
				default:
					t.Errorf("ID %q has disallowed character %q", entry.ID, r)
				}
			}

			if strings.TrimSpace(entry.DisplayName) == "" {
				t.Errorf("entry %q has empty DisplayName", entry.ID)
			}

			parsed, err := url.Parse(entry.HuggingFaceURL)
			if err != nil {
				t.Fatalf("entry %q has unparseable HuggingFaceURL: %v", entry.ID, err)
			}
			if parsed.Scheme != "https" {
				t.Errorf("entry %q URL is not https: %q", entry.ID, entry.HuggingFaceURL)
			}
			if !strings.HasSuffix(strings.ToLower(parsed.Path), ".gguf") {
				t.Errorf("entry %q URL is not a .gguf file: %q", entry.ID, entry.HuggingFaceURL)
			}
			// Direct download URLs include /resolve/. Catch the
			// "pasted the repo page URL by mistake" failure mode at
			// authoring time, not at install time.
			if !strings.Contains(parsed.Path, "/resolve/") {
				t.Errorf("entry %q URL is not a direct-download URL (missing /resolve/): %q", entry.ID, entry.HuggingFaceURL)
			}

			if entry.SizeBytes <= 0 {
				t.Errorf("entry %q has non-positive SizeBytes %d", entry.ID, entry.SizeBytes)
			}
			if entry.RecommendedContext <= 0 {
				t.Errorf("entry %q has non-positive RecommendedContext %d", entry.ID, entry.RecommendedContext)
			}
			if entry.RecommendedContext > entry.Capabilities.MaxContextTokens {
				t.Errorf("entry %q RecommendedContext %d exceeds MaxContextTokens %d", entry.ID, entry.RecommendedContext, entry.Capabilities.MaxContextTokens)
			}
		})
	}
}

// TestCatalogLookup covers the hit / miss / empty paths so the install
// handler can rely on the surfaced errors.
func TestCatalogLookup(t *testing.T) {
	t.Parallel()
	c := NewCatalog()

	got, err := c.Lookup("qwen2.5-0_5b-instruct-q4_k_m")
	if err != nil {
		t.Fatalf("lookup of known entry failed: %v", err)
	}
	if got.ID != "qwen2.5-0_5b-instruct-q4_k_m" {
		t.Fatalf("lookup returned wrong entry %q", got.ID)
	}

	if _, err := c.Lookup("not-a-real-model"); !errors.Is(err, ErrCatalogEntryNotFound) {
		t.Fatalf("expected ErrCatalogEntryNotFound, got %v", err)
	}

	if _, err := c.Lookup("  "); !errors.Is(err, ErrCatalogIDRequired) {
		t.Fatalf("expected ErrCatalogIDRequired, got %v", err)
	}
}

// TestCatalogSHA256Gaps is informational — it lists entries that ship
// without a pinned sha so the backfill task stays visible without
// breaking the build. Surface as a logged note rather than t.Error so
// CI doesn't go red on a known WIP gap; flip to t.Errorf before the
// first stable release.
func TestCatalogSHA256Gaps(t *testing.T) {
	t.Parallel()
	gaps := NewCatalog().CatalogSHA256Gaps()
	if len(gaps) == 0 {
		return
	}
	t.Logf("catalog entries missing pinned sha256 (backfill before stable release): %v", gaps)
}

// TestParsePasteURL covers the v1 paste-URL parser, including the two
// common operator mistakes we want a clean error for.
func TestParsePasteURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantErr  error
		wantID   string
		wantFile string
	}{
		{
			name:     "valid direct gguf",
			input:    "https://huggingface.co/bartowski/Qwen2.5-7B-Instruct-GGUF/resolve/main/Qwen2.5-7B-Instruct-Q4_K_M.gguf",
			wantID:   "qwen2-5-7b-instruct-q4-k-m",
			wantFile: "Qwen2.5-7B-Instruct-Q4_K_M.gguf",
		},
		{
			name:    "empty",
			input:   "",
			wantErr: ErrPasteURLEmpty,
		},
		{
			name:    "whitespace only",
			input:   "   ",
			wantErr: ErrPasteURLEmpty,
		},
		{
			name:    "http scheme rejected",
			input:   "http://huggingface.co/x/resolve/main/y.gguf",
			wantErr: ErrPasteURLInvalid,
		},
		{
			name:    "repo page URL",
			input:   "https://huggingface.co/bartowski/Qwen2.5-7B-Instruct-GGUF",
			wantErr: ErrPasteURLNotDirect,
		},
		{
			name:    "tree URL",
			input:   "https://huggingface.co/bartowski/Qwen2.5-7B-Instruct-GGUF/tree/main",
			wantErr: ErrPasteURLNotDirect,
		},
		{
			name:    "wrong file type (tokenizer.json)",
			input:   "https://huggingface.co/foo/resolve/main/tokenizer.json",
			wantErr: ErrPasteURLNotGGUF,
		},
		{
			name:    "no path",
			input:   "https://huggingface.co",
			wantErr: ErrPasteURLNotGGUF,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			canonical, file, id, err := ParsePasteURL(tt.input)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if canonical != strings.TrimSpace(tt.input) {
				t.Errorf("canonical mismatch: got %q, want %q", canonical, tt.input)
			}
			if file != tt.wantFile {
				t.Errorf("filename mismatch: got %q, want %q", file, tt.wantFile)
			}
			if id != tt.wantID {
				t.Errorf("derived ID mismatch: got %q, want %q", id, tt.wantID)
			}
		})
	}
}
