package services

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/AxeForging/dupehound/domain"
	"github.com/AxeForging/dupehound/helpers"
	"github.com/gobwas/glob"
)

// ScanOptions configures a scan run.
type ScanOptions struct {
	Path           string
	MinTokens      int
	MinLines       int // deprecated; if MinTokens == 0, converted to MinTokens = MinLines * 10
	Exclude        []string
	Include        []string // if non-empty, only files matching at least one pattern are scanned
	Language       string
	MinSimilarity  float64 // minimum Jaccard similarity for type-3 detection (0.50–1.00)
	MaxBucket      int     // max blocks per fuzzy bucket (0 = default 500)
	Staged         bool    // only report clones involving git-staged files
	Top            int     // max clones to show in text/md output (0 = all)
	Since          string  // git ref for diff-aware scanning
	ShowSuppressed bool    // include suppressed clones in output
	DeadCode       bool    // enable dead function detection
	GitChurn       bool    // annotate clones with git churn scores
	ChurnDays      int     // number of days for git churn window (default 90)
	IgnoreFile     string  // path to .dupehound-ignore file (auto-discovered if empty)
	MaxFiles       int     // hard cap on collected files (0 = no cap); fail-fast safety net
	MaxPairs       int     // runaway backstop on fuzzy pairs (0 = no cap); caps type-3 to a partial result past the limit
	MaxFileSize    int64   // per-file size cap in bytes (0 = no cap); oversized files are skipped before being read
	ScanGenerated  bool    // when true, do NOT skip machine-generated files (default: skip them)
	Baseline       string  // path to a baseline file: known clones become recorded debt, only new ones fail
	WriteBaseline  string  // path to write a new baseline capturing the current clones as accepted debt
}

// ScannerService performs code duplication detection.
type ScannerService struct{}

// NewScannerService creates a new ScannerService.
func NewScannerService() *ScannerService {
	return &ScannerService{}
}

