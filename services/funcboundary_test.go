package services

import (
	"fmt"
	"testing"
)

// --- helpers ---

func tokenText(tok Token) string {
	if tok.Text != "" {
		return tok.Text
	}
	if tok.OrigText != "" {
		return tok.OrigText
	}
	return fmt.Sprintf("kind-%d", tok.Kind)
}

// assertBodyMarked checks that at least one token on bodyLine is marked InFunc=true.
func assertBodyMarked(t *testing.T, lang string, tf TokenizedFile, bodyLine int) {
	t.Helper()
	for i, tok := range tf.Tokens {
		if tok.Line == bodyLine && tf.InFunc[i] {
			return
		}
	}
	t.Errorf("%s: no token on line %d marked as in-function", lang, bodyLine)
}

// assertLineExcluded checks that no token on the given line is marked InFunc=true.
func assertLineExcluded(t *testing.T, lang string, tf TokenizedFile, line int, desc string) {
	t.Helper()
	for i, tok := range tf.Tokens {
		if tok.Line == line && tf.InFunc[i] {
			t.Errorf("%s: token %q on line %d (%s) should NOT be in function", lang, tokenText(tok), line, desc)
			return
		}
	}
}

// --- nil language ---

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

// --- Go ---

func TestFuncBoundary_Go(t *testing.T) {
	src := `package main

var keywords = map[string]bool{
	"break": true,
}

func process() {
	result := compute()
}
`
	tf := BuildTokenizedFile("a.go", src, LangForName("go"))
	assertLineExcluded(t, "go", tf, 4, "var declaration")
	assertBodyMarked(t, "go", tf, 8)
}

// --- Python ---

func TestFuncBoundary_Python(t *testing.T) {
	src := `COLORS = ["red", "green", "blue"]

def process(data):
    result = transform(data)
    return result

ANOTHER = "value"
`
	tf := BuildTokenizedFile("a.py", src, LangForName("python"))
	assertLineExcluded(t, "python", tf, 1, "top-level assignment")
	assertBodyMarked(t, "python", tf, 4)
}

// --- JavaScript ---

func TestFuncBoundary_JavaScript(t *testing.T) {
	src := `const CONFIG = {
  host: "localhost",
  port: 3000,
};

function process(data) {
  const result = transform(data);
  return result;
}
`
	tf := BuildTokenizedFile("a.js", src, LangForName("javascript"))
	assertLineExcluded(t, "javascript", tf, 2, "const object")
	assertBodyMarked(t, "javascript", tf, 7)
}

// --- TypeScript ---

func TestFuncBoundary_TypeScript(t *testing.T) {
	src := `interface User {
  name: string;
  age: number;
}

function getUser(): User {
  return { name: "test", age: 25 };
}
`
	tf := BuildTokenizedFile("a.ts", src, LangForName("typescript"))
	assertLineExcluded(t, "typescript", tf, 2, "interface field")
	assertBodyMarked(t, "typescript", tf, 7)
}

// --- Java ---

func TestFuncBoundary_Java(t *testing.T) {
	src := `public class App {
    private static final int MAX = 100;

    public void process() {
        int result = compute();
        validate(result);
    }
}
`
	tf := BuildTokenizedFile("App.java", src, LangForName("java"))
	assertBodyMarked(t, "java", tf, 5)
}

// --- Kotlin ---

func TestFuncBoundary_Kotlin(t *testing.T) {
	src := `val MAX = 100

fun process(data: String): String {
    val result = transform(data)
    return result
}

val ANOTHER = "value"
`
	tf := BuildTokenizedFile("a.kt", src, LangForName("kotlin"))
	assertLineExcluded(t, "kotlin", tf, 1, "top-level val")
	assertBodyMarked(t, "kotlin", tf, 4)
}

// --- Rust ---

func TestFuncBoundary_Rust(t *testing.T) {
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
	assertLineExcluded(t, "rust", tf, 4, "struct field")
	assertBodyMarked(t, "rust", tf, 9)
}

// --- C ---

func TestFuncBoundary_C(t *testing.T) {
	src := `#include <stdio.h>

int MAX = 100;

void process() {
    int result = compute();
    validate(result);
}
`
	tf := BuildTokenizedFile("a.c", src, LangForName("c"))
	assertBodyMarked(t, "c", tf, 6)
}

// --- C++ ---

func TestFuncBoundary_Cpp(t *testing.T) {
	src := `#include <iostream>

const int MAX = 100;

class App {
public:
    void process() {
        int result = compute();
        validate(result);
    }
};
`
	tf := BuildTokenizedFile("a.cpp", src, LangForName("cpp"))
	assertBodyMarked(t, "cpp", tf, 8)
}

// --- C# ---

func TestFuncBoundary_CSharp(t *testing.T) {
	src := `namespace MyApp {
    public class Service {
        private const int MAX = 100;

        public void Process() {
            var result = Compute();
            Validate(result);
        }
    }
}
`
	tf := BuildTokenizedFile("a.cs", src, LangForName("csharp"))
	assertBodyMarked(t, "csharp", tf, 6)
}

// --- Swift ---

