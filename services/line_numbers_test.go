package services

import (
	"testing"
)

// Regression tests for source line tracking in the lexer.
//
// Every clone is reported as a file + line range, so a line counter that drifts
// makes the finding point at the wrong code. Two defects caused drift:
//
//   - a quote the lexer could not close on its line (a Rust lifetime, an
//     apostrophe in JSX text, one line of an unsupported multi-line string)
//     counted that line's newline twice;
//   - a backslash-newline inside a string was skipped without being counted.
//
// Found by scanning real repositories: clones in Rust and Kotlin files were
// reported past the end of the file.

// lineOfIdent returns the line the lexer assigned to the first identifier
// named name, or 0 if it was never emitted as an identifier.
func lineOfIdent(tokens []Token, name string) int {
	for _, tk := range tokens {
		if tk.Kind == TokIdent && tk.OrigText == name {
			return tk.Line
		}
	}
	return 0
}

func TestTokenize_LineNumbersSurviveTrickyQuotes(t *testing.T) {
	tests := []struct {
		name string
		lang string
		src  string
		want int // line of the identifier MARKER
	}{
		{
			name: "rust lifetime in struct fields",
			lang: "rust",
			src:  "struct Holder<'a> {\n    name: &'a str,\n}\nfn MARKER() {}\n",
			want: 4,
		},
		{
			name: "rust lifetime in fn signature",
			lang: "rust",
			src:  "fn first<'a>(x: &'a str) -> &'a str {\n    x\n}\nfn MARKER() {}\n",
			want: 4,
		},
		{
			name: "rust static lifetime",
			lang: "rust",
			src:  "const NAME: &'static str = \"n\";\nfn MARKER() {}\n",
			want: 2,
		},
		{
			name: "kotlin raw string holding quotes",
			lang: "kotlin",
			src:  "val banner = \"\"\"\n  it's \"quoted\" text\n  second \"line\n\"\"\"\nfun MARKER() {}\n",
			want: 5,
		},
		{
			name: "swift multi-line string",
			lang: "swift",
			src:  "let s = \"\"\"\n  a \"b\n  c\n  \"\"\"\nfunc MARKER() {}\n",
			want: 5,
		},
		{
			name: "scala multi-line string",
			lang: "scala",
			src:  "val s = \"\"\"a \"b\n c\"\"\"\ndef MARKER() = 1\n",
			want: 3,
		},
		{
			name: "dart multi-line string",
			lang: "dart",
			src:  "var s = '''\n it's\n''';\nvoid MARKER() {}\n",
			want: 4,
		},
		{
			name: "java text block",
			lang: "java",
			src:  "class A {\n  String s = \"\"\"\n    say \"hi\n    \"\"\";\n  void MARKER() {}\n}\n",
			want: 5,
		},
		{
			name: "csharp raw string literal",
			lang: "csharp",
			src:  "class A {\n  string s = \"\"\"\n    say \"hi\n    \"\"\";\n  void MARKER() {}\n}\n",
			want: 5,
		},
		{
			name: "elixir heredoc",
			lang: "elixir",
			src:  "def a do\n  \"\"\"\n  it's \"x\n  \"\"\"\nend\ndef MARKER do\nend\n",
			want: 6,
		},
		{
			name: "apostrophe in jsx text",
			lang: "typescript",
			src:  "const a = <p>don't</p>;\nconst b = <p>isn't</p>;\nfunction MARKER() {}\n",
			want: 3,
		},
		{
			name: "unterminated string",
			lang: "go",
			src:  "var a = \"oops\nvar b = 1\nfunc MARKER() {}\n",
			want: 3,
		},
		{
			name: "backslash-newline continuation inside a string",
			lang: "c",
			src:  "char *s = \"one \\\ntwo\";\nint MARKER(void) { return 0; }\n",
			want: 3,
		},
		{
			name: "backtick string spanning lines is unaffected",
			lang: "go",
			src:  "var s = `a\nb\nc`\nfunc MARKER() {}\n",
			want: 4,
		},
		{
			name: "python docstring is unaffected",
			lang: "python",
			src:  "def a():\n    \"\"\"\n    it's \"x\n    \"\"\"\n    return 1\ndef MARKER():\n    return 2\n",
			want: 6,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := TokenizeFile(tt.src, LangForName(tt.lang))
			if got := lineOfIdent(tokens, "MARKER"); got != tt.want {
				t.Errorf("MARKER lexed on line %d, want %d", got, tt.want)
			}
		})
	}
}

// A lifetime is not a character literal. Lexing it as one swallowed everything
// up to the next quote on the line — parentheses and parameter names included —
// which broke function-boundary detection for the signature.
func TestTokenize_RustLifetimeDoesNotSwallowCode(t *testing.T) {
	tokens := TokenizeFile("fn first<'a>(items: &'a str) -> &'a str {\n", LangForName("rust"))

	if lineOfIdent(tokens, "items") == 0 {
		t.Error("parameter `items` was swallowed by a lifetime lexed as a string")
	}
	for _, tk := range tokens {
		if tk.Kind == TokString {
			t.Errorf("lifetime lexed as a string literal: %q", tk.OrigText)
		}
	}
}

func TestTokenize_RustCharLiteralsStayLiterals(t *testing.T) {
	tests := []struct {
		name string
		lit  string
	}{
		{"plain char", `'a'`},
		{"escaped newline", `'\n'`},
		{"escaped quote", `'\''`},
		{"escaped backslash", `'\\'`},
		{"unicode escape", `'\u{1F600}'`},
		{"multi-byte char", `'é'`},
		{"quote char", `'"'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := TokenizeFile("let c = "+tt.lit+"; let after = 1;\n", LangForName("rust"))
			found := false
			for _, tk := range tokens {
				if tk.Kind == TokString && tk.OrigText == tt.lit {
					found = true
				}
			}
			if !found {
				t.Errorf("char literal %s not lexed as one string token", tt.lit)
			}
			if lineOfIdent(tokens, "after") == 0 {
				t.Errorf("code after char literal %s was swallowed", tt.lit)
			}
		})
	}
}

// End to end: a clone's reported range must be the lines the code is on.
func TestDetect_CloneLinesAreExactAfterRustLifetimes(t *testing.T) {
	body := "    let mut total = 0;\n" +
		"    for item in items.iter() {\n" +
		"        if item.value > limit {\n" +
		"            total += item.value * 2;\n" +
		"        } else {\n" +
		"            total += 1;\n" +
		"        }\n" +
		"    }\n" +
		"    total\n"
	src := "struct Holder<'a> {\n    name: &'a str,\n}\n\n" + // lines 1-4
		"fn alpha(items: &Vec<Item>, limit: i32) -> i32 {\n" + body + "}\n\n" + // fn on 5, body 6-14
		"fn beta(items: &Vec<Item>, limit: i32) -> i32 {\n" + body + "}\n" // fn on 17, body 18-26

	tf := BuildTokenizedFile("a.rs", src, LangForName("rust"))
	clones := Detect([]TokenizedFile{tf}, 30, 1.0)
	if len(clones) != 1 {
		t.Fatalf("expected 1 clone, got %d", len(clones))
	}
	want := [][2]int{{6, 14}, {18, 26}}
	for i, inst := range clones[0].Instances {
		if inst.StartLine != want[i][0] || inst.EndLine != want[i][1] {
			t.Errorf("instance %d reported at lines %d-%d, want %d-%d",
				i, inst.StartLine, inst.EndLine, want[i][0], want[i][1])
		}
	}
}
