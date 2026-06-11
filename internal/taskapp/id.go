package taskapp

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

func newOpaqueTaskResourceID(prefix string) string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return prefix + "_" + time.Now().UTC().Format("20060102150405.000000000")
	}
	return prefix + "_" + hex.EncodeToString(buf)
}
