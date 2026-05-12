package digest

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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

// readCursorFile reads the cursor file and returns the raw content (including
// any trailing newline). Use parseCursorOffset when you need the int64 value.
func readCursorFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readCursorFile: %v", err)
	}
	return string(data)
}

// parseCursorOffset parses the cursor file at path the same way production
// code does (strconv.ParseInt after trimming the trailing newline) and returns
// the int64 value. Tests should assert on the integer, not the raw string, so
// that a format change only needs to be fixed here and in TestCursorFileIsParseable.
func parseCursorOffset(t *testing.T, path string) int64 {
	t.Helper()
	raw := readCursorFile(t, path)
	s := strings.TrimRight(raw, "\n")
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("parseCursorOffset(%q): %v", raw, err)
	}
	return n
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
	gotOffset := parseCursorOffset(t, cursorPath)
	if gotOffset != int64(len(input)) {
		t.Fatalf("cursor offset = %d, want %d", gotOffset, len(input))
	}
}

// TestCursorSeekAdjustsAbsoluteOffset verifies that when CursorStartOffset is
// set (simulating a post-seek state), Stream adds it to bytes consumed and
// writes the correct absolute file position — i.e. the caller is responsible
// for the seek, but Stream accounts for the pre-seek bytes in the cursor.
func TestCursorSeekAdjustsAbsoluteOffset(t *testing.T) {
	dir := t.TempDir()
	cursorPath := filepath.Join(dir, "cursor")

	// Simulate: caller read 10 bytes already, Stream gets the tail.
	prefix := "0123456789" // 10 bytes already consumed by caller seek
	tail := "line two\n"
	var out bytes.Buffer
	opts := Options{
		CursorPath:        cursorPath,
		CursorStartOffset: int64(len(prefix)),
	}
	if err := Stream(strings.NewReader(tail), &out, opts); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	wantOffset := int64(len(prefix) + len(tail))
	gotOffset := parseCursorOffset(t, cursorPath)
	if gotOffset != wantOffset {
		t.Fatalf("cursor after seek = %d, want %d", gotOffset, wantOffset)
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

	gotOffset := parseCursorOffset(t, cursorPath)
	if gotOffset != fileSize {
		t.Fatalf("cursor offset = %d, want %d (file size)", gotOffset, fileSize)
	}
}

// TestCursorSecondRunEmitsNothing exercises the cursor codepath end-to-end:
// run Stream once to populate the cursor file, then open the same input,
// seek to the saved offset, and run Stream again — it should produce no output
// and leave the cursor file pointing at the same (end-of-file) offset.
func TestCursorSecondRunEmitsNothing(t *testing.T) {
	dir := t.TempDir()
	cursorPath := filepath.Join(dir, "cursor")

	// Build a small log file with known content.
	input := "line one\nline two\n"
	logPath := filepath.Join(dir, "test.ndjson")
	if err := os.WriteFile(logPath, []byte(input), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	// First run: process entire file, cursor written at EOF offset.
	f1, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("open first run: %v", err)
	}
	defer f1.Close()
	var out1 bytes.Buffer
	if err := Stream(f1, &out1, Options{CursorPath: cursorPath}); err != nil {
		t.Fatalf("Stream() first run error = %v", err)
	}
	savedOffset := parseCursorOffset(t, cursorPath)
	if savedOffset != int64(len(input)) {
		t.Fatalf("after first run cursor = %d, want %d", savedOffset, len(input))
	}

	// Second run: open same file, seek to saved offset, Stream should see EOF
	// immediately and produce no output.
	f2, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("open second run: %v", err)
	}
	defer f2.Close()
	if _, err := f2.Seek(savedOffset, io.SeekStart); err != nil {
		t.Fatalf("seek: %v", err)
	}
	var out2 bytes.Buffer
	if err := Stream(f2, &out2, Options{
		CursorPath:        cursorPath,
		CursorStartOffset: savedOffset,
	}); err != nil {
		t.Fatalf("Stream() second run error = %v", err)
	}
	if out2.Len() != 0 {
		t.Fatalf("expected no output on second run, got: %q", out2.String())
	}
	// Cursor file must still point at EOF.
	finalOffset := parseCursorOffset(t, cursorPath)
	if finalOffset != int64(len(input)) {
		t.Fatalf("cursor after second run = %d, want %d", finalOffset, len(input))
	}
}

