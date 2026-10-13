package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

// TestSuppressionByHash verifies that a clone matching a hash prefix is suppressed.
func TestSuppressionByHash(t *testing.T) {
	clones := []domain.Clone{
		{Hash: "deadbeef12345678", Type: domain.CloneType1, Instances: []domain.CloneInstance{
			{File: "a.go", StartLine: 1, EndLine: 10},
		}},
		{Hash: "cafebabe00000000", Type: domain.CloneType1, Instances: []domain.CloneInstance{
			{File: "b.go", StartLine: 1, EndLine: 10},
		}},
	}
	rules := []IgnoreRule{{Hash: "deadbeef"}}
	result, count := applySuppressionRules(clones, rules, false)

	if count != 1 {
		t.Errorf("expected 1 suppressed clone, got %d", count)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 remaining clone, got %d", len(result))
	}
	if result[0].Hash != "cafebabe00000000" {
		t.Errorf("wrong clone survived, got hash %s", result[0].Hash)
	}
}

// TestSuppressionByPathGlob verifies that clones matching a path glob are suppressed.
func TestSuppressionByPathGlob(t *testing.T) {
	clones := []domain.Clone{
		{Hash: "aaa", Instances: []domain.CloneInstance{
			{File: "/repo/src/generated/code.go", StartLine: 1, EndLine: 10},
			{File: "/repo/src/main.go", StartLine: 1, EndLine: 10},
		}},
		{Hash: "bbb", Instances: []domain.CloneInstance{
			{File: "/repo/src/main.go", StartLine: 20, EndLine: 30},
			{File: "/repo/src/util.go", StartLine: 20, EndLine: 30},
		}},
	}
	rules := []IgnoreRule{{PathGlob: "**/generated/**"}}
	result, count := applySuppressionRules(clones, rules, false)

	if count != 1 {
		t.Errorf("expected 1 suppressed clone, got %d", count)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 remaining clone, got %d", len(result))
	}
	if result[0].Hash != "bbb" {
		t.Errorf("wrong clone survived")
	}
}

// TestSuppressionByFileLineRange verifies file:line range suppression.
func TestSuppressionByFileLineRange(t *testing.T) {
	clones := []domain.Clone{
		{Hash: "aaa", Instances: []domain.CloneInstance{
			{File: "src/legacy/old.go", StartLine: 45, EndLine: 60},
			{File: "src/main.go", StartLine: 1, EndLine: 16},
		}},
		{Hash: "bbb", Instances: []domain.CloneInstance{
			{File: "src/new.go", StartLine: 1, EndLine: 20},
			{File: "src/util.go", StartLine: 1, EndLine: 20},
		}},
	}
	rules := []IgnoreRule{{FilePath: "src/legacy/old.go", FileStart: 45, FileEnd: 89}}
	result, count := applySuppressionRules(clones, rules, false)

	if count != 1 {
		t.Errorf("expected 1 suppressed clone (line range match), got %d", count)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 remaining clone, got %d", len(result))
	}
}

// TestSuppressionShowSuppressed verifies --show-suppressed includes tagged suppressed clones.
func TestSuppressionShowSuppressed(t *testing.T) {
	clones := []domain.Clone{
		{Hash: "deadbeef12345678", Type: domain.CloneType1, Instances: []domain.CloneInstance{
			{File: "a.go", StartLine: 1, EndLine: 10},
		}},
	}
	rules := []IgnoreRule{{Hash: "deadbeef"}}

	// Without show-suppressed.
	result, count := applySuppressionRules(clones, rules, false)
	if len(result) != 0 {
		t.Errorf("expected 0 clones when show-suppressed=false, got %d", len(result))
	}
	if count != 1 {
		t.Errorf("expected 1 suppressed count, got %d", count)
	}

	// With show-suppressed.
	result, count = applySuppressionRules(clones, rules, true)
	if len(result) != 1 {
		t.Fatalf("expected 1 clone when show-suppressed=true, got %d", len(result))
	}
	if !result[0].Suppressed {
		t.Error("expected Suppressed=true on the returned clone")
	}
	if count != 1 {
		t.Errorf("expected 1 suppressed count, got %d", count)
	}
}

