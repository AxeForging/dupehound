package services

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScan_NoDuplicates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n\nfunc foo() {\n\tx := 1\n\treturn x\n}\n")
	writeFile(t, dir, "b.go", "package main\n\nfunc bar() {\n\ty := 2\n\treturn y\n}\n")

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinLines: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalClones != 0 {
		t.Errorf("expected 0 clones, got %d", report.TotalClones)
	}
}

func TestScan_DetectsDuplicate(t *testing.T) {
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinLines: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalClones == 0 {
		t.Error("expected at least one clone, got 0")
	}
	if report.ScannedFiles != 2 {
		t.Errorf("expected 2 scanned files, got %d", report.ScannedFiles)
	}
}

func TestScan_SameFileNoDuplicate(t *testing.T) {
	// A block that only appears once should not be reported
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n\nfunc foo() {\n\ta()\n\tb()\n\tc()\n}\n")

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinLines: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalClones != 0 {
		t.Errorf("expected 0 clones, got %d", report.TotalClones)
	}
}

func TestScan_LanguageFilter(t *testing.T) {
	dir := t.TempDir()
	block := "def compute():\n    x = 1\n    y = 2\n    return x + y\n"
	writeFile(t, dir, "a.py", block)
	writeFile(t, dir, "b.py", block)
	writeFile(t, dir, "c.go", "package main\nfunc main() {}\n")

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinLines: 3, Language: "python"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalClones == 0 {
		t.Error("expected clones in python files")
	}
}

func TestScan_ExcludePattern(t *testing.T) {
	dir := t.TempDir()
	block := "func helper() {\n\tx := 1\n\ty := 2\n\tz := 3\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{
		Path:     dir,
		MinLines: 3,
		Exclude:  []string{"b.go"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only a.go scanned, no duplicates possible
	if report.TotalClones != 0 {
		t.Errorf("expected 0 clones after exclusion, got %d", report.TotalClones)
	}
}

func TestScan_PathNotFound(t *testing.T) {
	svc := NewScannerService()
	_, err := svc.Scan(ScanOptions{Path: "/nonexistent/path", MinLines: 5})
	if err == nil {
		t.Error("expected error for non-existent path")
	}
}

func TestScan_DuplicateLinesCount(t *testing.T) {
	dir := t.TempDir()
	block := "func dup() {\n\ta()\n\tb()\n\tc()\n\td()\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinLines: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.DuplicateLines == 0 {
		t.Error("expected duplicate line count > 0")
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	return path
}
