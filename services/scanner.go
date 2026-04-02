package services

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/AxeForging/dupehound/domain"
	"github.com/AxeForging/dupehound/helpers"
)

// ScanOptions configures a scan run.
type ScanOptions struct {
	Path      string
	MinTokens int
	MinLines  int // deprecated; if MinTokens == 0, converted to MinTokens = MinLines * 10
	Exclude   []string
	Language  string
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

	files, err := collectFiles(opts.Path, opts.Exclude, opts.Language)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, helpers.ErrNoFilesFound
	}

	helpers.Log.Debug().Int("files", len(files)).Msg("collected files")

	totalFiles := len(files)
	var tokenizedFiles []TokenizedFile
	scannedFiles := 0

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

		tf := BuildTokenizedFile(path, string(data), lang)

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

	clones := Detect(tokenizedFiles, minTokens)
	duplicateLines := countDuplicateLines(clones)

	return &domain.Report{
		TotalFiles:     totalFiles,
		ScannedFiles:   scannedFiles,
		TotalClones:    len(clones),
		DuplicateLines: duplicateLines,
		Clones:         clones,
	}, nil
}

// collectFiles walks path and returns files matching the language filter and not excluded.
func collectFiles(root string, exclude []string, langFilter string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, helpers.ErrPathNotFound
	}

	var files []string

	if !info.IsDir() {
		if matchesExcludes(root, exclude) {
			return nil, nil
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

// matchesExcludes returns true if path matches any exclude glob.
func matchesExcludes(path string, exclude []string) bool {
	base := filepath.Base(path)
	for _, pattern := range exclude {
		if ok, _ := filepath.Match(pattern, base); ok {
			return true
		}
		if ok, _ := filepath.Match(pattern, path); ok {
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
