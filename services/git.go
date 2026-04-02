package services

import (
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/AxeForging/dupehound/helpers"
)

// StagedFiles returns the absolute paths of files staged for commit (added, copied, modified).
// Returns an empty slice (no error) if not inside a git repository.
// Only files with supported language extensions are returned.
func StagedFiles(repoRoot string) ([]string, error) {
	cmd := exec.Command("git", "diff", "--cached", "--name-only", "--diff-filter=ACM")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		// Not a git repo or git not installed — not an error, just no staged files.
		helpers.Log.Warn().Msg("not inside a git repository or git not available, --staged ignored")
		return nil, nil
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		absPath := filepath.Join(repoRoot, line)
		if DetectLanguage(absPath) != nil {
			files = append(files, absPath)
		}
	}
	return files, nil
}
