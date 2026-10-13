package services

import (
	"strings"
	"unicode/utf8"
)

// Literal forms the generic string scanner cannot handle: multi-line strings, heredocs, Rust char literals, JS regexes.

// tripleQuoteLangs maps a language to the quote characters that open a
// triple-quoted multi-line string in it: Python docstrings, Kotlin/Scala raw
// strings, Swift multi-line literals, Java text blocks, C# raw string literals,
// Dart multi-line strings and Elixir heredocs. Lexed line by line instead, each
// line holding an odd number of quotes desynchronises the rest of the file.
var tripleQuoteLangs = map[string]string{
	"python": `"'`,
	"ruby":   `"'`,
	"dart":   `"'`,
	"elixir": `"'`,
	"kotlin": `"`,
	"scala":  `"`,
	"swift":  `"`,
	"java":   `"`,
	"csharp": `"`,
}

// isRustCharLiteral reports whether the `'` at pos opens a char literal
// (`'a'`, `'\n'`, `'\u{1F600}'`) rather than a lifetime or loop label.
func isRustCharLiteral(src string, pos int) bool {
	p := pos + 1
	if p >= len(src) {
		return false
	}
	if src[p] == '\\' {
		// Escape sequence: the literal closes at the next quote on this line.
		// The longest escape is `\u{10FFFF}`, so a short lookahead is enough.
		for p += 2; p < len(src) && p-pos <= 12 && src[p] != '\n'; p++ {
			if src[p] == '\'' {
				return true
			}
		}
		return false
	}
	if src[p] == '\'' || src[p] == '\n' {
		return false
	}
	_, size := utf8.DecodeRuneInString(src[p:])
	p += size
	return p < len(src) && src[p] == '\''
}

// jsRegexExpected reports whether a `/` at the current position should be read
// as the start of a regex literal rather than a division operator, based on the
// previously emitted token. A regex is expected at the start of an expression
// (after operators, keywords, `(`, `,`, `=`, …) but not after a value (an
// identifier, literal, `)`, `]`, or a postfix `++`/`--`).
func jsRegexExpected(tokens []Token) bool {
	if len(tokens) == 0 {
		return true
	}
	prev := tokens[len(tokens)-1]
	switch prev.Kind {
	case TokIdent, TokNumber, TokString:
		return false
	case TokKeyword:
		switch prev.Text {
		case "this", "super", "true", "false", "null":
			return false
		}
		return true
	case TokOperator:
		switch prev.Text {
		case ")", "]", "++", "--":
			return false
		}
		return true
	}
	return true
}

// scanJSRegex scans a JavaScript/TypeScript regex literal starting at src[pos]
// (which must be `/`) and returns the index just past the closing `/` and any
// trailing flags. A `/` inside a character class `[...]` does not terminate the
// regex. Returns pos unchanged if the literal is unterminated on the line (in
// which case the caller falls back to treating `/` as a division operator).
func scanJSRegex(src string, pos, n int) int {
	i := pos + 1
	inClass := false
	for i < n {
		c := src[i]
		if c == '\n' {
			return pos // unterminated — not a regex
		}
		if c == '\\' {
			i += 2 // escaped char (e.g. \/ or \[)
			continue
		}
		switch c {
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if !inClass {
				i++ // closing slash
				for i < n && isLetter(src[i]) {
					i++ // regex flags
				}
				return i
			}
		}
		i++
	}
	return pos // unterminated
}

// heredocLangs are the languages whose heredocs are lexed as string literals.
var heredocLangs = strSet("ruby", "shell", "php")

// heredocTerminator returns the terminator named by the heredoc opener at pos
// (which points at `<<`), or "" when the `<<` is something else — a shift, an
// append, a shell here-string.
func heredocTerminator(src string, pos int, lang string) string {
	p := pos + 2
	at := func(i int) byte {
		if i < len(src) {
			return src[i]
		}
		return 0
	}
	marked := false // carries a marker that only a heredoc has
	switch lang {
	case "php":
		if at(p) != '<' {
			return ""
		}
		p++
		marked = true
	case "shell":
		if at(p) == '<' {
			return "" // `<<<` is a here-string
		}
		if at(p) == '-' {
			p++
			marked = true
		}
		for at(p) == ' ' {
			p++
		}
	case "ruby":
		if at(p) == '~' || at(p) == '-' {
			p++
			marked = true
		}
	}
	if q := at(p); q == '\'' || q == '"' || q == '`' {
		p++
		marked = true
	}
	start := p
	for p < len(src) && isIdentChar(src[p]) {
		p++
	}
	if p == start || (src[start] != '_' && !isLetter(src[start])) {
		return ""
	}
	// Bare `<<name` is also how Ruby spells `list <<name`. By convention a
	// heredoc terminator is upper case; require that when nothing else marks it.
	if upper := src[start] >= 'A' && src[start] <= 'Z'; lang == "ruby" && !marked && !upper {
		return ""
	}
	return src[start:p]
}

// heredocBody locates the body of a heredoc that starts at pos and ends at the
// line holding terminator id. It returns the end of the body, the position
// lexing resumes at (just past the terminator), and whether a terminator line
// was found at all.
func heredocBody(src string, pos int, id string) (end, resume int, ok bool) {
	for lineStart := pos; lineStart < len(src); {
		p := lineStart
		for p < len(src) && (src[p] == ' ' || src[p] == '\t') {
			p++
		}
		if hasPrefix(src, p, id) && (p+len(id) == len(src) || !isIdentChar(src[p+len(id)])) {
			return lineStart, p + len(id), true
		}
		nl := strings.IndexByte(src[lineStart:], '\n')
		if nl < 0 {
			break
		}
		lineStart += nl + 1
	}
	return 0, 0, false
}
