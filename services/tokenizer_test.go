package services

import (
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

// goL/pyL/jsL are test helpers that return real Language definitions.
func goL() *domain.Language { return LangForName("go") }
func pyL() *domain.Language { return LangForName("python") }
func jsL() *domain.Language { return LangForName("javascript") }

// --- Identifier normalization ---

func TestTokenize_IdentNormalization_SameKindSequence(t *testing.T) {
	// result := compute() and output := compute() must produce identical kind sequences
	// because identifiers normalize to TokIdent regardless of name.
	a := TokenizeFile("result := compute()\n", goL())
	b := TokenizeFile("output := compute()\n", goL())
	if !kindsEqual(tokenKinds(a), tokenKinds(b)) {
		t.Errorf("type-2 clone not detected:\n  a kinds: %v\n  b kinds: %v", tokenKinds(a), tokenKinds(b))
	}
}

func TestTokenize_NumberNormalization(t *testing.T) {
	a := TokenizeFile("x = 42\n", pyL())
	b := TokenizeFile("x = 99\n", pyL())
	if !kindsEqual(tokenKinds(a), tokenKinds(b)) {
		t.Errorf("number normalization failed")
	}
}

func TestTokenize_StringNormalization(t *testing.T) {
	a := TokenizeFile(`s := "hello"`, goL())
	b := TokenizeFile(`s := "world"`, goL())
	if !kindsEqual(tokenKinds(a), tokenKinds(b)) {
		t.Errorf("string normalization failed")
	}
}

// --- Keywords vs identifiers ---

func TestTokenize_KeywordPreserved(t *testing.T) {
	// "return" must emit TokKeyword; "returnVal" must emit TokIdent.
	// They should produce DIFFERENT kind sequences.
	a := TokenizeFile("return x\n", goL())
	b := TokenizeFile("returnVal x\n", goL())
	if kindsEqual(tokenKinds(a), tokenKinds(b)) {
		t.Error("keyword and identifier produced same kind sequence — keywords not preserved")
	}
}

func TestTokenize_GoKeywordsAreKeywords(t *testing.T) {
	tokens := TokenizeFile("if x { return y }\n", goL())
	kwCount := 0
	for _, tk := range tokens {
		if tk.Kind == TokKeyword {
			kwCount++
		}
	}
	if kwCount < 2 {
		t.Errorf("expected at least 2 keywords (if, return), got %d", kwCount)
	}
}

func TestTokenize_PythonKeywordsAreKeywords(t *testing.T) {
	tokens := TokenizeFile("def foo():\n    return bar\n", pyL())
	kwCount := 0
	for _, tk := range tokens {
		if tk.Kind == TokKeyword {
			kwCount++
		}
	}
	if kwCount < 2 {
		t.Errorf("expected at least 2 keywords (def, return), got %d", kwCount)
	}
}

// --- Comment stripping ---

func TestTokenize_LineCommentStripped(t *testing.T) {
	withComment := TokenizeFile("x := 1 // set x\n", goL())
	withoutComment := TokenizeFile("x := 1\n", goL())
	if !kindsEqual(tokenKinds(withComment), tokenKinds(withoutComment)) {
		t.Errorf("line comment not stripped:\n  with: %v\n  without: %v",
			tokenKinds(withComment), tokenKinds(withoutComment))
	}
}

func TestTokenize_BlockCommentStripped(t *testing.T) {
	a := TokenizeFile("x := /* magic */ 1\n", goL())
	b := TokenizeFile("x := 1\n", goL())
	if !kindsEqual(tokenKinds(a), tokenKinds(b)) {
		t.Errorf("inline block comment not stripped")
	}
}

func TestTokenize_MultilineBlockCommentStripped(t *testing.T) {
	src := "x := 1\n/*\n  doc comment\n  spanning lines\n*/\ny := 2\n"
	tokens := TokenizeFile(src, goL())
	// Should only see tokens for x:=1 and y:=2
	identCount := 0
	for _, tk := range tokens {
		if tk.Kind == TokIdent {
			identCount++
		}
	}
	if identCount != 2 {
		t.Errorf("expected 2 identifiers (x, y), got %d", identCount)
	}
}

func TestTokenize_PythonHashCommentStripped(t *testing.T) {
	a := TokenizeFile("x = 1 # set x\n", pyL())
	b := TokenizeFile("x = 1\n", pyL())
	if !kindsEqual(tokenKinds(a), tokenKinds(b)) {
		t.Errorf("python # comment not stripped")
	}
}

// --- Line numbers ---

func TestTokenize_LineNumbersCorrect(t *testing.T) {
	src := "// comment\nfoo()\n\nbar()\n"
	tokens := TokenizeFile(src, goL())
	if len(tokens) == 0 {
		t.Fatal("expected tokens, got none")
	}
	// First token should be on line 2 (after the comment)
	if tokens[0].Line != 2 {
		t.Errorf("first token: want line 2, got %d", tokens[0].Line)
	}
}

func TestTokenize_LineNumbersAfterBlockComment(t *testing.T) {
	src := "a\n/*\ncomment\nspanning\n*/\nb\n"
	tokens := TokenizeFile(src, goL())
	if len(tokens) < 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	if tokens[0].Line != 1 {
		t.Errorf("first token: want line 1, got %d", tokens[0].Line)
	}
	if tokens[1].Line != 6 {
		t.Errorf("second token (b): want line 6, got %d", tokens[1].Line)
	}
}

// --- Edge cases ---

func TestTokenize_EmptyFile(t *testing.T) {
	tokens := TokenizeFile("", goL())
	if len(tokens) != 0 {
		t.Errorf("empty file: expected 0 tokens, got %d", len(tokens))
	}
}

func TestTokenize_OnlyComments(t *testing.T) {
	// Use Go (// and /* */) and Python (# only) separately.
	goSrc := "// line comment\n/* block comment */\n"
	tokens := TokenizeFile(goSrc, goL())
	if len(tokens) != 0 {
		t.Errorf("go comment-only: expected 0 tokens, got %d: %v", len(tokens), tokens)
	}

	pySrc := "# hash comment\n# another\n"
	tokens = TokenizeFile(pySrc, pyL())
	if len(tokens) != 0 {
		t.Errorf("python comment-only: expected 0 tokens, got %d: %v", len(tokens), tokens)
	}
}

func TestTokenize_BacktickString(t *testing.T) {
	src := "s := `raw\nstring`\n"
	tokens := TokenizeFile(src, goL())
	found := false
	for _, tk := range tokens {
		if tk.Kind == TokString {
			found = true
		}
	}
	if !found {
		t.Error("backtick string not tokenized as TokString")
	}
}

func TestTokenize_PythonTripleQuote(t *testing.T) {
	src := `def foo():
    """
    This is a
    multiline docstring
    """
    return 1
`
	tokens := TokenizeFile(src, pyL())
	// Should have tokens for def, ID, (, ), :, return, NUM
	// Triple-quoted string becomes one TokString
	strCount := 0
	for _, tk := range tokens {
		if tk.Kind == TokString {
			strCount++
		}
	}
	if strCount != 1 {
		t.Errorf("expected 1 TokString for triple-quoted, got %d", strCount)
	}
}

// --- Operators ---

func TestTokenize_TwoCharOperator(t *testing.T) {
	src := "x := 1\n"
	tokens := TokenizeFile(src, goL())
	// Should have: ID, OP(:=), NUM — 3 tokens
	if len(tokens) != 3 {
		t.Errorf("expected 3 tokens for 'x := 1', got %d: %v", len(tokens), tokens)
	}
	if tokens[1].Kind != TokOperator {
		t.Errorf("token[1] should be TokOperator, got %v", tokens[1].Kind)
	}
}

// --- JavaScript ---

func TestTokenize_JavaScript(t *testing.T) {
	a := TokenizeFile("let result = compute();\n", jsL())
	b := TokenizeFile("let output = compute();\n", jsL())
	// "let" is a keyword, compute is ident, result/output are idents
	// Both should produce same kind sequence
	if !kindsEqual(tokenKinds(a), tokenKinds(b)) {
		t.Errorf("JS type-2 clone not detected:\n  a: %v\n  b: %v", tokenKinds(a), tokenKinds(b))
	}
}

func TestTokenize_JavaScript_TemplateString(t *testing.T) {
	src := "const s = `hello ${name}`;\n"
	tokens := TokenizeFile(src, jsL())
	found := false
	for _, tk := range tokens {
		if tk.Kind == TokString {
			found = true
		}
	}
	if !found {
		t.Error("template string not tokenized as TokString")
	}
}

// --- CRLF line endings ---

func TestTokenize_CRLFLineEndings(t *testing.T) {
	// Windows CRLF should tokenize identically to Unix LF.
	crlf := TokenizeFile("x := 1\r\ny := 2\r\n", goL())
	lf := TokenizeFile("x := 1\ny := 2\n", goL())
	if !kindsEqual(tokenKinds(crlf), tokenKinds(lf)) {
		t.Errorf("CRLF and LF produced different token sequences: crlf=%v lf=%v", tokenKinds(crlf), tokenKinds(lf))
	}
	if len(crlf) == 0 {
		t.Fatal("expected tokens, got none")
	}
}

// --- No trailing newline ---

func TestTokenize_NoTrailingNewline(t *testing.T) {
	// File without trailing \n should still tokenize correctly.
	tokens := TokenizeFile("x := 1", goL())
	if len(tokens) != 3 {
		t.Errorf("expected 3 tokens for 'x := 1' (no newline), got %d: %v", len(tokens), tokens)
	}
}

// --- Unterminated string literal ---

func TestTokenize_UnterminatedString_NoPanic(t *testing.T) {
	// An unterminated string should not panic — just emit TokString and stop.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic on unterminated string: %v", r)
		}
	}()
	tokens := TokenizeFile(`x := "unterminated`, goL())
	found := false
	for _, tk := range tokens {
		if tk.Kind == TokString {
			found = true
		}
	}
	if !found {
		t.Error("expected a TokString token for unterminated string literal")
	}
}

