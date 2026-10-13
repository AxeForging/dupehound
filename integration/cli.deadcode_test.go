package integration

import (
	"os/exec"
	"strings"
	"testing"
)

// --dead-code fails the build only for findings it is sure of. A public
// function nothing in the repository calls is reported, but it may be API for
// callers elsewhere, so on its own it must not turn a library's build red.
func TestDeadCodeExitCodeFollowsConfidence(t *testing.T) {
	bin := buildBinary(t)

	scan := func(t *testing.T, src string) (int, string) {
		t.Helper()
		dir := t.TempDir()
		writeFile(t, dir, "lib.go", src)
		return exitCode(t, exec.Command(bin, "scan", "--path", dir, "--dead-code", "--quiet"))
	}

	t.Run("unused public function is reported but does not fail", func(t *testing.T) {
		code, out := scan(t, "package lib\n\nfunc PublicAPI() int { return 1 }\n")
		if !strings.Contains(out, "PublicAPI") {
			t.Errorf("the finding must still be shown:\n%s", out)
		}
		if code != 0 {
			t.Errorf("exit = %d, want 0 for a low-confidence finding\n%s", code, out)
		}
	})

	t.Run("unused private function fails", func(t *testing.T) {
		code, out := scan(t, "package lib\n\nfunc PublicAPI() int { return 1 }\n\nfunc orphan() int { return 2 }\n\nvar _ = PublicAPI\n")
		if !strings.Contains(out, "orphan") {
			t.Errorf("the finding must be shown:\n%s", out)
		}
		if code != 1 {
			t.Errorf("exit = %d, want 1 for a high-confidence finding\n%s", code, out)
		}
	})

	t.Run("no findings passes", func(t *testing.T) {
		if code, out := scan(t, "package lib\n\nfunc used() int { return 1 }\n\nvar X = used()\n"); code != 0 {
			t.Errorf("exit = %d, want 0\n%s", code, out)
		}
	})
}
