// Package safetext centralizes privacy-safe rendering of error strings that
// may cross persistence, telemetry, health, or user-facing boundaries.
package safetext

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

const MaxErrorMessageBytes = 4096

const MaxErrorTypeBytes = 128

var (
	remoteURLJSONEscape = strings.NewReplacer(
		`\u002f`, "/", `\u002F`, "/",
		`\u003a`, ":", `\u003A`, ":",
		`\u003f`, "?", `\u003F`, "?",
		`\u0023`, "#",
		`\u0040`, "@",
	)
)

// ErrorMessage removes inline image bytes and URL credential components, then
// caps untrusted upstream error text before it is recorded or returned. It
// intentionally leaves ordinary diagnostic text intact.
func ErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return SanitizeErrorMessage(err.Error())
}

func SanitizeErrorMessage(value string) string {
	value = strings.ToValidUTF8(value, "�")
	value = normalizeRemoteURLJSONEscapes(value)
	value = redactSensitiveRemoteURLs(value)
	value = redactScopedInlineImages(value)
	value = redactWrappedBase64(value)
	value = redactLongBase64Runs(value)
	if len(value) <= MaxErrorMessageBytes {
		return value
	}
	const ellipsis = "…"
	value = value[:MaxErrorMessageBytes-len(ellipsis)]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + ellipsis
}

// SanitizeErrorType admits only the compact token grammar used by provider
// error classifications. Error types are untrusted too: copying an echoed URL
// into this field would otherwise bypass message redaction and reach logs or
// Anthropic-compatible response envelopes.
func SanitizeErrorType(value, fallback string) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, ""))
	if value == "" || len(value) > MaxErrorTypeBytes {
		return fallback
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			continue
		}
		return fallback
	}
	return value
}

func normalizeRemoteURLJSONEscapes(value string) string {
	return remoteURLJSONEscape.Replace(value)
}

// redactSensitiveRemoteURLs removes credential-bearing URL components from
// untrusted diagnostics. Providers sometimes echo rejected image URLs, and a
// presigned query, fragment token, or userinfo password must not cross into
// traces, provider health, logs, persistence, or client-visible errors.
func redactSensitiveRemoteURLs(value string) string {
	var redacted strings.Builder
	redacted.Grow(len(value))
	for cursor := 0; cursor < len(value); {
		start, prefixEnd, ok := findRemoteURL(value, cursor)
		if !ok {
			redacted.WriteString(value[cursor:])
			break
		}
		redacted.WriteString(value[cursor:start])
		end := scanRemoteURLCandidate(value, prefixEnd)
		candidate := value[start:end]
		redacted.WriteString(redactRemoteURL(candidate))
		cursor = end
	}
	return redacted.String()
}

// findRemoteURL advances by whole scheme-like runs. Testing every byte as a
// possible scheme start would repeatedly rescan a long ASCII token and make
// untrusted diagnostics quadratic before the public length cap is applied.
func findRemoteURL(value string, start int) (int, int, bool) {
	for i := start; i < len(value); {
		if !isASCIILetter(value[i]) {
			i++
			continue
		}
		schemeStart := i
		i++
		for i < len(value) && isURLSchemeByte(value[i]) {
			i++
		}
		if prefixEnd, ok := remoteURLPrefixEnd(value, i); ok {
			return schemeStart, prefixEnd, true
		}
		// No suffix inside the same contiguous scheme run can succeed: every
		// suffix reaches the same non-scheme byte. Resume after that byte.
		if i < len(value) {
			i++
		}
	}
	return 0, 0, false
}

func remoteURLPrefixEnd(value string, schemeEnd int) (int, bool) {
	switch {
	case strings.HasPrefix(value[schemeEnd:], "://"):
		return schemeEnd + len("://"), true
	case strings.HasPrefix(value[schemeEnd:], `:\/\/`):
		return schemeEnd + len(`:\/\/`), true
	default:
		return 0, false
	}
}

// scanRemoteURLCandidate finds either a trustworthy lexical terminator or the
// start of the next hierarchical URL. Scheme-shaped spans are consumed as
// units, so a long host/path/query token is examined a constant number of
// times rather than once for every possible suffix.
func scanRemoteURLCandidate(value string, start int) int {
	for i := start; i < len(value); {
		if isRemoteURLTerminator(value, i) {
			return i
		}
		if !isASCIILetter(value[i]) {
			i++
			continue
		}
		schemeStart := i
		i++
		for i < len(value) && isURLSchemeByte(value[i]) {
			i++
		}
		if _, ok := remoteURLPrefixEnd(value, i); ok {
			return schemeStart
		}
	}
	return len(value)
}

func isASCIILetter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isURLSchemeByte(value byte) bool {
	return isASCIILetter(value) || value >= '0' && value <= '9' || value == '+' || value == '-' || value == '.'
}

