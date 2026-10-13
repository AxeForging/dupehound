package integration

import (
	"os/exec"
	"strings"
	"testing"
)

// End to end through the built binary: a three-line clone is hidden by the
// default floor and shown when the floor is lifted, by flag or by config.
func TestMinCloneLinesFlag(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	dense := strings.Repeat("\tcreate(\"a\", 1981, 2, 16, zone, true, nil, 0, \"x\", 1.5, opts)\n", 3)
	// Different signatures, so the clone is the three body lines and no more.
	writeFile(t, dir, "a.go", "package a\n\nfunc seed(zone int, opts int) {\n"+dense+"}\n")
	writeFile(t, dir, "b.go", "package b\n\nfunc plant(opts string, zone bool, extra float64) error {\n"+dense+"\treturn nil\n}\n")

	run := func(args ...string) (int, string) {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"scan", "--path", dir, "--min-tokens", "25", "--quiet"}, args...)...)
		cmd.Dir = dir
		return exitCode(t, cmd)
	}

	if code, out := run(); code != 0 {
		t.Errorf("default floor: exit=%d, want 0 (a 3-line clone is below it)\n%s", code, out)
	}
	if code, out := run("--min-clone-lines", "0"); code != 1 || !strings.Contains(out, "a.go") {
		t.Errorf("--min-clone-lines 0: exit=%d, want 1 with the clone reported\n%s", code, out)
	}
	if code, out := run("--min-clone-lines", "-1"); code == 0 || !strings.Contains(out, "min-clone-lines") {
		t.Errorf("negative floor: exit=%d, want a usage error\n%s", code, out)
	}

	writeFile(t, dir, ".dupehound.yml", "scan:\n  min-clone-lines: 0\n")
	if code, out := run(); code != 1 {
		t.Errorf("min-clone-lines: 0 in config: exit=%d, want 1\n%s", code, out)
	}
	if code, out := run("--min-clone-lines", "5"); code != 0 {
		t.Errorf("flag over config: exit=%d, want 0\n%s", code, out)
	}
}
