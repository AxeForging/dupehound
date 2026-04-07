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
