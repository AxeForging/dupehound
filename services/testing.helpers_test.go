package services

import (
	"github.com/AxeForging/dupehound/domain"
)

// BuildTokenizedFile is a test helper that tokenizes a source file without
// applying any inline-ignore rules.
func BuildTokenizedFile(path, content string, lang *domain.Language) TokenizedFile {
	return buildTokenizedFile(path, TokenizeFile(content, lang), lang)
}

// Detect is a test helper around DetectWithOptions for tests that only need
// the basic minTokens + minSimilarity knobs.
func Detect(files []TokenizedFile, minTokens int, minSimilarity float64) []domain.Clone {
	clones, _ := DetectWithOptions(files, DetectOptions{
		MinTokens:     minTokens,
		MinSimilarity: minSimilarity,
	})
	return clones
}
