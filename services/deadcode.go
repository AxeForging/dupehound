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

// findDeadFunctions analyses all tokenized files and returns functions whose
// names do not appear in any token outside their own definition line.
// This is a heuristic — cross-package calls and reflection produce false positives.
func findDeadFunctions(files []TokenizedFile) []domain.DeadFunc {
	// Phase 1: extract all function definitions.
	var defs []FunctionDef
	for _, tf := range files {
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

	// Phase 2: build a set of all identifier tokens across all files.
	// Map: name → set of lines where it appears (file+line pairs).
	type fileLineKey struct {
		file string
		line int
	}
	nameUsages := make(map[string]map[fileLineKey]bool)
	for _, tf := range files {
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
					defs = append(defs, FunctionDef{
						Name:   next.OrigText,
						File:   tf.Path,
						Line:   next.Line,
						Lang:   langName,
						DefTok: j,
					})
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
						defs = append(defs, FunctionDef{
							Name:   next.OrigText,
							File:   tf.Path,
							Line:   next.Line,
							Lang:   langName,
							DefTok: j,
						})
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
						defs = append(defs, FunctionDef{
							Name:   next.OrigText,
							File:   tf.Path,
							Line:   next.Line,
							Lang:   langName,
							DefTok: j,
						})
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
					defs = append(defs, FunctionDef{
						Name:   tok.OrigText,
						File:   tf.Path,
						Line:   tok.Line,
						Lang:   langName,
						DefTok: i,
					})
				}
			}
		}
	}

	return defs
}
