package services

import (
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

// The lexer: turns source text into the normalized token stream everything else works on.

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
//
// Field order matters: there is one Token per lexed token of the whole
// repository, and keeping the two one-byte fields together saves 8 bytes of
// padding on each.
type Token struct {
	Kind       TokenKind
	IgnoreMark bool   // true if this token is on a dupehound:ignore annotated line marker
	Line       int    // 1-indexed original source line
	Text       string // empty for Ident/Number/String; verbatim for Keyword/Operator
	OrigText   string // original text for Ident/Number/String; empty for Keyword/Operator
}

// matchDelimiter returns the index of the closing delimiter balancing the
// opening one at open, or -1 when the pair is unbalanced.
func matchDelimiter(tokens []Token, open int, openText, closeText string) int {
	depth := 0
	for j := open; j < len(tokens); j++ {
		if tokens[j].Kind != TokOperator {
			continue
		}
		switch tokens[j].Text {
		case openText:
			depth++
		case closeText:
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// TokenizeFile lexes content into a normalized token sequence for the given language.
// Comments are consumed (no token emitted). Identifiers become TokIdent, keywords become
// TokKeyword, number literals become TokNumber, string literals become TokString.
// Operators and punctuation are emitted as TokOperator.
func TokenizeFile(content string, lang *domain.Language) []Token {
	// A nil language means "no syntax knowledge": no keywords, no comment
	// markers, everything lexed as plain tokens. Scanning never reaches here
	// with one — collectFiles drops files whose language is unknown — but
	// markFunctionBodies handles nil explicitly, so lexing must too rather than
	// panicking on the first field access.
	if lang == nil {
		lang = &domain.Language{}
	}
	kws := getKeywords(lang.Name)
	src := content
	n := len(src)
	pos := 0
	line := 1
	var tokens []Token
	var heredocs []string // terminators of heredocs opened on the current line

	for pos < n {
		ch := src[pos]

		// Skip carriage returns (Windows line endings).
		if ch == '\r' {
			pos++
			continue
		}

		// Newline: just advance line counter. Heredocs opened on the line
		// that just ended have their bodies here.
		if ch == '\n' {
			line++
			pos++
			for _, id := range heredocs {
				end, resume, ok := heredocBody(src, pos, id)
				if !ok {
					break // no terminator: it was not a heredoc after all
				}
				tokens = append(tokens, Token{Kind: TokString, OrigText: src[pos:end], Line: line})
				line += strings.Count(src[pos:resume], "\n")
				pos = resume
			}
			heredocs = heredocs[:0]
			continue
		}

		// A heredoc opener (`<<~EOS`, `<<-EOF`, `<<<'EOT'`) is lexed as ordinary
		// tokens; only its terminator is remembered so the body can be taken as
		// one string once the line ends.
		if ch == '<' && heredocLangs[lang.Name] && hasPrefix(src, pos, "<<") {
			if id := heredocTerminator(src, pos, lang.Name); id != "" {
				heredocs = append(heredocs, id)
			}
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

		// Triple-quoted multi-line strings (must precede single-quote handling).
		if q := tripleQuoteLangs[lang.Name]; q != "" && (ch == '"' || ch == '\'') && strings.IndexByte(q, ch) >= 0 {
			if delim := src[pos:min(pos+3, n)]; delim == `"""` || delim == `'''` {
				start := pos
				startLine := line
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
				tokens = append(tokens, Token{Kind: TokString, OrigText: src[start:pos], Line: startLine})
				continue
			}
		}

		// Rust lifetimes and loop labels (`'a`, `'static`, `'outer:`) open with
		// the same quote as a char literal but never close. Emit the quote as
		// punctuation so the name lexes as an identifier, instead of letting the
		// string scanner swallow the rest of the signature.
		if lang.Name == "rust" && ch == '\'' && !isRustCharLiteral(src, pos) {
			tokens = append(tokens, Token{Kind: TokOperator, Text: "'", Line: line})
			pos++
			continue
		}

		// C# string prefixes: @"..." (verbatim), $"..." (interpolated), and the
		// combinations $@"..." / @$"...". In a verbatim string a backslash is a
		// literal character and a quote is escaped by doubling it (""), so the
		// generic `\`-escape handling below would mis-lex `@"C:\dir\"` and
		// swallow the following code. Handle these forms explicitly.
		if lang.Name == "csharp" && (ch == '@' || ch == '$') {
			p := pos
			verbatim := false
			for p < n && (src[p] == '@' || src[p] == '$') {
				if src[p] == '@' {
					verbatim = true
				}
				p++
			}
			if p < n && src[p] == '"' {
				start := pos
				startLine := line
				p++ // past the opening quote
				if verbatim {
					for p < n {
						if src[p] == '"' {
							if p+1 < n && src[p+1] == '"' {
								p += 2 // "" is an escaped quote inside a verbatim string
								continue
							}
							p++ // closing quote
							break
						}
						if src[p] == '\n' {
							line++ // verbatim strings may span lines
						}
						p++
					}
				} else {
					for p < n {
						c := src[p]
						if c == '\\' {
							p += 2
							continue
						}
						if c == '"' {
							p++
							break
						}
						if c == '\n' {
							break // non-verbatim strings do not span lines
						}
						p++
					}
				}
				tokens = append(tokens, Token{Kind: TokString, OrigText: src[start:p], Line: startLine})
				pos = p
				continue
			}
			// Not a string prefix (e.g. a verbatim identifier `@class`) — fall
			// through and let the normal operator/identifier path handle it.
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
					// An escaped newline is a line continuation: it is skipped
					// with the backslash but still has to be counted.
					if pos+1 < n && src[pos+1] == '\n' {
						line++
					}
					pos += 2
					continue
				}
				if c == '\n' {
					if delim != '`' {
						// Non-raw strings don't span lines; treat as ended. The
						// newline is left for the main loop, which counts it.
						break
					}
					line++
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

		// JavaScript / TypeScript regex literals. A `/` here is not a comment
		// (those are handled above), so it is either a division operator or the
		// start of a regex literal. We disambiguate with the standard
		// "expression vs value" heuristic: a regex is expected unless the
		// previous token produced a value. Without this, `/foo/.test(x)` lexes
		// as a chain of division operators and mangles the token stream.
		if (lang.Name == "javascript" || lang.Name == "typescript") && ch == '/' {
			if jsRegexExpected(tokens) {
				if end := scanJSRegex(src, pos, n); end > pos {
					tokens = append(tokens, Token{Kind: TokString, OrigText: src[pos:end], Line: line})
					pos = end
					continue
				}
			}
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

func isTwoCharOp(s string) bool { return twoCharOps[s] }

func isThreeCharOp(s string) bool { return threeCharOps[s] }

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

// matchOpening walks backwards from before and returns the index of the
// openText delimiter that is still open at that point — the partner of a
// closeText at before, or the opener of the block enclosing it. Nested pairs in
// between are skipped. It returns -1 when there is none.
func matchOpening(tokens []Token, before int, openText, closeText string) int {
	depth := 1
	for k := before - 1; k >= 0; k-- {
		if tokens[k].Kind != TokOperator {
			continue
		}
		switch tokens[k].Text {
		case closeText:
			depth++
		case openText:
			if depth--; depth == 0 {
				return k
			}
		}
	}
	return -1
}
