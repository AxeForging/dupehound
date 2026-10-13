package services

import (
	"github.com/AxeForging/dupehound/domain"
	"github.com/AxeForging/dupehound/helpers"
)

// Clone detection entry point: options, completeness stats, and the exact-then-fuzzy pipeline.

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
	// WholeFile widens type-1/2 detection from single function bodies to the
	// whole file, so a clone may span several functions or sit outside any —
	// a copied class made of small methods, block-style tests, a script.
	// Outside function bodies identifiers must match by name; see
	// strictSymbol. Type-3 detection stays within function bodies.
	WholeFile bool
	// MaxFuzzyBytes, if > 0, is the memory type-3 detection may use. When its
	// index would not fit, type-3 detection is skipped as a whole — a
	// predictable result, unlike analysing whichever files happened to come
	// first — and the result is marked partial. 0 means no limit.
	MaxFuzzyBytes int64
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
	// MemoryLimited is true when type-3 detection was skipped because its
	// index would not fit in --max-memory.
	MemoryLimited bool
	// EvaluatedPairs counts the fuzzy candidate pairs actually compared.
	EvaluatedPairs int
}

// Partial reports whether any guard reduced type-3 coverage.
func (s DetectStats) Partial() bool { return s.CappedPairs || s.BucketTruncated || s.MemoryLimited }

// Reason renders a human-readable explanation for a partial result, or "".
func (s DetectStats) Reason() string {
	switch {
	case s.MemoryLimited:
		return "type-3 detection skipped: it would not fit in --max-memory; type-1/2 results are complete"
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

	exact, windows := detectExact(files, opts.MinTokens, opts.InScopeFiles, opts.WholeFile)

	if opts.MinSimilarity >= 1.0 {
		return exact, DetectStats{}
	}

	if need := fuzzyIndexBytes(windows, opts.MinTokens); opts.MaxFuzzyBytes > 0 && need > opts.MaxFuzzyBytes {
		helpers.Log.Warn().
			Str("needed", formatBytes(need)).
			Str("available", formatBytes(opts.MaxFuzzyBytes)).
			Msg("type-3 detection skipped: it would not fit in --max-memory (type-1/2 results are complete; raise --max-memory to include type-3)")
		return exact, DetectStats{MemoryLimited: true}
	}

	maxBucket := opts.MaxBucket
	if maxBucket <= 0 {
		maxBucket = 5000
	}

	fuzzy, stats := detectFuzzy(files, opts.MinTokens, opts.MinSimilarity, maxBucket, exact, opts.InScopeFiles, opts.MaxPairs)
	return append(exact, fuzzy...), stats
}
