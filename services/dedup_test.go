package services

import (
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

func dedupClone(hash string, lineCount int, ranges ...[3]any) domain.Clone {
	c := domain.Clone{Hash: hash, Type: domain.CloneType3, LineCount: lineCount}
	for _, r := range ranges {
		c.Instances = append(c.Instances, domain.CloneInstance{
			File: r[0].(string), StartLine: r[1].(int), EndLine: r[2].(int),
		})
	}
	return c
}

func keptHashes(clones []domain.Clone) map[string]bool {
	m := map[string]bool{}
	for _, c := range clones {
		m[c.Hash] = true
	}
	return m
}

// A smaller clone that links the SAME two regions as a bigger kept clone —
// just offset by a couple of lines — is a re-report of one refactor target
// and must be dropped. Strict containment used to let these through.
func TestDedup_SameRegionPairOffsetIsDropped(t *testing.T) {
	big := dedupClone("big", 17, [3]any{"/t.go", 746, 762}, [3]any{"/t.go", 800, 815})
	frag := dedupClone("frag", 12, [3]any{"/t.go", 744, 755}, [3]any{"/t.go", 798, 809})
	kept := keptHashes(deduplicateOverlapping([]domain.Clone{big, frag}))
	if !kept["big"] || kept["frag"] {
		t.Errorf("offset re-report should be dropped, big kept: kept=%v", kept)
	}
}

// PAIR-AWARENESS: a candidate whose instances overlap two DIFFERENT kept
// clones links regions no single finding links — it is new information and
// must survive, even though each instance individually looks covered.
func TestDedup_InstancesCoveredByDifferentClonesIsKept(t *testing.T) {
	k1 := dedupClone("k1", 20, [3]any{"/t.go", 100, 119}, [3]any{"/t.go", 500, 519})
	k2 := dedupClone("k2", 20, [3]any{"/t.go", 200, 219}, [3]any{"/t.go", 600, 619})
	// bridge links k1's first region to k2's first region.
	bridge := dedupClone("bridge", 15, [3]any{"/t.go", 102, 116}, [3]any{"/t.go", 203, 217})
	kept := keptHashes(deduplicateOverlapping([]domain.Clone{k1, k2, bridge}))
	if !kept["bridge"] {
		t.Error("clone linking two different kept clones' regions is new information and must be kept")
	}
}

// Non-overlapping clones in the same file are all real findings.
func TestDedup_DistinctRegionsAllKept(t *testing.T) {
	a := dedupClone("a", 16, [3]any{"/t.go", 463, 478}, [3]any{"/t.go", 594, 608})
	b := dedupClone("b", 14, [3]any{"/t.go", 493, 506}, [3]any{"/t.go", 547, 559})
	kept := keptHashes(deduplicateOverlapping([]domain.Clone{a, b}))
	if !kept["a"] || !kept["b"] {
		t.Errorf("distinct regions must both survive, kept=%v", kept)
	}
}

// A 3-instance candidate is NOT subsumed by a 2-instance kept clone: the
// extra instance is exactly the signal the baseline ratchet cares about.
func TestDedup_ExtraInstanceIsKept(t *testing.T) {
	two := dedupClone("two", 15, [3]any{"/t.go", 100, 114}, [3]any{"/t.go", 200, 214})
	three := dedupClone("three", 12, [3]any{"/t.go", 101, 112}, [3]any{"/t.go", 201, 212}, [3]any{"/u.go", 50, 61})
	kept := keptHashes(deduplicateOverlapping([]domain.Clone{two, three}))
	if !kept["three"] {
		t.Error("candidate with an instance outside the kept clone must survive")
	}
}

// Low overlap (well under the threshold) never subsumes.
func TestDedup_LowOverlapKept(t *testing.T) {
	a := dedupClone("a", 20, [3]any{"/t.go", 100, 119}, [3]any{"/t.go", 300, 319})
	// overlaps only 4/20 = 20% of each instance
	b := dedupClone("b", 20, [3]any{"/t.go", 116, 135}, [3]any{"/t.go", 316, 335})
	kept := keptHashes(deduplicateOverlapping([]domain.Clone{a, b}))
	if !kept["a"] || !kept["b"] {
		t.Errorf("20%% overlap must not subsume, kept=%v", kept)
	}
}
