package services

import "testing"

// Regression tests for Python function-boundary detection, found while adding
// multi-language coverage for issue #31.
//
// markPythonFunctions ends a body when it meets the next blank-line-separated
// `def`, then used to resume the outer scan AFTER that keyword — stepping over
// it. Every definition following the first was therefore never marked as a
// function body, and since detection only looks inside function bodies, all
// duplication in those functions was invisible.

// funcNames returns the names of the function spans found in src.
func pyFuncNames(t *testing.T, src string) []string {
	t.Helper()
	lang := LangForName("python")
	tokens := TokenizeFile(src, lang)
	_, funcs := markFunctionBodies(tokens, lang)
	names := make([]string, 0, len(funcs))
	for _, f := range funcs {
		names = append(names, f.Name)
	}
	return names
}

func TestMarkPythonFunctions_FindsEveryDefinition(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "two blank-line separated defs",
			src:  "def alpha():\n    a = 1\n    return a\n\ndef bravo():\n    b = 2\n    return b\n",
			want: []string{"alpha", "bravo"},
		},
		{
			name: "four defs in a row",
			src: "def alpha():\n    return 1\n\ndef bravo():\n    return 2\n\n" +
				"def charlie():\n    return 3\n\ndef delta():\n    return 4\n",
			want: []string{"alpha", "bravo", "charlie", "delta"},
		},
		{
			name: "async def between sync defs",
			src:  "def alpha():\n    return 1\n\nasync def bravo():\n    return 2\n\ndef charlie():\n    return 3\n",
			want: []string{"alpha", "bravo", "charlie"},
		},
		{
			name: "def following a class",
			src:  "class Thing:\n    pass\n\ndef alpha():\n    return 1\n\ndef bravo():\n    return 2\n",
			want: []string{"alpha", "bravo"},
		},
		{
			name: "single def is unaffected",
			src:  "def alpha():\n    a = 1\n    return a\n",
			want: []string{"alpha"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pyFuncNames(t, tc.src)
			if len(got) != len(tc.want) {
				t.Fatalf("found %d functions %v, want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("function %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// The behaviour that actually matters: duplication in the second function is
// reported. Before the fix its body was never in scope, so this found nothing.
func TestDetect_PythonDuplicationInLaterFunctions(t *testing.T) {
	body := `    server = start_server()
    client = server.client()
    request = build_request(server.url)
    response = client.send(request)
    assert response.status == 200
    body = response.read_all()
    assert body is not None
    server.close()
    log_result(body)
`
	src := "def test_alpha():\n" + body + "\ndef test_bravo():\n" + body

	tf := BuildTokenizedFile("things_test.py", src, LangForName("python"))
	clones := Detect([]TokenizedFile{tf}, 50, 1.0)
	if len(clones) == 0 {
		t.Fatal("copy-pasted body in the second Python function must be reported")
	}
	assertNoOverlappingInstances(t, clones)
}

// A whole-file check: with several duplicated functions, every one of them is in
// scope, not just the first.
func TestMarkPythonFunctions_AllBodiesEnterScope(t *testing.T) {
	src := "def alpha():\n    x = compute()\n    return x\n\n" +
		"def bravo():\n    y = compute()\n    return y\n\n" +
		"def charlie():\n    z = compute()\n    return z\n"

	lang := LangForName("python")
	tf := BuildTokenizedFile("m.py", src, lang)

	// Each body contains a `compute` call; all three must be inside a function.
	seen := 0
	for i, tok := range tf.Tokens {
		if tok.OrigText != "compute" {
			continue
		}
		seen++
		if !tf.InFunc[i] {
			t.Errorf("compute call on line %d is not inside a function body", tok.Line)
		}
	}
	if seen != 3 {
		t.Fatalf("found %d compute calls, want 3", seen)
	}
}
