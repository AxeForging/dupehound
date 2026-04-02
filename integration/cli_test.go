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

// --- Config file ---

func TestConfigFile_SettingsRespected(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	// Write config with min-tokens=10 and exit-zero=true.
	writeFile(t, dir, ".dupehound.yml", "scan:\n  path: "+dir+"\n  min-tokens: 10\n  exit-zero: true\n")

	// Run scan with no flags — config should supply path and min-tokens.
	cmd := exec.Command(bin, "scan", "--config", filepath.Join(dir, ".dupehound.yml"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("scan with config failed: %v\n%s", err, string(out))
	}
	if strings.Contains(string(out), "No duplicates found") {
		t.Errorf("expected clones to be found via config, got: %s", string(out))
	}
}

func TestConfigFile_CLIFlagOverridesConfig(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	// Config sets min-tokens=10 (would find clones), but CLI overrides with 1000 (won't find any).
	writeFile(t, dir, ".dupehound.yml", "scan:\n  min-tokens: 10\n")

	out, err := exec.Command(bin, "scan",
		"--config", filepath.Join(dir, ".dupehound.yml"),
		"--path", dir,
		"--min-tokens", "1000",
		"--exit-zero",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("scan failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "No duplicates found") {
		t.Errorf("expected CLI --min-tokens=1000 to override config, got: %s", string(out))
	}
}

func TestConfigFile_ExcludeFromConfig(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.gen.go", "package main\n\n"+block)

	writeFile(t, dir, ".dupehound.yml", "scan:\n  min-tokens: 10\n  exclude:\n    - \"*.gen.go\"\n  exit-zero: true\n")

	out, err := exec.Command(bin, "scan",
		"--config", filepath.Join(dir, ".dupehound.yml"),
		"--path", dir,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("scan failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "No duplicates found") {
		t.Errorf("expected *.gen.go exclusion from config to prevent clone, got: %s", string(out))
	}
}

func TestInitCommand_CreatesConfigFile(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	cmd := exec.Command(bin, "init")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init failed: %v\n%s", err, string(out))
	}

	data, err := os.ReadFile(filepath.Join(dir, ".dupehound.yml"))
	if err != nil {
		t.Fatal(".dupehound.yml not created")
	}
	if !strings.Contains(string(data), "min-tokens") {
		t.Error(".dupehound.yml missing expected fields")
	}
}

func TestInitCommand_FailsIfAlreadyExists(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, ".dupehound.yml", "scan:\n  min-tokens: 10\n")

	cmd := exec.Command(bin, "init")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected error when .dupehound.yml already exists, got: %s", string(out))
	}
}

func TestInitCommand_ForceOverwrites(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, ".dupehound.yml", "scan:\n  min-tokens: 10\n")

	cmd := exec.Command(bin, "init", "--force")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init --force failed: %v\n%s", err, string(out))
	}
}

func TestConfigFile_AutoDiscovery(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	// Config placed in dir with no --config flag; binary runs from dir.
	writeFile(t, dir, ".dupehound.yml", "scan:\n  path: .\n  min-tokens: 10\n  exit-zero: true\n")

	cmd := exec.Command(bin, "scan")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto-discovery scan failed: %v\n%s", err, string(out))
	}
	if strings.Contains(string(out), "No duplicates found") {
		t.Errorf("expected clones detected via auto-discovered config, got: %s", string(out))
	}
}

func TestConfigFile_AutoDiscovery_WalksUp(t *testing.T) {
	bin := buildBinary(t)
	parent := t.TempDir()
	sub := filepath.Join(parent, "pkg", "foo")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeFile(t, sub, "a.go", "package foo\n\n"+block)
	writeFile(t, sub, "b.go", "package foo\n\n"+block)

	// Config lives at parent, scan runs from sub — must walk up and find it.
	writeFile(t, parent, ".dupehound.yml", "scan:\n  path: .\n  min-tokens: 10\n  exit-zero: true\n")

	cmd := exec.Command(bin, "scan", "--path", sub)
	cmd.Dir = sub
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("walk-up config discovery failed: %v\n%s", err, string(out))
	}
	if strings.Contains(string(out), "No duplicates found") {
		t.Errorf("expected clones found using parent config min-tokens, got: %s", string(out))
	}
}

func TestConfigFile_MissingExplicitPath_ReturnsError(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	cmd := exec.Command(bin, "scan", "--config", filepath.Join(dir, "nonexistent.yml"), "--path", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected error for missing --config file, got output: %s", string(out))
	}
}

func TestConfigFile_InvalidYAML_ReturnsError(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	cfgPath := writeFile(t, dir, ".dupehound.yml", "scan: [invalid: yaml: {")

	cmd := exec.Command(bin, "scan", "--config", cfgPath, "--path", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected error for invalid YAML config, got output: %s", string(out))
	}
}

