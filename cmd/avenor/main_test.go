package main

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "--version")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to run avenor --version: %v", err)
	}

	outputStr := strings.TrimSpace(string(output))
	expectedVersion := fmt.Sprintf("avenor v%s", Version)

	if outputStr != expectedVersion {
		t.Errorf("expected %q, got %q", expectedVersion, outputStr)
	}
}
