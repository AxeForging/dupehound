package services

import (
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

// TestDeadCode_CalledFunction verifies a called function is not reported as dead.
func TestDeadCode_CalledFunction(t *testing.T) {
	dir := t.TempDir()
	// File A defines compute(); file B calls compute().
	writeTestFile(t, dir, "a.go", `package main
func compute() int {
	x := 1
	y := 2
	return x + y
}
`)
	writeTestFile(t, dir, "b.go", `package main
func main() {
	result := compute()
	_ = result
}
`)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 5, DeadCode: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, df := range report.DeadFunctions {
		if df.Name == "compute" {
			t.Errorf("compute() should not be detected as dead (it is called in b.go)")
		}
	}
}

// TestDeadCode_UnusedFunction verifies a never-called function is detected as dead.
func TestDeadCode_UnusedFunction(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", `package main
func orphanFunction() int {
	x := 42
	y := x + 1
	return y
}
func main() {
	_ = 0
}
`)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 5, DeadCode: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, df := range report.DeadFunctions {
		if df.Name == "orphanFunction" {
			found = true
		}
	}
	if !found {
		t.Error("expected orphanFunction to be detected as dead")
	}
}

// TestDeadCode_TestOnlyHelperNotFlagged is the regression test for the false
// positive that dupehound flagged on its OWN PR #24: a function defined in
// production code, called only from a _test.go file, must NOT be reported as
// dead — even when the user excluded test files via --exclude. The fix is
// that the scanner builds a usage-only background tokenized set from a
// no-exclude collection pass, so excluded files still contribute to the
// identifier-usage map even though they don't appear in clone reports.
func TestDeadCode_TestOnlyHelperNotFlagged(t *testing.T) {
	dir := t.TempDir()
	// Production file: defines testOnlyHelper. No production caller.
	writeTestFile(t, dir, "lib.go", `package main
func testOnlyHelper(x int) int {
	return x * 2
}
func main() {
	_ = 0
}
`)
	// Test file: calls testOnlyHelper. Excluded from the scan via --exclude.
	writeTestFile(t, dir, "lib_test.go", `package main
func TestSomething() {
	got := testOnlyHelper(21)
	_ = got
}
`)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{
		Path:      dir,
		MinTokens: 5,
		DeadCode:  true,
		Exclude:   []string{"*_test.go"}, // the configuration that triggered the bug on PR #24
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// testOnlyHelper has a real caller (in the excluded test file). The
	// pre-fix code would report it as dead because the test file wasn't in
	// the tokenized set. After the fix, the usage background pass picks it
	// up and the function is correctly NOT flagged.
	for _, df := range report.DeadFunctions {
		if df.Name == "testOnlyHelper" {
			t.Errorf("testOnlyHelper has a caller in the excluded test file and must NOT be reported as dead; got: %+v", report.DeadFunctions)
		}
	}
}

// TestDeadCode_GenuinelyDeadStillFlaggedWithExcludes is the matching positive
// barrier: the broader usage scan must NOT cause genuinely-dead functions to
// be missed. A function with no callers anywhere — including tests — should
// still be flagged even when excludes are active.
func TestDeadCode_GenuinelyDeadStillFlaggedWithExcludes(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "lib.go", `package main
func reallyDead(x int) int {
	return x + 1
}
func main() {
	_ = 0
}
`)
	writeTestFile(t, dir, "lib_test.go", `package main
func TestSomethingElse() {
	_ = 0
}
`)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{
		Path:      dir,
		MinTokens: 5,
		DeadCode:  true,
		Exclude:   []string{"*_test.go"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, df := range report.DeadFunctions {
		if df.Name == "reallyDead" {
			found = true
		}
	}
	if !found {
		t.Errorf("reallyDead has zero callers anywhere and must still be flagged with excludes active; got: %+v", report.DeadFunctions)
	}
}

// TestDeadCode_AbsentFlag verifies that --dead-code absent means no dead function output.
func TestDeadCode_AbsentFlag(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", `package main
func orphanFunction() int {
	x := 42
	return x
}
func main() {
	_ = 0
}
`)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 5, DeadCode: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.DeadFunctions) != 0 {
		t.Errorf("expected 0 dead functions when --dead-code not set, got %d", len(report.DeadFunctions))
	}
}

// TestDeadCode_JSONOutput verifies dead functions appear in JSON output.
func TestDeadCode_JSONOutput(t *testing.T) {
	report := &domain.Report{
		TotalFiles:   1,
		ScannedFiles: 1,
		DeadFunctions: []domain.DeadFunc{
			{File: "src/util.go", Line: 44, Name: "parseOldFormat", Language: "go"},
		},
	}
	out, err := FormatReport(report, "json", FormatOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsStr(out, "parseOldFormat") {
		t.Error("expected dead function name in JSON output")
	}
	if !containsStr(out, "dead_functions") {
		t.Error("expected dead_functions key in JSON output")
	}
}

// TestDeadCode_TextOutput verifies dead functions appear in text output.
func TestDeadCode_TextOutput(t *testing.T) {
	report := &domain.Report{
		TotalFiles:   1,
		ScannedFiles: 1,
		TotalClones:  0,
		DeadFunctions: []domain.DeadFunc{
			{File: "/src/utils/parse.go", Line: 44, Name: "parseOldFormat", Language: "go"},
		},
	}
	out, err := FormatReport(report, "text", FormatOptions{ScanPath: "/src"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsStr(out, "Dead functions") {
		t.Error("expected 'Dead functions' section in text output")
	}
	if !containsStr(out, "parseOldFormat") {
		t.Error("expected dead function name in text output")
	}
	if !containsStr(out, "heuristic") {
		t.Error("expected heuristic disclaimer in text output")
	}
}

func containsStr(s, sub string) bool {
	return len(s) > 0 && len(sub) > 0 && func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}
