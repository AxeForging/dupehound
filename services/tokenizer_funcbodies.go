package services

import (
	"slices"

	"github.com/AxeForging/dupehound/domain"
)

// Function-body detection per language family: which tokens are inside a function, and what it is called.

// markFunctionBodies returns a per-token boolean slice indicating whether each
// token is inside a function or method body. This is used to restrict clone
// detection to logic (functions/methods) and skip data declarations, imports,
// struct definitions, and other top-level boilerplate.
//
// Strategy per language family:
//   - Brace-based with func keywords (Go, Rust, Swift, etc.): scan for the
//     keyword, find the opening {, track depth to matching }.
//   - Brace-based without func keywords (Java, C, C++, C#, Dart): treat any
//     top-level brace block that contains statements as a potential function.
//     Specifically, any { at brace depth 0 or 1 (to handle class > method)
//     marks its contents as in-function.
//   - Python: scan for def keyword, mark from the colon through the next def
//     or dedent (approximated by next token at same or lesser line offset).
//   - Ruby/Elixir: scan for def, mark through matching end keyword.
//
// It also returns the collected function spans (token ranges + best-effort
// names) so detected clones can be attributed to the function they live in.
func markFunctionBodies(tokens []Token, lang *domain.Language) ([]bool, []FuncSpan) {
	n := len(tokens)
	inFunc := make([]bool, n)

	if lang == nil || n == 0 {
		// No language info — mark everything as in-function (no filtering).
		for i := range inFunc {
			inFunc[i] = true
		}
		return inFunc, nil
	}

	var funcs []FuncSpan
	switch lang.Name {
	case "javascript", "typescript":
		funcs = markJSFunctions(tokens, inFunc)
	case "python":
		funcs = markPythonFunctions(tokens, inFunc)
	case "ruby":
		funcs = markDefEndFunctions(tokens, inFunc, "def", "end")
	case "elixir":
		funcs = markDefEndFunctions(tokens, inFunc, "", "end")
	case "lua":
		funcs = markDefEndFunctions(tokens, inFunc, "function", "end")
	case "sql":
		funcs = markSQLFunctions(tokens, inFunc)
	default:
		funcs = markBraceFunctions(tokens, inFunc, lang)
	}

	return inFunc, funcs
}

