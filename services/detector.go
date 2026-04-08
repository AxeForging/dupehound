package services

import (
	"fmt"
	"hash/fnv"
	"math"
	"os"
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
	Tokens  []Token // normalized token sequence (no newlines)
	InFunc  []bool  // per-token: true if inside a function/method body
	Ignored []bool  // per-token: true if in a dupehound:ignore annotated block
}

// BuildTokenizedFile tokenizes a source file and returns a TokenizedFile.
func BuildTokenizedFile(path, content string, lang *domain.Language) TokenizedFile {
	tokens := TokenizeFile(content, lang)
	return TokenizedFile{
		Path:   path,
		Tokens: tokens,
		InFunc: markFunctionBodies(tokens, lang),
	}
}

// BuildTokenizedFileWithIgnore tokenizes a source file, applying inline suppression
// markers from both the source (dupehound:ignore comments) and the ignore rules.
func BuildTokenizedFileWithIgnore(path, content string, lang *domain.Language, rules []IgnoreRule) TokenizedFile {
	tokens := TokenizeFileWithIgnore(content, lang)
	inFunc := markFunctionBodies(tokens, lang)
	ignored := markIgnoredBlocks(tokens, inFunc)
	// Zero out InFunc for ignored tokens so detection skips them.
	for i, ign := range ignored {
		if ign {
			inFunc[i] = false
		}
	}
	return TokenizedFile{
		Path:    path,
		Tokens:  tokens,
		InFunc:  inFunc,
		Ignored: ignored,
	}
}

// globalPos identifies a window position across all files.
type globalPos struct {
	FileIdx int
	Pos     int // index into the file's token slice
}

// Detect finds all clone groups (type-1, type-2, and type-3) in the given token sequences.
// minTokens is the minimum window size. minSimilarity is the Jaccard threshold for type-3
// detection (set to 1.0 to disable type-3).
// DetectOptions holds parameters for clone detection.
type DetectOptions struct {
	MinTokens     int
	MinSimilarity float64
	MaxBucket     int // max blocks per fuzzy mini-hash bucket (0 = default 500)
	// MaxPairs, if > 0, hard-caps the number of fuzzy candidate pairs the
	// detector is allowed to evaluate. When the cap is exceeded, fuzzy
	// detection is aborted and the partial result so far is discarded; the
	// caller is expected to lower --max-bucket or set --similarity 1.0.
	// 0 means no cap (original behavior).
	MaxPairs int
	// InScopeFiles, when non-nil, restricts detection to clones where at
	// least one instance is in a file marked true. Indexed by the position
	// of the file in the `files` slice passed to DetectWithOptions. The
	// fuzzy detector also uses this to skip candidate pairs where neither
	// block is in scope, which is the main cost win for diff-aware scanning.
	// Nil means "all files in scope" (original behavior).
	InScopeFiles []bool
}

func Detect(files []TokenizedFile, minTokens int, minSimilarity float64) []domain.Clone {
	return DetectWithOptions(files, DetectOptions{
		MinTokens:     minTokens,
		MinSimilarity: minSimilarity,
	})
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

// DetectWithOptions finds all clone groups with full control over detection parameters.
func DetectWithOptions(files []TokenizedFile, opts DetectOptions) []domain.Clone {
	if len(files) == 0 || opts.MinTokens <= 0 {
		return nil
	}

	exact := detectExact(files, opts.MinTokens, opts.InScopeFiles)

	if opts.MinSimilarity >= 1.0 {
		return exact
	}

	maxBucket := opts.MaxBucket
	if maxBucket <= 0 {
		maxBucket = 5000
	}

	fuzzy := detectFuzzy(files, opts.MinTokens, opts.MinSimilarity, maxBucket, exact, opts.InScopeFiles, opts.MaxPairs)
	return append(exact, fuzzy...)
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

		for _, s := range starts {
			for k := 0; k <= extLen; k++ {
				covered[globalPos{s.FileIdx, s.Pos + k}] = true
			}
		}

		instances := buildInstances(files, starts, totalTokens)

		lineCount := 0
		if len(instances) > 0 {
			lineCount = instances[0].EndLine - instances[0].StartLine + 1
		}

		cloneType, similarity := classifyClone(files, starts, totalTokens)

		clones = append(clones, domain.Clone{
			Hash:       fmt.Sprintf("%016x", cand.Hash),
			Type:       cloneType,
			Similarity: similarity,
			LineCount:  lineCount,
			TokenCount: totalTokens,
			Instances:  instances,
		})
	}

	return clones
}

