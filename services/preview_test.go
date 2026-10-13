package services

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Regression tests for clone preview loading.
//
// loadPreviewLines used to return substrings of the whole file it had just
// read. A substring keeps its backing array alive, so every clone instance
// pinned a private copy of its entire source file for the life of the report.
// On real repositories that was the dominant memory cost by far: 7.5 GB peak on
// hashicorp/terraform against 1.1 GB once previews own their bytes.

func writePreviewFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src.go")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestLoadPreviewLines_ReturnsRequestedRange(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		start, end int
		want       []string
	}{
		{"middle of file", "a\nb\nc\nd\n", 2, 3, []string{"b", "c"}},
		{"first line only", "a\nb\nc\n", 1, 1, []string{"a"}},
		{"last line without trailing newline", "a\nb\nc", 3, 3, []string{"c"}},
		{"range running past the end is clipped", "a\nb", 2, 9, []string{"b"}},
		{"range entirely past the end", "a\nb", 7, 9, []string{}},
		{"blank lines are preserved", "a\n\nc\n", 1, 3, []string{"a", "", "c"}},
		{"indentation is preserved", "a\n\tb := 1\n", 2, 2, []string{"\tb := 1"}},
		{"empty file", "", 1, 2, []string{""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := loadPreviewLines(writePreviewFile(t, tt.content), tt.start, tt.end)
			if !slices.Equal(got, tt.want) {
				t.Errorf("loadPreviewLines(%d, %d) = %q, want %q", tt.start, tt.end, got, tt.want)
			}
		})
	}
}

func TestLoadPreviewLines_MissingFileYieldsNoPreview(t *testing.T) {
	if got := loadPreviewLines(filepath.Join(t.TempDir(), "nope.go"), 1, 3); got != nil {
		t.Errorf("expected nil preview for a missing file, got %q", got)
	}
}

func TestLoadPreviewLines_DoesNotRetainWholeFile(t *testing.T) {
	const (
		fileSize = 4 << 20 // one large source file
		previews = 16      // clone instances reported in it
	)
	line := strings.Repeat("x", 99) + "\n"
	path := writePreviewFile(t, strings.Repeat(line, fileSize/len(line)))

	heapInUse := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}

	before := heapInUse()
	kept := make([][]string, 0, previews)
	for i := range previews {
		kept = append(kept, loadPreviewLines(path, i*10+1, i*10+5))
	}
	after := heapInUse()
	runtime.KeepAlive(kept)

	// 16 five-line previews are a few KB. Pinning the file per preview would
	// retain 16 × 4 MiB = 64 MiB; allow a generous 1 MiB so the test cannot
	// flake on allocator noise while still failing by a wide margin on a leak.
	retained := int64(after) - int64(before)
	if retained > 1<<20 {
		t.Errorf("previews retain %d KiB of heap; they must not keep the source file alive", retained>>10)
	}
}
