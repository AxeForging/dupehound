package services

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// Regression tests for dead-function accuracy, each one a defect found by
// scoring dupehound against tools that resolve the code: staticcheck U1000 on
// golang/tools, rustc's dead_code lint on tokio, and vulture on django.

// deadIn runs dead-function detection over a set of source files (path →
// content, language taken from the extension) and returns "file:name" for each
// finding, sorted. refs are non-source files whose text may mention functions.
func deadIn(t *testing.T, sources, refs map[string]string) []string {
	t.Helper()
	paths := make([]string, 0, len(sources))
	for p := range sources {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var files []TokenizedFile
	for _, p := range paths {
		lang := DetectLanguage(p)
		if lang == nil {
			t.Fatalf("no language for %s", p)
		}
		files = append(files, BuildTokenizedFile(p, sources[p], lang))
	}
	a := newDeadCodeAnalysis(files)
	for _, f := range files {
		a.observeSource(f.Path, f.Tokens)
	}
	for p, text := range refs {
		a.observeText(p, text)
	}
	got := []string{}
	for _, d := range a.dead() {
		got = append(got, filepath.Base(d.File)+":"+d.Name)
	}
	sort.Strings(got)
	return got
}

func TestDeadCode_Accuracy(t *testing.T) {
	tests := []struct {
		name    string
		sources map[string]string
		refs    map[string]string
		want    []string
	}{
		{
			// Minified bundles put a whole program on one line. Excluding the
			// definition's line from the usage search hid every call on it.
			name: "call on the same line as the definition is a usage",
			sources: map[string]string{
				"lib.js": "function bh(a){return a}function run(){return bh(1)}run();\n",
			},
			want: []string{},
		},
		{
			name: "function that only calls itself is still dead",
			sources: map[string]string{
				"a.go": "package a\n\nfunc loop(n int) int {\n\treturn loop(n - 1)\n}\n",
			},
			want: []string{"a.go:loop"},
		},
		{
			// staticcheck reports `describe` in cmd/splitdwarf as unused; other
			// packages have their own `describe`, which used to mask it.
			name: "unexported go function is only reachable from its own package",
			sources: map[string]string{
				"cmd/one/a.go": "package one\n\nfunc describe() {}\n\nfunc Run() { helper() }\n\nfunc helper() {}\n",
				"cmd/two/b.go": "package two\n\nfunc describe() {}\n\nfunc Run() { describe() }\n",
			},
			want: []string{"a.go:describe"},
		},
		{
			name: "unexported go function used from a sibling file of the package",
			sources: map[string]string{
				"pkg/a.go":      "package pkg\n\nfunc helper() {}\n",
				"pkg/b.go":      "package pkg\n\nfunc Run() { helper() }\n",
				"pkg/b_test.go": "package pkg\n\nfunc TestRun(t *testing.T) { Run() }\n",
			},
			want: []string{},
		},
		{
			name: "exported go function used from another package",
			sources: map[string]string{
				"pkg/a.go":    "package pkg\n\nfunc Helper() {}\n",
				"cmd/main.go": "package main\n\nfunc main() { pkg.Helper() }\n",
			},
			want: []string{},
		},
		{
			// rustc reports no dead code in tokio; these were all reported.
			name: "rust trait implementations and trait declarations are dispatched, not called by name",
			sources: map[string]string{
				"src/ready.rs": "impl ops::BitAnd for Ready {\n    fn bitand(self, other: Ready) -> Ready { self }\n}\n\n" +
					"trait Shape {\n    fn area(&self) -> f64;\n}\n\n" +
					"impl Ready {\n    fn orphan(&self) {}\n}\n\nfn _assert_kinds() {}\n",
			},
			want: []string{"ready.rs:orphan"},
		},
		{
			name: "rust private function is only reachable inside its crate tree",
			sources: map[string]string{
				"a/src/lib.rs":  "fn helper() {}\n\npub fn api() {}\n",
				"b/src/lib.rs":  "fn helper() {}\n\npub fn run() { helper(); a::api(); }\n",
				"b/src/main.rs": "fn main() { run(); }\n",
			},
			want: []string{"lib.rs:helper"},
		},
		{
			name: "rust function registered through an attribute",
			sources: map[string]string{
				"src/lib.rs": "#[no_mangle]\npub extern \"C\" fn exported_symbol() {}\n\n#[inline]\nfn orphan() {}\n",
			},
			want: []string{"lib.rs:orphan"},
		},
		{
			// logging.Handler.emit, HTMLParser.handle_entityref: the caller is a
			// base class that lives outside the repository.
			name: "python method overriding an external base class hook",
			sources: map[string]string{
				"log.py": "import logging\n\nclass Local:\n    def orphan(self):\n        return 1\n\n" +
					"class AdminEmailHandler(logging.Handler):\n    def emit(self, record):\n        return 2\n\n" +
					"class Child(Local):\n    def other_orphan(self):\n        return 3\n",
			},
			want: []string{"log.py:orphan", "log.py:other_orphan"},
		},
		{
			name: "name used only inside a string is a dynamic reference",
			sources: map[string]string{
				"admin.py": "def action_checkbox(obj):\n    return obj\n\ndef orphan(obj):\n    return obj\n\nlist_display = [\"action_checkbox\"]\n",
			},
			want: []string{"admin.py:orphan"},
		},
		{
			name: "function referenced from a template or config file",
			sources: map[string]string{
				"helpers.rb": "def show_helper\n  1\nend\n\ndef orphan_helper\n  2\nend\n",
			},
			refs: map[string]string{"views/index.html.erb": "<%= show_helper %>\n"},
			want: []string{"helpers.rb:orphan_helper"},
		},
		{
			// `func(x int) bool {` is a literal, not a function named bool.
			name: "go function literal is not a definition named after its result type",
			sources: map[string]string{
				"a.go": "package a\n\nvar check = func(x int) bool { return x > 0 }\n\nfunc Use() []error {\n\tgo func() error { return nil }()\n\treturn nil\n}\n",
			},
			want: []string{"a.go:Use"},
		},
		{
			// tokio declares AsyncReadExt's methods through a macro inside the trait.
			name: "rust trait method declared through a macro inside the trait block",
			sources: map[string]string{
				"src/ext.rs": "pub trait AsyncReadExt {\n    read_impl! {\n        fn read_u8(&mut self) -> ReadU8;\n        fn read_i8(&mut self) -> ReadI8;\n    }\n}\n",
			},
			want: []string{},
		},
		{
			name: "python method called by the method defined right after it",
			sources: map[string]string{
				"models.py": "class Local:\n    pass\n\nclass Manager(Local):\n    def create_user(self, name):\n        return name\n\n" +
					"    async def acreate_user(self, name):\n        return self.create_user(name)\n\n    def orphan(self):\n        return 1\n\nManager().acreate_user(1)\n",
			},
			want: []string{"models.py:orphan"},
		},
		{
			name: "python nested function used by the function enclosing it",
			sources: map[string]string{
				"search.py": "def get_results(fields):\n    def construct_search(name):\n        return name\n\n    return [construct_search(f) for f in fields]\n\nget_results([])\n",
			},
			want: []string{},
		},
		{
			name: "javascript arrow functions and function expressions are definitions",
			sources: map[string]string{
				"mod.js": "import x from 'x';\nconst used = (a) => a + 1;\nconst deadArrow = (a) => a * 2;\n" +
					"const deadExpr = function (a) { return a; };\nconst deadAsync = async (a) => a;\n" +
					"const notAFunction = 42;\nexport const api = (a) => used(a);\napi(notAFunction);\n",
			},
			want: []string{"mod.js:deadArrow", "mod.js:deadAsync", "mod.js:deadExpr"},
		},
		{
			// Found in nestjs/nest: `sleep` was reported dead with six callers.
			name: "expression-bodied arrow function called from a later callback",
			sources: map[string]string{
				"a.spec.ts": "import { x } from 'x';\nconst deadOne = (ms: number) => ms * 2;\n" +
					"const sleep = (ms: number) => new Promise(r => setTimeout(r, ms));\n" +
					"describe('suite', () => {\n  it('waits', async () => {\n    await sleep(20);\n  });\n});\n",
			},
			want: []string{"a.spec.ts:deadOne"},
		},
		{
			name: "javascript module-private function is only reachable inside its file",
			sources: map[string]string{
				"a.js": "import x from 'x';\nfunction helper() { return 1; }\nexport function run() { return 2; }\n",
				"b.js": "import { run } from './a';\nfunction helper() { return 3; }\nrun(helper());\n",
			},
			want: []string{"a.js:helper"},
		},
		{
			name: "javascript script function is global and reachable from any file",
			sources: map[string]string{
				"a.js": "function helper() { return 1; }\n",
				"b.js": "helper();\n",
			},
			want: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deadIn(t, tt.sources, tt.refs); !slices.Equal(got, tt.want) {
				t.Errorf("dead = %q, want %q", got, tt.want)
			}
		})
	}
}

