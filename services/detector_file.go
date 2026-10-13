package services

import "github.com/AxeForging/dupehound/domain"

// TokenizedFile: one lexed source file with the per-token masks detection reads.

// TokenizedFile holds the lexed representation of one source file.
// RawLines are not stored here to reduce memory; preview lines are
// loaded lazily from disk only for files that end up in detected clones.
type TokenizedFile struct {
	Path    string
	Tokens  []Token // normalized token sequence (no newlines)
	InFunc  []bool  // per-token: true if inside a function/method body
	Ignored []bool  // per-token: true if in a dupehound:ignore annotated block
	// Logic marks the tokens that are executable code: inside a function or
	// method body. For most languages that is InFunc. For the brace languages
	// without a function keyword (Java, C, C++, C#, Dart) InFunc also covers
	// class bodies, so Logic narrows it to blocks that follow a parameter list.
	// Whole-file detection matches loosely inside Logic and strictly outside.
	Logic []bool
	// Imports marks the tokens of import, include, use and package statements.
	// Whole-file detection leaves them out: every file opens with a block of
	// them, and nobody refactors two files for sharing their imports.
	Imports []bool
	Funcs   []FuncSpan // function body spans with best-effort names, for clone attribution
	// DataLiterals are the token spans of composite data literals written inside
	// function bodies — `[]T{…}`, `map[K]V{…}`, and the `[]struct{…}{…}` of a
	// table-driven test. They are not excluded from detection; they are used to
	// recognise a clone whose every instance sits inside ONE such literal, which
	// describes a data table rather than duplicated logic.
	DataLiterals []TokenSpan
	// FuncLiterals are the bodies of function literals. A body nested inside a
	// DataLiterals span is logic that merely lives in a table, so it is exempt
	// from the rule above.
	FuncLiterals []TokenSpan
}

// BuildTokenizedFileWithIgnore tokenizes a source file, applying inline suppression
// markers from both the source (dupehound:ignore comments) and the ignore rules.
func BuildTokenizedFileWithIgnore(path, content string, lang *domain.Language, rules []IgnoreRule) TokenizedFile {
	return buildTokenizedFile(path, TokenizeFileWithIgnore(content, lang), lang)
}

// buildTokenizedFile assembles a TokenizedFile from an already-lexed token
// stream. It is the single place the per-token masks and spans are derived, so
// the ignore-aware and plain constructors cannot drift apart.
func buildTokenizedFile(path string, tokens []Token, lang *domain.Language) TokenizedFile {
	inFunc, funcs := markFunctionBodies(tokens, lang)
	ignored := markIgnoredBlocks(tokens, inFunc)

	// Detection only ever looks at InFunc, so clearing it is how a suppression
	// takes effect.
	for i, ign := range ignored {
		if ign {
			inFunc[i] = false
		}
	}

	dataLiterals := findDataLiteralSpans(tokens, lang)

	return TokenizedFile{
		Path:         path,
		Tokens:       tokens,
		Imports:      markImports(tokens, inFunc, lang),
		Logic:        markLogic(tokens, inFunc, lang),
		InFunc:       inFunc,
		Ignored:      ignored,
		Funcs:        funcs,
		DataLiterals: dataLiterals,
		FuncLiterals: nestedFuncLiterals(findFuncLiteralBodies(tokens, lang, funcs), dataLiterals),
	}
}

// nestedFuncLiterals keeps only the function bodies that sit inside a data
// literal — the `run func(t *testing.T)` column of a table.
//
// The narrowing is what makes the exemption safe outside Go, where the spans
// come from markFunctionBodies and therefore include the enclosing test function
// itself. Left unfiltered, that outer span would cover every table in the file
// and exempt all of them, disabling the rule entirely.
func nestedFuncLiterals(funcSpans, dataLiterals []TokenSpan) []TokenSpan {
	if len(funcSpans) == 0 || len(dataLiterals) == 0 {
		return nil
	}
	nested := make([]TokenSpan, 0, len(funcSpans))
	for _, fs := range funcSpans {
		for _, dl := range dataLiterals {
			if fs.Start >= dl.Start && fs.End <= dl.End {
				nested = append(nested, fs)
				break
			}
		}
	}
	return nested
}

// dataLiteralAt returns the index of the data literal wholly containing the
// token window [pos, pos+length), or -1 when the window is not pure table data.
//
// Touching a function literal at all disqualifies the window. Logic written in
// a table — a `run func(t *testing.T)` column — is still logic, and a window
// that straddles a func body and the rows around it is not something the "this
// is just a data table" rule should ever silence.
func (tf TokenizedFile) dataLiteralAt(pos, length int) int {
	if len(tf.DataLiterals) == 0 || length <= 0 {
		return -1
	}
	end := pos + length - 1
	for _, fl := range tf.FuncLiterals {
		if pos <= fl.End && fl.Start <= end {
			return -1
		}
	}
	for i, s := range tf.DataLiterals {
		if pos >= s.Start && end <= s.End {
			return i
		}
	}
	return -1
}

// allInsideOneDataLiteral reports whether every instance of a clone group lands
// in the same composite data literal. The rows of a table are structurally
// identical by construction, so a "clone" confined to one literal describes a
// single data table rather than duplicated logic (issue #31). Instances spread
// across two literals, two functions, or two files are left alone — repeating
// the same table twice is duplication worth reporting.
func allInsideOneDataLiteral(files []TokenizedFile, starts []globalPos, totalTokens int) bool {
	first := starts[0]
	litIdx := files[first.FileIdx].dataLiteralAt(first.Pos, totalTokens)
	if litIdx < 0 {
		return false
	}
	for _, s := range starts[1:] {
		if s.FileIdx != first.FileIdx {
			return false
		}
		if files[s.FileIdx].dataLiteralAt(s.Pos, totalTokens) != litIdx {
			return false
		}
	}
	return true
}
