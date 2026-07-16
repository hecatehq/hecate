package safetext

import (
	"strings"
	"testing"
	"time"
)

func TestSanitizeErrorMessageRedactsInlineImages(t *testing.T) {
	t.Parallel()

	payload := strings.Repeat("A", 128)
	tests := []string{
		"invalid data:image/png;base64," + payload + " in request",
		`invalid {"source":{"type":"base64","media_type":"image/png","data":"` + payload + `"}}`,
	}
	for _, input := range tests {
		got := SanitizeErrorMessage(input)
		if strings.Contains(got, payload) || !strings.Contains(got, "[redacted inline image]") {
			t.Fatalf("SanitizeErrorMessage() = %q", got)
		}
	}
}

func TestSanitizeErrorMessageRedactsJSONEscapedInlineImageURI(t *testing.T) {
	t.Parallel()

	payload := strings.Repeat("Ab9+", 16)
	input := `invalid {"image_url":"data:image\/png;base64,` + payload + `"}`
	got := SanitizeErrorMessage(input)
	if strings.Contains(got, payload) || !strings.Contains(got, "[redacted inline image]") {
		t.Fatalf("SanitizeErrorMessage() = %q", got)
	}
}

func TestSanitizeErrorMessageRedactsWrappedInlineImages(t *testing.T) {
	t.Parallel()

	lineA := strings.Repeat("A", 76)
	lineB := strings.Repeat("B", 76)
	tests := []string{
		"invalid data:image/png;base64," + lineA + "\n" + lineB + "; provider rejected image",
		"invalid data:image/png;base64," + lineA + "\r\n  " + lineB + "; provider rejected image",
		`invalid data:image/png;base64,` + lineA + `\n` + lineB + `; provider rejected image`,
		`invalid {"source":{"type":"base64","media_type":"image/png","data":"` + lineA + `\r\n` + lineB + `"}}; provider rejected image`,
	}
	for _, input := range tests {
		got := SanitizeErrorMessage(input)
		if strings.Contains(got, lineA) || strings.Contains(got, lineB) {
			t.Fatalf("SanitizeErrorMessage leaked wrapped payload: %q", got)
		}
		if !strings.Contains(got, "[redacted inline image]") || !strings.Contains(got, "provider rejected image") {
			t.Fatalf("SanitizeErrorMessage() = %q", got)
		}
	}
}

func TestSanitizeErrorMessageRedactsNonBase64ImageDataURI(t *testing.T) {
	t.Parallel()

	input := "provider rejected data:image/svg+xml,<svg><text>private operator data</text></svg>"
	got := SanitizeErrorMessage(input)
	if strings.Contains(got, "private operator data") || !strings.Contains(got, "[redacted inline image]") {
		t.Fatalf("SanitizeErrorMessage() leaked non-base64 image data: %q", got)
	}
}

func TestSanitizeErrorMessageFailsClosedForMalformedBase64ImageDataURI(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"provider rejected data:image/png;base64,QUJD!private operator data",
		"provider rejected data:image/png;base64,QU=JD private operator data",
		"provider rejected data:image/png;base64,%private operator data",
		"provider rejected data:image/png;base64,;private operator data",
	} {
		got := SanitizeErrorMessage(input)
		if strings.Contains(got, "private operator data") || strings.Contains(got, ";private") ||
			!strings.Contains(got, "[redacted inline image]") {
			t.Fatalf("SanitizeErrorMessage() leaked malformed inline image data: %q", got)
		}
	}
}

func TestSanitizeErrorMessagePreservesOrdinaryTextAndCapsLength(t *testing.T) {
	t.Parallel()

	if got := SanitizeErrorMessage("provider unavailable"); got != "provider unavailable" {
		t.Fatalf("ordinary message = %q", got)
	}
	got := SanitizeErrorMessage(strings.Repeat("diagnostic message! ", MaxErrorMessageBytes))
	if len(got) > MaxErrorMessageBytes || !strings.HasSuffix(got, "…") {
		t.Fatalf("capped message length/suffix = %d/%q", len(got), got[len(got)-3:])
	}
}

func TestSanitizeErrorMessageRedactsGenericLongBase64Runs(t *testing.T) {
	t.Parallel()

	payload := strings.Repeat("Ab9_", 64)
	for _, input := range []string{
		`invalid {"url":"` + payload + `"}`,
		"invalid image=" + payload + " upstream",
		"invalid input_image " + payload + " upstream",
		"invalid input_image " + strings.Repeat("A", 76) + "\r\n  " + strings.Repeat("B", 76) + "; upstream rejected it",
		payload,
	} {
		got := SanitizeErrorMessage(input)
		if strings.Contains(got, payload) || strings.Contains(got, strings.Repeat("A", 76)) ||
			strings.Contains(got, strings.Repeat("B", 76)) || !strings.Contains(got, "[redacted inline payload]") {
			t.Fatalf("SanitizeErrorMessage() = %q", got)
		}
	}
}

func TestSanitizeErrorMessageRedactsAdjacentLongBase64Runs(t *testing.T) {
	t.Parallel()

	first := strings.Repeat("A", 128)
	second := strings.Repeat("B", 128)
	input := "invalid image=" + first + "," + second + " upstream"
	want := "invalid [redacted inline payload],[redacted inline payload] upstream"
	if got := SanitizeErrorMessage(input); got != want {
		t.Fatalf("SanitizeErrorMessage() = %q, want %q", got, want)
	}
}

