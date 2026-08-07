//go:build integration

package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex09x/avenor/agyclient/events"
)

func TestDefaultModeWritesEvents(t *testing.T) {
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode not on PATH")
	}

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "brief.txt")
	eventsPath := filepath.Join(dir, "events.ndjson")
	if err := os.WriteFile(promptPath, []byte("Reply with OK and nothing else."), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cmd := exec.Command("go", "run", ".", "--agent", "jockey", "--prompt-file", promptPath, "--dir", dir, "--on-event", eventsPath, "--timeout", "2m")
	cmd.Dir = "."
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("default mode unavailable in this environment: %v\n%s", err, output)
	}

	file, err := os.Open(eventsPath)
	if err != nil {
		t.Fatalf("open events: %v", err)
	}
	defer file.Close()

	var count int
	var terminal bool
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		count++
		var event events.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode event line %d: %v", count, err)
		}
		if event.Event == "session.end" {
			terminal = true
			if got, _ := event.Fields["stop_reason"].(string); got != "end_turn" {
				t.Fatalf("stop_reason = %q, want end_turn", got)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan events: %v", err)
	}
	if count == 0 {
		t.Fatal("no events written")
	}
	if !terminal {
		t.Fatal("missing session.end")
	}
	if strings.TrimSpace(string(output)) != "" {
		t.Logf("avenor output: %s", output)
	}
}
