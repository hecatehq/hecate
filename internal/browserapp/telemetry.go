package browserapp

import (
	"context"
	"errors"

	"github.com/hecatehq/hecate/internal/browserrunner"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var browserTracer = otel.Tracer("github.com/hecatehq/hecate/internal/browserapp")

// Every attribute is a closed classification. Never record OS errors, local
// paths, candidate tokens, requested URLs, page text, or browser diagnostics.
func finishSpan(span trace.Span, err error, readiness string) {
	outcome := "success"
	if err != nil {
		switch {
		case errors.Is(err, ErrRemote):
			outcome = "local_only"
		case errors.Is(err, ErrManaged):
			outcome = "environment_managed"
		case errors.Is(err, ErrCandidateChanged):
			outcome = "candidate_changed"
		case errors.Is(err, ErrStoreUnavailable):
			outcome = "settings_unavailable"
		case errors.Is(err, browserrunner.ErrUnavailable):
			outcome = "unavailable"
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			outcome = "cancelled"
		case errors.Is(err, browserrunner.ErrInvalidURL), errors.Is(err, browserrunner.ErrOriginNotAllowed), errors.Is(err, browserrunner.ErrPrivateNetwork):
			outcome = "blocked"
		default:
			outcome = "failed"
		}
		span.SetStatus(codes.Error, outcome)
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.SetAttributes(attribute.String("hecate.browser.outcome", outcome))
	switch readiness {
	case "not_configured", "configured", "working", "unavailable", "local_only":
		span.SetAttributes(attribute.String("hecate.browser.readiness", readiness))
	}
	span.End()
}