func isRemoteURLTerminator(value string, index int) bool {
	if value[index] <= ' ' {
		return true
	}
	switch value[index] {
	case '"', '\'', '<', '>', '`':
		return true
	case '\\':
		return index+1 >= len(value) || value[index+1] != '/'
	default:
		return false
	}
}

func redactRemoteURL(candidate string) string {
	normalized := strings.ReplaceAll(candidate, `\/`, "/")
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Host == "" ||
		(!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) {
		return redactRemoteURLLexically(normalized)
	}

	var redacted strings.Builder
	redacted.WriteString(parsed.Scheme)
	redacted.WriteString("://")
	if parsed.User != nil {
		redacted.WriteString("[redacted]@")
	}
	redacted.WriteString(parsed.Host)
	redacted.WriteString(parsed.EscapedPath())
	if parsed.RawQuery != "" {
		redacted.WriteString("?[redacted]")
	} else if parsed.ForceQuery {
		redacted.WriteByte('?')
	}
	if parsed.Fragment != "" {
		redacted.WriteString("#[redacted]")
	}
	return redacted.String()
}

// redactRemoteURLLexically is the conservative fallback for a malformed URL
// (for example, one with a bad percent escape). It still strips userinfo and
// everything after query/fragment delimiters instead of returning secrets just
// because net/url rejected the provider's echoed value.
func redactRemoteURLLexically(value string) string {
	schemeEnd := strings.Index(value, "://")
	if schemeEnd < 0 {
		return value
	}
	prefixEnd := schemeEnd + len("://")
	rest := value[prefixEnd:]
	authorityEnd := len(rest)
	if index := strings.IndexAny(rest, "/?#"); index >= 0 {
		authorityEnd = index
	}
	authority := rest[:authorityEnd]
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		authority = "[redacted]@" + authority[at+1:]
	}
	tail := rest[authorityEnd:]
	pathEnd := len(tail)
	if index := strings.IndexAny(tail, "?#"); index >= 0 {
		pathEnd = index
	}

	redacted := value[:prefixEnd] + authority + tail[:pathEnd]
	if strings.Contains(tail[pathEnd:], "?") {
		redacted += "?[redacted]"
	}
	if strings.Contains(tail[pathEnd:], "#") {
		redacted += "#[redacted]"
	}
	return redacted
}

func redactScopedInlineImages(value string) string {
	var redacted strings.Builder
	redacted.Grow(len(value))
	for i := 0; i < len(value); {
		if end, ok := scanInlineImageDataURI(value, i); ok {
			redacted.WriteString("[redacted inline image]")
			i = end
			continue
		}
		if payloadStart, payloadEnd, ok := scanJSONImageData(value, i); ok {
			redacted.WriteString(value[i:payloadStart])
			redacted.WriteString("[redacted inline image]")
			i = payloadEnd
			continue
		}
		redacted.WriteByte(value[i])
		i++
	}
	return redacted.String()
}

func scanInlineImageDataURI(value string, start int) (int, bool) {
	const prefix = "data:image"
	if len(value)-start < len(prefix) || !strings.EqualFold(value[start:start+len(prefix)], prefix) {
		return 0, false
	}
	i := start + len(prefix)
	switch {
	case i < len(value) && value[i] == '/':
		i++
	case i+1 < len(value) && value[i] == '\\' && value[i+1] == '/':
		i += 2
	default:
		return 0, false
	}
	for ; i < len(value) && i-start <= 512; i++ {
		switch value[i] {
		case ',', '\n', '\r', '\t', ' ', '"', '\'', '<', '>':
			if value[i] != ',' {
				return 0, false
			}
			header := value[start:i]
			if !strings.HasSuffix(strings.ToLower(header), ";base64") {
				// Non-base64 image data can contain arbitrary markup, quotes,
				// whitespace, and delimiters. Once detected in untrusted error
				// text, redact the conservative remainder rather than attempting
				// to find a boundary that could expose inline bytes.
				return len(value), true
			}
			end, count, _ := scanWrappedBase64(value, i+1)
			if count == 0 || !validWrappedStandardBase64(value, i+1, end) ||
				!isInlineImageDataURITerminator(value, end) {
				// A malformed payload has no trustworthy lexical boundary. A
				// provider can append echoed operator data after the first bad
				// byte, so fail closed and redact the remaining diagnostic.
				return len(value), true
			}
			return end, true
		}
	}
	// A malformed or overlong data:image header is still sensitive input.
	return len(value), true
}

