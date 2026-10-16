package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadConfigText(t *testing.T, text string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".dupehound.yml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	return err
}

func TestLoadConfig_ParsesRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".dupehound.yml")
	text := `
scan:
  min-tokens: 50
rules:
  - name: tests
    paths: ["**/*_test.py", "**/tests/**"]
    min-tokens: 25
    similarity: 0.8
  - paths: ["**/generated/**"]
    skip: true
`
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Rules) != 2 {
		t.Fatalf("want 2 rules, got %d", len(cfg.Rules))
	}
	r := cfg.Rules[0]
	if r.Name != "tests" || r.MinTokens != 25 || r.Similarity != 0.8 || len(r.Paths) != 2 || r.Skip {
		t.Errorf("rule 0 parsed wrong: %+v", r)
	}
	if !cfg.Rules[1].Skip || cfg.Rules[1].Name != "" {
		t.Errorf("rule 1 parsed wrong: %+v", cfg.Rules[1])
	}
}

// A rule with only paths is valid: it pins those files to the scan settings
// and stops later rules from matching them.
func TestLoadConfig_AcceptsRuleWithoutOverrides(t *testing.T) {
	if err := loadConfigText(t, "rules:\n  - paths: [\"src/**\"]\n"); err != nil {
		t.Fatalf("want valid, got %v", err)
	}
}

// Unknown keys under scan: stay accepted, as before rules existed, so
// existing configs keep loading.
func TestLoadConfig_ScanBlockStaysLenient(t *testing.T) {
	if err := loadConfigText(t, "scan:\n  min-tokens: 40\n  some-future-key: 1\n"); err != nil {
		t.Fatalf("want valid, got %v", err)
	}
}

func TestLoadConfig_RejectsInvalidRules(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want []string // fragments the error must contain
	}{
		{"unknown key", "rules:\n  - paths: [\"a/**\"]\n    min_tokens: 20\n", []string{"rules[0]", "line 3", `unknown key "min_tokens"`, "min-tokens"}},
		{"missing paths", "rules:\n  - min-tokens: 20\n", []string{"rules[0]", "paths"}},
		{"empty paths", "rules:\n  - paths: []\n    min-tokens: 20\n", []string{"rules[0]", "paths", "empty"}},
		{"empty pattern", "rules:\n  - paths: [\"\"]\n", []string{"rules[0]", "empty pattern"}},
		{"bad glob", "rules:\n  - paths: [\"src/[a\"]\n", []string{"rules[0]", "src/[a"}},
		{"bad doublestar glob", "rules:\n  - paths: [\"**/[a\"]\n", []string{"rules[0]", "**/[a"}},
		{"zero min-tokens", "rules:\n  - paths: [\"a/**\"]\n    min-tokens: 0\n", []string{"rules[0]", "min-tokens", "greater than 0"}},
		{"negative min-tokens", "rules:\n  - paths: [\"a/**\"]\n    min-tokens: -5\n", []string{"min-tokens", "greater than 0"}},
		{"similarity too low", "rules:\n  - paths: [\"a/**\"]\n    similarity: 0.3\n", []string{"similarity", "0.50"}},
		{"similarity too high", "rules:\n  - paths: [\"a/**\"]\n    similarity: 1.5\n", []string{"similarity", "1.00"}},
		{"skip with override", "rules:\n  - paths: [\"a/**\"]\n    skip: true\n    min-tokens: 20\n", []string{"rules[0]", "skip"}},
		{"duplicate name", "rules:\n  - name: t\n    paths: [\"a/**\"]\n  - name: t\n    paths: [\"b/**\"]\n", []string{"rules[1]", `"t"`, "rules[0]"}},
		{"reserved name", "rules:\n  - name: scan\n    paths: [\"a/**\"]\n", []string{"rules[0]", "scan", "reserved"}},
		{"rules not a list", "rules:\n  paths: [\"a/**\"]\n", []string{"rules", "list"}},
		{"rule not a mapping", "rules:\n  - \"a/**\"\n", []string{"rules[0]", "mapping"}},
		{"second rule reported by index", "rules:\n  - paths: [\"a/**\"]\n  - paths: [\"b/**\"]\n    min-tokens: 0\n", []string{"rules[1]", "line 4"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := loadConfigText(t, tt.yaml)
			if err == nil {
				t.Fatalf("want an error for %q, got nil", tt.yaml)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %q", err, w)
				}
			}
		})
	}
}

