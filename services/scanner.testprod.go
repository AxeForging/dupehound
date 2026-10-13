package services

import (
	"path/filepath"
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

// isTestFile returns true if the given file path is a test file.
// Uses language-specific heuristics based on filename and path.
func isTestFile(path string) bool {
	base := filepath.Base(path)
	normPath := filepath.ToSlash(path)

	// Go: _test.go suffix.
	if strings.HasSuffix(base, "_test.go") {
		return true
	}

	// JS/TS: .spec. or .test. in filename, or path contains __tests__/.
	ext := strings.ToLower(filepath.Ext(base))
	if ext == ".js" || ext == ".mjs" || ext == ".cjs" || ext == ".ts" || ext == ".tsx" {
		if strings.Contains(base, ".spec.") || strings.Contains(base, ".test.") {
			return true
		}
		if strings.Contains(normPath, "__tests__/") {
			return true
		}
	}

	// Python: test_ prefix or _test.py suffix, or path contains /tests/.
	if ext == ".py" {
		if strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") {
			return true
		}
		if strings.Contains(normPath, "/tests/") {
			return true
		}
	}

	// Java/Kotlin: path contains src/test/.
	if ext == ".java" || ext == ".kt" || ext == ".kts" {
		if strings.Contains(normPath, "src/test/") {
			return true
		}
	}

	// Ruby: path contains spec/ or _spec.rb suffix.
	if ext == ".rb" {
		if strings.Contains(normPath, "spec/") || strings.HasSuffix(base, "_spec.rb") {
			return true
		}
	}

	// General fallback: filename contains "test" or "spec".
	lowerBase := strings.ToLower(base)
	if strings.Contains(lowerBase, "test") || strings.Contains(lowerBase, "spec") {
		return true
	}

	return false
}

// annotateTestProdSpan sets TestProdSpan on each clone where instances span both
// test and non-test files. Also sets IsTest on each instance.
func annotateTestProdSpan(clones []domain.Clone) {
	for i := range clones {
		hasTest := false
		hasProd := false
		for j := range clones[i].Instances {
			inst := &clones[i].Instances[j]
			inst.IsTest = isTestFile(inst.File)
			if inst.IsTest {
				hasTest = true
			} else {
				hasProd = true
			}
		}
		clones[i].TestProdSpan = hasTest && hasProd
	}
}
