package services

import (
	"strings"
	"testing"
)

func TestMarkFunctionBodies_Go_OnlyBodyMarked(t *testing.T) {
	src := `package main

var x = 10

func hello() {
	fmt.Println("hi")
}

var y = 20
`
	tf := BuildTokenizedFile("a.go", src, LangForName("go"))
	if len(tf.InFunc) != len(tf.Tokens) {
		t.Fatalf("InFunc length %d != Tokens length %d", len(tf.InFunc), len(tf.Tokens))
	}

	// Tokens inside the function body should be marked true.
	// Tokens outside (package, var declarations) should be false.
	for i, tok := range tf.Tokens {
		if tok.Line >= 5 && tok.Line <= 7 {
			// Lines 5-7 are the function — body tokens should be true.
			// The func keyword and signature are outside, body is inside {}.
			if tok.Kind == TokKeyword && tok.Text == "func" {
				continue // func keyword itself may or may not be marked
			}
		}
		if tok.Line == 6 && !tf.InFunc[i] {
			t.Errorf("token %q at line %d should be inside function body", tokenText(tok), tok.Line)
		}
		if (tok.Line <= 3 || tok.Line >= 9) && tf.InFunc[i] {
			t.Errorf("token %q at line %d should NOT be inside function body", tokenText(tok), tok.Line)
		}
	}
}

func TestMarkFunctionBodies_Go_MultipleFunc(t *testing.T) {
	src := `package main

func a() {
	x := 1
}

func b() {
	y := 2
}
`
	tf := BuildTokenizedFile("a.go", src, LangForName("go"))

	bodyTokenCount := 0
	for _, v := range tf.InFunc {
		if v {
			bodyTokenCount++
		}
	}
	if bodyTokenCount == 0 {
		t.Fatal("expected some tokens inside function bodies")
	}

	// Package declaration should not be marked.
	for i, tok := range tf.Tokens {
		if tok.Kind == TokKeyword && tok.Text == "package" && tf.InFunc[i] {
			t.Error("package keyword should not be inside function body")
		}
	}
}

func TestMarkFunctionBodies_Go_TopLevelVarExcluded(t *testing.T) {
	src := `package main

var keywords = map[string]bool{
	"break": true,
	"case":  true,
	"chan":  true,
}

func process() {
	result := compute()
}
`
	tf := BuildTokenizedFile("a.go", src, LangForName("go"))

	// The var block (lines 3-7) should NOT be marked.
	for i, tok := range tf.Tokens {
		if tok.Line >= 3 && tok.Line <= 7 && tf.InFunc[i] {
			t.Errorf("token %q at line %d (var declaration) should NOT be in function body", tokenText(tok), tok.Line)
		}
	}

	// The function body (line 10) should be marked.
	found := false
	for i, tok := range tf.Tokens {
		if tok.Line == 10 && tf.InFunc[i] {
			found = true
			break
		}
	}
	if !found {
		t.Error("function body at line 10 should be marked as in-function")
	}
}

func TestMarkFunctionBodies_JavaScript(t *testing.T) {
	src := `const CONFIG = {
  host: "localhost",
  port: 3000,
};

function process(data) {
  const result = transform(data);
  return result;
}

const ANOTHER = "value";
`
	tf := BuildTokenizedFile("a.js", src, LangForName("javascript"))

	// CONFIG object (lines 1-4) should NOT be marked.
	for i, tok := range tf.Tokens {
		if tok.Line >= 1 && tok.Line <= 4 && tf.InFunc[i] {
			t.Errorf("JS: token %q at line %d (const object) should NOT be in function", tokenText(tok), tok.Line)
		}
	}

	// Function body (lines 7-8) should be marked.
	bodyFound := false
	for i, tok := range tf.Tokens {
		if tok.Line == 7 && tf.InFunc[i] {
			bodyFound = true
			break
		}
	}
	if !bodyFound {
		t.Error("JS: function body at line 7 should be marked")
	}
}

func TestMarkFunctionBodies_TypeScript(t *testing.T) {
	src := `interface User {
  name: string;
  age: number;
}

function getUser(): User {
  return { name: "test", age: 25 };
}
`
	tf := BuildTokenizedFile("a.ts", src, LangForName("typescript"))

	// Interface declaration should NOT be marked.
	for i, tok := range tf.Tokens {
		if tok.Line >= 1 && tok.Line <= 4 && tf.InFunc[i] {
			t.Errorf("TS: token %q at line %d (interface) should NOT be in function", tokenText(tok), tok.Line)
		}
	}

	// Function body should be marked.
	bodyFound := false
	for i, tok := range tf.Tokens {
		if tok.Line == 7 && tf.InFunc[i] {
			bodyFound = true
			break
		}
	}
	if !bodyFound {
		t.Error("TS: function body at line 7 should be marked")
	}
}

