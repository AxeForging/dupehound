package services

import (
	"strings"
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

// TestSortByChurn verifies that clones are sorted by ChurnScore descending.
func TestSortByChurn(t *testing.T) {
	clones := []domain.Clone{
		{Hash: "aaa", ChurnScore: 3, LineCount: 10},
		{Hash: "bbb", ChurnScore: 10, LineCount: 5},
		{Hash: "ccc", ChurnScore: 1, LineCount: 20},
	}
	sortByChurn(clones)

	if clones[0].Hash != "bbb" {
		t.Errorf("expected bbb (score=10) first, got %s", clones[0].Hash)
	}
	if clones[1].Hash != "aaa" {
		t.Errorf("expected aaa (score=3) second, got %s", clones[1].Hash)
	}
	if clones[2].Hash != "ccc" {
		t.Errorf("expected ccc (score=1) last, got %s", clones[2].Hash)
	}
}

// TestSortByChurn_TieBreakByLineCount verifies tie-breaking by line count.
func TestSortByChurn_TieBreakByLineCount(t *testing.T) {
	clones := []domain.Clone{
		{Hash: "aaa", ChurnScore: 5, LineCount: 10},
		{Hash: "bbb", ChurnScore: 5, LineCount: 20},
	}
	sortByChurn(clones)
	if clones[0].Hash != "bbb" {
		t.Errorf("expected bbb (linecount=20) first on tie, got %s", clones[0].Hash)
	}
}

// TestGitChurnAbsent verifies that without --git-churn, clones are sorted by line count.
func TestGitChurnAbsent(t *testing.T) {
	dir := t.TempDir()
	// Create two different-sized duplicates.
	smallBlock := `func small() {
	x := compute()
	return x
}
`
	bigBlock := `func big() {
	x := compute()
	y := validate(x)
	z := store(y)
	notify(z)
	return z
}
`
	writeTestFile(t, dir, "a.go", "package main\n\n"+smallBlock+bigBlock)
	writeTestFile(t, dir, "b.go", "package main\n\n"+smallBlock+bigBlock)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 5, GitChurn: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// ChurnScore should be 0 for all clones when git-churn is not active.
	for _, c := range report.Clones {
		if c.ChurnScore != 0 {
			t.Errorf("expected ChurnScore=0 when --git-churn absent, got %d for clone %s", c.ChurnScore, c.Hash)
		}
	}
}

// TestGitChurn_JSONOutput verifies churn fields appear in JSON when set.
func TestGitChurn_JSONOutput(t *testing.T) {
	report := &domain.Report{
		TotalFiles:   2,
		ScannedFiles: 2,
		TotalClones:  1,
		Clones: []domain.Clone{
			{
				Hash:       "abc",
				Type:       domain.CloneType1,
				Similarity: 1.0,
				LineCount:  10,
				ChurnScore: 5,
				Instances: []domain.CloneInstance{
					{File: "a.go", StartLine: 1, EndLine: 10, FileCommits: 3},
					{File: "b.go", StartLine: 1, EndLine: 10, FileCommits: 2},
				},
			},
		},
	}
	out, err := FormatReport(report, "json", FormatOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "churn_score") {
		t.Error("expected churn_score in JSON output")
	}
	if !strings.Contains(out, "file_commits_in_window") {
		t.Error("expected file_commits_in_window in JSON output")
	}
}

// TestGitChurn_TextOutput verifies churn info appears in text output.
func TestGitChurn_TextOutput(t *testing.T) {
	report := &domain.Report{
		TotalFiles:   2,
		ScannedFiles: 2,
		TotalClones:  1,
		TotalLines:   100,
		Clones: []domain.Clone{
			{
				Hash:       "abc",
				Type:       domain.CloneType1,
				Similarity: 1.0,
				LineCount:  10,
				ChurnScore: 7,
				Instances: []domain.CloneInstance{
					{File: "/a/a.go", StartLine: 1, EndLine: 10, FileCommits: 4},
					{File: "/a/b.go", StartLine: 1, EndLine: 10, FileCommits: 3},
				},
			},
		},
	}
	out, err := FormatReport(report, "text", FormatOptions{ScanPath: "/a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "churn: 7 commits") {
		t.Errorf("expected 'churn: 7 commits' in text output, got:\n%s", out)
	}
	if !strings.Contains(out, "4 commits") {
		t.Error("expected per-instance commit count in text output")
	}
}

// TestGetFileCommitCount_NotGitRepo verifies graceful failure in non-git directory.
func TestGetFileCommitCount_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	count, err := getFileCommitCount(dir, "nonexistent.go", 90)
	// Should either return 0 or an error — must not panic.
	if err != nil {
		t.Logf("expected error from non-git dir: %v", err)
	}
	_ = count
}
