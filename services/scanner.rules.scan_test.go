package services

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

// The fixture follows the case that motivated per-path rules (a Python uv
// workspace): small test fakes copied between test files — and once into
// production — that a 50-token threshold cannot see, next to a production
// clone well above it.
const (
	ruleFixtureFake = `class EchoModel:
    def generate(self, prompt, options):
        text = prompt.strip()
        if options.upper:
            text = text.upper()
        return {"text": text, "tokens": len(text)}
`
	ruleFixtureProd = `def load_settings(path, env):
    raw = read_file(path)
    data = parse_yaml(raw)
    for key, value in env.items():
        if key.startswith("APP_"):
            data[key[4:].lower()] = value
    validate(data)
    log.info("loaded settings", path=path, keys=len(data))
    return data
`
)

type ruleSpec struct {
	name       string
	paths      []string
	minTokens  int
	similarity float64
	skip       bool
}

func testRules(specs ...ruleSpec) []domain.Rule {
	rules := make([]domain.Rule, 0, len(specs))
	for _, s := range specs {
		rules = append(rules, domain.Rule{Name: s.name, Paths: s.paths, MinTokens: s.minTokens, Similarity: s.similarity, Skip: s.skip})
	}
	return rules
}

func writeRuleFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func ruleFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeRuleFile(t, dir, "tests/test_a.py", ruleFixtureFake+"\ndef test_a():\n    assert True\n")
	writeRuleFile(t, dir, "tests/test_b.py", ruleFixtureFake)
	writeRuleFile(t, dir, "src/app.py", "import x\n\n"+ruleFixtureFake+"\n"+ruleFixtureProd)
	writeRuleFile(t, dir, "src/other.py", ruleFixtureProd)
	return dir
}

func scanRules(t *testing.T, dir string, minTokens int, rules []domain.Rule) *domain.Report {
	t.Helper()
	r, err := NewScannerService().Scan(ScanOptions{Path: dir, MinTokens: minTokens, WholeFile: true, Rules: rules})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return r
}

// relFiles returns the instance files of c relative to dir, sorted.
func relFiles(t *testing.T, dir string, c domain.Clone) []string {
	t.Helper()
	var out []string
	for _, inst := range c.Instances {
		rel, err := filepath.Rel(dir, inst.File)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, filepath.ToSlash(rel))
	}
	slices.Sort(out)
	return out
}

func cloneTouching(t *testing.T, dir string, r *domain.Report, file string) (domain.Clone, bool) {
	t.Helper()
	for _, c := range r.Clones {
		if slices.Contains(relFiles(t, dir, c), file) {
			return c, true
		}
	}
	return domain.Clone{}, false
}

// cloneSpanning returns the clone whose instances lie in exactly files.
func cloneSpanning(t *testing.T, dir string, r *domain.Report, files ...string) (domain.Clone, bool) {
	t.Helper()
	slices.Sort(files)
	for _, c := range r.Clones {
		if reflect.DeepEqual(relFiles(t, dir, c), files) {
			return c, true
		}
	}
	return domain.Clone{}, false
}

var testsRule = ruleSpec{name: "tests", paths: []string{"**/tests/**", "**/*_test.py"}, minTokens: 25}

// The motivating case: at 50 tokens the copied fake is invisible; a tests
// rule at 25 finds it, among the test files only, while production keeps 50.
func TestRules_LowerTestThresholdFindsTestOnlyClone(t *testing.T) {
	dir := ruleFixture(t)

	before := scanRules(t, dir, 50, nil)
	if _, ok := cloneTouching(t, dir, before, "tests/test_a.py"); ok {
		t.Fatal("fixture: the fake must be below 50 tokens")
	}

	after := scanRules(t, dir, 50, testRules(testsRule))
	fake, ok := cloneTouching(t, dir, after, "tests/test_a.py")
	if !ok {
		t.Fatalf("tests rule at 25 did not report the copied fake; clones: %+v", after.Clones)
	}
	if got, want := relFiles(t, dir, fake), []string{"tests/test_a.py", "tests/test_b.py"}; !reflect.DeepEqual(got, want) {
		t.Errorf("fake instances = %v, want %v (the production copy is judged at 50)", got, want)
	}
	if fake.Rule != "tests" || fake.RuleMinTokens != 25 {
		t.Errorf("fake judged by %q/%d, want tests/25", fake.Rule, fake.RuleMinTokens)
	}

	prod, ok := cloneTouching(t, dir, after, "src/other.py")
	if !ok {
		t.Fatal("production clone lost")
	}
	if prod.Rule != ruleLabelScan || prod.RuleMinTokens != 50 {
		t.Errorf("production clone judged by %q/%d, want scan/50", prod.Rule, prod.RuleMinTokens)
	}
}

