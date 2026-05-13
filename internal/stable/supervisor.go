package stable

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"github.com/sdougbrown/avenor/internal/cli"
	"github.com/sdougbrown/avenor/internal/control"
	"github.com/sdougbrown/avenor/internal/events"
	"github.com/sdougbrown/avenor/internal/permission"
	"github.com/sdougbrown/avenor/internal/runtime"
	"github.com/sdougbrown/avenor/internal/runtime/opencodeacp"
)

type Config struct {
	ControlSocket   string
	HTTPDebug       string
	MaxRuntimes     int
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

type SpawnParams struct {
	Prompt            string `json:"prompt,omitempty"`
	PromptFile        string `json:"prompt_file,omitempty"`
	Dir               string `json:"dir"`
	Agent             string `json:"agent,omitempty"`
	Label             string `json:"label,omitempty"`
	Model             string `json:"model,omitempty"`
	ServerURL         string `json:"server_url,omitempty"`
	OnEvent           string `json:"on_event,omitempty"`
	SentinelFile      string `json:"sentinel_file,omitempty"`
	PermissionHandler string `json:"permission_handler,omitempty"`
	AutoApprove       bool   `json:"auto_approve,omitempty"`
	Timeout           int    `json:"timeout,omitempty"`
	MaxRetries        int    `json:"max_retries,omitempty"`
}

type SpawnResult struct {
	RuntimeID    string `json:"runtime_id"`
	SessionID    string `json:"session_id"`
	OnEvent      string `json:"on_event"`
	SentinelFile string `json:"sentinel_file"`
}

type childRuntime struct {
	id           string
	label        string
	provider     runtime.Provider
	session      runtime.Session
	eventWriter  cli.EventSink
	fileHandler  *permission.FileHandler
	autoApprove  bool
	runID        string
	dir          string
	onEvent      string
	sentinelFile string
	cancelFn     func()
	done         chan struct{}
	exitCode     int
	completed    bool
	promptQueue  []string
	mu           sync.Mutex
}

type Supervisor struct {
	config     Config
	runID      string
	control    *control.ControlServer
	state      *control.ControlState
	controlMu  sync.Mutex
	runtimes   map[string]*childRuntime
	nextID     int
	shutdownCh chan struct{}
	httpServer *control.HTTPDebugServer
}

func NewSupervisor(cfg Config) *Supervisor {
	runID := cli.GenerateRunID()
	state := control.NewState(runID, "", 0)
	sup := &Supervisor{
		config:     cfg,
		runID:      runID,
		state:      state,
		control:    control.NewServer(state),
		runtimes:   map[string]*childRuntime{},
		shutdownCh: make(chan struct{}),
	}
	sup.control.SetStableHandler(sup)
	return sup
}

func (s *Supervisor) Run() int {
	if err := s.control.Start(s.config.ControlSocket); err != nil {
		fmt.Fprintf(os.Stderr, "avenor stable: start control server: %v\n", err)
		return 1
	}
	defer s.control.Stop()

	if s.config.HTTPDebug != "" {
		s.httpServer = control.NewHTTPDebugServer(s.config.HTTPDebug, s.control)
		if err := s.httpServer.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "avenor stable: start http debug: %v\n", err)
			return 1
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = s.httpServer.Stop(shutdownCtx)
		}()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var idleDeadline time.Time
	if s.config.IdleTimeout > 0 {
		idleDeadline = time.Now().Add(s.config.IdleTimeout)
	}

	for {
		idleCh := idleCheck(s.config.IdleTimeout, s.activeRuntimeCount(), &idleDeadline)
		select {
		case <-ctx.Done():
			return s.shutdown("graceful")
		case <-s.shutdownCh:
			return s.shutdown("graceful")
		case <-idleCh:
			return s.shutdown("graceful")
		}
	}
}

func idleCheck(idleTimeout time.Duration, active int, deadline *time.Time) <-chan time.Time {
	if idleTimeout <= 0 {
		return nil
	}
	if active > 0 {
		*deadline = time.Now().Add(idleTimeout)
		return nil
	}
	return time.After(time.Until(*deadline))
}

