package services

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

// updateGolden regenerates the golden files instead of comparing against them:
//
//	go test ./services -run TestReporterGolden -update
//
// Run it once to capture the current output, eyeball the diff, then commit the
// .golden files. Subsequent runs assert the formatters stay byte-identical —
// which is exactly the guard a reporter refactor needs.
var updateGolden = flag.Bool("update", false, "update reporter golden files")

// goldenReport is a deliberately rich fixture: skipped + suppressed counts, a
// --since diff, more hotspots/clones/dead-functions than the top-N cap (to hit
// the overflow branches), and a test↔prod clone (to hit that section). It
// exercises every branch the text and markdown formatters share.
func goldenReport() *domain.Report {
	inst := func(file string, start, end int, lines ...string) domain.CloneInstance {
		return domain.CloneInstance{File: file, StartLine: start, EndLine: end, Lines: lines}
	}

	var clones []domain.Clone
	// One test↔prod clone (its own section).
	clones = append(clones, domain.Clone{
		Hash: "tp01", Type: domain.CloneType2, Similarity: 1, LineCount: 18, TokenCount: 120,
		TestProdSpan: true,
		Instances: []domain.CloneInstance{
			inst("/repo/svc/user.go", 10, 27, "func A() {", "  do()", "}"),
			inst("/repo/svc/user_test.go", 40, 57, "func A() {", "  do()", "}"),
		},
	})
	// One suppressed clone (only shown with ShowSuppressed).
	clones = append(clones, domain.Clone{
		Hash: "sup1", Type: domain.CloneType1, Similarity: 1, LineCount: 9, TokenCount: 60,
		Suppressed: true,
		Instances:  []domain.CloneInstance{inst("/repo/a.go", 1, 9), inst("/repo/b.go", 1, 9)},
	})
	// 12 normal clones with descending impact → triggers the top-clones overflow.
	for i := 0; i < 12; i++ {
		clones = append(clones, domain.Clone{
			Hash: "n" + string(rune('a'+i)), Type: domain.CloneType2, Similarity: 1,
			LineCount: 30 - i, TokenCount: 200 - i*5, ChurnScore: i,
			Instances: []domain.CloneInstance{
				inst("/repo/pkg/f.go", 100+i, 130+i-i, "x := 1", "y := 2"),
				inst("/repo/pkg/g.go", 200+i, 230+i-i, "x := 1", "y := 2"),
			},
		})
	}

	var stats []domain.FileStats
	for i := 0; i < 12; i++ {
		stats = append(stats, domain.FileStats{
			File:       "/repo/pkg/file" + string(rune('a'+i)) + ".go",
			TotalLines: 500 - i*10, DuplicateLines: 120 - i*8,
			DuplicationPct: 25.0 - float64(i),
		})
	}

	var dead []domain.DeadFunc
	for i := 0; i < 12; i++ {
		dead = append(dead, domain.DeadFunc{
			File: "/repo/pkg/dead.go", Line: 10 + i,
			Name: "unused" + string(rune('A'+i)), Language: "go",
		})
	}

	return &domain.Report{
		TotalFiles: 20, ScannedFiles: 17, SkippedFiles: 2,
		TotalClones: len(clones), TotalLines: 3714, DuplicateLines: 197, DuplicationPct: 5.3,
		SuppressedClones: 1,
		FileStats:        stats,
		Clones:           clones,
		DeadFunctions:    dead,
		NewClones:        clones[:3],
		SinceDiffRef:     "origin/main", SinceDiffFiles: 17,
	}
}

func TestReporterGolden(t *testing.T) {
	report := goldenReport()
	opts := FormatOptions{ScanPath: "/repo", Top: 10}

	cases := []struct {
		name, file, got string
	}{
		{"text", "reporter_text.golden", formatText(report, opts)},
		{"markdown", "reporter_md.golden", formatMarkdown(report, opts)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join("testdata", tc.file)
			if *updateGolden {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if tc.got != string(want) {
				t.Errorf("%s output drifted from golden.\n--- got ---\n%s\n--- want ---\n%s", tc.name, tc.got, want)
			}
		})
	}
}
