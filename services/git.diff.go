package services

import (
	"bufio"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/AxeForging/dupehound/domain"
	"github.com/AxeForging/dupehound/helpers"
)

// shaPattern matches a full 40-character git SHA-1 or shorter abbreviated SHA (≥4 chars).
var shaPattern = regexp.MustCompile(`^[0-9a-f]{4,40}$`)

// resolveGitRef resolves a human-readable ref (branch name, tag, HEAD~1, etc.)
// to its full 40-character SHA using `git rev-parse --verify`. The returned SHA
// is always safe to embed in subsequent git commands — it contains only lowercase
// hex characters and can never be misinterpreted as a flag or option.
func resolveGitRef(repoRoot, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("git ref must not be empty")
	}
	// Reject anything that starts with '-' before even calling git, since
	// exec.Command passes arguments directly to the OS and git would interpret
	// a leading '-' as a flag (e.g. --upload-pack=, --output=).
	if strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("invalid git ref %q: must not start with '-'", ref)
	}

	cmd := exec.Command("git", "rev-parse", "--verify", ref) //nolint:gosec // ref validated above
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --verify %q: %w", ref, err)
	}

	sha := strings.TrimSpace(string(out))
	if !shaPattern.MatchString(sha) {
		// Should never happen with a healthy git installation, but guard anyway.
		return "", fmt.Errorf("git rev-parse returned unexpected output %q for ref %q", sha, ref)
	}
	return sha, nil
}

// changedLineRange holds changed line ranges for a file.
type changedLineRange struct {
	start, end int
}

// getChangedLines runs git diff to get changed files and line ranges since a ref.
// The ref is first resolved to a SHA so that all downstream git calls use only
// a fixed hex string — never user-controlled text that could be misread as a flag.
// Returns map[absFilePath][]changedLineRange, total changed file count, error.
func getChangedLines(repoRoot, ref string) (map[string][]changedLineRange, int, error) {
	sha, err := resolveGitRef(repoRoot, ref)
	if err != nil {
		return nil, 0, err
	}

	// All git calls below use sha (hex only) — no user input reaches the command line.
	cmd := exec.Command("git", "diff", "--name-only", sha+"...HEAD")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, 0, fmt.Errorf("git diff --name-only: %w", err)
	}

	var changedFiles []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			changedFiles = append(changedFiles, line)
		}
	}

	result := make(map[string][]changedLineRange)
	for _, relFile := range changedFiles {
		absFile := filepath.Join(repoRoot, relFile)
		ranges, err := getChangedLinesForFile(repoRoot, sha, relFile)
		if err != nil {
			helpers.Log.Debug().Str("file", relFile).Err(err).Msg("could not get changed lines for file")
			// Include entire file as changed on error.
			result[absFile] = []changedLineRange{{1, 1<<31 - 1}}
			continue
		}
		result[absFile] = ranges
	}

	return result, len(changedFiles), nil
}

// getChangedLinesForFile parses unified diff output to extract changed line ranges.
// sha must be a hex SHA (output of resolveGitRef) — never raw user input.
func getChangedLinesForFile(repoRoot, sha, relFile string) ([]changedLineRange, error) {
	cmd := exec.Command("git", "diff", sha+"...HEAD", "--", relFile)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff for file: %w", err)
	}

	return parseDiffHunks(string(out)), nil
}

// parseDiffHunks parses unified diff output and returns added/changed line ranges.
func parseDiffHunks(diff string) []changedLineRange {
	var ranges []changedLineRange
	scanner := bufio.NewScanner(strings.NewReader(diff))
	for scanner.Scan() {
		line := scanner.Text()
		// Match hunk header: @@ -oldStart,oldCount +newStart,newCount @@
		if !strings.HasPrefix(line, "@@") {
			continue
		}
		// Parse the +start,count part.
		plusIdx := strings.Index(line, " +")
		if plusIdx < 0 {
			continue
		}
		rest := line[plusIdx+2:]
		spaceIdx := strings.Index(rest, " ")
		if spaceIdx >= 0 {
			rest = rest[:spaceIdx]
		}
		parts := strings.Split(rest, ",")
		start, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		count := 1
		if len(parts) > 1 {
			count, _ = strconv.Atoi(parts[1])
		}
		if count <= 0 {
			continue
		}
		ranges = append(ranges, changedLineRange{start, start + count - 1})
	}
	return ranges
}

// filterClonesInDiff returns clones where at least one instance overlaps with changed lines.
func filterClonesInDiff(clones []domain.Clone, changedLines map[string][]changedLineRange) []domain.Clone {
	var filtered []domain.Clone
	for _, c := range clones {
		for _, inst := range c.Instances {
			if overlapsChangedLines(inst.File, inst.StartLine, inst.EndLine, changedLines) {
				filtered = append(filtered, c)
				break
			}
		}
	}
	return filtered
}

// overlapsChangedLines checks if a file:line range overlaps any changed lines.
func overlapsChangedLines(file string, start, end int, changedLines map[string][]changedLineRange) bool {
	ranges, ok := changedLines[file]
	if !ok {
		return false
	}
	for _, r := range ranges {
		if rangesOverlap(start, end, r.start, r.end) {
			return true
		}
	}
	return false
}