func (s *Supervisor) spawn(params SpawnParams) (SpawnResult, error) {
	s.controlMu.Lock()
	if len(s.runtimes) >= s.config.MaxRuntimes {
		s.controlMu.Unlock()
		return SpawnResult{}, fmt.Errorf("max runtimes (%d) reached", s.config.MaxRuntimes)
	}
	s.nextID++
	rtID := fmt.Sprintf("rt_%d", s.nextID)

	// Reserve the slot to prevent TOCTOU bypass of the max-runtime limit.
	child := &childRuntime{
		id:   rtID,
		done: make(chan struct{}),
	}
	s.runtimes[rtID] = child
	s.controlMu.Unlock()

	// Clean up reserved slot on failure.
	defer func() {
		if child.provider == nil {
			s.controlMu.Lock()
			delete(s.runtimes, rtID)
			s.controlMu.Unlock()
		}
	}()

	if params.Dir == "" {
		params.Dir = "."
	}

	promptText := params.Prompt
	if promptText == "" && params.PromptFile != "" {
		data, err := os.ReadFile(params.PromptFile)
		if err != nil {
			return SpawnResult{}, fmt.Errorf("read prompt file: %w", err)
		}
		promptText = string(data)
	}
	if promptText == "" {
		return SpawnResult{}, fmt.Errorf("prompt or prompt_file is required")
	}

	// Per-runtime artifact directory.
	artifactDir := filepath.Join(os.TempDir(), "avenor-stable", s.runID, rtID)
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		return SpawnResult{}, fmt.Errorf("create artifact dir: %w", err)
	}

	onEvent := params.OnEvent
	if onEvent == "" {
		onEvent = filepath.Join(artifactDir, "events.ndjson")
	}
	sentinelFile := params.SentinelFile
	if sentinelFile == "" {
		sentinelFile = filepath.Join(artifactDir, "sentinel.env")
	}

	// Create event writer for this runtime.
	writer, err := cli.NewEventWriter(onEvent)
	if err != nil {
		return SpawnResult{}, fmt.Errorf("open event stream: %w", err)
	}

	var fileHandler *permission.FileHandler
	permHandler := params.PermissionHandler
	if permHandler == "" && !params.AutoApprove && sentinelFile != "" {
		permHandler = "file:" + cli.DerivePermBase(sentinelFile)
	}
	if permHandler != "" {
		fh, err := cli.ParsePermissionHandler(permHandler)
		if err != nil {
			_ = writer.Close()
			return SpawnResult{}, fmt.Errorf("permission handler: %w", err)
		}
		fileHandler = fh
	}

	// Start provider and session.
	startOpts := runtime.StartOptions{
		Agent: params.Agent,
		Label: params.Label,
		Dir:   params.Dir,
		Model: params.Model,
	}
	discovery := cli.DiscoverServer(params.ServerURL, os.Getenv)
	startOpts.ServerURL = discovery.URL

	provider := opencodeacp.NewWithOptions(startOpts)
	session, err := cli.StartSession(context.Background(), provider, startOpts, "")
	if err != nil {
		_ = provider.(interface{ Close() error }).Close()
		_ = writer.Close()
		return SpawnResult{}, fmt.Errorf("start session: %w", err)
	}

	// Populate child with fully-initialised state.
	child.label = params.Label
	child.provider = provider
	child.session = session
	child.eventWriter = writer
	child.fileHandler = fileHandler
	child.autoApprove = params.AutoApprove
	child.runID = s.runID
	child.dir = params.Dir
	child.onEvent = onEvent
	child.sentinelFile = sentinelFile

	// Initialise cancelFn in spawn so it's never nil when cancelRuntime reads it.
	childCtx, childCancel := context.WithCancel(context.Background())
	child.cancelFn = childCancel

	// Start the child event loop in a goroutine.
	go s.runChild(childCtx, child, promptText, params.Timeout, params.MaxRetries)

	return SpawnResult{
		RuntimeID:    rtID,
		SessionID:    session.SessionID,
		OnEvent:      onEvent,
		SentinelFile: sentinelFile,
	}, nil
}

