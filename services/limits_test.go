package services

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AxeForging/dupehound/helpers"
)

// TestScanner_MaxFiles_FailsFast is the feature test for the --max-files
// safety net: when the collected file count exceeds the cap, the scanner
// must return ErrTooManyFiles before any tokenization or detection runs.
// The error message must be actionable (mention the cap value).
func TestScanner_MaxFiles_FailsFast(t *testing.T) {
	dir := t.TempDir()
	// Create more files than the cap so the check actually trips.
	for i := 0; i < 6; i++ {
		writeTestFile(t, dir, fmt.Sprintf("f%d.go", i), "package main\n\nfunc f"+fmt.Sprint(i)+"() int { return 1 }\n")
	}

	svc := NewScannerService()
	_, err := svc.Scan(ScanOptions{
		Path:      dir,
		MinTokens: 10,
		MaxFiles:  3,
	})
	if err == nil {
		t.Fatal("expected error when collected files > MaxFiles, got nil")
	}
	if !errors.Is(err, helpers.ErrTooManyFiles) {
		t.Errorf("expected errors.Is ErrTooManyFiles, got %v", err)
	}
	// Actionable: mention the actual numbers and the relevant flags.
	msg := err.Error()
	if !strings.Contains(msg, "6") || !strings.Contains(msg, "3") {
		t.Errorf("error message should mention collected and cap counts, got: %q", msg)
	}
	if !strings.Contains(msg, "--include") && !strings.Contains(msg, "--max-files") {
		t.Errorf("error message should suggest --include or --max-files, got: %q", msg)
	}
}

// TestScanner_MaxFiles_Disabled verifies that MaxFiles=0 is treated as
// "no cap" (the default), not "no files allowed". This is the regression
// guard against accidentally inverting the check.
func TestScanner_MaxFiles_Disabled(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		writeTestFile(t, dir, fmt.Sprintf("f%d.go", i), "package main\n\nfunc f"+fmt.Sprint(i)+"() int { return 1 }\n")
	}

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{
		Path:      dir,
		MinTokens: 10,
		MaxFiles:  0, // explicit zero
	})
	if err != nil {
		t.Fatalf("MaxFiles=0 should be 'no cap', got error: %v", err)
	}
	if report.TotalFiles != 5 {
		t.Errorf("expected 5 files scanned with no cap, got %d", report.TotalFiles)
	}
}

// TestScanner_MaxFiles_AtBoundary is the off-by-one barrier test: exactly
// at the cap is OK; one over the cap fails. Catches future > vs >= bugs.
func TestScanner_MaxFiles_AtBoundary(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 4; i++ {
		writeTestFile(t, dir, fmt.Sprintf("f%d.go", i), "package main\n\nfunc f"+fmt.Sprint(i)+"() int { return 1 }\n")
	}

	svc := NewScannerService()

	// Exactly at the cap → OK.
	if _, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10, MaxFiles: 4}); err != nil {
		t.Errorf("MaxFiles=4 with 4 files should pass, got: %v", err)
	}

	// One over the cap → fails.
	if _, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10, MaxFiles: 3}); !errors.Is(err, helpers.ErrTooManyFiles) {
		t.Errorf("MaxFiles=3 with 4 files should fail with ErrTooManyFiles, got: %v", err)
	}
}

