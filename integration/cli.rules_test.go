package integration

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

// Per-path rules (spec 003), through the built binary.

const (
	rulesFake = `class EchoModel:
    def generate(self, prompt, options):
        text = prompt.strip()
        if options.upper:
            text = text.upper()
        return {"text": text, "tokens": len(text)}
`
	rulesProd = `def load_settings(path, env):
    raw = read_file(path)
    data = parse_yaml(raw)
    for key, value in env.items():
        if key.startswith("APP_"):
            data[key[4:].lower()] = value
    validate(data)
    log.info("loaded settings", path=path, keys=len(data))
    return data
`
	rulesConfig = `scan:
  min-tokens: 50
rules:
  - name: tests
    paths: ["**/*_test.py", "**/tests/**"]
    min-tokens: 25
  - name: migrations
    paths: ["**/migrations/versions/*.py"]
    min-tokens: 120
  - name: generated
    paths: ["**/generated/**"]
    skip: true
`
)

// rulesFixture is a small Python workspace: a test fake copied between test
// files and once into production, a production clone, a migration copied
// from production, and a generated file.
func rulesFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"tests/test_a.py":                    rulesFake + "\ndef test_a():\n    assert True\n",
		"tests/test_b.py":                    rulesFake,
		"src/app.py":                         "import x\n\n" + rulesFake + "\n" + rulesProd,
		"src/other.py":                       rulesProd,
		"db/migrations/versions/001_init.py": rulesProd,
		"src/generated/client.py":            rulesProd + "\n" + rulesFake,
	}
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Dir(path), filepath.Base(path), content)
	}
	return dir
}

// runIn runs bin in dir and returns stdout, stderr and the exit code.
func runIn(t *testing.T, bin, dir string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return stdout.String(), stderr.String(), code
}

