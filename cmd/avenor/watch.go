package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sdougbrown/avenor/internal/digest"
)

func runWatch(args []string) int {
	fs := flag.NewFlagSet("avenor watch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	follow := fs.Bool("follow", false, "poll and tail the log")
	format := fs.String("format", "plain", "output format: plain or json")
	pollInterval := fs.Duration("poll-interval", 250*time.Millisecond, "follow-mode sleep interval")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "avenor watch: <log> is required")
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "avenor watch: expected exactly one <log>")
		return 2
	}
	if *format != "plain" && *format != "json" {
		fmt.Fprintf(os.Stderr, "avenor watch: unsupported --format %q\n", *format)
		return 2
	}

	file, err := os.Open(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "avenor watch: open %s: %v\n", fs.Arg(0), err)
		return 2
	}
	defer file.Close()

	out := bufio.NewWriter(os.Stdout)
	opts := digest.Options{
		Follow:       *follow,
		PollInterval: *pollInterval,
		Format:       *format,
	}

	if !*follow {
		if err := digest.Stream(file, out, opts); err != nil {
			_ = out.Flush()
			fmt.Fprintf(os.Stderr, "avenor watch: %v\n", err)
			return 1
		}
		if err := out.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "avenor watch: flush stdout: %v\n", err)
			return 1
		}
		return 0
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- digest.Stream(file, out, opts)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case err := <-errCh:
		if flushErr := out.Flush(); flushErr != nil {
			fmt.Fprintf(os.Stderr, "avenor watch: flush stdout: %v\n", flushErr)
			return 1
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "avenor watch: %v\n", err)
			return 1
		}
		return 0
	case <-sigCh:
		_ = file.Close()
		var err error
		select {
		case err = <-errCh:
		case <-time.After(time.Second):
		}
		if flushErr := out.Flush(); flushErr != nil {
			fmt.Fprintf(os.Stderr, "avenor watch: flush stdout: %v\n", flushErr)
			return 1
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "avenor watch: %v\n", err)
			return 1
		}
		return 0
	}
}