// markBraceFunctions handles brace-based languages.
func markBraceFunctions(tokens []Token, inFunc []bool, lang *domain.Language) []FuncSpan {
	fkws := funcKeywords[lang.Name]
	hasFuncKeywords := len(fkws) > 0

	var funcs []FuncSpan
	n := len(tokens)
	if hasFuncKeywords {
		// Languages with explicit func keywords: find keyword, then opening {, then match }.
		for i := 0; i < n; i++ {
			t := tokens[i]
			if t.Kind != TokKeyword || !fkws[t.Text] {
				continue
			}
			// Find the opening brace after this keyword.
			braceIdx := -1
			for j := i + 1; j < n; j++ {
				if tokens[j].Kind == TokOperator && tokens[j].Text == "{" {
					braceIdx = j
					break
				}
				// Stop searching if we hit another function keyword or semicolon.
				if tokens[j].Kind == TokKeyword && fkws[tokens[j].Text] {
					break
				}
			}
			if braceIdx < 0 {
				continue
			}
			// R defines functions by assignment (`name <- function(…)`); every
			// other keyword language names them after the keyword.
			var name string
			if lang.Name == "r" {
				name = funcNameBeforeAssign(tokens, i)
			} else {
				name = funcNameAfterKeyword(tokens, i)
			}
			end := markBraceBody(tokens, inFunc, braceIdx)
			if end > braceIdx {
				funcs = append(funcs, FuncSpan{Start: braceIdx + 1, End: end, Name: name})
			}
		}
		if lang.Name == "shell" {
			funcs = append(funcs, markShellPosixFunctions(tokens, inFunc)...)
			slices.SortFunc(funcs, func(a, b FuncSpan) int { return a.Start - b.Start })
		}
	} else {
		// Languages without func keywords (Java, C, C++, C#, Dart):
		// Any brace block entered at depth 0 or 1 marks its contents.
		// depth 0 → top-level function (C) or class body (Java)
		// depth 1 → method inside a class
		//
		// Spans are recorded for every depth-0/1 brace block so that Java/C#
		// methods (depth 1, named via `…) {`) nest inside their class body
		// span (depth 0, anonymous); enclosingFuncName picks the innermost
		// NAMED span, which yields the method name.
		type openSpan struct {
			start int
			name  string
			depth int
		}
		var stack []openSpan
		depth := 0
		funcDepth := -1 // depth at which we entered the "function" block
		for i := 0; i < n; i++ {
			if tokens[i].Kind == TokOperator {
				switch tokens[i].Text {
				case "{":
					if depth <= 1 {
						stack = append(stack, openSpan{start: i + 1, name: cStyleFuncName(tokens, i), depth: depth})
						if funcDepth < 0 {
							funcDepth = depth
						}
					}
					depth++
				case "}":
					depth--
					if len(stack) > 0 && stack[len(stack)-1].depth == depth {
						top := stack[len(stack)-1]
						stack = stack[:len(stack)-1]
						if i-1 >= top.start {
							funcs = append(funcs, FuncSpan{Start: top.start, End: i - 1, Name: top.name})
						}
					}
					if depth == funcDepth {
						funcDepth = -1
					}
				}
			}
			if funcDepth >= 0 && depth > funcDepth {
				inFunc[i] = true
			}
		}
		// Unclosed blocks at EOF — keep what we marked.
		for _, s := range stack {
			if n-1 >= s.start {
				funcs = append(funcs, FuncSpan{Start: s.start, End: n - 1, Name: s.name})
			}
		}
	}
	return funcs
}

// markShellPosixFunctions marks the bodies of shell functions written in the
// portable `name() { … }` form. The bash-only `function name() {` spelling is
// already found through its keyword, so those are skipped here.
func markShellPosixFunctions(tokens []Token, inFunc []bool) []FuncSpan {
	isOp := func(i int, text string) bool {
		return tokens[i].Kind == TokOperator && tokens[i].Text == text
	}
	var funcs []FuncSpan
	for i := 0; i+3 < len(tokens); i++ {
		if tokens[i].Kind != TokIdent || !isOp(i+1, "(") || !isOp(i+2, ")") || !isOp(i+3, "{") {
			continue
		}
		if i > 0 && tokens[i-1].Kind == TokKeyword && tokens[i-1].Text == "function" {
			continue
		}
		if end := markBraceBody(tokens, inFunc, i+3); end > i+3 {
			funcs = append(funcs, FuncSpan{Start: i + 4, End: end, Name: tokens[i].OrigText})
			i = end
		}
	}
	return funcs
}

// jsControlKeywords are keywords that take a `(...)` and a `{ }` block but are
// NOT function definitions. Used to distinguish `if (x) {` from `compute(x) {`.
var jsControlKeywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true,
	"catch": true, "with": true,
}