// TestSuppressionNoFile verifies that without a .dupehound-ignore file, behavior is unchanged.
func TestSuppressionNoFile(t *testing.T) {
	dir := t.TempDir()
	block := `func helper() {
	x := compute()
	process(x)
	log(x)
	store(x)
	return x
}
`
	writeTestFile(t, dir, "a.go", "package main\n\n"+block)
	writeTestFile(t, dir, "b.go", "package main\n\n"+block)

	// Explicitly set IgnoreFile to a non-existent path.
	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{
		Path:       dir,
		MinTokens:  10,
		IgnoreFile: filepath.Join(dir, ".dupehound-ignore-nonexistent"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Without ignore file, suppressedCount should be 0.
	if report.SuppressedClones != 0 {
		t.Errorf("expected 0 suppressed clones, got %d", report.SuppressedClones)
	}
}

// TestSuppressionFromIgnoreFile verifies loading from actual file.
func TestSuppressionFromIgnoreFile(t *testing.T) {
	dir := t.TempDir()
	block := `func helper() {
	x := compute()
	process(x)
	log(x)
	store(x)
	return x
}
`
	writeTestFile(t, dir, "a.go", "package main\n\n"+block)
	writeTestFile(t, dir, "b.go", "package main\n\n"+block)

	svc := NewScannerService()
	// First, get the clone hash.
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Clones) == 0 {
		t.Skip("no clones detected, cannot test suppression")
	}
	hash := report.Clones[0].Hash[:8]

	// Write ignore file with that hash.
	ignPath := filepath.Join(dir, ".dupehound-ignore")
	if err := os.WriteFile(ignPath, []byte("# suppress clone\n"+hash+"\n"), 0o600); err != nil {
		t.Fatalf("write ignore file: %v", err)
	}

	// Scan again with the ignore file.
	report2, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10, IgnoreFile: ignPath})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report2.SuppressedClones == 0 {
		t.Error("expected at least 1 suppressed clone from ignore file")
	}
}

// TestParseIgnoreLine verifies all line types are parsed correctly.
func TestParseIgnoreLine(t *testing.T) {
	tests := []struct {
		line     string
		wantHash string
		wantGlob string
		wantFile string
		wantSt   int
		wantEnd  int
	}{
		{"a1b2c3d4e5f6g7h8", "", "", "", 0, 0}, // not hex → glob
		{"a1b2c3d4", "a1b2c3d4", "", "", 0, 0}, // 8 hex chars → hash
		{"deadbeef1234", "deadbeef1234", "", "", 0, 0},
		{"src/generated/**", "", "src/generated/**", "", 0, 0},
		{"src/legacy/old.go:45-89", "", "", "src/legacy/old.go", 45, 89},
		{"**/*_test.go", "", "**/*_test.go", "", 0, 0},
	}
	for _, tt := range tests {
		rule := parseIgnoreLine(tt.line)
		if rule == nil {
			t.Errorf("parseIgnoreLine(%q) returned nil", tt.line)
			continue
		}
		if tt.wantHash != "" && rule.Hash != tt.wantHash {
			t.Errorf("parseIgnoreLine(%q).Hash = %q, want %q", tt.line, rule.Hash, tt.wantHash)
		}
		if tt.wantGlob != "" && rule.PathGlob != tt.wantGlob {
			t.Errorf("parseIgnoreLine(%q).PathGlob = %q, want %q", tt.line, rule.PathGlob, tt.wantGlob)
		}
		if tt.wantFile != "" && rule.FilePath != tt.wantFile {
			t.Errorf("parseIgnoreLine(%q).FilePath = %q, want %q", tt.line, rule.FilePath, tt.wantFile)
		}
		if tt.wantSt != 0 && rule.FileStart != tt.wantSt {
			t.Errorf("parseIgnoreLine(%q).FileStart = %d, want %d", tt.line, rule.FileStart, tt.wantSt)
		}
		if tt.wantEnd != 0 && rule.FileEnd != tt.wantEnd {
			t.Errorf("parseIgnoreLine(%q).FileEnd = %d, want %d", tt.line, rule.FileEnd, tt.wantEnd)
		}
	}
}
