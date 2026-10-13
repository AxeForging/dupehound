package services

import (
	"fmt"
	"hash/fnv"
	"math"
	"slices"
	"sort"

	"github.com/AxeForging/dupehound/domain"
	"github.com/AxeForging/dupehound/helpers"
)

// Type-3 (near-miss) detection by Jaccard similarity over mini-window hashes.

// detectFuzzy finds type-3 near-miss clones using mini-window Jaccard similarity.
// It skips blocks already covered by exact clones.
// Requires minTokens >= 10 to produce meaningful mini-windows; returns nil otherwise.
// inScopeFiles, if non-nil, causes candidate pair construction to skip pairs
// where neither block is in a file marked in scope (used by --since).
// maxPairs, if > 0, caps the number of candidate pairs evaluated; when reached,
// detection stops at the next block boundary and returns the (deterministic)
// partial set of type-3 matches found so far rather than discarding them.
func detectFuzzy(files []TokenizedFile, minTokens int, threshold float64, maxBucket int, exactClones []domain.Clone, inScopeFiles []bool, maxPairs int) ([]domain.Clone, DetectStats) {
	if minTokens < 10 {
		return nil, DetectStats{}
	}

	miniSize := int(math.Ceil(float64(minTokens) / 3))
	if miniSize < 2 {
		miniSize = 2
	}

	// Build set of line ranges already covered by exact detection, keyed by file path.
	type lineRange struct {
		start, end int
	}
	exactCovered := make(map[string][]lineRange)
	for _, c := range exactClones {
		for _, inst := range c.Instances {
			exactCovered[inst.File] = append(exactCovered[inst.File], lineRange{inst.StartLine, inst.EndLine})
		}
	}
	isExactCovered := func(file string, start, end int) bool {
		for _, r := range exactCovered[file] {
			// Fully contained: the block is entirely within an exact clone.
			if start >= r.start && end <= r.end {
				return true
			}
		}
		return false
	}

	// blockKey identifies a unique block position (file + token start index).
	type blockKey struct {
		fileIdx int
		pos     int
	}

	// For each file, build mini-window hash sets for each block-sized window.
	// A "block" is a minTokens-wide token window at each position.
	type blockInfo struct {
		key       blockKey
		miniSet   []uint64 // sorted, deduped mini-window hashes
		startLine int
		endLine   int
		// dataLiteral is the index of the composite data literal wholly
		// containing this block, or -1. Resolved once here rather than per
		// candidate pair, since the pair loop runs millions of times.
		dataLiteral int
	}

	var blocks []blockInfo
	miniIndex := make(map[uint64][]int) // mini-hash → block indices in `blocks`

	for fi, tf := range files {
		n := len(tf.Tokens)
		// Neighbouring blocks share all but one of their mini-windows, so each
		// mini-window is hashed once here and reused by every block covering it.
		// Hashing them per block did the same work minTokens times over.
		var miniHashes []uint64
		for pos := 0; pos+minTokens <= n; pos++ {
			// Skip blocks not fully inside a function body.
			if !windowInFunc(tf.InFunc, pos, minTokens) {
				continue
			}

			startLine := tf.Tokens[pos].Line
			endIdx := pos + minTokens - 1
			if endIdx >= n {
				endIdx = n - 1
			}
			endLine := tf.Tokens[endIdx].Line

			// Skip blocks already covered by exact detection.
			if isExactCovered(tf.Path, startLine, endLine) {
				continue
			}

			// Build the mini-window hash set for this block as a sorted,
			// deduped []uint64. A packed slice costs ~8 bytes/entry vs the
			// ~50 bytes/entry of a map[uint64]bool; because detectFuzzy
			// allocates one set per token window across the entire repo, this
			// is the dominant memory cost and the map form was the source of
			// multi-GB peaks on large codebases. Set semantics (and therefore
			// results) are identical — jaccardSimilarity merges two sorted
			// slices instead of probing a map.
			// Uses hashWindowFull (includes OrigText) so that different
			// identifiers/literals produce different mini-hashes,
			// enabling meaningful Jaccard comparison.
			if miniHashes == nil {
				miniHashes = make([]uint64, n-miniSize+1)
				for i := range miniHashes {
					miniHashes[i] = hashWindowFull(tf.Tokens[i : i+miniSize])
				}
			}
			miniSet := make([]uint64, 0, minTokens-miniSize+1)
			for _, h := range miniHashes[pos : pos+minTokens-miniSize+1] {
				if h != 0 {
					miniSet = append(miniSet, h)
				}
			}
			miniSet = sortDedupU64(miniSet)

			idx := len(blocks)
			blocks = append(blocks, blockInfo{
				key:         blockKey{fi, pos},
				miniSet:     miniSet,
				startLine:   startLine,
				endLine:     endLine,
				dataLiteral: tf.dataLiteralAt(pos, minTokens),
			})

			for _, h := range miniSet {
				miniIndex[h] = append(miniIndex[h], idx)
			}
		}
	}

	// Evaluate candidate pairs by STREAMING over blocks instead of
	// materializing the full pair set. The previous approach built a global
	// `pairs []pair` slice plus a `seen map[pair]bool` dedup map, both of which
	// grow with the total number of candidate pairs — millions on a
	// high-duplication repo — and were the dominant memory cost (multi-GB
	// peaks). Here each block gathers its partners (other blocks sharing a
	// mini-hash) into a small scratch set that is reused across iterations and
	// evaluated inline, so the working set is bounded by a single block's
	// partner count rather than the whole pair universe.
	//
	// Each unordered pair {a,b} is handled exactly once — from the lower index
	// a, considering only partners b > a — which replaces the global `seen`
	// dedup with the natural ordering and costs no memory.
	type fuzzyClone struct {
		aIdx, bIdx int
		similarity float64
	}
	var fuzzyMatches []fuzzyClone

	truncationOccurred := false
	evaluated := 0

	// progressStep throttles progress logs to ~20 emissions across the whole
	// scan, based on block index since the total pair count is no longer known
	// up front. Below the threshold we stay silent.
	progressStep := len(blocks) / 20
	if progressStep < 1 {
		progressStep = 1
	}

	capped := false
	cappedAt := 0
	partners := make(map[int]struct{}) // reused per block; cleared each iteration
	for a := 0; a < len(blocks); a++ {
		// Graceful runaway backstop. If cumulative evaluated pairs already
		// exceeded the cap, stop here. Cutting on a BLOCK boundary (rather than
		// mid-block) keeps the partial result deterministic, and unlike a hard
		// abort it KEEPS the type-3 clones already found instead of discarding
		// them. Exact (type-1/2) clones are untouched — they are computed before
		// this function runs — so the worst case is partial type-3, never a
		// blackout.
		if maxPairs > 0 && evaluated > maxPairs {
			capped = true
			cappedAt = a
			break
		}

		if len(blocks) > 20000 && a > 0 && a%progressStep == 0 {
			helpers.Log.Info().
				Int("blocks_processed", a).
				Int("total_blocks", len(blocks)).
				Int("matches_so_far", len(fuzzyMatches)).
				Msg("fuzzy detection progress")
		}

		ba := blocks[a]
		clear(partners)
		for _, h := range ba.miniSet {
			bucket := miniIndex[h]
			if len(bucket) < 2 {
				continue
			}
			if len(bucket) > maxBucket {
				truncationOccurred = true
				// miniIndex buckets are built in ascending block-index order,
				// so [:maxBucket] keeps the same lowest-index members the old
				// per-bucket truncation did, preserving determinism.
				bucket = bucket[:maxBucket]
			}
			for _, b := range bucket {
				if b > a {
					partners[b] = struct{}{}
				}
			}
		}

		for b := range partners {
			bb := blocks[b]
			// Diff-aware scope filter: skip pairs where neither block touches a
			// file in scope. This is the dominant cost win for --since on a
			// large repo because it cuts the work before any Jaccard math.
			if inScopeFiles != nil &&
				!fileInScope(inScopeFiles, ba.key.fileIdx) &&
				!fileInScope(inScopeFiles, bb.key.fileIdx) {
				continue
			}
			// Skip same-file overlapping blocks, and blocks that are two
			// windows over the rows of a single data table (issue #31).
			if ba.key.fileIdx == bb.key.fileIdx {
				dist := ba.key.pos - bb.key.pos
				if dist < 0 {
					dist = -dist
				}
				if dist < minTokens {
					continue
				}
				if ba.dataLiteral >= 0 && ba.dataLiteral == bb.dataLiteral {
					continue
				}
			}

			evaluated++
			sim := jaccardSimilarity(ba.miniSet, bb.miniSet)
			if sim >= threshold && sim < 1.0 {
				fuzzyMatches = append(fuzzyMatches, fuzzyClone{a, b, sim})
			}
		}
	}

	if capped {
		helpers.Log.Warn().
			Int("max_pairs", maxPairs).
			Int("evaluated_pairs", evaluated).
			Int("blocks_processed", cappedAt).
			Int("total_blocks", len(blocks)).
			Msg("type-3 detection capped by --max-pairs: results are PARTIAL (type-1/2 are complete; raise --max-pairs, lower --max-bucket, or set --similarity 1.0 for full type-3 coverage)")
	}

	// One aggregated warning if any bucket was truncated, so a hot codebase
	// doesn't spam the log with hundreds of identical lines.
	if truncationOccurred {
		helpers.Log.Warn().
			Int("max_bucket", maxBucket).
			Msg("fuzzy bucket truncation: some near-miss clones may not be reported (raise --max-bucket to reduce)")
	}

	// Sort by similarity descending to prioritize best matches. The block
	// indices (aIdx < bIdx, both deterministic) are used as tiebreakers so the
	// ordering is total: without them, equal-similarity matches sort in an
	// arbitrary order and the greedy dedup below keeps a different (random)
	// subset of overlapping type-3 clones on every run.
	sort.Slice(fuzzyMatches, func(i, j int) bool {
		a, b := fuzzyMatches[i], fuzzyMatches[j]
		if a.similarity != b.similarity {
			return a.similarity > b.similarity
		}
		if a.aIdx != b.aIdx {
			return a.aIdx < b.aIdx
		}
		return a.bIdx < b.bIdx
	})

	// Deduplicate: mark blocks as used so overlapping pairs don't create duplicates.
	used := make(map[int]bool)
	var clones []domain.Clone

	for _, fm := range fuzzyMatches {
		if used[fm.aIdx] || used[fm.bIdx] {
			continue
		}
		used[fm.aIdx] = true
		used[fm.bIdx] = true

		ba, bb := blocks[fm.aIdx], blocks[fm.bIdx]

		instances := []domain.CloneInstance{
			buildSingleInstance(files, ba.key.fileIdx, ba.key.pos, minTokens),
			buildSingleInstance(files, bb.key.fileIdx, bb.key.pos, minTokens),
		}

		lineCount := 0
		if len(instances) > 0 {
			lineCount = instances[0].EndLine - instances[0].StartLine + 1
		}

		// Content-based pair fingerprint: combine the two blocks' full token
		// hashes in sorted order so the identity is independent of file order
		// and position. (The old fileIdx:pos hash changed whenever ANY file was
		// added or removed, which made baselines and hash suppression useless
		// for type-3 clones.)
		ha := hashWindowFull(files[ba.key.fileIdx].Tokens[ba.key.pos : ba.key.pos+minTokens])
		hb := hashWindowFull(files[bb.key.fileIdx].Tokens[bb.key.pos : bb.key.pos+minTokens])
		if ha > hb {
			ha, hb = hb, ha
		}
		h := fnv.New64a()
		_, _ = fmt.Fprintf(h, "%016x:%016x", ha, hb)

		clones = append(clones, domain.Clone{
			Hash:       fmt.Sprintf("%016x", h.Sum64()),
			Type:       domain.CloneType3,
			Similarity: math.Round(fm.similarity*100) / 100, // round to 2 decimals
			LineCount:  lineCount,
			TokenCount: minTokens,
			Instances:  instances,
		})
	}

	return clones, DetectStats{
		CappedPairs:     capped,
		BucketTruncated: truncationOccurred,
		EvaluatedPairs:  evaluated,
	}
}

// jaccardSimilarity computes |A ∩ B| / |A ∪ B| for two hash sets.
// jaccardSimilarity computes |a∩b| / |a∪b| for two sorted, deduped hash
// slices using a single linear two-pointer merge. This is both lighter and
// faster than the previous map-based form: no per-probe hashing, and the
// scans are sequential and cache-friendly — which matters because this is the
// innermost loop, run once per candidate pair (millions of times on big repos).
func jaccardSimilarity(a, b []uint64) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	intersection := 0
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			intersection++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// sortDedupU64 sorts a hash slice ascending and removes adjacent duplicates in
// place, turning a multiset into the set form jaccardSimilarity expects.
func sortDedupU64(s []uint64) []uint64 {
	if len(s) < 2 {
		return s
	}
	slices.Sort(s)
	out := s[:1]
	for _, v := range s[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}
