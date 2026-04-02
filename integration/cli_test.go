package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	bin := filepath.Join(t.TempDir(), "dupehound-test"+ext)
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, string(out))
	}
	return bin
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(wd)
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	return path
}

func TestHelp(t *testing.T) {
	bin := buildBinary(t)
	out, err := exec.Command(bin, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("--help failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "dupehound") {
		t.Errorf("--help output missing 'dupehound': %s", string(out))
	}
}

func TestVersion(t *testing.T) {
	bin := buildBinary(t)
	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("version failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "dupehound version") {
		t.Errorf("version output unexpected: %s", string(out))
	}
}

func TestScanNoDuplicates(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n\nfunc foo() {\n\ta()\n\tb()\n\tc()\n}\n")
	writeFile(t, dir, "b.go", "package main\n\nfunc bar() {\n\tx()\n\ty()\n\tz()\n}\n")

	out, err := exec.Command(bin, "scan", "--path", dir, "--min-lines", "3").CombinedOutput()
	if err != nil {
		t.Fatalf("scan failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "No duplicates found") {
		t.Errorf("expected 'No duplicates found', got: %s", string(out))
	}
}

func TestScanDetectsDuplicate(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	out, err := exec.Command(bin, "scan", "--path", dir, "--min-lines", "3").CombinedOutput()
	if err != nil {
		t.Fatalf("scan failed: %v\n%s", err, string(out))
	}
	if strings.Contains(string(out), "No duplicates found") {
		t.Errorf("expected duplicates to be found, got: %s", string(out))
	}
}

func TestScanJSONOutput(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func dup() {\n\ta()\n\tb()\n\tc()\n\td()\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	cmd := exec.Command(bin, "scan", "--path", dir, "--min-lines", "3", "--format", "json")
	out, err := cmd.Output() // stdout only — logger writes to stderr
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	var report domain.Report
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, string(out))
	}
	if report.ScannedFiles != 2 {
		t.Errorf("expected 2 scanned files, got %d", report.ScannedFiles)
	}
}

func TestScanSARIFOutput(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func dup() {\n\ta()\n\tb()\n\tc()\n\td()\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	cmd := exec.Command(bin, "scan", "--path", dir, "--min-lines", "3", "--format", "sarif")
	out, err := cmd.Output() // stdout only — logger writes to stderr
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	var sarif map[string]interface{}
	if err := json.Unmarshal(out, &sarif); err != nil {
		t.Fatalf("invalid SARIF JSON: %v\n%s", err, string(out))
	}
	if sarif["version"] != "2.1.0" {
		t.Errorf("expected SARIF version 2.1.0, got %v", sarif["version"])
	}
}

func TestScanFileOutput(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func dup() {\n\ta()\n\tb()\n\tc()\n\td()\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)
	outFile := filepath.Join(dir, "result.txt")

	cmd := exec.Command(bin, "scan", "--path", dir, "--min-lines", "3", "--output", outFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("scan failed: %v\n%s", err, string(out))
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("output file not created: %v", err)
	}
	if len(data) == 0 {
		t.Error("output file is empty")
	}
}

func TestScanInvalidFormat(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n")

	cmd := exec.Command(bin, "scan", "--path", dir, "--format", "xml")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected error for invalid format, got output: %s", string(out))
	}
}

func TestScanExclude(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func dup() {\n\ta()\n\tb()\n\tc()\n\td()\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	out, err := exec.Command(bin, "scan", "--path", dir, "--min-lines", "3", "--exclude", "b.go").CombinedOutput()
	if err != nil {
		t.Fatalf("scan failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "No duplicates found") {
		t.Errorf("expected no duplicates after exclude, got: %s", string(out))
	}
}
