package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const dupBlock = `func helper() int {
	x := compute()
	validate(x)
	transform(x)
	persist(x)
	notify(x)
	return x
}
`

// dupBlock2 must be structurally DIFFERENT from dupBlock (not just renamed
// identifiers), otherwise type-2 detection merges all four files into one
// clone group — whose grown instance count correctly trips the ratchet.
const dupBlock2 = `func other(s string) string {
	if s == "" {
		return fallback()
	}
	for i := 0; i < 3; i++ {
		s = expand(s, i)
	}
	return s + suffix()
}
`

// exitCode runs the command and returns its exit code and combined output.
func exitCode(t *testing.T, cmd *exec.Cmd) (int, string) {
	t.Helper()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	}
	t.Fatalf("command failed to run: %v\n%s", err, string(out))
	return -1, ""
}

// TestBaselineRatchetFlow is the end-to-end hook workflow:
// record debt → clean scan passes → new duplication fails.
func TestBaselineRatchetFlow(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n\n"+dupBlock)
	writeFile(t, dir, "b.go", "package main\n\n"+dupBlock)
	baseline := filepath.Join(t.TempDir(), "baseline.json")

	// 1. Write the baseline: must exit 0 even though clones exist.
	code, out := exitCode(t, exec.Command(bin, "scan", "--path", dir, "--min-tokens", "15", "--write-baseline", baseline))
	if code != 0 {
		t.Fatalf("--write-baseline must exit 0, got %d\n%s", code, out)
	}
	if _, err := os.Stat(baseline); err != nil {
		t.Fatalf("baseline file not written: %v", err)
	}

	// 2. Compare against it: all clones are known debt → exit 0.
	code, out = exitCode(t, exec.Command(bin, "scan", "--path", dir, "--min-tokens", "15", "--baseline", baseline))
	if code != 0 {
		t.Fatalf("clean baseline compare must exit 0, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "0 NEW") {
		t.Errorf("summary should report 0 NEW, got:\n%s", out)
	}

	// 3. Introduce NEW duplication → exit 1, and only the new clone is listed.
	writeFile(t, dir, "c.go", "package main\n\n"+dupBlock2)
	writeFile(t, dir, "d.go", "package main\n\n"+dupBlock2)
	code, out = exitCode(t, exec.Command(bin, "scan", "--path", dir, "--min-tokens", "15", "--baseline", baseline))
	if code != 1 {
		t.Fatalf("new duplication must exit 1, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "1 NEW") {
		t.Errorf("summary should report 1 NEW, got:\n%s", out)
	}
	// Clone instance lines look like "a.go:4-9"; the known a.go/b.go clone
	// must be hidden, the new c.go/d.go one shown.
	if !strings.Contains(out, "c.go:") || strings.Contains(out, "a.go:") {
		t.Errorf("clone list should show only the NEW clone (c.go/d.go), got:\n%s", out)
	}
}

// A pure line shift must NOT trip the ratchet — this is what makes the
// baseline usable in day-to-day development.
func TestBaselineSurvivesLineShift(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n\n"+dupBlock)
	writeFile(t, dir, "b.go", "package main\n\n"+dupBlock)
	baseline := filepath.Join(t.TempDir(), "baseline.json")

	if code, out := exitCode(t, exec.Command(bin, "scan", "--path", dir, "--min-tokens", "15", "--write-baseline", baseline)); code != 0 {
		t.Fatalf("write-baseline exit %d\n%s", code, out)
	}

	// Shift the clone in a.go down by 30 lines of comments.
	writeFile(t, dir, "a.go", "package main\n\n"+strings.Repeat("// filler\n", 30)+dupBlock)
	code, out := exitCode(t, exec.Command(bin, "scan", "--path", dir, "--min-tokens", "15", "--baseline", baseline))
	if code != 0 {
		t.Errorf("line shift must not count as new duplication, exit %d\n%s", code, out)
	}
}

func TestBaselineMissingFileIsHardError(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n\n"+dupBlock)
	writeFile(t, dir, "b.go", "package main\n\n"+dupBlock)

	code, _ := exitCode(t, exec.Command(bin, "scan", "--path", dir, "--min-tokens", "15", "--baseline", filepath.Join(dir, "missing.json")))
	if code != 2 {
		t.Errorf("missing baseline must be a tool error (exit 2), got %d", code)
	}
}

func TestFormatGitHubCLI(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n\n"+dupBlock)
	writeFile(t, dir, "b.go", "package main\n\n"+dupBlock)

	code, out := exitCode(t, exec.Command(bin, "scan", "--path", dir, "--min-tokens", "15", "--format", "github", "--quiet"))
	if code != 1 {
		t.Fatalf("clones found must still exit 1 with github format, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "::warning file=a.go,line=") {
		t.Errorf("expected a workflow-command annotation, got:\n%s", out)
	}
	if !strings.Contains(out, "::notice title=dupehound summary::") {
		t.Errorf("expected a summary notice, got:\n%s", out)
	}
	if !strings.Contains(out, "in helper") {
		t.Errorf("annotation should carry function attribution, got:\n%s", out)
	}
}

func TestTextOutputCarriesFunctionNames(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n\n"+dupBlock)
	writeFile(t, dir, "b.go", "package main\n\n"+strings.ReplaceAll(dupBlock, "helper", "worker"))

	_, out := exitCode(t, exec.Command(bin, "scan", "--path", dir, "--min-tokens", "15", "--exit-zero"))
	if !strings.Contains(out, "(in helper)") || !strings.Contains(out, "(in worker)") {
		t.Errorf("text output should attribute instances to helper/worker, got:\n%s", out)
	}
	if !strings.Contains(out, "saves ~") {
		t.Errorf("text output should show refactor savings, got:\n%s", out)
	}
}

func TestMaxFileSizeFlag(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n\n"+dupBlock)
	writeFile(t, dir, "b.go", "package main\n\n"+dupBlock)
	writeFile(t, dir, "big.go", "package main\n\nfunc big() {\n"+strings.Repeat("\tprintln(1)\n", 500)+"}\n")

	_, out := exitCode(t, exec.Command(bin, "scan", "--path", dir, "--min-tokens", "15", "--max-file-size", "1024", "--exit-zero"))
	if !strings.Contains(out, "over --max-file-size") {
		t.Errorf("summary should report the oversized skip, got:\n%s", out)
	}
	// The skip WARNING mentions big.go on stderr (that's desirable); the
	// results themselves (hotspots "big.go" entries, instances "big.go:1-2")
	// must not.
	if strings.Contains(out, "big.go:") || strings.Contains(out, "lines  big.go") {
		t.Errorf("oversized file must not appear in results, got:\n%s", out)
	}
}
