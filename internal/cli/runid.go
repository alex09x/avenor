package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"
)

// generateRunID returns a random 16-character hex string (8 bytes of entropy).
// If crypto/rand is unavailable (e.g. fd exhaustion), it falls back to a
// time+pid-derived ID prefixed with "auto-" so consumers can distinguish it
// from the normal random form. The fallback is logged to stderr; the function
// never panics.
func generateRunID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		fmt.Fprintf(os.Stderr, "avenor: crypto/rand unavailable, using time-based run id: %v\n", err)
		// Combine UnixNano and PID for reasonable uniqueness without crypto.
		v := uint64(time.Now().UnixNano()) ^ (uint64(os.Getpid()) << 16)
		return fmt.Sprintf("auto-%016x", v)
	}
	return hex.EncodeToString(b[:])
}