// TestDetector_MaxPairs_AbortsFuzzy is the feature test for the --max-pairs
// safety net: when the candidate pair count would exceed the cap, the fuzzy
// detector must abort and return whatever exact clones it already had,
// rather than blowing memory on a giant dedup map.
//
// We construct a many-file scenario where the fuzzy detector would normally
// build many candidate pairs, then assert that with a tight cap the result
// drops to exact-only (the fuzzy result must be empty).
func TestDetector_MaxPairs_AbortsFuzzy(t *testing.T) {
	lang := LangForName("go")
	if lang == nil {
		t.Fatal("go language not registered")
	}

	// 8 files, all near-duplicates → many fuzzy candidate pairs.
	files := make([]TokenizedFile, 8)
	for i := range files {
		// Slightly different middle line per file so they're near-misses, not exact.
		content := fmt.Sprintf(`package main
func handler%d() int {
	x := compute()
	y := validate(x)
	z := transform(y)
	w := persist(z)
	q := decorate%d(w)
	notify(q)
	return q
}
`, i, i)
		files[i] = BuildTokenizedFile(fmt.Sprintf("/f%d.go", i), content, lang)
	}

	// Baseline: no pair cap.
	baseline := DetectWithOptions(files, DetectOptions{
		MinTokens:     10,
		MinSimilarity: 0.6,
	})

	// Capped: pair cap = 1, which is virtually guaranteed to trip on the
	// first emitted pair. The fuzzy half must abort and return only the
	// exact clones (zero in this corpus, since no two files are identical).
	capped := DetectWithOptions(files, DetectOptions{
		MinTokens:     10,
		MinSimilarity: 0.6,
		MaxPairs:      1,
	})

	// The cap result must be a strict (or equal) subset of the baseline.
	// In our corpus the cap will produce 0 (no exact clones) while the
	// baseline may produce some fuzzy clones.
	if len(capped) > len(baseline) {
		t.Errorf("cap returned MORE clones than baseline: capped=%d baseline=%d", len(capped), len(baseline))
	}
	// And no clone in the capped result may be a fuzzy (type-3) one.
	for _, c := range capped {
		if c.Type == "type-3" {
			t.Errorf("MaxPairs=1 should have aborted fuzzy detection but a type-3 clone was returned: %+v", c)
		}
	}
}

// TestDetector_MaxPairs_Zero is the regression guard: MaxPairs=0 must mean
// "no cap" — same behavior as omitting the option entirely.
func TestDetector_MaxPairs_Zero(t *testing.T) {
	files := []TokenizedFile{
		makeTokenizedDup(t, "/a.go"),
		makeTokenizedDup(t, "/b.go"),
	}
	withZero := DetectWithOptions(files, DetectOptions{
		MinTokens:     10,
		MinSimilarity: 0.6,
		MaxPairs:      0,
	})
	withoutOption := DetectWithOptions(files, DetectOptions{
		MinTokens:     10,
		MinSimilarity: 0.6,
	})
	if len(withZero) != len(withoutOption) {
		t.Errorf("MaxPairs=0 changed clone count: %d vs %d", len(withZero), len(withoutOption))
	}
}

// TestScanner_MaxFiles_AppliedAfterIncludeFilter is a behavioral barrier
// test: the cap is checked against the post-filter file count, not the raw
// directory walk. So a user can set --max-files 5 and still scan a huge tree
// as long as --include narrows it to ≤5 files. Catches future regressions
// where someone moves the cap check upstream of include/exclude filtering.
func TestScanner_MaxFiles_AppliedAfterIncludeFilter(t *testing.T) {
	dir := t.TempDir()
	// Create 4 files we want to keep + 10 we want to filter out by extension.
	for i := 0; i < 4; i++ {
		writeTestFile(t, dir, fmt.Sprintf("f%d.go", i), "package main\n\nfunc f"+fmt.Sprint(i)+"() int { return 1 }\n")
	}
	if err := os.MkdirAll(filepath.Join(dir, "py"), 0o755); err != nil {
		t.Fatalf("mkdir py: %v", err)
	}
	for i := 0; i < 10; i++ {
		writeTestFile(t, dir, filepath.Join("py", fmt.Sprintf("v%d.py", i)), "def v"+fmt.Sprint(i)+"():\n    return 1\n")
	}

	svc := NewScannerService()
	// 14 source files exist, but --include narrows to 4 .go files. Cap=5 → pass.
	report, err := svc.Scan(ScanOptions{
		Path:      dir,
		MinTokens: 10,
		Include:   []string{"**/*.go"},
		MaxFiles:  5,
	})
	if err != nil {
		t.Fatalf("--include should narrow before cap, got error: %v", err)
	}
	if report.TotalFiles != 4 {
		t.Errorf("expected 4 .go files after --include, got %d", report.TotalFiles)
	}
}
