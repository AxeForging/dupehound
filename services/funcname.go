package services

// FuncSpan records the token-index range of one function/method body together
// with the best-effort name of the function it belongs to. Start and End are
// inclusive token indices of the body contents. Name is "" when the function
// is anonymous or its name could not be determined.
//
// Spans are collected by the same per-language markers that build the InFunc
// mask, so name attribution never disagrees with what the detector considers
// "inside a function".
type FuncSpan struct {
	Start int
	End   int
	Name  string
}

// enclosingFuncName returns the name of the innermost NAMED function span
// containing the token index pos, or "" when no named span contains it.
// Nested spans overlap; the one with the greatest Start is the innermost.
// Anonymous spans (callbacks, IIFEs, arrow bodies) are skipped so that a clone
// inside `users.map(u => …)` within `loadUsers` is attributed to "loadUsers".
func enclosingFuncName(spans []FuncSpan, pos int) string {
	name := ""
	best := -1
	for _, s := range spans {
		if s.Name != "" && s.Start <= pos && pos <= s.End && s.Start > best {
			best = s.Start
			name = s.Name
		}
	}
	return name
}

// funcNameAfterKeyword extracts the function name for keyword-introduced
// definitions: `func Name(`, `fn name(`, `def name(`, `function a.b:c(`.
// kwIdx is the token index of the introducing keyword. A parenthesized Go
// receiver (`func (r T) Name(`) is skipped. Qualified names are joined:
// Lua `function obj:method`, Ruby `def self.foo`, SQL `FUNCTION schema.proc`.
func funcNameAfterKeyword(tokens []Token, kwIdx int) string {
	n := len(tokens)
	j := kwIdx + 1
	afterReceiver := false
	// Skip a parenthesized receiver: `func (r T) Name(`.
	if j < n && tokens[j].Kind == TokOperator && tokens[j].Text == "(" {
		afterReceiver = true
		depth := 1
		for j++; j < n && depth > 0; j++ {
			if tokens[j].Kind == TokOperator {
				switch tokens[j].Text {
				case "(":
					depth++
				case ")":
					depth--
				}
			}
		}
	}
	if j >= n {
		return ""
	}
	var name string
	switch {
	case tokens[j].Kind == TokIdent:
		name = tokens[j].OrigText
	case tokens[j].Kind == TokKeyword && tokens[j].Text == "self":
		name = "self" // Ruby `def self.foo`
	default:
		return ""
	}
	for j+2 < n &&
		tokens[j+1].Kind == TokOperator && (tokens[j+1].Text == "." || tokens[j+1].Text == ":") &&
		tokens[j+2].Kind == TokIdent {
		name += tokens[j+1].Text + tokens[j+2].OrigText
		j += 2
	}
	// Disambiguate the receiver-skip path: for an ANONYMOUS Go func the first
	// paren group is the parameter list, so an identifier after it is a return
	// type (`func(x int) int {`), not a name. A real method name is always
	// followed by its parameter list `(` (or generics `[`).
	if afterReceiver {
		if j+1 >= n || tokens[j+1].Kind != TokOperator ||
			(tokens[j+1].Text != "(" && tokens[j+1].Text != "[") {
			return ""
		}
	}
	return name
}

// funcNameBeforeAssign extracts the name for assignment-style definitions like
// R's `name <- function(...)` (also `name = function(...)`). kwIdx is the
// index of the `function` keyword.
func funcNameBeforeAssign(tokens []Token, kwIdx int) string {
	if kwIdx >= 2 &&
		tokens[kwIdx-1].Kind == TokOperator && (tokens[kwIdx-1].Text == "<-" || tokens[kwIdx-1].Text == "=") &&
		tokens[kwIdx-2].Kind == TokIdent {
		return tokens[kwIdx-2].OrigText
	}
	return ""
}