// Barrier: a clone exactly at the rule's threshold is reported, one token
// more threshold and it is not.
func TestRules_ThresholdBarrier(t *testing.T) {
	dir := ruleFixture(t)
	// Size of the test-only clone, measured the way the tests rule sees it.
	probe := scanRules(t, dir, 50, testRules(ruleSpec{name: "tests", paths: testsRule.paths, minTokens: 10}))
	fake, ok := cloneTouching(t, dir, probe, "tests/test_a.py")
	if !ok {
		t.Fatal("probe found no test clone")
	}
	n := fake.TokenCount

	at := scanRules(t, dir, 50, testRules(ruleSpec{name: "tests", paths: testsRule.paths, minTokens: n}))
	if c, ok := cloneTouching(t, dir, at, "tests/test_a.py"); !ok || c.TokenCount != n {
		t.Errorf("clone of exactly %d tokens not reported at min-tokens %d", n, n)
	}
	above := scanRules(t, dir, 50, testRules(ruleSpec{name: "tests", paths: testsRule.paths, minTokens: n + 1}))
	if _, ok := cloneTouching(t, dir, above, "tests/test_a.py"); ok {
		t.Errorf("clone of %d tokens reported at min-tokens %d", n, n+1)
	}
}

// Cross-rule policy: a clone is judged by the highest min-tokens among the
// files it spans, and that rule is reported.
func TestRules_CrossRuleCloneJudgedByHighestThreshold(t *testing.T) {
	dir := ruleFixture(t)

	// Base 40 is under the fake's size: the copy in src/app.py joins the
	// clone, and the clone is judged by scan (40), the higher of 25 and 40.
	r := scanRules(t, dir, 40, testRules(testsRule))
	fake, ok := cloneSpanning(t, dir, r, "src/app.py", "tests/test_a.py", "tests/test_b.py")
	if !ok {
		t.Fatalf("want one fake clone spanning tests and src/app.py; clones: %+v", r.Clones)
	}
	if fake.Rule != ruleLabelScan || fake.RuleMinTokens != 40 {
		t.Errorf("cross-rule clone judged by %q/%d, want scan/40", fake.Rule, fake.RuleMinTokens)
	}
	// The tests pass found the same block among the test files alone; it is
	// the same finding and must not be reported twice.
	if _, dup := cloneSpanning(t, dir, r, "tests/test_a.py", "tests/test_b.py"); dup {
		t.Error("the test-only part of the cross-rule clone is reported as a second clone")
	}

	// Same files, a looser production rule on the other side: still the
	// higher threshold decides, whichever side of the clone it is on.
	r = scanRules(t, dir, 50, testRules(testsRule, ruleSpec{name: "app", paths: []string{"src/app.py"}, minTokens: 30}))
	fake, ok = cloneSpanning(t, dir, r, "src/app.py", "tests/test_a.py", "tests/test_b.py")
	if !ok || fake.Rule != "app" || fake.RuleMinTokens != 30 {
		t.Errorf("want 3-instance clone judged by app/30, got %+v", fake)
	}
}

// A higher threshold on one file (migrations) raises the bar for every clone
// that touches it.
func TestRules_HigherThresholdRule(t *testing.T) {
	dir := ruleFixture(t)
	prodSize := func() int {
		c, ok := cloneTouching(t, dir, scanRules(t, dir, 50, nil), "src/other.py")
		if !ok {
			t.Fatal("fixture: no production clone")
		}
		return c.TokenCount
	}()

	loose := ruleSpec{name: "migrations", paths: []string{"src/other.py"}, minTokens: prodSize + 1}
	if _, ok := cloneTouching(t, dir, scanRules(t, dir, 50, testRules(loose)), "src/other.py"); ok {
		t.Errorf("clone of %d tokens reported although src/other.py requires %d", prodSize, prodSize+1)
	}
	loose.minTokens = prodSize
	c, ok := cloneTouching(t, dir, scanRules(t, dir, 50, testRules(loose)), "src/other.py")
	if !ok || c.Rule != "migrations" || c.RuleMinTokens != prodSize {
		t.Errorf("want clone judged by migrations/%d, got ok=%v %+v", prodSize, ok, c)
	}
}

