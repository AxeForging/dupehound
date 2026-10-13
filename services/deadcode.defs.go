package services

import (
	"slices"
	"sort"
)

// Finding function definitions in a token stream, per language family.

// FunctionDef holds extracted function definition metadata.
type FunctionDef struct {
	Name   string
	File   string
	Line   int
	Lang   string
	DefTok int // token index of the function name
	KwTok  int // token index of the introducing keyword, or -1 when there is none
	// BodyStart and BodyEnd bound the function's body in tokens (inclusive), or
	// are both -1 when it is unknown. A reference from inside the body is the
	// function calling itself, which does not make it used.
	BodyStart, BodyEnd int
	Visibility         defVisibility
	// VisibilityKnown is set when the extractor already worked out Visibility
	// from the declaration's modifiers, so visibilityOf must not recompute it.
	VisibilityKnown bool
}

func (d FunctionDef) containsToken(i int) bool {
	return d.BodyStart >= 0 && i >= d.BodyStart && i <= d.BodyEnd
}

// appendFuncDef records the identifier at tf.Tokens[tokIdx] as a function
// definition. Centralizes the FunctionDef struct literal used by every
// per-language scanner in extractFunctionDefs.
func appendFuncDef(defs []FunctionDef, tf TokenizedFile, langName string, kwIdx, tokIdx int) []FunctionDef {
	tok := tf.Tokens[tokIdx]
	bodyStart, bodyEnd := bodySpanAfter(tf.Funcs, tokIdx)
	// The span must be this function's own: a declaration without a body, or an
	// arrow function whose body is a bare expression, ends at a `;` or `=>`
	// before any block opens, and whatever block comes next belongs to
	// something else.
	for k := tokIdx + 1; k < bodyStart-1; k++ {
		t := tf.Tokens[k]
		if t.Kind != TokOperator {
			continue
		}
		// A `}` before the span opens means this function's own (empty) body
		// already closed; `;` or a bare `=>` means it never had a block.
		if t.Text == "}" || ((t.Text == ";" || t.Text == "=>") && tf.Tokens[k+1].Text != "{") {
			bodyStart, bodyEnd = -1, -1
			break
		}
	}
	return append(defs, FunctionDef{
		Name:   tok.OrigText,
		File:   tf.Path,
		Line:   tok.Line,
		Lang:   langName,
		DefTok: tokIdx,
		KwTok:  kwIdx,

		BodyStart: bodyStart,
		BodyEnd:   bodyEnd,
	})
}

// extractFunctionDefs extracts function name + location from a TokenizedFile.
func extractFunctionDefs(tf TokenizedFile, langName string) []FunctionDef {
	var defs []FunctionDef
	n := len(tf.Tokens)
	// bodySpanAfter binary-searches the spans, so they must be in source order.
	byStart := func(a, b FuncSpan) int { return a.Start - b.Start }
	if !slices.IsSortedFunc(tf.Funcs, byStart) {
		tf.Funcs = slices.Clone(tf.Funcs)
		slices.SortFunc(tf.Funcs, byStart)
	}
	if langName == "python" || langName == "ruby" {
		// Python bodies are delimited by indentation, which the token stream
		// does not carry, and Ruby's `x if y` modifiers open no block; the
		// recorded spans are approximate in both. Without a trustworthy body
		// there is no telling a self-call from a real one.
		tf.Funcs = nil
	}
	switch langName {
	case "go", "rust", "kotlin", "swift", "scala", "php", "ruby", "lua", "python", "elixir", "javascript", "typescript":
		// Keyword-based: a func/fn/fun/def/function keyword followed by a name.
		funcKwSet := funcKeywords[langName]
		for i := 0; i < n-1; i++ {
			if tok := tf.Tokens[i]; tok.Kind != TokKeyword || !funcKwSet[tok.Text] {
				continue
			}
			if (langName == "javascript" || langName == "typescript") && isFunctionExpression(tf.Tokens, i) {
				continue
			}
			j := namedFuncIdent(tf.Tokens, i, langName == "go")
			if langName == "kotlin" {
				j = kotlinFuncIdent(tf.Tokens, i)
			}
			if j >= 0 {
				defs = appendFuncDef(defs, tf, langName, i, j)
			}
		}
		if langName == "javascript" || langName == "typescript" {
			defs = append(defs, jsAssignedFunctionDefs(tf, langName)...)
			defs = append(defs, jsClassMethodDefs(tf, langName)...)
			slices.SortFunc(defs, func(a, b FunctionDef) int { return a.DefTok - b.DefTok })
		}
	default:
		// C, C++, Java, C#, Dart: no function keyword to look for.
		defs = cFamilyFuncDefs(tf, langName)
	}

	return defs
}

