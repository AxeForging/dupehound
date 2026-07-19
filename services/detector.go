package services

import (
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/AxeForging/dupehound/domain"
	"github.com/AxeForging/dupehound/helpers"
)

// TokenizedFile holds the lexed representation of one source file.
// RawLines are not stored here to reduce memory; preview lines are
// loaded lazily from disk only for files that end up in detected clones.
type TokenizedFile struct {
	Path    string
	Tokens  []Token    // normalized token sequence (no newlines)
	InFunc  []bool     // per-token: true if inside a function/method body
	Ignored []bool     // per-token: true if in a dupehound:ignore annotated block
	Funcs   []FuncSpan // function body spans with best-effort names, for clone attribution
	// DataLiterals are the token spans of composite data literals written inside
	// function bodies — `[]T{…}`, `map[K]V{…}`, and the `[]struct{…}{…}` of a
	// table-driven test. They are not excluded from detection; they are used to
	// recognise a clone whose every instance sits inside ONE such literal, which
	// describes a data table rather than duplicated logic.
	DataLiterals []TokenSpan
	// FuncLiterals are the bodies of function literals. A body nested inside a
	// DataLiterals span is logic that merely lives in a table, so it is exempt
	// from the rule above.
	FuncLiterals []TokenSpan
}

// BuildTokenizedFileWithIgnore tokenizes a source file, applying inline suppression
// markers from both the source (dupehound:ignore comments) and the ignore rules.
func BuildTokenizedFileWithIgnore(path, content string, lang *domain.Language, rules []IgnoreRule) TokenizedFile {
	return buildTokenizedFile(path, TokenizeFileWithIgnore(content, lang), lang)
}

// buildTokenizedFile assembles a TokenizedFile from an already-lexed token
// stream. It is the single place the per-token masks and spans are derived, so
// the ignore-aware and plain constructors cannot drift apart.
func buildTokenizedFile(path string, tokens []Token, lang *domain.Language) TokenizedFile {
	inFunc, funcs := markFunctionBodies(tokens, lang)
	ignored := markIgnoredBlocks(tokens, inFunc)

	// Detection only ever looks at InFunc, so clearing it is how a suppression
	// takes effect.
	for i, ign := range ignored {
		if ign {
			inFunc[i] = false
		}
	}

	dataLiterals := findDataLiteralSpans(tokens, lang)

	return TokenizedFile{
		Path:         path,
		Tokens:       tokens,
		InFunc:       inFunc,
		Ignored:      ignored,
		Funcs:        funcs,
		DataLiterals: dataLiterals,
		FuncLiterals: nestedFuncLiterals(findFuncLiteralBodies(tokens, lang, funcs), dataLiterals),
	}
}

// nestedFuncLiterals keeps only the function bodies that sit inside a data
// literal — the `run func(t *testing.T)` column of a table.
//
// The narrowing is what makes the exemption safe outside Go, where the spans
// come from markFunctionBodies and therefore include the enclosing test function
// itself. Left unfiltered, that outer span would cover every table in the file
// and exempt all of them, disabling the rule entirely.
func nestedFuncLiterals(funcSpans, dataLiterals []TokenSpan) []TokenSpan {
	if len(funcSpans) == 0 || len(dataLiterals) == 0 {
		return nil
	}
	nested := make([]TokenSpan, 0, len(funcSpans))
	for _, fs := range funcSpans {
		for _, dl := range dataLiterals {
			if fs.Start >= dl.Start && fs.End <= dl.End {
				nested = append(nested, fs)
				break
			}
		}
	}
	return nested
}

// dataLiteralAt returns the index of the data literal wholly containing the
// token window [pos, pos+length), or -1 when the window is not pure table data.
//
// Touching a function literal at all disqualifies the window. Logic written in
// a table — a `run func(t *testing.T)` column — is still logic, and a window
// that straddles a func body and the rows around it is not something the "this
// is just a data table" rule should ever silence.
func (tf TokenizedFile) dataLiteralAt(pos, length int) int {
	if len(tf.DataLiterals) == 0 || length <= 0 {
		return -1
	}
	end := pos + length - 1
	for _, fl := range tf.FuncLiterals {
		if pos <= fl.End && fl.Start <= end {
			return -1
		}
	}
	for i, s := range tf.DataLiterals {
		if pos >= s.Start && end <= s.End {
			return i
		}
	}
	return -1
}

