package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/sdougbrown/avenor/internal/events"
	"github.com/sdougbrown/avenor/internal/permission"
	"github.com/sdougbrown/avenor/internal/runtime"
	"github.com/sdougbrown/avenor/internal/runtime/opencodeacp"
)

const defaultBackend = "opencode-acp"

type ServerDiscovery struct {
	URL    string
	Source string
	Mode   string
}

func DiscoverServer(flagURL string, getenv func(string) string) ServerDiscovery {
	if strings.TrimSpace(flagURL) != "" {
		return ServerDiscovery{URL: flagURL, Source: "flag", Mode: "external"}
	}
	if getenv != nil {
		if envURL := strings.TrimSpace(getenv("AVENOR_OPENCODE_URL")); envURL != "" {
			return ServerDiscovery{URL: envURL, Source: "env", Mode: "external"}
		}
	}
	return ServerDiscovery{Source: "subprocess", Mode: "subprocess"}
}

func OverrideStopReason(stopReason string, cancelled bool, timedOut bool) string {
	switch {
	case timedOut:
		return "timeout"
	case cancelled:
		return "cancelled"
	default:
		return stopReason
	}
}

func Run(args []string) int {
	return run(args, os.Getenv, os.Stderr)
}

func run(args []string, getenv func(string) string, stderr io.Writer) int {
	fs := flag.NewFlagSet("avenor", flag.ContinueOnError)
	fs.SetOutput(stderr)

	agent := fs.String("agent", "", "agent name")
	label := fs.String("label", "", "free-form label for log correlation")
	promptFile := fs.String("prompt-file", "", "path to prompt file")
	dir := fs.String("dir", ".", "working directory for the agent")
	resume := fs.String("resume", "", "resume an existing session id")
	serverURL := fs.String("server-url", "", "long-lived ACP server endpoint")
	onEvent := fs.String("on-event", "", "path to write NDJSON events")
	permissionHandler := fs.String("permission-handler", "", "permission handler, supports file:<path>")
	timeout := fs.Duration("timeout", 0, "overall session timeout")
	model := fs.String("model", "", "backend-specific model id")
	backend := fs.String("backend", defaultBackend, "runtime backend")

	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *backend != defaultBackend {
		fmt.Fprintf(stderr, "avenor: unknown backend %q\n", *backend)
		return 1
	}
	if *promptFile == "" {
		fmt.Fprintln(stderr, "avenor: --prompt-file is required")
		return 1
	}
	if *onEvent == "" {
		fmt.Fprintln(stderr, "avenor: --on-event is required")
		return 1
	}

	prompt, err := os.ReadFile(*promptFile)
	if err != nil {
		fmt.Fprintf(stderr, "avenor: read prompt file: %v\n", err)
		return 1
	}

	writer, err := newEventWriter(*onEvent)
	if err != nil {
		fmt.Fprintf(stderr, "avenor: open event stream: %v\n", err)
		return 1
	}
	defer writer.Close()

	var fileHandler *permission.FileHandler
	if *permissionHandler != "" {
		fileHandler, err = parsePermissionHandler(*permissionHandler)
		if err != nil {
			fmt.Fprintf(stderr, "avenor: %v\n", err)
			return 1
		}
	}

	discovery := DiscoverServer(*serverURL, getenv)
	startOptions := runtime.StartOptions{
		Agent:     *agent,
		Label:     *label,
		Dir:       *dir,
		ServerURL: discovery.URL,
		Model:     *model,
	}
	provider := opencodeacp.NewWithOptions(startOptions)
	if closer, ok := provider.(interface{ Close() error }); ok {
		defer closer.Close()
	}

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()

	session, err := startSession(ctx, provider, startOptions, *resume)
	if err != nil {
		fmt.Fprintf(stderr, "avenor: start session: %v\n", err)
		return 1
	}

	eventCtx, cancelEvents := context.WithCancel(ctx)
	defer cancelEvents()
	eventCh, err := provider.Events(eventCtx, session.SessionID)
	if err != nil {
		fmt.Fprintf(stderr, "avenor: subscribe events: %v\n", err)
		return 1
	}

	promptDone := make(chan error, 1)
	go func() {
		promptDone <- provider.Prompt(ctx, session.SessionID, string(prompt))
	}()

	var timer <-chan time.Time
	if *timeout > 0 {
		t := time.NewTimer(*timeout)
		defer t.Stop()
		timer = t.C
	}

	return waitForSession(ctx, provider, writer, fileHandler, eventCh, promptDone, session.SessionID, timer, stderr)
}

