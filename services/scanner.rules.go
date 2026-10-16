package services

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/AxeForging/dupehound/domain"
	"github.com/gobwas/glob"
	"gopkg.in/yaml.v3"
)

// Per-path rules: validation of the `rules:` block, which rule a file falls
// under, and detection with a different threshold per file.
//
// Policy (spec 003): a clone is judged by the highest min-tokens — and, for
// type-3, the highest similarity — among the files it spans. Production at 50
// is never reported below 50 because a test copy is at 25, and the result does
// not depend on which instance happens to come first.

// ruleLabelScan names the base settings in output, for clones no rule raised.
const ruleLabelScan = "scan"

var ruleKeys = []string{"name", "paths", "min-tokens", "similarity", "skip"}

// validateRulesNode checks the `rules:` entry of a parsed .dupehound.yml.
// It works on the YAML node so errors can name the line. Unknown keys are
// errors here (unlike under `scan:`) because a mistyped override would
// otherwise silently do nothing.
func validateRulesNode(doc *yaml.Node) error {
	root := doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}
	var rules *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "rules" {
			rules = root.Content[i+1]
		}
	}
	if rules == nil || rules.Tag == "!!null" {
		return nil
	}
	if rules.Kind != yaml.SequenceNode {
		return fmt.Errorf("rules (line %d): must be a list of rules, each with paths and overrides", rules.Line)
	}
	names := map[string]int{}
	for i, item := range rules.Content {
		if err := validateRule(i, item, names); err != nil {
			return err
		}
	}
	return nil
}

func validateRule(i int, item *yaml.Node, names map[string]int) error {
	fail := func(line int, format string, args ...any) error {
		return fmt.Errorf("rules[%d] (line %d): %s", i, line, fmt.Sprintf(format, args...))
	}
	if item.Kind != yaml.MappingNode {
		return fail(item.Line, "must be a mapping with paths and overrides, e.g. {paths: [\"**/tests/**\"], min-tokens: 25}")
	}
	set := map[string]int{} // key → line
	for k := 0; k+1 < len(item.Content); k += 2 {
		key := item.Content[k]
		if !slices.Contains(ruleKeys, key.Value) {
			return fail(key.Line, "unknown key %q; allowed: %s", key.Value, strings.Join(ruleKeys, ", "))
		}
		set[key.Value] = key.Line
	}
	var r domain.Rule
	if err := item.Decode(&r); err != nil {
		return fail(item.Line, "%v", err)
	}
	has := func(key string) bool { _, ok := set[key]; return ok }
	if !has("paths") {
		return fail(item.Line, "paths is required")
	}
	if len(r.Paths) == 0 {
		return fail(set["paths"], "paths is empty; list at least one glob")
	}
	for _, p := range r.Paths {
		if err := validateRulePattern(p); err != nil {
			return fail(set["paths"], "%v", err)
		}
	}
	if has("min-tokens") && r.MinTokens <= 0 {
		return fail(set["min-tokens"], "min-tokens must be greater than 0, got %d", r.MinTokens)
	}
	if has("similarity") && (r.Similarity < 0.5 || r.Similarity > 1.0) {
		return fail(set["similarity"], "similarity must be between 0.50 and 1.00, got %g", r.Similarity)
	}
	if r.Skip && (has("min-tokens") || has("similarity")) {
		return fail(set["skip"], "skip: true cannot be combined with min-tokens or similarity (a skipped file is not scanned)")
	}
	if r.Name != "" {
		if r.Name == ruleLabelScan {
			return fail(set["name"], "name %q is reserved for the scan settings", r.Name)
		}
		if prev, ok := names[r.Name]; ok {
			return fail(set["name"], "name %q is already used by rules[%d]", r.Name, prev)
		}
		names[r.Name] = i
	}
	return nil
}

// validateRulePattern rejects patterns that could never match the intended
// files: empty ones and malformed globs (which matchGlob treats as no match).
func validateRulePattern(p string) error {
	if strings.TrimSpace(p) == "" {
		return fmt.Errorf("empty pattern in paths")
	}
	if _, err := glob.Compile(p, '/'); err != nil {
		return fmt.Errorf("invalid glob %q: %v", p, err)
	}
	if !strings.Contains(p, "**") {
		if _, err := filepath.Match(p, ""); err != nil {
			return fmt.Errorf("invalid glob %q: %v", p, err)
		}
	}
	return nil
}

// rulePatternMatch reports whether pattern matches rel, a slash-separated
// path relative to the scan root. It is matchGlob (the exclude dialect: a
// pattern without "/" also matches the base name) plus one convenience: a
// leading "**/" also matches no directory, so "**/tests/**" covers a tests
// directory at the root too.
func rulePatternMatch(pattern, rel string) bool {
	if matchGlob(pattern, rel) {
		return true
	}
	if rest, ok := strings.CutPrefix(pattern, "**/"); ok && rest != "" {
		return matchGlob(rest, rel)
	}
	return false
}

// fileSettings is what applies to one file: the scan settings or one rule's.
type fileSettings struct {
	label      string  // rule name, "rules[N]", or "scan"
	ruleIdx    int     // position in rules; len(rules) for the scan settings
	minTokens  int     // window and minimum clone size
	similarity float64 // type-3 floor
	skip       bool
}

type ruleSet struct {
	root  string
	rules []domain.Rule
	base  fileSettings
}

// newRuleSet resolves rules against the scan root and the base settings
// (scan block, already overridden by CLI flags).
func newRuleSet(root string, minTokens int, similarity float64, rules []domain.Rule) ruleSet {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return ruleSet{
		root:  root,
		rules: rules,
		base:  fileSettings{label: ruleLabelScan, ruleIdx: len(rules), minTokens: minTokens, similarity: similarity},
	}
}

