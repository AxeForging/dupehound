package services

import (
	"path/filepath"
	"strings"
)

// Function definitions in languages that have no function keyword (Java, C,
// C++, C#, Dart), and class members in JS/TS. A definition here is recognised
// by shape — `name(params) {` outside executable code — so each rule below says
// what else has that shape and how it is told apart.

// declBoundaryWords can never be the token before a definition's name: what
// follows them is a call, an instantiation or a type declaration.
var declBoundaryWords = strSet("new", "record", "class", "return", "throw", "case", "else", "typeof", "sizeof")

// callContextOps precede a call or an initializer, never a definition's name.
var callContextOps = strSet(":", ",", ".", "=", "(", "@", "->", "!", "&&", "||", "?", "+", "-", "<<", "[")

// headerExts are C and C++ header extensions.
var headerExts = strSet(".h", ".hh", ".hpp", ".hxx", ".inc")

// javaSerializationHooks are private methods the JVM calls during
// serialization; no source ever names them.
var javaSerializationHooks = strSet("writeObject", "readObject", "readObjectNoData", "writeReplace", "readResolve")

// cFamilyFuncDefs finds function and method definitions for the brace
// languages without a function keyword.
func cFamilyFuncDefs(tf TokenizedFile, langName string) []FunctionDef {
	toks := tf.Tokens
	// Seen through a header or assembled with `##`, a static function can be
	// used without its name appearing in its own file.
	isHeader := headerExts[strings.ToLower(filepath.Ext(tf.Path))] || pastesTokens(toks)
	var defs []FunctionDef
	for i := 0; i+1 < len(toks); i++ {
		if toks[i].Kind != TokIdent || !isOpText(toks, i+1, "(") || tf.Logic[i] {
			continue
		}
		if i > 0 {
			prev := toks[i-1]
			if (prev.Kind == TokOperator && callContextOps[prev.Text]) || declBoundaryWords[prev.Text+prev.OrigText] {
				continue
			}
		}
		closeParen := matchDelimiter(toks, i+1, "(", ")")
		if closeParen < 0 {
			continue
		}
		brace := bodyBraceAfter(toks, closeParen)
		if brace < 0 {
			continue // a prototype, an abstract method, a macro call
		}
		start := declStart(toks, i)
		if isDispatchedMember(toks, start, i, closeParen, brace, langName) {
			continue
		}
		defs = appendFuncDef(defs, tf, langName, -1, i)
		d := &defs[len(defs)-1]
		d.Visibility, d.VisibilityKnown = memberVisibility(toks, start, i, langName, isHeader), true
	}
	return defs
}

// qualifiersWithArgs may follow a parameter list and take arguments of their own.
var qualifiersWithArgs = strSet("noexcept", "throw", "requires", "alignas", "__attribute__", "__declspec")

// bodyBraceAfter returns the `{` opening the body of a definition whose
// parameter list closes at closeParen, or -1 when the declaration ends first.
// Qualifiers may sit in between: `throws E`, `const noexcept`, `-> T`, a C++
// constructor's initializer list.
func bodyBraceAfter(toks []Token, closeParen int) int {
	inInitializers := false
	for j := closeParen + 1; j < len(toks) && j-closeParen <= 60; j++ {
		if toks[j].Kind != TokOperator {
			continue
		}
		switch toks[j].Text {
		case "{":
			return j
		case ";", "=", "}":
			return -1
		case ":":
			inInitializers = true
		case "(":
			// A parenthesised group is a C++ member initializer (after the
			// `:`) or the argument of a qualifier (`noexcept(…)`). Anywhere
			// else it is the parameter list of the real function, and the
			// `name(…)` we started from was an attribute macro before it.
			prev := toks[j-1].Text + toks[j-1].OrigText
			if !inInitializers && !qualifiersWithArgs[prev] {
				return -1
			}
			if end := matchDelimiter(toks, j, "(", ")"); end > 0 {
				j = end
			}
		}
	}
	return -1
}

