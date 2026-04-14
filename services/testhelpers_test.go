package services

import (
	"github.com/AxeForging/dupehound/domain"
)

// BuildTokenizedFile is a test helper that tokenizes a source file without
// applying any inline-ignore rules.
func BuildTokenizedFile(path, content string, lang *domain.Language) TokenizedFile {
	tokens := TokenizeFile(content, lang)
	return TokenizedFile{
		Path:   path,
		Tokens: tokens,
		InFunc: markFunctionBodies(tokens, lang),
	}
}

// Detect is a test helper around DetectWithOptions for tests that only need
// the basic minTokens + minSimilarity knobs.
func Detect(files []TokenizedFile, minTokens int, minSimilarity float64) []domain.Clone {
	return DetectWithOptions(files, DetectOptions{
		MinTokens:     minTokens,
		MinSimilarity: minSimilarity,
	})
}
