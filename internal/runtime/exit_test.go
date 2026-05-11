package runtime

import "testing"

func TestExitCodeForStopReason(t *testing.T) {
	tests := map[string]int{
		"end_turn":          0,
		"refusal":           2,
		"max_tokens":        3,
		"max_turn_requests": 4,
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