func TestRules_SkipExcludesFilesEntirely(t *testing.T) {
	dir := ruleFixture(t)
	r := scanRules(t, dir, 25, testRules(ruleSpec{name: "gen", paths: []string{"tests/**"}, skip: true}))
	for _, c := range r.Clones {
		for _, f := range relFiles(t, dir, c) {
			if strings.HasPrefix(f, "tests/") {
				t.Errorf("skipped file %s reported in a clone", f)
			}
		}
	}
	if r.SkippedByRule != 2 {
		t.Errorf("SkippedByRule = %d, want 2", r.SkippedByRule)
	}
	if r.TotalFiles != 2 || r.ScannedFiles != 2 {
		t.Errorf("skipped files counted: total %d scanned %d, want 2/2", r.TotalFiles, r.ScannedFiles)
	}
}

func TestRules_FirstMatchingRuleWins(t *testing.T) {
	dir := ruleFixture(t)
	// tests/** matches first, so the later skip of every .py file does not
	// apply to test files.
	r := scanRules(t, dir, 50, testRules(testsRule, ruleSpec{name: "py", paths: []string{"*.py"}, skip: true}))
	if _, ok := cloneTouching(t, dir, r, "tests/test_a.py"); !ok {
		t.Error("test files fell through to the later skip rule")
	}
	if r.SkippedByRule != 2 {
		t.Errorf("SkippedByRule = %d, want 2 (src/*.py)", r.SkippedByRule)
	}
	// Reversed order: the skip matches everything first.
	r2, err := NewScannerService().Scan(ScanOptions{Path: dir, MinTokens: 50, Rules: testRules(ruleSpec{name: "py", paths: []string{"*.py"}, skip: true}, testsRule)})
	if err == nil && len(r2.Clones) != 0 {
		t.Errorf("skip rule listed first should exclude every file, got %d clones", len(r2.Clones))
	}
}

// Adding rules must not change the fingerprint of a clone judged by the base
// settings: baselines recorded before the rules keep matching.
func TestRules_BaselineFingerprintUnchangedByRules(t *testing.T) {
	dir := ruleFixture(t)
	before, ok := cloneTouching(t, dir, scanRules(t, dir, 50, nil), "src/other.py")
	if !ok {
		t.Fatal("fixture: no production clone")
	}
	after, ok := cloneTouching(t, dir, scanRules(t, dir, 50, testRules(testsRule, ruleSpec{name: "gen", paths: []string{"**/generated/**"}, skip: true})), "src/other.py")
	if !ok {
		t.Fatal("production clone lost with rules")
	}
	if before.Hash != after.Hash || before.TokenCount != after.TokenCount || !reflect.DeepEqual(relFiles(t, dir, before), relFiles(t, dir, after)) {
		t.Errorf("production clone changed: before %s/%d, after %s/%d", before.Hash, before.TokenCount, after.Hash, after.TokenCount)
	}

	// And the baseline file written with rules still knows it.
	bl := filepath.Join(t.TempDir(), "baseline.json")
	if err := WriteBaseline(bl, []domain.Clone{before}, 50); err != nil {
		t.Fatal(err)
	}
	r, err := NewScannerService().Scan(ScanOptions{Path: dir, MinTokens: 50, WholeFile: true, Baseline: bl, Rules: testRules(testsRule)})
	if err != nil {
		t.Fatal(err)
	}
	if r.BaselineKnown != 1 || r.BaselineNew != 1 {
		t.Errorf("baseline known/new = %d/%d, want 1 (production) / 1 (test fake)", r.BaselineKnown, r.BaselineNew)
	}
}

// Without rules the scan is the one that ran before rules existed: no rule
// annotations and an identical report.
func TestRules_EmptyRulesMatchesPlainScan(t *testing.T) {
	dir := ruleFixture(t)
	plain, err := json.Marshal(scanRules(t, dir, 25, nil))
	if err != nil {
		t.Fatal(err)
	}
	empty, err := json.Marshal(scanRules(t, dir, 25, []domain.Rule{}))
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != string(empty) {
		t.Errorf("empty rules changed the report:\n%s\n%s", plain, empty)
	}
	if strings.Contains(string(plain), `"rule"`) {
		t.Error("report without rules carries rule annotations")
	}
}

// A rule whose threshold equals the base, matching every file, must give
// the same clones as no rules at all — only annotated.
func TestRules_RuleEqualToBaseFindsSameClones(t *testing.T) {
	dir := ruleFixture(t)
	plain := scanRules(t, dir, 30, nil)
	ruled := scanRules(t, dir, 30, testRules(ruleSpec{name: "all", paths: []string{"**"}, minTokens: 30}))
	if len(plain.Clones) != len(ruled.Clones) {
		t.Fatalf("clone count %d vs %d", len(plain.Clones), len(ruled.Clones))
	}
	for i := range plain.Clones {
		p, r := plain.Clones[i], ruled.Clones[i]
		if r.Rule != "all" {
			t.Errorf("clone %d judged by %q, want all", i, r.Rule)
		}
		r.Rule, r.RuleMinTokens = "", 0
		if !reflect.DeepEqual(p, r) {
			t.Errorf("clone %d differs:\n%+v\n%+v", i, p, r)
		}
	}
}

