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
func isFrameworkInvoked(tf TokenizedFile, def FunctionDef) bool {
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
		return isPythonDecorated(tf.Tokens, def.KwTok)
	case "ruby":
		return inTestFile && (strings.HasPrefix(name, "test_") || xunitHooks[name])
	case "php":
		return inTestFile && (strings.HasPrefix(name, "test") || xunitHooks[name])
	case "rust":
		return hasRustTestAttribute(tf.Tokens, def.KwTok)
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

// hasRustTestAttribute reports whether the `fn` at kwIdx carries an attribute
// that registers it with a test or benchmark harness: `#[test]`,
// `#[tokio::test]`, `#[bench]`, `#[rstest]`, and so on. Stacked attributes
// (`#[test] #[should_panic]`) are all inspected.
func hasRustTestAttribute(tokens []Token, kwIdx int) bool {
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
		if open < 1 || !isOp(open-1, "#") {
			return false
		}
		for k := open + 1; k < i; k++ {
			word := tokens[k].OrigText + tokens[k].Text
			if strings.Contains(word, "test") || strings.Contains(word, "bench") {
				return true
			}
		}
		i = open - 2
	}
	return false
}
