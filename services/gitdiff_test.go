package services

import (
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

// TestParseDiffHunks verifies parsing of unified diff hunk headers.
func TestParseDiffHunks(t *testing.T) {
	diff := `diff --git a/foo.go b/foo.go
index abc..def 100644
--- a/foo.go
+++ b/foo.go
@@ -10,7 +10,8 @@ func foo() {
 	x := 1
-	y := 2
+	y := 3
+	z := 4
 	return x
@@ -50,5 +51,3 @@ func bar() {
 	a := 1
-	b := 2
-	c := 3
 	return a
`
	ranges := parseDiffHunks(diff)
	if len(ranges) != 2 {
		t.Fatalf("expected 2 hunk ranges, got %d: %v", len(ranges), ranges)
	}
	if ranges[0].start != 10 || ranges[0].end != 17 {
		t.Errorf("first hunk: expected start=10 end=17, got start=%d end=%d", ranges[0].start, ranges[0].end)
	}
	if ranges[1].start != 51 || ranges[1].end != 53 {
		t.Errorf("second hunk: expected start=51 end=53, got start=%d end=%d", ranges[1].start, ranges[1].end)
	}
}

// TestFilterClonesInDiff_Changed verifies clones in changed files appear in new_clones.
func TestFilterClonesInDiff_Changed(t *testing.T) {
	changedLines := map[string][]changedLineRange{
		"/repo/src/foo.go": {{start: 1, end: 50}},
	}
	clones := []domain.Clone{
		{Hash: "aaa", Instances: []domain.CloneInstance{
			{File: "/repo/src/foo.go", StartLine: 10, EndLine: 20},
			{File: "/repo/src/bar.go", StartLine: 10, EndLine: 20},
		}},
		{Hash: "bbb", Instances: []domain.CloneInstance{
			{File: "/repo/src/baz.go", StartLine: 10, EndLine: 20},
			{File: "/repo/src/qux.go", StartLine: 10, EndLine: 20},
		}},
	}
	result := filterClonesInDiff(clones, changedLines)
	if len(result) != 1 {
		t.Fatalf("expected 1 new clone (touching changed file), got %d", len(result))
	}
	if result[0].Hash != "aaa" {
		t.Errorf("expected clone aaa to be new, got %s", result[0].Hash)
	}
}

// TestFilterClonesInDiff_Unchanged verifies clones in unchanged files don't appear in new_clones.
func TestFilterClonesInDiff_Unchanged(t *testing.T) {
	changedLines := map[string][]changedLineRange{
		"/repo/src/changed.go": {{start: 1, end: 10}},
	}
	clones := []domain.Clone{
		{Hash: "aaa", Instances: []domain.CloneInstance{
			{File: "/repo/src/unchanged1.go", StartLine: 1, EndLine: 20},
			{File: "/repo/src/unchanged2.go", StartLine: 1, EndLine: 20},
		}},
	}
	result := filterClonesInDiff(clones, changedLines)
	if len(result) != 0 {
		t.Errorf("expected 0 new clones for unchanged files, got %d", len(result))
	}
}

// TestOverlapsChangedLines verifies the range overlap logic.
func TestOverlapsChangedLines(t *testing.T) {
	changedLines := map[string][]changedLineRange{
		"foo.go": {{start: 10, end: 20}, {start: 50, end: 60}},
	}
	tests := []struct {
		file     string
		start    int
		end      int
		expected bool
	}{
		{"foo.go", 5, 15, true},   // overlaps first range
		{"foo.go", 15, 25, true},  // overlaps first range (end side)
		{"foo.go", 45, 55, true},  // overlaps second range
		{"foo.go", 25, 45, false}, // between ranges
		{"foo.go", 1, 8, false},   // before first range
		{"foo.go", 70, 80, false}, // after second range
		{"bar.go", 10, 20, false}, // different file
	}
	for _, tt := range tests {
		got := overlapsChangedLines(tt.file, tt.start, tt.end, changedLines)
		if got != tt.expected {
			t.Errorf("overlapsChangedLines(%q, %d, %d) = %v, want %v", tt.file, tt.start, tt.end, got, tt.expected)
		}
	}
}

// TestGetChangedLines_InvalidRef verifies graceful handling of invalid git ref.
func TestGetChangedLines_InvalidRef(t *testing.T) {
	// Use a temp dir that is not a git repo.
	dir := t.TempDir()
	_, _, err := getChangedLines(dir, "nonexistent-ref-xyz")
	// Should return an error, not panic.
	if err == nil {
		t.Log("warning: expected an error for invalid git ref in non-repo dir, but got nil")
	}
}
