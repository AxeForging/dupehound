package services

import "github.com/AxeForging/dupehound/domain"

// Data-table detection: spans of composite literals, used to tell a table from duplicated logic.

// Data-literal syntax per language. A table-driven test is idiomatic well
// beyond Go, so the rule applies wherever a collection literal can be told from
// surrounding code by tokens alone. Each language is opted in only where that
// distinction is unambiguous; everything else is left out, because a wrong span
// silences findings while a missing one merely leaves noise.
//
// bracketDataLiteralLangs: `[` in expression position opens a list or array
// literal that closes at the matching `]` — Python lists of tuples, JS arrays of
// objects, Rust `vec![…]`, PHP and Elixir lists. An index expression (`m[k]`)
// uses the same bracket, so position is what separates them; see
// startsBracketLiteral.
//
// Java, C, C++, C# and Kotlin are absent on purpose: there `[` is virtually
// always an index or an array type, and their array initializers use `{`, which
// is indistinguishable from a block. Scala is absent because `[` is type
// parameters.
var bracketDataLiteralLangs = strSet(
	"python", "javascript", "typescript", "ruby", "rust", "php", "elixir", "swift", "dart",
)

// braceDataLiteralLangs: `{` is unambiguously a data constructor rather than a
// block, because these languages delimit blocks some other way — indentation in
// Python, `do`/`then` … `end` in Lua and Elixir.
//
// Ruby is absent: `{` there is either a hash literal or a block argument
// (`each { |x| … }`), and the two cannot be told apart by tokens alone.
var braceDataLiteralLangs = strSet("python", "lua", "elixir")

// goDataLiteralLang gates Go's own form, which is neither of the above: the
// literal is introduced by a TYPE prefix (`[]T{…}`, `map[K]V{…}`) and its body
// starts at the `{` that follows the type.
const goDataLiteralLang = "go"

// TokenSpan is an inclusive token index range.
type TokenSpan struct {
	Start int
	End   int
}

// findDataLiteralSpans locates composite DATA literals and returns their token
// spans: Go's `[]T{…}` / `map[K]V{…}` / `[]struct{…}{…}`, and the bracket- or
// brace-delimited collection literals of the other opted-in languages.
//
// Detection is restricted to function bodies so that data and config
// declarations never register as duplicated logic. That holds for anything
// declared at the top level, but a data table written INSIDE a function slipped
// through, and the rows of a table are structurally identical by construction —
// so idiomatic table-driven tests were reported as clones of themselves
// (issue #31).
//
// The spans are not excluded from detection. Excluding them would punch holes
// through surrounding code: a small inline `[]T{a, b}` in the middle of a
// genuinely duplicated block would split that block in two and lose the finding.
// The detector instead uses these spans to drop a clone whose every instance
// lies within ONE of them, which is the shape a data table produces and real
// duplication does not.
//
// Only outermost literals are returned; a literal nested in another is already
// covered by its parent's span.
func findDataLiteralSpans(tokens []Token, lang *domain.Language) []TokenSpan {
	if lang == nil {
		return nil
	}
	if lang.Name == goDataLiteralLang {
		return goDataLiteralSpans(tokens)
	}
	brackets := bracketDataLiteralLangs[lang.Name]
	braces := braceDataLiteralLangs[lang.Name]
	if !brackets && !braces {
		return nil
	}
	return delimitedDataLiteralSpans(tokens, brackets, braces)
}

// goDataLiteralSpans finds Go composite literals, which are introduced by a type
// prefix rather than by the brace itself.
//
// The scan is deliberately conservative: only literals carrying an explicit
// slice, array, or map type prefix are reported. A bare `T{…}` struct literal is
// left out, since it is far more often a single value than a data table.
func goDataLiteralSpans(tokens []Token) []TokenSpan {
	var spans []TokenSpan
	for i := 0; i < len(tokens); i++ {
		if !startsGoTypePrefix(tokens, i) {
			continue
		}
		braceIdx := goTypeEnd(tokens, i)
		if braceIdx < 0 || braceIdx >= len(tokens) || !isOpenBrace(tokens[braceIdx]) {
			continue
		}
		end := matchDelimiter(tokens, braceIdx, "{", "}")
		if end < 0 {
			continue
		}
		spans = append(spans, TokenSpan{Start: i, End: end})
		i = end
	}
	return spans
}

