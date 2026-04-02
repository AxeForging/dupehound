package services

import (
	"path/filepath"
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

// NormalizedFile holds normalized lines alongside their original line numbers.
type NormalizedFile struct {
	Lines       []string // normalized, non-empty lines
	LineNumbers []int    // 1-indexed original line number for each normalized line
}

// DetectLanguage returns the Language for a file path, or nil if unsupported.
func DetectLanguage(path string) *domain.Language {
	ext := strings.ToLower(filepath.Ext(path))
	for i := range domain.SupportedLanguages {
		for _, e := range domain.SupportedLanguages[i].Extensions {
			if e == ext {
				return &domain.SupportedLanguages[i]
			}
		}
	}
	return nil
}

// NormalizeFile strips comments, normalizes whitespace, and removes blank lines.
// Returns a NormalizedFile that maps each normalized line back to its original line number.
func NormalizeFile(content string, lang *domain.Language) NormalizedFile {
	rawLines := strings.Split(content, "\n")
	result := NormalizedFile{}

	inBlock := false

	for lineNum, raw := range rawLines {
		line := raw

		// Handle block comment end
		if inBlock {
			if lang.BlockEnd != "" {
				idx := strings.Index(line, lang.BlockEnd)
				if idx >= 0 {
					line = line[idx+len(lang.BlockEnd):]
					inBlock = false
				} else {
					continue
				}
			} else {
				continue
			}
		}

		// Handle block comment start
		if lang.BlockStart != "" {
			for {
				idx := strings.Index(line, lang.BlockStart)
				if idx < 0 {
					break
				}
				endIdx := strings.Index(line[idx+len(lang.BlockStart):], lang.BlockEnd)
				if endIdx >= 0 {
					// Block comment fully on this line — remove it
					after := line[idx+len(lang.BlockStart)+endIdx+len(lang.BlockEnd):]
					line = line[:idx] + after
				} else {
					// Block comment continues to next line
					line = line[:idx]
					inBlock = true
					break
				}
			}
		}

		// Handle line comment
		if lang.LineComment != "" {
			idx := strings.Index(line, lang.LineComment)
			if idx >= 0 {
				line = line[:idx]
			}
		}

		// Normalize whitespace
		line = strings.TrimSpace(line)
		// Collapse internal whitespace
		line = collapseSpaces(line)

		if line == "" {
			continue
		}

		result.Lines = append(result.Lines, line)
		result.LineNumbers = append(result.LineNumbers, lineNum+1) // 1-indexed
	}

	return result
}

// collapseSpaces replaces runs of whitespace with a single space.
func collapseSpaces(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if !inSpace {
				b.WriteRune(' ')
				inSpace = true
			}
		} else {
			b.WriteRune(r)
			inSpace = false
		}
	}
	return b.String()
}
