package services

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nearMissFiles builds N files with scattered operator flips so their windows
// fall through to the fuzzy (type-3) path — the same shape limits_test uses.
func nearMissFiles(t *testing.T, dir string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		ops := []string{"+", "+", "+", "+", "+", "+", "+", "+", "+", "+", "+", "+"}
		ops[i%len(ops)] = "-"
		ops[(i*2+3)%len(ops)] = "*"
		var sb strings.Builder
		sb.WriteString("package main\nfunc calc(items []int) int {\n\tacc := 0\n\tfor _, item := range items {\n")
		for _, op := range ops {
			fmt.Fprintf(&sb, "\t\tacc = acc %s item\n", op)
		}
		sb.WriteString("\t}\n\treturn acc\n}\n")
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.go", i)), []byte(sb.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetectStats_PartialAndReason(t *testing.T) {
	var s DetectStats
	if s.Partial() || s.Reason() != "" {
		t.Error("zero stats must not be partial")
	}
	s = DetectStats{CappedPairs: true}
	if !s.Partial() || !strings.Contains(s.Reason(), "max-pairs") {
		t.Errorf("capped stats: Partial=%v Reason=%q", s.Partial(), s.Reason())
	}
	s = DetectStats{BucketTruncated: true}
	if !s.Partial() || !strings.Contains(s.Reason(), "max-bucket") {
		t.Errorf("truncated stats: Partial=%v Reason=%q", s.Partial(), s.Reason())
	}
	s = DetectStats{CappedPairs: true, BucketTruncated: true}
	if !strings.Contains(s.Reason(), "max-pairs") || !strings.Contains(s.Reason(), "max-bucket") {
		t.Errorf("combined reason should mention both guards: %q", s.Reason())
	}
}

// The report itself must say when detection was capped — a CI consumer reading
// JSON or text output has no access to stderr warnings.
func TestScan_MaxPairsCap_SurfacesPartialInReport(t *testing.T) {
	dir := t.TempDir()
	nearMissFiles(t, dir, 8)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{
		Path:          dir,
		MinTokens:     20,
		MinSimilarity: 0.6,
		MaxPairs:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Partial {
		t.Fatal("report.Partial must be true when --max-pairs capped detection")
	}
	if !strings.Contains(report.PartialReason, "max-pairs") {
		t.Errorf("PartialReason should name the guard, got %q", report.PartialReason)
	}

	// And the human formats must surface it.
	text, err := FormatReport(report, "text", FormatOptions{ScanPath: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "PARTIAL RESULT") {
		t.Error("text output must carry the partial banner")
	}
	md, err := FormatReport(report, "md", FormatOptions{ScanPath: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "Partial result") {
		t.Error("markdown output must carry the partial banner")
	}
}

func TestScan_NoCap_NotPartial(t *testing.T) {
	dir := t.TempDir()
	nearMissFiles(t, dir, 3)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 20, MinSimilarity: 0.6})
	if err != nil {
		t.Fatal(err)
	}
	if report.Partial {
		t.Errorf("uncapped scan must not be partial (reason: %q)", report.PartialReason)
	}
}

// --max-file-size must skip oversized files BEFORE tokenizing them and count
// them in the report.
func TestScan_MaxFileSize_SkipsOversized(t *testing.T) {
	dir := t.TempDir()
	dup := "package main\n\nfunc helper() int {\n\tx := compute()\n\tvalidate(x)\n\ttransform(x)\n\tpersist(x)\n\treturn x\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(dup), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte(dup), 0o600); err != nil {
		t.Fatal(err)
	}
	// A "huge" generated-looking file, way over the cap we'll set.
	big := "package main\n\nfunc big() {\n" + strings.Repeat("\tprintln(1)\n", 500) + "}\n"
	if err := os.WriteFile(filepath.Join(dir, "big.go"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 15, MaxFileSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if report.SkippedLargeFiles != 1 {
		t.Errorf("want 1 oversized skip, got %d", report.SkippedLargeFiles)
	}
	for _, c := range report.Clones {
		for _, inst := range c.Instances {
			if strings.HasSuffix(inst.File, "big.go") {
				t.Error("oversized file leaked into detection")
			}
		}
	}
	// The a.go/b.go duplication must still be found.
	if report.TotalClones == 0 {
		t.Error("small duplicated files should still produce a clone")
	}
	// Summary row shows the skip.
	text, err := FormatReport(report, "text", FormatOptions{ScanPath: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "over --max-file-size") {
		t.Error("text summary should mention oversized skips")
	}
}

func TestScan_MaxFileSizeZero_Unlimited(t *testing.T) {
	dir := t.TempDir()
	dup := "package main\n\nfunc helper() int {\n\tx := compute()\n\tvalidate(x)\n\ttransform(x)\n\tpersist(x)\n\treturn x\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(dup), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte(dup), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 15, MaxFileSize: 0})
	if err != nil {
		t.Fatal(err)
	}
	if report.SkippedLargeFiles != 0 {
		t.Errorf("MaxFileSize=0 must mean unlimited, got %d skips", report.SkippedLargeFiles)
	}
}
