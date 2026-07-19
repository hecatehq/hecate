package codeintel

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const maxResultTextBytes = 64 * 1024

func formatResult(result Result) string {
	var builder strings.Builder
	if result.Operation == OpCapabilities {
		for _, capability := range result.Capabilities {
			status := "unavailable"
			if capability.Available {
				status = "available"
			}
			fmt.Fprintf(&builder, "%s: %s via %s [%s] (%s)\n", capability.Language, status, capability.Provider, capability.Status, capability.Detail)
		}
		return truncateResultText(strings.TrimSpace(builder.String()))
	}
	if len(result.Items) == 0 {
		fmt.Fprintf(&builder, "%s returned no results", result.Operation)
	} else {
		fmt.Fprintf(&builder, "%s via %s: %d result(s)\n", result.Operation, result.Provider, len(result.Items))
		for _, item := range result.Items {
			location := item.Path
			if item.StartLine > 0 {
				location = fmt.Sprintf("%s:%d:%d", location, item.StartLine, item.StartColumn)
			}
			label := strings.TrimSpace(strings.Join(nonEmpty(item.Kind, item.Name), " "))
			body := item.Message
			if body == "" {
				body = item.Detail
			}
			if item.Severity != "" && item.Severity != "unknown" {
				label = strings.TrimSpace(item.Severity + " " + label)
			}
			fmt.Fprint(&builder, location)
			if label != "" {
				fmt.Fprintf(&builder, " %s", concise(label, 256))
			}
			if body != "" {
				fmt.Fprintf(&builder, " — %s", concise(body, 2048))
			}
			if item.Preview != "" {
				fmt.Fprintf(&builder, "\n  %s", concise(item.Preview, 512))
			}
			builder.WriteByte('\n')
			if builder.Len() >= maxResultTextBytes {
				break
			}
		}
	}
	if result.OmittedExternal > 0 {
		fmt.Fprintf(&builder, "\nOmitted %d unsafe or external result(s).", result.OmittedExternal)
	}
	if result.Truncated {
		builder.WriteString("\nResults were truncated at the configured limit.")
	}
	return truncateResultText(strings.TrimSpace(builder.String()))
}

func nonEmpty(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, strings.TrimSpace(value))
		}
	}
	return result
}

func truncateResultText(value string) string {
	if len(value) <= maxResultTextBytes {
		return value
	}
	const suffix = "\n... output truncated"
	value = value[:maxResultTextBytes-len(suffix)]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return strings.TrimSpace(value) + suffix
}
