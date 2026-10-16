package services

import (
	"strings"
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

func ruleReport(rule string, minTokens int) *domain.Report {
	c := domain.Clone{
		Hash: "00000000000000aa", Type: domain.CloneType1, Similarity: 1, LineCount: 6, TokenCount: 30,
		Rule: rule, RuleMinTokens: minTokens,
		Instances: []domain.CloneInstance{
			{File: "/repo/tests/test_a.py", StartLine: 1, EndLine: 6, Lines: []string{"x = 1"}},
			{File: "/repo/tests/test_b.py", StartLine: 1, EndLine: 6, Lines: []string{"x = 1"}},
		},
	}
	return &domain.Report{TotalFiles: 2, ScannedFiles: 2, TotalClones: 1, TotalLines: 12, DuplicateLines: 12, Clones: []domain.Clone{c}}
}

// Every format says which rule judged a clone, so thresholds can be tuned
// from any report.
func TestFormatReport_EveryFormatShowsTheRule(t *testing.T) {
	want := map[string]string{
		"text":   "[rule: tests ≥25]",
		"md":     "rule tests ≥25",
		"json":   `"rule": "tests"`,
		"sarif":  `"rule": "tests"`,
		"github": "rule tests ≥25",
	}
	for format, fragment := range want {
		out, err := FormatReport(ruleReport("tests", 25), format, FormatOptions{ScanPath: "/repo"})
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if !strings.Contains(out, fragment) {
			t.Errorf("%s output does not show the rule (%q):\n%s", format, fragment, out)
		}
	}
	for format, fragment := range map[string]string{"json": `"rule_min_tokens": 25`, "sarif": `"ruleMinTokens": 25`} {
		out, _ := FormatReport(ruleReport("tests", 25), format, FormatOptions{ScanPath: "/repo"})
		if !strings.Contains(out, fragment) {
			t.Errorf("%s output does not carry the rule threshold (%q):\n%s", format, fragment, out)
		}
	}
}

// Without rules nothing about rules appears.
func TestFormatReport_NoRuleNoMention(t *testing.T) {
	for _, format := range []string{"text", "md", "json", "sarif", "github"} {
		out, err := FormatReport(ruleReport("", 0), format, FormatOptions{ScanPath: "/repo"})
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if strings.Contains(strings.ToLower(out), "rule tests") || strings.Contains(out, `"rule"`) || strings.Contains(out, "[rule:") {
			t.Errorf("%s output mentions a rule without one:\n%s", format, out)
		}
	}
}

func TestFormatReport_SummaryCountsFilesSkippedByRule(t *testing.T) {
	r := ruleReport("tests", 25)
	r.SkippedByRule = 3
	for format, fragment := range map[string]string{"text": "Skipped files : 3 (skip rule)", "md": "Skipped (skip rule) | 3"} {
		out, _ := FormatReport(r, format, FormatOptions{ScanPath: "/repo"})
		if !strings.Contains(out, fragment) {
			t.Errorf("%s summary missing %q:\n%s", format, fragment, out)
		}
	}
}