func TestFuncBoundary_Swift(t *testing.T) {
	src := `let MAX = 100

struct Config {
    var host: String
    var port: Int
}

func process(data: String) -> String {
    let result = transform(data)
    return result
}
`
	tf := BuildTokenizedFile("a.swift", src, LangForName("swift"))
	assertLineExcluded(t, "swift", tf, 4, "struct field")
	assertBodyMarked(t, "swift", tf, 9)
}

// --- Scala ---

func TestFuncBoundary_Scala(t *testing.T) {
	src := `val MAX = 100

def process(data: String): String = {
  val result = transform(data)
  result
}
`
	tf := BuildTokenizedFile("a.scala", src, LangForName("scala"))
	assertLineExcluded(t, "scala", tf, 1, "top-level val")
	assertBodyMarked(t, "scala", tf, 4)
}

// --- PHP ---

func TestFuncBoundary_PHP(t *testing.T) {
	src := `$MAX = 100;

function process($data) {
    $result = transform($data);
    return $result;
}

$ANOTHER = "value";
`
	tf := BuildTokenizedFile("a.php", src, LangForName("php"))
	assertLineExcluded(t, "php", tf, 1, "top-level variable")
	assertBodyMarked(t, "php", tf, 4)
}

// --- Ruby ---

func TestFuncBoundary_Ruby(t *testing.T) {
	src := `COLORS = ["red", "green", "blue"]

def process(data)
  result = transform(data)
  result
end

ANOTHER = "value"
`
	tf := BuildTokenizedFile("a.rb", src, LangForName("ruby"))
	assertLineExcluded(t, "ruby", tf, 1, "top-level constant")
	assertBodyMarked(t, "ruby", tf, 4)
}

// --- Shell ---

func TestFuncBoundary_Shell(t *testing.T) {
	src := `MAX=100

function process() {
    result=$(compute)
    validate "$result"
}

ANOTHER="value"
`
	tf := BuildTokenizedFile("a.sh", src, LangForName("shell"))
	assertLineExcluded(t, "shell", tf, 1, "top-level variable")
	assertBodyMarked(t, "shell", tf, 4)
}

// --- SQL ---

func TestFuncBoundary_SQL(t *testing.T) {
	src := `CREATE TABLE users (
    id INT PRIMARY KEY,
    name VARCHAR(100)
);

CREATE FUNCTION get_user(uid INT) RETURNS VARCHAR AS $$
BEGIN
    RETURN (SELECT name FROM users WHERE id = uid);
END;
$$ LANGUAGE plpgsql;
`
	tf := BuildTokenizedFile("a.sql", src, LangForName("sql"))
	assertBodyMarked(t, "sql", tf, 8)
}

// --- Lua ---

func TestFuncBoundary_Lua(t *testing.T) {
	src := `local MAX = 100

function process(data)
    local result = transform(data)
    return result
end

local ANOTHER = "value"
`
	tf := BuildTokenizedFile("a.lua", src, LangForName("lua"))
	assertLineExcluded(t, "lua", tf, 1, "top-level local")
	assertBodyMarked(t, "lua", tf, 4)
}

// --- Elixir ---

func TestFuncBoundary_Elixir(t *testing.T) {
	src := `@max 100

def process(data) do
  result = transform(data)
  result
end

@another "value"
`
	tf := BuildTokenizedFile("a.ex", src, LangForName("elixir"))
	assertBodyMarked(t, "elixir", tf, 4)
}

// --- Dart ---

func TestFuncBoundary_Dart(t *testing.T) {
	src := `const int MAX = 100;

class Config {
  final String host;
  final int port;
  Config(this.host, this.port);
}

void process(String data) {
  var result = transform(data);
  print(result);
}
`
	tf := BuildTokenizedFile("a.dart", src, LangForName("dart"))
	assertBodyMarked(t, "dart", tf, 10)
}

// --- R ---

func TestFuncBoundary_R(t *testing.T) {
	src := `MAX <- 100

process <- function(data) {
  result <- transform(data)
  result
}

ANOTHER <- "value"
`
	tf := BuildTokenizedFile("a.r", src, LangForName("r"))
	assertLineExcluded(t, "r", tf, 1, "top-level assignment")
	assertBodyMarked(t, "r", tf, 4)
}

// --- Cross-language detection test ---

func TestDetect_SkipsTopLevelDeclarations(t *testing.T) {
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

	for _, c := range clones {
		for _, inst := range c.Instances {
			if inst.StartLine >= 3 && inst.EndLine <= 6 {
				t.Errorf("clone detected in var declaration (lines %d-%d) — should be filtered", inst.StartLine, inst.EndLine)
			}
		}
	}
}

// --- Go multiple functions test ---

func TestFuncBoundary_Go_MultipleFunctions(t *testing.T) {
	src := `package main

func a() {
	x := 1
}

func b() {
	y := 2
}
`
	tf := BuildTokenizedFile("a.go", src, LangForName("go"))

	bodyCount := 0
	for _, v := range tf.InFunc {
		if v {
			bodyCount++
		}
	}
	if bodyCount == 0 {
		t.Fatal("expected some tokens inside function bodies")
	}

	for i, tok := range tf.Tokens {
		if tok.Kind == TokKeyword && tok.Text == "package" && tf.InFunc[i] {
			t.Error("package keyword should not be inside function body")
		}
	}
}