// type-3 near-miss pair in test files: the similarity floor is per rule.
func typeThreeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body := `def build_report(rows, opts):
    total = 0
    for row in rows:
        total += row.amount
        if row.flagged:
            audit(row, reason="flag")
%s    summary = {"total": total, "count": len(rows)}
    emit(summary, channel=opts.channel)
    archive(summary, bucket=opts.bucket)
    return summary
`
	writeRuleFile(t, dir, "tests/test_one.py", strings.Replace(body, "%s", "", 1))
	writeRuleFile(t, dir, "tests/test_two.py", strings.Replace(body, "%s", "        notify_owner(row.owner, row.amount, \"flag raised\")\n", 1))
	return dir
}

func countType3Report(r *domain.Report) int { return countType3(r.Clones) }

func TestRules_SimilarityFloorPerRule(t *testing.T) {
	dir := typeThreeFixture(t)
	scan := func(base float64, rules []domain.Rule) *domain.Report {
		r, err := NewScannerService().Scan(ScanOptions{Path: dir, MinTokens: 40, MinSimilarity: base, WholeFile: true, Rules: rules})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if countType3Report(scan(0.7, nil)) == 0 {
		t.Fatal("fixture: no type-3 clone at 0.70")
	}
	if countType3Report(scan(0.95, nil)) != 0 {
		t.Fatal("fixture: type-3 clone at 0.95")
	}
	strict := testRules(ruleSpec{name: "tests", paths: []string{"tests/**"}, similarity: 0.95})
	if n := countType3Report(scan(0.7, strict)); n != 0 {
		t.Errorf("stricter tests similarity still reported %d type-3 clones", n)
	}
	loose := testRules(ruleSpec{name: "tests", paths: []string{"tests/**"}, similarity: 0.7})
	r := scan(0.95, loose)
	if countType3Report(r) == 0 {
		t.Fatal("looser tests similarity found no type-3 clone")
	}
	for _, c := range r.Clones {
		if c.Type == domain.CloneType3 && c.Rule != "tests" {
			t.Errorf("type-3 clone judged by %q, want tests", c.Rule)
		}
	}
}

// Low-memory mode holds window hashes for one window size; a pass with
// another size must still find the same clones.
func TestRules_LowMemoryModeFindsTheSameClones(t *testing.T) {
	dir := ruleFixture(t)
	rules := testRules(testsRule)
	full := scanRules(t, dir, 50, rules)
	fullNeed, compactNeed := scanMemoryNeeds(t, dir)
	r, err := NewScannerService().Scan(ScanOptions{
		Path: dir, MinTokens: 50, MinSimilarity: 1.0, WholeFile: true, Rules: rules,
		MaxMemory: (fullNeed + compactNeed) / 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	exactOnly := func(cs []domain.Clone) []string {
		var out []string
		for _, c := range cs {
			if c.Type != domain.CloneType3 {
				out = append(out, c.Hash+"/"+c.Rule)
			}
		}
		slices.Sort(out)
		return out
	}
	if got, want := exactOnly(r.Clones), exactOnly(full.Clones); !reflect.DeepEqual(got, want) {
		t.Errorf("low-memory clones %v, want %v", got, want)
	}
}

// --since and --staged filter the merged clones as before; only the scope
// plumbing per pass is new. A file outside the diff must not produce clones.
func TestRules_InScopeFilesRespectedPerPass(t *testing.T) {
	dir := ruleFixture(t)
	files := []TokenizedFile{}
	var settings []fileSettings
	rs := newRuleSet(dir, 50, 0.7, testRules(testsRule))
	for _, rel := range []string{"src/app.py", "src/other.py", "tests/test_a.py", "tests/test_b.py"} {
		path := filepath.Join(dir, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, BuildTokenizedFileWithIgnore(path, string(data), DetectLanguage(path), nil))
		settings = append(settings, rs.forFile(path))
	}
	// Only src/other.py changed: the test-only fake must not be reported.
	clones, _ := detectWithRules(files, settings, DetectOptions{MinTokens: 50, MinSimilarity: 0.7, WholeFile: true, InScopeFiles: []bool{false, true, false, false}})
	for _, c := range clones {
		if slices.Contains(relFiles(t, dir, c), "tests/test_a.py") {
			t.Errorf("clone outside the diff reported: %v", relFiles(t, dir, c))
		}
	}
	if len(clones) == 0 {
		t.Error("in-scope production clone lost")
	}
}