// allInsideOneDataLiteral reports whether every instance of a clone group lands
// in the same composite data literal. The rows of a table are structurally
// identical by construction, so a "clone" confined to one literal describes a
// single data table rather than duplicated logic (issue #31). Instances spread
// across two literals, two functions, or two files are left alone — repeating
// the same table twice is duplication worth reporting.
func allInsideOneDataLiteral(files []TokenizedFile, starts []globalPos, totalTokens int) bool {
	first := starts[0]
	litIdx := files[first.FileIdx].dataLiteralAt(first.Pos, totalTokens)
	if litIdx < 0 {
		return false
	}
	for _, s := range starts[1:] {
		if s.FileIdx != first.FileIdx {
			return false
		}
		if files[s.FileIdx].dataLiteralAt(s.Pos, totalTokens) != litIdx {
			return false
		}
	}
	return true
}

// globalPos identifies a window position across all files.
type globalPos struct {
	FileIdx int
	Pos     int // index into the file's token slice
}

// DetectOptions holds parameters for clone detection.
type DetectOptions struct {
	MinTokens     int
	MinSimilarity float64
	MaxBucket     int // max blocks per fuzzy mini-hash bucket (0 = default 500)
	// MaxPairs, if > 0, caps the number of fuzzy candidate pairs the detector
	// evaluates — a runaway backstop that bounds CPU time on pathological
	// inputs. When the cap is reached, type-3 detection stops at the next block
	// boundary and returns the matches found so far (a deterministic PARTIAL
	// result); type-1/2 clones are unaffected. 0 means no cap.
	MaxPairs int
	// InScopeFiles, when non-nil, restricts detection to clones where at
	// least one instance is in a file marked true. Indexed by the position
	// of the file in the `files` slice passed to DetectWithOptions. The
	// fuzzy detector also uses this to skip candidate pairs where neither
	// block is in scope, which is the main cost win for diff-aware scanning.
	// Nil means "all files in scope" (original behavior).
	InScopeFiles []bool
}

// fileInScope is a small helper that returns true if either the scope filter
// is disabled (nil) or the given file index is marked in scope. Out-of-range
// indices are treated as in-scope to keep the helper safe at call sites.
func fileInScope(scope []bool, fileIdx int) bool {
	if scope == nil {
		return true
	}
	if fileIdx < 0 || fileIdx >= len(scope) {
		return true
	}
	return scope[fileIdx]
}

// DetectStats reports how complete a detection run was. The scan is normally
// exhaustive; these flags flip only when a safety guard reduced type-3
// coverage. Type-1/2 results are always complete.
type DetectStats struct {
	// CappedPairs is true when --max-pairs stopped fuzzy detection early:
	// the type-3 results are a deterministic partial set.
	CappedPairs bool
	// BucketTruncated is true when at least one fuzzy candidate bucket was
	// cut to --max-bucket, so some near-miss pairs were never compared.
	BucketTruncated bool
	// EvaluatedPairs counts the fuzzy candidate pairs actually compared.
	EvaluatedPairs int
}

// Partial reports whether any guard reduced type-3 coverage.
func (s DetectStats) Partial() bool { return s.CappedPairs || s.BucketTruncated }

// Reason renders a human-readable explanation for a partial result, or "".
func (s DetectStats) Reason() string {
	switch {
	case s.CappedPairs && s.BucketTruncated:
		return "type-3 detection capped by --max-pairs and --max-bucket; type-1/2 results are complete"
	case s.CappedPairs:
		return "type-3 detection stopped at --max-pairs; type-1/2 results are complete"
	case s.BucketTruncated:
		return "some fuzzy candidate buckets were truncated by --max-bucket; type-1/2 results are complete"
	default:
		return ""
	}
}