func TestConfigFile_FormatFromConfig(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	writeFile(t, dir, ".dupehound.yml", "scan:\n  path: "+dir+"\n  min-tokens: 10\n  format: json\n  exit-zero: true\n")

	cmd := exec.Command(bin, "scan", "--config", filepath.Join(dir, ".dupehound.yml"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("scan with format=json from config failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), `"clones"`) {
		t.Errorf("expected JSON output from config format setting, got: %s", string(out))
	}
}

func TestConfigFile_OutputFileFromConfig(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	outFile := filepath.Join(dir, "report.txt")
	writeFile(t, dir, ".dupehound.yml", "scan:\n  path: "+dir+"\n  min-tokens: 10\n  output: "+outFile+"\n  exit-zero: true\n")

	cmd := exec.Command(bin, "scan", "--config", filepath.Join(dir, ".dupehound.yml"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("scan with output from config failed: %v\n%s", err, string(out))
	}
	data, readErr := os.ReadFile(outFile)
	if readErr != nil {
		t.Fatalf("expected output file %q to be created: %v\ncmd output: %s", outFile, readErr, string(out))
	}
	if len(data) == 0 {
		t.Error("output file should not be empty")
	}
}

func TestConfigFile_LanguageFromConfig(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	// Go files with duplicates.
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)
	// Python files with the same content; filtered out by language=go.
	writeFile(t, dir, "a.py", block)
	writeFile(t, dir, "b.py", block)

	// Config sets language=go; only Go files should be scanned, clones still found.
	writeFile(t, dir, ".dupehound.yml", "scan:\n  path: "+dir+"\n  min-tokens: 10\n  language: go\n  exit-zero: true\n")

	cmd := exec.Command(bin, "scan", "--config", filepath.Join(dir, ".dupehound.yml"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("scan with language=go from config failed: %v\n%s", err, string(out))
	}
	if strings.Contains(string(out), "No duplicates found") {
		t.Errorf("expected Go clones found with language=go config, got: %s", string(out))
	}
}

// --- Clone type labels and similarity ---

func TestCloneType_TextOutput_ContainsTypeLabel(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	out, err := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "10", "--exit-zero").CombinedOutput()
	if err != nil {
		t.Fatalf("scan failed: %v\n%s", err, string(out))
	}
	output := string(out)
	if !strings.Contains(output, "type-1") && !strings.Contains(output, "type-2") {
		t.Errorf("text output should contain clone type label, got: %s", output)
	}
	if !strings.Contains(output, "similarity:") {
		t.Errorf("text output should contain similarity score, got: %s", output)
	}
}

func TestCloneType_TextOutput_Type1ForIdenticalCode(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	// Identical code → type-1
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	out, err := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "10", "--exit-zero").CombinedOutput()
	if err != nil {
		t.Fatalf("scan failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "type-1") {
		t.Errorf("identical code should be type-1, got: %s", string(out))
	}
}

func TestCloneType_TextOutput_Type2ForRenamedVariable(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	// Same structure, renamed variable → type-2
	writeFile(t, dir, "a.go", "package main\n\nfunc process() {\n\tresult := compute()\n\tvalidate(result)\n\tstore(result)\n\tnotify(result)\n\treturn result\n}\n")
	writeFile(t, dir, "b.go", "package main\n\nfunc process() {\n\toutput := compute()\n\tvalidate(output)\n\tstore(output)\n\tnotify(output)\n\treturn output\n}\n")

	out, err := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "10", "--exit-zero").CombinedOutput()
	if err != nil {
		t.Fatalf("scan failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "type-2") {
		t.Errorf("renamed variable should produce type-2, got: %s", string(out))
	}
}

func TestCloneType_JSONOutput_HasTypeAndSimilarity(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	cmd := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "10", "--exit-zero", "--format", "json")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	var report domain.Report
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, string(out))
	}
	if len(report.Clones) == 0 {
		t.Fatal("expected clones in JSON output")
	}
	for _, c := range report.Clones {
		if c.Type == "" {
			t.Error("JSON clone missing type field")
		}
		if c.Similarity <= 0 {
			t.Errorf("JSON clone has non-positive similarity: %f", c.Similarity)
		}
	}
}

func TestCloneType_SARIFOutput_HasProperties(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	writeFile(t, dir, "a.go", "package main\n\n"+block)
	writeFile(t, dir, "b.go", "package main\n\n"+block)

	cmd := exec.Command(bin, "scan", "--path", dir, "--min-tokens", "10", "--exit-zero", "--format", "sarif")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	// Parse the SARIF output and check properties.
	var sarif struct {
		Runs []struct {
			Results []struct {
				Properties struct {
					CloneType  string  `json:"cloneType"`
					Similarity float64 `json:"similarity"`
				} `json:"properties"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(out, &sarif); err != nil {
		t.Fatalf("invalid SARIF JSON: %v\n%s", err, string(out))
	}
	if len(sarif.Runs) == 0 || len(sarif.Runs[0].Results) == 0 {
		t.Fatal("expected SARIF results")
	}
	result := sarif.Runs[0].Results[0]
	if result.Properties.CloneType == "" {
		t.Error("SARIF result missing cloneType property")
	}
	if result.Properties.Similarity <= 0 {
		t.Errorf("SARIF result has non-positive similarity: %f", result.Properties.Similarity)
	}
}
