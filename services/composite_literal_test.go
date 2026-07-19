package services

import (
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

// Tests for the composite-literal mask (issue #31). The mask decides which
// tokens are data rather than logic, so both directions matter: masking a
// control-flow block would blind the detector, and failing to mask a data table
// brings the false positives back.

// maskProbe pairs a tokenized fixture with its data-literal spans so a test can
// ask whether a specific, distinctive token was classified as table data.
type maskProbe struct {
	tokens []Token
	mask   []bool
}

// spansToMask expands token spans into a per-token bool, which makes the
// assertions below read in terms of individual tokens.
func spansToMask(n int, spans []TokenSpan) []bool {
	mask := make([]bool, n)
	for _, s := range spans {
		for i := s.Start; i <= s.End && i < n; i++ {
			mask[i] = true
		}
	}
	return mask
}

// probeLiterals reports the raw data-literal spans, before function literals
// nested inside a table are carved back out.
func probeLiterals(src string) maskProbe {
	tokens := TokenizeFile(src, goL())
	return maskProbe{tokens: tokens, mask: spansToMask(len(tokens), findDataLiteralSpans(tokens, goL()))}
}

// probeDetectionMask reports what the detector actually treats as table data:
// inside a data literal, but not inside a function literal nested in one.
func probeDetectionMask(src string) maskProbe {
	return probeDetectionMaskLang(src, goL())
}

func probeDetectionMaskLang(src string, lang *domain.Language) maskProbe {
	tokens := TokenizeFile(src, lang)
	_, funcs := markFunctionBodies(tokens, lang)
	literals := findDataLiteralSpans(tokens, lang)
	data := spansToMask(len(tokens), literals)
	for _, fl := range nestedFuncLiterals(findFuncLiteralBodies(tokens, lang, funcs), literals) {
		for i := fl.Start; i <= fl.End && i < len(data); i++ {
			data[i] = false
		}
	}
	return maskProbe{tokens: tokens, mask: data}
}

// wantMasked asserts that every occurrence of a source token is classified as
// table data (or, for want=false, that none of them are). The fixtures use
// distinctive names so a probe identifies exactly one region of interest.
func (p maskProbe) wantMasked(t *testing.T, text string, want bool) {
	t.Helper()
	found := 0
	for i, tok := range p.tokens {
		s := tok.Text
		if s == "" {
			s = tok.OrigText
		}
		if s != text {
			continue
		}
		found++
		if p.mask[i] != want {
			t.Errorf("token %q (occurrence %d, line %d): data=%v, want data=%v",
				text, found, tok.Line, p.mask[i], want)
		}
	}
	if found == 0 {
		t.Fatalf("probe token %q never appears in the fixture — the test is not asserting anything", text)
	}
}

func TestMarkCompositeLiterals_DataLiteralsAreMasked(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		probe string
	}{
		{
			name:  "slice of named type",
			src:   "package p\nfunc f() {\n\tx := []string{ALPHA, BRAVO}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "slice of anonymous struct (table-driven test shape)",
			src:   "package p\nfunc f() {\n\tx := []struct{ n int }{{n: ALPHA}, {n: BRAVO}}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "map literal",
			src:   "package p\nfunc f() {\n\tx := map[string]int{ALPHA: 1, BRAVO: 2}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "map of slices",
			src:   "package p\nfunc f() {\n\tx := map[string][]int{ALPHA: {1, 2}}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "slice of pointers",
			src:   "package p\nfunc f() {\n\tx := []*Thing{{Name: ALPHA}}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "slice of slices",
			src:   "package p\nfunc f() {\n\tx := [][]int{{ALPHA}, {BRAVO}}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "fixed-size array",
			src:   "package p\nfunc f() {\n\tx := [3]int{ALPHA, BRAVO, CHARLIE}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "ellipsis array",
			src:   "package p\nfunc f() {\n\tx := [...]int{ALPHA, BRAVO}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "qualified element type",
			src:   "package p\nfunc f() {\n\tx := []time.Duration{ALPHA, BRAVO}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "generic element type",
			src:   "package p\nfunc f() {\n\tx := []Set[int]{{ALPHA}, {BRAVO}}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "slice of interface",
			src:   "package p\nfunc f() {\n\tx := []interface{}{ALPHA, BRAVO}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "literal passed as a call argument",
			src:   "package p\nfunc f() {\n\tprocess([]int{ALPHA, BRAVO})\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "address-of literal",
			src:   "package p\nfunc f() {\n\tx := &[]int{ALPHA}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "literal returned directly",
			src:   "package p\nfunc f() []int {\n\treturn []int{ALPHA, BRAVO}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "nested literal inside an outer literal",
			src:   "package p\nfunc f() {\n\tx := []Row{{Vals: []int{ALPHA}}}\n}\n",
			probe: "ALPHA",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			probeLiterals(tc.src).wantMasked(t, tc.probe, true)
		})
	}
}

