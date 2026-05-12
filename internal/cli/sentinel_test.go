package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// derivePermBase
// ---------------------------------------------------------------------------

func TestDerivePermBase(t *testing.T) {
	tests := []struct {
		name     string
		sentinel string
		want     string
	}{
		{
			name:     "done suffix stripped and replaced with perm",
			sentinel: "/tmp/jockey-run.done",
			want:     "/tmp/jockey-run.perm",
		},
		{
			name:     "no done suffix appends perm",
			sentinel: "/tmp/jockey-run",
			want:     "/tmp/jockey-run.perm",
		},
		{
			name:     "path with directory and done suffix",
			sentinel: "/var/run/avenor/session.done",
			want:     "/var/run/avenor/session.perm",
		},
		{
			name:     "path with directory and no done suffix",
			sentinel: "/var/run/avenor/session",
			want:     "/var/run/avenor/session.perm",
		},
		{
			name:     "done in middle of path is not stripped",
			sentinel: "/tmp/done-run/session",
			want:     "/tmp/done-run/session.perm",
		},
		{
			name:     "only the trailing .done is stripped",
			sentinel: "/tmp/foo.done.done",
			want:     "/tmp/foo.done.perm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := derivePermBase(tt.sentinel)
			if got != tt.want {
				t.Fatalf("derivePermBase(%q) = %q, want %q", tt.sentinel, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// sentinelContent
// ---------------------------------------------------------------------------

func TestSentinelContent(t *testing.T) {
	tests := []struct {
		name       string
		exitCode   int
		sessionID  string
		stopReason string
		wantPrefix string
		wantLines  []string
	}{
		{
			name:       "exit 0 clean",
			exitCode:   0,
			sessionID:  "ses_abc123",
			stopReason: "end_turn",
			wantPrefix: "DONE\n",
			wantLines: []string{
				"DONE",
				"SESSION=ses_abc123",
				"STOP_REASON=end_turn",
			},
		},
		{
			name:       "exit 0 default stop reason",
			exitCode:   0,
			sessionID:  "ses_xyz",
			stopReason: "",
			wantLines: []string{
				"DONE",
				"SESSION=ses_xyz",
				"STOP_REASON=end_turn",
			},
		},
		{
			name:       "exit 124 timeout",
			exitCode:   124,
			sessionID:  "ses_t",
			stopReason: "timeout",
			wantLines: []string{
				"TIMEOUT",
				"SESSION=ses_t",
				"STOP_REASON=timeout",
			},
		},
		{
			name:       "exit 124 default stop reason",
			exitCode:   124,
			sessionID:  "ses_t2",
			stopReason: "",
			wantLines: []string{
				"TIMEOUT",
				"SESSION=ses_t2",
				"STOP_REASON=timeout",
			},
		},
		{
			name:       "exit 130 sigint",
			exitCode:   130,
			sessionID:  "ses_k",
			stopReason: "cancelled",
			wantLines: []string{
				"KILLED",
				"SESSION=ses_k",
				"STOP_REASON=cancelled",
				"EXIT_CODE=130",
			},
		},
		{
			name:       "exit 130 default stop reason",
			exitCode:   130,
			sessionID:  "ses_k2",
			stopReason: "",
			wantLines: []string{
				"KILLED",
				"SESSION=ses_k2",
				"STOP_REASON=cancelled",
				"EXIT_CODE=130",
			},
		},
		{
			name:       "exit 1 generic failure",
			exitCode:   1,
			sessionID:  "ses_f",
			stopReason: "some_error",
			wantLines: []string{
				"FAILED",
				"SESSION=ses_f",
				"STOP_REASON=some_error",
				"EXIT_CODE=1",
			},
		},
		{
			name:       "exit 1 default stop reason",
			exitCode:   1,
			sessionID:  "ses_f2",
			stopReason: "",
			wantLines: []string{
				"FAILED",
				"SESSION=ses_f2",
				"STOP_REASON=exit_1",
				"EXIT_CODE=1",
			},
		},
		{
			name:       "exit 2 refusal",
			exitCode:   2,
			sessionID:  "ses_r",
			stopReason: "refusal",
			wantLines: []string{
				"FAILED",
				"SESSION=ses_r",
				"STOP_REASON=refusal",
				"EXIT_CODE=2",
			},
		},
		{
			name:       "empty session id",
			exitCode:   0,
			sessionID:  "",
			stopReason: "end_turn",
			wantLines: []string{
				"DONE",
				"SESSION=",
				"STOP_REASON=end_turn",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sentinelContent(tt.exitCode, tt.sessionID, tt.stopReason)
			for _, line := range tt.wantLines {
				if !strings.Contains(got, line+"\n") {
					t.Errorf("sentinelContent(%d, %q, %q) missing line %q\ngot: %q",
						tt.exitCode, tt.sessionID, tt.stopReason, line, got)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// sessionEndFields
// ---------------------------------------------------------------------------

func TestSessionEndFields(t *testing.T) {
	tests := []struct {
		name           string
		ndjson         string
		wantSessionID  string
		wantStopReason string
	}{
		{
			name:           "single session.end event",
			ndjson:         `{"event":"session.end","session_id":"ses_abc","stop_reason":"end_turn"}` + "\n",
			wantSessionID:  "ses_abc",
			wantStopReason: "end_turn",
		},
		{
			name: "multiple events, last session.end wins",
			ndjson: `{"event":"agent.message_chunk","session_id":"ses_x"}` + "\n" +
				`{"event":"session.end","session_id":"ses_first","stop_reason":"timeout"}` + "\n" +
				`{"event":"session.end","session_id":"ses_last","stop_reason":"end_turn"}` + "\n",
			wantSessionID:  "ses_last",
			wantStopReason: "end_turn",
		},
		{
			name:           "no session.end returns empty strings",
			ndjson:         `{"event":"agent.message_chunk","session_id":"ses_x"}` + "\n",
			wantSessionID:  "",
			wantStopReason: "",
		},
		{
			name:           "empty log returns empty strings",
			ndjson:         "",
			wantSessionID:  "",
			wantStopReason: "",
		},
		{
			name: "malformed JSON lines are skipped",
			ndjson: `not-json` + "\n" +
				`{"event":"session.end","session_id":"ses_good","stop_reason":"end_turn"}` + "\n",
			wantSessionID:  "ses_good",
			wantStopReason: "end_turn",
		},
		{
			name:           "sessionID camelCase fallback",
			ndjson:         `{"event":"session.end","sessionID":"ses_camel","stop_reason":"cancelled"}` + "\n",
			wantSessionID:  "ses_camel",
			wantStopReason: "cancelled",
		},
		{
			name:           "missing file returns empty strings",
			ndjson:         "", // handled separately below
			wantSessionID:  "",
			wantStopReason: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var path string
			if tt.name == "missing file returns empty strings" {
				path = "/tmp/avenor-nonexistent-sentinel-test-file-that-does-not-exist.ndjson"
			} else {
				f, err := os.CreateTemp(t.TempDir(), "events-*.ndjson")
				if err != nil {
					t.Fatalf("create temp: %v", err)
				}
				if _, err := f.WriteString(tt.ndjson); err != nil {
					t.Fatalf("write: %v", err)
				}
				f.Close()
				path = f.Name()
			}

			gotSID, gotSR := sessionEndFields(path)
			if gotSID != tt.wantSessionID {
				t.Errorf("sessionEndFields session_id = %q, want %q", gotSID, tt.wantSessionID)
			}
			if gotSR != tt.wantStopReason {
				t.Errorf("sessionEndFields stop_reason = %q, want %q", gotSR, tt.wantStopReason)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// writeSentinel
// ---------------------------------------------------------------------------

func TestWriteSentinel(t *testing.T) {
	t.Run("writes correct content for exit 0", func(t *testing.T) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "events.ndjson")
		sentPath := filepath.Join(dir, "run.done")

		if err := os.WriteFile(logPath, []byte(`{"event":"session.end","session_id":"ses_write","stop_reason":"end_turn"}`+"\n"), 0o600); err != nil {
			t.Fatalf("write log: %v", err)
		}

		var stderr strings.Builder
		writeSentinel(sentPath, 0, logPath, &stderr)

		if stderr.Len() > 0 {
			t.Errorf("unexpected stderr: %s", stderr.String())
		}

		content, err := os.ReadFile(sentPath)
		if err != nil {
			t.Fatalf("read sentinel: %v", err)
		}
		got := string(content)
		for _, line := range []string{"DONE", "SESSION=ses_write", "STOP_REASON=end_turn"} {
			if !strings.Contains(got, line+"\n") {
				t.Errorf("sentinel missing line %q\ngot: %q", line, got)
			}
		}
	})

	t.Run("atomic rename leaves no tmp files on success", func(t *testing.T) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "events.ndjson")
		sentPath := filepath.Join(dir, "run.done")
		os.WriteFile(logPath, []byte(""), 0o600)

		var stderr strings.Builder
		writeSentinel(sentPath, 0, logPath, &stderr)

		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".tmp") {
				t.Errorf("leftover tmp file: %s", e.Name())
			}
		}
	})
}

// ---------------------------------------------------------------------------
// cleanupSentinelFiles
// ---------------------------------------------------------------------------

func TestCleanupSentinelFiles(t *testing.T) {
	t.Run("removes sentinel and perm req files", func(t *testing.T) {
		dir := t.TempDir()
		sentPath := filepath.Join(dir, "run.done")
		permBase := filepath.Join(dir, "run.perm")

		for _, p := range []string{
			sentPath,
			permBase + ".req",
			permBase + ".req.response",
		} {
			if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
				t.Fatalf("write %s: %v", p, err)
			}
		}

		cleanupSentinelFiles(sentPath, permBase)

		for _, p := range []string{
			sentPath,
			permBase + ".req",
			permBase + ".req.response",
		} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("expected %s to be removed", p)
			}
		}
	})

	t.Run("missing files do not error", func(t *testing.T) {
		dir := t.TempDir()
		// These paths don't exist; cleanupSentinelFiles should not panic.
		cleanupSentinelFiles(filepath.Join(dir, "no-sent.done"), filepath.Join(dir, "no-perm"))
	})

	t.Run("empty permBase skips perm cleanup", func(t *testing.T) {
		dir := t.TempDir()
		sentPath := filepath.Join(dir, "run.done")
		os.WriteFile(sentPath, []byte("old"), 0o600)

		cleanupSentinelFiles(sentPath, "")

		if _, err := os.Stat(sentPath); !os.IsNotExist(err) {
			t.Error("expected sentinel to be removed")
		}
	})
}

// ---------------------------------------------------------------------------
// run — flag integration tests
// ---------------------------------------------------------------------------

func TestRunSentinelFileNotSet(t *testing.T) {
	// Regression: callers without --sentinel-file should see zero sentinel behavior.
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	eventsPath := filepath.Join(dir, "events.ndjson")
	sentPath := filepath.Join(dir, "run.done")

	os.WriteFile(promptPath, []byte("hello"), 0o600)

	var stderr strings.Builder
	// The run will fail (no ACP server) but we only care that no sentinel file
	// is created when --sentinel-file is not passed.
	run([]string{
		"--prompt-file", promptPath,
		"--on-event", eventsPath,
	}, func(string) string { return "" }, &stderr)

	if _, err := os.Stat(sentPath); !os.IsNotExist(err) {
		t.Error("sentinel file written when --sentinel-file was not set")
	}
}

func TestRunExplicitPermissionHandlerWins(t *testing.T) {
	// When both --sentinel-file and --permission-handler are supplied, the
	// explicit --permission-handler must be used as-is (no derivation from
	// sentinel path).
	//
	// We verify the explicit perm base .req/.req.response are cleaned (not the
	// derived base), and that a sentinel is written after run completes.
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	eventsPath := filepath.Join(dir, "events.ndjson")
	sentPath := filepath.Join(dir, "run.done")
	explicitPerm := filepath.Join(dir, "custom.perm")
	derivedPerm := filepath.Join(dir, "run.perm") // what would be derived from sentPath

	os.WriteFile(promptPath, []byte("hello"), 0o600)

	// Create stale files: both the explicit and the derived perm bases, plus sentinel.
	os.WriteFile(sentPath, []byte("stale"), 0o600)
	os.WriteFile(explicitPerm+".req", []byte("stale-explicit"), 0o600)
	os.WriteFile(explicitPerm+".req.response", []byte("stale-explicit"), 0o600)
	os.WriteFile(derivedPerm+".req", []byte("stale-derived"), 0o600)

	var stderr strings.Builder
	run([]string{
		"--prompt-file", promptPath,
		"--on-event", eventsPath,
		"--sentinel-file", sentPath,
		"--permission-handler", "file:" + explicitPerm,
	}, func(string) string { return "" }, &stderr)

	// The explicit perm's req files should have been cleaned.
	for _, p := range []string{explicitPerm + ".req", explicitPerm + ".req.response"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected explicit perm file to be cleaned: %s", p)
		}
	}

	// The derived perm base .req file should NOT have been cleaned (wrong base).
	if _, err := os.Stat(derivedPerm + ".req"); os.IsNotExist(err) {
		t.Errorf("derived perm .req should not have been cleaned when explicit handler was provided")
	}

	// A sentinel file must be written after the run (success or failure).
	if _, err := os.Stat(sentPath); os.IsNotExist(err) {
		t.Error("expected sentinel file to be written after run")
	}
}

func TestRunSentinelWrittenAfterFailedStart(t *testing.T) {
	// --sentinel-file is set; run fails because ACP server is unavailable.
	// Sentinel should still be written (FAILED).
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	eventsPath := filepath.Join(dir, "events.ndjson")
	sentPath := filepath.Join(dir, "run.done")

	os.WriteFile(promptPath, []byte("hello"), 0o600)

	var stderr strings.Builder
	exitCode := run([]string{
		"--prompt-file", promptPath,
		"--on-event", eventsPath,
		"--sentinel-file", sentPath,
	}, func(string) string { return "" }, &stderr)

	if exitCode == 0 {
		t.Skip("unexpected success (opencode may be available); skipping failure-path test")
	}

	content, err := os.ReadFile(sentPath)
	if err != nil {
		t.Fatalf("sentinel not written: %v", err)
	}
	got := string(content)
	if !strings.HasPrefix(got, "FAILED\n") && !strings.HasPrefix(got, "TIMEOUT\n") && !strings.HasPrefix(got, "KILLED\n") {
		t.Errorf("unexpected sentinel content: %q", got)
	}
}

func TestRunPreRunCleanup(t *testing.T) {
	// When --sentinel-file is set, stale sentinel and perm req files must be
	// removed before the run starts (even if it fails immediately after).
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	eventsPath := filepath.Join(dir, "events.ndjson")
	sentPath := filepath.Join(dir, "run.done")
	permBase := filepath.Join(dir, "run.perm")

	os.WriteFile(promptPath, []byte("hello"), 0o600)

	// Create stale files that the shim would clean.
	staleReq := permBase + ".req"
	staleResp := permBase + ".req.response"
	for _, p := range []string{sentPath, staleReq, staleResp} {
		os.WriteFile(p, []byte("stale"), 0o600)
	}

	// We need to observe cleanup BEFORE the run writes a new sentinel. Use a
	// trick: the run itself will write a new sentinel, so we can't check sentPath
	// after. But we CAN check the perm req files – they're cleaned and NOT
	// recreated by the run (the run only creates them when a permission.request
	// event arrives, which won't happen in a failing test).
	var stderr strings.Builder
	run([]string{
		"--prompt-file", promptPath,
		"--on-event", eventsPath,
		"--sentinel-file", sentPath,
	}, func(string) string { return "" }, &stderr)

	for _, p := range []string{staleReq, staleResp} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("stale file not cleaned: %s", p)
		}
	}
}
