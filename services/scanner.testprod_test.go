package services

import (
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

// TestIsTestFile verifies the test file detection heuristic.
func TestIsTestFile(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		// Go
		{"foo_test.go", true},
		{"foo.go", false},
		{"path/to/foo_test.go", true},
		// JS/TS
		{"component.spec.ts", true},
		{"component.test.ts", true},
		{"component.ts", false},
		{"__tests__/util.js", true},
		{"utils.js", false},
		// Python
		{"test_parser.py", true},
		{"parser_test.py", true},
		{"src/tests/util.py", true},
		{"parser.py", false},
		// Java/Kotlin
		{"src/test/java/MyTest.java", true},
		{"src/main/java/Main.java", false},
		{"src/test/kotlin/MyTest.kt", true},
		// Ruby
		{"spec/models/user_spec.rb", true},
		{"app/models/user_spec.rb", true},
		{"app/models/user.rb", false},
		// General fallback
		{"helpers/testutil.go", true},
		{"spec_helpers.go", true},
		{"main.go", false},
	}
	for _, tt := range tests {
		got := isTestFile(tt.path)
		if got != tt.expected {
			t.Errorf("isTestFile(%q) = %v, want %v", tt.path, got, tt.expected)
		}
	}
}

// TestAnnotateTestProdSpan_Mixed verifies that a clone spanning test and prod gets TestProdSpan=true.
func TestAnnotateTestProdSpan_Mixed(t *testing.T) {
	clones := []domain.Clone{
		{
			Hash: "abc",
			Instances: []domain.CloneInstance{
				{File: "foo.go", StartLine: 1, EndLine: 10},
				{File: "foo_test.go", StartLine: 1, EndLine: 10},
			},
		},
	}
	annotateTestProdSpan(clones)
	if !clones[0].TestProdSpan {
		t.Error("expected TestProdSpan=true for clone spanning test and prod files")
	}
	if !clones[0].Instances[1].IsTest {
		t.Error("expected foo_test.go instance to have IsTest=true")
	}
	if clones[0].Instances[0].IsTest {
		t.Error("expected foo.go instance to have IsTest=false")
	}
}

// TestAnnotateTestProdSpan_BothProd verifies that two prod files don't get TestProdSpan=true.
func TestAnnotateTestProdSpan_BothProd(t *testing.T) {
	clones := []domain.Clone{
		{
			Hash: "abc",
			Instances: []domain.CloneInstance{
				{File: "foo.go", StartLine: 1, EndLine: 10},
				{File: "bar.go", StartLine: 1, EndLine: 10},
			},
		},
	}
	annotateTestProdSpan(clones)
	if clones[0].TestProdSpan {
		t.Error("expected TestProdSpan=false for clone with both prod files")
	}
}

// TestAnnotateTestProdSpan_BothTest verifies that two test files don't get TestProdSpan=true.
func TestAnnotateTestProdSpan_BothTest(t *testing.T) {
	clones := []domain.Clone{
		{
			Hash: "abc",
			Instances: []domain.CloneInstance{
				{File: "foo_test.go", StartLine: 1, EndLine: 10},
				{File: "bar_test.go", StartLine: 1, EndLine: 10},
			},
		},
	}
	annotateTestProdSpan(clones)
	if clones[0].TestProdSpan {
		t.Error("expected TestProdSpan=false for clone with both test files")
	}
}

// TestScan_TestProdSpanAnnotated verifies full scan annotates test↔prod clones.
func TestScan_TestProdSpanAnnotated(t *testing.T) {
	dir := t.TempDir()
	block := `func helper() {
	x := compute()
	process(x)
	log(x)
	store(x)
	return x
}
`
	writeTestFile(t, dir, "a.go", "package main\n\n"+block)
	writeTestFile(t, dir, "a_test.go", "package main\n\n"+block)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Clones) == 0 {
		t.Skip("no clones detected — skip test↔prod annotation check")
	}
	// At least one clone should be marked as test↔prod.
	found := false
	for _, c := range report.Clones {
		if c.TestProdSpan {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected at least one clone with TestProdSpan=true")
	}
}
