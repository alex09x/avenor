//go:build integration

package codexappserver

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/sdougbrown/avenor/internal/runtime"
)

func codexAvailable() bool {
	if _, err := exec.LookPath("codex"); err != nil {
		return false
	}
	return os.Getenv("CODEX_API_KEY") != ""
}

// TestLiveCodexRoundtrip verifies a real Start → Prompt → Events → session.end
// cycle against a live codex app-server process. Requires codex on PATH and
// CODEX_API_KEY in the environment.
//
// To run:
//
//	CODEX_API_KEY=sk-... go test -tags=integration -run TestLiveCodexRoundtrip -v -timeout 5m
func TestLiveCodexRoundtrip(t *testing.T) {
	if !codexAvailable() {
		t.Skip("codex not on PATH or CODEX_API_KEY not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	p := NewWithOptions(runtime.StartOptions{})

	sess, err := p.Start(ctx, runtime.StartOptions{Dir: "."})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Logf("started session %s", sess.SessionID)

	eventsCh, err := p.Events(ctx, sess.SessionID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	err = p.Prompt(ctx, sess.SessionID, "Say hello world and nothing else.")
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	// Wait for session.end.
	terminal := false
	for ev := range eventsCh {
		t.Logf("event: %s (session=%s)", ev.Event, ev.SessionID)
		if ev.Event == "session.end" {
			terminal = true
			break
		}
	}
	if !terminal {
		t.Fatal("did not receive session.end before events channel closed")
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