// markJSFunctions marks JavaScript/TypeScript function and method bodies.
//
// Unlike the generic func-keyword scanner, JS/TS routinely define logic in
// forms that have no `function` keyword: class/object methods (`compute() {`),
// arrow functions (`(x) => {`), getters/setters, and constructors. Restricting
// detection to `function` bodies hid all of those from clone detection.
//
// A `{` opens a function body when:
//   - it is immediately preceded by `=>` (arrow function body), or
//   - it follows a `)` (optionally with a TS return-type annotation `): T` in
//     between) whose matching `(` is NOT introduced by a control-flow keyword
//     (if/for/while/switch/catch/with).
//
// Object literals (`= {`), class/interface bodies (`class C {`), and type
// aliases are not preceded by `)`/`=>`, so they remain excluded.
func markJSFunctions(tokens []Token, inFunc []bool) []FuncSpan {
	var funcs []FuncSpan
	n := len(tokens)
	for i := 0; i < n; i++ {
		t := tokens[i]
		if t.Kind != TokOperator {
			continue
		}
		bodyIdx := -1
		name := ""
		switch t.Text {
		case "=>":
			// Arrow function body: `=> {`.
			if i+1 < n && isOpenBrace(tokens[i+1]) {
				bodyIdx = i + 1
				name = jsArrowFuncName(tokens, i)
			}
		case ")":
			if b := jsFindBodyBrace(tokens, i); b >= 0 && jsParenIsFunctionParams(tokens, i) {
				bodyIdx = b
				name = jsFuncName(tokens, i)
			}
		}
		if bodyIdx >= 0 {
			end := markBraceBody(tokens, inFunc, bodyIdx)
			if end > bodyIdx {
				funcs = append(funcs, FuncSpan{Start: bodyIdx + 1, End: end, Name: name})
			}
		}
	}
	return funcs
}

// isOpenBrace reports whether tok is a `{` operator.
func isOpenBrace(tok Token) bool {
	return tok.Kind == TokOperator && tok.Text == "{"
}

// markBraceBody marks every token strictly inside the balanced `{ }` pair that
// opens at braceIdx as in-function. The braces themselves are not marked.
// Returns the index of the last marked token (braceIdx when the body is empty).
func markBraceBody(tokens []Token, inFunc []bool, braceIdx int) int {
	return markNestedBody(tokens, inFunc, braceIdx, func(t Token) int {
		if t.Kind != TokOperator {
			return 0
		}
		switch t.Text {
		case "{":
			return 1
		case "}":
			return -1
		}
		return 0
	})
}

// markNestedBody marks every token after openIdx as in-function until the
// nesting opened there closes, and returns the index of the last token marked
// (openIdx when the body is empty). nesting reports what a token does to the
// depth: +1 opens a level, -1 closes one, 0 leaves it alone. Brace bodies,
// SQL BEGIN/END and def/end blocks differ only in that function.
func markNestedBody(tokens []Token, inFunc []bool, openIdx int, nesting func(Token) int) int {
	depth := 1
	end := openIdx
	for j := openIdx + 1; j < len(tokens) && depth > 0; j++ {
		depth += nesting(tokens[j])
		if depth > 0 {
			inFunc[j] = true
			end = j
		}
	}
	return end
}

// jsFindBodyBrace returns the index of the `{` that opens the body following the
// `)` at closeParen, or -1 if no body brace follows. A TS return-type annotation
// (`): Foo`, `): Promise<T>`, `): A | B`, `): Foo[]`, …) between the `)` and the
// `{` is skipped. Returns -1 for signatures with no body (`foo(): void;`).
func jsFindBodyBrace(tokens []Token, closeParen int) int {
	n := len(tokens)
	j := closeParen + 1
	if j >= n {
		return -1
	}
	if tokens[j].Kind == TokOperator && tokens[j].Text == ":" {
		// Skip the return-type annotation up to the body brace.
		for j++; j < n; j++ {
			tk := tokens[j]
			if tk.Kind != TokOperator {
				continue
			}
			switch tk.Text {
			case "{":
				return j
			case ";", "=", "}", "=>":
				return -1
			}
		}
		return -1
	}
	if tokens[j].Kind == TokOperator && tokens[j].Text == "{" {
		return j
	}
	return -1
}

// jsParenIsFunctionParams reports whether the `)` at closeParen closes a function
// parameter list (as opposed to a control-flow condition such as `if (...)`). It
// finds the matching `(` and rejects it when introduced by a control keyword.
func jsParenIsFunctionParams(tokens []Token, closeParen int) bool {
	open := matchOpenParen(tokens, closeParen)
	if open < 0 {
		return false
	}
	if open == 0 {
		return true // `(...) {` at file start — treat as a function.
	}
	before := tokens[open-1]
	if before.Kind == TokKeyword && jsControlKeywords[before.Text] {
		return false
	}
	return true
}

