// Package sse provides the small, protocol-level primitives shared by Hecate's
// Server-Sent Events readers.
package sse

// FieldValue parses one SSE field line. Per the SSE grammar, a field may omit
// the colon (yielding an empty value), and exactly one optional ASCII space
// immediately after the colon is ignored.
func FieldValue(line, field string) (string, bool) {
	if field == "" {
		return "", false
	}
	if line == field {
		return "", true
	}
	if len(line) <= len(field) || line[:len(field)] != field || line[len(field)] != ':' {
		return "", false
	}
	value := line[len(field)+1:]
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}
	return value, true
}

// DataValue parses an SSE data field line.
func DataValue(line string) (string, bool) {
	return FieldValue(line, "data")
}
