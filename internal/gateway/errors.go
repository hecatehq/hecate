package gateway

import (
	"errors"
	"strings"

	"github.com/hecatehq/hecate/internal/ratelimit"
	"github.com/hecatehq/hecate/internal/safetext"
)

var (
	errDenied = errors.New("request denied")
	errClient = errors.New("client error")
)

func IsDeniedError(err error) bool {
	return errors.Is(err, errDenied)
}

func IsClientError(err error) bool {
	return errors.Is(err, errClient)
}

// UserFacingMessage returns err's message stripped of the internal
// "client error" / "request denied" classification prefixes that
// IsClientError / IsDeniedError add by wrapping.
//
// The classifications drive HTTP status routing — they shouldn't leak
// into the body the UI renders. Without this helper, the chat would
// show strings like "client error: api key is required for cloud
// provider anthropic …" where only the part after the colon is
// useful to the operator. The HTTP status (400/403/etc.) already
// conveys which class of error this is.
func UserFacingMessage(err error) string {
	if err == nil {
		return ""
	}
	msg := safetext.ErrorMessage(err)
	for _, prefix := range []string{errClient.Error() + ": ", errDenied.Error() + ": "} {
		if strings.HasPrefix(msg, prefix) {
			return strings.TrimPrefix(msg, prefix)
		}
	}
	return msg
}

// IsRateLimitedError returns true when err is a ratelimit.ExceededError —
// callers should return HTTP 429.
func IsRateLimitedError(err error) bool {
	var target *ratelimit.ExceededError
	return errors.As(err, &target)
}

// AsRateLimitedError extracts the ratelimit.ExceededError from err if present.
func AsRateLimitedError(err error) (*ratelimit.ExceededError, bool) {
	var target *ratelimit.ExceededError
	ok := errors.As(err, &target)
	return target, ok
}