// TestCursorFollowPeriodicRewrite verifies that in follow mode the cursor is
// rewritten after cursorRewriteInterval events even while Stream is still
// running — i.e. the periodic path fires BEFORE the shutdown (ErrClosed) path
// gets a chance to write the cursor.
//
// Strategy: write a file with exactly cursorRewriteInterval lines. Open it and
// start Stream in follow mode. Stream will drain the file, then block in its
// polling loop (no new data, no EOF yet from our perspective because we have
// not closed the file). At that point only the periodic-rewrite path could
// have written the cursor. We poll until the cursor appears, assert the offset
// BEFORE closing the file, then close and confirm Stream exits cleanly.
func TestCursorFollowPeriodicRewrite(t *testing.T) {
	dir := t.TempDir()
	cursorPath := filepath.Join(dir, "cursor")

	// Build exactly cursorRewriteInterval lines so exactly one periodic rewrite fires.
	var sb strings.Builder
	for i := 0; i < cursorRewriteInterval; i++ {
		sb.WriteString(fmt.Sprintf("{\"event\":\"session.end\",\"session_id\":\"s%d\"}\n", i))
	}
	content := sb.String()
	contentSize := int64(len(content))

	logPath := filepath.Join(dir, "test.ndjson")
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

	// Poll for the cursor file. Stream is in follow mode, polling at
	// PollInterval after EOF — file is still open, so no ErrClosed. The only
	// code that writes the cursor before the file is closed is the periodic
	// path (digest.go lines 136-141).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(cursorPath); statErr == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, statErr := os.Stat(cursorPath); statErr != nil {
		f.Close()
		<-done
		t.Fatalf("cursor file not written by periodic path within deadline")
	}

	// Key assertion: verify the offset WHILE Stream is still live (file not
	// yet closed). This is the periodic write — shutdown has not happened.
	periodicOffset := parseCursorOffset(t, cursorPath)
	if periodicOffset != contentSize {
		t.Fatalf("periodic cursor offset = %d, want %d (content size)", periodicOffset, contentSize)
	}

	// Close the file to trigger ErrClosed shutdown path in Stream.
	f.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stream() returned error after file close: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stream() did not return after file close")
	}

	// After shutdown the cursor must be at or beyond the periodic offset.
	finalOffset := parseCursorOffset(t, cursorPath)
	if finalOffset < periodicOffset {
		t.Fatalf("final cursor offset %d < periodic offset %d", finalOffset, periodicOffset)
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

// TestCursorWriteCleansUpTempFileOnSuccess verifies that writeCursor does not
// leave a .tmp file behind after a successful write.
func TestCursorWriteCleansUpTempFileOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor")

	if err := writeCursor(path, 99); err != nil {
		t.Fatalf("writeCursor: %v", err)
	}
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf(".tmp file still exists after writeCursor success")
	}
}

// TestCursorWriteRenameFailure verifies that when os.Rename fails (simulated
// by placing a directory at the target path so rename is rejected), writeCursor
// returns an error, does NOT overwrite the original target, and cleans up the
// .tmp file.
func TestCursorWriteRenameFailure(t *testing.T) {
	dir := t.TempDir()
	// Place a directory at the cursor path so os.Rename into it will fail.
	targetPath := filepath.Join(dir, "cursor")
	if err := os.Mkdir(targetPath, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	err := writeCursor(targetPath, 42)
	if err == nil {
		t.Fatal("writeCursor: expected error when target is a directory, got nil")
	}

	// The .tmp file must be cleaned up even on failure.
	tmpPath := targetPath + ".tmp"
	if _, statErr := os.Stat(tmpPath); !os.IsNotExist(statErr) {
		t.Fatalf(".tmp file still exists after rename failure")
	}

	// The original directory (target) must be untouched.
	info, statErr := os.Stat(targetPath)
	if statErr != nil {
		t.Fatalf("target path gone after failure: %v", statErr)
	}
	if !info.IsDir() {
		t.Fatalf("target path is no longer a directory after failure")
	}
}
