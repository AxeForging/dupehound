package services

import (
	"strings"
	"testing"
)

// spanNames tokenizes src for the named language and returns the non-empty
// function names collected by markFunctionBodies, in extraction order.
func spanNames(t *testing.T, langName, src string) []string {
	t.Helper()
	lang := LangForName(langName)
	if lang == nil {
		t.Fatalf("unknown language %q", langName)
	}
	tokens := TokenizeFile(src, lang)
	_, funcs := markFunctionBodies(tokens, lang)
	var names []string
	for _, f := range funcs {
		if f.Name != "" {
			names = append(names, f.Name)
		}
	}
	return names
}

func assertNames(t *testing.T, got []string, want ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, n := range got {
		set[n] = true
	}
	for _, w := range want {
		if !set[w] {
			t.Errorf("missing function name %q; extracted: %v", w, got)
		}
	}
}

func TestFuncName_Go(t *testing.T) {
	src := `package main

func Plain(a int) int {
	return a + 1
}

func (s *Server) Handle(w io.Writer) {
	s.log(w)
}
`
	assertNames(t, spanNames(t, "go", src), "Plain", "Handle")
}

func TestFuncName_Go_AnonymousLiteralFallsBackToEnclosing(t *testing.T) {
	src := `package main

func Outer() {
	cb := func(x int) int {
		return x * 2
	}
	cb(1)
}
`
	lang := LangForName("go")
	tokens := TokenizeFile(src, lang)
	_, funcs := markFunctionBodies(tokens, lang)
	// Find the token for the literal `2` (inside the anonymous func).
	for i, tok := range tokens {
		if tok.OrigText == "2" {
			if got := enclosingFuncName(funcs, i); got != "Outer" {
				t.Errorf("anonymous literal should attribute to Outer, got %q", got)
			}
			return
		}
	}
	t.Fatal("token `2` not found")
}

func TestFuncName_JavaScript(t *testing.T) {
	src := `
function declared(a) {
	return a + 1;
}
const arrow = (a, b) => {
	return a + b;
};
const single = x => {
	return x;
};
const assigned = function(a) {
	return a - 1;
};
class Widget {
	compute(v) {
		return v * 2;
	}
}
const obj = {
	handler: (e) => {
		e.stop();
	},
};
`
	assertNames(t, spanNames(t, "javascript", src),
		"declared", "arrow", "single", "assigned", "compute", "handler")
}

func TestFuncName_TypeScript(t *testing.T) {
	src := `
const typedArrow: Handler = (req: Request): Response => {
	return handle(req);
};
async function fetchAll(ids: string[]): Promise<void> {
	await load(ids);
}
export class Repo {
	find(id: string): Item {
		return this.items[id];
	}
}
`
	assertNames(t, spanNames(t, "typescript", src), "typedArrow", "fetchAll", "find")
}

func TestFuncName_Python(t *testing.T) {
	src := `
def top_level(a, b):
    return a + b

async def fetch_all(session):
    return await session.get()
`
	assertNames(t, spanNames(t, "python", src), "top_level", "fetch_all")
}

func TestFuncName_Ruby(t *testing.T) {
	src := `
def plain_method(a)
  a + 1
end

def self.factory(kind)
  new(kind)
end
`
	assertNames(t, spanNames(t, "ruby", src), "plain_method", "self.factory")
}

func TestFuncName_Elixir(t *testing.T) {
	src := `
defmodule Demo do
  def add(a, b) do
    a + b
  end

  defp helper(x) do
    x * 2
  end
end
`
	assertNames(t, spanNames(t, "elixir", src), "add", "helper")
}

func TestFuncName_Java(t *testing.T) {
	src := `
public class Service {
	public int compute(int a, int b) {
		return a + b;
	}

	private void log(String msg) {
		System.out.println(msg);
	}
}
`
	assertNames(t, spanNames(t, "java", src), "compute", "log")
}

func TestFuncName_C(t *testing.T) {
	src := `
#include <stdio.h>

int add(int a, int b) {
	return a + b;
}

static void print_all(int *vals, int n) {
	for (int i = 0; i < n; i++) printf("%d", vals[i]);
}
`
	assertNames(t, spanNames(t, "c", src), "add", "print_all")
}

func TestFuncName_Cpp_QualifiedMethod(t *testing.T) {
	src := `
int Foo::bar(int x) {
	return x + 1;
}
`
	assertNames(t, spanNames(t, "cpp", src), "bar")
}

func TestFuncName_Rust(t *testing.T) {
	src := `
fn compute(a: i32) -> i32 {
	a + 1
}

pub fn public_api(v: Vec<u8>) -> usize {
	v.len()
}
`
	assertNames(t, spanNames(t, "rust", src), "compute", "public_api")
}

func TestFuncName_Lua_DottedAndColon(t *testing.T) {
	src := `
function plain(a)
  return a + 1
end

function obj.helper(a)
  return a - 1
end

function obj:method(a)
  return a * 2
end
`
	assertNames(t, spanNames(t, "lua", src), "plain", "obj.helper", "obj:method")
}

func TestFuncName_R_AssignmentStyle(t *testing.T) {
	src := `
normalize <- function(x) {
  (x - mean(x)) / sd(x)
}
`
	assertNames(t, spanNames(t, "r", src), "normalize")
}

func TestFuncName_SQL(t *testing.T) {
	src := `
CREATE FUNCTION get_total(id INT) RETURNS INT
BEGIN
  RETURN (SELECT SUM(amount) FROM orders WHERE user_id = id);
END;
`
	assertNames(t, spanNames(t, "sql", src), "get_total")
}

// TestFuncName_EndToEnd_CloneAttribution is the integration check: detected
// clone instances carry the enclosing function name of each instance.
func TestFuncName_EndToEnd_CloneAttribution(t *testing.T) {
	body := "\tx := compute()\n\tvalidate(x)\n\ttransform(x)\n\tpersist(x)\n\tnotify(x)\n\treturn x\n"
	a := BuildTokenizedFile("/a.go", "package main\n\nfunc LoadUser() int {\n"+body+"}\n", LangForName("go"))
	b := BuildTokenizedFile("/b.go", "package main\n\nfunc LoadOrder() int {\n"+body+"}\n", LangForName("go"))

	clones := Detect([]TokenizedFile{a, b}, 15, 1.0)
	if len(clones) == 0 {
		t.Fatal("expected a clone")
	}
	var funcs []string
	for _, inst := range clones[0].Instances {
		funcs = append(funcs, inst.Function)
	}
	joined := strings.Join(funcs, ",")
	if !strings.Contains(joined, "LoadUser") || !strings.Contains(joined, "LoadOrder") {
		t.Errorf("instances should be attributed to LoadUser and LoadOrder, got %v", funcs)
	}
}

func TestEnclosingFuncName_InnermostNamedWins(t *testing.T) {
	spans := []FuncSpan{
		{Start: 0, End: 100, Name: "outer"},
		{Start: 10, End: 50, Name: "inner"},
		{Start: 20, End: 30, Name: ""}, // anonymous innermost
	}
	if got := enclosingFuncName(spans, 25); got != "inner" {
		t.Errorf("pos 25: want inner (innermost NAMED), got %q", got)
	}
	if got := enclosingFuncName(spans, 60); got != "outer" {
		t.Errorf("pos 60: want outer, got %q", got)
	}
	if got := enclosingFuncName(spans, 200); got != "" {
		t.Errorf("pos 200: want empty, got %q", got)
	}
}
