package services

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AxeForging/dupehound/domain"
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

// TestDetector_MaxPairs_CapsFuzzyGracefully is the feature test for the
// --max-pairs runaway backstop. When the candidate pair count exceeds the cap,
// type-3 detection stops at the next block boundary and returns the matches
// found so far — a deterministic PARTIAL result — rather than discarding all
// type-3 work. Type-1/2 clones are always complete.
//
// We construct a many-file near-duplicate corpus (lots of fuzzy candidate
// pairs), then assert that a tight cap yields a deterministic, strict subset of
// the uncapped result instead of either crashing or blacking out all type-3.
func TestDetector_MaxPairs_CapsFuzzyGracefully(t *testing.T) {
	// 8 near-miss functions with identical structure except for two scattered
	// operator flips per file. Operators are distinct token kinds (not
	// identifiers or literals), so they survive normalization and stop the
	// files from collapsing into exact type-2 clones; scattering them ensures
	// no full window is exact-covered, so the divergent windows fall to the
	// fuzzy (type-3) path and generate many overlapping candidate pairs.
	const minTokens = 20
	const minSim = 0.6
	var files []TokenizedFile
	for i := 0; i < 8; i++ {
		ops := []string{"+", "+", "+", "+", "+", "+", "+", "+", "+", "+", "+", "+"}
		ops[i%len(ops)] = "-"
		ops[(i*2+3)%len(ops)] = "*"
		var sb strings.Builder
		sb.WriteString("package main\nfunc calc(items []int) int {\n\tacc := 0\n\tfor _, item := range items {\n")
		for _, op := range ops {
			fmt.Fprintf(&sb, "\t\tacc = acc %s item\n", op)
		}
		sb.WriteString("\t}\n\treturn acc\n}\n")
		files = append(files, makeFile(fmt.Sprintf("f%d.go", i), sb.String()))
	}

	// Baseline: no pair cap → full type-3 coverage.
	baseline, _ := DetectWithOptions(files, DetectOptions{
		MinTokens:     minTokens,
		MinSimilarity: minSim,
	})
	baseT3 := countType3(baseline)
	if baseT3 == 0 {
		t.Fatal("fixture produced no type-3 clones; it no longer exercises the cap")
	}

	// Capped: pair cap = 1 trips almost immediately, so detection stops after
	// the first block's worth of pairs.
	opts := DetectOptions{MinTokens: minTokens, MinSimilarity: minSim, MaxPairs: 1}
	capped, _ := DetectWithOptions(files, opts)

	// 1. Partial, not a blackout, not more than baseline.
	if len(capped) > len(baseline) {
		t.Errorf("cap returned MORE clones than baseline: capped=%d baseline=%d", len(capped), len(baseline))
	}
	if cappedT3 := countType3(capped); cappedT3 >= baseT3 {
		t.Errorf("cap did not reduce type-3: capped=%d baseline=%d (expected a partial subset)", cappedT3, baseT3)
	}

	// 2. The partial result must be deterministic across runs — the whole point
	// of cutting on a block boundary rather than mid-block.
	first := fingerprint(capped)
	for run := 1; run < 10; run++ {
		rerun, _ := DetectWithOptions(files, opts)
		if got := fingerprint(rerun); got != first {
			t.Fatalf("capped result is non-deterministic\nrun 0:\n%s\nrun %d:\n%s", first, run, got)
		}
	}
}

func countType3(clones []domain.Clone) int {
	n := 0
	for _, c := range clones {
		if c.Type == domain.CloneType3 {
			n++
		}
	}
	return n
}

// TestDetector_MaxPairs_Zero is the regression guard: MaxPairs=0 must mean
// "no cap" — same behavior as omitting the option entirely.
func TestDetector_MaxPairs_Zero(t *testing.T) {
	files := []TokenizedFile{
		makeTokenizedDup(t, "/a.go"),
		makeTokenizedDup(t, "/b.go"),
	}
	withZero, _ := DetectWithOptions(files, DetectOptions{
		MinTokens:     10,
		MinSimilarity: 0.6,
		MaxPairs:      0,
	})
	withoutOption, _ := DetectWithOptions(files, DetectOptions{
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
