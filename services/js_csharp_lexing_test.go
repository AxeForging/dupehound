package services

import (
	"strings"
	"testing"
)

// These tests lock in four reliability fixes:
//   1. JS/TS clone detection now covers class methods and arrow functions
//      (previously only `function`-keyword bodies were scanned).
//   2. .jsx / .mts / .cts files are recognized.
//   3. C# verbatim (@"...") and interpolated ($"...") strings lex correctly.
//   4. JS/TS regex literals are lexed as literals, not chains of `/` operators.
//
// Each "Clone" test is a regression guard for a previously-missed duplicate;
// each "NotFlagged" test is a barrier guard ensuring the broader function-body
// marking did not start flagging non-logic (data/config/type) blocks.

// --- helpers ---

// firstStringToken returns the OrigText of the first TokString in src, or "".
func firstStringTokens(src, lang string) []string {
	toks := TokenizeFile(src, LangForName(lang))
	var out []string
	for _, t := range toks {
		if t.Kind == TokString {
			out = append(out, t.OrigText)
		}
	}
	return out
}

// countOpKind returns how many operator tokens equal text.
func countOp(src, lang, text string) int {
	toks := TokenizeFile(src, LangForName(lang))
	c := 0
	for _, t := range toks {
		if t.Kind == TokOperator && t.Text == text {
			c++
		}
	}
	return c
}

// --- 1. JS/TS function-body coverage (regression: these clones were missed) ---

func TestJSDetect_ClassMethodClone(t *testing.T) {
	a := makeFileLang("a.js", `class UserService {
  computeTotal(items) {
    let total = 0;
    for (const item of items) {
      total += item.price * item.quantity;
      total += item.tax;
    }
    return total;
  }
}`, "javascript")
	b := makeFileLang("b.js", `class OrderService {
  computeSum(things) {
    let sum = 0;
    for (const thing of things) {
      sum += thing.price * thing.quantity;
      sum += thing.tax;
    }
    return sum;
  }
}`, "javascript")
	clones := Detect([]TokenizedFile{a, b}, 20, 1.0)
	if len(clones) == 0 {
		t.Fatal("expected duplicated class methods to be detected, got 0 clones")
	}
}

func TestJSDetect_ArrowFunctionClone(t *testing.T) {
	a := makeFileLang("a.js", `const handleA = (data) => {
  const result = [];
  for (const d of data) {
    if (d.active && d.valid) {
      result.push(d.id);
      result.push(d.name);
    }
  }
  return result;
};`, "javascript")
	b := makeFileLang("b.js", `const handleB = (rows) => {
  const out = [];
  for (const r of rows) {
    if (r.active && r.valid) {
      out.push(r.id);
      out.push(r.name);
    }
  }
  return out;
};`, "javascript")
	clones := Detect([]TokenizedFile{a, b}, 20, 1.0)
	if len(clones) == 0 {
		t.Fatal("expected duplicated arrow functions to be detected, got 0 clones")
	}
}

func TestTSDetect_AsyncMethodWithReturnTypeClone(t *testing.T) {
	a := makeFileLang("a.ts", `class Repo {
  async fetchUser(id: number): Promise<User> {
    const conn = await pool.acquire();
    const row = await conn.query(id);
    const user = mapRow(row);
    return user;
  }
}`, "typescript")
	b := makeFileLang("b.ts", `class Store {
  async loadItem(key: number): Promise<Item> {
    const conn = await pool.acquire();
    const row = await conn.query(key);
    const item = mapRow(row);
    return item;
  }
}`, "typescript")
	clones := Detect([]TokenizedFile{a, b}, 20, 1.0)
	if len(clones) == 0 {
		t.Fatal("expected duplicated async methods with return-type annotations to be detected, got 0 clones")
	}
}

func TestFuncBoundary_JS_GetterAndConstructor(t *testing.T) {
	src := `class Box {
  constructor(value) {
    this.value = value;
    this.created = Date.now();
  }
  get doubled() {
    const computed = this.value * 2;
    return computed;
  }
}`
	tf := BuildTokenizedFile("a.js", src, LangForName("javascript"))
	assertBodyMarked(t, "javascript", tf, 3) // constructor body
	assertBodyMarked(t, "javascript", tf, 7) // getter body
}

// --- 1b. Barrier: data/config/type blocks must NOT be flagged as logic ---

func TestJSDetect_IdenticalObjectLiteralsNotFlagged(t *testing.T) {
	obj := `const CONFIG = {
  host: "localhost",
  port: 3000,
  retries: 5,
  timeout: 30000,
  database: "primary",
  poolSize: 10,
  ssl: true,
  region: "us-east-1",
};`
	a := makeFileLang("a.js", obj, "javascript")
	b := makeFileLang("b.js", obj, "javascript")
	clones := Detect([]TokenizedFile{a, b}, 15, 1.0)
	if len(clones) != 0 {
		t.Fatalf("top-level object literals are data, not logic; expected 0 clones, got %d", len(clones))
	}
}

