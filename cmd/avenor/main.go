package main

import (
	"flag"
	"fmt"
	"os"
)

const Version = "0.0.1"

func main() {
	fs := flag.NewFlagSet("avenor", flag.ExitOnError)
	version := fs.Bool("version", false, "print version and exit")
	fs.BoolVar(version, "v", false, "print version and exit (short)")

	err := fs.Parse(os.Args[1:])
	if err != nil {
		os.Exit(1)
	}

	if *version {
		fmt.Printf("avenor v%s\n", Version)
		os.Exit(0)
	}

	// Default behavior: print usage
	fmt.Fprintf(os.Stderr, "Usage: avenor [options]\n")
	fmt.Fprintf(os.Stderr, "Options:\n")
	fs.PrintDefaults()
	os.Exit(0)
}