// detectFuzzy finds type-3 near-miss clones using mini-window Jaccard similarity.
// It skips blocks already covered by exact clones.
// Requires minTokens >= 10 to produce meaningful mini-windows; returns nil otherwise.
// inScopeFiles, if non-nil, causes candidate pair construction to skip pairs
// where neither block is in a file marked in scope (used by --since).
// maxPairs, if > 0, aborts fuzzy detection when the candidate pair count
// would exceed the cap; the partial result is discarded and the function
// returns nil. Caller is responsible for surfacing this to the user via a log.
func detectFuzzy(files []TokenizedFile, minTokens int, threshold float64, maxBucket int, exactClones []domain.Clone, inScopeFiles []bool, maxPairs int) []domain.Clone {
	if minTokens < 10 {
		return nil
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
		miniSet   map[uint64]bool
		startLine int
		endLine   int
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

			// Build mini-window hash set for this block.
			// Uses hashWindowFull (includes OrigText) so that different
			// identifiers/literals produce different mini-hashes,
			// enabling meaningful Jaccard comparison.
			miniSet := make(map[uint64]bool)
			for i := pos; i+miniSize <= pos+minTokens; i++ {
				h := hashWindowFull(tf.Tokens[i : i+miniSize])
				if h != 0 {
					miniSet[h] = true
				}
			}

			idx := len(blocks)
			blocks = append(blocks, blockInfo{
				key:       blockKey{fi, pos},
				miniSet:   miniSet,
				startLine: startLine,
				endLine:   endLine,
			})

			for h := range miniSet {
				miniIndex[h] = append(miniIndex[h], idx)
			}
		}
	}

	// Find candidate pairs: blocks sharing ≥1 mini-window hash.
	type pair struct {
		a, b int
	}
	seen := make(map[pair]bool)
	var pairs []pair

	truncatedBuckets := 0
	totalSkipped := 0
	for _, indices := range miniIndex {
		if len(indices) < 2 {
			continue
		}
		bucket := indices
		if len(bucket) > maxBucket {
			truncatedBuckets++
			totalSkipped += len(bucket) - maxBucket
			bucket = bucket[:maxBucket]
		}
		for i := 0; i < len(bucket); i++ {
			for j := i + 1; j < len(bucket); j++ {
				a, b := bucket[i], bucket[j]
				ba, bb := blocks[a], blocks[b]
				// Diff-aware scope filter: skip pairs where neither block
				// touches a file in scope. This is the dominant cost win
				// for --since on a large repo because it cuts the pair set
				// (and the dedup map) before any Jaccard work.
				if inScopeFiles != nil &&
					!fileInScope(inScopeFiles, ba.key.fileIdx) &&
					!fileInScope(inScopeFiles, bb.key.fileIdx) {
					continue
				}
				// Skip same-file overlapping blocks.
				if ba.key.fileIdx == bb.key.fileIdx {
					dist := ba.key.pos - bb.key.pos
					if dist < 0 {
						dist = -dist
					}
					if dist < minTokens {
						continue
					}
				}
				p := pair{a, b}
				if a > b {
					p = pair{b, a}
				}
				if !seen[p] {
					seen[p] = true
					pairs = append(pairs, p)
					if maxPairs > 0 && len(pairs) > maxPairs {
						helpers.Log.Warn().
							Int("max_pairs", maxPairs).
							Int("buckets_processed", len(seen)).
							Msg("fuzzy detector aborted: --max-pairs cap exceeded; skipping type-3 detection (lower --max-bucket, raise --max-pairs, or use --similarity 1.0)")
						return nil
					}
				}
			}
		}
	}

	// Aggregate the per-bucket truncation events into a single warning so a
	// hot codebase doesn't spam the log with hundreds of identical lines.
	if truncatedBuckets > 0 {
		helpers.Log.Warn().
			Int("buckets_truncated", truncatedBuckets).
			Int("blocks_skipped", totalSkipped).
			Int("max_bucket", maxBucket).
			Msg("fuzzy bucket truncation: some near-miss clones may not be reported (raise --max-bucket to reduce)")
	}

	// Evaluate Jaccard similarity for each candidate pair.
	type fuzzyClone struct {
		aIdx, bIdx int
		similarity float64
	}
	var fuzzyMatches []fuzzyClone

	if len(pairs) > 1000 {
		helpers.Log.Info().
			Int("pairs", len(pairs)).
			Msg("evaluating fuzzy candidate pairs — this may take a moment on large codebases")
	}

	// Throttle progress logs to at most ~20 emissions across the whole loop,
	// regardless of pair count. On a fast machine the previous "every 10k"
	// produced thousands of lines per second.
	progressStep := len(pairs) / 20
	if progressStep < 10000 {
		progressStep = 10000
	}

	for i, p := range pairs {
		if len(pairs) > 10000 && i > 0 && i%progressStep == 0 {
			helpers.Log.Info().
				Int("evaluated", i).
				Int("total", len(pairs)).
				Int("matches_so_far", len(fuzzyMatches)).
				Msg("fuzzy detection progress")
		}
		ba, bb := blocks[p.a], blocks[p.b]
		sim := jaccardSimilarity(ba.miniSet, bb.miniSet)
		if sim >= threshold && sim < 1.0 {
			fuzzyMatches = append(fuzzyMatches, fuzzyClone{p.a, p.b, sim})
		}
	}

	// Sort by similarity descending to prioritize best matches.
	sort.Slice(fuzzyMatches, func(i, j int) bool {
		return fuzzyMatches[i].similarity > fuzzyMatches[j].similarity
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

		// Build a combined hash for the pair.
		h := fnv.New64a()
		_, _ = fmt.Fprintf(h, "%d:%d:%d:%d", ba.key.fileIdx, ba.key.pos, bb.key.fileIdx, bb.key.pos)

		clones = append(clones, domain.Clone{
			Hash:       fmt.Sprintf("%016x", h.Sum64()),
			Type:       domain.CloneType3,
			Similarity: math.Round(fm.similarity*100) / 100, // round to 2 decimals
			LineCount:  lineCount,
			TokenCount: minTokens,
			Instances:  instances,
		})
	}

	return clones
}