// declStart returns the index of the first token of the declaration whose name
// is at nameIdx: everything back to the previous `;`, `{` or `}`, stepping over
// bracketed groups so that `@Component({ … })` or `[Theory(Skip = "…")]` do not
// end the walk early.
func declStart(toks []Token, nameIdx int) int {
	i := nameIdx - 1
	for i >= 0 {
		tok := toks[i]
		if tok.Kind == TokOperator {
			switch tok.Text {
			case "#":
				// A preprocessor line (`#define LOCAL static`, `#if …`) is
				// not part of the declaration below it.
				if i == 0 || toks[i-1].Line < tok.Line {
					for i < nameIdx && toks[i].Line == tok.Line {
						i++
					}
					return i
				}
			case ";", "{", "}":
				return i + 1
			case ")":
				if open := matchOpenParen(toks, i); open >= 0 {
					i = open
				}
			case "]":
				if open := matchOpenBracket(toks, i); open >= 0 {
					i = open
				}
			}
		}
		i--
	}
	return 0
}

// matchOpenBracket returns the index of the `[` matching the `]` at closeIdx.
func matchOpenBracket(toks []Token, closeIdx int) int {
	depth := 1
	for k := closeIdx - 1; k >= 0; k-- {
		if toks[k].Kind != TokOperator {
			continue
		}
		switch toks[k].Text {
		case "]":
			depth++
		case "[":
			if depth--; depth == 0 {
				return k
			}
		}
	}
	return -1
}

// isDispatchedMember reports whether a member is reached without its name
// being written at a call site: an annotated method (`@Test`, `@Override`,
// `@Bean`), a C# method under an attribute (`[Fact]`), an override or a
// virtual method.
func isDispatchedMember(toks []Token, start, nameIdx, closeParen, brace int, langName string) bool {
	if langName == "csharp" && toks[nameIdx].OrigText == "Main" {
		return true
	}
	if langName == "java" && javaSerializationHooks[toks[nameIdx].OrigText] {
		return true
	}
	if langName == "csharp" && isOpText(toks, start, "[") {
		return true
	}
	for i := start; i < nameIdx; i++ {
		switch toks[i].Text + toks[i].OrigText {
		case "@", "override", "virtual", "abstract":
			return true
		}
	}
	for i := closeParen + 1; i < brace; i++ {
		if w := toks[i].Text + toks[i].OrigText; w == "override" || w == "final" {
			return true
		}
	}
	return false
}

// memberVisibility works out how far a member can be called from, by its
// modifiers.
func memberVisibility(toks []Token, start, nameIdx int, langName string, isHeaderFile bool) defVisibility {
	has := func(word string) bool {
		for i := start; i < nameIdx; i++ {
			if toks[i].Text+toks[i].OrigText == word {
				return true
			}
		}
		return false
	}
	switch langName {
	case "java":
		if has("private") {
			return visFile // nested classes share the file
		}
	case "csharp":
		if has("private") {
			return visDir // a partial class may continue in a sibling file
		}
	case "c", "cpp":
		// `static` confines a function to its translation unit — which, for a
		// header, is every file that includes it.
		if has("static") && !isHeaderFile {
			return visFile
		}
	case "dart":
		if len(toks[nameIdx].OrigText) > 0 && toks[nameIdx].OrigText[0] == '_' {
			return visDir // library-private; `part` files sit alongside
		}
	}
	return visRepo
}

func isOpText(toks []Token, i int, text string) bool {
	return i >= 0 && i < len(toks) && toks[i].Kind == TokOperator && toks[i].Text == text
}

// jsMemberModifiers may stand between a decorator and a member's name.
var jsMemberModifiers = strSet("public", "private", "protected", "static", "async", "readonly", "override", "abstract", "declare", "*", "#")