func writeConfig(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dupehound.yml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// goldenScan compares the binary's output on the fixture with a golden file
// recorded by the release before rules existed. To re-record from another
// binary: DUPEHOUND_GOLDEN_BIN=/path/to/dupehound go test ./integration -run Golden
func goldenScan(t *testing.T, name string, output string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if bin := os.Getenv("DUPEHOUND_GOLDEN_BIN"); bin != "" {
		t.Logf("recording %s with %s", path, bin)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if output != string(want) {
		t.Errorf("output differs from %s (recorded before rules existed)\n--- got ---\n%s\n--- want ---\n%s", path, output, want)
	}
}

// Without rules — no rules block, or --no-rules — the output is byte for byte
// what dupehound printed before rules existed.
func TestRules_NoRulesOutputIsUnchanged_Golden(t *testing.T) {
	bin := buildBinary(t)
	if golden := os.Getenv("DUPEHOUND_GOLDEN_BIN"); golden != "" {
		bin = golden
	}
	dir := rulesFixture(t)
	plain := writeConfig(t, "scan:\n  min-tokens: 50\n")
	for _, format := range []string{"json", "text"} {
		golden := "rules.norules." + format + ".golden"
		out, stderr, code := runIn(t, bin, dir, "scan", "--path", ".", "--config", plain, "--format", format, "-q", "--top", "0")
		if code != 1 {
			t.Fatalf("exit %d, want 1 (clones found)\n%s", code, stderr)
		}
		if os.Getenv("DUPEHOUND_GOLDEN_BIN") != "" {
			if err := os.WriteFile(filepath.Join("testdata", golden), []byte(out), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		goldenScan(t, golden, out)

		if os.Getenv("DUPEHOUND_GOLDEN_BIN") != "" {
			continue // the recording binary has no --no-rules
		}
		ruled := writeConfig(t, rulesConfig)
		out, stderr, code = runIn(t, bin, dir, "scan", "--path", ".", "--config", ruled, "--no-rules", "--format", format, "-q", "--top", "0")
		if code != 1 {
			t.Fatalf("--no-rules: exit %d\n%s", code, stderr)
		}
		goldenScan(t, golden, out)
	}
}

func TestRules_ConfigRulesAppliedAndReported(t *testing.T) {
	bin := buildBinary(t)
	dir := rulesFixture(t)
	cfg := writeConfig(t, rulesConfig)

	out, stderr, code := runIn(t, bin, dir, "scan", "--path", ".", "--config", cfg, "--format", "json", "-q")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, stderr)
	}
	var r domain.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	byRule := map[string][]string{}
	for _, c := range r.Clones {
		var files []string
		for _, inst := range c.Instances {
			files = append(files, filepath.ToSlash(inst.File))
		}
		byRule[c.Rule] = append(byRule[c.Rule], strings.Join(files, ","))
		if strings.Contains(strings.Join(files, ","), "generated/") {
			t.Errorf("file under a skip rule reported: %v", files)
		}
	}
	if got := byRule["tests"]; len(got) != 1 || got[0] != "tests/test_a.py,tests/test_b.py" {
		t.Errorf("tests rule clones = %v, want the fake in the two test files", got)
	}
	if got := byRule["scan"]; len(got) != 1 || got[0] != "src/app.py,src/other.py" {
		t.Errorf("scan clones = %v, want the production pair (the migration copy is judged at 120)", got)
	}
	if r.SkippedByRule != 1 {
		t.Errorf("skipped_by_rule = %d, want 1", r.SkippedByRule)
	}

	text, _, _ := runIn(t, bin, dir, "scan", "--path", ".", "--config", cfg, "-q")
	for _, want := range []string{"[rule: tests ≥25]", "[rule: scan ≥50]", "Skipped files : 1 (skip rule)"} {
		if !strings.Contains(text, want) {
			t.Errorf("text output missing %q:\n%s", want, text)
		}
	}
}

// --min-tokens on the command line replaces the scan threshold; rules still
// apply on top of it.
func TestRules_CLIMinTokensSetsTheBaseAndRulesStillApply(t *testing.T) {
	bin := buildBinary(t)
	dir := rulesFixture(t)
	cfg := writeConfig(t, rulesConfig)
	out, _, _ := runIn(t, bin, dir, "scan", "--path", ".", "--config", cfg, "--min-tokens", "200", "--format", "json", "-q", "--exit-zero")
	var r domain.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Clones) != 1 || r.Clones[0].Rule != "tests" {
		t.Errorf("want only the tests-rule clone at base 200, got %+v", r.Clones)
	}
}

func TestRules_InvalidConfigIsRejected(t *testing.T) {
	bin := buildBinary(t)
	dir := rulesFixture(t)
	cases := map[string]string{
		"unknown key":    "rules:\n  - paths: [\"tests/**\"]\n    min_tokens: 25\n",
		"empty paths":    "rules:\n  - paths: []\n",
		"bad glob":       "rules:\n  - paths: [\"tests/[\"]\n",
		"zero threshold": "rules:\n  - paths: [\"tests/**\"]\n    min-tokens: 0\n",
	}
	for name, text := range cases {
		_, stderr, code := runIn(t, bin, dir, "scan", "--path", ".", "--config", writeConfig(t, text), "-q")
		if code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
		if !strings.Contains(stderr, "rules[0] (line ") {
			t.Errorf("%s: error does not name the rule and line:\n%s", name, stderr)
		}
	}
}

// The starter config documents rules and is itself valid.
func TestRules_InitConfigLoadsAndUsesRules(t *testing.T) {
	bin := buildBinary(t)
	dir := rulesFixture(t)
	if _, stderr, code := runIn(t, bin, dir, "init"); code != 0 {
		t.Fatalf("init: exit %d\n%s", code, stderr)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".dupehound.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "rules:") {
		t.Fatalf("starter config has no rules example:\n%s", data)
	}
	out, stderr, code := runIn(t, bin, dir, "scan", "--format", "json", "-q", "--exit-zero")
	if code != 0 {
		t.Fatalf("scan with the starter config: exit %d\n%s", code, stderr)
	}
	if !strings.Contains(out, `"rule": "tests"`) {
		t.Errorf("starter rules did not apply to the test files:\n%s", out)
	}
}
