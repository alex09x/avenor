package looprunner

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"time"

	"github.com/sdougbrown/avenor/internal/events"
)

type EventWriter interface {
	Write(event events.Event) error
}

type RunOptions struct {
	WorkDir      string
	RunID        string
	EventSink    EventWriter
	Config       *LoopConfig
	MaxRetries   int
	PhaseAttempt func(ctx context.Context, phase Phase, attemptNum int, iteration int, prevSessionID string) (PhaseAttemptResult, error)
}

type PhaseAttemptResult struct {
	ExitCode      int
	SessionID     string
	LoopDirective string
	LoopLabel     string
}

type RunResult struct {
	ExitCode   int
	StopReason string
	SessionID  string
	Reason     string
}

type loopMarker struct {
	directive string
	label     string
}

func Run(ctx context.Context, opts RunOptions) (RunResult, error) {
	if err := emitLoopStart(opts.EventSink, opts.RunID, opts.Config.MaxIterations, len(opts.Config.Pre), len(opts.Config.Loop)); err != nil {
		return RunResult{}, err
	}

	prevPhaseCommit, _ := captureHeadCommit(opts.WorkDir)

	for _, phase := range opts.Config.Pre {
		if err := ctx.Err(); err != nil {
			return cancelledRunResult(ctx, opts, 0)
		}

		result, err := executePhase(ctx, opts, phase, 0, "", prevPhaseCommit)
		if err != nil {
			_ = emitLoopEnd(opts.EventSink, opts.RunID, "phase_failure", "", 0)
			return RunResult{}, err
		}

		prevPhaseCommit, _ = captureHeadCommit(opts.WorkDir)

		if err := ctx.Err(); err != nil {
			return cancelledRunResult(ctx, opts, 0)
		}

		sr := stopReasonFromExitCode(result.ExitCode)
		if sr != "end_turn" {
			_ = emitLoopEnd(opts.EventSink, opts.RunID, "phase_failure", "", 0)
			return RunResult{
				ExitCode:   result.ExitCode,
				StopReason: sr,
				SessionID:  result.SessionID,
			}, nil
		}
	}

	if len(opts.Config.Loop) == 0 {
		_ = emitLoopEnd(opts.EventSink, opts.RunID, "end_turn", "", 0)
		return RunResult{ExitCode: 0, StopReason: "end_turn"}, nil
	}

	prevSessionIDs := make(map[int]string)
	var lastSessionIDs map[int]string
	iterationsCompleted := 0

	for iteration := 1; iteration <= opts.Config.MaxIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			return cancelledRunResult(ctx, opts, iterationsCompleted)
		}

		for phaseIdx, phase := range opts.Config.Loop {
			prevSessionID := ""
			if phase.ResumeFromPrevious && iteration > 1 && phaseIdx > 0 {
				prevSessionID = lastSessionIDs[phaseIdx-1]
			}

			result, err := executePhase(ctx, opts, phase, iteration, prevSessionID, prevPhaseCommit)
			if err != nil {
				_ = emitLoopEnd(opts.EventSink, opts.RunID, "phase_failure", "", iterationsCompleted)
				return RunResult{}, err
			}

			prevSessionIDs[phaseIdx] = result.SessionID
			prevPhaseCommit, _ = captureHeadCommit(opts.WorkDir)

			if err := ctx.Err(); err != nil {
				return cancelledRunResult(ctx, opts, iterationsCompleted)
			}

			sr := stopReasonFromExitCode(result.ExitCode)

			switch result.LoopDirective {
			case "abort":
				_ = emitLoopEnd(opts.EventSink, opts.RunID, "abort", result.LoopLabel, iterationsCompleted)
				return RunResult{
					ExitCode:   5,
					StopReason: "blocked",
					SessionID:  result.SessionID,
					Reason:     result.LoopLabel,
				}, nil
			case "exit":
				_ = emitLoopEnd(opts.EventSink, opts.RunID, "marker", result.LoopLabel, iterationsCompleted)
				return RunResult{
					ExitCode:   0,
					StopReason: "end_turn",
					SessionID:  result.SessionID,
				}, nil
			}

			if sr != "end_turn" {
				_ = emitLoopEnd(opts.EventSink, opts.RunID, "phase_failure", "", iterationsCompleted)
				return RunResult{
					ExitCode:   result.ExitCode,
					StopReason: sr,
					SessionID:  result.SessionID,
				}, nil
			}
		}

		lastSessionIDs = make(map[int]string, len(prevSessionIDs))
		for k, v := range prevSessionIDs {
			lastSessionIDs[k] = v
		}

		iterationsCompleted = iteration
	}

	_ = emitLoopEnd(opts.EventSink, opts.RunID, "max_iterations", "", iterationsCompleted)
	return RunResult{ExitCode: 0, StopReason: "end_turn"}, nil
}

