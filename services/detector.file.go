package services

import (
	"os"
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

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
	// ObjectLiterals are object literals outside executable code (JS/TS locale
	// tables, config objects). Whole-file detection compares their values
	// exactly; see objectLiteralSpans.
	ObjectLiterals []TokenSpan
	// FuncLiterals are the bodies of function literals. A body nested inside a
	// DataLiterals span is logic that merely lives in a table, so it is exempt
	// from the rule above.
	FuncLiterals []TokenSpan

	// compact, when set, replaces Tokens and the per-token masks: the file is
	// held as an index sufficient for exact detection. See compacted.
	compact *compactIndex
}

// compactIndex is the low-memory form of a tokenized file: one window hash and
// one line number per token. Everything else exact detection needs about a
// clone — its token text, for classifying and fingerprinting it — is re-lexed
// from disk for the few files a clone actually touches.
type compactIndex struct {
	hashes []uint64
	lines  []int32
	// extraLines records, for the string tokens that span several lines, how
	// many lines beyond their first they cover.
	extraLines map[int32]int32
	lang       *domain.Language
}

// compacted returns the low-memory form of tf for a detection run with the
// given window size and scope. Function, data-literal and function-literal
// spans are kept; they are small and attribution needs them.
func (tf TokenizedFile) compacted(minTokens int, wholeFile bool, lang *domain.Language) TokenizedFile {
	if tf.compact != nil {
		return tf
	}
	idx := &compactIndex{
		hashes: windowHashes(tf, minTokens, wholeFile),
		lines:  make([]int32, len(tf.Tokens)),
		lang:   lang,
	}
	for i, tok := range tf.Tokens {
		idx.lines[i] = int32(tok.Line)
		if tok.Kind == TokString {
			if extra := strings.Count(tok.OrigText, "\n"); extra > 0 {
				if idx.extraLines == nil {
					idx.extraLines = make(map[int32]int32)
				}
				idx.extraLines[int32(i)] = int32(extra)
			}
		}
	}
	return TokenizedFile{
		Path: tf.Path, Funcs: tf.Funcs, DataLiterals: tf.DataLiterals, FuncLiterals: tf.FuncLiterals, compact: idx,
	}
}

// materialized returns tf with its tokens and masks present, re-lexing the file
// from disk when it is held in compact form. A file that can no longer be read
// comes back empty.
func (tf TokenizedFile) materialized() TokenizedFile {
	if tf.compact == nil {
		return tf
	}
	data, err := os.ReadFile(tf.Path)
	if err != nil {
		return TokenizedFile{Path: tf.Path}
	}
	return BuildTokenizedFileWithIgnore(tf.Path, string(data), tf.compact.lang, nil)
}

// numTokens is the number of tokens in the file, in either form.
func (tf TokenizedFile) numTokens() int {
	if tf.compact != nil {
		return len(tf.compact.lines)
	}
	return len(tf.Tokens)
}

// lineAt is the line the token at i starts on.
func (tf TokenizedFile) lineAt(i int) int {
	if tf.compact != nil {
		return int(tf.compact.lines[i])
	}
	return tf.Tokens[i].Line
}

// endLineAt is the line the token at i ends on: later than lineAt for a string
// literal that spans lines (heredocs, raw and triple-quoted strings).
func (tf TokenizedFile) endLineAt(i int) int {
	if tf.compact != nil {
		return int(tf.compact.lines[i] + tf.compact.extraLines[int32(i)])
	}
	line := tf.Tokens[i].Line
	if tok := tf.Tokens[i]; tok.Kind == TokString {
		line += strings.Count(tok.OrigText, "\n")
	}
	return line
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
	logic := markLogic(tokens, inFunc, lang)

	return TokenizedFile{
		Path:           path,
		Tokens:         tokens,
		Imports:        markImports(tokens, inFunc, lang),
		Logic:          logic,
		ObjectLiterals: objectLiteralSpans(tokens, logic, lang),
		InFunc:         inFunc,
		Ignored:        ignored,
		Funcs:          funcs,
		DataLiterals:   dataLiterals,
		FuncLiterals:   nestedFuncLiterals(findFuncLiteralBodies(tokens, lang, funcs), dataLiterals),
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
