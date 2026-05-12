package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// derivePermBase derives the permission handler base path from a sentinel path.
// Rule: if the path ends in ".done", strip it and append ".perm".
// Otherwise append ".perm" directly.
// This mirrors the derive_permission_base function in dispatch-avenor.sh.
func derivePermBase(sentinelPath string) string {
	if strings.HasSuffix(sentinelPath, ".done") {
		return strings.TrimSuffix(sentinelPath, ".done") + ".perm"
	}
	return sentinelPath + ".perm"
}

// sentinelContent builds the sentinel file content from an exit code plus
// session metadata extracted from the event log. This matches the shim's
// output format exactly.
//
// Exit code semantics:
//
//	0   → DONE\nSESSION=...\nSTOP_REASON=...\n
//	124 → TIMEOUT\nSESSION=...\nSTOP_REASON=...\n
//	130 → KILLED\nSESSION=...\nSTOP_REASON=...\nEXIT_CODE=130\n
//	N   → FAILED\nSESSION=...\nSTOP_REASON=...\nEXIT_CODE=N\n
func sentinelContent(exitCode int, sessionID, stopReason string) string {
	switch exitCode {
	case 0:
		if stopReason == "" {
			stopReason = "end_turn"
		}
		return fmt.Sprintf("DONE\nSESSION=%s\nSTOP_REASON=%s\n", sessionID, stopReason)
	case 124:
		if stopReason == "" {
			stopReason = "timeout"
		}
		return fmt.Sprintf("TIMEOUT\nSESSION=%s\nSTOP_REASON=%s\n", sessionID, stopReason)
	case 130:
		if stopReason == "" {
			stopReason = "cancelled"
		}
		return fmt.Sprintf("KILLED\nSESSION=%s\nSTOP_REASON=%s\nEXIT_CODE=130\n", sessionID, stopReason)
	default:
		if stopReason == "" {
			stopReason = fmt.Sprintf("exit_%d", exitCode)
		}
		return fmt.Sprintf("FAILED\nSESSION=%s\nSTOP_REASON=%s\nEXIT_CODE=%d\n", sessionID, stopReason, exitCode)
	}
}

// sessionEndFields scans an NDJSON event log and returns the session_id and
// stop_reason from the LAST session.end event. If no such event exists, both
// strings are empty.
func sessionEndFields(eventLogPath string) (sessionID, stopReason string) {
	f, err := os.Open(eventLogPath)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Large events (tool output) can exceed the default 64 KiB limit.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var row struct {
			Event     string `json:"event"`
			SessionID string `json:"session_id"`
			// sessionID is the alternate casing seen in some event payloads.
			SessionIDCamel string `json:"sessionID"`
			StopReason     string `json:"stop_reason"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			continue
		}
		if row.Event != "session.end" {
			continue
		}
		sid := row.SessionID
		if sid == "" {
			sid = row.SessionIDCamel
		}
		sessionID = sid
		stopReason = row.StopReason
	}
	return sessionID, stopReason
}

// writeSentinel writes the completion sentinel file at path using an
// atomic tmp+rename strategy. It extracts session metadata from the event log.
// Errors are logged to stderr but never affect the caller's exit code.
func writeSentinel(path string, exitCode int, eventLogPath string, stderr io.Writer) {
	sessionID, stopReason := sessionEndFields(eventLogPath)
	content := sentinelContent(exitCode, sessionID, stopReason)

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".sentinel-*.tmp")
	if err != nil {
		fmt.Fprintf(stderr, "avenor: sentinel: create tmp: %v\n", err)
		return
	}
	tmpPath := tmp.Name()

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		fmt.Fprintf(stderr, "avenor: sentinel: write tmp: %v\n", err)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		fmt.Fprintf(stderr, "avenor: sentinel: close tmp: %v\n", err)
		return
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		fmt.Fprintf(stderr, "avenor: sentinel: rename: %v\n", err)
		return
	}
}

// cleanupSentinelFiles removes pre-existing sentinel and permission request
// files before a run, matching the shim's pre-run cleanup.
// Only called when --sentinel-file is set.
func cleanupSentinelFiles(sentinelPath, permBase string) {
	os.Remove(sentinelPath)
	if permBase != "" {
		os.Remove(permBase + ".req")
		os.Remove(permBase + ".req.response")
	}
}
