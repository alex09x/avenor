package digest

import (
	"regexp"
	"strings"
)

var loopMarkerRe = regexp.MustCompile(`(?i)\[loop:\s*(\w+)(?:\s*\|\s*([^\]]*))?\]`)

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
	matches := loopMarkerRe.FindAllStringSubmatch(text, -1)
	var bestDir string
	var bestLabel string
	bestSev := 0
	for _, m := range matches {
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