func validWrappedStandardBase64(value string, start, end int) bool {
	symbols := 0
	padding := 0
	sawPadding := false
	for i := start; i < end; {
		current := value[i]
		if current == '\\' && i+1 < end && value[i+1] == '/' {
			current = '/'
			i += 2
		} else if width := wrappedLineBreakWidth(value, i); width > 0 {
			i += width
			for i < end && (value[i] == ' ' || value[i] == '\t') {
				i++
			}
			continue
		} else {
			i++
		}

		symbols++
		switch {
		case current == '=':
			sawPadding = true
			padding++
			if padding > 2 {
				return false
			}
		case current >= 'a' && current <= 'z' || current >= 'A' && current <= 'Z' ||
			current >= '0' && current <= '9' || current == '+' || current == '/':
			if sawPadding {
				return false
			}
		default:
			return false
		}
	}
	return symbols > 0 && symbols%4 == 0
}

func isInlineImageDataURITerminator(value string, index int) bool {
	if index >= len(value) {
		return true
	}
	switch value[index] {
	case ' ', '\t', '\n', '\r', '"', '\'', ';', '}', ']':
		return true
	default:
		return false
	}
}

func scanJSONImageData(value string, start int) (int, int, bool) {
	const key = `"data"`
	if len(value)-start < len(key) || !strings.EqualFold(value[start:start+len(key)], key) {
		return 0, 0, false
	}
	i := skipASCIIWhitespace(value, start+len(key))
	if i >= len(value) || value[i] != ':' {
		return 0, 0, false
	}
	i = skipASCIIWhitespace(value, i+1)
	if i >= len(value) || value[i] != '"' {
		return 0, 0, false
	}
	payloadStart := i + 1
	payloadEnd, count, _ := scanWrappedBase64(value, payloadStart)
	if count < 32 {
		return 0, 0, false
	}
	return payloadStart, payloadEnd, true
}

func scanWrappedBase64(value string, start int) (int, int, int) {
	i := start
	count := 0
	lineBreaks := 0
	for i < len(value) {
		if isBase64Byte(value[i]) {
			i++
			count++
			continue
		}
		if value[i] == '\\' && i+1 < len(value) && value[i+1] == '/' {
			i += 2
			count++
			continue
		}
		width := wrappedLineBreakWidth(value, i)
		if width == 0 {
			break
		}
		next := i + width
		for next < len(value) && (value[next] == ' ' || value[next] == '\t') {
			next++
		}
		if !isWrappedBase64SymbolStart(value, next) {
			break
		}
		lineBreaks++
		i = next
	}
	return i, count, lineBreaks
}

func redactWrappedBase64(value string) string {
	var redacted strings.Builder
	redacted.Grow(len(value))
	for i := 0; i < len(value); {
		if isWrappedBase64SymbolStart(value, i) {
			end, count, lineBreaks := scanWrappedBase64(value, i)
			if count >= 128 && (lineBreaks > 0 || strings.Contains(value[i:end], `\/`)) {
				redacted.WriteString("[redacted inline payload]")
			} else {
				redacted.WriteString(value[i:end])
			}
			// Consume every scanned lexical run even when it did not need
			// redaction. Restarting inside padding or JSON-escaped slashes can
			// otherwise rescan the same suffix quadratically.
			i = end
			continue
		}
		redacted.WriteByte(value[i])
		i++
	}
	return redacted.String()
}

// redactLongBase64Runs preserves every boundary byte outside the encoded run.
// A regexp that includes both delimiters in its match consumes the separator
// after one payload, preventing an immediately adjacent payload from matching
// in the same non-overlapping replacement pass.
func redactLongBase64Runs(value string) string {
	var redacted strings.Builder
	redacted.Grow(len(value))
	for i := 0; i < len(value); {
		if !isBase64Byte(value[i]) {
			redacted.WriteByte(value[i])
			i++
			continue
		}
		start := i
		for i < len(value) && isBase64Byte(value[i]) {
			i++
		}
		if i-start >= 128 {
			redacted.WriteString("[redacted inline payload]")
		} else {
			redacted.WriteString(value[start:i])
		}
	}
	return redacted.String()
}

func isWrappedBase64SymbolStart(value string, index int) bool {
	return index < len(value) && (isBase64Byte(value[index]) ||
		(value[index] == '\\' && index+1 < len(value) && value[index+1] == '/'))
}

func wrappedLineBreakWidth(value string, start int) int {
	if start >= len(value) {
		return 0
	}
	switch value[start] {
	case '\n':
		return 1
	case '\r':
		if start+1 < len(value) && value[start+1] == '\n' {
			return 2
		}
		return 1
	case '\\':
		if start+1 >= len(value) {
			return 0
		}
		switch value[start+1] {
		case 'n':
			return 2
		case 'r':
			if start+3 < len(value) && value[start+2] == '\\' && value[start+3] == 'n' {
				return 4
			}
			return 2
		}
	}
	return 0
}

func isBase64Byte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' || strings.ContainsRune("+/_=-", rune(value))
}

func skipASCIIWhitespace(value string, start int) int {
	for start < len(value) {
		switch value[start] {
		case ' ', '\t', '\n', '\r':
			start++
		default:
			return start
		}
	}
	return start
}
