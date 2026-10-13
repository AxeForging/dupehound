package integration

import (
	"os/exec"
	"strings"
	"testing"
)

// End to end through the built binary: an impossible budget is a clear,
// non-zero exit naming the flag — never a crash — and a sane one scans normally.
func TestMaxMemoryFlag(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n\n"+dupBlock)
	writeFile(t, dir, "b.go", "package main\n\n"+dupBlock)

	code, out := exitCode(t, exec.Command(bin, "scan", "--path", dir, "--min-tokens", "15", "--max-memory", "64"))
	if code == 0 || !strings.Contains(out, "--max-memory") {
		t.Errorf("tiny budget: exit=%d, want non-zero with a message naming --max-memory\n%s", code, out)
	}
	if strings.Contains(out, "panic") || strings.Contains(out, "goroutine ") {
		t.Errorf("tiny budget must fail cleanly, got a crash:\n%s", out)
	}

	code, out = exitCode(t, exec.Command(bin, "scan", "--path", dir, "--min-tokens", "15", "--max-memory", "1G", "--exit-zero"))
	if code != 0 || !strings.Contains(out, "Clones found") && !strings.Contains(out, "clone") {
		t.Errorf("1G budget: exit=%d, want a normal scan\n%s", code, out)
	}

	code, out = exitCode(t, exec.Command(bin, "scan", "--path", dir, "--max-memory", "lots"))
	if code == 0 || !strings.Contains(out, "max-memory") {
		t.Errorf("invalid size: exit=%d, want a usage error\n%s", code, out)
	}
}
