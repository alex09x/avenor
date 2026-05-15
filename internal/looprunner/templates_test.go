package looprunner

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestTemplateRenderPrompt(t *testing.T) {
	tests := []struct {
		name    string
		tmpl    string
		ctx     TemplateContext
		want    string
		wantErr bool
	}{
		{
			name: "phase and iteration",
			tmpl: "Phase {{.Phase}}, iteration {{.Iteration}}",
			ctx: TemplateContext{
				Phase:     "test",
				Iteration: 2,
			},
			want: "Phase test, iteration 2",
		},
		{
			name: "run id",
			tmpl: "{{.RunID}}",
			ctx: TemplateContext{
				RunID: "abc123",
			},
			want: "abc123",
		},
		{
			name: "changed files present",
			tmpl: "{{if .ChangedFiles}}changed: {{.ChangedFiles}}{{else}}no changes{{end}}",
			ctx: TemplateContext{
				ChangedFiles: "foo.go\nbar.go",
			},
			want: "changed: foo.go\nbar.go",
		},
		{
			name: "changed files absent",
			tmpl: "{{if .ChangedFiles}}changed: {{.ChangedFiles}}{{else}}no changes{{end}}",
			ctx: TemplateContext{
				ChangedFiles: "",
			},
			want: "no changes",
		},
		{
			name:    "malformed template",
			tmpl:    "{{.NonexistentField",
			ctx:     TemplateContext{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RenderPrompt(tt.tmpl, tt.ctx)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("RenderPrompt() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTemplateCaptureGitDelta(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	workDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = workDir
	if err := cmd.Run(); err != nil {
		t.Skip("not in a git repository")
	}

	t.Run("empty prevCommit", func(t *testing.T) {
		diffStat, changedFiles, err := CaptureGitDelta(workDir, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if diffStat != "" {
			t.Errorf("diffStat = %q, want empty", diffStat)
		}
		if changedFiles != "" {
			t.Errorf("changedFiles = %q, want empty", changedFiles)
		}
	})

	t.Run("valid commit SHA", func(t *testing.T) {
		cmd := exec.Command("git", "log", "--oneline", "-1", "--format=%H")
		cmd.Dir = workDir
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		prevCommit := strings.TrimSpace(string(out))
		if prevCommit == "" {
			t.Skip("no commits in repository")
		}

		diffStat, changedFiles, err := CaptureGitDelta(workDir, prevCommit)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_ = diffStat
		_ = changedFiles
	})
}
