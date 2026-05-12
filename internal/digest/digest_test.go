package digest

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "update golden files")

func TestDigestLine(t *testing.T) {
	long := strings.Repeat("x", 121)
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "agent message content text",
			raw:  `{"event":"agent.message_chunk","session_id":"ses_1","content":{"text":"hello\n\tworld","type":"text"}}`,
			want: "EVENT agent.message_chunk ses_1 hello world",
		},
		{
			name: "agent thought content text",
			raw:  `{"event":"agent.thought_chunk","session_id":"ses_1","content":{"text":"thinking","type":"text"}}`,
			want: "EVENT agent.thought_chunk ses_1 thinking",
		},
		{
			name: "user array content is empty",
			raw:  `{"event":"user.message_chunk","session_id":"ses_1","content":[{"text":"ignore me"}]}`,
			want: "EVENT user.message_chunk ses_1 ",
		},
		{
			name: "tool kind title status",
			raw:  `{"event":"tool.call","session_id":"ses_1","kind":"shell","title":"go test","status":"running"}`,
			want: "EVENT tool.call ses_1 shell:go test [running]",
		},
		{
			name: "tool title only",
			raw:  `{"event":"tool.call_update","session_id":"ses_1","title":"build"}`,
			want: "EVENT tool.call_update ses_1 build",
		},
		{
			name: "permission falls back to tool",
			raw:  `{"event":"permission.request","session_id":"ses_1","question":"","tool":"exec"}`,
			want: "EVENT permission.request ses_1 exec",
		},
		{
			name: "plan falls back to title",
			raw:  `{"event":"session.plan","session_id":"ses_1","label":"","title":"Draft plan"}`,
			want: "EVENT session.plan ses_1 Draft plan",
		},
		{
			name: "session end empty stop reason",
			raw:  `{"event":"session.end","session_id":"ses_1"}`,
			want: "EVENT session.end ses_1 stop_reason=",
		},
		{
			name: "unknown event",
			raw:  `{"event":"session.started","session_id":"ses_1","message":"ignored"}`,
			want: "EVENT session.started ses_1 ",
		},
		{
			name: "sessionID fallback and truncation",
			raw:  `{"event":"agent.message_chunk","sessionID":"ses_2","content":{"text":"` + long + `","type":"text"}}`,
			want: "EVENT agent.message_chunk ses_2 " + strings.Repeat("x", 120),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DigestLine([]byte(tt.raw))
			if err != nil {
				t.Fatalf("DigestLine() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("DigestLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDigestLineMalformed(t *testing.T) {
	if _, err := DigestLine([]byte(`{"event":`)); err == nil {
		t.Fatal("DigestLine() error = nil, want malformed JSON error")
	}
}

func TestStreamGolden(t *testing.T) {
	fixtures := []string{
		"events-vocabulary",
		"happy-path",
		"permission-request",
	}

	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			inputPath := filepath.Join("..", "..", "testdata", "digest", fixture+".ndjson")
			goldenPath := filepath.Join("..", "..", "testdata", "digest", fixture+".golden")

			input, err := os.Open(inputPath)
			if err != nil {
				t.Fatalf("open input: %v", err)
			}
			defer input.Close()

			var out bytes.Buffer
			if err := Stream(input, &out, Options{}); err != nil {
				t.Fatalf("Stream() error = %v", err)
			}

			if *update {
				if err := os.WriteFile(goldenPath, out.Bytes(), 0o644); err != nil {
					t.Fatalf("update golden: %v", err)
				}
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			if got := out.String(); got != string(want) {
				t.Fatalf("Stream() mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
			}
		})
	}
}

func TestStreamJSONFormat(t *testing.T) {
	input := "{\"event\":\"session.plan\"}\n\nnot-json\n{\"event\":\"session.end\"}\n"
	var out bytes.Buffer
	if err := Stream(strings.NewReader(input), &out, Options{Format: "json"}); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	want := "{\"event\":\"session.plan\"}\n{\"event\":\"session.end\"}\n"
	if got := out.String(); got != want {
		t.Fatalf("Stream() = %q, want %q", got, want)
	}
}

// ---- cursor tests ----

// writeCursorFile is a test helper that writes a cursor file with the given
// content (no newline appended — caller controls exact content).
func writeCursorFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeCursorFile: %v", err)
	}
}

// readCursorFile reads the cursor file and returns the trimmed content.
func readCursorFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readCursorFile: %v", err)
	}
	return string(data)
}

// TestCursorMissingStartsAtZero verifies that when no cursor file exists,
// Stream processes from the beginning and writes offset = file size.
func TestCursorMissingStartsAtZero(t *testing.T) {
	dir := t.TempDir()
	cursorPath := filepath.Join(dir, "cursor")

	input := "line one\nline two\n"
	var out bytes.Buffer
	opts := Options{
		CursorPath:        cursorPath,
		CursorStartOffset: 0,
	}
	if err := Stream(strings.NewReader(input), &out, opts); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	// Cursor should now exist and contain the byte count.
	got := readCursorFile(t, cursorPath)
	want := fmt.Sprintf("%d\n", len(input))
	if got != want {
		t.Fatalf("cursor = %q, want %q", got, want)
	}
}