func (s *Supervisor) runChild(ctx context.Context, child *childRuntime, promptText string, timeoutSec, maxRetries int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "avenor stable: child %s panic: %v\n", child.id, r)
			s.emitChildError(child, fmt.Sprintf("panic: %v", r), "error")
		}
		close(child.done)
		_ = child.eventWriter.Close()
		if closer, ok := child.provider.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		child.mu.Lock()
		child.completed = true
		child.mu.Unlock()
	}()

	var timer <-chan time.Time
	if timeoutSec > 0 {
		t := time.NewTimer(time.Duration(timeoutSec) * time.Second)
		defer t.Stop()
		timer = t.C
	}

	resumeID := ""
	for attempt := 1; ; attempt++ {
		if attempt > 1 && resumeID == "" && child.session.SessionID != "" {
			resumeID = child.session.SessionID
		}
		result := s.runChildAttempt(ctx, child, resumeID, promptText, timer)
		if result.exitCode != 1 || attempt > maxRetries {
			child.mu.Lock()
			child.exitCode = result.exitCode
			child.mu.Unlock()

			// Check for queued follow-up prompts after a successful turn.
			if result.exitCode == 0 {
				child.mu.Lock()
				if len(child.promptQueue) > 0 {
					nextPrompt := child.promptQueue[0]
					child.promptQueue = child.promptQueue[1:]
					child.mu.Unlock()
					if _, err := cli.StartSession(ctx, child.provider, runtime.StartOptions{
						Label: child.label,
						Dir:   child.dir,
					}, resumeID); err != nil {
						fmt.Fprintf(os.Stderr, "avenor stable: resume for queued prompt: %v\n", err)
						break
					}
					promptText = nextPrompt
					attempt = 1
					resumeID = result.sessionID
					if resumeID == "" && child.session.SessionID != "" {
						resumeID = child.session.SessionID
					}
					continue
				}
				child.mu.Unlock()
			}

			if child.sentinelFile != "" {
				cli.WriteSentinel(child.sentinelFile, result.exitCode, result.sessionID, runtime.StopReasonForExitCode(result.exitCode), s.runID, os.Stderr)
			}
			s.emitSessionEnd(child, result.exitCode, "end_turn")
			return
		}
		if ctx.Err() != nil {
			// Check for interrupt restart: a queued prompt from RuntimeInterruptAndPrompt.
			child.mu.Lock()
			hasPrompt := len(child.promptQueue) > 0
			if hasPrompt {
				nextPrompt := child.promptQueue[0]
				child.promptQueue = child.promptQueue[1:]
				child.mu.Unlock()
				if _, err := cli.StartSession(ctx, child.provider, runtime.StartOptions{
					Label: child.label,
					Dir:   child.dir,
				}, resumeID); err != nil {
					fmt.Fprintf(os.Stderr, "avenor stable: resume after interrupt: %v\n", err)
					return
				}
				promptText = nextPrompt
				attempt = 1
				resumeID = result.sessionID
				if resumeID == "" && child.session.SessionID != "" {
					resumeID = child.session.SessionID
				}
				continue
			}
			child.mu.Unlock()
			return
		}
		if result.sessionID != "" {
			resumeID = result.sessionID
		}
		select {
		case <-time.After(backoffDelay(attempt)):
		case <-ctx.Done():
			return
		}
	}
}

type childAttemptResult struct {
	exitCode  int
	sessionID string
}

