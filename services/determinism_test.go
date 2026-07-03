package services

import (
	"fmt"
	"strings"
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

// fingerprint renders a clone slice into a stable string so two detection runs
// can be compared for exact equality (order included).
func fingerprint(clones []domain.Clone) string {
	var b strings.Builder
	for _, c := range clones {
		fmt.Fprintf(&b, "%s|%.2f|%dt|", c.Type, c.Similarity, c.TokenCount)
		for _, inst := range c.Instances {
			fmt.Fprintf(&b, "%s:%d-%d,", inst.File, inst.StartLine, inst.EndLine)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// TestDetect_Deterministic guards against the map-iteration-order bug that made
// clone detection (especially the type-3 fuzzy path) return a different subset
// of overlapping clones on each run. The fixtures below are deliberately a set
// of near-miss functions so the fuzzy detector produces many overlapping
// candidate pairs with tied similarity — exactly the case that used to wobble.
func TestDetect_Deterministic(t *testing.T) {
	var files []TokenizedFile
	for i := 0; i < 8; i++ {
		src := fmt.Sprintf(`package main
func handler%d(items []int) int {
	acc := 0
	for _, item := range items {
		acc += item * %d
		acc += item - %d
		acc = acc %% 1000
	}
	if acc > %d {
		acc = acc - 1
	}
	return acc
}
`, i, i+1, i+2, i*10)
		files = append(files, makeFile(fmt.Sprintf("f%d.go", i), src))
	}

	want := ""
	for run := 0; run < 25; run++ {
		clones, _ := DetectWithOptions(files, DetectOptions{MinTokens: 12, MinSimilarity: 0.7})
		got := fingerprint(clones)
		if run == 0 {
			want = got
			if !strings.Contains(got, domain.CloneType3) && len(clones) == 0 {
				t.Fatal("fixture produced no clones; it no longer exercises the fuzzy path")
			}
			continue
		}
		if got != want {
			t.Fatalf("detection is non-deterministic\nrun 0:\n%s\nrun %d:\n%s", want, run, got)
		}
	}
}
