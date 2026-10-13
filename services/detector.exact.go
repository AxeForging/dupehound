package services

import (
	"fmt"
	"hash/fnv"
	"slices"
	"sort"

	"github.com/AxeForging/dupehound/domain"
)

// Type-1/2 detection: rolling window hashes, grouping, greedy extension, classification.

// detectExact finds type-1 and type-2 clones using hash-based sliding windows.
// inScopeFiles, if non-nil, causes the detector to skip clone groups where
// no participating file is in scope (used by --since for diff-aware scanning).
// It also returns the number of in-scope windows it examined, which is what
// sizes the type-3 index.
func detectExact(files []TokenizedFile, minTokens int, inScopeFiles []bool, wholeFile bool) ([]domain.Clone, int) {
	// Step 1: for each file, compute the hash of every minTokens-wide window.
	// Windows that are not fully inside a function body hash to 0 and are skipped.
	posToHash := make([][]uint64, len(files))
	windows := 0
	for fi, tf := range files {
		if tf.compact != nil {
			posToHash[fi] = tf.compact.hashes // computed when the file was compacted
		} else {
			posToHash[fi] = windowHashes(tf, minTokens, wholeFile)
		}
		for _, h := range posToHash[fi] {
			if h != 0 {
				windows++
			}
		}
	}

	// Step 2: build a global hash → positions index, but only for hashes that
	// occur at least twice. Most windows in a codebase are unique, and giving
	// each of them a map entry was the bulk of this function's memory. Sorting
	// a flat copy of the hashes finds the repeated ones at 8 bytes per window.
	repeated := repeatedHashes(posToHash, windows)
	hashIndex := make(map[uint64][]globalPos, len(repeated))
	for fi, hashes := range posToHash {
		for pi, h := range hashes {
			if h == 0 {
				continue
			}
			if _, ok := repeated[h]; ok {
				hashIndex[h] = append(hashIndex[h], globalPos{fi, pi})
			}
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
	tokens := newTokenCache(files)
	covered := make([][]bool, len(files))
	for fi := range files {
		covered[fi] = make([]bool, len(posToHash[fi]))
	}
	var clones []domain.Clone

	for _, cand := range candidates {
		gp := globalPos{cand.FileIdx, cand.Pos}
		if covered[gp.FileIdx][gp.Pos] {
			continue
		}

		allGroup := hashIndex[cand.Hash]

		// Group positions by file.
		byFile := make(map[int][]globalPos)
		for _, p := range allGroup {
			if !covered[p.FileIdx][p.Pos] {
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
					covered[s.FileIdx][s.Pos] = true
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
				covered[s.FileIdx][s.Pos+k] = true
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

		cloneType, similarity := classifyClone(tokens, starts, totalTokens)

		// Fingerprint the FULL extended block (not just the seed window) so the
		// hash is a stable, content-based identity: every instance shares the
		// same normalized structure, so the fingerprint is independent of file
		// order, position, and line numbers. Baseline ratcheting and hash-based
		// suppression rules rely on this stability.
		ref := tokens.of(starts[0].FileIdx)
		fingerprint := hashWindow(ref[starts[0].Pos : starts[0].Pos+totalTokens])

		clones = append(clones, domain.Clone{
			Hash:       fmt.Sprintf("%016x", fingerprint),
			Type:       cloneType,
			Similarity: similarity,
			LineCount:  lineCount,
			TokenCount: totalTokens,
			Instances:  instances,
		})
	}

	return clones, windows
}

// rollingBase is the multiplier of the polynomial window hash. Any odd
// constant with well-mixed bits works; this is the 64-bit golden ratio.
const rollingBase = 0x9E3779B97F4A7C15

// tokenSymbol reduces a token to the 64-bit value detection compares: its kind,
// plus its text for keywords and operators. Identifiers and literals carry no
// text, so they all share one symbol per kind — which is what makes a renamed
// copy hash the same as the original.
func tokenSymbol(t Token) uint64 {
	const offset, prime = 14695981039346656037, 1099511628211
	h := (uint64(offset) ^ uint64(t.Kind)) * prime
	for i := 0; i < len(t.Text); i++ {
		h = (h ^ uint64(t.Text[i])) * prime
	}
	return h
}

// strictSymbol is tokenSymbol for code outside any function body, where
// structure alone says little: a list of fields, of constants, of signatures
// has the same shape in every class.
//
// Identifiers therefore count by name. Literal values still do not — two
// statements that differ only in the strings they pass are the same statement —
// unless exact is set, which the caller does inside a data table: two different
// tables of the same shape (translations, fixtures, config) are not duplicates.
func strictSymbol(t Token, exact bool) uint64 {
	const prime = 1099511628211
	h := tokenSymbol(t) ^ 0x5bd1e9955bd1e995 // never equal to the loose symbol
	if t.Kind != TokIdent && !exact {
		return h
	}
	for i := 0; i < len(t.OrigText); i++ {
		h = (h ^ uint64(t.OrigText[i])) * prime
	}
	return h
}

// windowHashes returns, for every position in tf, the hash of the minTokens
// tokens starting there — or 0 when the window runs off the file or leaves the
// detection scope.
//
// The scope is a single function body by default. With wholeFile it is the
// whole file minus import statements and ignored blocks, and tokens outside
// function bodies are compared by name (see strictSymbol).
//
// It is a rolling hash: each window is derived from the previous one in
// constant time, instead of rehashing all minTokens tokens at every position.
func windowHashes(tf TokenizedFile, minTokens int, wholeFile bool) []uint64 {
	n := len(tf.Tokens)
	hashes := make([]uint64, n)
	if n < minTokens {
		return hashes
	}
	// A hand-built TokenizedFile may carry no masks at all: everything is then
	// in scope and treated as function code.
	hasFuncs := len(tf.InFunc) > 0
	inScope := func(i int) bool {
		if !wholeFile {
			return !hasFuncs || tf.InFunc[i]
		}
		ignored := len(tf.Ignored) > 0 && tf.Ignored[i]
		imported := len(tf.Imports) > 0 && tf.Imports[i]
		return !ignored && !imported
	}
	logic := tf.Logic
	if len(logic) == 0 {
		logic = tf.InFunc
	}
	var inTable []bool // tokens inside a data literal; whole-file mode only
	if wholeFile && len(tf.DataLiterals) > 0 {
		inTable = make([]bool, n)
		for _, span := range tf.DataLiterals {
			for i := span.Start; i <= span.End && i < n; i++ {
				inTable[i] = true
			}
		}
	}
	symbol := func(i int) uint64 {
		if wholeFile && hasFuncs && !logic[i] {
			return strictSymbol(tf.Tokens[i], inTable != nil && inTable[i])
		}
		return tokenSymbol(tf.Tokens[i])
	}
	// topPower is rollingBase^(minTokens-1): the weight of the token leaving
	// the window.
	topPower := uint64(1)
	for i := 1; i < minTokens; i++ {
		topPower *= rollingBase
	}
	var h uint64
	scoped := 0 // tokens of the current window that are in scope
	for i := 0; i < n; i++ {
		if i >= minTokens {
			h -= symbol(i-minTokens) * topPower
			if inScope(i - minTokens) {
				scoped--
			}
		}
		h = h*rollingBase + symbol(i)
		if inScope(i) {
			scoped++
		}
		start := i - minTokens + 1
		if start < 0 || scoped != minTokens {
			continue
		}
		if h == 0 {
			hashes[start] = 1 // 0 is reserved for "no window here"
		} else {
			hashes[start] = h
		}
	}
	return hashes
}

// repeatedHashes returns the set of window hashes that occur at more than one
// position across all files.
func repeatedHashes(posToHash [][]uint64, windows int) map[uint64]struct{} {
	all := make([]uint64, 0, windows)
	for _, hashes := range posToHash {
		for _, h := range hashes {
			if h != 0 {
				all = append(all, h)
			}
		}
	}
	slices.Sort(all)
	repeated := make(map[uint64]struct{})
	for i := 1; i < len(all); i++ {
		if all[i] == all[i-1] {
			repeated[all[i]] = struct{}{}
		}
	}
	return repeated
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
func classifyClone(tokens *tokenCache, starts []globalPos, totalTokens int) (string, float64) {
	// Caller guarantees len(starts) >= 2.
	ref := tokens.of(starts[0].FileIdx)[starts[0].Pos : starts[0].Pos+totalTokens]

	for _, s := range starts[1:] {
		other := tokens.of(s.FileIdx)[s.Pos : s.Pos+totalTokens]
		for i := range ref {
			if ref[i].OrigText != other[i].OrigText {
				return domain.CloneType2, 1.0
			}
		}
	}

	return domain.CloneType1, 1.0
}

// tokenCacheLimit bounds how many re-lexed tokens the cache holds in low-memory
// mode — about 100 MB — before it is emptied and refilled.
const tokenCacheLimit = 2_000_000

// tokenCache hands out a file's tokens. Files held in full answer directly;
// files held as a compact index are re-lexed from disk and kept for a while,
// since the clones of one file tend to be processed together.
type tokenCache struct {
	files  []TokenizedFile
	loaded map[int][]Token
	held   int
}

func newTokenCache(files []TokenizedFile) *tokenCache {
	return &tokenCache{files: files, loaded: make(map[int][]Token)}
}

func (c *tokenCache) of(fileIdx int) []Token {
	tf := c.files[fileIdx]
	if tf.compact == nil {
		return tf.Tokens
	}
	if toks, ok := c.loaded[fileIdx]; ok {
		return toks
	}
	toks := tf.materialized().Tokens
	if len(toks) != tf.numTokens() {
		// The file changed on disk since it was indexed. Pad so positions stay
		// in range; the clone is then classified on what can still be read.
		toks = append(toks, make([]Token, max(tf.numTokens()-len(toks), 0))...)
	}
	if c.held+len(toks) > tokenCacheLimit {
		clear(c.loaded)
		c.held = 0
	}
	c.loaded[fileIdx] = toks
	c.held += len(toks)
	return toks
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