// relPath is path relative to the scan root, slash-separated. Scanning a
// single file makes the root that file; its base name is used then.
func (rs ruleSet) relPath(path string) string {
	abs := path
	if a, err := filepath.Abs(path); err == nil {
		abs = a
	}
	rel, err := filepath.Rel(rs.root, abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		rel = filepath.Base(abs)
	}
	return filepath.ToSlash(rel)
}

// forFile returns the settings of the first rule matching path, or the base
// settings when none does.
func (rs ruleSet) forFile(path string) fileSettings {
	rel := rs.relPath(path)
	for i, r := range rs.rules {
		for _, p := range r.Paths {
			if !rulePatternMatch(p, rel) {
				continue
			}
			s := rs.base
			s.ruleIdx = i
			s.label = r.Name
			if s.label == "" {
				s.label = fmt.Sprintf("rules[%d]", i)
			}
			if r.MinTokens > 0 {
				s.minTokens = r.MinTokens
			}
			if r.Similarity > 0 {
				s.similarity = r.Similarity
			}
			s.skip = r.Skip
			return s
		}
	}
	return rs.base
}

// detectWithRules runs detection once per distinct min-tokens T, over the
// files whose own threshold is at most T, and keeps from each pass only the
// clones judged at T. A window of T cannot see a smaller clone, so a lower
// threshold needs its own pass; restricting each pass to the files that allow
// T means a pass never reports what a stricter file forbids. Every clone is
// kept by exactly one pass. With one threshold this is a single ordinary
// detection run.
func detectWithRules(files []TokenizedFile, settings []fileSettings, opts DetectOptions) ([]domain.Clone, DetectStats) {
	byPath := make(map[string]fileSettings, len(files))
	var thresholds []int
	for i, tf := range files {
		byPath[tf.Path] = settings[i]
		thresholds = append(thresholds, settings[i].minTokens)
	}
	slices.Sort(thresholds)
	thresholds = slices.Compact(thresholds)

	var all []domain.Clone
	var stats DetectStats
	for _, t := range thresholds {
		var sub []TokenizedFile
		var scope []bool
		sim := 0.0
		for i, tf := range files {
			if settings[i].minTokens > t {
				continue
			}
			sub = append(sub, tf)
			if opts.InScopeFiles != nil {
				scope = append(scope, fileInScope(opts.InScopeFiles, i))
			}
			if sim == 0 || settings[i].similarity < sim {
				sim = settings[i].similarity
			}
		}
		pass := opts
		pass.MinTokens, pass.MinSimilarity, pass.InScopeFiles = t, sim, scope
		if opts.MaxPairs > 0 {
			// The pair budget is for the whole scan, not per pass.
			if left := opts.MaxPairs - stats.EvaluatedPairs; left > 0 {
				pass.MaxPairs = left
			} else {
				pass.MinSimilarity = 1.0 // budget spent: exact detection only
			}
		}
		clones, st := DetectWithOptions(sub, pass)
		stats.CappedPairs = stats.CappedPairs || st.CappedPairs
		stats.BucketTruncated = stats.BucketTruncated || st.BucketTruncated
		stats.MemoryLimited = stats.MemoryLimited || st.MemoryLimited
		stats.EvaluatedPairs += st.EvaluatedPairs

		for _, c := range clones {
			judge, floor := judgeClone(c, byPath)
			if judge.minTokens != t {
				continue // another pass owns it
			}
			if c.Type == domain.CloneType3 && c.Similarity < floor {
				continue
			}
			c.Rule, c.RuleMinTokens = judge.label, judge.minTokens
			all = append(all, c)
		}
	}
	return dropPartialCopies(all), stats
}

// dropPartialCopies removes a clone when another clone with the same
// fingerprint has every one of its instances and more. That happens when a
// lower-threshold pass finds a block among, say, the test files alone and a
// higher pass finds the same block in those files plus a production file: it
// is one finding, reported with all its locations.
func dropPartialCopies(clones []domain.Clone) []domain.Clone {
	type loc struct {
		file       string
		start, end int
	}
	locs := make([]map[loc]bool, len(clones))
	byHash := map[string][]int{}
	for i, c := range clones {
		locs[i] = make(map[loc]bool, len(c.Instances))
		for _, inst := range c.Instances {
			locs[i][loc{inst.File, inst.StartLine, inst.EndLine}] = true
		}
		byHash[c.Hash] = append(byHash[c.Hash], i)
	}
	contained := func(a, b int) bool { // every instance of a is in b, and b has more
		if len(locs[a]) >= len(locs[b]) {
			return false
		}
		for l := range locs[a] {
			if !locs[b][l] {
				return false
			}
		}
		return true
	}
	kept := clones[:0]
	for i, c := range clones {
		if !slices.ContainsFunc(byHash[c.Hash], func(j int) bool { return j != i && contained(i, j) }) {
			kept = append(kept, c)
		}
	}
	return kept
}

// judgeClone returns the settings that judge c — the highest min-tokens among
// its files, the earliest rule on a tie — and the highest similarity floor.
func judgeClone(c domain.Clone, byPath map[string]fileSettings) (fileSettings, float64) {
	var judge fileSettings
	floor := 0.0
	for i, inst := range c.Instances {
		s := byPath[inst.File]
		if i == 0 || cmp.Or(cmp.Compare(s.minTokens, judge.minTokens), cmp.Compare(judge.ruleIdx, s.ruleIdx)) > 0 {
			judge = s
		}
		floor = max(floor, s.similarity)
	}
	return judge, floor
}
