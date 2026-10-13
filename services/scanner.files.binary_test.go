package services

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScan_BinaryFileSkipped verifies that a file containing null bytes is skipped
// and the skip count is recorded in the report.
func TestScan_BinaryFileSkipped(t *testing.T) {
	dir := t.TempDir()

	// Write a binary file (contains null bytes).
	binPath := filepath.Join(dir, "binary.go")
	binContent := []byte("package main\x00\x00\x00func foo() {}\n")
	if err := os.WriteFile(binPath, binContent, 0o600); err != nil {
		t.Fatalf("write binary file: %v", err)
	}

	// Write a normal text file.
	writeTestFile(t, dir, "text.go", `package main
func bar() {
	x := compute()
	validate(x)
	return x
}
`)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 5})
	if err != nil && err.Error() != "no supported source files found in path" {
		t.Fatalf("unexpected error: %v", err)
	}
	if report != nil && report.SkippedFiles != 1 {
		t.Errorf("expected 1 skipped file, got %d", report.SkippedFiles)
	}
}

// TestScan_BinaryFileNotInClones verifies that binary files don't appear in clone results.
func TestScan_BinaryFileNotInClones(t *testing.T) {
	dir := t.TempDir()

	// A duplicate block that also exists in a binary-looking file.
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

	// Write a binary file containing the same text but with null bytes.
	binPath := filepath.Join(dir, "c.go")
	binContent := []byte("package main\n\n" + block + "\x00\x00binary garbage")
	if err := os.WriteFile(binPath, binContent, 0o600); err != nil {
		t.Fatalf("write binary file: %v", err)
	}

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The binary file should be skipped.
	if report.SkippedFiles != 1 {
		t.Errorf("expected 1 skipped binary file, got %d", report.SkippedFiles)
	}

	// Clones should only reference a.go and b.go, not c.go.
	for _, clone := range report.Clones {
		for _, inst := range clone.Instances {
			if inst.File == binPath {
				t.Errorf("binary file %s should not appear in clone instances", binPath)
			}
		}
	}
}

// TestScan_NormalTextFileScanned verifies that a normal text .go file is scanned.
func TestScan_NormalTextFileScanned(t *testing.T) {
	dir := t.TempDir()
	block := `func helper() {
	x := compute()
	process(x)
	log(x)
	return x
}
`
	writeTestFile(t, dir, "a.go", "package main\n\n"+block)
	writeTestFile(t, dir, "b.go", "package main\n\n"+block)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.SkippedFiles != 0 {
		t.Errorf("expected 0 skipped files, got %d", report.SkippedFiles)
	}
	if report.ScannedFiles != 2 {
		t.Errorf("expected 2 scanned files, got %d", report.ScannedFiles)
	}
}

// TestIsBinaryData verifies the binary detection function.
func TestIsBinaryData(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected bool
	}{
		{"empty", []byte{}, false},
		{"text", []byte("package main\nfunc foo() {}\n"), false},
		{"with null", []byte("package\x00main"), true},
		{"null at byte 511", func() []byte {
			b := make([]byte, 512)
			b[511] = 0
			for i := 0; i < 511; i++ {
				b[i] = 'a'
			}
			return b
		}(), true},
		{"null only at byte 512 after check window", func() []byte {
			b := make([]byte, 513)
			for i := 0; i < 512; i++ {
				b[i] = 'a'
			}
			b[512] = 0
			return b
		}(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isBinaryData(tt.data)
			if got != tt.expected {
				t.Errorf("isBinaryData(%q) = %v, want %v", tt.data, got, tt.expected)
			}
		})
	}
}