// jsClassMethodDefs finds the methods and function-valued fields of JS/TS
// classes.
//
// Only classes that neither extend nor implement anything are considered: a
// method on a derived class may be a hook its base calls (`render`,
// `ngOnInit`), and nothing in the file would say so. Decorated members
// (`@Get()`), getters, setters and the constructor are left out for the same
// reason — something other than a call by name reaches them.
func jsClassMethodDefs(tf TokenizedFile, langName string) []FunctionDef {
	toks := tf.Tokens
	word := func(i int) string {
		if i < 0 || i >= len(toks) {
			return ""
		}
		return toks[i].Text + toks[i].OrigText
	}
	var defs []FunctionDef
	for i := 0; i < len(toks); i++ {
		if word(i) != "class" {
			continue
		}
		open, derived := -1, false
		for j := i + 1; j < len(toks) && j-i < 80; j++ {
			if w := word(j); w == "extends" || w == "implements" {
				derived = true
			} else if w == "{" {
				open = j
				break
			} else if w == ";" || w == "(" {
				break
			}
		}
		if open < 0 {
			continue
		}
		closeIdx := matchDelimiter(toks, open, "{", "}")
		if closeIdx < 0 {
			break
		}
		if !derived {
			defs = appendJSMembers(defs, tf, langName, open, closeIdx)
		}
		i = open // nested classes are found by the outer loop
	}
	return defs
}

// appendJSMembers appends the reportable members declared directly in the
// class body spanning (open, closeIdx).
func appendJSMembers(defs []FunctionDef, tf TokenizedFile, langName string, open, closeIdx int) []FunctionDef {
	toks := tf.Tokens
	word := func(i int) string { return toks[i].Text + toks[i].OrigText }
	for j := open + 1; j < closeIdx; j++ {
		// Step over anything nested: bodies, parameter lists, initializers.
		if toks[j].Kind == TokOperator {
			switch toks[j].Text {
			case "{", "(", "[":
				closeText := map[string]string{"{": "}", "(": ")", "[": "]"}[toks[j].Text]
				if end := matchDelimiter(toks, j, toks[j].Text, closeText); end > 0 {
					j = end
				}
			}
			continue
		}
		if toks[j].Kind != TokIdent || word(j) == "constructor" {
			continue
		}
		body := -1
		switch {
		case isOpText(toks, j+1, "("): // method: name(params) [: T] {
			if cp := matchDelimiter(toks, j+1, "(", ")"); cp > 0 {
				body = bodyBraceAfter(toks, cp)
			}
		case isOpText(toks, j+1, "="): // field: name = [async] (params) => …
			k := j + 2
			if k < closeIdx && word(k) == "async" {
				k++
			}
			if isOpText(toks, k, "(") {
				if cp := matchDelimiter(toks, k, "(", ")"); cp > 0 && (isOpText(toks, cp+1, "=>") || isOpText(toks, cp+1, ":")) {
					body = cp
				}
			}
		}
		if body < 0 {
			continue
		}
		start := declStart(toks, j)
		private, dispatched := false, false
		for k := start; k < j; k++ {
			switch w := word(k); {
			case w == "@" || w == "override" || w == "get" || w == "set":
				dispatched = true
			case w == "private" || w == "#":
				private = true
			case !jsMemberModifiers[w]:
				dispatched = true // not a plain member declaration; leave it alone
			}
		}
		if dispatched {
			continue
		}
		defs = appendFuncDef(defs, tf, langName, -1, j)
		d := &defs[len(defs)-1]
		d.Visibility, d.VisibilityKnown = visRepo, true
		if private {
			d.Visibility = visFile
		}
	}
	return defs
}

// includedFileNames returns the base names of the files named by `#include`
// directives anywhere in files.
func includedFileNames(files []TokenizedFile) map[string]bool {
	names := make(map[string]bool)
	for _, tf := range files {
		toks := tf.Tokens
		for i := 0; i+2 < len(toks); i++ {
			if !isOpText(toks, i, "#") || toks[i+1].OrigText+toks[i+1].Text != "include" || toks[i+2].Kind != TokString {
				continue
			}
			names[filepath.Base(strings.Trim(toks[i+2].OrigText, "\"<>"))] = true
		}
	}
	return names
}

// pastesTokens reports whether the file uses the preprocessor's `##` operator.
func pastesTokens(toks []Token) bool {
	for i := 0; i+1 < len(toks); i++ {
		if isOpText(toks, i, "#") && isOpText(toks, i+1, "#") && toks[i].Line == toks[i+1].Line {
			return true
		}
	}
	return false
}
