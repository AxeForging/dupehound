package services

import (
	"strings"
	"testing"
)

// At 50 tokens a clone can be three dense lines — two calls with long argument
// lists, a pair of sort comparators. Measured on seven repositories, clones
// under five lines were 0.1–8% of findings and read as noise. A floor on the
// number of lines removes them without touching the token threshold.
func TestScan_MinCloneLinesDropsShortClones(t *testing.T) {
	dense := "\tcreate(\"a\", 1981, 2, 16, zone, true, nil, 0, \"x\", 1.5, opts)\n" +
		"\tcreate(\"b\", 2012, 2, 29, zone, true, nil, 0, \"y\", 2.5, opts)\n" +
		"\tcreate(\"c\", 2012, 1, 31, zone, true, nil, 0, \"z\", 3.5, opts)\n"
	long := "\ttotal := 0\n\tfor _, x := range xs {\n\t\tif x > limit {\n\t\t\ttotal += x * 2\n\t\t} else {\n\t\t\ttotal++\n\t\t}\n\t}\n\treturn total\n"
	file := func(pkg string) string {
		return "package " + pkg + "\n\nfunc seed(zone int, opts int) {\n" + dense + "}\n\nfunc sum(xs []int, limit int) int {\n" + long + "}\n"
	}
	dir := t.TempDir()
	writeFile(t, dir, "a.go", file("a"))
	writeFile(t, dir, "b.go", file("b"))

	lineCounts := func(minCloneLines int) []int {
		t.Helper()
		report, err := NewScannerService().Scan(ScanOptions{Path: dir, MinTokens: 25, MinSimilarity: 1.0, MinCloneLines: minCloneLines})
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		var got []int
		for _, c := range report.Clones {
			got = append(got, c.LineCount)
		}
		return got
	}

	all := lineCounts(0)
	if len(all) != 2 {
		t.Fatalf("fixture must hold a 3-line and a 9-line clone with no floor, got line counts %v", all)
	}
	floored := lineCounts(5)
	if len(floored) != 1 || floored[0] < 5 {
		t.Errorf("with a 5-line floor got clones of %v lines, want only the long one", floored)
	}
	if got := lineCounts(3); len(got) != 2 {
		t.Errorf("a floor equal to the short clone's length must keep it, got %v", got)
	}
}

// The summary must describe what is reported: dropped clones count toward
// neither the clone total nor the duplicated lines.
func TestScan_MinCloneLinesIsReflectedInTotals(t *testing.T) {
	dense := strings.Repeat("\tcreate(\"a\", 1981, 2, 16, zone, true, nil, 0, \"x\", 1.5, opts)\n", 3)
	src := func(pkg string) string {
		return "package " + pkg + "\n\nfunc seed(zone int, opts int) {\n" + dense + "}\n"
	}
	dir := t.TempDir()
	writeFile(t, dir, "a.go", src("a"))
	writeFile(t, dir, "b.go", src("b"))

	report, err := NewScannerService().Scan(ScanOptions{Path: dir, MinTokens: 25, MinSimilarity: 1.0, MinCloneLines: 5})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if report.TotalClones != 0 || report.DuplicateLines != 0 || report.DuplicationPct != 0 {
		t.Errorf("totals still count dropped clones: clones=%d lines=%d pct=%v", report.TotalClones, report.DuplicateLines, report.DuplicationPct)
	}
}
