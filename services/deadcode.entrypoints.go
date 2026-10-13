package services

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Dead-function detection asks "is this name mentioned anywhere else?". That
// question has no meaning for a function a runtime or framework calls on the
// program's behalf: nothing in the repository mentions `main`, a test function,
// or a route handler by name, yet none of them are dead. This file recognises
// those so they are never reported.
//
// Each rule is deliberately narrow — a test-shaped name only counts inside a
// test file, an attribute only when it names a test or benchmark — because a
// rule that is too generous hides real dead code.

// goTestFuncPrefixes are the name prefixes `go test` discovers.
var goTestFuncPrefixes = []string{"Test", "Benchmark", "Fuzz", "Example"}

// xunitHooks are the lifecycle methods an xUnit-style runner (unittest,
// minitest, PHPUnit) calls around each test.
var xunitHooks = strSet(
	"setUp", "tearDown", "setUpClass", "tearDownClass", "setUpModule", "tearDownModule",
	"setUpTestData", "asyncSetUp", "asyncTearDown", "setup", "teardown",
	"setUpBeforeClass", "tearDownAfterClass",
)

// isFrameworkInvoked reports whether def is called by a runtime, test runner or
// framework rather than by name from the scanned code.
func isFrameworkInvoked(tf TokenizedFile, def FunctionDef, classes map[string]bool) bool {
	name := def.Name
	if name == "main" {
		return true
	}
	inTestFile := isTestFile(tf.Path)

	switch def.Lang {
	case "go":
		if name == "init" {
			return true
		}
		if inTestFile && hasAnyPrefix(name, goTestFuncPrefixes) {
			return true
		}
		// An exported method can satisfy an interface declared anywhere,
		// including outside the scanned tree (fmt.Stringer, error, …).
		return isGoMethod(tf.Tokens, def.KwTok) && isExportedName(name)
	case "python":
		if strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__") {
			return true
		}
		if inTestFile && (strings.HasPrefix(name, "test") || xunitHooks[name]) {
			return true
		}
		// unittest's camelCase hooks are unmistakable wherever they appear.
		if xunitHooks[name] && name != strings.ToLower(name) {
			return true
		}
		return isPythonDecorated(tf.Tokens, def.KwTok) || overridesExternalBase(tf.Tokens, def, classes)
	case "ruby":
		return inTestFile && (strings.HasPrefix(name, "test_") || xunitHooks[name])
	case "php":
		return inTestFile && (strings.HasPrefix(name, "test") || xunitHooks[name])
	case "rust":
		// A leading underscore is Rust's own "intentionally unused" marker.
		return strings.HasPrefix(name, "_") ||
			hasRustRegisteringAttribute(tf.Tokens, def.KwTok) ||
			isRustTraitMethod(tf.Tokens, def.KwTok)
	}
	return false
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func isExportedName(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}

// isGoMethod reports whether the `func` keyword at kwIdx declares a method,
// i.e. is followed by a parenthesised receiver.
func isGoMethod(tokens []Token, kwIdx int) bool {
	next := kwIdx + 1
	return kwIdx >= 0 && next < len(tokens) &&
		tokens[next].Kind == TokOperator && tokens[next].Text == "("
}

// isPythonDecorated reports whether the `def` at kwIdx is decorated. A
// decorator hands the function to something else — a router, a fixture
// registry, a CLI — which then calls it without the name appearing again.
func isPythonDecorated(tokens []Token, kwIdx int) bool {
	if kwIdx <= 0 {
		return false
	}
	// Step back over `async` to the last token before the definition, then to
	// the first token of that token's line: a decorator line starts with `@`.
	i := kwIdx - 1
	if tokens[i].Kind == TokKeyword && tokens[i].Text == "async" && tokens[i].Line == tokens[kwIdx].Line {
		i--
	}
	if i < 0 || tokens[i].Line == tokens[kwIdx].Line {
		return false
	}
	for i > 0 && tokens[i-1].Line == tokens[i].Line {
		i--
	}
	return tokens[i].Kind == TokOperator && tokens[i].Text == "@"
}

// rustInertAttributes only affect compilation or diagnostics. Any other
// attribute may hand the function to something that calls it without naming it:
// a test harness (`#[test]`, `#[tokio::test]`, `#[bench]`), the linker
// (`#[no_mangle]`), a macro framework (`#[wasm_bindgen]`, `#[pyfunction]`, a web
// route). Listing the harmless ones is the safe direction: an attribute this
// list has never heard of keeps the function out of the report.
var rustInertAttributes = strSet(
	"inline", "allow", "warn", "deny", "expect", "forbid", "cfg", "cfg_attr", "doc",
	"must_use", "cold", "track_caller", "deprecated", "rustfmt", "clippy", "derive",
)

// hasRustRegisteringAttribute reports whether the `fn` at kwIdx carries an
// attribute outside rustInertAttributes. Stacked attributes are all inspected.
func hasRustRegisteringAttribute(tokens []Token, kwIdx int) bool {
	isOp := func(i int, text string) bool {
		return i >= 0 && tokens[i].Kind == TokOperator && tokens[i].Text == text
	}
	i := kwIdx - 1
	// Skip modifiers between the attributes and `fn`: pub, pub(crate), async,
	// unsafe, const, extern "C".
	for i >= 0 && !isOp(i, "]") && !isOp(i, "}") && !isOp(i, ";") && !isOp(i, "{") {
		i--
	}
	for isOp(i, "]") {
		open := i
		for depth := 0; open >= 0; open-- {
			if isOp(open, "]") {
				depth++
			} else if isOp(open, "[") {
				if depth--; depth == 0 {
					break
				}
			}
		}
		if open < 1 || !isOp(open-1, "#") || open+1 >= i {
			return false
		}
		if first := tokens[open+1]; !rustInertAttributes[first.OrigText+first.Text] {
			return true
		}
		i = open - 2
	}
	return false
}

