package cli

import (
	"crypto/rand"
	"encoding/hex"
)

// generateRunID returns a random 16-character hex string (8 bytes of entropy).
func generateRunID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
