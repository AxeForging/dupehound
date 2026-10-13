package integration

import (
	"os/exec"
	"strings"
	"testing"
)

// smallMethods renders a PHP class made only of short accessors: no single
// method is long enough to be a clone, but the class as a whole is copied.
func smallMethods(class string) string {
	var b strings.Builder
	b.WriteString("<?php\n\nclass " + class + "\n{\n")
	for _, f := range []string{"items", "perPage", "path", "query", "fragment", "pageName", "options", "total"} {
		b.WriteString("    public function " + f + "()\n    {\n        return $this->" + f + ";\n    }\n\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// End to end through the built binary: a class copied wholesale is found by
// default, and --scope function restores detection inside single functions.
func TestScopeFlag(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "Paginator.php", smallMethods("Paginator"))
	writeFile(t, dir, "CursorPaginator.php", smallMethods("CursorPaginator"))

	code, out := exitCode(t, exec.Command(bin, "scan", "--path", dir, "--quiet"))
	if code != 1 || !strings.Contains(out, "Paginator.php") {
		t.Errorf("default scope: exit=%d, want 1 with the copied class reported\n%s", code, out)
	}

	code, out = exitCode(t, exec.Command(bin, "scan", "--path", dir, "--quiet", "--scope", "function"))
	if code != 0 {
		t.Errorf("--scope function: exit=%d, want 0 (no single method is a clone)\n%s", code, out)
	}

	code, out = exitCode(t, exec.Command(bin, "scan", "--path", dir, "--scope", "everything"))
	if code == 0 || !strings.Contains(out, "scope") {
		t.Errorf("invalid scope: exit=%d, want a usage error naming the flag\n%s", code, out)
	}

	// The config file can set it too; the flag still wins.
	writeFile(t, dir, ".dupehound.yml", "scan:\n  scope: function\n")
	cmd := exec.Command(bin, "scan", "--path", dir, "--quiet")
	cmd.Dir = dir
	if code, out := exitCode(t, cmd); code != 0 {
		t.Errorf("scope: function in config: exit=%d, want 0\n%s", code, out)
	}
	cmd = exec.Command(bin, "scan", "--path", dir, "--quiet", "--scope", "file")
	cmd.Dir = dir
	if code, out := exitCode(t, cmd); code != 1 {
		t.Errorf("--scope file over config: exit=%d, want 1\n%s", code, out)
	}
}