// markPythonFunctions marks tokens after `def name(...):` as in-function.
// Since we don't have indentation info in the token stream, we mark from
// the colon after def through to the next def/class at the same scope or EOF.
func markPythonFunctions(tokens []Token, inFunc []bool) []FuncSpan {
	var funcs []FuncSpan
	n := len(tokens)
	i := 0
	for i < n {
		t := tokens[i]
		if t.Kind == TokKeyword && (t.Text == "def" || t.Text == "async") {
			// For async, check next token is def.
			start := i
			if t.Text == "async" {
				if i+1 < n && tokens[i+1].Kind == TokKeyword && tokens[i+1].Text == "def" {
					i++
				} else {
					i++
					continue
				}
			}
			name := funcNameAfterKeyword(tokens, i) // i points at `def`
			// Find the colon that ends the signature.
			colonIdx := -1
			for j := start; j < n; j++ {
				if tokens[j].Kind == TokOperator && tokens[j].Text == ":" {
					colonIdx = j
					break
				}
			}
			if colonIdx < 0 {
				i++
				continue
			}
			// Mark everything after the colon until next def/class at same or lesser indentation.
			end := colonIdx
			// resume is the index of the definition that ended this body, so the
			// outer scan can restart ON it. Restarting AFTER it would step over
			// the keyword and drop that function entirely — which is what used
			// to happen, leaving every def after the first one unmarked and its
			// duplication invisible.
			resume := -1
			for j := colonIdx + 1; j < n; j++ {
				// Stop at next top-level def or class (heuristic: if the def/class
				// is on a line that's <= the original def line's indent, we stop).
				// `async def` arrives as the async keyword first, so treat it as a
				// def-boundary too — otherwise the gap check below compares def
				// against the same-line async token and never fires.
				if tokens[j].Kind == TokKeyword &&
					(tokens[j].Text == "def" || tokens[j].Text == "class" ||
						(tokens[j].Text == "async" && j+1 < n && tokens[j+1].Kind == TokKeyword && tokens[j+1].Text == "def")) {
					// Same or earlier line offset means we've left the function.
					// Since we don't have indentation, use a simpler heuristic:
					// if there's a blank-line gap (line difference > 1 from previous token),
					// this might be a new top-level definition.
					if j > 0 && tokens[j].Line > tokens[j-1].Line+1 {
						resume = j
						break
					}
				}
				inFunc[j] = true
				end = j
			}
			if end > colonIdx {
				funcs = append(funcs, FuncSpan{Start: colonIdx + 1, End: end, Name: name})
			}
			if resume >= 0 {
				i = resume
			} else {
				// The body ran to the end of the file.
				i = n
			}
			continue
		}
		i++
	}
	return funcs
}

// markSQLFunctions marks tokens inside SQL FUNCTION/PROCEDURE bodies (BEGIN...END).
func markSQLFunctions(tokens []Token, inFunc []bool) []FuncSpan {
	var funcs []FuncSpan
	n := len(tokens)
	fkws := map[string]bool{
		"FUNCTION": true, "PROCEDURE": true,
		"function": true, "procedure": true,
	}
	for i := 0; i < n; i++ {
		if tokens[i].Kind != TokKeyword || !fkws[tokens[i].Text] {
			continue
		}
		// Find the BEGIN after the function keyword.
		beginIdx := -1
		for j := i + 1; j < n; j++ {
			if tokens[j].Kind == TokKeyword && (tokens[j].Text == "BEGIN" || tokens[j].Text == "begin") {
				beginIdx = j
				break
			}
		}
		if beginIdx < 0 {
			continue
		}
		name := funcNameAfterKeyword(tokens, i)
		end := markNestedBody(tokens, inFunc, beginIdx, func(t Token) int {
			if t.Kind != TokKeyword {
				return 0
			}
			switch t.Text {
			case "BEGIN", "begin":
				return 1
			case "END", "end":
				return -1
			}
			return 0
		})
		if end > beginIdx {
			funcs = append(funcs, FuncSpan{Start: beginIdx + 1, End: end, Name: name})
		}
	}
	return funcs
}

