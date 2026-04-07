package services

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
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

	helpers.Log.Debug().Int("files", len(files)).Msg("collected files")

	// Load suppression rules.
	ignoreRules, _ := loadIgnoreFile(opts.IgnoreFile, opts.Path)

	totalFiles := len(files)
	var tokenizedFiles []TokenizedFile
	scannedFiles := 0
	skippedFiles := 0
	fileLineCount := make(map[string]int) // path → total lines

	for _, path := range files {
		lang := DetectLanguage(path)
		if lang == nil {
			continue
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
	clones := DetectWithOptions(tokenizedFiles, DetectOptions{
		MinTokens:     minTokens,
		MinSimilarity: minSimilarity,
		MaxBucket:     opts.MaxBucket,
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

	// Diff-aware scanning: filter to clones touching changed lines.
	var newClones []domain.Clone
	var sinceDiffFiles int
	if opts.Since != "" {
		absPath, err := filepath.Abs(opts.Path)
		if err != nil {
			absPath = opts.Path
		}
		changedLines, changedFiles, diffErr := getChangedLines(absPath, opts.Since)
		if diffErr != nil {
			helpers.Log.Warn().Err(diffErr).Str("ref", opts.Since).Msg("could not get git diff, falling back to full scan")
		} else {
			sinceDiffFiles = changedFiles
			newClones = filterClonesInDiff(clones, changedLines)
		}
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
	var deadFuncs []domain.DeadFunc
	if opts.DeadCode {
		deadFuncs = findDeadFunctions(tokenizedFiles)
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
		TotalFiles:       totalFiles,
		ScannedFiles:     scannedFiles,
		SkippedFiles:     skippedFiles,
		TotalClones:      len(clones),
		TotalLines:       totalLines,
		DuplicateLines:   duplicateLines,
		DuplicationPct:   duplicationPct,
		SuppressedClones: suppressedCount,
		FileStats:        fileStats,
		Clones:           filterSuppressed(clones, opts.ShowSuppressed),
		DeadFunctions:    deadFuncs,
	}
	if opts.Since != "" && sinceDiffFiles >= 0 {
		report.SinceDiffRef = opts.Since
		report.SinceDiffFiles = sinceDiffFiles
		report.NewClones = filterSuppressed(newClones, false)
	}

	return report, nil
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

// deduplicateOverlapping removes clones whose instances are fully contained
// within a larger clone's instances in the same files. This prevents the top
// clones list from showing the same region multiple times at different sizes.
func deduplicateOverlapping(clones []domain.Clone) []domain.Clone {
	// Build a set of all instance ranges per clone, sorted largest first.
	sort.Slice(clones, func(i, j int) bool {
		return clones[i].LineCount > clones[j].LineCount
	})

	// For each kept clone, record its covered ranges.
	type rangeKey struct {
		file  string
		start int
		end   int
	}
	kept := make([]domain.Clone, 0, len(clones))
	coveredRanges := make([]rangeKey, 0, len(clones)*2)

	for _, c := range clones {
		allSubsumed := len(c.Instances) > 0
		for _, inst := range c.Instances {
			subsumed := false
			for _, r := range coveredRanges {
				if inst.File == r.file && inst.StartLine >= r.start && inst.EndLine <= r.end {
					subsumed = true
					break
				}
			}
			if !subsumed {
				allSubsumed = false
				break
			}
		}
		if allSubsumed {
			continue
		}
		kept = append(kept, c)
		for _, inst := range c.Instances {
			coveredRanges = append(coveredRanges, rangeKey{inst.File, inst.StartLine, inst.EndLine})
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
		return stats[i].DuplicationPct > stats[j].DuplicationPct
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