func TestMarkCompositeLiterals_LogicIsNotMasked(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		probe string
	}{
		{
			name:  "map index guarding an if block",
			src:   "package p\nfunc f() {\n\tif m[k] {\n\t\tALPHA()\n\t}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "slice index in a range statement",
			src:   "package p\nfunc f() {\n\tfor _, v := range m[k] {\n\t\tALPHA(v)\n\t}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "index expression in a comparison",
			src:   "package p\nfunc f() {\n\tif arr[i] > 0 {\n\t\tALPHA()\n\t}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "index on a call result",
			src:   "package p\nfunc f() {\n\tif rows()[0] {\n\t\tALPHA()\n\t}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "plain struct literal is left alone",
			src:   "package p\nfunc f() {\n\tx := Config{Name: ALPHA, Port: 8080}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "for loop body",
			src:   "package p\nfunc f() {\n\tfor i := 0; i < 10; i++ {\n\t\tALPHA(i)\n\t}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "switch body",
			src:   "package p\nfunc f() {\n\tswitch v {\n\tcase 1:\n\t\tALPHA()\n\t}\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "func literal assigned to a variable",
			src:   "package p\nfunc f() {\n\tg := func() {\n\t\tALPHA()\n\t}\n\tg()\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "statement following a literal",
			src:   "package p\nfunc f() {\n\tx := []int{1, 2, 3}\n\tALPHA(x)\n}\n",
			probe: "ALPHA",
		},
		{
			name:  "brace inside a string literal",
			src:   "package p\nfunc f() {\n\ts := \"{not a brace\"\n\tALPHA(s)\n}\n",
			probe: "ALPHA",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			probeLiterals(tc.src).wantMasked(t, tc.probe, false)
		})
	}
}

