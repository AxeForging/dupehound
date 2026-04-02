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

	out, err := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "30").CombinedOutput()
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
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\tstore(x)\n\treturn x\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	out, err := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "10", "--exit-zero").CombinedOutput()
	if err != nil {
		t.Fatalf("scan failed: %v\n%s", err, string(out))
	}
	if strings.Contains(string(out), "No duplicates found") {
		t.Errorf("expected duplicates to be found, got: %s", string(out))
	}
}

func TestScanType2Clone(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.go", `package main
func process() {
	result := compute()
	validate(result)
	store(result)
	notify(result)
	return result
}
`)
	writeFile(t, dir, "b.go", `package main
func process() {
	output := compute()
	validate(output)
	store(output)
	notify(output)
	return output
}
`)
	out, err := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "15", "--exit-zero").CombinedOutput()
	if err != nil {
		t.Fatalf("scan failed: %v\n%s", err, string(out))
	}
	if strings.Contains(string(out), "No duplicates found") {
		t.Errorf("type-2 clone not detected (renamed variables): %s", string(out))
	}
}

func TestScanJSONOutput(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func dup() {\n\ta := compute()\n\tb := validate(a)\n\tc := store(b)\n\treturn c\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	cmd := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "10", "--format", "json", "--exit-zero")
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
	block := "func dup() {\n\ta := compute()\n\tb := validate(a)\n\tc := store(b)\n\treturn c\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	cmd := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "10", "--format", "sarif", "--exit-zero")
	out, err := cmd.Output()
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
	block := "func dup() {\n\ta := compute()\n\tb := validate(a)\n\tc := store(b)\n\treturn c\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)
	outFile := filepath.Join(dir, "result.txt")

	cmd := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "10", "--output", outFile, "--exit-zero")
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
		t.Errorf("expected error for invalid format, got: %s", string(out))
	}
}

func TestScanExclude(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	out, err := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "10", "--exclude", "b.go").CombinedOutput()
	if err != nil {
		t.Fatalf("scan failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "No duplicates found") {
		t.Errorf("expected no duplicates after exclude, got: %s", string(out))
	}
}

func TestScanMinLinesDeprecatedStillWorks(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	// --min-lines=3 should be accepted and converted to tokens internally.
	out, err := exec.Command(bin, "scan", "--path", dir, "--min-lines", "3", "--exit-zero").CombinedOutput()
	if err != nil {
		t.Fatalf("--min-lines flag rejected: %v\n%s", err, string(out))
	}
}

func TestScanSingleFile(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	path := writeFile(t, dir, "a.go", "package main\nfunc foo() { a()\nb()\nc() }\n")

	out, err := exec.Command(bin, "scan", "--path", path, "--min-tokens", "5").CombinedOutput()
	if err != nil {
		t.Fatalf("single file scan failed: %v\n%s", err, string(out))
	}
	// Single file cannot have cross-file clones; "No duplicates found" is expected.
	if !strings.Contains(string(out), "No duplicates found") {
		t.Errorf("expected 'No duplicates found' for single file scan, got: %s", string(out))
	}
}

func TestScanEmptyDirectory(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	// Empty dir: no source files — binary should exit non-zero with a clean error.
	cmd := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "10")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected non-zero exit for empty directory, got: %s", string(out))
	}
	if len(out) == 0 {
		t.Error("expected some error output for empty directory, got none")
	}
}

func TestScanSARIFResultsStructure(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func dup() {\n\ta := compute()\n\tb := validate(a)\n\tc := store(b)\n\treturn c\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	cmd := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "10", "--format", "sarif", "--exit-zero")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	var sarif map[string]interface{}
	if err := json.Unmarshal(out, &sarif); err != nil {
		t.Fatalf("invalid SARIF JSON: %v\n%s", err, string(out))
	}
	runs, ok := sarif["runs"].([]interface{})
	if !ok || len(runs) == 0 {
		t.Fatal("SARIF missing 'runs' array")
	}
	run, ok := runs[0].(map[string]interface{})
	if !ok {
		t.Fatal("SARIF runs[0] is not an object")
	}
	if _, ok := run["results"]; !ok {
		t.Error("SARIF runs[0] missing 'results' field")
	}
	if _, ok := run["tool"]; !ok {
		t.Error("SARIF runs[0] missing 'tool' field")
	}
}

func TestScanMergeAdjacentWindows(t *testing.T) {
	bin := buildBinary(t)
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
	writeFile(t, dir, "a.go", block)
	writeFile(t, dir, "b.go", block)

	cmd := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "10", "--format", "json", "--exit-zero")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	var report domain.Report
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Count clones ≥8 lines: should be 1, not many
	large := 0
	for _, c := range report.Clones {
		if c.LineCount >= 8 {
			large++
		}
	}
	if large > 1 {
		t.Errorf("window merging broken: %d large clones, expected 1", large)
	}
}

// --- Exit code semantics ---

func TestExitCode_ClonesFound_ExitsOne(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	cmd := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "10")
	if err := cmd.Run(); err == nil {
		t.Fatal("expected exit code 1 when clones found, got 0")
	}
	if code := cmd.ProcessState.ExitCode(); code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

func TestExitCode_NoClonesFound_ExitsZero(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\nfunc foo() { a()\nb()\nc() }\n")
	writeFile(t, dir, "b.go", "package main\nfunc bar() { x()\ny()\nz() }\n")

	cmd := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "30")
	if err := cmd.Run(); err != nil {
		t.Errorf("expected exit code 0 when no clones, got: %v (code %d)", err, cmd.ProcessState.ExitCode())
	}
}

func TestExitCode_ExitZeroFlag_SuppressesOne(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	// Clones exist but --exit-zero forces exit 0.
	cmd := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "10", "--exit-zero")
	if err := cmd.Run(); err != nil {
		t.Errorf("expected exit code 0 with --exit-zero, got: %v (code %d)", err, cmd.ProcessState.ExitCode())
	}
}

func TestExitCode_ToolError_ExitsTwo(t *testing.T) {
	bin := buildBinary(t)

	cmd := exec.Command(bin, "scan", "--path", "/nonexistent/path/dupehound-test")
	cmd.Run() //nolint:errcheck
	if code := cmd.ProcessState.ExitCode(); code != 2 {
		t.Errorf("expected exit code 2 for tool error, got %d", code)
	}
}