// TestCursorSeekSkipsAlreadyProcessed verifies that when CursorStartOffset is
// set (simulating a post-seek state), Stream counts from that starting offset
// and writes the correct absolute position.
func TestCursorSeekSkipsAlreadyProcessed(t *testing.T) {
	dir := t.TempDir()
	cursorPath := filepath.Join(dir, "cursor")

	// Simulate: caller read 10 bytes already, Stream gets the tail.
	prefix := "0123456789" // 10 bytes already consumed
	tail := "line two\n"
	var out bytes.Buffer
	opts := Options{
		CursorPath:        cursorPath,
		CursorStartOffset: int64(len(prefix)),
	}
	if err := Stream(strings.NewReader(tail), &out, opts); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	want := fmt.Sprintf("%d\n", len(prefix)+len(tail))
	got := readCursorFile(t, cursorPath)
	if got != want {
		t.Fatalf("cursor after seek = %q, want %q", got, want)
	}
}

// TestCursorRewrittenOnFullRun verifies the end-to-end happy path using an
// actual file (so we can test the caller-side seek via the real fixture).
func TestCursorRewrittenOnFullRun(t *testing.T) {
	dir := t.TempDir()
	cursorPath := filepath.Join(dir, "cursor")

	inputPath := filepath.Join("..", "..", "testdata", "digest", "happy-path.ndjson")
	info, err := os.Stat(inputPath)
	if err != nil {
		t.Fatalf("stat happy-path.ndjson: %v", err)
	}
	fileSize := info.Size()

	f, err := os.Open(inputPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	var out bytes.Buffer
	opts := Options{
		CursorPath:        cursorPath,
		CursorStartOffset: 0,
	}
	if err := Stream(f, &out, opts); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	want := fmt.Sprintf("%d\n", fileSize)
	got := readCursorFile(t, cursorPath)
	if got != want {
		t.Fatalf("cursor = %q, want %q (file size=%d)", got, want, fileSize)
	}
}

// TestCursorSecondRunEmitsNothing verifies that re-running with an offset
// equal to the file size produces zero output.
func TestCursorSecondRunEmitsNothing(t *testing.T) {
	// Use a simple in-memory string; simulate "cursor already at end".
	input := "line one\nline two\n"
	var out bytes.Buffer
	opts := Options{
		// CursorPath intentionally blank — we just want to verify that starting
		// at the end produces no output; we test the cursor file separately.
		CursorStartOffset: int64(len(input)),
	}
	// Pass an empty reader (simulating a seek to the very end).
	if err := Stream(strings.NewReader(""), &out, opts); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no output on second run, got: %q", out.String())
	}
}

// TestCursorFollowPeriodicRewrite verifies that in follow mode the cursor is
// rewritten every cursorRewriteInterval events even before EOF.
// We use a file that gets new lines appended mid-stream to simulate follow.
func TestCursorFollowPeriodicRewrite(t *testing.T) {
	dir := t.TempDir()
	cursorPath := filepath.Join(dir, "cursor")

	// Write exactly cursorRewriteInterval lines so that one periodic rewrite
	// fires, then close the file to trigger ErrClosed / EOF shutdown.
	logPath := filepath.Join(dir, "test.ndjson")
	var sb strings.Builder
	for i := 0; i < cursorRewriteInterval; i++ {
		sb.WriteString(fmt.Sprintf("{\"event\":\"session.end\",\"session_id\":\"s%d\"}\n", i))
	}
	content := sb.String()
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	f, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}

	opts := Options{
		Follow:            true,
		PollInterval:      5 * time.Millisecond,
		CursorPath:        cursorPath,
		CursorStartOffset: 0,
	}

	done := make(chan error, 1)
	var out bytes.Buffer
	go func() {
		done <- Stream(f, &out, opts)
	}()

	// Wait for the periodic rewrite: poll until the cursor file exists.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(cursorPath); statErr == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, statErr := os.Stat(cursorPath); statErr != nil {
		f.Close()
		<-done
		t.Fatalf("cursor file not written within deadline: %v", statErr)
	}

	// Graceful shutdown.
	f.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stream() did not return after file close")
	}

	got := readCursorFile(t, cursorPath)
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("cursor file %q missing trailing newline", got)
	}
	// The offset should be positive (we processed content).
	var offset int64
	if _, scanErr := fmt.Sscan(strings.TrimSpace(got), &offset); scanErr != nil {
		t.Fatalf("cursor file content %q is not an integer: %v", got, scanErr)
	}
	if offset <= 0 {
		t.Fatalf("cursor offset = %d, want > 0", offset)
	}
}

// TestCursorFileIsParseable verifies the writeCursor helper produces the
// correct format (decimal + newline, no extras).
func TestCursorFileIsParseable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor")

	if err := writeCursor(path, 12345); err != nil {
		t.Fatalf("writeCursor: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "12345\n" {
		t.Fatalf("cursor content = %q, want %q", got, "12345\n")
	}
}

// TestCursorAtomicWrite verifies that writeCursor does not leave a .tmp file
// behind on success.
func TestCursorAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor")

	if err := writeCursor(path, 99); err != nil {
		t.Fatalf("writeCursor: %v", err)
	}
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf(".tmp file still exists after writeCursor")
	}
}
