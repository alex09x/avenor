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

	"github.com/sdougbrown/avenor/internal/digest"
	"github.com/sdougbrown/avenor/internal/events"
	"github.com/sdougbrown/avenor/internal/permission"
	"github.com/sdougbrown/avenor/internal/runtime"
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
	sentinelFile := fs.String("sentinel-file", "", "path to write a completion sentinel (also derives permission base unless --permission-handler is set)")
	timeout := fs.Duration("timeout", 0, "overall session timeout")
	model := fs.String("model", "", "backend-specific model id")
	backend := fs.String("backend", defaultBackend, "runtime backend")
	runIDFlag := fs.String("run-id", "", "correlation id for this run (generated if not set)")
	maxRetries := fs.Int("max-retries", 0, "maximum retry attempts on transient failure (0 = no retry)")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	runID := *runIDFlag
	if runID == "" {
		runID = generateRunID()
	}

	// Helper to write sentinel and return the provided exit code. Used at
	// every return path when --sentinel-file is active. Defined immediately
	// after fs.Parse so it is available for all post-parse return paths.
	// When *onEvent is empty (flag-validation failures), sessionEndFields
	// will open-fail silently and produce empty strings — the sentinel will
	// still be written with FAILED/exit_1 so stableboy gets a signal.
	exitWithSentinel := func(code int) int {
		if *sentinelFile != "" {
			writeSentinel(*sentinelFile, code, *onEvent, runID, stderr)
		}
		return code
	}

	if *backend != defaultBackend {
		fmt.Fprintf(stderr, "avenor: unknown backend %q\n", *backend)
		return exitWithSentinel(1)
	}
	if *promptFile == "" {
		fmt.Fprintln(stderr, "avenor: --prompt-file is required")
		return exitWithSentinel(1)
	}
	if *onEvent == "" {
		fmt.Fprintln(stderr, "avenor: --on-event is required")
		return exitWithSentinel(1)
	}

	// Derive permission handler base from sentinel when --permission-handler is
	// not set and --sentinel-file is set. The caller's explicit --permission-handler
	// always wins.
	var effectivePermHandler string
	if *sentinelFile != "" && *permissionHandler == "" {
		effectivePermHandler = "file:" + derivePermBase(*sentinelFile)
	} else {
		effectivePermHandler = *permissionHandler
	}

	// Pre-run cleanup: truncate event log and remove stale sentinel/perm files.
	// Only when --sentinel-file is active to avoid changing behavior for callers
	// that don't use it. The event log is recreated below via newEventWriter.
	// Use the effective permission base (derived or explicit) for cleanup.
	if *sentinelFile != "" {
		var cleanupPermBase string
		const filePrefix = "file:"
		if strings.HasPrefix(effectivePermHandler, filePrefix) {
			cleanupPermBase = strings.TrimPrefix(effectivePermHandler, filePrefix)
		}
		cleanupSentinelFiles(*sentinelFile, cleanupPermBase, stderr)
	}

	prompt, err := os.ReadFile(*promptFile)
	if err != nil {
		fmt.Fprintf(stderr, "avenor: read prompt file: %v\n", err)
		return exitWithSentinel(1)
	}

	writer, err := newEventWriter(*onEvent)
	if err != nil {
		fmt.Fprintf(stderr, "avenor: open event stream: %v\n", err)
		return exitWithSentinel(1)
	}
	defer writer.Close()

	var fileHandler *permission.FileHandler
	if effectivePermHandler != "" {
		fileHandler, err = parsePermissionHandler(effectivePermHandler)
		if err != nil {
			fmt.Fprintf(stderr, "avenor: %v\n", err)
			return exitWithSentinel(1)
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

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()

	var timer <-chan time.Time
	if *timeout > 0 {
		t := time.NewTimer(*timeout)
		defer t.Stop()
		timer = t.C
	}

	resumeID := *resume
	var result attemptResult
	for attempt := 1; ; attempt++ {
		result = runSingleAttempt(ctx, startOptions, resumeID, writer, fileHandler, string(prompt), runID, timer, stderr)

		if result.exitCode != 1 || attempt > *maxRetries {
			break
		}

		if result.sessionID != "" {
			resumeID = result.sessionID
		}

		writeRetryEvent(writer, result.sessionID, runID, attempt+1, *maxRetries)

		select {
		case <-time.After(backoffDelay(attempt)):
		case <-timer:
			return exitWithSentinel(124)
		case <-ctx.Done():
			return exitWithSentinel(130)
		}
	}

	return exitWithSentinel(result.exitCode)
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
	runID string,
	timeout <-chan time.Time,
	stderr io.Writer,
) int {
	var finalStopReason string
	promptReturned := false
	var permissionDone <-chan error
	tracker := newStatusTracker(sessionID, runID)

	writeStatus := func(ev events.Event, ok bool) bool {
		if !ok {
			return true
		}
		if err := writer.Write(ev); err != nil {
			fmt.Fprintf(stderr, "avenor: write event: %v\n", err)
			return false
		}
		return true
	}

	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				if finalStopReason == "" {
					return 1
				}
				return runtime.ExitCodeForStopReason(finalStopReason)
			}
			markerHandled := false
			if event.Event == "agent.message_chunk" || event.Event == "agent.thought_chunk" {
				if text := chunkText(event); text != "" {
					if phase, label, ok := digest.ExtractStatusMarker(text); ok {
						if !writeStatus(tracker.ObserveMarker(phase, label)) {
							return 1
						}
						markerHandled = true
					}
				}
			}
			if !markerHandled {
				if !writeStatus(tracker.Observe(event)) {
					return 1
				}
			}
			if event.Event == "permission.request" && fileHandler != nil {
				if permissionDone != nil {
					emitErrorEvent(writer, sessionID, runID, "permission", "another permission request is already pending", stderr)
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
					return cancelAndEnd(provider, writer, sessionID, runID, "cancelled", stderr)
				}
				emitErrorEvent(writer, sessionID, runID, "prompt", fmt.Sprintf("prompt: %v", err), stderr)
				return 1
			}
			if finalStopReason != "" {
				return runtime.ExitCodeForStopReason(finalStopReason)
			}
		case err := <-permissionDone:
			permissionDone = nil
			if err != nil {
				emitErrorEvent(writer, sessionID, runID, "permission", fmt.Sprintf("permission handler: %v", err), stderr)
				return 1
			}
			if !writeStatus(tracker.PermissionAnswered()) {
				return 1
			}
		case <-ctx.Done():
			return cancelAndEnd(provider, writer, sessionID, runID, "cancelled", stderr)
		case <-timeout:
			return cancelAndEnd(provider, writer, sessionID, runID, "timeout", stderr)
		}
	}
}

func cancelAndEnd(provider runtime.Provider, writer *eventWriter, sessionID, runID, stopReason string, stderr io.Writer) int {
	cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := provider.Cancel(cancelCtx, sessionID); err != nil {
		emitErrorEvent(writer, sessionID, runID, "cancel", fmt.Sprintf("cancel session: %v", err), stderr)
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