// delimitedDataLiteralSpans finds collection literals that their opening
// delimiter alone identifies: `[…]` in expression position, and — for languages
// that do not use braces for blocks — `{…}` anywhere.
func delimitedDataLiteralSpans(tokens []Token, brackets, braces bool) []TokenSpan {
	var spans []TokenSpan
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if t.Kind != TokOperator {
			continue
		}

		var end int
		switch {
		case brackets && t.Text == "[" && startsBracketLiteral(tokens, i):
			end = matchDelimiter(tokens, i, "[", "]")
		case braces && t.Text == "{":
			end = matchDelimiter(tokens, i, "{", "}")
		default:
			continue
		}
		if end < 0 {
			continue
		}

		spans = append(spans, TokenSpan{Start: i, End: end})
		i = end
	}
	return spans
}

// findFuncLiteralBodies returns the body spans of function literals, so the
// detector can tell logic that lives inside a table from the table's own rows.
//
// Go gets a dedicated scan because markFunctionBodies keys off the `func`
// keyword alone and cannot tell a struct field's func TYPE (`setup func()`) from
// an actual literal (`setup: func() { … }`). Other languages reuse the spans
// that pass already computed; callers narrow them to the ones nested inside a
// data literal.
func findFuncLiteralBodies(tokens []Token, lang *domain.Language, funcs []FuncSpan) []TokenSpan {
	if lang == nil {
		return nil
	}
	if lang.Name != goDataLiteralLang {
		spans := make([]TokenSpan, 0, len(funcs))
		for _, f := range funcs {
			if !isRealFunctionBody(tokens, f.Start) {
				continue
			}
			spans = append(spans, TokenSpan{Start: f.Start, End: f.End})
		}
		return spans
	}

	var spans []TokenSpan
	for i, t := range tokens {
		if t.Kind != TokKeyword || t.Text != "func" {
			continue
		}
		brace := goFuncLiteralBody(tokens, i)
		if brace < 0 {
			continue
		}
		end := matchDelimiter(tokens, brace, "{", "}")
		if end < 0 {
			continue
		}
		// The body only — the signature belongs to the row that declares it.
		spans = append(spans, TokenSpan{Start: brace + 1, End: end - 1})
	}
	return spans
}

// isRealFunctionBody reports whether the span starting at bodyStart is genuinely
// a function body, given the token that opens it.
//
// Languages without a `func` keyword (Dart, Java, C#, C, C++) get their bodies
// from a brace-depth heuristic that cannot tell a function body from a `{…}` map
// or array literal sitting at the same depth. Left unchecked, that reads every
// row of a Dart table as a function body and exempts the whole table from the
// data-literal rule.
//
// A brace-delimited body always follows a parameter list or an arrow; a data
// literal follows an assignment, a comma, or an opening bracket. Bodies that are
// not brace-delimited at all (Python's `:`, Ruby and Lua's `def … end`) are
// accepted as-is, since no literal shares that shape.
func isRealFunctionBody(tokens []Token, bodyStart int) bool {
	braceIdx := bodyStart - 1
	if braceIdx < 0 || braceIdx >= len(tokens) || !isOpenBrace(tokens[braceIdx]) {
		return true
	}
	prev := braceIdx - 1
	if prev < 0 {
		return false
	}
	if tokens[prev].Kind != TokOperator {
		// A keyword or identifier before the brace — `try {`, `else {`, or a
		// language whose bodies are named rather than parenthesized.
		return true
	}
	switch tokens[prev].Text {
	case ")", "=>", "->":
		return true
	}
	return false
}

// startsBracketLiteral reports whether the `[` at i opens a collection literal
// rather than an index or slice expression. Both spell the same bracket, so what
// precedes it decides: indexing always follows the thing being indexed — an
// identifier, a literal, or a closing bracket, brace, or paren.
func startsBracketLiteral(tokens []Token, i int) bool {
	if i == 0 {
		return true
	}
	prev := tokens[i-1]
	switch prev.Kind {
	case TokIdent, TokNumber, TokString:
		return false
	case TokOperator:
		switch prev.Text {
		case ")", "]", "}":
			return false
		}
	}
	return true
}