func executePhase(ctx context.Context, opts RunOptions, phase Phase, iteration int, prevSessionID string, prevPhaseCommit string) (result PhaseAttemptResult, rerr error) {
	diffStat, changedFiles, _ := CaptureGitDelta(opts.WorkDir, prevPhaseCommit)

	tmplCtx := TemplateContext{
		RunID:           opts.RunID,
		Phase:           phase.Name,
		Iteration:       iteration,
		MaxIterations:   opts.Config.MaxIterations,
		WorkDir:         opts.WorkDir,
		PrevPhaseCommit: prevPhaseCommit,
		DiffStat:        diffStat,
		ChangedFiles:    changedFiles,
	}

	rendered, err := RenderPrompt(phase.Prompt, tmplCtx)
	if err != nil {
		return PhaseAttemptResult{}, fmt.Errorf("render prompt for phase %q: %w", phase.Name, err)
	}

	renderedPhase := phase
	renderedPhase.Prompt = rendered

	kind := "pre"
	if iteration > 0 {
		kind = "loop"
	}

	if err := emitPhaseStart(opts.EventSink, opts.RunID, phase.Name, iteration, kind); err != nil {
		return PhaseAttemptResult{}, err
	}

	started := true
	defer func() {
		if started {
			_ = emitPhaseEnd(opts.EventSink, opts.RunID, phase.Name, iteration, stopReasonFromExitCode(result.ExitCode), markerFromResult(result))
		}
	}()

	var retryCount int

	wrappedAttempt := func(ctx context.Context) (PhaseAttemptResult, error) {
		if retryCount > 0 && retryCount < opts.MaxRetries {
			emitRetry(opts.EventSink, opts.RunID, retryCount, opts.MaxRetries)
		}
		r, err := opts.PhaseAttempt(ctx, renderedPhase, retryCount, iteration, prevSessionID)
		retryCount++
		if err != nil {
			return r, err
		}
		return r, nil
	}

	result, rerr = runPhaseWithRetry(ctx, wrappedAttempt, opts.MaxRetries)
	return
}

func runPhaseWithRetry(ctx context.Context, attemptFn func(ctx context.Context) (PhaseAttemptResult, error), maxRetries int) (PhaseAttemptResult, error) {
	result, err := attemptFn(ctx)
	if err != nil {
		return result, err
	}

	for retry := 1; retry <= maxRetries; retry++ {
		if result.ExitCode == 0 {
			return result, nil
		}

		select {
		case <-ctx.Done():
			return result, nil
		case <-time.After(backoffDuration(retry - 1)):
		}

		result, err = attemptFn(ctx)
		if err != nil {
			return result, err
		}
	}

	return result, nil
}

func cancelledRunResult(ctx context.Context, opts RunOptions, iterationsCompleted int) (RunResult, error) {
	exitReason := "cancelled"
	code := 130
	if ctx.Err() == context.DeadlineExceeded {
		exitReason = "timeout"
		code = 124
	}
	_ = emitLoopEnd(opts.EventSink, opts.RunID, exitReason, "", iterationsCompleted)
	return RunResult{ExitCode: code, StopReason: exitReason}, nil
}

func markerFromResult(result PhaseAttemptResult) *loopMarker {
	if result.LoopDirective == "" {
		return nil
	}
	return &loopMarker{directive: result.LoopDirective, label: result.LoopLabel}
}

func emitRetry(w EventWriter, runID string, attempt, maxRetries int) {
	fields := map[string]any{
		"attempt":     attempt,
		"max_retries": maxRetries,
		"ts":          time.Now().UnixMilli(),
	}
	if runID != "" {
		fields["run_id"] = runID
	}
	_ = w.Write(events.Event{
		Event:  "avenor.retry",
		Fields: fields,
	})
}

func emitLoopStart(w EventWriter, runID string, maxIterations, preCount, loopCount int) error {
	fields := map[string]any{
		"ts":               time.Now().UnixMilli(),
		"max_iterations":   maxIterations,
		"pre_phase_count":  preCount,
		"loop_phase_count": loopCount,
	}
	if runID != "" {
		fields["run_id"] = runID
	}
	return w.Write(events.Event{
		Event:  "avenor.loop.start",
		Fields: fields,
	})
}

func emitPhaseStart(w EventWriter, runID, phase string, iteration int, kind string) error {
	fields := map[string]any{
		"ts":        time.Now().UnixMilli(),
		"phase":     phase,
		"iteration": iteration,
		"kind":      kind,
	}
	if runID != "" {
		fields["run_id"] = runID
	}
	return w.Write(events.Event{
		Event:  "avenor.phase.start",
		Fields: fields,
	})
}

func emitPhaseEnd(w EventWriter, runID, phase string, iteration int, stopReason string, marker *loopMarker) error {
	fields := map[string]any{
		"ts":          time.Now().UnixMilli(),
		"phase":       phase,
		"iteration":   iteration,
		"stop_reason": stopReason,
	}
	if runID != "" {
		fields["run_id"] = runID
	}
	if marker != nil {
		if marker.directive == "abort" {
			fields["abort_marker"] = true
			if marker.label != "" {
				fields["abort_marker_label"] = marker.label
			}
		} else if marker.directive == "exit" {
			fields["exit_marker"] = true
			if marker.label != "" {
				fields["exit_marker_label"] = marker.label
			}
		}
	}
	return w.Write(events.Event{
		Event:  "avenor.phase.end",
		Fields: fields,
	})
}

func emitLoopEnd(w EventWriter, runID string, exitReason, exitLabel string, iterationsCompleted int) error {
	fields := map[string]any{
		"ts":                   time.Now().UnixMilli(),
		"exit_reason":          exitReason,
		"iterations_completed": iterationsCompleted,
	}
	if runID != "" {
		fields["run_id"] = runID
	}
	if exitLabel != "" {
		fields["exit_label"] = exitLabel
	}
	return w.Write(events.Event{
		Event:  "avenor.loop.end",
		Fields: fields,
	})
}

func captureHeadCommit(workDir string) (string, error) {
	cmd := exec.Command("git", "-C", workDir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

func stopReasonFromExitCode(code int) string {
	switch code {
	case 0:
		return "end_turn"
	case 2:
		return "refusal"
	case 3:
		return "max_tokens"
	case 4:
		return "max_turn_requests"
	case 5:
		return "blocked"
	case 124:
		return "timeout"
	case 130:
		return "cancelled"
	default:
		return ""
	}
}

func backoffDuration(retry int) time.Duration {
	d := time.Duration(math.Pow(2, float64(retry+1))) * time.Second
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}
