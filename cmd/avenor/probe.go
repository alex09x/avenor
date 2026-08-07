package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/alex09x/avenor/agyclient/events"
	"github.com/alex09x/avenor/agyclient/runtime"
	"github.com/alex09x/avenor/internal/runtime/opencodeacp"
)

func runProbe(args []string) int {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	dir := fs.String("dir", ".", "working directory for the probe")
	out := fs.String("out", "", "path to write the probe transcript")
	prompt := fs.String("prompt", "", "override the default probe prompt (Stage 1 discovery)")
	timeout := fs.Duration("timeout", 5*time.Minute, "probe timeout")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *out == "" {
		fmt.Fprintln(os.Stderr, "avenor probe: --out is required")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	probePrompt := opencodeacp.ProbePrompt
	if *prompt != "" {
		probePrompt = *prompt
	}
	transcript, err := opencodeacp.RunProbeWithPrompt(ctx, *dir, probePrompt)
	if writeErr := writeTranscript(*out, transcript); writeErr != nil {
		fmt.Fprintf(os.Stderr, "avenor probe: write transcript: %v\n", writeErr)
		return 1
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "avenor probe: %v\n", err)
		return 1
	}

	stopReason := finalStopReason(transcript)
	return runtime.ExitCodeForStopReason(stopReason)
}

func writeTranscript(path string, transcript []events.Event) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	for _, event := range transcript {
		if err := encoder.Encode(event); err != nil {
			return err
		}
	}
	return nil
}

func finalStopReason(transcript []events.Event) string {
	for i := len(transcript) - 1; i >= 0; i-- {
		if transcript[i].Event != "session.end" {
			continue
		}
		stopReason, _ := transcript[i].Fields["stop_reason"].(string)
		return stopReason
	}
	return ""
}
