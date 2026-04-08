package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// makeTokenizedDup is a tiny helper that builds a TokenizedFile carrying a
// realistic-looking duplicate function body. Two files built with the same
// content will produce a clone in the detector.
func makeTokenizedDup(t *testing.T, path string) TokenizedFile {
	t.Helper()
	lang := LangForName("go")
	if lang == nil {
		t.Fatal("go language not registered")
	}
	content := `package main
func handler() int {
	x := compute()
	y := validate(x)
	z := transform(y)
	w := persist(z)
	notify(w)
	return w
}
`
	return BuildTokenizedFile(path, content, lang)
}

// TestDetector_InScopeFilter_OnlyReportsClonesTouchingScope is the core
// regression+feature test for issue follow-up #6: when InScopeFiles is set,
// the detector must skip clones whose instances are all in out-of-scope
// files. We build three files (A, B, C) all containing the same function;
// only file C is marked in scope. The result must contain exactly one clone,
// and that clone must include C among its instances.
func TestDetector_InScopeFilter_OnlyReportsClonesTouchingScope(t *testing.T) {
	files := []TokenizedFile{
		makeTokenizedDup(t, "/a.go"),
		makeTokenizedDup(t, "/b.go"),
		makeTokenizedDup(t, "/c.go"),
	}

	// Baseline: no scope filter → 1 clone group with all 3 files.
	baseline := DetectWithOptions(files, DetectOptions{
		MinTokens:     20,
		MinSimilarity: 1.0, // exact only — keeps the test deterministic
	})
	if len(baseline) != 1 {
		t.Fatalf("baseline: expected 1 clone, got %d", len(baseline))
	}
	if len(baseline[0].Instances) != 3 {
		t.Fatalf("baseline: expected 3 instances in the clone, got %d", len(baseline[0].Instances))
	}

	// In-scope: only c.go is in scope. Clone group still spans all 3 files
	// (the detector doesn't drop instances; it just decides whether to emit
	// the group), and it must be emitted because c.go IS in scope.
	scoped := DetectWithOptions(files, DetectOptions{
		MinTokens:     20,
		MinSimilarity: 1.0,
		InScopeFiles:  []bool{false, false, true}, // a, b out; c in
	})
	if len(scoped) != 1 {
		t.Fatalf("scoped (c in): expected 1 clone, got %d", len(scoped))
	}
	hasC := false
	for _, inst := range scoped[0].Instances {
		if filepath.Base(inst.File) == "c.go" {
			hasC = true
		}
	}
	if !hasC {
		t.Errorf("scoped (c in): clone instances do not include c.go: %+v", scoped[0].Instances)
	}
}

// TestDetector_InScopeFilter_DropsAllOutOfScopeClones is the negative
// counterpart: when NO file is in scope, the detector must emit zero clones
// even though plenty of duplicates exist. This is the "the diff is empty"
// fast-path that --since relies on for safety.
func TestDetector_InScopeFilter_DropsAllOutOfScopeClones(t *testing.T) {
	files := []TokenizedFile{
		makeTokenizedDup(t, "/a.go"),
		makeTokenizedDup(t, "/b.go"),
		makeTokenizedDup(t, "/c.go"),
	}
	clones := DetectWithOptions(files, DetectOptions{
		MinTokens:     20,
		MinSimilarity: 1.0,
		InScopeFiles:  []bool{false, false, false},
	})
	if len(clones) != 0 {
		t.Errorf("expected 0 clones when nothing is in scope, got %d", len(clones))
	}
}

// TestDetector_InScopeFilter_NilPreservesOriginalBehavior is the
// backward-compatibility regression test: nil InScopeFiles must be
// indistinguishable from omitting the option entirely. If anyone in the
// future tries to make nil mean "nothing in scope", this test will fail
// loudly.
func TestDetector_InScopeFilter_NilPreservesOriginalBehavior(t *testing.T) {
	files := []TokenizedFile{
		makeTokenizedDup(t, "/a.go"),
		makeTokenizedDup(t, "/b.go"),
	}
	withNil := DetectWithOptions(files, DetectOptions{
		MinTokens:     20,
		MinSimilarity: 1.0,
		InScopeFiles:  nil,
	})
	withoutOption := DetectWithOptions(files, DetectOptions{
		MinTokens:     20,
		MinSimilarity: 1.0,
	})
	if len(withNil) != len(withoutOption) {
		t.Errorf("nil InScopeFiles changed clone count: %d vs %d", len(withNil), len(withoutOption))
	}
}

