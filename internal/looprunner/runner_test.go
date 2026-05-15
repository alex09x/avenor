package looprunner

import (
	"context"
	"fmt"
	"testing"

	"github.com/sdougbrown/avenor/internal/events"
)

func TestRunResumeFromPreviousUsesImmediatePriorPhase(t *testing.T) {
	cfg := &LoopConfig{
		MaxIterations: 2,
		Loop: []Phase{
			{Name: "review", Prompt: "review"},
			{Name: "verify", Prompt: "verify", ResumeFromPrevious: true},
		},
	}

	var verifyPrevSessions []string
	result, err := Run(context.Background(), RunOptions{
		WorkDir:   t.TempDir(),
		RunID:     "run_1",
		EventSink: discardEventWriter{},
		Config:    cfg,
		PhaseAttempt: func(ctx context.Context, phase Phase, attemptNum int, iteration int, prevSessionID string) (PhaseAttemptResult, error) {
			if phase.Name == "verify" {
				verifyPrevSessions = append(verifyPrevSessions, prevSessionID)
			}
			return PhaseAttemptResult{
				ExitCode:  0,
				SessionID: fmt.Sprintf("%s-session-%d", phase.Name, iteration),
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	want := []string{"review-session-1", "review-session-2"}
	if len(verifyPrevSessions) != len(want) {
		t.Fatalf("verify prev sessions = %#v, want %#v", verifyPrevSessions, want)
	}
	for i := range want {
		if verifyPrevSessions[i] != want[i] {
			t.Fatalf("verify prev sessions = %#v, want %#v", verifyPrevSessions, want)
		}
	}
}

type discardEventWriter struct{}

func (discardEventWriter) Write(events.Event) error { return nil }
