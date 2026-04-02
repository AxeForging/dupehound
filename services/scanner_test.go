package services

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScan_NoDuplicates(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", "package main\n\nfunc foo() {\n\ta()\n\tb()\n\tc()\n}\n")
	writeTestFile(t, dir, "b.go", "package main\n\nfunc bar() {\n\tx()\n\ty()\n\tz()\n}\n")

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 20})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalClones != 0 {
		t.Errorf("expected 0 clones, got %d", report.TotalClones)
	}
}

func TestScan_DetectsDuplicate(t *testing.T) {
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\tstore(x)\n\treturn x\n}\n"
	writeTestFile(t, dir, "a.go", "package main\n\n"+block)
	writeTestFile(t, dir, "b.go", "package main\n\n"+block)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10})
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

func TestScan_Type2Clone_RenamedVariable(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", `package main
func process() {
	result := compute()
	validate(result)
	store(result)
	notify(result)
	return result
}
`)
	writeTestFile(t, dir, "b.go", `package main
func process() {
	output := compute()
	validate(output)
	store(output)
	notify(output)
	return output
}
`)
	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 15})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalClones == 0 {
		t.Fatal("type-2 clone not detected: 'result' vs 'output' with same structure should match")
	}
}

func TestScan_CommentsIgnoredForMatching(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", `package main
func process() {
	// step one: compute
	x := compute()
	// step two: validate
	validate(x)
	store(x)
	return x
}
`)
	writeTestFile(t, dir, "b.go", `package main
func process() {
	x := compute()
	validate(x)
	store(x)
	return x
}
`)
	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalClones == 0 {
		t.Fatal("blocks that differ only by comments should be detected as clones")
	}
}

func TestScan_MergeAdjacentWindows(t *testing.T) {
	dir := t.TempDir()
	block := `package main
func bigHelper() {
	a := compute()
	b := validate(a)
	c := transform(b)
	d := store(c)
	e := notify(d)
	f := log(e)
	g := audit(f)
	return g
}
`
	writeTestFile(t, dir, "a.go", block)
	writeTestFile(t, dir, "b.go", block)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Count clones with ≥8 lines — should be exactly 1 (not one per window).
	large := 0
	for _, c := range report.Clones {
		if c.LineCount >= 8 {
			large++
		}
	}
	if large > 1 {
		t.Errorf("adjacent windows not merged: got %d large clones, expected 1", large)
	}
	if large == 0 {
		t.Error("expected the helper body to be detected as one large clone")
	}
}

func TestScan_ThreeWayClone(t *testing.T) {
	dir := t.TempDir()
	block := `package main
func dup() {
	x := compute()
	validate(x)
	store(x)
	notify(x)
	return x
}
`
	writeTestFile(t, dir, "a.go", block)
	writeTestFile(t, dir, "b.go", block)
	writeTestFile(t, dir, "c.go", block)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, c := range report.Clones {
		if len(c.Instances) >= 3 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a clone with 3 instances for three identical files")
	}
}

func TestScan_LanguageFilter(t *testing.T) {
	dir := t.TempDir()
	block := `def process():
    result = compute()
    validate(result)
    store(result)
    return result
`
	writeTestFile(t, dir, "a.py", block)
	writeTestFile(t, dir, "b.py", block)
	writeTestFile(t, dir, "c.go", "package main\nfunc main() {}\n")

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10, Language: "python"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalClones == 0 {
		t.Error("expected clones in python files")
	}
	// Go file should not have been scanned
	for _, c := range report.Clones {
		for _, inst := range c.Instances {
			if filepath.Ext(inst.File) == ".go" {
				t.Errorf("go file appeared in python-only scan: %s", inst.File)
			}
		}
	}
}

func TestScan_ExcludePattern(t *testing.T) {
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
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
		t.Errorf("expected 0 clones after exclusion, got %d", report.TotalClones)
	}
}

func TestScan_PathNotFound(t *testing.T) {
	svc := NewScannerService()
	_, err := svc.Scan(ScanOptions{Path: "/nonexistent/path", MinTokens: 20})
	if err == nil {
		t.Error("expected error for non-existent path")
	}
}

func TestScan_MinLinesDeprecatedFlag(t *testing.T) {
	// MinLines=3 should be converted to MinTokens=30 internally.
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeTestFile(t, dir, "a.go", "package main\n\n"+block)
	writeTestFile(t, dir, "b.go", "package main\n\n"+block)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinLines: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = report // just ensure it runs without panic
}

func TestScan_SameFileInternalDuplicate(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", `package main
func first() {
	x := compute()
	validate(x)
	store(x)
	notify(x)
	return x
}
func second() {
	x := compute()
	validate(x)
	store(x)
	notify(x)
	return x
}
`)
	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalClones == 0 {
		t.Error("expected clone for repeated function body within same file")
	}
}

func TestScan_DuplicateLinesCount(t *testing.T) {
	dir := t.TempDir()
	block := "func dup() {\n\ta()\n\tb()\n\tc()\n\td()\n\te()\n}\n"
	writeTestFile(t, dir, "a.go", "package main\n\n"+block)
	writeTestFile(t, dir, "b.go", "package main\n\n"+block)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.DuplicateLines == 0 {
		t.Error("expected duplicate line count > 0")
	}
}

func TestScan_CRLFLineEndings(t *testing.T) {
	// CRLF files should be detected as clones just like LF files.
	dir := t.TempDir()
	block := "package main\r\n\r\nfunc helper() {\r\n\tx := compute()\r\n\tvalidate(x)\r\n\tstore(x)\r\n\treturn x\r\n}\r\n"
	writeTestFile(t, dir, "a.go", block)
	writeTestFile(t, dir, "b.go", block)
	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalClones == 0 {
		t.Error("CRLF files should be detected as clones same as LF files")
	}
}

func TestScan_SingleFilePath(t *testing.T) {
	// A single file path (not a dir) should scan without crashing.
	dir := t.TempDir()
	path := writeTestFile(t, dir, "a.go", "package main\nfunc foo() { a()\nb()\nc() }\n")
	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: path, MinTokens: 5})
	if err != nil {
		t.Fatalf("single file scan returned unexpected error: %v", err)
	}
	if report.ScannedFiles != 1 {
		t.Errorf("expected 1 scanned file, got %d", report.ScannedFiles)
	}
}

func TestScan_GlobExclude_GenFiles(t *testing.T) {
	// *.gen.go files should be excluded by glob pattern.
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeTestFile(t, dir, "a.go", "package main\n\n"+block)
	writeTestFile(t, dir, "b.gen.go", "package main\n\n"+block)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{
		Path:      dir,
		MinTokens: 10,
		Exclude:   []string{"*.gen.go"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalClones != 0 {
		t.Errorf("expected 0 clones after excluding *.gen.go, got %d", report.TotalClones)
	}
}

func TestScan_EmptyDirectory(t *testing.T) {
	// Empty directory produces no source files; scanner returns an error (not a crash).
	dir := t.TempDir()
	svc := NewScannerService()
	_, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10})
	if err == nil {
		t.Error("expected an error for empty directory (no source files), got nil")
	}
}

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeTestFile: %v", err)
	}
	return path
}
