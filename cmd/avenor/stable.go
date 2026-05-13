package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sdougbrown/avenor/internal/stable"
)

func runStable(args []string) int {
	fs := flag.NewFlagSet("stable", flag.ContinueOnError)
	controlSocket := fs.String("control-socket", "", "unix socket path for the control plane (required)")
	httpDebug := fs.String("http-debug", "", "http debug adapter bind address")
	maxRuntimes := fs.Int("max-runtimes", 8, "maximum concurrent child runtimes")
	idleTimeout := fs.Duration("idle-timeout", 0, "exit after this duration with no child runtimes and no control connections")
	shutdownTimeout := fs.Duration("shutdown-timeout", 10*time.Second, "graceful shutdown timeout before killing children")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *controlSocket == "" {
		fmt.Fprintln(os.Stderr, "avenor stable: --control-socket is required")
		return 1
	}

	sup := stable.NewSupervisor(stable.Config{
		ControlSocket:   *controlSocket,
		HTTPDebug:       *httpDebug,
		MaxRuntimes:     *maxRuntimes,
		IdleTimeout:     *idleTimeout,
		ShutdownTimeout: *shutdownTimeout,
	})
	return sup.Run()
}