func startSession(ctx context.Context, provider runtime.Provider, opts runtime.StartOptions, resumeID string) (runtime.Session, error) {
	if resumeID != "" {
		return provider.Resume(ctx, resumeID)
	}
	return provider.Start(ctx, opts)
}

func waitForSession(
	ctx context.Context,
	provider runtime.Provider,
	writer *eventWriter,
	fileHandler *permission.FileHandler,
	eventCh <-chan events.Event,
	promptDone <-chan error,
	sessionID string,
	timeout <-chan time.Time,
	stderr io.Writer,
) int {
	var finalStopReason string
	promptReturned := false
	var permissionDone <-chan error

	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				if finalStopReason == "" {
					return 1
				}
				return runtime.ExitCodeForStopReason(finalStopReason)
			}
			if event.Event == "permission.request" && fileHandler != nil {
				if permissionDone != nil {
					fmt.Fprintln(stderr, "avenor: another permission request is already pending")
					return 1
				}
				done := make(chan error, 1)
				permissionDone = done
				go func() {
					done <- fileHandler.Handle(ctx, provider, event, writer.Write)
				}()
				continue
			}
			if event.Event == "session.end" {
				finalStopReason, _ = event.Fields["stop_reason"].(string)
			}
			if err := writer.Write(event); err != nil {
				fmt.Fprintf(stderr, "avenor: write event: %v\n", err)
				return 1
			}
			if event.Event == "session.end" && promptReturned {
				return runtime.ExitCodeForStopReason(finalStopReason)
			}
		case err := <-promptDone:
			promptReturned = true
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return cancelAndEnd(provider, writer, sessionID, "cancelled", stderr)
				}
				fmt.Fprintf(stderr, "avenor: prompt: %v\n", err)
				return 1
			}
			if finalStopReason != "" {
				return runtime.ExitCodeForStopReason(finalStopReason)
			}
		case err := <-permissionDone:
			permissionDone = nil
			if err != nil {
				fmt.Fprintf(stderr, "avenor: permission handler: %v\n", err)
				return 1
			}
		case <-ctx.Done():
			return cancelAndEnd(provider, writer, sessionID, "cancelled", stderr)
		case <-timeout:
			return cancelAndEnd(provider, writer, sessionID, "timeout", stderr)
		}
	}
}

func cancelAndEnd(provider runtime.Provider, writer *eventWriter, sessionID string, stopReason string, stderr io.Writer) int {
	cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := provider.Cancel(cancelCtx, sessionID); err != nil {
		fmt.Fprintf(stderr, "avenor: cancel session: %v\n", err)
	}
	if err := writer.Write(events.Event{
		Event:     "session.end",
		SessionID: sessionID,
		Fields: map[string]any{
			"stop_reason": stopReason,
		},
	}); err != nil {
		fmt.Fprintf(stderr, "avenor: write terminal event: %v\n", err)
		return 1
	}
	return runtime.ExitCodeForStopReason(stopReason)
}

type eventWriter struct {
	mu      sync.Mutex
	file    *os.File
	encoder *json.Encoder
}

func newEventWriter(path string) (*eventWriter, error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &eventWriter{
		file:    file,
		encoder: json.NewEncoder(file),
	}, nil
}

func (w *eventWriter) Write(event events.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.encoder.Encode(event)
}

func (w *eventWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

func parsePermissionHandler(value string) (*permission.FileHandler, error) {
	const prefix = "file:"
	if !strings.HasPrefix(value, prefix) {
		return nil, fmt.Errorf("--permission-handler supports file:<path> only")
	}
	path := strings.TrimPrefix(value, prefix)
	if path == "" {
		return nil, fmt.Errorf("--permission-handler file path is required")
	}
	return permission.NewFileHandler(path), nil
}