// cStyleFuncName extracts a best-effort function name for a body brace at
// braceIdx in brace-heuristic languages (Java, C, C++, C#, Dart): walk back
// over signature trailers between `)` and `{` (`const`, `noexcept`,
// `throws A, B`, `override`, …), match the parameter list's `)` to its `(`,
// and take the identifier before it (`Foo::bar(...)` → "bar").
func cStyleFuncName(tokens []Token, braceIdx int) string {
	k := braceIdx - 1
	// Bounded walk-back over trailers so we never wander into unrelated code.
	for limit := 0; k >= 0 && limit < 8; limit++ {
		t := tokens[k]
		if t.Kind == TokIdent || t.Kind == TokKeyword ||
			(t.Kind == TokOperator && t.Text == ",") {
			k--
			continue
		}
		break
	}
	if k < 0 || tokens[k].Kind != TokOperator || tokens[k].Text != ")" {
		return ""
	}
	open := matchOpenParen(tokens, k)
	if open <= 0 {
		return ""
	}
	if tokens[open-1].Kind == TokIdent {
		return tokens[open-1].OrigText
	}
	return ""
}

// matchOpenParen returns the index of the `(` matching the `)` at closeParen,
// or -1 when unbalanced.
func matchOpenParen(tokens []Token, closeParen int) int {
	depth := 1
	for k := closeParen - 1; k >= 0; k-- {
		if tokens[k].Kind != TokOperator {
			continue
		}
		switch tokens[k].Text {
		case ")":
			depth++
		case "(":
			depth--
			if depth == 0 {
				return k
			}
		}
	}
	return -1
}

// jsFuncName extracts the name for a JS/TS function whose parameter list ends
// at the `)` at closeParen: `function name(`, method shorthand `compute() {`,
// and `const name = function(` assignments.
func jsFuncName(tokens []Token, closeParen int) string {
	open := matchOpenParen(tokens, closeParen)
	if open <= 0 {
		return ""
	}
	before := tokens[open-1]
	if before.Kind == TokIdent {
		return before.OrigText
	}
	if before.Kind == TokKeyword && before.Text == "function" {
		// `const name = function(` / `name: function(` — walk past the keyword.
		if open >= 3 &&
			tokens[open-2].Kind == TokOperator && (tokens[open-2].Text == "=" || tokens[open-2].Text == ":") &&
			tokens[open-3].Kind == TokIdent {
			return tokens[open-3].OrigText
		}
	}
	return ""
}

// jsArrowFuncName extracts the name an arrow function is assigned to:
// `const name = (a, b) => {`, `name: x => {`, including `async` arrows and a
// simple `const name: Type = (…) => {` annotation.
func jsArrowFuncName(tokens []Token, arrowIdx int) string {
	p := arrowIdx - 1
	if p < 0 {
		return ""
	}
	// Skip a simple TS return-type annotation between the params and the
	// arrow: `(req: Request): Response => {`.
	if tokens[p].Kind == TokIdent && p >= 2 &&
		tokens[p-1].Kind == TokOperator && tokens[p-1].Text == ":" &&
		tokens[p-2].Kind == TokOperator && tokens[p-2].Text == ")" {
		p -= 2
	}
	switch {
	case tokens[p].Kind == TokOperator && tokens[p].Text == ")":
		open := matchOpenParen(tokens, p)
		if open < 0 {
			return ""
		}
		p = open - 1
	case tokens[p].Kind == TokIdent:
		p-- // single-param arrow: `name = x => {`
	default:
		return ""
	}
	if p >= 0 && tokens[p].Kind == TokKeyword && tokens[p].Text == "async" {
		p--
	}
	if p < 1 || tokens[p].Kind != TokOperator || (tokens[p].Text != "=" && tokens[p].Text != ":") {
		return ""
	}
	if tokens[p-1].Kind == TokIdent {
		// `const name: SomeType = (…) => {` — hop over a simple type annotation.
		if tokens[p].Text == "=" && p >= 3 &&
			tokens[p-2].Kind == TokOperator && tokens[p-2].Text == ":" &&
			tokens[p-3].Kind == TokIdent {
			return tokens[p-3].OrigText
		}
		return tokens[p-1].OrigText
	}
	return ""
}