func TestMarkFunctionBodies_Python(t *testing.T) {
	src := `COLORS = ["red", "green", "blue"]

def process(data):
    result = transform(data)
    return result

ANOTHER = "value"
`
	tf := BuildTokenizedFile("a.py", src, LangForName("python"))

	// COLORS list (line 1) should NOT be marked.
	for i, tok := range tf.Tokens {
		if tok.Line == 1 && tf.InFunc[i] {
			t.Errorf("Python: token %q at line %d should NOT be in function", tokenText(tok), tok.Line)
		}
	}

	// Function body (lines 4-5) should be marked.
	bodyFound := false
	for i, tok := range tf.Tokens {
		if tok.Line == 4 && tf.InFunc[i] {
			bodyFound = true
			break
		}
	}
	if !bodyFound {
		t.Error("Python: function body at line 4 should be marked")
	}
}

func TestMarkFunctionBodies_Rust(t *testing.T) {
	src := `const MAX: i32 = 100;

struct Config {
    host: String,
    port: i32,
}

fn process(data: &str) -> String {
    let result = transform(data);
    result
}
`
	tf := BuildTokenizedFile("a.rs", src, LangForName("rust"))

	// Struct (lines 3-6) should NOT be marked.
	for i, tok := range tf.Tokens {
		if tok.Line >= 3 && tok.Line <= 6 && tf.InFunc[i] {
			t.Errorf("Rust: token %q at line %d (struct) should NOT be in function", tokenText(tok), tok.Line)
		}
	}

	// fn body (lines 9-10) should be marked.
	bodyFound := false
	for i, tok := range tf.Tokens {
		if tok.Line == 9 && tf.InFunc[i] {
			bodyFound = true
			break
		}
	}
	if !bodyFound {
		t.Error("Rust: function body at line 9 should be marked")
	}
}

func TestMarkFunctionBodies_Java_MethodInsideClass(t *testing.T) {
	src := `public class App {
    private static final int MAX = 100;

    public void process() {
        int result = compute();
        validate(result);
    }
}
`
	tf := BuildTokenizedFile("App.java", src, LangForName("java"))

	// Method body (lines 5-6) should be marked.
	bodyFound := false
	for i, tok := range tf.Tokens {
		if tok.Line == 5 && tf.InFunc[i] {
			bodyFound = true
			break
		}
	}
	if !bodyFound {
		t.Error("Java: method body at line 5 should be marked")
	}
}

func TestMarkFunctionBodies_Ruby(t *testing.T) {
	src := `COLORS = ["red", "green", "blue"]

def process(data)
  result = transform(data)
  result
end

ANOTHER = "value"
`
	tf := BuildTokenizedFile("a.rb", src, LangForName("ruby"))

	// COLORS (line 1) should NOT be marked.
	for i, tok := range tf.Tokens {
		if tok.Line == 1 && tf.InFunc[i] {
			t.Errorf("Ruby: token %q at line %d should NOT be in function", tokenText(tok), tok.Line)
		}
	}

	// Function body (lines 4-5) should be marked.
	bodyFound := false
	for i, tok := range tf.Tokens {
		if tok.Line == 4 && tf.InFunc[i] {
			bodyFound = true
			break
		}
	}
	if !bodyFound {
		t.Error("Ruby: function body at line 4 should be marked")
	}
}

func TestMarkFunctionBodies_NilLang_EverythingMarked(t *testing.T) {
	tokens := []Token{
		{Kind: TokKeyword, Text: "var", Line: 1},
		{Kind: TokIdent, OrigText: "x", Line: 1},
	}
	result := markFunctionBodies(tokens, nil)
	for i, v := range result {
		if !v {
			t.Errorf("nil lang: token %d should be marked true (no filtering)", i)
		}
	}
}

func TestDetect_SkipsTopLevelDeclarations(t *testing.T) {
	// Two files with identical top-level var maps but different functions.
	// With function-level detection, only the function bodies should be compared.
	aCode := `package main

var keywords = map[string]bool{
	"break": true, "case": true, "chan": true,
	"const": true, "continue": true, "default": true,
}

func process() {
	result := compute()
	validate(result)
	store(result)
	notify(result)
	return result
}
`
	bCode := `package main

var keywords = map[string]bool{
	"break": true, "case": true, "chan": true,
	"const": true, "continue": true, "default": true,
}

func different() {
	output := calculate()
	check(output)
	save(output)
	alert(output)
	return output
}
`
	files := []TokenizedFile{
		BuildTokenizedFile("a.go", aCode, LangForName("go")),
		BuildTokenizedFile("b.go", bCode, LangForName("go")),
	}
	clones := Detect(files, 10, 1.0)

	// The var declaration maps are identical but should NOT be detected as clones.
	for _, c := range clones {
		for _, inst := range c.Instances {
			if inst.StartLine >= 3 && inst.EndLine <= 6 {
				t.Errorf("clone detected in var declaration (lines %d-%d) — should be filtered by function-level detection",
					inst.StartLine, inst.EndLine)
			}
		}
	}
}

func tokenText(tok Token) string {
	if tok.Text != "" {
		return tok.Text
	}
	if tok.OrigText != "" {
		return tok.OrigText
	}
	return strings.TrimSpace(string(rune(tok.Kind + '0')))
}