// DetectWithOptions finds all clone groups with full control over detection
// parameters. The returned DetectStats reports whether any safety guard
// (--max-pairs, --max-bucket) reduced type-3 coverage.
func DetectWithOptions(files []TokenizedFile, opts DetectOptions) ([]domain.Clone, DetectStats) {
	if len(files) == 0 || opts.MinTokens <= 0 {
		return nil, DetectStats{}
	}

	exact := detectExact(files, opts.MinTokens, opts.InScopeFiles)

	if opts.MinSimilarity >= 1.0 {
		return exact, DetectStats{}
	}

	maxBucket := opts.MaxBucket
	if maxBucket <= 0 {
		maxBucket = 5000
	}

	fuzzy, stats := detectFuzzy(files, opts.MinTokens, opts.MinSimilarity, maxBucket, exact, opts.InScopeFiles, opts.MaxPairs)
	return append(exact, fuzzy...), stats
}

// detectExact finds type-1 and type-2 clones using hash-based sliding windows.
// inScopeFiles, if non-nil, causes the detector to skip clone groups where
// no participating file is in scope (used by --since for diff-aware scanning).
func detectExact(files []TokenizedFile, minTokens int, inScopeFiles []bool) []domain.Clone {
	// Step 1: for each file, compute the hash of every minTokens-wide window.
	// Skip windows that are not fully inside a function body.
	posToHash := make([][]uint64, len(files))
	for fi, tf := range files {
		n := len(tf.Tokens)
		hashes := make([]uint64, n)
		for i := 0; i+minTokens <= n; i++ {
			if !windowInFunc(tf.InFunc, i, minTokens) {
				continue
			}
			hashes[i] = hashWindow(tf.Tokens[i : i+minTokens])
		}
		posToHash[fi] = hashes
	}

	// Step 2: build a global hash → positions index.
	hashIndex := make(map[uint64][]globalPos)
	for fi, hashes := range posToHash {
		for pi, h := range hashes {
			if h == 0 {
				continue
			}
			hashIndex[h] = append(hashIndex[h], globalPos{fi, pi})
		}
	}

	// Step 3: collect all candidate positions (those whose hash has ≥2 occurrences)
	// and sort them by (fileIdx, pos) for deterministic, left-to-right processing.
	type candidate struct {
		FileIdx int
		Pos     int
		Hash    uint64
	}
	var candidates []candidate
	for h, positions := range hashIndex {
		if len(positions) < 2 {
			continue
		}
		for _, p := range positions {
			candidates = append(candidates, candidate{p.FileIdx, p.Pos, h})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.FileIdx != b.FileIdx {
			return a.FileIdx < b.FileIdx
		}
		return a.Pos < b.Pos
	})

	// Step 4: sweep left-to-right. For each uncovered candidate, gather its group,
	// extend the block as far as all members agree, then mark the block as covered.
	covered := make(map[globalPos]bool)
	var clones []domain.Clone

	for _, cand := range candidates {
		gp := globalPos{cand.FileIdx, cand.Pos}
		if covered[gp] {
			continue
		}

		allGroup := hashIndex[cand.Hash]

		// Group positions by file.
		byFile := make(map[int][]globalPos)
		for _, p := range allGroup {
			if !covered[p] {
				byFile[p.FileIdx] = append(byFile[p.FileIdx], p)
			}
		}
		for fi := range byFile {
			fps := byFile[fi]
			sort.Slice(fps, func(i, j int) bool { return fps[i].Pos < fps[j].Pos })
			byFile[fi] = fps
		}

		var starts []globalPos

		if len(byFile) >= 2 {
			for _, fps := range byFile {
				starts = append(starts, fps[0])
			}
		} else {
			for _, fps := range byFile {
				if len(fps) < 2 {
					continue
				}
				starts = []globalPos{fps[0]}
				for _, p := range fps[1:] {
					prev := starts[len(starts)-1]
					if p.Pos-prev.Pos >= minTokens {
						starts = append(starts, p)
					}
				}
			}
		}

		if len(starts) < 2 {
			continue
		}

		// `starts` is assembled from a map, so its order varies run to run.
		// Sort it for deterministic instance ordering in the output.
		sort.Slice(starts, func(i, j int) bool {
			if starts[i].FileIdx != starts[j].FileIdx {
				return starts[i].FileIdx < starts[j].FileIdx
			}
			return starts[i].Pos < starts[j].Pos
		})

		// Diff-aware scope filter: skip this clone group if none of the
		// participating files were marked in scope by --since. This is the
		// upfront work-skip that turns --since from a post-filter into a
		// real cost reduction.
		if inScopeFiles != nil {
			anyInScope := false
			for _, s := range starts {
				if fileInScope(inScopeFiles, s.FileIdx) {
					anyInScope = true
					break
				}
			}
			if !anyInScope {
				// Mark covered so the same hash group isn't reconsidered.
				for _, s := range starts {
					covered[globalPos{s.FileIdx, s.Pos}] = true
				}
				continue
			}
		}

		// Step 5: extend forward greedily.
		extLen := 0
		for {
			var nextHash uint64
			canExtend := true
			for _, s := range starts {
				nextPi := s.Pos + extLen + 1
				if nextPi >= len(posToHash[s.FileIdx]) || posToHash[s.FileIdx][nextPi] == 0 {
					canExtend = false
					break
				}
				nh := posToHash[s.FileIdx][nextPi]
				if nextHash == 0 {
					nextHash = nh
				} else if nh != nextHash {
					canExtend = false
					break
				}
			}
			if !canExtend {
				break
			}
			extLen++
		}

		totalTokens := minTokens + extLen

		// Every seed position is marked covered — including ones dropped just
		// below — so the same region is never reseeded as a second group.
		for _, s := range starts {
			for k := 0; k <= extLen; k++ {
				covered[globalPos{s.FileIdx, s.Pos + k}] = true
			}
		}

		// Seeds within one file were spaced minTokens apart, but greedy
		// extension grew every block to totalTokens, so neighbouring instances
		// can now overlap. Overlapping windows are one stretch of text counted
		// several times rather than distinct duplicates, so keep only instances
		// that stay clear of the previous one.
		starts = dropOverlappingStarts(starts, totalTokens)
		if len(starts) < 2 {
			continue
		}

		// A group confined to one composite data literal is a table matching
		// its own rows, not duplicated logic.
		if allInsideOneDataLiteral(files, starts, totalTokens) {
			continue
		}

		instances := buildInstances(files, starts, totalTokens)

		lineCount := 0
		if len(instances) > 0 {
			lineCount = instances[0].EndLine - instances[0].StartLine + 1
		}

		cloneType, similarity := classifyClone(files, starts, totalTokens)

		// Fingerprint the FULL extended block (not just the seed window) so the
		// hash is a stable, content-based identity: every instance shares the
		// same normalized structure, so the fingerprint is independent of file
		// order, position, and line numbers. Baseline ratcheting and hash-based
		// suppression rules rely on this stability.
		ref := files[starts[0].FileIdx]
		fingerprint := hashWindow(ref.Tokens[starts[0].Pos : starts[0].Pos+totalTokens])

		clones = append(clones, domain.Clone{
			Hash:       fmt.Sprintf("%016x", fingerprint),
			Type:       cloneType,
			Similarity: similarity,
			LineCount:  lineCount,
			TokenCount: totalTokens,
			Instances:  instances,
		})
	}

	return clones
}

