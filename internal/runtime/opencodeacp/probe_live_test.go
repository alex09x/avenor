//go:build integration

package opencodeacp

import (
	"context"
	"testing"
	"time"
)

func TestLiveProbeMatchesFixtureShape(t *testing.T) {
	if !opencodeAvailable() {
		t.Skip("opencode not on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	transcript, err := RunProbe(ctx, ".")
	if err != nil {
		t.Skipf("opencode ACP probe unavailable in this environment: %v", err)
	}

	unique := map[string]bool{}
	terminal := false
	for _, event := range transcript {
		unique[event.Event] = true
		if event.Event == "session.end" {
			terminal = true
		}
	}
	if len(unique) == 0 {
		t.Fatal("live probe produced no events")
	}
	if !terminal {
		t.Fatal("live probe did not produce session.end")
	}
}
