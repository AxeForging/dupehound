package integration

import (
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

// End-to-end coverage for issue #31, driven through the built binary at its
// real defaults. The unit tests pin the mask and the overlap guard; these pin
// what a user actually sees when they run `dupehound scan` on a Go package with
// table-driven tests.

// issue31Repro is the fixture from the issue report.
const issue31Repro = `package repro

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		absent  string
		present string
	}{
		{name: "openai preset case", input: "token openai_AAAA0\n", absent: "openai_AAAA", present: "openai_***"},
		{name: "anthropic preset case", input: "token anthropic_AAAA1\n", absent: "anthropic_AAAA", present: "anthropic_***"},
		{name: "github preset case", input: "token github_AAAA2\n", absent: "github_AAAA", present: "github_***"},
		{name: "awskey preset case", input: "token awskey_AAAA3\n", absent: "awskey_AAAA", present: "awskey_***"},
		{name: "bearer preset case", input: "token bearer_AAAA4\n", absent: "bearer_AAAA", present: "bearer_***"},
		{name: "stripe preset case", input: "token stripe_AAAA5\n", absent: "stripe_AAAA", present: "stripe_***"},
		{name: "gcp preset case", input: "token gcp_AAAA6\n", absent: "gcp_AAAA", present: "gcp_***"},
		{name: "azure preset case", input: "token azure_AAAA7\n", absent: "azure_AAAA", present: "azure_***"},
		{name: "npm preset case", input: "token npm_AAAA8\n", absent: "npm_AAAA", present: "npm_***"},
		{name: "slack preset case", input: "token slack_AAAA9\n", absent: "slack_AAAA", present: "slack_***"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Contains(tc.present, tc.absent) {
				t.Errorf("%s", tc.name)
			}
		})
	}
}
`

// neutralConfig writes a config with no excludes into dir and returns its path.
//
// Config discovery walks up from the working directory, so a scan launched from
// the test binary would otherwise inherit dupehound's own .dupehound.yml — which
// excludes *_test.go and would silently skip every fixture here. Passing an
// explicit config pins these tests to default scanning behaviour, which is the
// behaviour issue #31 is about.
func neutralConfig(t *testing.T, dir string) string {
	t.Helper()
	return writeFile(t, dir, "neutral.yml", "scan:\n  exclude: []\n")
}

// scanJSON runs a scan over dir and decodes the report.
func scanJSON(t *testing.T, bin, dir string, args ...string) domain.Report {
	t.Helper()
	full := append([]string{
		"scan", "--path", dir,
		"--config", neutralConfig(t, dir),
		"--format", "json", "--exit-zero",
	}, args...)
	out, err := exec.Command(bin, full...).Output() // stdout only — logs go to stderr
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	var report domain.Report
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, string(out))
	}
	return report
}

// scanExitCode runs a scan for its exit status — the signal CI and pre-commit
// hooks actually gate on.
func scanExitCode(t *testing.T, bin, dir string) error {
	t.Helper()
	return exec.Command(bin, "scan", "--path", dir,
		"--config", neutralConfig(t, dir), "--quiet").Run()
}

func TestScan_Issue31_TableDrivenTestIsClean(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "table_test.go", issue31Repro)

	report := scanJSON(t, bin, dir)

	if len(report.Clones) != 0 {
		t.Errorf("table-driven test reported %d clone(s); expected a clean scan", len(report.Clones))
		for _, c := range report.Clones {
			t.Logf("  %s similarity=%.2f instances=%d", c.Type, c.Similarity, len(c.Instances))
			for _, in := range c.Instances {
				t.Logf("    %s:%d-%d", in.File, in.StartLine, in.EndLine)
			}
		}
	}
}

// The scan must exit 0 for a package whose only "duplication" was a data table
// — that exit code is what gates CI and pre-commit hooks.
func TestScan_Issue31_TableDrivenTestExitsZeroWithoutExitZeroFlag(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "table_test.go", issue31Repro)

	// Deliberately without --exit-zero: a clean scan must exit 0 on its own.
	if err := scanExitCode(t, bin, dir); err != nil {
		t.Errorf("scan exited non-zero on a package with only a data table: %v", err)
	}
}

// The counterpart guard: genuinely duplicated logic in the same package is
// still found, and the scan still fails. Without this, "no clones" could just
// mean detection is broken.
func TestScan_Issue31_RealDuplicationStillFailsTheScan(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "table_test.go", issue31Repro)

	dup := `
	server := newServer()
	defer server.Close()
	client := server.Client()
	req := buildRequest(server.URL)
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	body := readAll(resp.Body)
	if resp.StatusCode != 200 {
		panic(body)
	}
	record(body)
`
	writeFile(t, dir, "handlers.go",
		"package repro\n\nfunc alpha() {"+dup+"}\n\nfunc bravo() {"+dup+"}\n")

	report := scanJSON(t, bin, dir)
	if len(report.Clones) == 0 {
		t.Fatal("copy-pasted function bodies must still be reported")
	}

	// Every reported instance must come from the real duplication, not the table.
	for _, c := range report.Clones {
		for _, in := range c.Instances {
			if in.File == "table_test.go" || filepathBase(in.File) == "table_test.go" {
				t.Errorf("clone instance points at the data table: %s:%d-%d", in.File, in.StartLine, in.EndLine)
			}
		}
	}

	// And a scan with real duplication must fail (exit non-zero) so CI blocks it.
	if err := scanExitCode(t, bin, dir); err == nil {
		t.Error("scan exited 0 despite genuine duplication; CI would not block it")
	}
}

// No clone group may ever report overlapping instances within one file.
func TestScan_Issue31_NoOverlappingInstancesInReport(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	// A long, highly periodic body — the shape that produced sliding-window
	// "instances" of the same text before the overlap guard.
	src := "package repro\n\nfunc run() {\n"
	for i := 0; i < 40; i++ {
		src += "\tstep(a, b)\n\tcheck(a, b)\n\temit(a, b)\n"
	}
	src += "}\n"
	writeFile(t, dir, "periodic.go", src)

	report := scanJSON(t, bin, dir)
	for _, c := range report.Clones {
		for i := range c.Instances {
			for j := i + 1; j < len(c.Instances); j++ {
				a, b := c.Instances[i], c.Instances[j]
				if a.File != b.File {
					continue
				}
				if a.StartLine <= b.EndLine && b.StartLine <= a.EndLine {
					t.Errorf("clone %s reports overlapping instances in %s: %d-%d and %d-%d",
						c.Hash, a.File, a.StartLine, a.EndLine, b.StartLine, b.EndLine)
				}
			}
		}
	}
}

// filepathBase avoids importing path/filepath just for one call in assertions.
func filepathBase(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}
