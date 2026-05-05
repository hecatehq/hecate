package agentadapters

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// newApprovalID returns a stable id for an approval row. Format:
// "appr_" + 24 hex chars (96 bits of randomness). Not a strict ULID
// for now — switching to ULID is a follow-up if/when we want lexical
// ordering at the storage layer.
func newApprovalID() string {
	return prefixedID("appr_")
}

func newGrantID() string {
	return prefixedID("grnt_")
}

func prefixedID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read returning an error is exceptional in modern Go;
		// fall back to a time-based id so the system stays alive.
		return fmt.Sprintf("%s%x", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(b[:])
}
