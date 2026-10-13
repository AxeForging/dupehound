package services

import (
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

// Regression tests for issue #31: idiomatic Go table-driven tests were reported
// as type-2 clones because (a) composite data literals inside function bodies
// were treated as logic, and (b) same-file clone instances were allowed to
// overlap each other after greedy extension.
//
// The fixtures here are deliberately shaped like real code: gofmt-style
// multi-line rows, one-line rows, nested literals, and — for the negative
// cases — genuine copy-pasted logic that MUST still be reported.

// issue31Repro is the reproduction from the issue report, verbatim in shape:
// a ten-row table-driven test with one-line rows.
const issue31Repro = `package repro

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		absent  string
		present string
	}{
		{name: "openai preset case", input: "token openai_AAAA0\n", absent: "openai_AAAA", present: "openai_***"},
		{name: "anthropic preset case", input: "token anthropic_AAAA1\n", absent: "anthropic_AAAA", present: "anthropic_***"},
		{name: "github preset case", input: "token github_AAAA2\n", absent: "github_AAAA", present: "github_***"},
		{name: "awskey preset case", input: "token awskey_AAAA3\n", absent: "awskey_AAAA", present: "awskey_***"},
		{name: "bearer preset case", input: "token bearer_AAAA4\n", absent: "bearer_AAAA", present: "bearer_***"},
		{name: "stripe preset case", input: "token stripe_AAAA5\n", absent: "stripe_AAAA", present: "stripe_***"},
		{name: "gcp preset case", input: "token gcp_AAAA6\n", absent: "gcp_AAAA", present: "gcp_***"},
		{name: "azure preset case", input: "token azure_AAAA7\n", absent: "azure_AAAA", present: "azure_***"},
		{name: "npm preset case", input: "token npm_AAAA8\n", absent: "npm_AAAA", present: "npm_***"},
		{name: "slack preset case", input: "token slack_AAAA9\n", absent: "slack_AAAA", present: "slack_***"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Contains(tc.present, tc.absent) {
				t.Errorf("%s", tc.name)
			}
		})
	}
}
`

// gofmtStyleTable uses one field per line, which is what gofmt produces for
// larger literals. The issue notes the effect is stronger in this shape
// because each row spans many more tokens and lines.
const gofmtStyleTable = `package repro

import "testing"

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{
			name:    "empty input is rejected",
			input:   "",
			want:    0,
			wantErr: true,
		},
		{
			name:    "single element parses",
			input:   "a",
			want:    1,
			wantErr: false,
		},
		{
			name:    "two elements parse",
			input:   "a,b",
			want:    2,
			wantErr: false,
		},
		{
			name:    "three elements parse",
			input:   "a,b,c",
			want:    3,
			wantErr: false,
		},
		{
			name:    "trailing comma is rejected",
			input:   "a,",
			want:    0,
			wantErr: true,
		},
		{
			name:    "leading comma is rejected",
			input:   ",a",
			want:    0,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		got, err := Validate(tc.input)
		if (err != nil) != tc.wantErr {
			t.Fatalf("%s: err = %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %d want %d", tc.name, got, tc.want)
		}
	}
}
`

func TestDetect_Issue31_TableDrivenTestNotReported(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "one-line rows (issue #31 repro)", src: issue31Repro},
		{name: "gofmt multi-line rows", src: gofmtStyleTable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := []TokenizedFile{makeFile("table_test.go", tc.src)}
			clones := Detect(files, 50, 0.70)
			if len(clones) != 0 {
				t.Errorf("table-driven test data reported as %d clone(s); a single data table is not duplicated logic", len(clones))
				for _, c := range clones {
					t.Logf("  %s similarity=%.2f tokens=%d instances=%d", c.Type, c.Similarity, c.TokenCount, len(c.Instances))
					for _, in := range c.Instances {
						t.Logf("    %s:%d-%d", in.File, in.StartLine, in.EndLine)
					}
				}
			}
		})
	}
}

