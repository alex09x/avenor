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
)

var lineBreakWhitespace = regexp.MustCompile(`[\r\n\t]+`)

type Options struct {
	Follow       bool
	PollInterval time.Duration
	Format       string
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

	reader := bufio.NewReader(in)
	lineNumber := 0
	for {
		raw, err := reader.ReadBytes('\n')
		if len(raw) > 0 {
			lineNumber++
			if err := streamLine(raw, lineNumber, out, format); err != nil {
				return err
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
			return nil
		}
		if errors.Is(err, os.ErrClosed) {
			return nil
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
