package services

import (
	"bufio"
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

// Inline `dupehound:ignore` markers.

// ignoredLines returns a set of 1-indexed line numbers that are immediately
// followed by a function definition, where the preceding comment contains
// "dupehound:ignore". This is a two-pass approach: first find marker lines,
// then mark the function body lines that follow.
func findIgnoreMarkerLines(content string, lang *domain.Language) map[int]bool {
	markers := make(map[int]bool)
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		// Check for a comment containing dupehound:ignore.
		if isIgnoreComment(line, lang) {
			markers[lineNum] = true
		}
	}
	return markers
}

// isIgnoreComment returns true if the line is a comment containing "dupehound:ignore".
func isIgnoreComment(line string, lang *domain.Language) bool {
	if lang == nil {
		return false
	}
	// Check line comment prefix.
	if lang.LineComment != "" && strings.HasPrefix(line, lang.LineComment) {
		return strings.Contains(line, "dupehound:ignore")
	}
	// Python/Ruby/Shell: # comment.
	if strings.HasPrefix(line, "#") {
		return strings.Contains(line, "dupehound:ignore")
	}
	// Block comment single line.
	if lang.BlockStart != "" && strings.HasPrefix(line, lang.BlockStart) {
		return strings.Contains(line, "dupehound:ignore")
	}
	return false
}

// TokenizeFileWithIgnore tokenizes content and also returns which tokens follow
// a dupehound:ignore marker. Works like TokenizeFile but sets IgnoreMark on
// the first token on lines immediately after an ignore comment.
func TokenizeFileWithIgnore(content string, lang *domain.Language) []Token {
	// First pass: find lines with ignore markers.
	markerLines := findIgnoreMarkerLines(content, lang)
	// Second pass: tokenize normally.
	tokens := TokenizeFile(content, lang)
	// Mark tokens on lines immediately after a marker line.
	for i := range tokens {
		if markerLines[tokens[i].Line-1] {
			tokens[i].IgnoreMark = true
		}
	}
	return tokens
}

// markIgnoredBlocks identifies function bodies that follow a dupehound:ignore marker.
// Returns a per-token bool slice where true means the token is in a suppressed block.
func markIgnoredBlocks(tokens []Token, inFunc []bool) []bool {
	n := len(tokens)
	ignored := make([]bool, n)

	// Find tokens with IgnoreMark that are inside function boundaries.
	// The strategy: for each token with IgnoreMark, find the function body
	// that starts at or after this line and mark all its tokens as ignored.
	for i := 0; i < n; i++ {
		if !tokens[i].IgnoreMark {
			continue
		}
		// Find the first token that is in-function on a subsequent line.
		markerLine := tokens[i].Line
		for j := i + 1; j < n; j++ {
			if tokens[j].Line <= markerLine {
				continue
			}
			if j < len(inFunc) && inFunc[j] {
				// Found the start of the function body — mark until end.
				// Walk forward until we find a token that's no longer in this function.
				// Use brace depth tracking relative to start.
				for k := j; k < n; k++ {
					if k < len(inFunc) && inFunc[k] {
						ignored[k] = true
					} else if k > j {
						break
					}
				}
				break
			}
			// If we've skipped past any potential function start, stop.
			if tokens[j].Line > markerLine+3 {
				break
			}
		}
	}

	return ignored
}
