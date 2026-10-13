package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")
	// Initial commit so HEAD exists.
	placeholder := filepath.Join(dir, ".gitkeep")
	if err := os.WriteFile(placeholder, []byte(""), 0o600); err != nil {
		t.Fatalf("write .gitkeep: %v", err)
	}
	run("add", ".")
	run("commit", "-m", "init")
}

func stageFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	cmd := exec.Command("git", "add", name)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add %s: %v\n%s", name, err, string(out))
	}
}

func TestStagedFiles_ReturnsStagedGoFiles(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	stageFile(t, dir, "a.go", "package main\nfunc main() {}\n")
	stageFile(t, dir, "b.txt", "not a source file\n")

	files, err := StagedFiles(dir)
	if err != nil {
		t.Fatalf("StagedFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 staged Go file, got %d: %v", len(files), files)
	}
	if filepath.Base(files[0]) != "a.go" {
		t.Errorf("expected a.go, got %s", files[0])
	}
}

func TestStagedFiles_NoStagedFiles_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	files, err := StagedFiles(dir)
	if err != nil {
		t.Fatalf("StagedFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 staged files, got %d", len(files))
	}
}

func TestStagedFiles_NotGitRepo_ReturnsNil(t *testing.T) {
	dir := t.TempDir()

	files, err := StagedFiles(dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if files != nil {
		t.Errorf("expected nil, got %v", files)
	}
}

func TestFilterStagedClones_KeepsOnlyStagedInstances(t *testing.T) {
	stagedSet := map[string]bool{"/repo/a.go": true}
	clones := []domain.Clone{
		{
			Hash: "1",
			Instances: []domain.CloneInstance{
				{File: "/repo/a.go", StartLine: 1, EndLine: 5},
				{File: "/repo/b.go", StartLine: 1, EndLine: 5},
			},
		},
		{
			Hash: "2",
			Instances: []domain.CloneInstance{
				{File: "/repo/c.go", StartLine: 1, EndLine: 5},
				{File: "/repo/d.go", StartLine: 1, EndLine: 5},
			},
		},
	}

	filtered := filterStagedClones(clones, stagedSet)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 clone (touching staged file), got %d", len(filtered))
	}
	if filtered[0].Hash != "1" {
		t.Errorf("expected clone '1', got %q", filtered[0].Hash)
	}
}