// bodySpanAfter returns the body of the function whose name token is nameIdx:
// the first recorded function span that opens after it. Spans are produced in
// source order by markFunctionBodies.
func bodySpanAfter(funcs []FuncSpan, nameIdx int) (start, end int) {
	// `>=`: in the def/end languages a span opens at the name itself.
	i := sort.Search(len(funcs), func(i int) bool { return funcs[i].Start >= nameIdx })
	if i == len(funcs) {
		return -1, -1
	}
	return funcs[i].Start, funcs[i].End
}

// jsAssignedFunctionDefs finds module-level functions written as assignments:
// `const name = (…) => …`, `const name = async x => …`, `const name = function…`.
// Functions assigned inside another function are locals and are left alone.
func jsAssignedFunctionDefs(tf TokenizedFile, langName string) []FunctionDef {
	toks := tf.Tokens
	isOp := func(i int, text string) bool {
		return i < len(toks) && toks[i].Kind == TokOperator && toks[i].Text == text
	}
	isWord := func(i int, word string) bool {
		return i >= 0 && i < len(toks) && (toks[i].Text == word || toks[i].OrigText == word)
	}
	var defs []FunctionDef
	for i := 1; i+2 < len(toks); i++ {
		if toks[i].Kind != TokIdent || !isOp(i+1, "=") || tf.InFunc[i] {
			continue
		}
		if !isWord(i-1, "const") && !isWord(i-1, "let") && !isWord(i-1, "var") {
			continue
		}
		j := i + 2
		if isWord(j, "async") {
			j++
		}
		isFunc := isWord(j, "function")
		switch {
		case isFunc:
		case toks[j].Kind == TokIdent && isOp(j+1, "=>"):
			isFunc = true
		case isOp(j, "("):
			if closeIdx := matchDelimiter(toks, j, "(", ")"); closeIdx > 0 {
				// `) =>` or a TS return type `): T =>`.
				isFunc = isOp(closeIdx+1, "=>") || isOp(closeIdx+1, ":")
			}
		}
		if isFunc {
			defs = appendFuncDef(defs, tf, langName, i-1, i)
		}
	}
	return defs
}

// namedFuncIdent returns the index of the name introduced by the function
// keyword at kwIdx, or -1 when the keyword opens an anonymous function or a
// function type: `func(x int) bool {`, `fn(i32) -> i32`, `function () use ($x)`.
// With goReceiver set, one parenthesised receiver may precede the name.
func namedFuncIdent(tokens []Token, kwIdx int, goReceiver bool) int {
	isOp := func(i int, text string) bool {
		return i < len(tokens) && tokens[i].Kind == TokOperator && tokens[i].Text == text
	}
	j := kwIdx + 1
	if goReceiver {
		if isOp(j, "(") {
			if j = matchDelimiter(tokens, j, "(", ")") + 1; j == 0 {
				return -1
			}
		}
		// A Go declaration is `name(` or, with type parameters, `name[`.
		if j < len(tokens) && tokens[j].Kind == TokIdent && (isOp(j+1, "(") || isOp(j+1, "[")) {
			return j
		}
		return -1
	}
	for ; j < len(tokens); j++ {
		tok := tokens[j]
		if tok.Kind == TokIdent && tok.OrigText != "" {
			return j
		}
		if tok.Kind == TokOperator && (tok.Text == "(" || tok.Text == "{") {
			return -1
		}
	}
	return -1
}

// isFunctionExpression reports whether the `function` keyword at kwIdx is used
// as a value — assigned, passed, returned — rather than declared. A name on a
// function expression is visible only inside that function, so nothing else
// could ever call it.
func isFunctionExpression(tokens []Token, kwIdx int) bool {
	i := kwIdx - 1
	if i >= 0 && tokens[i].Text == "async" {
		i--
	}
	if i < 0 {
		return false
	}
	prev := tokens[i]
	if prev.Kind == TokOperator {
		switch prev.Text {
		case "=", "(", ",", ":", "?", "!", "&&", "||", "??", "=>", "[", "+", "-":
			return true
		}
		return false
	}
	switch prev.Text + prev.OrigText {
	case "return", "new", "typeof", "yield", "await", "void", "delete", "throw", "in", "of":
		return true
	}
	return false
}

// kotlinFuncIdent returns the index of the name declared by the `fun` at kwIdx.
// The name is the identifier right before the parameter list; what precedes it
// may be type parameters and an extension receiver: `fun <T> List<T>.second()`.
func kotlinFuncIdent(tokens []Token, kwIdx int) int {
	for j := kwIdx + 1; j < len(tokens) && j-kwIdx <= 40; j++ {
		if tokens[j].Kind != TokOperator {
			continue
		}
		switch tokens[j].Text {
		case "(":
			if tokens[j-1].Kind == TokIdent && j-1 > kwIdx {
				return j - 1
			}
			return -1 // anonymous: `fun(x: Int): Int { … }`
		case "{", "=", ";":
			return -1
		}
	}
	return -1
}