func TestRulePatternMatch_RelativeToScanRoot(t *testing.T) {
	tests := []struct {
		pattern, rel string
		want         bool
	}{
		{"**/tests/**", "tests/test_a.py", true}, // leading **/ also matches zero directories
		{"**/tests/**", "pkg/core/tests/test_a.py", true},
		{"**/tests/**", "pkg/testsuite/a.py", false},
		{"tests/**", "tests/sub/a.py", true},
		{"tests/**", "pkg/tests/a.py", false}, // anchored at the scan root
		{"*_test.py", "pkg/a_test.py", true},  // no slash: matches the base name
		{"*_test.py", "pkg/a.py", false},
		{"**/*_test.py", "a_test.py", true},
		{"**/*.test.ts", "web/src/app.test.ts", true},
		{"**/migrations/versions/*.py", "migrations/versions/001_init.py", true},
		{"**/migrations/versions/*.py", "svc/migrations/versions/001_init.py", true},
		{"**/migrations/versions/*.py", "svc/migrations/versions/old/001.py", false},
		{"src/*.py", "src/a.py", true},
		{"src/*.py", "src/sub/a.py", false},
	}
	for _, tt := range tests {
		if got := rulePatternMatch(tt.pattern, tt.rel); got != tt.want {
			t.Errorf("rulePatternMatch(%q, %q) = %v, want %v", tt.pattern, tt.rel, got, tt.want)
		}
	}
}

func TestResolveRule_FirstMatchWins(t *testing.T) {
	rs := newRuleSet(t.TempDir(), 50, 0.7, testRules(
		ruleSpec{name: "tests", paths: []string{"**/tests/**"}, minTokens: 25},
		ruleSpec{name: "all-python", paths: []string{"*.py"}, minTokens: 80},
	))
	root := rs.root
	cases := []struct {
		file      string
		wantLabel string
		wantMin   int
	}{
		{"tests/test_a.py", "tests", 25},  // matches both; the first wins
		{"src/app.py", "all-python", 80},  // only the second
		{"src/app.go", ruleLabelScan, 50}, // none: scan settings
		{"pkg/tests/x.go", "tests", 25},   // nested test dir
		{"pkg/testsuite/x.go", ruleLabelScan, 50},
	}
	for _, c := range cases {
		got := rs.forFile(filepath.Join(root, c.file))
		if got.label != c.wantLabel || got.minTokens != c.wantMin {
			t.Errorf("%s: got rule %q min %d, want %q min %d", c.file, got.label, got.minTokens, c.wantLabel, c.wantMin)
		}
	}
}

func TestResolveRule_UnnamedRuleLabelledByIndexAndInheritsUnsetValues(t *testing.T) {
	rs := newRuleSet(".", 50, 0.7, testRules(
		ruleSpec{paths: []string{"a/**"}, minTokens: 30},
		ruleSpec{paths: []string{"b/**"}, similarity: 0.9},
	))
	a := rs.forFile("a/x.go")
	if a.label != "rules[0]" || a.minTokens != 30 || a.similarity != 0.7 {
		t.Errorf("a: %+v", a)
	}
	b := rs.forFile("b/x.go")
	if b.label != "rules[1]" || b.minTokens != 50 || b.similarity != 0.9 {
		t.Errorf("b: %+v", b)
	}
}

// Empty and comment-only config files load as before.
func TestLoadConfig_EmptyFileStillLoads(t *testing.T) {
	for _, text := range []string{"", "# nothing here\n", "rules:\n"} {
		if err := loadConfigText(t, text); err != nil {
			t.Errorf("config %q: %v", text, err)
		}
	}
}