// --- Language-specific comments ---

func TestTokenize_SQL_DashDashCommentStripped(t *testing.T) {
	sql := LangForName("sql")
	a := TokenizeFile("SELECT id FROM users -- fetch all\n", sql)
	b := TokenizeFile("SELECT id FROM users\n", sql)
	if !kindsEqual(tokenKinds(a), tokenKinds(b)) {
		t.Errorf("SQL -- comment not stripped:\n  with: %v\n  without: %v", tokenKinds(a), tokenKinds(b))
	}
}

func TestTokenize_PHP_HashCommentStripped(t *testing.T) {
	php := LangForName("php")
	a := TokenizeFile("echo 1; # set it\n", php)
	b := TokenizeFile("echo 1;\n", php)
	if !kindsEqual(tokenKinds(a), tokenKinds(b)) {
		t.Errorf("PHP # comment not stripped:\n  with: %v\n  without: %v", tokenKinds(a), tokenKinds(b))
	}
}

func TestTokenize_Lua_BlockCommentStripped(t *testing.T) {
	lua := LangForName("lua")
	a := TokenizeFile("x = 1\n--[[ block\ncomment\n]]\ny = 2\n", lua)
	b := TokenizeFile("x = 1\ny = 2\n", lua)
	if !kindsEqual(tokenKinds(a), tokenKinds(b)) {
		t.Errorf("Lua --[[]] block comment not stripped:\n  with: %v\n  without: %v", tokenKinds(a), tokenKinds(b))
	}
}