// Barrier cases: the mask must survive degenerate and malformed input without
// panicking and without swallowing the rest of the file.
func TestMarkCompositeLiterals_BarrierCases(t *testing.T) {
	t.Run("empty literal", func(t *testing.T) {
		probeLiterals("package p\nfunc f() {\n\tx := []int{}\n\tALPHA(x)\n}\n").
			wantMasked(t, "ALPHA", false)
	})

	t.Run("empty struct literal", func(t *testing.T) {
		probeLiterals("package p\nfunc f() {\n\tx := struct{}{}\n\tALPHA(x)\n}\n").
			wantMasked(t, "ALPHA", false)
	})

	t.Run("unterminated literal does not mask trailing code", func(t *testing.T) {
		// A truncated file: the literal never closes, so nothing balances it.
		// The mask must decline rather than run to the end of the stream.
		p := probeLiterals("package p\nfunc f() {\n\tx := []int{1, 2\n")
		for i, m := range p.mask {
			if m {
				t.Fatalf("unbalanced literal masked token %d; expected no mask at all", i)
			}
		}
	})

	t.Run("literal at the very start of the stream", func(t *testing.T) {
		// No preceding token, so the "is this an index expression" check has
		// nothing to look back at.
		probeLiterals("[]int{ALPHA}").wantMasked(t, "ALPHA", true)
	})

	t.Run("empty token stream", func(t *testing.T) {
		if got := findDataLiteralSpans(nil, goL()); len(got) != 0 {
			t.Errorf("spans for empty stream = %d, want 0", len(got))
		}
	})

	t.Run("nil language is a no-op", func(t *testing.T) {
		// Scanning tokenizes with a resolved language, but markFunctionBodies
		// treats a nil language as "no syntax knowledge, mark everything";
		// literal discovery must be equally defensive rather than guessing.
		tokens := TokenizeFile("package p\nfunc f() {\n\tx := []int{1}\n}\n", goL())
		if got := findDataLiteralSpans(tokens, nil); len(got) != 0 {
			t.Errorf("nil language produced %d spans; discovery requires known syntax", len(got))
		}
	})

	t.Run("nil language lexes instead of panicking", func(t *testing.T) {
		// markFunctionBodies documents nil as "no syntax knowledge, mark
		// everything", but TokenizeFile used to dereference it and panic on the
		// keyword lookup. The scanner drops unknown-language files before
		// reaching here, so this is about the contract being coherent.
		src := "package p\nfunc f() {\n\tx := 1\n}\n"
		tokens := TokenizeFile(src, nil)
		if len(tokens) == 0 {
			t.Fatal("nil language produced no tokens")
		}
		tf := BuildTokenizedFile("unknown.xyz", src, nil)
		if len(tf.Tokens) != len(tokens) {
			t.Errorf("BuildTokenizedFile produced %d tokens, TokenizeFile %d", len(tf.Tokens), len(tokens))
		}
	})

	t.Run("opted-out language is a no-op", func(t *testing.T) {
		// Java is deliberately excluded: `[` there is an index or an array type,
		// and its array initializers use `{`, which is also a block.
		src := "class C {\n  void f() {\n    int[] xs = {1, 2, 3};\n    if (xs[0] > 0) { g(); }\n  }\n}\n"
		lang := LangForName("java")
		tokens := TokenizeFile(src, lang)
		if got := findDataLiteralSpans(tokens, lang); len(got) != 0 {
			t.Errorf("java source produced %d spans; the language is opted out", len(got))
		}
	})
}

// A table whose fields hold real logic must keep that logic visible: only the
// surrounding data rows are excluded.
func TestMaskDataLiterals_FuncLiteralsInsideTablesStayVisible(t *testing.T) {
	src := `package p

func f() {
	tests := []struct {
		name  string
		setup func()
	}{
		{
			name: ROWNAME,
			setup: func() {
				ALPHA()
				BRAVO()
			},
		},
	}
	_ = tests
}
`
	p := probeDetectionMask(src)
	// The data column is excluded...
	p.wantMasked(t, "ROWNAME", true)
	// ...but the logic carried in the func-literal field is not.
	p.wantMasked(t, "ALPHA", false)
	p.wantMasked(t, "BRAVO", false)
}

// A `func` inside a table is either a field's TYPE (data — stays masked) or a
// literal supplying a value (logic — its body re-opens). Getting this backwards
// either hides real duplication or lets a whole table back through, so each
// signature shape is pinned down.
func TestMaskDataLiterals_FuncTypeVersusFuncLiteral(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// probe sits inside the func region; wantMasked says whether that
		// region is data (true) or logic that must stay visible (false).
		probe      string
		wantMasked bool
	}{
		{
			name:       "field type with no body stays data",
			src:        "package p\nfunc f() {\n\tx := []struct{ setup func(PROBE int) }{{}}\n\t_ = x\n}\n",
			probe:      "PROBE",
			wantMasked: true,
		},
		{
			name:       "field type with a result stays data",
			src:        "package p\nfunc f() {\n\tx := []struct{ run func() PROBE }{{}}\n\t_ = x\n}\n",
			probe:      "PROBE",
			wantMasked: true,
		},
		{
			name:       "literal with no result re-opens",
			src:        "package p\nfunc f() {\n\tx := []Case{{fn: func() { PROBE() }}}\n\t_ = x\n}\n",
			probe:      "PROBE",
			wantMasked: false,
		},
		{
			name:       "literal with a single result re-opens",
			src:        "package p\nfunc f() {\n\tx := []Case{{fn: func() error { PROBE(); return nil }}}\n\t_ = x\n}\n",
			probe:      "PROBE",
			wantMasked: false,
		},
		{
			name:       "literal with parenthesized results re-opens",
			src:        "package p\nfunc f() {\n\tx := []Case{{fn: func() (int, error) { PROBE(); return 0, nil }}}\n\t_ = x\n}\n",
			probe:      "PROBE",
			wantMasked: false,
		},
		{
			name:       "literal taking arguments re-opens",
			src:        "package p\nfunc f() {\n\tx := []Case{{fn: func(t *testing.T) { PROBE(t) }}}\n\t_ = x\n}\n",
			probe:      "PROBE",
			wantMasked: false,
		},
		{
			name:       "nested literal inside a re-opened body stays visible",
			src:        "package p\nfunc f() {\n\tx := []Case{{fn: func() { g(func() { PROBE() }) }}}\n\t_ = x\n}\n",
			probe:      "PROBE",
			wantMasked: false,
		},
		{
			name:       "the row data around a re-opened body stays masked",
			src:        "package p\nfunc f() {\n\tx := []Case{{name: PROBE, fn: func() { g() }}}\n\t_ = x\n}\n",
			probe:      "PROBE",
			wantMasked: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			probeDetectionMask(tc.src).wantMasked(t, tc.probe, tc.wantMasked)
		})
	}
}

