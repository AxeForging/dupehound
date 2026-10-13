package services

import (
	"strings"
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

func githubReport() *domain.Report {
	return &domain.Report{
		TotalFiles: 3, ScannedFiles: 3,
		TotalClones: 2, TotalLines: 500, DuplicateLines: 40, DuplicationPct: 8.0,
		SavedLines: 25,
		Clones: []domain.Clone{
			{
				Hash: "aaaa", Type: domain.CloneType2, Similarity: 1, LineCount: 15, TokenCount: 90,
				SavedLines: 15, Scope: domain.ScopeCrossFile,
				Instances: []domain.CloneInstance{
					{File: "/repo/pkg/a.go", StartLine: 10, EndLine: 24, Function: "loadUser"},
					{File: "/repo/pkg/b.go", StartLine: 40, EndLine: 54, Function: "loadOrder"},
				},
			},
			{
				Hash: "bbbb", Type: domain.CloneType3, Similarity: 0.82, LineCount: 10, TokenCount: 50,
				SavedLines: 10, Scope: domain.ScopeSameFile,
				Instances: []domain.CloneInstance{
					{File: "/repo/pkg/c.go", StartLine: 5, EndLine: 14},
					{File: "/repo/pkg/c.go", StartLine: 60, EndLine: 69},
				},
			},
		},
		DeadFunctions: []domain.DeadFunc{
			{File: "/repo/pkg/dead.go", Line: 33, Name: "unusedHelper", Language: "go"},
		},
	}
}

func TestFormatGitHub_Annotations(t *testing.T) {
	out, err := FormatReport(githubReport(), "github", FormatOptions{ScanPath: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	// Every line must be a valid workflow command.
	for _, l := range lines {
		if !strings.HasPrefix(l, "::warning ") && !strings.HasPrefix(l, "::notice ") {
			t.Errorf("not a workflow command: %q", l)
		}
	}

	// First clone: annotated at its primary instance with repo-relative path,
	// function attribution, and the duplicate's location in the message.
	if !strings.Contains(out, "file=pkg/a.go,line=10,endLine=24") {
		t.Error("primary instance annotation missing or path not relative")
	}
	if !strings.Contains(out, "in loadUser") {
		t.Error("function attribution missing from message")
	}
	if !strings.Contains(out, "pkg/b.go:40-54 (in loadOrder)") {
		t.Error("duplicate location missing from message")
	}

	// Titles escape ',' and ':' per workflow-command property rules.
	if !strings.Contains(out, "title=dupehound%3A type-2 clone%2C ~15 lines saveable") {
		t.Error("title escaping (%%3A / %%2C) missing")
	}

	// Dead function annotated.
	if !strings.Contains(out, "file=pkg/dead.go,line=33") || !strings.Contains(out, "unusedHelper") {
		t.Error("dead function annotation missing")
	}

	// Summary notice last, with % escaped as %25.
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "::notice title=dupehound summary::") {
		t.Errorf("last line must be the summary notice, got %q", last)
	}
	if !strings.Contains(last, "8.0%25 duplication") {
		t.Errorf("summary must escape %% as %%25, got %q", last)
	}
}

func TestFormatGitHub_PartialWarningFirst(t *testing.T) {
	r := githubReport()
	r.Partial = true
	r.PartialReason = "type-3 detection stopped at --max-pairs; type-1/2 results are complete"
	out, err := FormatReport(r, "github", FormatOptions{ScanPath: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(first, "partial result") || !strings.HasPrefix(first, "::warning ") {
		t.Errorf("partial warning must lead the output, got %q", first)
	}
}

func TestFormatGitHub_TopCapNotice(t *testing.T) {
	r := githubReport()
	out, err := FormatReport(r, "github", FormatOptions{ScanPath: "/repo", Top: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "::warning file=") != 2 { // 1 clone + 1 dead function
		t.Errorf("Top=1 should annotate exactly one clone (plus dead func):\n%s", out)
	}
	if !strings.Contains(out, "1 more clones not annotated") {
		t.Error("overflow notice missing")
	}
}

func TestFormatGitHub_EscapesNewlines(t *testing.T) {
	if got := ghEscapeData("a\nb%c\rd"); got != "a%0Ab%25c%0Dd" {
		t.Errorf("ghEscapeData: got %q", got)
	}
	if got := ghEscapeProp("t: a,b"); got != "t%3A a%2Cb" {
		t.Errorf("ghEscapeProp: got %q", got)
	}
}

func TestFormatGitHub_BaselineSummary(t *testing.T) {
	r := githubReport()
	r.BaselineFile = ".dupehound-baseline.json"
	r.BaselineKnown = 5
	r.BaselineNew = 2
	out, err := FormatReport(r, "github", FormatOptions{ScanPath: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	// `:` and `,` are only escaped in properties, not in message data.
	if !strings.Contains(out, "baseline: 5 known, 2 new") {
		t.Errorf("summary must include baseline split, got:\n%s", out)
	}
}
