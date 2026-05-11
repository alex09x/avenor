package runtime

// ExitCodeForStopReason returns Avenor's locked process exit code for a
// terminal ACP stop reason.
func ExitCodeForStopReason(stopReason string) int {
	switch stopReason {
	case "end_turn":
		return 0
	case "refusal":
		return 2
	case "max_tokens":
		return 3
	case "max_turn_requests":
		return 4
	case "cancelled":
		return 130
	case "timeout":
		return 124
	default:
		return 1
	}
}