// --- Numeric literal variants ---

func TestTokenize_HexLiteralNormalized(t *testing.T) {
	// 0xFF and 42 should both produce TokNumber — names/values don't matter.
	a := TokenizeFile("x := 42\n", goL())
	b := TokenizeFile("x := 0xFF\n", goL())
	if !kindsEqual(tokenKinds(a), tokenKinds(b)) {
		t.Errorf("hex literal not normalized to same kind as decimal: %v vs %v", tokenKinds(a), tokenKinds(b))
	}
}

func TestTokenize_FloatLiteralNormalized(t *testing.T) {
	a := TokenizeFile("x := 42\n", goL())
	b := TokenizeFile("x := 3.14\n", goL())
	if !kindsEqual(tokenKinds(a), tokenKinds(b)) {
		t.Errorf("float literal not normalized to same kind as integer: %v vs %v", tokenKinds(a), tokenKinds(b))
	}
}

// --- Three-character operators ---

func TestTokenize_ThreeCharOp_StrictEqual(t *testing.T) {
	// === must be emitted as one TokOperator, not == then =.
	tokens := TokenizeFile("a === b\n", jsL())
	var opTexts []string
	for _, tk := range tokens {
		if tk.Kind == TokOperator {
			opTexts = append(opTexts, tk.Text)
		}
	}
	if len(opTexts) != 1 {
		t.Errorf("expected 1 operator for '===', got %d: %v", len(opTexts), opTexts)
	} else if opTexts[0] != "===" {
		t.Errorf("expected operator text '===', got %q", opTexts[0])
	}
}

func TestTokenize_ThreeCharOp_StrictNotEqual(t *testing.T) {
	tokens := TokenizeFile("a !== b\n", jsL())
	var opTexts []string
	for _, tk := range tokens {
		if tk.Kind == TokOperator {
			opTexts = append(opTexts, tk.Text)
		}
	}
	if len(opTexts) != 1 {
		t.Errorf("expected 1 operator for '!==', got %d: %v", len(opTexts), opTexts)
	} else if opTexts[0] != "!==" {
		t.Errorf("expected operator text '!==', got %q", opTexts[0])
	}
}

// --- Cross-language structural equivalence ---

func TestTokenize_CrossLangStructural(t *testing.T) {
	// Go and JS have different syntax, but similar patterns should
	// have different kind sequences due to language-specific keywords.
	goSrc := TokenizeFile("func compute(x int) int { return x * 2 }\n", goL())
	jsSrc := TokenizeFile("function compute(x) { return x * 2; }\n", jsL())
	// Both "func"/"function" are keywords, but different keyword texts
	// means the sequences are different (correct behavior)
	goKws := 0
	jsKws := 0
	for _, tk := range goSrc {
		if tk.Kind == TokKeyword {
			goKws++
		}
	}
	for _, tk := range jsSrc {
		if tk.Kind == TokKeyword {
			jsKws++
		}
	}
	if goKws == 0 || jsKws == 0 {
		t.Errorf("expected keywords in both: go=%d js=%d", goKws, jsKws)
	}
}
