package services

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/AxeForging/dupehound/domain"
	"github.com/AxeForging/dupehound/helpers"
	"github.com/gobwas/glob"
)

const ignoreFileName = ".dupehound-ignore"

// IgnoreRule represents a single line in a .dupehound-ignore file.
type IgnoreRule struct {
	Raw       string // original text
	Hash      string // 8+ hex chars → suppress by clone hash
	PathGlob  string // path glob (may contain **) → suppress all clones touching this path
	FilePath  string // file:start-end → suppress specific line range
	FileStart int
	FileEnd   int
}

// loadIgnoreFile loads .dupehound-ignore from an explicit path, or auto-discovers it
// at scanPath root or CWD.
func loadIgnoreFile(explicit, scanPath string) ([]IgnoreRule, error) {
	candidates := []string{}
	if explicit != "" {
		candidates = append(candidates, explicit)
	}
	if scanPath != "" {
		absPath, err := filepath.Abs(scanPath)
		if err == nil {
			if fi, err := os.Stat(absPath); err == nil && fi.IsDir() {
				candidates = append(candidates, filepath.Join(absPath, ignoreFileName))
			} else {
				candidates = append(candidates, filepath.Join(filepath.Dir(absPath), ignoreFileName))
			}
		}
	}
	cwd, err := os.Getwd()
	if err == nil {
		candidates = append(candidates, filepath.Join(cwd, ignoreFileName))
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return parseIgnoreFile(p)
		}
	}
	return nil, nil
}

// parseIgnoreFile reads and parses a .dupehound-ignore file.
func parseIgnoreFile(path string) ([]IgnoreRule, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var rules []IgnoreRule
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip inline comments.
		if idx := strings.Index(line, " #"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		if line == "" {
			continue
		}

		rule := parseIgnoreLine(line)
		if rule != nil {
			rules = append(rules, *rule)
		}
	}
	return rules, scanner.Err()
}

// parseIgnoreLine parses a single non-comment line into an IgnoreRule.
func parseIgnoreLine(line string) *IgnoreRule {
	rule := &IgnoreRule{Raw: line}

	// file:start-end pattern.
	if colonIdx := strings.LastIndex(line, ":"); colonIdx > 0 {
		maybePath := line[:colonIdx]
		maybeRange := line[colonIdx+1:]
		if dashIdx := strings.Index(maybeRange, "-"); dashIdx > 0 {
			start, err1 := strconv.Atoi(maybeRange[:dashIdx])
			end, err2 := strconv.Atoi(maybeRange[dashIdx+1:])
			if err1 == nil && err2 == nil {
				rule.FilePath = maybePath
				rule.FileStart = start
				rule.FileEnd = end
				return rule
			}
		}
	}

	// Hash pattern: 8+ hex chars, no path separators.
	if len(line) >= 8 && isHexString(line) && !strings.ContainsAny(line, "/*?") {
		rule.Hash = strings.ToLower(line)
		return rule
	}

	// Everything else is a path glob.
	rule.PathGlob = line
	return rule
}

// isHexString returns true if s consists entirely of hex chars.
func isHexString(s string) bool {
	for _, c := range s {
		isDigit := c >= '0' && c <= '9'
		isLowerHex := c >= 'a' && c <= 'f'
		isUpperHex := c >= 'A' && c <= 'F'
		if !isDigit && !isLowerHex && !isUpperHex {
			return false
		}
	}
	return len(s) > 0
}

// applySuppressionRules marks clones as suppressed based on rules.
// Returns the (possibly trimmed) slice and count of suppressed clones.
// If showSuppressed is true, suppressed clones are retained with Suppressed=true.
func applySuppressionRules(clones []domain.Clone, rules []IgnoreRule, showSuppressed bool) ([]domain.Clone, int) {
	suppressed := 0
	result := make([]domain.Clone, 0, len(clones))
	for i := range clones {
		c := clones[i]
		if isSuppressed(c, rules) {
			suppressed++
			if showSuppressed {
				c.Suppressed = true
				result = append(result, c)
			}
		} else {
			result = append(result, c)
		}
	}
	return result, suppressed
}

// isSuppressed returns true if a clone matches any suppress rule.
func isSuppressed(c domain.Clone, rules []IgnoreRule) bool {
	for _, r := range rules {
		if r.Hash != "" {
			// Match by hash prefix.
			if strings.HasPrefix(strings.ToLower(c.Hash), r.Hash) {
				return true
			}
		} else if r.PathGlob != "" {
			// Match if any instance path matches the glob.
			g, err := glob.Compile(r.PathGlob, '/')
			if err != nil {
				helpers.Log.Warn().Str("pattern", r.PathGlob).Err(err).Msg("invalid glob in .dupehound-ignore")
				continue
			}
			for _, inst := range c.Instances {
				normPath := filepath.ToSlash(inst.File)
				if g.Match(normPath) || g.Match(filepath.Base(normPath)) {
					return true
				}
			}
		} else if r.FilePath != "" {
			// Match if any instance overlaps the file:line range.
			for _, inst := range c.Instances {
				if matchesFilePath(inst.File, r.FilePath) &&
					rangesOverlap(inst.StartLine, inst.EndLine, r.FileStart, r.FileEnd) {
					return true
				}
			}
		}
	}
	return false
}

// matchesFilePath checks if inst file path ends with or equals ruleFile.
func matchesFilePath(instFile, ruleFile string) bool {
	normInst := filepath.ToSlash(instFile)
	normRule := filepath.ToSlash(ruleFile)
	return normInst == normRule ||
		strings.HasSuffix(normInst, "/"+normRule) ||
		strings.HasSuffix(normInst, normRule)
}

// rangesOverlap returns true if [a1,a2] and [b1,b2] overlap.
func rangesOverlap(a1, a2, b1, b2 int) bool {
	return a1 <= b2 && b1 <= a2
}