// startsGoTypePrefix reports whether tokens[i] can open the slice, array, or map
// type of a composite literal rather than an index expression. From the bracket
// onward `m[k] {` in `if m[k] { … }` is indistinguishable from a type, so the
// token BEFORE the bracket is what settles it: an index expression always
// follows the thing being indexed — an identifier, a literal, or a closing
// bracket, brace, or paren.
func startsGoTypePrefix(tokens []Token, i int) bool {
	t := tokens[i]
	if t.Kind == TokKeyword && t.Text == "map" {
		// `map` is reserved, so it can only begin a type.
		return true
	}
	if t.Kind != TokOperator || t.Text != "[" {
		return false
	}
	return startsBracketLiteral(tokens, i)
}

// goTypeEnd parses the Go type expression starting at tokens[i] and returns the
// index just past it, or -1 for anything this scan does not model. Only the
// forms that can precede a composite literal are handled; func and channel
// types return -1, which leaves their literals unmasked.
func goTypeEnd(tokens []Token, i int) int {
	n := len(tokens)
	if i >= n {
		return -1
	}
	t := tokens[i]
	switch {
	case t.Kind == TokOperator && t.Text == "[":
		// Slice `[]T`, array `[N]T`, or `[...]T` — whatever sits between the
		// brackets is a length expression we never need to interpret.
		closeIdx := matchDelimiter(tokens, i, "[", "]")
		if closeIdx < 0 {
			return -1
		}
		return goTypeEnd(tokens, closeIdx+1)

	case t.Kind == TokKeyword && t.Text == "map":
		if i+1 >= n || tokens[i+1].Kind != TokOperator || tokens[i+1].Text != "[" {
			return -1
		}
		closeIdx := matchDelimiter(tokens, i+1, "[", "]")
		if closeIdx < 0 {
			return -1
		}
		return goTypeEnd(tokens, closeIdx+1)

	case t.Kind == TokOperator && t.Text == "*":
		return goTypeEnd(tokens, i+1)

	case t.Kind == TokKeyword && (t.Text == "struct" || t.Text == "interface"):
		if i+1 >= n || !isOpenBrace(tokens[i+1]) {
			return -1
		}
		closeIdx := matchDelimiter(tokens, i+1, "{", "}")
		if closeIdx < 0 {
			return -1
		}
		return closeIdx + 1

	case t.Kind == TokIdent:
		j := i + 1
		// Qualified name: pkg.Type
		if j+1 < n && tokens[j].Kind == TokOperator && tokens[j].Text == "." && tokens[j+1].Kind == TokIdent {
			j += 2
		}
		// Generic instantiation: Type[int]
		if j < n && tokens[j].Kind == TokOperator && tokens[j].Text == "[" {
			closeIdx := matchDelimiter(tokens, j, "[", "]")
			if closeIdx < 0 {
				return -1
			}
			j = closeIdx + 1
		}
		return j
	}
	return -1
}

// goFuncLiteralBody returns the index of the body brace of the function literal
// whose `func` keyword sits at i, or -1 when that `func` opens a TYPE rather
// than a literal. Both forms appear inside a table: `setup func()` declares a
// struct field's type, while `setup: func() { … }` supplies a value. What
// follows the signature tells them apart — only a literal has a body.
func goFuncLiteralBody(tokens []Token, i int) int {
	n := len(tokens)
	if i+1 >= n || tokens[i+1].Kind != TokOperator || tokens[i+1].Text != "(" {
		return -1
	}
	closeParen := matchDelimiter(tokens, i+1, "(", ")")
	if closeParen < 0 {
		return -1
	}

	j := closeParen + 1
	if j >= n {
		return -1
	}
	switch {
	case isOpenBrace(tokens[j]):
		return j
	case tokens[j].Kind == TokOperator && tokens[j].Text == "(":
		// Parenthesized results: `func() (int, error) {`.
		closeResults := matchDelimiter(tokens, j, "(", ")")
		if closeResults < 0 {
			return -1
		}
		j = closeResults + 1
	default:
		// A single result type: `func() error {`.
		end := goTypeEnd(tokens, j)
		if end < 0 {
			return -1
		}
		j = end
	}

	if j < n && isOpenBrace(tokens[j]) {
		return j
	}
	return -1
}
