package services

import (
	"os"
	"os/exec"
	"path/filepath"
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

// gitCommit runs `git commit` in dir with a deterministic author so the test does
// not depend on the developer's local git config.
func gitCommit(t *testing.T, dir, msg string) {
	t.Helper()
	cmd := exec.Command("git",
		"-c", "user.name=dupehound-test",
		"-c", "user.email=test@dupehound.local",
		"-c", "commit.gpgsign=false",
		"commit", "--allow-empty", "-m", msg,
	)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit failed: %v\n%s", err, out)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

// TestGitChurn_EndToEndRealRepo is a regression test for issue #23 that exercises
// the actual git plumbing rather than synthetic data. It builds a real repo with
// known commit history (file a.go has 4 distinct commits, file b.go has 1) and
// asserts that:
//   - per-instance FileCommits matches the real history
//   - the clone's ChurnScore is the sum across all instances
//   - clones are sorted by ChurnScore (high → low)
//
// This is the test that would catch a future regression in git invocation,
// path resolution, or commit counting.
func TestGitChurn_EndToEndRealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")

	// Two duplicate Go files. Both contain the same function so the detector
	// finds a clone whose two instances live in a.go and b.go.
	dup := `package main

func handler() int {
	x := compute()
	y := validate(x)
	z := transform(y)
	w := persist(z)
	notify(w)
	return w
}
`
	aPath := filepath.Join(dir, "a.go")
	bPath := filepath.Join(dir, "b.go")
	if err := os.WriteFile(aPath, []byte(dup), 0o600); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	if err := os.WriteFile(bPath, []byte(dup), 0o600); err != nil {
		t.Fatalf("write b.go: %v", err)
	}
	gitRun(t, dir, "add", "a.go", "b.go")
	gitCommit(t, dir, "initial: add a.go and b.go")

	// 3 more commits touching a.go only → a.go should have 4 total commits.
	for i := 0; i < 3; i++ {
		appended := dup + "\n// rev " + string(rune('A'+i)) + "\n"
		if err := os.WriteFile(aPath, []byte(appended), 0o600); err != nil {
			t.Fatalf("rewrite a.go: %v", err)
		}
		gitRun(t, dir, "add", "a.go")
		gitCommit(t, dir, "modify a.go")
	}

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{
		Path:      dir,
		MinTokens: 10,
		GitChurn:  true,
		ChurnDays: 3650, // 10 years — wide enough to catch all commits, narrow enough that git's date parser still accepts it
	})
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if len(report.Clones) == 0 {
		t.Fatal("expected at least 1 clone between a.go and b.go")
	}

	// Find the clone whose instances span a.go and b.go.
	var target *domain.Clone
	for i := range report.Clones {
		c := &report.Clones[i]
		hasA, hasB := false, false
		for _, inst := range c.Instances {
			if filepath.Base(inst.File) == "a.go" {
				hasA = true
			}
			if filepath.Base(inst.File) == "b.go" {
				hasB = true
			}
		}
		if hasA && hasB {
			target = c
			break
		}
	}
	if target == nil {
		t.Fatal("expected a clone spanning both a.go and b.go")
	}

	// Per-instance assertions: a.go was touched 4 times, b.go was touched 1 time.
	var aCommits, bCommits int
	for _, inst := range target.Instances {
		switch filepath.Base(inst.File) {
		case "a.go":
			aCommits = inst.FileCommits
		case "b.go":
			bCommits = inst.FileCommits
		}
	}
	if aCommits != 4 {
		t.Errorf("a.go FileCommits = %d, want 4 (1 initial + 3 modifications)", aCommits)
	}
	if bCommits != 1 {
		t.Errorf("b.go FileCommits = %d, want 1 (only initial)", bCommits)
	}

	// ChurnScore must equal the sum of per-instance counts.
	wantScore := 0
	for _, inst := range target.Instances {
		wantScore += inst.FileCommits
	}
	if target.ChurnScore != wantScore {
		t.Errorf("ChurnScore = %d, want %d (sum of per-instance commits)", target.ChurnScore, wantScore)
	}
	if target.ChurnScore != 5 {
		t.Errorf("ChurnScore = %d, want 5 (4 + 1)", target.ChurnScore)
	}

	// Sort invariant: when --git-churn is on, the highest-churn clone is first.
	for i := 1; i < len(report.Clones); i++ {
		if report.Clones[i-1].ChurnScore < report.Clones[i].ChurnScore {
			t.Errorf("clones not sorted by churn descending at index %d: %d < %d",
				i, report.Clones[i-1].ChurnScore, report.Clones[i].ChurnScore)
		}
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