// markDefEndFunctions marks tokens between def and end keywords (Ruby, Elixir).
func markDefEndFunctions(tokens []Token, inFunc []bool, defKw, endKw string) []FuncSpan {
	defKeywords := map[string]bool{"def": true}
	if defKw != "" {
		defKeywords = map[string]bool{defKw: true}
	}
	// Elixir has multiple def-like keywords.
	if defKw == "" {
		defKeywords = map[string]bool{
			"def": true, "defp": true, "defmacro": true, "defmacrop": true,
		}
	}
	nestKeywords := map[string]bool{
		"do": true, "class": true, "module": true,
		"if": true, "case": true, "cond": true,
		"for": true, "while": true, "repeat": true,
	}
	var funcs []FuncSpan
	n := len(tokens)
	for i := 0; i < n; i++ {
		t := tokens[i]
		if t.Kind != TokKeyword || !defKeywords[t.Text] {
			continue
		}
		name := funcNameAfterKeyword(tokens, i)
		// Find matching end keyword, tracking nested def/end/do pairs.
		end := markNestedBody(tokens, inFunc, i, func(t Token) int {
			switch {
			case t.Kind != TokKeyword:
				return 0
			case defKeywords[t.Text] || nestKeywords[t.Text]:
				return 1
			case t.Text == endKw:
				return -1
			}
			return 0
		})
		if end > i {
			funcs = append(funcs, FuncSpan{Start: i + 1, End: end, Name: name})
		}
	}
	return funcs
}

// markLogic returns the per-token mask of executable code. See
// TokenizedFile.Logic.
func markLogic(tokens []Token, inFunc []bool, lang *domain.Language) []bool {
	if lang == nil || len(funcKeywords[lang.Name]) > 0 {
		return inFunc
	}
	if _, braceHeuristic := funcKeywords[lang.Name]; !braceHeuristic {
		return inFunc // indentation- or keyword-delimited languages
	}
	// A body is a `{ … }` block whose header ends in a parameter list:
	// `name(args) {`, `name(args) throws E {`, `name(args) const override {`,
	// a lambda `(args) -> {` / `(args) => {`. Class, struct, enum and
	// namespace blocks have no `)` in their header and stay outside.
	logic := make([]bool, len(tokens))
	var bodyDepths []int // brace depths at which a body block was opened
	depth := 0
	for i, tok := range tokens {
		if tok.Kind == TokOperator {
			switch tok.Text {
			case "{":
				if len(bodyDepths) == 0 && opensBody(tokens, i) {
					bodyDepths = append(bodyDepths, depth)
				}
				depth++
				continue
			case "}":
				depth--
				if n := len(bodyDepths); n > 0 && bodyDepths[n-1] == depth {
					bodyDepths = bodyDepths[:n-1]
					continue
				}
			}
		}
		logic[i] = len(bodyDepths) > 0 && inFunc[i]
	}
	return logic
}

// opensBody reports whether the `{` at braceIdx follows a parameter list,
// allowing the qualifiers that can stand between the two.
func opensBody(tokens []Token, braceIdx int) bool {
	for i := braceIdx - 1; i >= 0 && braceIdx-i <= 12; i-- {
		tok := tokens[i]
		if tok.Kind == TokOperator {
			switch tok.Text {
			case ")":
				return true
			case "->", "=>", ",", ".", "<", ">", "::", "[", "]", "?":
				continue // lambda arrow, `throws A, B`, qualified and generic names
			}
			return false
		}
		// identifiers and keywords: throws, const, noexcept, override, async, …
	}
	return false
}
