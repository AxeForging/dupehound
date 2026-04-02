package services

import (
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

// TokenKind is the semantic category of a token after normalization.
// Only the Kind is used for hashing — values (identifier names, literal values) are discarded.
// This means `result := compute()` and `output := compute()` hash identically (type-2 detection).
type TokenKind uint8

const (
	TokKeyword  TokenKind = iota // reserved word, kept verbatim for structure
	TokIdent                     // user-defined name → all normalize to same kind
	TokNumber                    // numeric literal → normalizes to same kind
	TokString                    // string/char literal → normalizes to same kind
	TokOperator                  // operator or punctuation, kept verbatim
)

// Token is a normalized lexical unit.
//
// Text is only set for TokKeyword and TokOperator (verbatim value for hashing,
// so `if` ≠ `for` and `+` ≠ `-`). It is empty for Ident/Number/String.
//
// OrigText is only set for TokIdent, TokNumber, and TokString (original source
// text, used after detection to classify clones as type-1 vs type-2).
// It is empty for Keyword/Operator.
type Token struct {
	Kind     TokenKind
	Text     string // empty for Ident/Number/String; verbatim for Keyword/Operator
	OrigText string // original text for Ident/Number/String; empty for Keyword/Operator
	Line     int    // 1-indexed original source line
}

// TokenizeFile lexes content into a normalized token sequence for the given language.
// Comments are consumed (no token emitted). Identifiers become TokIdent, keywords become
// TokKeyword, number literals become TokNumber, string literals become TokString.
// Operators and punctuation are emitted as TokOperator.
func TokenizeFile(content string, lang *domain.Language) []Token {
	kws := getKeywords(lang.Name)
	src := content
	n := len(src)
	pos := 0
	line := 1
	var tokens []Token

	for pos < n {
		ch := src[pos]

		// Skip carriage returns (Windows line endings).
		if ch == '\r' {
			pos++
			continue
		}

		// Newline: just advance line counter.
		if ch == '\n' {
			line++
			pos++
			continue
		}

		// Skip horizontal whitespace.
		if ch == ' ' || ch == '\t' {
			pos++
			continue
		}

		// Block comment must be checked before line comment (Lua: -- vs --[[).
		if lang.BlockStart != "" && hasPrefix(src, pos, lang.BlockStart) {
			pos += len(lang.BlockStart)
			for pos < n {
				if lang.BlockEnd != "" && hasPrefix(src, pos, lang.BlockEnd) {
					pos += len(lang.BlockEnd)
					break
				}
				if src[pos] == '\n' {
					line++
				}
				pos++
			}
			continue
		}

		// Line comment.
		if lang.LineComment != "" && hasPrefix(src, pos, lang.LineComment) {
			for pos < n && src[pos] != '\n' {
				pos++
			}
			continue
		}

		// PHP and Perl also support # as a line comment.
		if (lang.Name == "php" || lang.Name == "perl") && ch == '#' {
			for pos < n && src[pos] != '\n' {
				pos++
			}
			continue
		}

		// Python / Ruby triple-quoted strings (must precede single-quote handling).
		if lang.Name == "python" || lang.Name == "ruby" {
			if hasPrefix(src, pos, `"""`) || hasPrefix(src, pos, `'''`) {
				start := pos
				delim := src[pos : pos+3]
				pos += 3
				for pos < n {
					if hasPrefix(src, pos, delim) {
						pos += 3
						break
					}
					if src[pos] == '\n' {
						line++
					}
					pos++
				}
				tokens = append(tokens, Token{Kind: TokString, OrigText: src[start:pos], Line: line})
				continue
			}
		}

		// String and character literals.
		if ch == '"' || ch == '\'' || ch == '`' {
			start := pos
			startLine := line
			delim := ch
			pos++
			for pos < n {
				c := src[pos]
				if c == '\\' {
					pos += 2
					continue
				}
				if c == '\n' {
					line++
					if delim != '`' {
						// Non-raw strings don't span lines; treat as ended.
						break
					}
				}
				if c == delim {
					pos++
					break
				}
				pos++
			}
			tokens = append(tokens, Token{Kind: TokString, OrigText: src[start:pos], Line: startLine})
			continue
		}

		// Numeric literals (including 0x hex, 0b binary, floats, underscored).
		if ch >= '0' && ch <= '9' {
			start := pos
			for pos < n && isNumChar(src[pos]) {
				pos++
			}
			tokens = append(tokens, Token{Kind: TokNumber, OrigText: src[start:pos], Line: line})
			continue
		}

		// Identifiers and keywords.
		if ch == '_' || isLetter(ch) {
			start := pos
			for pos < n && isIdentChar(src[pos]) {
				pos++
			}
			word := src[start:pos]
			if kws[word] {
				tokens = append(tokens, Token{Kind: TokKeyword, Text: word, Line: line})
			} else {
				tokens = append(tokens, Token{Kind: TokIdent, OrigText: word, Line: line})
			}
			continue
		}

		// Three-character operators (check before two-char).
		if pos+2 < n {
			three := src[pos : pos+3]
			if isThreeCharOp(three) {
				tokens = append(tokens, Token{Kind: TokOperator, Text: three, Line: line})
				pos += 3
				continue
			}
		}

		// Multi-character operators.
		if pos+1 < n {
			two := src[pos : pos+2]
			if isTwoCharOp(two) {
				tokens = append(tokens, Token{Kind: TokOperator, Text: two, Line: line})
				pos += 2
				continue
			}
		}

		// Single-character operator or punctuation.
		tokens = append(tokens, Token{Kind: TokOperator, Text: src[pos : pos+1], Line: line})
		pos++
	}

	return tokens
}

// hasPrefix reports whether src[pos:] starts with prefix.
func hasPrefix(src string, pos int, prefix string) bool {
	return pos+len(prefix) <= len(src) && src[pos:pos+len(prefix)] == prefix
}

func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentChar(c byte) bool {
	return c == '_' || isLetter(c) || (c >= '0' && c <= '9')
}

func isNumChar(c byte) bool {
	return (c >= '0' && c <= '9') || c == '.' || c == '_' ||
		c == 'e' || c == 'E' || c == 'x' || c == 'X' ||
		c == 'b' || c == 'B' || c == 'o' || c == 'O' ||
		(c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

var twoCharOps = map[string]bool{
	":=": true, "==": true, "!=": true, "<=": true, ">=": true,
	"<<": true, ">>": true, "->": true, "=>": true, "<-": true,
	"+=": true, "-=": true, "*=": true, "/=": true, "%=": true,
	"&&": true, "||": true, "++": true, "--": true, "**": true,
	"::": true, "??": true, "?.": true, "!.": true, ".=": true,
	"^^": true, "&^": true, "|>": true,
}

var threeCharOps = map[string]bool{
	"<<=": true, ">>=": true, "...": true, "<=>": true, "===": true, "!==": true,
}

func isTwoCharOp(s string) bool   { return twoCharOps[s] }
func isThreeCharOp(s string) bool { return threeCharOps[s] }

// getKeywords returns the keyword set for the given language name.
func getKeywords(name string) map[string]bool {
	kws, ok := langKeywords[name]
	if !ok {
		return map[string]bool{}
	}
	return kws
}

// langKeywords maps language name → reserved keywords.
// Keywords are kept verbatim in the token stream (structural); identifiers are normalized.
var langKeywords = map[string]map[string]bool{
	"go": strSet(
		"break", "case", "chan", "const", "continue", "default", "defer", "else",
		"fallthrough", "for", "func", "go", "goto", "if", "import", "interface",
		"map", "package", "range", "return", "select", "struct", "switch", "type", "var",
	),
	"python": strSet(
		"False", "None", "True", "and", "as", "assert", "async", "await",
		"break", "class", "continue", "def", "del", "elif", "else", "except",
		"finally", "for", "from", "global", "if", "import", "in", "is",
		"lambda", "nonlocal", "not", "or", "pass", "raise", "return",
		"try", "while", "with", "yield",
	),
	"javascript": strSet(
		"break", "case", "catch", "class", "const", "continue", "debugger",
		"default", "delete", "do", "else", "export", "extends", "finally",
		"for", "function", "if", "import", "in", "instanceof", "let", "new",
		"return", "static", "super", "switch", "this", "throw", "try",
		"typeof", "var", "void", "while", "with", "yield", "of", "async", "await",
	),
	"typescript": strSet(
		"break", "case", "catch", "class", "const", "continue", "debugger",
		"default", "delete", "do", "else", "enum", "export", "extends", "finally",
		"for", "function", "if", "implements", "import", "in", "instanceof",
		"interface", "let", "new", "package", "private", "protected", "public",
		"return", "static", "super", "switch", "this", "throw", "try",
		"type", "typeof", "var", "void", "while", "with", "yield", "of",
		"async", "await", "abstract", "as", "declare", "from", "readonly",
	),
	"java": strSet(
		"abstract", "assert", "boolean", "break", "byte", "case", "catch", "char",
		"class", "const", "continue", "default", "do", "double", "else", "enum",
		"extends", "final", "finally", "float", "for", "goto", "if", "implements",
		"import", "instanceof", "int", "interface", "long", "native", "new",
		"package", "private", "protected", "public", "return", "short", "static",
		"strictfp", "super", "switch", "synchronized", "this", "throw", "throws",
		"transient", "try", "void", "volatile", "while",
	),
	"kotlin": strSet(
		"as", "break", "class", "continue", "do", "else", "false", "for", "fun",
		"if", "in", "interface", "is", "null", "object", "package", "return",
		"super", "this", "throw", "true", "try", "typealias", "typeof", "val",
		"var", "when", "while", "by", "catch", "constructor", "delegate",
		"dynamic", "field", "file", "finally", "get", "import", "init",
		"param", "property", "receiver", "set", "setparam", "value", "where",
		"actual", "abstract", "annotation", "companion", "crossinline",
		"data", "enum", "expect", "external", "final", "infix", "inline",
		"inner", "internal", "lateinit", "noinline", "open", "operator",
		"out", "override", "private", "protected", "public", "reified",
		"sealed", "suspend", "tailrec", "vararg",
	),
	"rust": strSet(
		"as", "async", "await", "break", "const", "continue", "crate", "dyn",
		"else", "enum", "extern", "false", "fn", "for", "if", "impl", "in",
		"let", "loop", "match", "mod", "move", "mut", "pub", "ref", "return",
		"self", "Self", "static", "struct", "super", "trait", "true", "type",
		"union", "unsafe", "use", "where", "while",
	),
	"c": strSet(
		"auto", "break", "case", "char", "const", "continue", "default", "do",
		"double", "else", "enum", "extern", "float", "for", "goto", "if",
		"inline", "int", "long", "register", "restrict", "return", "short",
		"signed", "sizeof", "static", "struct", "switch", "typedef", "union",
		"unsigned", "void", "volatile", "while",
	),
	"cpp": strSet(
		"alignas", "alignof", "and", "and_eq", "asm", "auto", "bitand", "bitor",
		"bool", "break", "case", "catch", "char", "char8_t", "char16_t", "char32_t",
		"class", "compl", "concept", "const", "consteval", "constexpr", "constinit",
		"const_cast", "continue", "co_await", "co_return", "co_yield", "decltype",
		"default", "delete", "do", "double", "dynamic_cast", "else", "enum",
		"explicit", "export", "extern", "false", "float", "for", "friend", "goto",
		"if", "inline", "int", "long", "mutable", "namespace", "new", "noexcept",
		"not", "not_eq", "nullptr", "operator", "or", "or_eq", "private", "protected",
		"public", "register", "reinterpret_cast", "requires", "return", "short",
		"signed", "sizeof", "static", "static_assert", "static_cast", "struct",
		"switch", "template", "this", "thread_local", "throw", "true", "try",
		"typedef", "typeid", "typename", "union", "unsigned", "using", "virtual",
		"void", "volatile", "wchar_t", "while", "xor", "xor_eq",
	),
	"csharp": strSet(
		"abstract", "as", "base", "bool", "break", "byte", "case", "catch", "char",
		"checked", "class", "const", "continue", "decimal", "default", "delegate",
		"do", "double", "else", "enum", "event", "explicit", "extern", "false",
		"finally", "fixed", "float", "for", "foreach", "goto", "if", "implicit",
		"in", "int", "interface", "internal", "is", "lock", "long", "namespace",
		"new", "null", "object", "operator", "out", "override", "params", "private",
		"protected", "public", "readonly", "ref", "return", "sbyte", "sealed",
		"short", "sizeof", "stackalloc", "static", "string", "struct", "switch",
		"this", "throw", "true", "try", "typeof", "uint", "ulong", "unchecked",
		"unsafe", "ushort", "using", "virtual", "void", "volatile", "while",
		"async", "await", "var", "dynamic",
	),
	"swift": strSet(
		"associatedtype", "class", "deinit", "enum", "extension", "fileprivate",
		"func", "import", "init", "inout", "internal", "let", "open", "operator",
		"precedencegroup", "private", "protocol", "public", "rethrows", "return",
		"static", "struct", "subscript", "typealias", "var", "break", "case",
		"catch", "continue", "default", "defer", "do", "else", "fallthrough",
		"for", "guard", "if", "in", "repeat", "throw", "throws", "try", "where",
		"while", "as", "Any", "false", "is", "nil", "self", "Self", "super", "true",
		"async", "await",
	),
	"scala": strSet(
		"abstract", "case", "catch", "class", "def", "do", "else", "extends",
		"false", "final", "finally", "for", "forSome", "if", "implicit", "import",
		"lazy", "match", "new", "null", "object", "override", "package", "private",
		"protected", "return", "sealed", "super", "this", "throw", "trait", "try",
		"true", "type", "val", "var", "while", "with", "yield",
	),
	"php": strSet(
		"abstract", "and", "array", "as", "break", "callable", "case", "catch",
		"class", "clone", "const", "continue", "declare", "default", "die", "do",
		"echo", "else", "elseif", "empty", "enddeclare", "endfor", "endforeach",
		"endif", "endswitch", "endwhile", "eval", "exit", "extends", "final",
		"finally", "fn", "for", "foreach", "function", "global", "goto", "if",
		"implements", "include", "include_once", "instanceof", "insteadof",
		"interface", "isset", "list", "match", "namespace", "new", "or", "print",
		"private", "protected", "public", "readonly", "require", "require_once",
		"return", "static", "switch", "throw", "trait", "try", "unset", "use",
		"var", "while", "xor", "yield",
	),
	"ruby": strSet(
		"__ENCODING__", "__LINE__", "__FILE__", "BEGIN", "END", "alias", "and",
		"begin", "break", "case", "class", "def", "defined?", "do", "else",
		"elsif", "end", "ensure", "false", "for", "if", "in", "module", "next",
		"nil", "not", "or", "redo", "rescue", "retry", "return", "self", "super",
		"then", "true", "undef", "unless", "until", "when", "while", "yield",
	),
	"shell": strSet(
		"if", "then", "else", "elif", "fi", "for", "in", "do", "done", "while",
		"until", "case", "esac", "function", "return", "break", "continue",
		"exit", "export", "local", "readonly", "unset", "shift", "echo",
	),
	"sql": strSet(
		"SELECT", "FROM", "WHERE", "AND", "OR", "NOT", "INSERT", "INTO", "VALUES",
		"UPDATE", "SET", "DELETE", "CREATE", "TABLE", "DROP", "ALTER", "ADD",
		"COLUMN", "PRIMARY", "KEY", "FOREIGN", "REFERENCES", "INDEX", "UNIQUE",
		"NULL", "DEFAULT", "CONSTRAINT", "JOIN", "LEFT", "RIGHT", "INNER", "OUTER",
		"ON", "GROUP", "BY", "ORDER", "HAVING", "LIMIT", "OFFSET", "AS", "IN",
		"EXISTS", "BETWEEN", "LIKE", "CASE", "WHEN", "THEN", "ELSE", "END",
		"BEGIN", "COMMIT", "ROLLBACK", "TRANSACTION", "VIEW", "TRIGGER", "FUNCTION",
		"PROCEDURE", "RETURNS", "RETURN", "IF", "IS", "DISTINCT", "ALL", "ANY",
		"UNION", "EXCEPT", "INTERSECT", "WITH", "RECURSIVE",
		// lowercase variants
		"select", "from", "where", "and", "or", "not", "insert", "into", "values",
		"update", "set", "delete", "create", "table", "drop", "alter", "add",
		"join", "left", "right", "inner", "outer", "on", "group", "by", "order",
		"having", "limit", "offset", "as", "in", "exists", "between", "like",
		"case", "when", "then", "else", "end", "begin", "commit", "rollback",
		"null", "is", "distinct", "all", "any", "union", "except", "with",
	),
	"lua": strSet(
		"and", "break", "do", "else", "elseif", "end", "false", "for", "function",
		"goto", "if", "in", "local", "nil", "not", "or", "repeat", "return",
		"then", "true", "until", "while",
	),
	"elixir": strSet(
		"after", "alias", "and", "case", "catch", "cond", "def", "defcallback",
		"defdelegate", "defexception", "defguard", "defguardp", "defimpl",
		"defmacro", "defmacrop", "defmodule", "defoverridable", "defp",
		"defprotocol", "defrecord", "defstruct", "do", "else", "end", "false",
		"fn", "for", "if", "import", "in", "nil", "not", "or", "quote",
		"raise", "receive", "require", "rescue", "return", "super", "throw",
		"true", "try", "unless", "unquote", "unquote_splicing", "use", "when",
		"with",
	),
	"dart": strSet(
		"abstract", "as", "assert", "async", "await", "base", "break", "case",
		"catch", "class", "const", "continue", "covariant", "default", "deferred",
		"do", "dynamic", "else", "enum", "export", "extends", "extension",
		"external", "factory", "false", "final", "finally", "for", "Function",
		"get", "hide", "if", "implements", "import", "in", "interface", "is",
		"late", "library", "mixin", "new", "null", "of", "on", "operator",
		"part", "required", "rethrow", "return", "sealed", "set", "show",
		"static", "super", "switch", "sync", "this", "throw", "true", "try",
		"type", "typedef", "var", "void", "when", "with", "while", "yield",
	),
	"r": strSet(
		"if", "else", "repeat", "while", "function", "for", "in", "next",
		"break", "TRUE", "FALSE", "NULL", "Inf", "NaN", "NA", "NA_integer_",
		"NA_real_", "NA_complex_", "NA_character_", "return",
	),
}

// funcKeywords maps language names to the keywords that introduce function/method bodies.
var funcKeywords = map[string]map[string]bool{
	"go":         strSet("func"),
	"python":     strSet("def"),
	"javascript": strSet("function"),
	"typescript": strSet("function"),
	"java":       strSet(), // uses brace-depth heuristic (methods are inside class braces)
	"kotlin":     strSet("fun"),
	"rust":       strSet("fn"),
	"c":          strSet(), // uses brace-depth heuristic
	"cpp":        strSet(), // uses brace-depth heuristic
	"csharp":     strSet(), // uses brace-depth heuristic
	"swift":      strSet("func"),
	"scala":      strSet("def"),
	"php":        strSet("function"),
	"ruby":       strSet("def"),
	"shell":      strSet("function"),
	"sql":        strSet("FUNCTION", "PROCEDURE", "function", "procedure"),
	"lua":        strSet("function"),
	"elixir":     strSet("def", "defp", "defmacro", "defmacrop"),
	"dart":       strSet(), // uses brace-depth heuristic
	"r":          strSet("function"),
}

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
func markFunctionBodies(tokens []Token, lang *domain.Language) []bool {
	n := len(tokens)
	inFunc := make([]bool, n)

	if lang == nil || n == 0 {
		// No language info — mark everything as in-function (no filtering).
		for i := range inFunc {
			inFunc[i] = true
		}
		return inFunc
	}

	switch lang.Name {
	case "python":
		markPythonFunctions(tokens, inFunc)
	case "ruby":
		markDefEndFunctions(tokens, inFunc, "def", "end")
	case "elixir":
		markDefEndFunctions(tokens, inFunc, "", "end")
	case "lua":
		markDefEndFunctions(tokens, inFunc, "function", "end")
	case "sql":
		markSQLFunctions(tokens, inFunc)
	default:
		markBraceFunctions(tokens, inFunc, lang)
	}

	return inFunc
}

// markBraceFunctions handles brace-based languages.
func markBraceFunctions(tokens []Token, inFunc []bool, lang *domain.Language) {
	fkws := funcKeywords[lang.Name]
	hasFuncKeywords := len(fkws) > 0

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
			// Track brace depth from the opening brace.
			depth := 1
			for j := braceIdx + 1; j < n && depth > 0; j++ {
				if tokens[j].Kind == TokOperator {
					switch tokens[j].Text {
					case "{":
						depth++
					case "}":
						depth--
					}
				}
				if depth > 0 {
					inFunc[j] = true
				}
			}
		}
	} else {
		// Languages without func keywords (Java, C, C++, C#, Dart):
		// Any brace block entered at depth 0 or 1 marks its contents.
		// depth 0 → top-level function (C) or class body (Java)
		// depth 1 → method inside a class
		depth := 0
		funcDepth := -1 // depth at which we entered the "function" block
		for i := 0; i < n; i++ {
			if tokens[i].Kind == TokOperator {
				switch tokens[i].Text {
				case "{":
					if depth <= 1 && funcDepth < 0 {
						funcDepth = depth
					}
					depth++
				case "}":
					depth--
					if depth == funcDepth {
						funcDepth = -1
					}
				}
			}
			if funcDepth >= 0 && depth > funcDepth {
				inFunc[i] = true
			}
		}
	}
}

// markPythonFunctions marks tokens after `def name(...):` as in-function.
// Since we don't have indentation info in the token stream, we mark from
// the colon after def through to the next def/class at the same scope or EOF.
func markPythonFunctions(tokens []Token, inFunc []bool) {
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
			defLine := tokens[start].Line
			for j := colonIdx + 1; j < n; j++ {
				// Stop at next top-level def or class (heuristic: if the def/class
				// is on a line that's <= the original def line's indent, we stop).
				if tokens[j].Kind == TokKeyword && (tokens[j].Text == "def" || tokens[j].Text == "class") {
					// Same or earlier line offset means we've left the function.
					// Since we don't have indentation, use a simpler heuristic:
					// if there's a blank-line gap (line difference > 1 from previous token),
					// this might be a new top-level definition.
					if j > 0 && tokens[j].Line > tokens[j-1].Line+1 {
						i = j
						break
					}
				}
				inFunc[j] = true
				if j == n-1 {
					i = n
				}
			}
			_ = defLine
		}
		i++
	}
}

