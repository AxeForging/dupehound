package services

import (
	"strings"
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

// buildTestReport creates a Report with n fake clones for testing output truncation.
func buildTestReport(n int) *domain.Report {
	clones := make([]domain.Clone, n)
	for i := range clones {
		clones[i] = domain.Clone{
			Hash:       "deadbeef",
			Type:       domain.CloneType1,
			Similarity: 1.0,
			LineCount:  10 + i,
			TokenCount: 50,
			Instances: []domain.CloneInstance{
				{File: "/a/foo.go", StartLine: 1, EndLine: 10 + i},
				{File: "/a/bar.go", StartLine: 1, EndLine: 10 + i},
			},
		}
	}
	return &domain.Report{
		TotalFiles:     2,
		ScannedFiles:   2,
		TotalClones:    n,
		TotalLines:     500,
		DuplicateLines: 100,
		DuplicationPct: 20.0,
		Clones:         clones,
	}
}

// TestTopN_DefaultIsTop10 verifies that without --top, at most 10 clones appear in Top section.
func TestTopN_DefaultIsTop10(t *testing.T) {
	report := buildTestReport(20)
	out, err := FormatReport(report, "text", FormatOptions{ScanPath: "/a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Count lines with "#" clone header in the "Top clones" section.
	if !strings.Contains(out, "Top clones (by impact):") {
		t.Fatal("could not find 'Top clones' section")
	}
	// Should mention "10 more clones" or similar.
	if !strings.Contains(out, "more clones") {
		t.Error("expected truncation notice in output")
	}
}

// TestTopN_Top3 verifies --top 3 limits clone output to 3.
func TestTopN_Top3(t *testing.T) {
	report := buildTestReport(10)
	out, err := FormatReport(report, "text", FormatOptions{ScanPath: "/a", Top: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	topStart := strings.Index(out, "Top clones (by impact):")
	if topStart < 0 {
		t.Fatal("could not find 'Top clones' section")
	}
	topSectionRaw := out[topStart:]
	allClonesIdx := strings.Index(topSectionRaw, "All clones:")
	topSection := topSectionRaw
	if allClonesIdx > 0 {
		topSection = topSectionRaw[:allClonesIdx]
	}
	// Should show exactly 3 clone headers (#1, #2, #3).
	count := strings.Count(topSection, "  #")
	if count != 3 {
		t.Errorf("expected 3 clones in Top section with --top 3, got %d", count)
	}
}

// TestTopN_ShowAll verifies --top -1 (meaning 0 in CLI / show all) shows all clones.
func TestTopN_ShowAll(t *testing.T) {
	report := buildTestReport(15)
	out, err := FormatReport(report, "text", FormatOptions{ScanPath: "/a", Top: -1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should NOT contain truncation notice.
	if strings.Contains(out, "more clones") {
		t.Error("expected no truncation notice when Top=-1 (show all)")
	}
}

// TestTopN_JSONUnaffected verifies JSON output is never truncated by --top.
func TestTopN_JSONUnaffected(t *testing.T) {
	report := buildTestReport(20)
	// JSON with Top=3 should still have all 20 clones.
	out, err := FormatReport(report, "json", FormatOptions{ScanPath: "/a", Top: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Count "hash" occurrences as proxy for clone count.
	count := strings.Count(out, `"hash"`)
	if count != 20 {
		t.Errorf("expected 20 clones in JSON output regardless of --top, got %d", count)
	}
}

// TestTopN_NoAllClonesDump is a regression test that --top truly truncates
// the text output. Before the fix, formatText printed an unconditional
// "All clones:" section after the top-N section, which silently undid the
// truncation. After the fix, no such section should exist.
func TestTopN_NoAllClonesDump(t *testing.T) {
	report := buildTestReport(20)
	out, err := FormatReport(report, "text", FormatOptions{ScanPath: "/a", Top: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "All clones:") {
		t.Error("text output must not contain 'All clones:' dump — it defeats --top")
	}
	// Count clone headers in the entire output. Should be exactly 3 (top section).
	cloneHeaders := strings.Count(out, "  #")
	if cloneHeaders != 3 {
		t.Errorf("expected 3 clone headers total with --top 3, got %d", cloneHeaders)
	}
}

// TestTopN_TestProdNotDoubleListed verifies that a test↔prod clone appears
// in its own section but NOT also under "Top clones (by impact)".
func TestTopN_TestProdNotDoubleListed(t *testing.T) {
	report := &domain.Report{
		TotalFiles: 4, ScannedFiles: 4, TotalLines: 200, TotalClones: 2,
		Clones: []domain.Clone{
			{
				Hash: "tprod111", Type: domain.CloneType1, Similarity: 1.0,
				LineCount: 20, TokenCount: 100, TestProdSpan: true,
				Instances: []domain.CloneInstance{
					{File: "/a/svc.go", StartLine: 1, EndLine: 20},
					{File: "/a/svc_test.go", StartLine: 1, EndLine: 20, IsTest: true},
				},
			},
			{
				Hash: "norm2222", Type: domain.CloneType1, Similarity: 1.0,
				LineCount: 15, TokenCount: 80,
				Instances: []domain.CloneInstance{
					{File: "/a/foo.go", StartLine: 1, EndLine: 15},
					{File: "/a/bar.go", StartLine: 1, EndLine: 15},
				},
			},
		},
	}
	out, err := FormatReport(report, "text", FormatOptions{ScanPath: "/a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tpStart := strings.Index(out, "Test↔Prod clones")
	topStart := strings.Index(out, "Top clones (by impact):")
	if tpStart < 0 || topStart < 0 {
		t.Fatalf("expected both sections in output:\n%s", out)
	}
	topSection := out[topStart:]
	if strings.Contains(topSection, "tprod111") || strings.Contains(topSection, "svc_test.go") {
		t.Errorf("test↔prod clone leaked into Top section:\n%s", topSection)
	}
	// Sanity: normal clone IS in the Top section.
	if !strings.Contains(topSection, "foo.go") {
		t.Errorf("normal clone should appear in Top section:\n%s", topSection)
	}
}

// TestMarkdown_ChurnBadgeAndCommits verifies the markdown formatter renders
// churn information so PR comments retain that signal (was previously dropped).
func TestMarkdown_ChurnBadgeAndCommits(t *testing.T) {
	report := &domain.Report{
		TotalFiles: 2, ScannedFiles: 2, TotalLines: 100, TotalClones: 1,
		Clones: []domain.Clone{
			{
				Hash: "abc", Type: domain.CloneType1, Similarity: 1.0,
				LineCount: 10, TokenCount: 50, ChurnScore: 7,
				Instances: []domain.CloneInstance{
					{File: "/a/a.go", StartLine: 1, EndLine: 10, FileCommits: 4},
					{File: "/a/b.go", StartLine: 1, EndLine: 10, FileCommits: 3},
				},
			},
		},
	}
	out, err := FormatReport(report, "md", FormatOptions{ScanPath: "/a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "churn 7") {
		t.Errorf("markdown output missing churn badge:\n%s", out)
	}
	if !strings.Contains(out, "4 commits in window") {
		t.Errorf("markdown output missing per-instance commit count:\n%s", out)
	}
}

// TestMarkdown_TestProdBadge verifies the test↔prod badge appears on the clone summary.
func TestMarkdown_TestProdBadge(t *testing.T) {
	report := &domain.Report{
		TotalFiles: 2, ScannedFiles: 2, TotalLines: 100, TotalClones: 1,
		Clones: []domain.Clone{
			{
				Hash: "abc", Type: domain.CloneType1, Similarity: 1.0,
				LineCount: 10, TestProdSpan: true,
				Instances: []domain.CloneInstance{
					{File: "/a/svc.go", StartLine: 1, EndLine: 10},
					{File: "/a/svc_test.go", StartLine: 1, EndLine: 10, IsTest: true},
				},
			},
		},
	}
	out, err := FormatReport(report, "md", FormatOptions{ScanPath: "/a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "test↔prod") {
		t.Errorf("markdown missing test↔prod badge:\n%s", out)
	}
	if !strings.Contains(out, "_(test)_") {
		t.Errorf("markdown missing per-instance (test) tag:\n%s", out)
	}
}

// TestDeadFunctions_TruncatedByTop verifies dead-function lists are capped
// in both text and markdown so a real repo's hundred-deep list doesn't
// flood the terminal or blow past GitHub's PR comment size limit.
func TestDeadFunctions_TruncatedByTop(t *testing.T) {
	deads := make([]domain.DeadFunc, 25)
	for i := range deads {
		deads[i] = domain.DeadFunc{File: "/a/x.go", Line: i + 1, Name: "fn"}
	}
	report := &domain.Report{
		TotalFiles: 1, ScannedFiles: 1, TotalLines: 100,
		DeadFunctions: deads,
	}

	textOut, err := FormatReport(report, "text", FormatOptions{ScanPath: "/a", Top: 5})
	if err != nil {
		t.Fatalf("text format: %v", err)
	}
	if !strings.Contains(textOut, "20 more") {
		t.Errorf("expected '20 more' truncation note in text dead-funcs section:\n%s", textOut)
	}

	mdOut, err := FormatReport(report, "md", FormatOptions{ScanPath: "/a", Top: 5})
	if err != nil {
		t.Fatalf("md format: %v", err)
	}
	if !strings.Contains(mdOut, "20 more dead functions") {
		t.Errorf("expected '20 more dead functions' details block in markdown:\n%s", mdOut)
	}
	if !strings.Contains(mdOut, "<details>") {
		t.Errorf("expected <details> wrapper around dead-fn overflow:\n%s", mdOut)
	}
}

// TestTopNWithOpt verifies topNWithOpt behavior.
func TestTopNWithOpt(t *testing.T) {
	tests := []struct {
		total    int
		opt      FormatOptions
		expected int
	}{
		{5, FormatOptions{Top: 0}, 5},          // fewer than default 10
		{20, FormatOptions{Top: 0}, 10},        // default 10
		{20, FormatOptions{Top: 3}, 3},         // explicit top 3
		{20, FormatOptions{Top: -1}, 20},       // show all
		{20, FormatOptions{Verbose: true}, 20}, // verbose shows all
	}
	for _, tt := range tests {
		got := topNWithOpt(tt.total, tt.opt)
		if got != tt.expected {
			t.Errorf("topNWithOpt(%d, %+v) = %d, want %d", tt.total, tt.opt, got, tt.expected)
		}
	}
}