// Scan walks the given path, tokenizes all source files, and returns a Report of all
// detected clones. Uses the token-based detector for type-1 and type-2 clone detection.
func (s *ScannerService) Scan(opts ScanOptions) (*domain.Report, error) {
	minTokens := opts.MinTokens
	if minTokens <= 0 && opts.MinLines > 0 {
		minTokens = opts.MinLines * 10
	}
	if minTokens <= 0 {
		minTokens = 50 // sensible default
	}

	files, err := collectFiles(opts.Path, opts.Exclude, opts.Include, opts.Language)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, helpers.ErrNoFilesFound
	}

	// Hard cap: refuse to scan absurdly large file sets up front. The cap is
	// off by default; users opt in via --max-files when running unattended on
	// monorepos so a misconfigured --path can't OOM or hang the runner.
	if opts.MaxFiles > 0 && len(files) > opts.MaxFiles {
		helpers.Log.Error().
			Int("collected", len(files)).
			Int("max_files", opts.MaxFiles).
			Msg("file collection exceeded --max-files cap")
		return nil, fmt.Errorf("%w: collected %d files, cap is %d (narrow with --include / --exclude or raise --max-files)",
			helpers.ErrTooManyFiles, len(files), opts.MaxFiles)
	}

	helpers.Log.Debug().Int("files", len(files)).Msg("collected files")

	// Load suppression rules.
	ignoreRules, _ := loadIgnoreFile(opts.IgnoreFile, opts.Path)

	totalFiles := len(files)
	var tokenizedFiles []TokenizedFile
	scannedFiles := 0
	skippedFiles := 0
	fileLineCount := make(map[string]int) // path → total lines

	skippedLarge := 0
	for _, path := range files {
		lang := DetectLanguage(path)
		if lang == nil {
			continue
		}

		// Size guard BEFORE reading: a single giant (usually minified or
		// generated) file would otherwise be pulled fully into memory and
		// tokenized before any other cap could help.
		if opts.MaxFileSize > 0 {
			if fi, statErr := os.Stat(path); statErr == nil && fi.Size() > opts.MaxFileSize {
				helpers.Log.Warn().Str("file", path).Int64("size_bytes", fi.Size()).Int64("max_file_size", opts.MaxFileSize).
					Msg("skipping oversized file (raise --max-file-size to include)")
				skippedLarge++
				continue
			}
		}

		data, err := os.ReadFile(path)
		if err != nil {
			helpers.Log.Warn().Str("file", path).Err(err).Msg("skipping unreadable file")
			continue
		}

		// Binary file detection: check first 512 bytes for null byte.
		if isBinaryData(data) {
			helpers.Log.Debug().Str("file", path).Msg("skipping binary file")
			skippedFiles++
			continue
		}

		content := string(data)

		// Skip machine-generated files by default — they are duplicated by
		// construction and drown out real, refactorable clones. Opt back in
		// with --scan-generated.
		if !opts.ScanGenerated && isGeneratedFile(path, content) {
			helpers.Log.Debug().Str("file", path).Msg("skipping generated file")
			skippedFiles++
			continue
		}

		fileLineCount[path] = countLines(content)

		tf := BuildTokenizedFileWithIgnore(path, content, lang, ignoreRules)

		// Skip files with too few tokens to form even one window.
		if len(tf.Tokens) < minTokens {
			scannedFiles++
			helpers.Log.Debug().Str("file", path).Int("tokens", len(tf.Tokens)).Msg("too few tokens, skipping")
			continue
		}

		tokenizedFiles = append(tokenizedFiles, tf)
		scannedFiles++
		helpers.Log.Debug().Str("file", path).Int("tokens", len(tf.Tokens)).Msg("tokenized")
	}

	minSimilarity := opts.MinSimilarity
	if minSimilarity <= 0 {
		minSimilarity = 0.70
	}

	// Diff-aware: resolve the changed-files set BEFORE detection so the
	// detector can skip clones whose instances are all in unchanged files.
	// We also keep the line ranges around to refine the report afterwards
	// with line-precise NewClones data.
	//
	// Path normalization: getChangedLines returns the changed-file map keyed
	// by absolute paths, but tokenized files carry whatever path form the
	// walker produced (relative when --path was relative). We rekey the map
	// so both the scope filter and the post-detection NewClones filter work
	// regardless of how the user passed --path. This was a silent bug pre-fix:
	// `dupehound scan --path . --since main` would always report 0 clones.
	var changedLines map[string][]changedLineRange
	var sinceDiffFiles int
	var inScopeFiles []bool
	if opts.Since != "" {
		absPath, absErr := filepath.Abs(opts.Path)
		if absErr != nil {
			absPath = opts.Path
		}
		cl, cf, diffErr := getChangedLines(absPath, opts.Since)
		if diffErr != nil {
			helpers.Log.Warn().Err(diffErr).Str("ref", opts.Since).Msg("could not get git diff, falling back to full scan")
		} else {
			// Rekey by the same form used in tokenizedFiles[i].Path so the
			// downstream lookups (scope filter AND NewClones filter) match.
			changedLines = make(map[string][]changedLineRange, len(cl))
			tfByAbs := make(map[string]string, len(tokenizedFiles))
			for _, tf := range tokenizedFiles {
				abs, err := filepath.Abs(tf.Path)
				if err != nil {
					abs = tf.Path
				}
				tfByAbs[filepath.Clean(abs)] = tf.Path
			}
			for absKey, ranges := range cl {
				if origPath, ok := tfByAbs[filepath.Clean(absKey)]; ok {
					changedLines[origPath] = ranges
				}
			}
			sinceDiffFiles = cf
			inScopeFiles = make([]bool, len(tokenizedFiles))
			for i, tf := range tokenizedFiles {
				if _, ok := changedLines[tf.Path]; ok {
					inScopeFiles[i] = true
				}
			}
			inScope := 0
			for _, b := range inScopeFiles {
				if b {
					inScope++
				}
			}
			helpers.Log.Info().
				Int("changed_files", cf).
				Int("in_scope_tokenized", inScope).
				Int("total_tokenized", len(tokenizedFiles)).
				Msg("diff-aware scope")
		}
	}

	clones, detectStats := DetectWithOptions(tokenizedFiles, DetectOptions{
		MinTokens:     minTokens,
		MinSimilarity: minSimilarity,
		MaxBucket:     opts.MaxBucket,
		MaxPairs:      opts.MaxPairs,
		InScopeFiles:  inScopeFiles,
	})
	clones = deduplicateOverlapping(clones)

	// Annotate test↔prod spans.
	annotateTestProdSpan(clones)

	// Apply suppression rules from .dupehound-ignore.
	var suppressedCount int
	if len(ignoreRules) > 0 {
		clones, suppressedCount = applySuppressionRules(clones, ignoreRules, opts.ShowSuppressed)
	}

	// When --staged is active, filter to clones touching staged files.
	if opts.Staged {
		absPath, err := filepath.Abs(opts.Path)
		if err != nil {
			absPath = opts.Path
		}
		staged, err := StagedFiles(absPath)
		if err != nil {
			return nil, err
		}
		if len(staged) == 0 {
			helpers.Log.Info().Msg("no staged files found")
			clones = nil
		} else {
			helpers.Log.Info().Int("staged_files", len(staged)).Msg("filtering clones to staged files")
			stagedSet := make(map[string]bool, len(staged))
			for _, f := range staged {
				stagedSet[f] = true
			}
			clones = filterStagedClones(clones, stagedSet)
		}
	}

	// Insight annotations: clone scope (same-file / cross-file / cross-dir)
	// and refactor savings ((instances-1) × line_count).
	annotateInsights(clones)

	// Baseline ratchet: mark clones recorded in the baseline as known debt.
	// Only clones NOT in the baseline (or with grown instance counts) are
	// "new" and fail the scan. Load errors are hard errors — silently
	// scanning without the ratchet would let new duplication through.
	var baselineKnown, baselineNew int
	if opts.Baseline != "" {
		bl, blErr := LoadBaseline(opts.Baseline)
		if blErr != nil {
			return nil, blErr
		}
		baselineKnown, baselineNew = ApplyBaseline(clones, bl)
		helpers.Log.Info().
			Str("baseline", opts.Baseline).
			Int("known", baselineKnown).
			Int("new", baselineNew).
			Msg("baseline applied")
	}

	// Write a new baseline capturing the current state as accepted debt.
	if opts.WriteBaseline != "" {
		if wbErr := WriteBaseline(opts.WriteBaseline, clones, minTokens); wbErr != nil {
			return nil, wbErr
		}
		helpers.Log.Info().Str("baseline", opts.WriteBaseline).Int("clones", len(clones)).Msg("baseline written")
	}

	// Diff-aware scanning: refine to clones whose instances overlap actual
	// changed line ranges. The file-level filter has already run upfront in
	// the detector; this is the line-precise pass that produces NewClones.
	var newClones []domain.Clone
	if opts.Since != "" && changedLines != nil {
		newClones = filterClonesInDiff(clones, changedLines)
	}

	// Git churn annotation.
	if opts.GitChurn {
		absPath, err := filepath.Abs(opts.Path)
		if err != nil {
			absPath = opts.Path
		}
		churnDays := opts.ChurnDays
		if churnDays <= 0 {
			churnDays = 90
		}
		annotateGitChurn(clones, absPath, churnDays)
		// Re-sort by churn score.
		sortByChurn(clones)
	}

	// Dead code detection.
	//
	// The heuristic needs a *broader* file set than the in-scope tokenized
	// files to avoid false positives: if the user excluded test files via
	// --exclude or .dupehound.yml, a function called only from a test would
	// otherwise be reported as dead. Re-collect with no excludes (and with
	// --include disabled) to build a usage-only background set.
	var deadFuncs []domain.DeadFunc
	if opts.DeadCode {
		usageFiles := tokenizedFiles
		if len(opts.Exclude) > 0 || len(opts.Include) > 0 {
			extraTokenized := buildUsageBackground(opts.Path, opts.Language, ignoreRules, tokenizedFiles, opts.MaxFileSize)
			if len(extraTokenized) > 0 {
				usageFiles = append(append([]TokenizedFile{}, tokenizedFiles...), extraTokenized...)
				helpers.Log.Debug().
					Int("in_scope", len(tokenizedFiles)).
					Int("background", len(extraTokenized)).
					Msg("dead-code: tokenized extra files for usage background")
			}
		}
		deadFuncs = findDeadFunctions(tokenizedFiles, usageFiles)
	}

	duplicateLines := countDuplicateLines(clones)

	totalLines := 0
	for _, lc := range fileLineCount {
		totalLines += lc
	}

	var duplicationPct float64
	if totalLines > 0 {
		duplicationPct = math.Round(float64(duplicateLines)/float64(totalLines)*1000) / 10
	}

	fileStats := buildFileStats(clones, fileLineCount)

	report := &domain.Report{
		TotalFiles:        totalFiles,
		ScannedFiles:      scannedFiles,
		SkippedFiles:      skippedFiles,
		SkippedLargeFiles: skippedLarge,
		TotalClones:       len(clones),
		TotalLines:        totalLines,
		DuplicateLines:    duplicateLines,
		DuplicationPct:    duplicationPct,
		SavedLines:        totalSavedLines(clones),
		SuppressedClones:  suppressedCount,
		Partial:           detectStats.Partial(),
		PartialReason:     detectStats.Reason(),
		FileStats:         fileStats,
		Clones:            filterBaselineKnown(filterSuppressed(clones, opts.ShowSuppressed), opts.ShowSuppressed),
		DeadFunctions:     deadFuncs,
	}
	if opts.Baseline != "" {
		report.BaselineFile = opts.Baseline
		report.BaselineKnown = baselineKnown
		report.BaselineNew = baselineNew
	}
	if opts.Since != "" && sinceDiffFiles >= 0 {
		report.SinceDiffRef = opts.Since
		report.SinceDiffFiles = sinceDiffFiles
		report.NewClones = filterSuppressed(newClones, false)
	}

	return report, nil
}