// buildInstances creates CloneInstance structs with lazy-loaded preview lines.
func buildInstances(files []TokenizedFile, starts []globalPos, totalTokens int) []domain.CloneInstance {
	instances := make([]domain.CloneInstance, 0, len(starts))
	for _, s := range starts {
		instances = append(instances, buildSingleInstance(files, s.FileIdx, s.Pos, totalTokens))
	}
	return instances
}

// buildSingleInstance creates a single CloneInstance with lazy preview.
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
		Lines:     preview,
	}
}

// jaccardSimilarity computes |A ∩ B| / |A ∪ B| for two hash sets.
func jaccardSimilarity(a, b map[uint64]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	intersection := 0
	for h := range a {
		if b[h] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
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
	h := fnv.New64a()
	for _, t := range tokens {
		_, _ = h.Write([]byte{byte(t.Kind)})
		if t.Text != "" {
			_, _ = h.Write([]byte(t.Text))
		}
		_, _ = h.Write([]byte{0}) // separator
	}
	return h.Sum64()
}

// hashWindowFull hashes a token window using ALL token text (including OrigText).
// Unlike hashWindow (used for type-1/2 detection), this preserves identifier and
// literal differences so that near-miss blocks produce partial mini-window overlap.
func hashWindowFull(tokens []Token) uint64 {
	h := fnv.New64a()
	for _, t := range tokens {
		_, _ = h.Write([]byte{byte(t.Kind)})
		if t.Text != "" {
			_, _ = h.Write([]byte(t.Text))
		}
		if t.OrigText != "" {
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