// isRustTraitMethod reports whether the `fn` at kwIdx is declared in a `trait`
// block or an `impl Trait for Type` block. Both are reached through the trait —
// operators, `Display`, `Future::poll` — so their names need never appear at a
// call site.
func isRustTraitMethod(tokens []Token, kwIdx int) bool {
	// Check every enclosing block, innermost first: a method can sit inside a
	// macro invocation that is itself inside the trait.
	for at := kwIdx; ; {
		open := enclosingOpenBrace(tokens, at)
		if open < 0 {
			return false
		}
		// Read the block header backwards to the previous item.
		sawFor := false
	header:
		for i := open - 1; i >= 0; i-- {
			tok := tokens[i]
			if tok.Kind == TokOperator && (tok.Text == "}" || tok.Text == ";" || tok.Text == "{") {
				break
			}
			switch tok.Text + tok.OrigText {
			case "for":
				sawFor = true
			case "trait":
				return true
			case "impl":
				if sawFor {
					return true
				}
				break header
			}
		}
		at = open
	}
}

// enclosingOpenBrace returns the index of the `{` whose block contains the
// token at idx, or -1 at the top level.
func enclosingOpenBrace(tokens []Token, idx int) int {
	return matchOpening(tokens, idx, "{", "}")
}

// repoClassNames returns the names of the classes defined in files.
func repoClassNames(files []TokenizedFile) map[string]bool {
	names := make(map[string]bool)
	for _, tf := range files {
		for i := 0; i+1 < len(tf.Tokens); i++ {
			if tf.Tokens[i].Kind == TokKeyword && tf.Tokens[i].Text == "class" && tf.Tokens[i+1].Kind == TokIdent {
				names[tf.Tokens[i+1].OrigText] = true
			}
		}
	}
	return names
}

// overridesExternalBase reports whether the Python method def belongs to a
// class that inherits from something not defined in the scanned code. Such a
// method may be a hook the outside base class calls — `logging.Handler.emit`,
// `HTMLParser.handle_starttag`, `ast.NodeVisitor.visit_Name` — and nothing in
// the repository would mention it. A method on a class whose bases are all
// local is not exempt: if it overrides anything, the name appears on the base.
func overridesExternalBase(tokens []Token, def FunctionDef, classes map[string]bool) bool {
	// Only methods: the first parameter is self or cls.
	p := def.DefTok + 2
	if p >= len(tokens) || (tokens[p].OrigText != "self" && tokens[p].OrigText != "cls") {
		return false
	}
	// The enclosing class is the nearest `class` statement above.
	c := def.KwTok - 1
	for c >= 0 && (tokens[c].Kind != TokKeyword || tokens[c].Text != "class") {
		c--
	}
	if c < 0 || c+2 >= len(tokens) || tokens[c+2].Text != "(" {
		return false
	}
	end := matchDelimiter(tokens, c+2, "(", ")")
	for i := c + 3; i < end; i++ {
		tok := tokens[i]
		if tok.Kind != TokIdent {
			continue
		}
		next := tokens[i+1]
		if next.Kind == TokOperator && (next.Text == "." || next.Text == "=") {
			continue // a module qualifier, or a keyword argument such as metaclass=
		}
		if prev := tokens[i-1]; prev.Kind == TokOperator && prev.Text == "=" {
			continue // the value of a keyword argument
		}
		if tok.OrigText != "object" && !classes[tok.OrigText] {
			return true
		}
	}
	return false
}

// visibilityOf works out how far def can be called from.
func visibilityOf(tf TokenizedFile, def FunctionDef) defVisibility {
	if def.VisibilityKnown {
		return def.Visibility
	}
	toks := tf.Tokens
	switch def.Lang {
	case "go":
		if !isExportedName(def.Name) {
			return visDir
		}
	case "rust":
		// Private unless `pub` sits between the previous item and `fn`.
		for i := def.KwTok - 1; i >= 0; i-- {
			tok := toks[i]
			if tok.Kind == TokOperator && (tok.Text == "}" || tok.Text == ";" || tok.Text == "{" || tok.Text == "]") {
				break
			}
			if tok.Text+tok.OrigText == "pub" {
				return visRepo
			}
		}
		return visTree
	case "javascript", "typescript":
		if !isJSModule(toks) {
			return visRepo // a classic script: top-level functions are globals
		}
		for i := def.KwTok - 1; i >= 0 && toks[i].Line == toks[def.KwTok].Line; i-- {
			if w := toks[i].Text + toks[i].OrigText; w == "export" || w == "default" {
				return visRepo
			}
		}
		return visFile
	}
	return visRepo
}

// isJSModule reports whether a JS/TS file uses a module system. In a module a
// top-level function is private unless exported.
func isJSModule(tokens []Token) bool {
	for _, tok := range tokens {
		switch tok.Text + tok.OrigText {
		case "import", "export", "require", "module", "exports":
			return true
		}
	}
	return false
}