// A clone's instances describe distinct occurrences of the same block. If two
// instances of one group overlap, they are the same text counted twice — never
// a real duplicate. This invariant is asserted broadly across the suite via
// assertNoOverlappingInstances.
func TestDetect_Issue31_InstancesNeverOverlapWithinAFile(t *testing.T) {
	// A long, highly periodic function body: greedy extension grows blocks
	// past the seed window spacing, which is exactly how overlapping
	// instances were produced.
	src := "package main\n\nfunc run() {\n"
	for i := 0; i < 40; i++ {
		src += "\tstep(a, b)\n\tcheck(a, b)\n\temit(a, b)\n"
	}
	src += "}\n"

	files := []TokenizedFile{makeFile("periodic.go", src)}
	clones := Detect(files, 50, 0.70)
	assertNoOverlappingInstances(t, clones)
}

// Negative case — the fix must not blind the detector to real duplication that
// happens to live in a test file next to a table.
func TestDetect_Issue31_RealDuplicationInTestFilesStillReported(t *testing.T) {
	dup := `
	server := newServer(t)
	defer server.Close()
	client := server.Client()
	req := buildRequest(t, server.URL)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
`
	src := "package repro\n\nimport \"testing\"\n\nfunc TestAlpha(t *testing.T) {" + dup + "}\n\nfunc TestBeta(t *testing.T) {" + dup + "}\n"

	files := []TokenizedFile{makeFile("dup_test.go", src)}
	clones := Detect(files, 50, 0.70)
	if len(clones) == 0 {
		t.Fatal("genuine copy-pasted test setup logic must still be reported")
	}
	assertNoOverlappingInstances(t, clones)
}

// A table followed by genuinely duplicated logic in the same file: excluding
// the literal must not swallow the logic that follows it.
func TestDetect_Issue31_LogicAfterTableStillReported(t *testing.T) {
	body := `
	cfg := load(path)
	if cfg == nil {
		t.Fatal("nil config")
	}
	conn, err := dial(cfg.Addr, cfg.Timeout, cfg.Retries)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
`
	src := `package repro

import "testing"

func TestWithTable(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		{name: "alpha", want: 1},
		{name: "bravo", want: 2},
		{name: "charlie", want: 3},
		{name: "delta", want: 4},
		{name: "echo", want: 5},
		{name: "foxtrot", want: 6},
	}
	_ = cases
` + body + `}

func TestPlain(t *testing.T) {` + body + `}
`

	files := []TokenizedFile{makeFile("mixed_test.go", src)}
	clones := Detect(files, 50, 0.70)
	if len(clones) == 0 {
		t.Fatal("duplicated logic following a table literal must still be reported")
	}
	// And the reported clone must be the logic, not the table.
	for _, c := range clones {
		for _, in := range c.Instances {
			for _, line := range in.Lines {
				if containsAny(line, `{name: "alpha"`, `{name: "bravo"`) {
					t.Errorf("clone instance %s:%d-%d covers table literal rows: %q", in.File, in.StartLine, in.EndLine, line)
				}
			}
		}
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

// assertNoOverlappingInstances enforces the core invariant: within one clone
// group, no two instances in the same file may overlap.
func assertNoOverlappingInstances(t *testing.T, clones []domain.Clone) {
	t.Helper()
	for _, c := range clones {
		for i := range c.Instances {
			for j := i + 1; j < len(c.Instances); j++ {
				a, b := c.Instances[i], c.Instances[j]
				if a.File != b.File {
					continue
				}
				if a.StartLine <= b.EndLine && b.StartLine <= a.EndLine {
					t.Errorf("clone %s has overlapping instances in %s: %d-%d and %d-%d",
						c.Hash, a.File, a.StartLine, a.EndLine, b.StartLine, b.EndLine)
				}
			}
		}
	}
}