func TestTSDetect_IdenticalInterfacesNotFlagged(t *testing.T) {
	a := makeFileLang("a.ts", `interface User {
  id: number;
  name: string;
  email: string;
  age: number;
  active: boolean;
  roles: string[];
  createdAt: Date;
}`, "typescript")
	b := makeFileLang("b.ts", `interface Account {
  id: number;
  name: string;
  email: string;
  age: number;
  active: boolean;
  roles: string[];
  createdAt: Date;
}`, "typescript")
	clones := Detect([]TokenizedFile{a, b}, 15, 1.0)
	if len(clones) != 0 {
		t.Fatalf("interfaces are type declarations, not logic; expected 0 clones, got %d", len(clones))
	}
}

// --- 2. Extension recognition ---

func TestDetectLanguage_NewExtensions(t *testing.T) {
	cases := map[string]string{
		"Component.jsx": "javascript",
		"module.mts":    "typescript",
		"module.cts":    "typescript",
		"View.tsx":      "typescript",
	}
	for path, want := range cases {
		lang := DetectLanguage(path)
		if lang == nil {
			t.Errorf("%s: expected language %q, got nil (unsupported)", path, want)
			continue
		}
		if lang.Name != want {
			t.Errorf("%s: expected language %q, got %q", path, want, lang.Name)
		}
	}
}

// --- 3. C# string lexing (regression: verbatim strings swallowed code) ---

func TestTokenize_CSharpVerbatimString(t *testing.T) {
	// The trailing `\"` inside the verbatim string must NOT be treated as an
	// escaped quote; the string ends at the real closing quote so the following
	// `;` and `int x` remain separate, correctly-lexed tokens.
	src := `class P { void M() { var path = @"C:\temp\"; int x = 1; } }`
	toks := TokenizeFile(src, LangForName("csharp"))

	var strs []string
	semicolons := 0
	sawX := false
	for _, tk := range toks {
		switch {
		case tk.Kind == TokString:
			strs = append(strs, tk.OrigText)
		case tk.Kind == TokOperator && tk.Text == ";":
			semicolons++
		case tk.Kind == TokIdent && tk.OrigText == "x":
			sawX = true
		}
	}
	if len(strs) != 1 || strs[0] != `@"C:\temp\"` {
		t.Fatalf("expected one verbatim string token %q, got %v", `@"C:\temp\"`, strs)
	}
	if semicolons != 2 {
		t.Errorf("expected 2 semicolons after the verbatim string, got %d (string swallowed code)", semicolons)
	}
	if !sawX {
		t.Error("expected identifier `x` after the verbatim string to be lexed (it was swallowed)")
	}
}

func TestTokenize_CSharpInterpolatedStrings(t *testing.T) {
	src := `var a = $"hello {name}"; var b = $@"path\{dir}";`
	strs := firstStringTokens(src, "csharp")
	if len(strs) != 2 {
		t.Fatalf("expected 2 string tokens, got %d: %v", len(strs), strs)
	}
	if !strings.HasPrefix(strs[0], `$"`) || !strings.HasPrefix(strs[1], `$@"`) {
		t.Errorf("interpolated strings mis-lexed: %v", strs)
	}
}

// --- 4. JS/TS regex literals (regression: lexed as division) ---

func TestTokenize_JSRegexLiteral(t *testing.T) {
	// Escaped slash and a slash inside a character class must not end the regex.
	src := `const m = /foo\/bar[/]/gi.test(x);`
	strs := firstStringTokens(src, "javascript")
	if len(strs) != 1 || strs[0] != `/foo\/bar[/]/gi` {
		t.Fatalf("expected one regex literal token %q, got %v", `/foo\/bar[/]/gi`, strs)
	}
}

func TestTokenize_JSDivisionNotRegex(t *testing.T) {
	// Barrier: division after a value (ident, `)`, number) must stay operators,
	// not be mistaken for the start of a regex literal.
	src := `const d = (a + b) / c / 2;`
	if got := countOp(src, "javascript", "/"); got != 2 {
		t.Fatalf("expected 2 division operators, got %d (division mis-lexed as regex)", got)
	}
	if strs := firstStringTokens(src, "javascript"); len(strs) != 0 {
		t.Fatalf("expected no string/regex tokens in a division expression, got %v", strs)
	}
}

func TestTokenize_JSRegexAfterKeyword(t *testing.T) {
	// `return /re/;` — a regex is expected after a keyword, not division.
	strs := firstStringTokens(`function f() { return /re/g; }`, "javascript")
	if len(strs) != 1 || strs[0] != `/re/g` {
		t.Fatalf("expected regex literal after return, got %v", strs)
	}
}