func TestRedactWrappedBase64HandlesLargeAdversarialRunsInLinearTime(t *testing.T) {
	const maxDuration = 2 * time.Second
	tests := []struct {
		name         string
		input        string
		wantRedacted bool
	}{
		{name: "padding only", input: strings.Repeat("=", 64<<10)},
		{name: "alternating padding", input: strings.Repeat("=A", 32<<10)},
		{name: "JSON escaped slashes", input: strings.Repeat(`\/`, 32<<10), wantRedacted: true},
		{
			name:         "wrapped JSON escaped slashes",
			input:        strings.Repeat(`\/`, 76) + `\r\n` + strings.Repeat(`\/`, 76),
			wantRedacted: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			started := time.Now()
			got := redactWrappedBase64(test.input)
			if elapsed := time.Since(started); elapsed > maxDuration {
				t.Fatalf("redactWrappedBase64() took %v for %d adversarial bytes; want structurally linear processing", elapsed, len(test.input))
			}
			if test.wantRedacted {
				if got != "[redacted inline payload]" {
					t.Fatalf("redactWrappedBase64() = %q, want privacy redaction", got)
				}
				if sanitized := SanitizeErrorMessage(test.input); sanitized != "[redacted inline payload]" {
					t.Fatalf("SanitizeErrorMessage() = %q, want privacy redaction", sanitized)
				}
			} else if got != test.input {
				t.Fatalf("redactWrappedBase64() changed an unwrapped adversarial run")
			}
		})
	}
}

func TestSanitizeErrorMessageRedactsRemoteURLCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "userinfo query and fragment",
			in:   "image fetch failed: https://operator:password@images.example.test/cat.png?X-Amz-Signature=private#access-token",
			want: "image fetch failed: https://[redacted]@images.example.test/cat.png?[redacted]#[redacted]",
		},
		{
			name: "JSON escaped slashes",
			in:   `rejected https:\/\/operator:password@images.example.test\/cat.png?sig=private#access-token`,
			want: "rejected https://[redacted]@images.example.test/cat.png?[redacted]#[redacted]",
		},
		{
			name: "malformed URL still redacts",
			in:   "rejected https://operator:password@images.example.test/%ZZ?sig=private#access-token",
			want: "rejected https://[redacted]@images.example.test/%ZZ?[redacted]#[redacted]",
		},
		{
			name: "URL without sensitive components",
			in:   "rejected https://images.example.test/cat.png",
			want: "rejected https://images.example.test/cat.png",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := SanitizeErrorMessage(test.in); got != test.want {
				t.Fatalf("SanitizeErrorMessage() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRedactSensitiveRemoteURLsHandlesLargeSchemeLikeTextInLinearTime(t *testing.T) {
	const maxDuration = 2 * time.Second
	const schemeLikeBytes = 64 << 10

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "long non-URL token before sensitive URL",
			input: strings.Repeat("a", schemeLikeBytes) + " https://operator:password@images.example/cat.png?signature=secret#access-token",
			want:  strings.Repeat("a", schemeLikeBytes) + " https://[redacted]@images.example/cat.png?[redacted]#[redacted]",
		},
		{
			name:  "long hierarchical scheme",
			input: strings.Repeat("a", schemeLikeBytes) + "://operator:password@files.example/image.png?token=secret",
			want:  strings.Repeat("a", schemeLikeBytes) + "://[redacted]@files.example/image.png?[redacted]",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			started := time.Now()
			got := redactSensitiveRemoteURLs(test.input)
			if elapsed := time.Since(started); elapsed > maxDuration {
				t.Fatalf("redactSensitiveRemoteURLs() took %v for %d adversarial bytes; want structurally linear processing", elapsed, len(test.input))
			}
			if got != test.want {
				t.Fatalf("redactSensitiveRemoteURLs() did not preserve text and redact URL credentials")
			}
		})
	}
}

func TestSanitizeErrorMessageSeparatesAdjacentRemoteURLs(t *testing.T) {
	t.Parallel()

	input := "failed https://public.example/a,https://operator:password@private.example/b?signature=secret"
	want := "failed https://public.example/a,https://[redacted]@private.example/b?[redacted]"
	if got := SanitizeErrorMessage(input); got != want {
		t.Fatalf("SanitizeErrorMessage() = %q, want %q", got, want)
	}
}

func TestSanitizeErrorMessageNormalizesJSONEscapedRemoteURLs(t *testing.T) {
	t.Parallel()

	input := `{"error":"https:\u002f\u002foperator:password@images.example/a.png\u003fsignature=secret"}`
	want := `{"error":"https://[redacted]@images.example/a.png?[redacted]"}`
	if got := SanitizeErrorMessage(input); got != want {
		t.Fatalf("SanitizeErrorMessage() = %q, want %q", got, want)
	}
}

func TestSanitizeErrorMessageRedactsCredentialsForHierarchicalSchemes(t *testing.T) {
	t.Parallel()

	input := "fetch ftp://operator:password@files.example/image.png?token=secret failed"
	want := "fetch ftp://[redacted]@files.example/image.png?[redacted] failed"
	if got := SanitizeErrorMessage(input); got != want {
		t.Fatalf("SanitizeErrorMessage() = %q, want %q", got, want)
	}
}

func TestSanitizeErrorTypeAllowsTokensAndRejectsUntrustedValues(t *testing.T) {
	t.Parallel()

	if got := SanitizeErrorType(" invalid_request_error ", "fallback"); got != "invalid_request_error" {
		t.Fatalf("ordinary type = %q, want invalid_request_error", got)
	}
	for _, value := range []string{
		"https://operator:password@example.test/error",
		strings.Repeat("x", MaxErrorTypeBytes+1),
		"invalid request error",
	} {
		if got := SanitizeErrorType(value, "upstream_error"); got != "upstream_error" {
			t.Fatalf("SanitizeErrorType(%q) = %q, want upstream_error", value, got)
		}
	}
}
