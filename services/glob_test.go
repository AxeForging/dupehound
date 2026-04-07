package services

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMatchGlob_DoubleStarExclude verifies ** glob matching.
func TestMatchGlob_DoubleStarExclude(t *testing.T) {
	tests := []struct {
		pattern  string
		path     string
		expected bool
	}{
		{"vendor/**", "vendor/pkg/file.go", true},
		{"vendor/**", "src/file.go", false},
		{"**/*_test.go", "services/scanner_test.go", true},
		{"**/*_test.go", "services/scanner.go", false},
		{"*.go", "file.go", true},     // no ** → basename match
		{"*.go", "dir/file.go", true}, // no ** → basename match (dir/file.go basename is file.go)
		{"*.go", "other.js", false},
	}

	for _, tt := range tests {
		got := matchGlob(tt.pattern, tt.path)
		if got != tt.expected {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.expected)
		}
	}
}

// TestScan_ExcludeDoubleStarVendor verifies --exclude "vendor/**" excludes all vendor files.
func TestScan_ExcludeDoubleStarVendor(t *testing.T) {
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

	vendorDir := filepath.Join(dir, "vendor", "pkg")
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		t.Fatalf("mkdir vendor: %v", err)
	}
	vendorFile := filepath.Join(vendorDir, "util.go")
	if err := os.WriteFile(vendorFile, []byte("package pkg\n\n"+block), 0o600); err != nil {
		t.Fatalf("write vendor file: %v", err)
	}

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{
		Path:      dir,
		MinTokens: 10,
		Exclude:   []string{"vendor/**"},
	})
	// With only a.go, there can be no clones.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalClones != 0 {
		t.Errorf("expected 0 clones after excluding vendor/**, got %d (vendor files should be excluded)", report.TotalClones)
	}
}

// TestScan_ExcludeTestFiles verifies --exclude "**/*_test.go" excludes all test files.
func TestScan_ExcludeTestFiles(t *testing.T) {
	dir := t.TempDir()
	block := `func helper() {
	x := compute()
	process(x)
	log(x)
	return x
}
`
	writeTestFile(t, dir, "a.go", "package main\n\n"+block)
	writeTestFile(t, dir, "a_test.go", "package main\n\n"+block)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{
		Path:      dir,
		MinTokens: 10,
		Exclude:   []string{"**/*_test.go"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only a.go scanned, no clones possible.
	if report.TotalClones != 0 {
		t.Errorf("expected 0 clones after excluding test files, got %d", report.TotalClones)
	}
}

// TestScan_ExcludeNoDoublestar verifies simple *.go patterns still work.
func TestScan_ExcludeNoDoublestar(t *testing.T) {
	dir := t.TempDir()
	block := `func helper() {
	x := compute()
	process(x)
	log(x)
	return x
}
`
	writeTestFile(t, dir, "a.go", "package main\n\n"+block)
	writeTestFile(t, dir, "b.go", "package main\n\n"+block)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{
		Path:      dir,
		MinTokens: 10,
		Exclude:   []string{"b.go"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalClones != 0 {
		t.Errorf("expected 0 clones after excluding b.go, got %d", report.TotalClones)
	}
}

// TestScan_IncludeFlag verifies --include only scans matching files.
func TestScan_IncludeFlag(t *testing.T) {
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
	writeTestFile(t, dir, "b.go", "package main\n\n"+block)

	pyDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(pyDir, 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	// Write a Python file - should NOT be included when include is *.go only.
	pyFile := filepath.Join(pyDir, "util.py")
	if err := os.WriteFile(pyFile, []byte("def helper():\n\tx = compute()\n\tprocess(x)\n"), 0o600); err != nil {
		t.Fatalf("write py file: %v", err)
	}

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{
		Path:      dir,
		MinTokens: 10,
		Include:   []string{"**/*.go"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only .go files scanned; clones possible since a.go and b.go are duplicates.
	if report.ScannedFiles < 2 {
		t.Errorf("expected at least 2 scanned .go files, got %d", report.ScannedFiles)
	}
	// No Python file in clone instances.
	for _, clone := range report.Clones {
		for _, inst := range clone.Instances {
			if filepath.Ext(inst.File) == ".py" {
				t.Errorf("Python file should not appear in clones when include is **/*.go")
			}
		}
	}
}

// TestScan_IncludeNoFlag verifies that without --include, all supported files are scanned.
func TestScan_IncludeNoFlag(t *testing.T) {
	dir := t.TempDir()
	block := `func helper() {
	x := compute()
	process(x)
	log(x)
	return x
}
`
	writeTestFile(t, dir, "a.go", "package main\n\n"+block)
	writeTestFile(t, dir, "b.go", "package main\n\n"+block)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.ScannedFiles != 2 {
		t.Errorf("expected 2 scanned files, got %d", report.ScannedFiles)
	}
}

// TestScan_IncludeAndExcludeCombined verifies that include + exclude are both applied.
func TestScan_IncludeAndExcludeCombined(t *testing.T) {
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
	writeTestFile(t, dir, "b.go", "package main\n\n"+block)
	writeTestFile(t, dir, "a_test.go", "package main\n\n"+block)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{
		Path:      dir,
		MinTokens: 10,
		Include:   []string{"**/*.go"},
		Exclude:   []string{"**/*_test.go"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// a_test.go should be excluded; a.go and b.go included.
	for _, clone := range report.Clones {
		for _, inst := range clone.Instances {
			if filepath.Base(inst.File) == "a_test.go" {
				t.Errorf("a_test.go should be excluded by --exclude flag")
			}
		}
	}
}

// TestMatchesIncludes verifies matchesIncludes correctly handles empty and non-empty slices.
func TestMatchesIncludes(t *testing.T) {
	tests := []struct {
		include  []string
		path     string
		expected bool
	}{
		{nil, "any/file.go", true},        // no include → match all
		{[]string{}, "any/file.go", true}, // empty include → match all
		{[]string{"**/*.go"}, "src/main.go", true},
		{[]string{"**/*.go"}, "src/main.py", false},
		{[]string{"src/**"}, "src/main.go", true},
		{[]string{"src/**"}, "lib/main.go", false},
	}
	for _, tt := range tests {
		got := matchesIncludes(tt.path, tt.include)
		if got != tt.expected {
			t.Errorf("matchesIncludes(%q, %v) = %v, want %v", tt.path, tt.include, got, tt.expected)
		}
	}
}