// dropOverlappingStarts keeps only the instances of one clone group that do not
// overlap an instance already kept. starts must be sorted by (FileIdx, Pos);
// instances in different files can never overlap, so the check is per-file.
// The first instance is always kept, which keeps the group's fingerprint and
// classification (both derived from starts[0]) stable.
func dropOverlappingStarts(starts []globalPos, totalTokens int) []globalPos {
	kept := make([]globalPos, 0, len(starts))
	lastFile, lastEnd := -1, 0
	for _, s := range starts {
		if s.FileIdx == lastFile && s.Pos < lastEnd {
			continue
		}
		kept = append(kept, s)
		lastFile, lastEnd = s.FileIdx, s.Pos+totalTokens
	}
	return kept
}

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
			miniSet := make([]uint64, 0, minTokens-miniSize+1)
			for i := pos; i+miniSize <= pos+minTokens; i++ {
				h := hashWindowFull(tf.Tokens[i : i+miniSize])
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

// buildInstances creates CloneInstance structs with lazy-loaded preview lines.
func buildInstances(files []TokenizedFile, starts []globalPos, totalTokens int) []domain.CloneInstance {
	instances := make([]domain.CloneInstance, 0, len(starts))
	for _, s := range starts {
		instances = append(instances, buildSingleInstance(files, s.FileIdx, s.Pos, totalTokens))
	}
	return instances
}

// buildSingleInstance creates a single CloneInstance with lazy preview and
// best-effort enclosing-function attribution.
func buildSingleInstance(files []TokenizedFile, fileIdx, pos, totalTokens int) domain.CloneInstance {
	toks := files[fileIdx].Tokens
	startLine := toks[pos].Line
	endIdx := pos + totalTokens - 1
	if endIdx >= len(toks) {
		endIdx = len(toks) - 1
	}
	endLine := toks[endIdx].Line
	preview := loadPreviewLines(files[fileIdx].Path, startLine, endLine)

	return domain.CloneInstance{
		File:      files[fileIdx].Path,
		StartLine: startLine,
		EndLine:   endLine,
		Function:  enclosingFuncName(files[fileIdx].Funcs, pos),
		Lines:     preview,
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

// windowInFunc returns true if all tokens in the window [pos, pos+size) are
// inside a function body. Returns true if InFunc is nil (no filtering).
func windowInFunc(inFunc []bool, pos, size int) bool {
	if len(inFunc) == 0 {
		return true
	}
	end := pos + size
	if end > len(inFunc) {
		end = len(inFunc)
	}
	for i := pos; i < end; i++ {
		if !inFunc[i] {
			return false
		}
	}
	return true
}

// classifyClone determines the clone type and similarity by comparing
// the original token text across all instances.
// If all tokens (including identifier names, literals) are identical → type-1 (similarity 1.0).
// If structure matches but some identifiers/literals differ → type-2 (similarity 1.0).
func classifyClone(files []TokenizedFile, starts []globalPos, totalTokens int) (string, float64) {
	// Caller guarantees len(starts) >= 2.
	ref := files[starts[0].FileIdx].Tokens[starts[0].Pos : starts[0].Pos+totalTokens]

	for _, s := range starts[1:] {
		other := files[s.FileIdx].Tokens[s.Pos : s.Pos+totalTokens]
		for i := range ref {
			if ref[i].OrigText != other[i].OrigText {
				return domain.CloneType2, 1.0
			}
		}
	}

	return domain.CloneType1, 1.0
}

// hashWindow hashes a token window for structural clone detection.
//
// Hashing rules:
//   - TokIdent, TokNumber, TokString: only Kind is hashed (Text is empty).
//     This means all identifiers hash identically, enabling type-2 detection.
//   - TokKeyword, TokOperator: Kind + Text are hashed, so `if` ≠ `for` and
//     `+` ≠ `-`, preserving structural differences.
func hashWindow(tokens []Token) uint64 {
	return hashTokens(tokens, false)
}

// hashWindowFull hashes a token window using ALL token text (including OrigText).
// Unlike hashWindow (used for type-1/2 detection), this preserves identifier and
// literal differences so that near-miss blocks produce partial mini-window overlap.
func hashWindowFull(tokens []Token) uint64 {
	return hashTokens(tokens, true)
}

// hashTokens is the shared core of hashWindow / hashWindowFull. When
// includeOrig is true, OrigText is also mixed in so identifier/literal
// differences are preserved.
func hashTokens(tokens []Token, includeOrig bool) uint64 {
	h := fnv.New64a()
	for _, t := range tokens {
		_, _ = h.Write([]byte{byte(t.Kind)})
		if t.Text != "" {
			_, _ = h.Write([]byte(t.Text))
		}
		if includeOrig && t.OrigText != "" {
			_, _ = h.Write([]byte(t.OrigText))
		}
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

// loadPreviewLines reads the specified line range from a file on disk.
// This avoids holding all source lines in memory during detection.
func loadPreviewLines(path string, startLine, endLine int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	rawLines := strings.Split(string(data), "\n")
	preview := make([]string, 0, endLine-startLine+1)
	for ln := startLine; ln <= endLine && ln-1 < len(rawLines); ln++ {
		preview = append(preview, rawLines[ln-1])
	}
	return preview
}