// buildUsageBackground walks the scan path with no exclude/include filters
// and tokenizes any source files that aren't already in the in-scope set.
// The returned slice is meant to be appended to the in-scope tokenized files
// before passing to findDeadFunctions, so that excluded test files (and any
// other excluded code) still contribute their identifier-usage information
// to the dead-code analysis. Binary files are skipped, just like the main
// collection path.
func buildUsageBackground(scanPath, language string, ignoreRules []IgnoreRule, inScope []TokenizedFile, maxFileSize int64) []TokenizedFile {
	allFiles, err := collectFiles(scanPath, nil, nil, language)
	if err != nil || len(allFiles) == 0 {
		return nil
	}
	have := make(map[string]bool, len(inScope))
	for _, tf := range inScope {
		have[tf.Path] = true
	}
	var extra []TokenizedFile
	for _, path := range allFiles {
		if have[path] {
			continue
		}
		lang := DetectLanguage(path)
		if lang == nil {
			continue
		}
		if maxFileSize > 0 {
			if fi, statErr := os.Stat(path); statErr == nil && fi.Size() > maxFileSize {
				continue
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if isBinaryData(data) {
			continue
		}
		extra = append(extra, BuildTokenizedFileWithIgnore(path, string(data), lang, ignoreRules))
	}
	return extra
}

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
	result := make([]domain.Clone, 0, len(clones))
	for _, c := range clones {
		if !c.Baseline {
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
	result := make([]domain.Clone, 0, len(clones))
	for _, c := range clones {
		if !c.Suppressed {
			result = append(result, c)
		}
	}
	return result
}

// isBinaryData returns true if the given data slice contains a null byte in the first 512 bytes.
func isBinaryData(data []byte) bool {
	checkLen := 512
	if len(data) < checkLen {
		checkLen = len(data)
	}
	return bytes.IndexByte(data[:checkLen], 0) >= 0
}

// generatedNameSuffixes are filename patterns that almost universally denote
// machine-generated source. Generated code is duplicated by construction (one
// template, many emissions), so it inflates duplication numbers and buries
// real, refactorable clones. These are skipped by default — like vendor/ and
// node_modules/ — and re-enabled with --scan-generated. The list spans the
// ecosystems dupehound supports; anything not covered here is still caught by
// the language-agnostic content marker below, which is the real workhorse.
var generatedNameSuffixes = []string{
	// protobuf / gRPC across languages
	".pb.go", ".pb.gw.go", // Go
	"_pb2.py", "_pb2_grpc.py", "_pb2.pyi", // Python
	".pb.cc", ".pb.h", // C/C++
	".pb.swift",                                                // Swift
	".pb.dart", ".pbenum.dart", ".pbgrpc.dart", ".pbjson.dart", // Dart
	// Go conventions
	"_gen.go", ".gen.go", "_string.go", ".generated.go",
	// JS / TS codegen (graphql-codegen, openapi, etc.)
	".generated.ts", ".generated.tsx", ".generated.js",
	// C# / .NET
	".designer.cs", ".g.cs", ".g.i.cs", ".generated.cs",
	// Dart / Flutter build_runner
	".g.dart", ".freezed.dart", ".gr.dart", ".config.dart",
}

// generatedContentRE matches the in-file marker emitted by the vast majority of
// code generators, regardless of language. It is intentionally broad and
// case-insensitive so it catches the conventions of every ecosystem dupehound
// supports: Go's `Code generated … DO NOT EDIT`, the `@generated` tag used by
// Relay / GraphQL-codegen / SignedSource (JS/TS), .NET's `<auto-generated>`,
// Dart's `GENERATED CODE - DO NOT MODIFY BY HAND`, protobuf banners, etc. Only
// the first few KB are scanned, so these high-precision phrases sitting in a
// header almost never collide with hand-written code.
// Bare "do not edit" is deliberately excluded — it appears in hand-written
// comments often enough to cause false skips. Every alternative below pairs
// with explicit generation intent, so it only fires on real generated headers.
var generatedContentRE = regexp.MustCompile(`(?i)` + strings.Join([]string{
	`code generated`,
	`do not modify by hand`,
	`@generated`,
	`@autogenerated`,
	`<auto-?generated`,
	`auto-?generated (?:file|code|by)`,
	`automatically generated`,
	`generated code`,
	`generated by`,
	`this (?:file|code) (?:is|was) (?:auto(?:matically)?[- ])?generated`,
}, "|"))

// isGeneratedFile reports whether a file is machine-generated — by filename
// suffix (cheap, language-spanning) or by the standard in-file marker, scanned
// over the first few KB only since generators always place it at the top.
func isGeneratedFile(path, content string) bool {
	lower := strings.ToLower(path)
	for _, suf := range generatedNameSuffixes {
		if strings.HasSuffix(lower, suf) {
			return true
		}
	}
	head := content
	if len(head) > 4096 {
		head = head[:4096]
	}
	return generatedContentRE.MatchString(head)
}

// collectFiles walks path and returns files matching the language filter, include patterns, and not excluded.
func collectFiles(root string, exclude []string, include []string, langFilter string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, helpers.ErrPathNotFound
	}

	var files []string

	if !info.IsDir() {
		if matchesExcludes(root, exclude) {
			return nil, nil
		}
		if !matchesIncludes(root, include) {
			return nil, helpers.ErrNoFilesFound
		}
		lang := DetectLanguage(root)
		if lang == nil {
			return nil, helpers.ErrNoFilesFound
		}
		if langFilter != "" && lang.Name != strings.ToLower(langFilter) {
			return nil, helpers.ErrNoFilesFound
		}
		return []string{root}, nil
	}

	err = filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() {
			if path != root && isHiddenOrVendored(fi.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if matchesExcludes(path, exclude) {
			return nil
		}
		if !matchesIncludes(path, include) {
			return nil
		}
		lang := DetectLanguage(path)
		if lang == nil {
			return nil
		}
		if langFilter != "" && lang.Name != strings.ToLower(langFilter) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files, err
}

// isHiddenOrVendored returns true for directories that should be skipped.
func isHiddenOrVendored(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	skip := map[string]bool{
		"vendor": true, "node_modules": true, "dist": true,
		"build": true, "target": true, "__pycache__": true,
		"testdata": true,
	}
	return skip[name]
}

// matchGlob matches path against a glob pattern using gobwas/glob for ** support.
// Falls back to filepath.Match when the pattern contains no **.
func matchGlob(pattern, path string) bool {
	if strings.Contains(pattern, "**") {
		// ** patterns must match the full (normalized) path.
		g, err := glob.Compile(pattern, '/')
		if err != nil {
			return false
		}
		return g.Match(path)
	}
	// No **, use stdlib filepath.Match on both base name and full path so that
	// plain patterns like "*.go" or "*_test.go" work regardless of how the path
	// was supplied.
	base := filepath.Base(path)
	if ok, _ := filepath.Match(pattern, base); ok {
		return true
	}
	if ok, _ := filepath.Match(pattern, path); ok {
		return true
	}
	return false
}

// matchesExcludes returns true if path matches any exclude glob (supports **).
func matchesExcludes(path string, exclude []string) bool {
	// Normalize to forward slashes for glob matching.
	normPath := filepath.ToSlash(path)
	for _, pattern := range exclude {
		if matchGlob(pattern, normPath) {
			return true
		}
	}
	return false
}

// matchesIncludes returns true if include is empty or path matches at least one include pattern.
func matchesIncludes(path string, include []string) bool {
	if len(include) == 0 {
		return true
	}
	normPath := filepath.ToSlash(path)
	for _, pattern := range include {
		if matchGlob(pattern, normPath) {
			return true
		}
	}
	return false
}

// DetectLanguage returns the Language for a file path, or nil if unsupported.
func DetectLanguage(path string) *domain.Language {
	ext := strings.ToLower(filepath.Ext(path))
	for i := range domain.SupportedLanguages {
		for _, e := range domain.SupportedLanguages[i].Extensions {
			if e == ext {
				return &domain.SupportedLanguages[i]
			}
		}
	}
	return nil
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
	sort.Slice(clones, func(i, j int) bool {
		if clones[i].LineCount != clones[j].LineCount {
			return clones[i].LineCount > clones[j].LineCount
		}
		return clones[i].Hash < clones[j].Hash
	})

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

// countLines returns the number of lines in a string.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if s[len(s)-1] != '\n' {
		n++
	}
	return n
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

	sort.Slice(stats, func(i, j int) bool {
		if stats[i].DuplicationPct != stats[j].DuplicationPct {
			return stats[i].DuplicationPct > stats[j].DuplicationPct
		}
		return stats[i].File < stats[j].File // total order for stable output
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
