package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// ---- readCursor tests ----

func TestReadCursor(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, dir string) string // returns path
		wantOffset  int64
		wantErrSub  string // non-empty: error must contain this substring
	}{
		{
			name: "file missing returns zero",
			setup: func(t *testing.T, dir string) string {
				return filepath.Join(dir, "no-such-cursor")
			},
			wantOffset: 0,
		},
		{
			name: "valid offset",
			setup: func(t *testing.T, dir string) string {
				p := filepath.Join(dir, "cursor")
				if err := os.WriteFile(p, []byte("1234\n"), 0o644); err != nil {
					t.Fatalf("setup: %v", err)
				}
				return p
			},
			wantOffset: 1234,
		},
		{
			name: "trailing newline trimmed",
			setup: func(t *testing.T, dir string) string {
				p := filepath.Join(dir, "cursor")
				// two trailing newlines — TrimRight strips all of them
				if err := os.WriteFile(p, []byte("42\n\n"), 0o644); err != nil {
					t.Fatalf("setup: %v", err)
				}
				return p
			},
			wantOffset: 42,
		},
		{
			name: "negative integer rejected",
			setup: func(t *testing.T, dir string) string {
				p := filepath.Join(dir, "cursor")
				if err := os.WriteFile(p, []byte("-5\n"), 0o644); err != nil {
					t.Fatalf("setup: %v", err)
				}
				return p
			},
			wantErrSub: "negative offset",
		},
		{
			name: "non-decimal rejected",
			setup: func(t *testing.T, dir string) string {
				p := filepath.Join(dir, "cursor")
				if err := os.WriteFile(p, []byte("0xFF\n"), 0o644); err != nil {
					t.Fatalf("setup: %v", err)
				}
				return p
			},
			wantErrSub: "invalid cursor file",
		},
		{
			name: "unreadable file returns error",
			setup: func(t *testing.T, dir string) string {
				p := filepath.Join(dir, "cursor")
				if err := os.WriteFile(p, []byte("99\n"), 0o644); err != nil {
					t.Fatalf("setup: %v", err)
				}
				// Make it unreadable.
				if err := os.Chmod(p, 0o000); err != nil {
					t.Fatalf("chmod: %v", err)
				}
				t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
				return p
			},
			wantErrSub: "read cursor file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := tt.setup(t, dir)

			// Skip permission test when running as root (chmod has no effect).
			if tt.name == "unreadable file returns error" {
				if os.Getuid() == 0 {
					t.Skip("running as root; file permission restrictions do not apply")
				}
			}

			got, err := readCursor(path)

			if tt.wantErrSub != "" {
				if err == nil {
					t.Fatalf("readCursor() error = nil, want error containing %q", tt.wantErrSub)
				}
				if errMsg := err.Error(); !containsStr(errMsg, tt.wantErrSub) {
					t.Fatalf("readCursor() error = %q, want to contain %q", errMsg, tt.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("readCursor() unexpected error = %v", err)
			}
			if got != tt.wantOffset {
				t.Fatalf("readCursor() = %d, want %d", got, tt.wantOffset)
			}
		})
	}
}

// TestReadCursorWrapsParseError verifies that a parse error from strconv is
// wrapped (not stringified), so callers can errors.As to a *strconv.NumError.
func TestReadCursorWrapsParseError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cursor")
	if err := os.WriteFile(p, []byte("not-a-number\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := readCursor(p)
	if err == nil {
		t.Fatal("readCursor() error = nil, want error")
	}
	var numErr *strconv.NumError
	if !errors.As(err, &numErr) {
		t.Fatalf("readCursor() error %v not wrapping *strconv.NumError", err)
	}
}

// ---- validateCursorAgainstLog tests ----

// fakeFileInfo is a minimal os.FileInfo used to test validateCursorAgainstLog
// without needing a real file.
type fakeFileInfo struct {
	name string
	size int64
}

func (f fakeFileInfo) Name() string      { return f.name }
func (f fakeFileInfo) Size() int64       { return f.size }
func (f fakeFileInfo) Mode() os.FileMode { return 0o644 }
func (f fakeFileInfo) IsDir() bool       { return false }
func (f fakeFileInfo) Sys() any          { return nil }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }

// validateCursorAgainstLogTests covers the three meaningful cases.
func TestValidateCursorAgainstLog(t *testing.T) {
	info := fakeFileInfo{name: "app.log", size: 100}

	tests := []struct {
		name    string
		offset  int64
		wantErr bool
	}{
		{name: "offset less than size", offset: 50, wantErr: false},
		{name: "offset equal to size", offset: 100, wantErr: false},
		{name: "offset greater than size", offset: 101, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCursorAgainstLog(tt.offset, info)
			if tt.wantErr && err == nil {
				t.Fatalf("validateCursorAgainstLog(%d, size=%d) = nil, want error", tt.offset, info.Size())
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateCursorAgainstLog(%d, size=%d) = %v, want nil", tt.offset, info.Size(), err)
			}
			if tt.wantErr {
				msg := err.Error()
				want := fmt.Sprintf("offset %d > size %d", tt.offset, info.Size())
				if !containsStr(msg, want) {
					t.Fatalf("error %q does not contain %q", msg, want)
				}
			}
		})
	}
}

func containsStr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && findSubstring(s, sub))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
