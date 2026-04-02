package services

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

// TokenizedFile holds the lexed representation of one source file.
type TokenizedFile struct {
	Path     string
	Tokens   []Token  // normalized token sequence (no newlines)
	RawLines []string // original source split on "\n" for preview
}

// BuildTokenizedFile tokenizes a source file and returns a TokenizedFile.
func BuildTokenizedFile(path, content string, lang *domain.Language) TokenizedFile {
	return TokenizedFile{
		Path:     path,
		Tokens:   TokenizeFile(content, lang),
		RawLines: strings.Split(content, "\n"),
	}
}

// globalPos identifies a window position across all files.
type globalPos struct {
	FileIdx int
	Pos     int // index into the file's token slice
}

// Detect finds all maximal clone groups in the given token sequences.
// minTokens is the minimum window size.
// Each returned Clone contains all instances of the same structural block.
func Detect(files []TokenizedFile, minTokens int) []domain.Clone {
	if len(files) == 0 || minTokens <= 0 {
		return nil
	}

	// Step 1: for each file, compute the hash of every minTokens-wide window.
	// posToHash[fi][i] = hash of files[fi].Tokens[i:i+minTokens]
	// empty string means window is out of range.
	posToHash := make([][]string, len(files))
	for fi, tf := range files {
		n := len(tf.Tokens)
		hashes := make([]string, n)
		for i := 0; i+minTokens <= n; i++ {
			hashes[i] = hashWindow(tf.Tokens[i : i+minTokens])
		}
		posToHash[fi] = hashes
	}

	// Step 2: build a global hash → positions index.
	hashIndex := make(map[string][]globalPos)
	for fi, hashes := range posToHash {
		for pi, h := range hashes {
			if h == "" {
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
		Hash    string
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

		// Collect group members.
		// Strategy:
		//   1. Cross-file: take the first uncovered position from each distinct file.
		//   2. Same-file fallback: if only one file has this hash, take two non-overlapping
		//      positions from that file (to detect internal duplicates within one file).
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
			// Cross-file: one representative per file (the first uncovered position).
			for _, fps := range byFile {
				starts = append(starts, fps[0])
			}
		} else {
			// Same-file: need two non-overlapping positions (≥ minTokens apart).
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

		// Step 5: extend forward greedily.
		// A group can be extended by 1 if all members' next window share the same hash.
		extLen := 0
		for {
			var nextHash string
			canExtend := true
			for _, s := range starts {
				nextPi := s.Pos + extLen + 1
				if nextPi >= len(posToHash[s.FileIdx]) || posToHash[s.FileIdx][nextPi] == "" {
					canExtend = false
					break
				}
				nh := posToHash[s.FileIdx][nextPi]
				if nextHash == "" {
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

		// Total tokens covered by this maximal block.
		totalTokens := minTokens + extLen

		// Mark all positions in this block as covered.
		for _, s := range starts {
			for k := 0; k <= extLen; k++ {
				covered[globalPos{s.FileIdx, s.Pos + k}] = true
			}
		}

		// Build clone instances with original line ranges and preview lines.
		instances := make([]domain.CloneInstance, 0, len(starts))
		for _, s := range starts {
			toks := files[s.FileIdx].Tokens
			startLine := toks[s.Pos].Line
			endIdx := s.Pos + totalTokens - 1
			if endIdx >= len(toks) {
				endIdx = len(toks) - 1
			}
			endLine := toks[endIdx].Line

			rawLines := files[s.FileIdx].RawLines
			preview := make([]string, 0, endLine-startLine+1)
			for ln := startLine; ln <= endLine && ln-1 < len(rawLines); ln++ {
				preview = append(preview, rawLines[ln-1])
			}

			instances = append(instances, domain.CloneInstance{
				File:      files[s.FileIdx].Path,
				StartLine: startLine,
				EndLine:   endLine,
				Lines:     preview,
			})
		}

		lineCount := 0
		if len(instances) > 0 {
			lineCount = instances[0].EndLine - instances[0].StartLine + 1
		}

		cloneType, similarity := classifyClone(files, starts, totalTokens)

		clones = append(clones, domain.Clone{
			Hash:       cand.Hash,
			Type:       cloneType,
			Similarity: similarity,
			LineCount:  lineCount,
			TokenCount: totalTokens,
			Instances:  instances,
		})
	}

	return clones
}

// classifyClone determines the clone type and similarity by comparing
// the original token text across all instances.
// If all tokens (including identifier names, literals) are identical → type-1 (similarity 1.0).
// If structure matches but some identifiers/literals differ → type-2 (similarity 1.0).
// Type-3 will be set by the fuzzy detector (future).
func classifyClone(files []TokenizedFile, starts []globalPos, totalTokens int) (string, float64) {
	if len(starts) < 2 {
		return domain.CloneType1, 1.0
	}

	// Get the token slice for the first instance as the reference.
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
func hashWindow(tokens []Token) string {
	h := fnv.New64a()
	for _, t := range tokens {
		_, _ = h.Write([]byte{byte(t.Kind)})
		if t.Text != "" {
			_, _ = h.Write([]byte(t.Text))
		}
		_, _ = h.Write([]byte{0}) // separator
	}
	return fmt.Sprintf("%016x", h.Sum64())
}