// End-to-end at the detector level: duplicated logic carried in table func
// fields is still real duplication and must be reported.
func TestDetect_DuplicatedLogicInTableFuncFieldsStillReported(t *testing.T) {
	body := `
			srv := newServer(t)
			defer srv.Close()
			req := build(t, srv.URL)
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("status %d", resp.StatusCode)
			}
			checkHeaders(t, resp)
			checkTrailers(t, resp)
`
	src := `package p

import "testing"

func TestCases(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "alpha",
			run: func(t *testing.T) {` + body + `},
		},
		{
			name: "bravo",
			run: func(t *testing.T) {` + body + `},
		},
	}
	_ = cases
}
`
	clones := Detect([]TokenizedFile{makeFile("cases_test.go", src)}, 50, 1.0)
	if len(clones) == 0 {
		t.Fatal("copy-pasted logic inside table func fields must still be reported")
	}
	assertNoOverlappingInstances(t, clones)
}

// Data literals must stay INSIDE function scope. Excluding their tokens from
// InFunc was the tempting fix and it is wrong: a small inline literal sitting in
// the middle of a duplicated block would split that block in two and lose the
// finding. The literal is recorded as a span instead, and the detector uses the
// span to judge whole clone groups.
func TestBuildTokenizedFile_LiteralTokensStayInFunctionScope(t *testing.T) {
	src := "package p\nfunc f() {\n\tx := []int{ALPHA, BRAVO}\n\tCHARLIE(x)\n}\n"
	tf := BuildTokenizedFile("a.go", src, goL())

	for _, probe := range []string{"ALPHA", "CHARLIE"} {
		found := false
		for i, tok := range tf.Tokens {
			s := tok.Text
			if s == "" {
				s = tok.OrigText
			}
			if s != probe {
				continue
			}
			found = true
			if !tf.InFunc[i] {
				t.Errorf("token %q left function scope; literals must stay visible to detection", probe)
			}
		}
		if !found {
			t.Fatalf("probe token %q not found in fixture", probe)
		}
	}

	if len(tf.DataLiterals) != 1 {
		t.Fatalf("recorded %d data literal spans, want 1", len(tf.DataLiterals))
	}
}

// The regression that drove the span-based design: a short inline literal in the
// middle of two otherwise-identical blocks must not break them apart.
func TestDetect_InlineLiteralDoesNotSplitDuplicatedBlock(t *testing.T) {
	body := `
	dir := t.TempDir()
	a := makeThing(t, dir, "a.go", "package main")
	b := makeThing(t, dir, "b.go", "package main")
	results := Combine([]Thing{a, b}, 10, 0.5)
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	for _, r := range results {
		if r.Score < 0 {
			t.Errorf("negative score: %v", r)
		}
	}
`
	src := "package p\n\nimport \"testing\"\n\nfunc TestAlpha(t *testing.T) {" + body + "}\n\nfunc TestBravo(t *testing.T) {" + body + "}\n"

	clones := Detect([]TokenizedFile{makeFile("split_test.go", src)}, 50, 1.0)
	if len(clones) == 0 {
		t.Fatal("duplicated block containing an inline []Thing{a, b} literal must still be reported")
	}
	assertNoOverlappingInstances(t, clones)
}
