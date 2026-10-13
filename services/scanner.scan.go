package services

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/AxeForging/dupehound/domain"
	"github.com/AxeForging/dupehound/helpers"
)

// The scan pipeline: collect, tokenize, detect, annotate, report.

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
	MaxMemory      int64   // memory budget in bytes (0 = no budget); see memoryBudget
	WholeFile      bool    // detect type-1/2 clones across the whole file, not only inside one function body
	MinCloneLines  int     // drop clones spanning fewer lines than this (0 = no floor)
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
	var smallFiles []TokenizedFile // below min-tokens; kept only for dead-code analysis
	scannedFiles := 0
	skippedFiles := 0
	fileLineCount := make(map[string]int) // path → total lines

	budget := newMemoryBudget(opts.MaxMemory)
	defer budget.release()

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
			if opts.DeadCode {
				// Too small to hold a clone, but not too small to hold a
				// function nobody calls.
				smallFiles = append(smallFiles, tf)
			}
			helpers.Log.Debug().Str("file", path).Int("tokens", len(tf.Tokens)).Msg("too few tokens, skipping")
			continue
		}

		// Stay inside the budget. When holding every token would exceed it,
		// fall back to low-memory mode: files are kept as a compact index and
		// re-read only where a clone needs their text. When even that does
		// not fit, stop before the machine does.
		budget.add(len(data), len(tf.Tokens))
		if budget.exceeded() && !budget.compact {
			budget.compact = true
			for i := range tokenizedFiles {
				tokenizedFiles[i] = tokenizedFiles[i].compacted(minTokens, opts.WholeFile, DetectLanguage(tokenizedFiles[i].Path))
			}
			helpers.Log.Warn().Str("budget", formatBytes(opts.MaxMemory)).
				Msg("switching to low-memory mode to stay within --max-memory: type-1/2 detection is unaffected, type-3 is skipped")
		}
		if budget.exceeded() {
			return nil, fmt.Errorf("%w: needs more than %s after %d of %d files (raise --max-memory, or narrow the scan with --include / --exclude)",
				helpers.ErrMemoryBudget, formatBytes(opts.MaxMemory), scannedFiles+1, len(files))
		}
		if budget.compact {
			tf = tf.compacted(minTokens, opts.WholeFile, lang)
		}

		tokenizedFiles = append(tokenizedFiles, tf)
		scannedFiles++
		helpers.Log.Debug().Str("file", path).Int("tokens", tf.numTokens()).Msg("tokenized")
	}

	helpers.Log.Debug().
		Str("estimate", formatBytes(budget.estimate())).
		Str("budget", formatBytes(opts.MaxMemory)).
		Msg("memory estimate for exact detection")

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
		MaxFuzzyBytes: budget.remaining(),
		WholeFile:     opts.WholeFile,
		InScopeFiles:  inScopeFiles,
	})
	clones = deduplicateOverlapping(clones)
	if opts.MinCloneLines > 0 {
		// Applied before anything is counted or annotated, so every total in
		// the report describes the clones it actually lists.
		clones = clonesWithout(clones, func(c domain.Clone) bool { return c.LineCount < opts.MinCloneLines })
	}

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

	// Dead code detection. Definitions come from the files in scope; references
	// are gathered from the whole tree (see scanDeadCode).
	var deadFuncs []domain.DeadFunc
	if opts.DeadCode {
		defFiles := append(slices.Clone(tokenizedFiles), smallFiles...)
		sort.SliceStable(defFiles, func(i, j int) bool { return defFiles[i].Path < defFiles[j].Path })
		deadFuncs = scanDeadCode(opts.Path, defFiles, opts.MaxFileSize)
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
