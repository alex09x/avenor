package digest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	defaultFormat       = "plain"
	defaultPollInterval = 250 * time.Millisecond
	maxExcerptRunes     = 120

	// cursorRewriteInterval is how many events are processed between periodic
	// cursor rewrites in follow mode, so a crash loses at most this many events
	// of progress.
	cursorRewriteInterval = 10
)

var lineBreakWhitespace = regexp.MustCompile(`[\r\n\t]+`)

type Options struct {
	Follow       bool
	PollInterval time.Duration
	Format       string

	// CursorPath, when non-empty, causes Stream to atomically rewrite the
	// cursor file after processing. The caller is responsible for seeking the
	// underlying reader to the offset recorded in the cursor file before
	// calling Stream; CursorStartOffset must be set to that same offset so
	// Stream can compute the absolute file position when writing the cursor.
	//
	// Design note: we keep Stream's signature as io.Reader (rather than
	// *os.File) so tests can pass bytes.Buffer or strings.Reader without
	// touching the filesystem. The caller in watch.go handles the actual
	// os.File seek; we track bytes consumed here via a counting wrapper.
	CursorPath        string
	CursorStartOffset int64
}

// countingReader wraps an io.Reader and counts total bytes read.
type countingReader struct {
	r     io.Reader
	total int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.total += int64(n)
	return n, err
}

// writeCursor atomically rewrites the cursor file at path with offset.
// It writes to path+".tmp" then renames into place.
func writeCursor(path string, offset int64) error {
	tmp := path + ".tmp"
	content := fmt.Sprintf("%d\n", offset)
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		// Clean up the temp file if it was partially written.
		_ = os.Remove(tmp)
		return fmt.Errorf("write cursor tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename cursor %s -> %s: %w", tmp, path, err)
	}
	return nil
}

func DigestLine(raw []byte) (string, error) {
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &event); err != nil {
		return "", err
	}

	name := stringField(event, "event")
	sessionID := stringField(event, "session_id")
	if sessionID == "" {
		sessionID = stringField(event, "sessionID")
	}

	return fmt.Sprintf("EVENT %s %s %s", name, sessionID, clean(excerpt(event, name))), nil
}

func Stream(in io.Reader, out io.Writer, opts Options) error {
	format := opts.Format
	if format == "" {
		format = defaultFormat
	}
	pollInterval := opts.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}

	// Wrap the reader with a byte counter so we can track our position in the
	// file and update the cursor without needing a seekable *os.File here.
	cr := &countingReader{r: in}
	reader := bufio.NewReader(cr)

	// currentOffset returns the absolute file position (start offset + bytes
	// consumed so far, minus any buffered-but-not-yet-processed bytes in the
	// bufio layer). bufio.Reader may have read ahead; subtract the buffered
	// amount to get the position of the last fully-delivered byte.
	currentOffset := func() int64 {
		buffered := int64(reader.Buffered())
		return opts.CursorStartOffset + cr.total - buffered
	}

	writeCurrentCursor := func() error {
		if opts.CursorPath == "" {
			return nil
		}
		return writeCursor(opts.CursorPath, currentOffset())
	}

	lineNumber := 0
	eventsSinceCursorWrite := 0
	for {
		raw, err := reader.ReadBytes('\n')
		if len(raw) > 0 {
			lineNumber++
			if streamErr := streamLine(raw, lineNumber, out, format); streamErr != nil {
				return streamErr
			}
			eventsSinceCursorWrite++
			// In follow mode, rewrite the cursor periodically so a crash only
			// loses at most cursorRewriteInterval events of progress.
			if opts.Follow && opts.CursorPath != "" && eventsSinceCursorWrite >= cursorRewriteInterval {
				if cursorErr := writeCurrentCursor(); cursorErr != nil {
					return cursorErr
				}
				eventsSinceCursorWrite = 0
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			if opts.Follow {
				time.Sleep(pollInterval)
				continue
			}
			// Oneshot mode: write cursor on clean EOF.
			return writeCurrentCursor()
		}
		if errors.Is(err, os.ErrClosed) {
			// Graceful shutdown (file closed by signal handler): write cursor.
			return writeCurrentCursor()
		}
		return err
	}
}

func streamLine(raw []byte, lineNumber int, out io.Writer, format string) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if format == "json" {
		if err := validateJSON(raw); err != nil {
			logMalformed(lineNumber, err)
			return nil
		}
		_, err := out.Write(raw)
		return err
	}

	line, err := DigestLine(raw)
	if err != nil {
		logMalformed(lineNumber, err)
		return nil
	}
	_, err = fmt.Fprintln(out, line)
	return err
}

func validateJSON(raw []byte) error {
	var value any
	return json.Unmarshal(bytes.TrimSpace(raw), &value)
}

func logMalformed(lineNumber int, err error) {
	fmt.Fprintf(os.Stderr, "avenor watch: malformed event at line %d: %v\n", lineNumber, err)
}

func excerpt(event map[string]any, name string) string {
	switch name {
	case "agent.message_chunk", "agent.thought_chunk", "user.message_chunk":
		return contentText(event["content"])
	case "tool.call", "tool.call_update":
		return toolSummary(event)
	case "permission.request":
		if question := stringField(event, "question"); question != "" {
			return question
		}
		return stringField(event, "tool")
	case "session.plan":
		if label := stringField(event, "label"); label != "" {
			return label
		}
		return stringField(event, "title")
	case "session.end":
		return "stop_reason=" + stringField(event, "stop_reason")
	default:
		return ""
	}
}

func contentText(value any) string {
	content, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	return stringValue(content["text"])
}

func toolSummary(event map[string]any) string {
	kind := stringField(event, "kind")
	title := stringField(event, "title")
	status := stringField(event, "status")

	base := ""
	switch {
	case kind != "" && title != "":
		base = kind + ":" + title
	case kind != "":
		base = kind
	case title != "":
		base = title
	}
	if status != "" {
		return base + " [" + status + "]"
	}
	return base
}

func stringField(event map[string]any, key string) string {
	return stringValue(event[key])
}

func stringValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func clean(text string) string {
	text = lineBreakWhitespace.ReplaceAllString(text, " ")
	runes := []rune(text)
	if len(runes) > maxExcerptRunes {
		text = string(runes[:maxExcerptRunes])
	}
	return strings.TrimRightFunc(text, unicode.IsSpace)
}
