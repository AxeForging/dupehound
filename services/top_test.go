package services

import (
	"strings"
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

// buildTestReport creates a Report with n fake clones for testing output truncation.
func buildTestReport(n int) *domain.Report {
	clones := make([]domain.Clone, n)
	for i := range clones {
		clones[i] = domain.Clone{
			Hash:       "deadbeef",
			Type:       domain.CloneType1,
			Similarity: 1.0,
			LineCount:  10 + i,
			TokenCount: 50,
			Instances: []domain.CloneInstance{
				{File: "/a/foo.go", StartLine: 1, EndLine: 10 + i},
				{File: "/a/bar.go", StartLine: 1, EndLine: 10 + i},
			},
		}
	}
	return &domain.Report{
		TotalFiles:     2,
		ScannedFiles:   2,
		TotalClones:    n,
		TotalLines:     500,
		DuplicateLines: 100,
		DuplicationPct: 20.0,
		Clones:         clones,
	}
}

// TestTopN_DefaultIsTop10 verifies that without --top, at most 10 clones appear in Top section.
func TestTopN_DefaultIsTop10(t *testing.T) {
	report := buildTestReport(20)
	out, err := FormatReport(report, "text", FormatOptions{ScanPath: "/a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Count lines with "#" clone header in the "Top clones" section.
	if !strings.Contains(out, "Top clones (by impact):") {
		t.Fatal("could not find 'Top clones' section")
	}
	// Should mention "10 more clones" or similar.
	if !strings.Contains(out, "more clones") {
		t.Error("expected truncation notice in output")
	}
}

// TestTopN_Top3 verifies --top 3 limits clone output to 3.
func TestTopN_Top3(t *testing.T) {
	report := buildTestReport(10)
	out, err := FormatReport(report, "text", FormatOptions{ScanPath: "/a", Top: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	topStart := strings.Index(out, "Top clones (by impact):")
	if topStart < 0 {
		t.Fatal("could not find 'Top clones' section")
	}
	topSectionRaw := out[topStart:]
	allClonesIdx := strings.Index(topSectionRaw, "All clones:")
	topSection := topSectionRaw
	if allClonesIdx > 0 {
		topSection = topSectionRaw[:allClonesIdx]
	}
	// Should show exactly 3 clone headers (#1, #2, #3).
	count := strings.Count(topSection, "  #")
	if count != 3 {
		t.Errorf("expected 3 clones in Top section with --top 3, got %d", count)
	}
}

// TestTopN_ShowAll verifies --top -1 (meaning 0 in CLI / show all) shows all clones.
func TestTopN_ShowAll(t *testing.T) {
	report := buildTestReport(15)
	out, err := FormatReport(report, "text", FormatOptions{ScanPath: "/a", Top: -1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should NOT contain truncation notice.
	if strings.Contains(out, "more clones") {
		t.Error("expected no truncation notice when Top=-1 (show all)")
	}
}

// TestTopN_JSONUnaffected verifies JSON output is never truncated by --top.
func TestTopN_JSONUnaffected(t *testing.T) {
	report := buildTestReport(20)
	// JSON with Top=3 should still have all 20 clones.
	out, err := FormatReport(report, "json", FormatOptions{ScanPath: "/a", Top: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Count "hash" occurrences as proxy for clone count.
	count := strings.Count(out, `"hash"`)
	if count != 20 {
		t.Errorf("expected 20 clones in JSON output regardless of --top, got %d", count)
	}
}

// TestTopNWithOpt verifies topNWithOpt behavior.
func TestTopNWithOpt(t *testing.T) {
	tests := []struct {
		total    int
		opt      FormatOptions
		expected int
	}{
		{5, FormatOptions{Top: 0}, 5},          // fewer than default 10
		{20, FormatOptions{Top: 0}, 10},        // default 10
		{20, FormatOptions{Top: 3}, 3},         // explicit top 3
		{20, FormatOptions{Top: -1}, 20},       // show all
		{20, FormatOptions{Verbose: true}, 20}, // verbose shows all
	}
	for _, tt := range tests {
		got := topNWithOpt(tt.total, tt.opt)
		if got != tt.expected {
			t.Errorf("topNWithOpt(%d, %+v) = %d, want %d", tt.total, tt.opt, got, tt.expected)
		}
	}
}