// TestDetector_InScopeFilter_FuzzyPath verifies the scope filter is honored
// in the fuzzy (type-3) detector code path as well, not just the exact one.
// Two near-miss files only — neither in scope — must produce zero clones.
func TestDetector_InScopeFilter_FuzzyPath(t *testing.T) {
	lang := LangForName("go")
	if lang == nil {
		t.Fatal("go language not registered")
	}
	a := BuildTokenizedFile("/a.go", `package main
func handler() int {
	x := compute()
	y := validate(x)
	z := transform(y)
	w := persist(z)
	notify(w)
	return w
}
`, lang)
	// b.go is structurally similar but with one extra step → near-miss.
	b := BuildTokenizedFile("/b.go", `package main
func handler() int {
	x := compute()
	y := validate(x)
	z := transform(y)
	q := decorate(z)
	w := persist(q)
	notify(w)
	return w
}
`, lang)

	// Baseline with fuzzy on: at minimum the exact path is exercised.
	baseline := DetectWithOptions([]TokenizedFile{a, b}, DetectOptions{
		MinTokens:     10,
		MinSimilarity: 0.6,
	})
	_ = baseline // The exact count varies with bucket parameters; we only need the comparison below.

	scoped := DetectWithOptions([]TokenizedFile{a, b}, DetectOptions{
		MinTokens:     10,
		MinSimilarity: 0.6,
		InScopeFiles:  []bool{false, false}, // nothing in scope
	})
	if len(scoped) != 0 {
		t.Errorf("fuzzy path: expected 0 clones with empty scope, got %d", len(scoped))
	}
}

// TestScanner_Since_RealRepo_FewerCloneEvaluations is the end-to-end
// regression test for the diff-aware optimization: when --since is set on a
// real git repo, the detector must produce a strictly smaller (or equal)
// clone set than a full scan, AND the scan must still surface the clones
// that touch the diff. We build a 4-file repo, commit all of it, then add
// a single new duplicate file in a follow-up commit and run with --since.
func TestScanner_Since_RealRepo_FewerCloneEvaluations(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")

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
	// Commit 1: a.go and b.go are duplicates of each other.
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(dup), 0o600); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte(dup), 0o600); err != nil {
		t.Fatalf("write b.go: %v", err)
	}
	gitRun(t, dir, "add", "a.go", "b.go")
	gitCommit(t, dir, "initial: a and b duplicates")

	// Commit 2: c.go is a new duplicate. This is the file that --since
	// HEAD~1 should consider "in scope".
	if err := os.WriteFile(filepath.Join(dir, "c.go"), []byte(dup), 0o600); err != nil {
		t.Fatalf("write c.go: %v", err)
	}
	gitRun(t, dir, "add", "c.go")
	gitCommit(t, dir, "add c duplicate")

	svc := NewScannerService()

	// Full scan baseline.
	full, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 20})
	if err != nil {
		t.Fatalf("full scan: %v", err)
	}

	// Diff-aware scan since the previous commit.
	scoped, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 20, Since: "HEAD~1"})
	if err != nil {
		t.Fatalf("scoped scan: %v", err)
	}

	// Feature assertion: --since must produce ≤ as many top-level clones as
	// the full scan. (For this 3-duplicate-file case both will surface a
	// single grouped clone; the important guarantee is "no MORE work".)
	if len(scoped.Clones) > len(full.Clones) {
		t.Errorf("--since scan reported more clones than full scan: %d > %d",
			len(scoped.Clones), len(full.Clones))
	}

	// Regression: the diff metadata must be populated and the changed-file
	// count from git must be exposed.
	if scoped.SinceDiffRef != "HEAD~1" {
		t.Errorf("SinceDiffRef = %q, want HEAD~1", scoped.SinceDiffRef)
	}
	if scoped.SinceDiffFiles == 0 {
		t.Error("SinceDiffFiles = 0, want > 0 (commit 2 added c.go)")
	}

	// Feature: NewClones (line-precise) must contain the clone touching c.go.
	foundC := false
	for _, c := range scoped.NewClones {
		for _, inst := range c.Instances {
			if filepath.Base(inst.File) == "c.go" {
				foundC = true
			}
		}
	}
	if !foundC {
		t.Errorf("expected NewClones to surface a clone instance in c.go, got: %+v", scoped.NewClones)
	}
}