// A function called only from a file the scan skips (generated code, a file
// too small to hold a clone, an excluded test) is not dead.
func TestScan_DeadCodeCountsUsageFromSkippedFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "handlers.go", "package app\n\nfunc registerHandlers() {}\n\nfunc orphan() {}\n")
	writeFile(t, dir, "zz_generated.go", "// Code generated by wire. DO NOT EDIT.\n\npackage app\n\nfunc init() { registerHandlers() }\n")

	report, err := NewScannerService().Scan(ScanOptions{Path: dir, MinTokens: 5, DeadCode: true})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	var names []string
	for _, d := range report.DeadFunctions {
		names = append(names, d.Name)
	}
	if !slices.Equal(names, []string{"orphan"}) {
		t.Errorf("dead functions = %q, want [orphan]", names)
	}
}

// Dead functions in files too small to contain a clone must still be found.
func TestScan_DeadCodeCoversFilesTooSmallForCloneDetection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tiny.go", "package app\n\nfunc orphan() {}\n")

	report, err := NewScannerService().Scan(ScanOptions{Path: dir, MinTokens: 50, DeadCode: true})
	if err != nil && !strings.Contains(err.Error(), "no") {
		t.Fatalf("scan: %v", err)
	}
	if report == nil || len(report.DeadFunctions) != 1 || report.DeadFunctions[0].Name != "orphan" {
		t.Errorf("expected the dead function in a tiny file to be reported, got %+v", report)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// A private function nobody refers to is dead; a public one may simply be API
// that callers outside the repository use. The two are told apart, and the
// certain findings come first.
func TestDeadCode_ConfidenceSeparatesPrivateFromPublicAPI(t *testing.T) {
	files := []TokenizedFile{
		BuildTokenizedFile("pkg/a.go", "package pkg\n\nfunc PublicAPI() {}\n\nfunc privateOrphan() {}\n", LangForName("go")),
	}
	got := findDeadFunctions(files, files)
	if len(got) != 2 {
		t.Fatalf("expected 2 dead functions, got %+v", got)
	}
	if got[0].Name != "privateOrphan" || got[0].Confidence != "high" {
		t.Errorf("first finding = %s (%s), want privateOrphan (high)", got[0].Name, got[0].Confidence)
	}
	if got[1].Name != "PublicAPI" || got[1].Confidence != "low" {
		t.Errorf("second finding = %s (%s), want PublicAPI (low)", got[1].Name, got[1].Confidence)
	}
}

func TestDeadCode_XunitHookOutsideATestFile(t *testing.T) {
	// No base class, so nothing but the hook names can exempt these.
	src := "class Decorator:\n    def setUp(self):\n        pass\n\n    def tearDown(self):\n        pass\n"
	if got := deadIn(t, map[string]string{"django/test/utils.py": src}, nil); len(got) != 0 {
		t.Errorf("dead = %q, want none", got)
	}
}
