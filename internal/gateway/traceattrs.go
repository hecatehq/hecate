package gateway

import (
	"errors"
	"reflect"

	"github.com/hecate/agent-runtime/internal/policy"
	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/telemetry"
)

// errorKind* aliases map gateway-local names to the authoritative exported
// constants in the telemetry package. All recording sites in this package use
// these aliases so that refactoring the constants requires one change here.
const (
	errorKindInvalidRequest     = telemetry.ErrorKindInvalidRequest
	errorKindRequestDenied      = telemetry.ErrorKindRequestDenied
	errorKindRouterFailed       = telemetry.ErrorKindRouterFailed
	errorKindRouteDenied        = telemetry.ErrorKindRouteDenied
	errorKindProviderCallFailed = telemetry.ErrorKindProviderCallFailed
	errorKindRetryBackoffFailed = telemetry.ErrorKindRetryBackoff
	errorKindProviderHealth     = telemetry.ErrorKindProviderHealth
	errorKindUsageRecordFailed  = telemetry.ErrorKindUsageRecord
)

func tracePhaseAttrs(phase string, attrs map[string]any) map[string]any {
	out := cloneTraceAttrs(attrs)
	if phase != "" {
		out[telemetry.AttrHecatePhase] = phase
	}
	return out
}

func traceErrorAttrs(phase, kind string, err error, attrs map[string]any) map[string]any {
	out := tracePhaseAttrs(phase, attrs)
	if kind != "" {
		out[telemetry.AttrHecateErrorKind] = kind
		out[telemetry.AttrErrorType] = kind
	}
	if err != nil {
		out[telemetry.AttrErrorMessage] = err.Error()
		if _, ok := out[telemetry.AttrErrorType]; !ok {
			out[telemetry.AttrErrorType] = traceErrorType(err)
		}
		var policyErr *policy.Error
		if errors.As(err, &policyErr) && policyErr != nil {
			if policyErr.Evaluation.RuleID != "" {
				out[telemetry.AttrHecatePolicyRuleID] = policyErr.Evaluation.RuleID
			}
			if policyErr.Evaluation.Action != "" {
				out[telemetry.AttrHecatePolicyAction] = policyErr.Evaluation.Action
			}
			if policyErr.Evaluation.Reason != "" {
				out[telemetry.AttrHecatePolicyReason] = policyErr.Evaluation.Reason
			}
		}
	}
	return out
}

func recordTrace(trace *profiler.Trace, name, phase string, attrs map[string]any) {
	trace.Record(name, tracePhaseAttrs(phase, attrs))
}

func recordTraceError(trace *profiler.Trace, name, phase, kind string, err error, attrs map[string]any) {
	trace.Record(name, traceErrorAttrs(phase, kind, err, attrs))
}

func cloneTraceAttrs(attrs map[string]any) map[string]any {
	if len(attrs) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(attrs)+3)
	for key, value := range attrs {
		out[key] = value
	}
	return out
}

func traceErrorType(err error) string {
	if err == nil {
		return ""
	}
	t := reflect.TypeOf(err)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Name() != "" {
		return t.Name()
	}
	return t.String()
}
