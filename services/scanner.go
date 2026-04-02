package services

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"

	"github.com/AxeForging/dupehound/domain"
	"github.com/AxeForging/dupehound/helpers"
)

// ScanOptions configures a scan run.
type ScanOptions struct {
	Path     string
	MinLines int
	Exclude  []string
	Language string
}

// windowKey uniquely identifies a clone window location.
type windowKey struct {
	file      string
	startLine int
	endLine   int
}

// ScannerService performs code duplication detection.
type ScannerService struct{}

// NewScannerService creates a new ScannerService.
func NewScannerService() *ScannerService {
	return &ScannerService{}
}

// Scan walks the given path and returns a Report of all detected duplicates.
func (s *ScannerService) Scan(opts ScanOptions) (*domain.Report, error) {
	files, err := collectFiles(opts.Path, opts.Exclude, opts.Language)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, helpers.ErrNoFilesFound
	}

	helpers.Log.Debug().Int("files", len(files)).Msg("collected files")

	// hash → list of instances
	index := make(map[string][]domain.CloneInstance)

	totalFiles := len(files)
	scannedFiles := 0

	for _, f := range files {
		lang := DetectLanguage(f)
		if lang == nil {
			continue
		}

		data, err := os.ReadFile(f)
		if err != nil {
			helpers.Log.Warn().Str("file", f).Err(err).Msg("skipping unreadable file")
			continue
		}

		rawLines := strings.Split(string(data), "\n")
		norm := NormalizeFile(string(data), lang)

		if len(norm.Lines) < opts.MinLines {
			scannedFiles++
			continue
		}

		for i := 0; i+opts.MinLines <= len(norm.Lines); i++ {
			chunk := norm.Lines[i : i+opts.MinLines]
			h := hashChunk(chunk)

			startOriginal := norm.LineNumbers[i]
			endOriginal := norm.LineNumbers[i+opts.MinLines-1]

			// Collect original (unnormalized) lines for display
			origLines := make([]string, 0, endOriginal-startOriginal+1)
			for ln := startOriginal; ln <= endOriginal && ln-1 < len(rawLines); ln++ {
				origLines = append(origLines, rawLines[ln-1])
			}

			index[h] = append(index[h], domain.CloneInstance{
				File:      f,
				StartLine: startOriginal,
				EndLine:   endOriginal,
				Lines:     origLines,
			})
		}

		scannedFiles++
		helpers.Log.Debug().Str("file", f).Msg("scanned")
	}

	clones := buildClones(index, opts.MinLines)
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

// isHiddenOrVendored skips common non-source directories.
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

// hashChunk produces a hex string hash of the given lines.
func hashChunk(lines []string) string {
	h := fnv.New64a()
	for _, l := range lines {
		_, _ = h.Write([]byte(l))
		_, _ = h.Write([]byte("\n"))
	}
	return fmt.Sprintf("%016x", h.Sum64())
}

// buildClones filters the index for entries with 2+ instances and deduplicates
// overlapping windows within the same file.
func buildClones(index map[string][]domain.CloneInstance, minLines int) []domain.Clone {
	var clones []domain.Clone

	for hash, instances := range index {
		if len(instances) < 2 {
			continue
		}
		// Deduplicate: keep only non-overlapping windows per file
		deduped := deduplicateInstances(instances)
		if len(deduped) < 2 {
			continue
		}
		clones = append(clones, domain.Clone{
			Hash:      hash,
			LineCount: minLines,
			Instances: deduped,
		})
	}

	return clones
}

// deduplicateInstances removes overlapping windows in the same file,
// keeping only the first occurrence of each overlapping group.
func deduplicateInstances(instances []domain.CloneInstance) []domain.CloneInstance {
	seen := make(map[string]bool)
	var result []domain.CloneInstance

	for _, inst := range instances {
		key := fmt.Sprintf("%s:%d", inst.File, inst.StartLine)
		if seen[key] {
			continue
		}
		// Mark all lines in this instance's range as seen for this file
		for ln := inst.StartLine; ln <= inst.EndLine; ln++ {
			seen[fmt.Sprintf("%s:%d", inst.File, ln)] = true
		}
		result = append(result, inst)
	}

	return result
}

// countDuplicateLines counts the total number of lines involved in clones.
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
