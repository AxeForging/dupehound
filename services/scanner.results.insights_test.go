package services

import (
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

func insightClone(lineCount int, files ...string) domain.Clone {
	c := domain.Clone{Type: domain.CloneType2, LineCount: lineCount}
	for _, f := range files {
		c.Instances = append(c.Instances, domain.CloneInstance{File: f, StartLine: 1, EndLine: lineCount})
	}
	return c
}

func TestAnnotateInsights_Scope(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{"same file twice", []string{"/repo/pkg/a.go", "/repo/pkg/a.go"}, domain.ScopeSameFile},
		{"two files one dir", []string{"/repo/pkg/a.go", "/repo/pkg/b.go"}, domain.ScopeCrossFile},
		{"two dirs", []string{"/repo/pkg/a.go", "/repo/other/b.go"}, domain.ScopeCrossDir},
		{"mixed three instances", []string{"/repo/pkg/a.go", "/repo/pkg/a.go", "/repo/other/c.go"}, domain.ScopeCrossDir},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clones := []domain.Clone{insightClone(10, tc.files...)}
			annotateInsights(clones)
			if clones[0].Scope != tc.want {
				t.Errorf("scope: want %s, got %s", tc.want, clones[0].Scope)
			}
		})
	}
}

func TestAnnotateInsights_SavedLines(t *testing.T) {
	clones := []domain.Clone{
		insightClone(10, "/a.go", "/b.go"),         // 2 instances → 10 saveable
		insightClone(7, "/a.go", "/b.go", "/c.go"), // 3 instances → 14 saveable
	}
	annotateInsights(clones)
	if clones[0].SavedLines != 10 {
		t.Errorf("2-instance clone: want 10 saved lines, got %d", clones[0].SavedLines)
	}
	if clones[1].SavedLines != 14 {
		t.Errorf("3-instance clone: want 14 saved lines, got %d", clones[1].SavedLines)
	}
	if got := totalSavedLines(clones); got != 24 {
		t.Errorf("total: want 24, got %d", got)
	}
}

func TestTotalSavedLines_SkipsSuppressed(t *testing.T) {
	clones := []domain.Clone{
		insightClone(10, "/a.go", "/b.go"),
		insightClone(10, "/a.go", "/b.go"),
	}
	annotateInsights(clones)
	clones[1].Suppressed = true
	if got := totalSavedLines(clones); got != 10 {
		t.Errorf("suppressed clone must not count: want 10, got %d", got)
	}
}

func TestFilterBaselineKnown(t *testing.T) {
	clones := []domain.Clone{
		{Hash: "new1"},
		{Hash: "known1", Baseline: true},
		{Hash: "new2"},
	}
	hidden := filterBaselineKnown(clones, false)
	if len(hidden) != 2 || hidden[0].Hash != "new1" || hidden[1].Hash != "new2" {
		t.Errorf("default filtering should keep only new clones, got %v", hidden)
	}
	shown := filterBaselineKnown(clones, true)
	if len(shown) != 3 {
		t.Errorf("showAll should keep all clones, got %d", len(shown))
	}
}