// TestScanner_Since_NoChangesNoClones is the barrier test for the
// "is-it-really-cheap" promise: when --since points at HEAD itself (zero
// changed files), the detector must emit zero clones. This is what makes
// --since safe to wire into pre-commit hooks on huge repos: a no-op diff
// produces a no-op scan, regardless of how much duplication exists.
func TestScanner_Since_NoChangesNoClones(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")

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
	for _, name := range []string{"a.go", "b.go", "c.go", "d.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(dup), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	gitRun(t, dir, "add", "a.go", "b.go", "c.go", "d.go")
	gitCommit(t, dir, "initial: 4 duplicates")

	svc := NewScannerService()

	// Sanity: full scan finds duplicates.
	full, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 20})
	if err != nil {
		t.Fatalf("full scan: %v", err)
	}
	if len(full.Clones) == 0 {
		t.Fatal("sanity check failed: expected duplicates between the 4 files")
	}

	// Now scan with --since HEAD: no diff at all → no clones.
	scoped, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 20, Since: "HEAD"})
	if err != nil {
		t.Fatalf("scoped scan: %v", err)
	}
	if len(scoped.Clones) != 0 {
		// Helpful diagnostic: dump the offending clones so a regression is easy to debug.
		var locs []string
		for _, c := range scoped.Clones {
			for _, inst := range c.Instances {
				locs = append(locs, filepath.Base(inst.File))
			}
		}
		t.Errorf("--since HEAD with no changes must produce 0 clones, got %d (instances: %v)",
			len(scoped.Clones), locs)
	}
	if len(scoped.NewClones) != 0 {
		t.Errorf("--since HEAD with no changes must produce 0 NewClones, got %d", len(scoped.NewClones))
	}
}

// TestScanner_Since_RelativePath is the regression test for a silent
// pre-fix bug where `dupehound scan --path . --since <ref>` always reported
// zero clones. The cause was a path-form mismatch: getChangedLines keyed its
// map by absolute paths, but tokenized files carried relative paths from
// the walker, so the in-scope lookup AND the NewClones post-filter both
// missed every file. This test runs --since with a relative --path and
// asserts the diff is actually picked up.
func TestScanner_Since_RelativePath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")

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
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(dup), 0o600); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte(dup), 0o600); err != nil {
		t.Fatalf("write b.go: %v", err)
	}
	gitRun(t, dir, "add", "a.go", "b.go")
	gitCommit(t, dir, "initial: a and b duplicates")

	if err := os.WriteFile(filepath.Join(dir, "c.go"), []byte(dup), 0o600); err != nil {
		t.Fatalf("write c.go: %v", err)
	}
	gitRun(t, dir, "add", "c.go")
	gitCommit(t, dir, "add c duplicate")

	// chdir into the repo and scan with --path "." (relative). This is the
	// common pre-commit / Makefile invocation form, and the one that used
	// to silently produce empty results.
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	svc := NewScannerService()
	scoped, err := svc.Scan(ScanOptions{Path: ".", MinTokens: 20, Since: "HEAD~1"})
	if err != nil {
		t.Fatalf("scoped scan: %v", err)
	}
	if scoped.SinceDiffFiles == 0 {
		t.Errorf("SinceDiffFiles = 0 with relative --path; the diff should have been picked up")
	}
	// At least one new clone instance must reference c.go (the file added
	// in HEAD).
	foundC := false
	for _, c := range scoped.NewClones {
		for _, inst := range c.Instances {
			if filepath.Base(inst.File) == "c.go" {
				foundC = true
			}
		}
	}
	if !foundC {
		t.Errorf("relative --path: NewClones did not include c.go (regression of silent path-form bug); got %+v", scoped.NewClones)
	}
}

// TestDetector_InScopeFilter_OutOfRangeIndexIsSafe is a defensive barrier
// test: passing a scope slice shorter than the file list must NOT panic.
// Out-of-range entries are treated as in-scope (the safe default), so the
// behavior is identical to passing no slice for those positions.
func TestDetector_InScopeFilter_OutOfRangeIndexIsSafe(t *testing.T) {
	files := []TokenizedFile{
		makeTokenizedDup(t, "/a.go"),
		makeTokenizedDup(t, "/b.go"),
		makeTokenizedDup(t, "/c.go"),
	}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("detector panicked on short scope slice: %v", r)
		}
	}()
	clones := DetectWithOptions(files, DetectOptions{
		MinTokens:     20,
		MinSimilarity: 1.0,
		InScopeFiles:  []bool{true}, // length 1 — shorter than file count
	})
	// We don't pin the count — only that no panic occurred and we got a
	// well-formed slice back.
	_ = clones
	// And the convenience helper itself: out-of-range → in scope.
	if !fileInScope([]bool{true}, 9) {
		t.Error("fileInScope should treat out-of-range indices as in scope")
	}
	if !fileInScope(nil, 0) {
		t.Error("fileInScope nil scope should always return true")
	}
}
