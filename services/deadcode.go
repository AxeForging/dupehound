package services

import (
	"path/filepath"
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

// FunctionDef holds extracted function definition metadata.
type FunctionDef struct {
	Name   string
	File   string
	Line   int
	Lang   string
	DefTok int // token index of the function name
}

// findDeadFunctions analyses tokenized files and returns functions whose
// names do not appear in any token outside their own definition line.
//
// defFiles is the set the user wants findings reported for (typically the
// in-scope set after --include / --exclude). usageFiles is the broader set
// used only to build the identifier-usage map — usually a superset of
// defFiles that includes test files even when the user excluded them from
// clone reporting. Passing the same slice for both is the simple "scan
// everything" mode and matches the original single-arg behavior.
//
// This is a heuristic — cross-package calls and reflection produce false
// positives. Splitting defs from usages eliminates the test-only-helper
// false positive: a function defined in production code but called only
// from a test file is no longer reported as dead, as long as the test file
// is in the usageFiles set.
func findDeadFunctions(defFiles, usageFiles []TokenizedFile) []domain.DeadFunc {
	// Phase 1: extract all function definitions from the in-scope set only.
	var defs []FunctionDef
	for _, tf := range defFiles {
		lang := DetectLanguage(tf.Path)
		if lang == nil {
			continue
		}
		extracted := extractFunctionDefs(tf, lang.Name)
		defs = append(defs, extracted...)
	}

	if len(defs) == 0 {
		return nil
	}

	// Phase 2: build a set of all identifier tokens across the broader
	// usage set. This is what fixes the false positive — a test-only helper
	// will appear here even if its defining file is excluded from clone
	// reporting.
	type fileLineKey struct {
		file string
		line int
	}
	nameUsages := make(map[string]map[fileLineKey]bool)
	for _, tf := range usageFiles {
		for _, tok := range tf.Tokens {
			if tok.Kind != TokIdent || tok.OrigText == "" {
				continue
			}
			name := tok.OrigText
			if nameUsages[name] == nil {
				nameUsages[name] = make(map[fileLineKey]bool)
			}
			nameUsages[name][fileLineKey{tf.Path, tok.Line}] = true
		}
	}

	// Phase 3: for each function def, check if its name appears anywhere else.
	var dead []domain.DeadFunc
	for _, def := range defs {
		usages, ok := nameUsages[def.Name]
		if !ok {
			// Name doesn't appear at all (only as keyword text, not ident) — dead.
			dead = append(dead, domain.DeadFunc{
				File:     def.File,
				Line:     def.Line,
				Name:     def.Name,
				Language: def.Lang,
			})
			continue
		}

		// Check if there's any usage outside the definition line in the def's file.
		hasExternalUsage := false
		for flk := range usages {
			if flk.file == def.File && flk.line == def.Line {
				continue // this is the definition line itself
			}
			hasExternalUsage = true
			break
		}
		if !hasExternalUsage {
			dead = append(dead, domain.DeadFunc{
				File:     def.File,
				Line:     def.Line,
				Name:     def.Name,
				Language: def.Lang,
			})
		}
	}

	return dead
}

// appendFuncDef records the identifier at tf.Tokens[tokIdx] as a function
// definition. Centralizes the FunctionDef struct literal used by every
// per-language scanner in extractFunctionDefs.
func appendFuncDef(defs []FunctionDef, tf TokenizedFile, langName string, tokIdx int) []FunctionDef {
	tok := tf.Tokens[tokIdx]
	return append(defs, FunctionDef{
		Name:   tok.OrigText,
		File:   tf.Path,
		Line:   tok.Line,
		Lang:   langName,
		DefTok: tokIdx,
	})
}

// extractFunctionDefs extracts function name + location from a TokenizedFile.
func extractFunctionDefs(tf TokenizedFile, langName string) []FunctionDef {
	var defs []FunctionDef
	n := len(tf.Tokens)
	ext := strings.ToLower(filepath.Ext(tf.Path))
	_ = ext

	switch langName {
	case "go", "rust", "kotlin", "swift", "scala", "php", "ruby", "lua":
		// Keyword-based: look for func/fn/fun/def/function keyword followed by an identifier.
		funcKwSet := funcKeywords[langName]
		for i := 0; i < n-1; i++ {
			tok := tf.Tokens[i]
			if tok.Kind != TokKeyword {
				continue
			}
			if !funcKwSet[tok.Text] {
				continue
			}
			// Next non-paren/non-receiver identifier is the function name.
			for j := i + 1; j < n; j++ {
				next := tf.Tokens[j]
				// Skip receiver in Go: (recv Type)
				if next.Kind == TokOperator && next.Text == "(" {
					// Find matching close paren.
					depth := 1
					j++
					for j < n && depth > 0 {
						if tf.Tokens[j].Kind == TokOperator {
							switch tf.Tokens[j].Text {
							case "(":
								depth++
							case ")":
								depth--
							}
						}
						j++
					}
					j-- // outer loop will j++ again
					continue
				}
				if next.Kind == TokIdent && next.OrigText != "" {
					defs = appendFuncDef(defs, tf, langName, j)
					break
				}
				// Stop at opening brace.
				if next.Kind == TokOperator && (next.Text == "{" || next.Text == "(") {
					break
				}
			}
		}
	case "python", "elixir":
		// def keyword, next ident is function name.
		defKws := map[string]bool{"def": true, "defp": true, "defmacro": true, "defmacrop": true}
		for i := 0; i < n-1; i++ {
			tok := tf.Tokens[i]
			if tok.Kind == TokKeyword && defKws[tok.Text] {
				for j := i + 1; j < n; j++ {
					next := tf.Tokens[j]
					if next.Kind == TokIdent && next.OrigText != "" {
						defs = appendFuncDef(defs, tf, langName, j)
						break
					}
					if next.Kind == TokOperator && next.Text == ":" {
						break
					}
				}
			}
		}
	case "javascript", "typescript":
		// function keyword or method definitions (ident followed by `(`).
		for i := 0; i < n-1; i++ {
			tok := tf.Tokens[i]
			if tok.Kind == TokKeyword && tok.Text == "function" {
				for j := i + 1; j < n; j++ {
					next := tf.Tokens[j]
					if next.Kind == TokIdent && next.OrigText != "" {
						defs = appendFuncDef(defs, tf, langName, j)
						break
					}
					if next.Kind == TokOperator && next.Text == "(" {
						break // anonymous function
					}
				}
			}
		}
	default:
		// For C, C++, Java, C#, Dart: heuristic — ident followed by `(` at function depth.
		for i := 0; i < n-1; i++ {
			tok := tf.Tokens[i]
			if tok.Kind == TokIdent && i+1 < n && tf.Tokens[i+1].Kind == TokOperator && tf.Tokens[i+1].Text == "(" {
				// Must be inside/at function scope boundary.
				if tf.InFunc != nil && i < len(tf.InFunc) && !tf.InFunc[i] {
					// Check if this token is at the boundary (the token before the func body).
					// Only include if the next matching { starts a new function body.
					// Heuristic: if token at position i is NOT inFunc but i+n tokens ARE, it's a def.
					// For simplicity, only take top-level idents followed by (.
					defs = appendFuncDef(defs, tf, langName, i)
				}
			}
		}
	}

	return defs
}
