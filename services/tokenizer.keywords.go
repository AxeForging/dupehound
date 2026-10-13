package services

import (
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

// Per-language keyword tables. Adding a language starts here and in domain.SupportedLanguages.

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

// LangForName returns the Language for a given name, or nil if not found.
func LangForName(name string) *domain.Language {
	for i := range domain.SupportedLanguages {
		if strings.EqualFold(domain.SupportedLanguages[i].Name, name) {
			return &domain.SupportedLanguages[i]
		}
	}
	return nil
}
