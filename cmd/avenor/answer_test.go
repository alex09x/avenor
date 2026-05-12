package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeReqFile writes a minimal .req file with the given options to dir/<name>.req
// and returns the perm-base path (without the .req suffix).
func writeReqFile(t *testing.T, dir, name string, optionIDs []string) string {
	t.Helper()
	type reqOption struct {
		OptionID string `json:"optionId"`
		Kind     string `json:"kind"`
	}
	type reqBody struct {
		RequestID string      `json:"request_id"`
		Question  string      `json:"question"`
		Options   []reqOption `json:"options"`
	}
	opts := make([]reqOption, len(optionIDs))
	for i, id := range optionIDs {
		opts[i] = reqOption{OptionID: id, Kind: id}
	}
	body := reqBody{RequestID: "1", Question: "Proceed?", Options: opts}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal req: %v", err)
	}
	permBase := filepath.Join(dir, name)
	if err := os.WriteFile(permBase+".req", data, 0o644); err != nil {
		t.Fatalf("write req: %v", err)
	}
	return permBase
}

// answerRun calls runAnswerTo and captures stderr.
func answerRun(args []string) (code int, stderr string) {
	var buf bytes.Buffer
	code = runAnswerTo(args, &buf)
	return code, buf.String()
}

// ---- table-driven tests ----

func TestRunAnswer(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, dir string) []string // returns args
		wantCode  int
		stderrSub string // non-empty: stderr must contain this
	}{
		{
			name: "missing req file exits 2 with message",
			setup: func(t *testing.T, dir string) []string {
				return []string{"--option", "allow", filepath.Join(dir, "no-such.perm")}
			},
			wantCode:  2,
			stderrSub: "does not exist",
		},
		{
			name: "malformed req JSON exits 2",
			setup: func(t *testing.T, dir string) []string {
				p := filepath.Join(dir, "bad.perm")
				if err := os.WriteFile(p+".req", []byte("{not json}"), 0o644); err != nil {
					t.Fatalf("setup: %v", err)
				}
				return []string{"--option", "allow", p}
			},
			wantCode:  2,
			stderrSub: "parse",
		},
		{
			name: "missing --option exits 2",
			setup: func(t *testing.T, dir string) []string {
				permBase := writeReqFile(t, dir, "test.perm", []string{"allow", "deny"})
				return []string{permBase}
			},
			wantCode:  2,
			stderrSub: "--option",
		},
		{
			name: "unknown option id exits 2 listing valid ids",
			setup: func(t *testing.T, dir string) []string {
				permBase := writeReqFile(t, dir, "test.perm", []string{"allow", "deny"})
				return []string{"--option", "bogus", permBase}
			},
			wantCode:  2,
			stderrSub: "allow",
		},
		{
			name: "valid option exits 0 and writes response",
			setup: func(t *testing.T, dir string) []string {
				permBase := writeReqFile(t, dir, "test.perm", []string{"allow", "deny"})
				return []string{"--option", "allow", permBase}
			},
			wantCode: 0,
		},
		{
			name: "response exists without --force exits 2",
			setup: func(t *testing.T, dir string) []string {
				permBase := writeReqFile(t, dir, "test.perm", []string{"allow"})
				responsePath := permBase + ".req.response"
				if err := os.WriteFile(responsePath, []byte(`{"outcome":"selected","option_id":"allow","message":""}`+"\n"), 0o600); err != nil {
					t.Fatalf("setup: %v", err)
				}
				return []string{"--option", "allow", permBase}
			},
			wantCode:  2,
			stderrSub: "--force",
		},
		{
			name: "response exists with --force exits 0 and overwrites",
			setup: func(t *testing.T, dir string) []string {
				permBase := writeReqFile(t, dir, "test.perm", []string{"allow"})
				responsePath := permBase + ".req.response"
				if err := os.WriteFile(responsePath, []byte("old content\n"), 0o600); err != nil {
					t.Fatalf("setup: %v", err)
				}
				return []string{"--option", "allow", "--force", permBase}
			},
			wantCode: 0,
		},
		{
			name: "cancelled outcome written correctly",
			setup: func(t *testing.T, dir string) []string {
				permBase := writeReqFile(t, dir, "test.perm", []string{"allow"})
				return []string{"--option", "allow", "--outcome", "cancelled", permBase}
			},
			wantCode: 0,
		},
		{
			name: "bogus outcome exits 2",
			setup: func(t *testing.T, dir string) []string {
				permBase := writeReqFile(t, dir, "test.perm", []string{"allow"})
				return []string{"--option", "allow", "--outcome", "bogus", permBase}
			},
			wantCode:  2,
			stderrSub: "outcome",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			args := tt.setup(t, dir)
			code, stderr := answerRun(args)

			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d; stderr: %s", code, tt.wantCode, stderr)
			}
			if tt.stderrSub != "" && !containsStr(stderr, tt.stderrSub) {
				t.Errorf("stderr %q does not contain %q", stderr, tt.stderrSub)
			}
		})
	}
}