func (s *Supervisor) runChildAttempt(ctx context.Context, child *childRuntime, resumeID, promptText string, timer <-chan time.Time) childAttemptResult {
	session, err := cli.StartSession(ctx, child.provider, runtime.StartOptions{
		Agent: "",
		Label: child.label,
		Dir:   child.dir,
	}, resumeID)
	if err != nil {
		return childAttemptResult{exitCode: 1}
	}
	child.mu.Lock()
	child.session = session
	child.mu.Unlock()

	eventCtx, cancelEvents := context.WithCancel(ctx)
	defer cancelEvents()

	eventCh, err := child.provider.Events(eventCtx, session.SessionID)
	if err != nil {
		return childAttemptResult{exitCode: 1, sessionID: session.SessionID}
	}

	promptDone := make(chan error, 1)
	go func() {
		defer func() { recover() }()
		promptDone <- child.provider.Prompt(ctx, session.SessionID, promptText)
	}()

	// Tag events with runtime_id and fan out to both file and control subscribers.
	taggedWriter := &runtimeFanoutWriter{
		base:      child.eventWriter,
		runtimeID: child.id,
		control:   s.control,
	}
	exitCode := cli.WaitForSession(ctx, child.provider, taggedWriter, child.fileHandler, nil, eventCh, promptDone, nil, session.SessionID, s.runID, child.label, child.autoApprove, timer, os.Stderr)
	return childAttemptResult{exitCode: exitCode, sessionID: session.SessionID}
}

func (s *Supervisor) emitSessionEnd(child *childRuntime, exitCode int, stopReason string) {
	s.control.PublishEvent(events.Event{
		Event:     "session.end",
		SessionID: child.session.SessionID,
		Fields: map[string]any{
			"stop_reason": stopReason,
			"runtime_id":  child.id,
			"exit_code":   exitCode,
			"ts":          time.Now().UnixMilli(),
		},
	})
}

func (s *Supervisor) emitChildError(child *childRuntime, message, source string) {
	s.control.PublishEvent(events.Event{
		Event:     "avenor.error",
		SessionID: child.session.SessionID,
		Fields: map[string]any{
			"message":    message,
			"source":     source,
			"runtime_id": child.id,
			"ts":         time.Now().UnixMilli(),
		},
	})
}

func (s *Supervisor) shutdown(mode string) int {
	s.controlMu.Lock()
	runtimes := make([]*childRuntime, 0, len(s.runtimes))
	for _, rt := range s.runtimes {
		runtimes = append(runtimes, rt)
	}
	s.controlMu.Unlock()

	for _, rt := range runtimes {
		if rt.cancelFn != nil {
			rt.cancelFn()
		}
	}

	timeout := s.config.ShutdownTimeout
	if mode == "kill" || timeout == 0 {
		for _, rt := range runtimes {
			<-rt.done
		}
		return 0
	}

	deadline := time.After(timeout)
	remaining := len(runtimes)
	for _, rt := range runtimes {
		select {
		case <-rt.done:
			remaining--
		case <-deadline:
		}
	}
	if remaining > 0 {
		fmt.Fprintf(os.Stderr, "avenor stable: %d runtimes did not finish within %v\n", remaining, timeout)
	}
	return 0
}

func (s *Supervisor) activeRuntimeCount() int {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	n := 0
	for _, rt := range s.runtimes {
		rt.mu.Lock()
		if !rt.completed {
			n++
		}
		rt.mu.Unlock()
	}
	return n
}

func (s *Supervisor) listRuntimes() []map[string]any {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	out := make([]map[string]any, 0, len(s.runtimes))
	for _, rt := range s.runtimes {
		rt.mu.Lock()
		status := "running"
		if rt.completed {
			status = "ended"
		}
		entry := map[string]any{
			"runtime_id":    rt.id,
			"session_id":    rt.session.SessionID,
			"label":         rt.label,
			"dir":           rt.dir,
			"status":        status,
			"exit_code":     rt.exitCode,
			"on_event":      rt.onEvent,
			"sentinel_file": rt.sentinelFile,
		}
		rt.mu.Unlock()
		out = append(out, entry)
	}
	return out
}

func (s *Supervisor) cancelRuntime(rtID string) error {
	s.controlMu.Lock()
	rt := s.runtimes[rtID]
	s.controlMu.Unlock()
	if rt == nil {
		return fmt.Errorf("runtime %q not found", rtID)
	}
	if rt.cancelFn != nil {
		rt.cancelFn()
	}
	return nil
}

