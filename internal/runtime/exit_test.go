package runtime

import "testing"

func TestStopReasonForExitCode(t *testing.T) {
	tests := map[int]string{
		0:   "end_turn",
		2:   "refusal",
		3:   "max_tokens",
		4:   "max_turn_requests",
		5:   "blocked",
		124: "timeout",
		130: "cancelled",
		1:   "",
		42:  "",
	}
	for exitCode, want := range tests {
		if got := StopReasonForExitCode(exitCode); got != want {
			t.Errorf("StopReasonForExitCode(%d) = %q, want %q", exitCode, got, want)
		}
	}
}

func TestExitCodeForStopReason(t *testing.T) {
	tests := map[string]int{
		"end_turn":          0,
		"refusal":           2,
		"max_tokens":        3,
		"max_turn_requests": 4,
		"blocked":           5,
		"cancelled":         130,
		"timeout":           124,
		"":                  1,
		"unknown":           1,
	}

	for stopReason, want := range tests {
		if got := ExitCodeForStopReason(stopReason); got != want {
			t.Errorf("ExitCodeForStopReason(%q) = %d, want %d", stopReason, got, want)
		}
	}
}
