package services

import (
	"cmp"
	"math"
	"path/filepath"
	"slices"

	"github.com/AxeForging/dupehound/domain"
)

// Post-processing of detected clones: de-duplication, filters, insights and per-file statistics.

// annotateInsights fills in the per-clone refactor metrics: SavedLines (lines
// removable by deduplicating: every instance beyond the first is deletable)
// and Scope (same-file / cross-file / cross-dir), which signals how hard the
// refactor is — same-file extractions are trivial, cross-dir ones usually
// need a shared package.
func annotateInsights(clones []domain.Clone) {
	for i := range clones {
		c := &clones[i]
		if n := len(c.Instances); n > 1 {
			c.SavedLines = (n - 1) * c.LineCount
		}
		files := make(map[string]bool, len(c.Instances))
		dirs := make(map[string]bool, len(c.Instances))
		for _, inst := range c.Instances {
			files[inst.File] = true
			dirs[filepath.Dir(inst.File)] = true
		}
		switch {
		case len(dirs) > 1:
			c.Scope = domain.ScopeCrossDir
		case len(files) > 1:
			c.Scope = domain.ScopeCrossFile
		default:
			c.Scope = domain.ScopeSameFile
		}
	}
}

// totalSavedLines sums the refactor savings across all non-suppressed clones
// (baseline-known clones count too — they are real, standing debt).
func totalSavedLines(clones []domain.Clone) int {
	total := 0
	for _, c := range clones {
		if c.Suppressed {
			continue
		}
		total += c.SavedLines
	}
	return total
}

// filterBaselineKnown hides baseline-known clones from the clone list unless
// showAll is set (the summary still counts them via BaselineKnown). This keeps
// hook and CI output focused on the clones that are actually actionable.
func filterBaselineKnown(clones []domain.Clone, showAll bool) []domain.Clone {
	if showAll {
		return clones
	}
	return clonesWithout(clones, func(c domain.Clone) bool { return c.Baseline })
}

// clonesWithout returns the clones drop does not select. The result is never
// nil, so an empty list still renders as [] in JSON output.
func clonesWithout(clones []domain.Clone, drop func(domain.Clone) bool) []domain.Clone {
	result := make([]domain.Clone, 0, len(clones))
	for _, c := range clones {
		if !drop(c) {
			result = append(result, c)
		}
	}
	return result
}

// filterSuppressed returns only non-suppressed clones (or all if includeSuppressed).
func filterSuppressed(clones []domain.Clone, includeSuppressed bool) []domain.Clone {
	if includeSuppressed {
		return clones
	}
	return clonesWithout(clones, func(c domain.Clone) bool { return c.Suppressed })
}

// overlapSubsumeFrac is the fraction of an instance's lines that must be
// covered by an already-kept clone's instance for it to count as "the same
// region". The check is PAIR-AWARE: a candidate is dropped only when all of
// its instances are covered by instances of the SAME kept clone — i.e. it
// links the same regions an existing finding already links. That precision is
// what allows a threshold this low without eating genuinely distinct clones;
// the old strict-containment rule (100%, any kept range) let one-line-offset
// re-reports of the same region through, so heavily duplicated code produced
// several findings for a single refactor target.
const overlapSubsumeFrac = 0.6

// deduplicateOverlapping removes clones that report the same duplicated
// region pair as a larger already-kept clone (possibly at a slightly
// different size or offset). This keeps one finding per refactor target.
func deduplicateOverlapping(clones []domain.Clone) []domain.Clone {
	// Sort largest first so the biggest description of a region wins.
	// The Hash tiebreak makes the kept order deterministic when sizes are equal.
	slices.SortFunc(clones, largestFirst)

	// Kept instance ranges, indexed by file, each tagged with the kept clone
	// it belongs to so the subsume check can require a single common clone.
	type keptRange struct {
		start, end int
		cloneIdx   int
	}
	byFile := make(map[string][]keptRange)
	kept := make([]domain.Clone, 0, len(clones))

	for _, c := range clones {
		// For each instance, collect the kept clones that cover it; the
		// candidate is redundant only if one kept clone covers ALL instances.
		common := map[int]bool{}
		subsumed := len(c.Instances) > 0
		for i, inst := range c.Instances {
			instLines := inst.EndLine - inst.StartLine + 1
			cover := map[int]bool{}
			for _, r := range byFile[inst.File] {
				overlap := min(inst.EndLine, r.end) - max(inst.StartLine, r.start) + 1
				if overlap > 0 && float64(overlap) >= overlapSubsumeFrac*float64(instLines) {
					cover[r.cloneIdx] = true
				}
			}
			if i == 0 {
				common = cover
			} else {
				for idx := range common {
					if !cover[idx] {
						delete(common, idx)
					}
				}
			}
			if len(common) == 0 {
				subsumed = false
				break
			}
		}
		if subsumed {
			continue
		}
		idx := len(kept)
		kept = append(kept, c)
		for _, inst := range c.Instances {
			byFile[inst.File] = append(byFile[inst.File], keptRange{inst.StartLine, inst.EndLine, idx})
		}
	}

	return kept
}

// filterStagedClones keeps only clones where at least one instance is in a staged file.
func filterStagedClones(clones []domain.Clone, stagedSet map[string]bool) []domain.Clone {
	var filtered []domain.Clone
	for _, c := range clones {
		for _, inst := range c.Instances {
			if stagedSet[inst.File] {
				filtered = append(filtered, c)
				break
			}
		}
	}
	return filtered
}

// buildFileStats creates per-file duplication metrics from clones and line counts.
func buildFileStats(clones []domain.Clone, fileLineCount map[string]int) []domain.FileStats {
	fileDupLines := make(map[string]map[int]bool)
	for _, c := range clones {
		for _, inst := range c.Instances {
			if fileDupLines[inst.File] == nil {
				fileDupLines[inst.File] = make(map[int]bool)
			}
			for ln := inst.StartLine; ln <= inst.EndLine; ln++ {
				fileDupLines[inst.File][ln] = true
			}
		}
	}

	var stats []domain.FileStats
	for file, dupLines := range fileDupLines {
		totalLines := fileLineCount[file]
		if totalLines == 0 {
			totalLines = 1
		}
		dl := len(dupLines)
		pct := math.Round(float64(dl)/float64(totalLines)*1000) / 10
		stats = append(stats, domain.FileStats{
			File:           file,
			TotalLines:     fileLineCount[file],
			DuplicateLines: dl,
			DuplicationPct: pct,
		})
	}

	// Most duplicated first; the file name makes the order total and stable.
	slices.SortFunc(stats, func(a, b domain.FileStats) int {
		return cmp.Or(cmp.Compare(b.DuplicationPct, a.DuplicationPct), cmp.Compare(a.File, b.File))
	})

	return stats
}

// countDuplicateLines counts distinct (file, line) pairs across all clones.
func countDuplicateLines(clones []domain.Clone) int {
	type lineKey struct {
		file string
		line int
	}
	seen := make(map[lineKey]bool)
	for _, clone := range clones {
		for _, inst := range clone.Instances {
			for ln := inst.StartLine; ln <= inst.EndLine; ln++ {
				seen[lineKey{inst.File, ln}] = true
			}
		}
	}
	return len(seen)
}

// largestFirst orders clones by size, biggest first, with the hash as a
// tiebreak so the order is total and repeatable.
func largestFirst(a, b domain.Clone) int {
	return cmp.Or(cmp.Compare(b.LineCount, a.LineCount), cmp.Compare(a.Hash, b.Hash))
}