// ---- focused tests for response file content ----

func TestRunAnswerResponseContent(t *testing.T) {
	dir := t.TempDir()
	permBase := writeReqFile(t, dir, "test.perm", []string{"allow", "deny"})

	code, stderr := answerRun([]string{"--option", "allow", "--message", "looks good", permBase})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}

	responsePath := permBase + ".req.response"
	data, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	var resp answerResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.Outcome != "selected" {
		t.Errorf("outcome = %q, want %q", resp.Outcome, "selected")
	}
	if resp.OptionID != "allow" {
		t.Errorf("option_id = %q, want %q", resp.OptionID, "allow")
	}
	if resp.Message != "looks good" {
		t.Errorf("message = %q, want %q", resp.Message, "looks good")
	}
}

func TestRunAnswerResponseAllFieldsPresent(t *testing.T) {
	// Even with no --message, all three fields must be present in the JSON.
	dir := t.TempDir()
	permBase := writeReqFile(t, dir, "test.perm", []string{"deny"})

	code, stderr := answerRun([]string{"--option", "deny", permBase})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}

	data, err := os.ReadFile(permBase + ".req.response")
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	for _, key := range []string{"outcome", "option_id", "message"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("response JSON missing field %q; got: %s", key, strings.TrimSpace(string(data)))
		}
	}
}

func TestRunAnswerCancelledOutcome(t *testing.T) {
	dir := t.TempDir()
	permBase := writeReqFile(t, dir, "test.perm", []string{"allow"})

	code, stderr := answerRun([]string{"--option", "allow", "--outcome", "cancelled", permBase})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}

	data, err := os.ReadFile(permBase + ".req.response")
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp answerResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.Outcome != "cancelled" {
		t.Errorf("outcome = %q, want %q", resp.Outcome, "cancelled")
	}
}

func TestRunAnswerForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	permBase := writeReqFile(t, dir, "test.perm", []string{"allow"})
	responsePath := permBase + ".req.response"

	// Write first response.
	code, stderr := answerRun([]string{"--option", "allow", "--message", "first", permBase})
	if code != 0 {
		t.Fatalf("first write exit code = %d, stderr: %s", code, stderr)
	}

	// Overwrite with --force.
	code, stderr = answerRun([]string{"--option", "allow", "--message", "second", "--force", permBase})
	if code != 0 {
		t.Fatalf("force overwrite exit code = %d, stderr: %s", code, stderr)
	}

	data, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp answerResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.Message != "second" {
		t.Errorf("message = %q, want %q (force overwrite did not take effect)", resp.Message, "second")
	}
}

func TestRunAnswerUnknownOptionListsValid(t *testing.T) {
	dir := t.TempDir()
	permBase := writeReqFile(t, dir, "test.perm", []string{"allow", "deny", "ignore"})

	code, stderr := answerRun([]string{"--option", "bogus", permBase})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr: %s", code, stderr)
	}
	// All three valid IDs must appear in the error message.
	for _, id := range []string{"allow", "deny", "ignore"} {
		if !containsStr(stderr, id) {
			t.Errorf("stderr %q does not mention valid option %q", stderr, id)
		}
	}
}