// markSQLFunctions marks tokens inside SQL FUNCTION/PROCEDURE bodies (BEGIN...END).
func markSQLFunctions(tokens []Token, inFunc []bool) {
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
		// Track BEGIN/END depth.
		depth := 1
		for j := beginIdx + 1; j < n && depth > 0; j++ {
			if tokens[j].Kind == TokKeyword {
				switch tokens[j].Text {
				case "BEGIN", "begin":
					depth++
				case "END", "end":
					depth--
				}
			}
			if depth > 0 {
				inFunc[j] = true
			}
		}
	}
}

// markDefEndFunctions marks tokens between def and end keywords (Ruby, Elixir).
func markDefEndFunctions(tokens []Token, inFunc []bool, defKw, endKw string) {
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
	n := len(tokens)
	for i := 0; i < n; i++ {
		t := tokens[i]
		if t.Kind != TokKeyword || !defKeywords[t.Text] {
			continue
		}
		// Find matching end keyword, tracking nested def/end/do pairs.
		depth := 1
		for j := i + 1; j < n && depth > 0; j++ {
			if tokens[j].Kind == TokKeyword {
				if defKeywords[tokens[j].Text] || nestKeywords[tokens[j].Text] {
					depth++
				} else if tokens[j].Text == endKw {
					depth--
				}
			}
			if depth > 0 {
				inFunc[j] = true
			}
		}
	}
}

// strSet converts a variadic list of strings into a bool map.
func strSet(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// tokenKinds returns just the Kind sequence from a token slice (for testing).
func tokenKinds(tokens []Token) []TokenKind {
	kinds := make([]TokenKind, len(tokens))
	for i, t := range tokens {
		kinds[i] = t.Kind
	}
	return kinds
}

// kindsEqual reports whether two kind sequences are equal.
// Used in tests to assert type-2 clone detection.
func kindsEqual(a, b []TokenKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// LangForName returns the Language for a given name, or nil if not found.
func LangForName(name string) *domain.Language {
	for i := range domain.SupportedLanguages {
		if strings.EqualFold(domain.SupportedLanguages[i].Name, name) {
			return &domain.SupportedLanguages[i]
		}
	}
	return nil
}
