package digest

import (
	"regexp"
	"strings"
)

var loopMarkerRe = regexp.MustCompile(`(?i)^\s*\[loop:\s*(\w+)(?:\s*\|\s*([^\]]*))?\]\s*$`)

func loopDirectiveSeverity(directive string) int {
	switch directive {
	case "abort":
		return 3
	case "exit":
		return 2
	case "continue":
		return 1
	default:
		return 0
	}
}

func ExtractLoopMarker(text string) (directive, label string, ok bool) {
	var bestDir string
	var bestLabel string
	bestSev := 0
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := loopMarkerRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		dir := strings.ToLower(m[1])
		sev := loopDirectiveSeverity(dir)
		if sev == 0 {
			continue
		}
		if sev > bestSev {
			bestDir = dir
			bestLabel = strings.TrimSpace(m[2])
			bestSev = sev
		}
	}
	if bestSev == 0 {
		return "", "", false
	}
	return bestDir, bestLabel, true
}
