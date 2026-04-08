package services

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AxeForging/dupehound/domain"
	"github.com/AxeForging/dupehound/helpers"
)

// annotateGitChurn annotates clones with git commit counts for their files.
// churnDays specifies the rolling window (e.g. 90 days).
func annotateGitChurn(clones []domain.Clone, repoRoot string, churnDays int) {
	// Collect unique files from all instances.
	fileSet := make(map[string]bool)
	for _, c := range clones {
		for _, inst := range c.Instances {
			fileSet[inst.File] = true
		}
	}

	// Fetch commit counts for each file.
	commitCounts := make(map[string]int, len(fileSet))
	for file := range fileSet {
		count, err := getFileCommitCount(repoRoot, file, churnDays)
		if err != nil {
			helpers.Log.Debug().Str("file", file).Err(err).Msg("could not get git churn for file")
			count = 0
		}
		commitCounts[file] = count
	}

	// Annotate clones.
	for i := range clones {
		churnScore := 0
		for j := range clones[i].Instances {
			file := clones[i].Instances[j].File
			count := commitCounts[file]
			clones[i].Instances[j].FileCommits = count
			churnScore += count
		}
		clones[i].ChurnScore = churnScore
	}
}

// getFileCommitCount returns the number of commits touching file in the last churnDays days.
func getFileCommitCount(repoRoot, file string, churnDays int) (int, error) {
	// Use relative path for git log.
	relFile, err := filepath.Rel(repoRoot, file)
	if err != nil {
		relFile = file
	}

	since := fmt.Sprintf("%d days ago", churnDays)
	cmd := exec.Command("git", "log", "--oneline", fmt.Sprintf("--since=%s", since), "--", relFile)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("git log: %w", err)
	}

	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count, nil
}

// sortByChurn re-sorts clones by ChurnScore descending (highest churn first).
func sortByChurn(clones []domain.Clone) {
	sort.SliceStable(clones, func(i, j int) bool {
		if clones[i].ChurnScore != clones[j].ChurnScore {
			return clones[i].ChurnScore > clones[j].ChurnScore
		}
		return clones[i].LineCount > clones[j].LineCount
	})
}
