package looprunner

import (
	"bytes"
	"os/exec"
	"text/template"
)

type TemplateContext struct {
	RunID           string
	Phase           string
	Iteration       int
	MaxIterations   int
	WorkDir         string
	PrevPhaseCommit string
	DiffStat        string
	ChangedFiles    string
}

func RenderPrompt(tmpl string, ctx TemplateContext) (string, error) {
	t, err := template.New("prompt").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func CaptureGitDelta(workDir, prevCommit string) (diffStat, changedFiles string, err error) {
	if prevCommit == "" {
		return "", "", nil
	}

	diffStat, err = runGit(workDir, "diff", "--stat", prevCommit)
	if err != nil {
		return "", "", nil
	}

	changedFiles, err = runGit(workDir, "diff", "--name-only", prevCommit)
	if err != nil {
		return "", "", nil
	}

	return diffStat, changedFiles, nil
}

func runGit(workDir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(bytes.TrimRight(out, "\n")), nil
}
