package services

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"github.com/AxeForging/dupehound/domain"
	"github.com/AxeForging/dupehound/helpers"
	"github.com/gobwas/glob"
)

// Which files a scan reads: tree walk, include/exclude globs, language by extension.

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
