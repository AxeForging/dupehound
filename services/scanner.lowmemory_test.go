package services

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/AxeForging/dupehound/domain"
	"github.com/AxeForging/dupehound/helpers"
)

// lowMemoryFixture writes a small tree with exact clones in and across files,
// a renamed copy, a clone ending in a multi-line string, and dead functions.
func lowMemoryFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for i := range 6 {
		var b strings.Builder
		fmt.Fprintf(&b, "package p%d\n\n", i)
		b.WriteString(goFunc(fmt.Sprintf("sum%d", i), 0))
		b.WriteString(goFunc(fmt.Sprintf("other%d", i), i+1))
		fmt.Fprintf(&b, "func query%d() string {\n\tname := \"q\"\n\tlimit := 10\n\toffset := 20\n\tprintln(name, limit, offset)\n\treturn `select\n  *\nfrom\n  t`\n}\n\n", i)
		fmt.Fprintf(&b, "func orphan%d() {}\n\nfunc Use%d() int { return sum%d(nil, 1) + other%d(nil, 2) + len(query%d()) }\n", i, i, i, i, i)
		// The file ends in a multi-line string, so the last clone's range has
		// to come from where that token ends, in either mode.
		b.WriteString("\nvar a, b, c, d, e, f, g, h = 1, 2, 3, 4, 5, 6, 7, `select\n  *\nfrom\n  t`\n")
		writeFile(t, dir, fmt.Sprintf("f%d.go", i), b.String())
	}
	return dir
}

// When a scan does not fit its budget with every token in memory, it falls
// back to a compact index and re-reads files on demand. The findings must be
// exactly those of an unconstrained scan.
func TestScan_LowMemoryModeFindsTheSameClonesAndDeadCode(t *testing.T) {
	dir := lowMemoryFixture(t)
	base := ScanOptions{Path: dir, MinTokens: 30, MinSimilarity: 1.0, WholeFile: true, DeadCode: true}

	full, err := NewScannerService().Scan(base)
	if err != nil {
		t.Fatalf("unconstrained scan: %v", err)
	}
	if len(full.Clones) < 2 || len(full.DeadFunctions) == 0 {
		t.Fatalf("fixture too thin: %d clones, %d dead functions", len(full.Clones), len(full.DeadFunctions))
	}

	// A budget the full representation cannot meet but the compact one can.
	fullNeed, compactNeed := scanMemoryNeeds(t, dir)
	if compactNeed >= fullNeed {
		t.Fatalf("compact mode must need less: full=%d compact=%d", fullNeed, compactNeed)
	}
	constrained := base
	constrained.MaxMemory = (fullNeed + compactNeed) / 2
	low, err := NewScannerService().Scan(constrained)
	if err != nil {
		t.Fatalf("low-memory scan: %v", err)
	}

	if !reflect.DeepEqual(cloneKeys(full.Clones), cloneKeys(low.Clones)) {
		t.Errorf("clones differ in low-memory mode\n full: %v\n low:  %v", cloneKeys(full.Clones), cloneKeys(low.Clones))
	}
	if !reflect.DeepEqual(full.DeadFunctions, low.DeadFunctions) {
		t.Errorf("dead functions differ in low-memory mode\n full: %v\n low:  %v", full.DeadFunctions, low.DeadFunctions)
	}
	if full.DuplicateLines != low.DuplicateLines || full.TotalLines != low.TotalLines {
		t.Errorf("totals differ: full %d/%d, low %d/%d", full.DuplicateLines, full.TotalLines, low.DuplicateLines, low.TotalLines)
	}
}

// Type-3 detection needs every token; in low-memory mode it is skipped and the
// report says so.
func TestScan_LowMemoryModeSkipsType3AndSaysSo(t *testing.T) {
	dir := lowMemoryFixture(t)
	fullNeed, compactNeed := scanMemoryNeeds(t, dir)
	report, err := NewScannerService().Scan(ScanOptions{Path: dir, MinTokens: 30, MinSimilarity: 0.7, MaxMemory: (fullNeed + compactNeed) / 2})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !report.Partial || !strings.Contains(report.PartialReason, "--max-memory") {
		t.Errorf("report must flag the skipped type-3 pass: partial=%v reason=%q", report.Partial, report.PartialReason)
	}
	for _, c := range report.Clones {
		if c.Type == domain.CloneType3 {
			t.Fatalf("type-3 clone reported in low-memory mode: %+v", c)
		}
	}
}

// Below what even the compact index needs, the scan still stops cleanly.
func TestScan_FailsCleanlyWhenEvenLowMemoryModeDoesNotFit(t *testing.T) {
	dir := lowMemoryFixture(t)
	_, compactNeed := scanMemoryNeeds(t, dir)
	_, err := NewScannerService().Scan(ScanOptions{Path: dir, MinTokens: 30, MinSimilarity: 1.0, MaxMemory: compactNeed / 2})
	if !errors.Is(err, helpers.ErrMemoryBudget) {
		t.Errorf("scan error = %v, want ErrMemoryBudget", err)
	}
}

// scanMemoryNeeds returns the budget estimate for dir with every token held
// and with the compact index.
func scanMemoryNeeds(t *testing.T, dir string) (full, compact int64) {
	t.Helper()
	files, err := collectFiles(dir, nil, nil, "")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	b := newMemoryBudget(0)
	for _, f := range files {
		src := readFile(t, f)
		b.add(len(src), len(TokenizeFile(src, DetectLanguage(f))))
	}
	full = b.estimate()
	b.compact = true
	return full, b.estimate()
}

// cloneKeys reduces clones to what identifies a finding, in a stable order.
func cloneKeys(clones []domain.Clone) []string {
	keys := make([]string, 0, len(clones))
	for _, c := range clones {
		k := fmt.Sprintf("%s %s %d tok %d lines:", c.Hash, c.Type, c.TokenCount, c.LineCount)
		for _, i := range c.Instances {
			k += fmt.Sprintf(" %s:%d-%d(%s)", i.File, i.StartLine, i.EndLine, i.Function)
		}
		keys = append(keys, k)
	}
	return keys
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