func (s *Supervisor) answerPermission(rtID, requestID, optionID string) error {
	s.controlMu.Lock()
	rt := s.runtimes[rtID]
	s.controlMu.Unlock()
	if rt == nil {
		return fmt.Errorf("runtime %q not found", rtID)
	}
	return rt.provider.AnswerPermission(context.Background(), rt.session.SessionID, requestID, runtime.PermissionResponse{
		Outcome:  "selected",
		OptionID: optionID,
	})
}

type runtimeFanoutWriter struct {
	base      cli.EventSink
	runtimeID string
	control   *control.ControlServer
}

func (w *runtimeFanoutWriter) Write(ev events.Event) error {
	if ev.Fields == nil {
		ev.Fields = map[string]any{}
	}
	ev.Fields["runtime_id"] = w.runtimeID
	if w.control != nil {
		w.control.PublishEvent(ev)
	}
	return w.base.Write(ev)
}

func (w *runtimeFanoutWriter) Close() error { return w.base.Close() }

// StableHandler implementation.

func (s *Supervisor) Spawn(raw json.RawMessage) (any, error) {
	var p SpawnParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid spawn params: %w", err)
	}
	return s.spawn(p)
}

func (s *Supervisor) List() any {
	return s.listRuntimes()
}

func (s *Supervisor) Shutdown(mode string) error {
	if mode != "graceful" && mode != "kill" {
		return fmt.Errorf("shutdown mode must be graceful or kill")
	}
	s.shutdown(mode)
	select {
	case <-s.shutdownCh:
	default:
		close(s.shutdownCh)
	}
	return nil
}

func (s *Supervisor) RuntimeStatus(rtID string) (any, error) {
	s.controlMu.Lock()
	rt := s.runtimes[rtID]
	s.controlMu.Unlock()
	if rt == nil {
		return nil, fmt.Errorf("runtime %q not found", rtID)
	}
	rt.mu.Lock()
	status := "running"
	if rt.completed {
		status = "ended"
	}
	entry := map[string]any{
		"runtime_id":    rt.id,
		"session_id":    rt.session.SessionID,
		"label":         rt.label,
		"dir":           rt.dir,
		"status":        status,
		"exit_code":     rt.exitCode,
		"on_event":      rt.onEvent,
		"sentinel_file": rt.sentinelFile,
	}
	rt.mu.Unlock()
	return entry, nil
}

func (s *Supervisor) RuntimeCancel(rtID string) error {
	return s.cancelRuntime(rtID)
}

func (s *Supervisor) RuntimePrompt(rtID, text string) error {
	s.controlMu.Lock()
	rt := s.runtimes[rtID]
	s.controlMu.Unlock()
	if rt == nil {
		return fmt.Errorf("runtime %q not found", rtID)
	}
	rt.mu.Lock()
	rt.promptQueue = append(rt.promptQueue, text)
	rt.mu.Unlock()
	return nil
}

func (s *Supervisor) RuntimeAnswerPermission(rtID, requestID, optionID string) error {
	return s.answerPermission(rtID, requestID, optionID)
}

func (s *Supervisor) RuntimeInterruptAndPrompt(rtID, text string, keepQueue bool) error {
	s.controlMu.Lock()
	rt := s.runtimes[rtID]
	s.controlMu.Unlock()
	if rt == nil {
		return fmt.Errorf("runtime %q not found", rtID)
	}
	rt.mu.Lock()
	if !keepQueue {
		rt.promptQueue = nil
	}
	// Prepend the interrupt prompt to the front of the queue so it runs next.
	rt.promptQueue = append([]string{text}, rt.promptQueue...)
	rt.mu.Unlock()
	if rt.cancelFn != nil {
		rt.cancelFn()
	}
	return nil
}

func backoffDelay(attempt int) time.Duration {
	seconds := 2 << uint(attempt-1)
	if seconds > 30 {
		seconds = 30
	}
	return time.Duration(seconds) * time.Second
}
