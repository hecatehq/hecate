package sse

import "testing"

func TestFieldValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		line  string
		field string
		want  string
		ok    bool
	}{
		{name: "optional space", line: "data: payload", field: "data", want: "payload", ok: true},
		{name: "no space", line: "data:payload", field: "data", want: "payload", ok: true},
		{name: "only one space ignored", line: "data:  payload", field: "data", want: " payload", ok: true},
		{name: "colon omitted", line: "data", field: "data", want: "", ok: true},
		{name: "event field", line: "event:message_stop", field: "event", want: "message_stop", ok: true},
		{name: "wrong field", line: "event: message_stop", field: "data", ok: false},
		{name: "field prefix", line: "database: payload", field: "data", ok: false},
		{name: "empty field", line: ": payload", field: "", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := FieldValue(test.line, test.field)
			if ok != test.ok || got != test.want {
				t.Fatalf("FieldValue(%q, %q) = %q, %v; want %q, %v", test.line, test.field, got, ok, test.want, test.ok)
			}
		})
	}
}
