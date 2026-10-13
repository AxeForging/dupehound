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

// Dead-function detection in the languages that have no function keyword
// (Java, C, C++, C#, Dart) and for JS/TS class methods. These used to be found
// by "an identifier followed by `(` outside a function", which matched macro
// calls and prototypes and missed every method inside a class.
func TestDeadCode_ClassAndCFamilyDefinitions(t *testing.T) {
	tests := []struct {
		name    string
		sources map[string]string
		want    []string
	}{
		{
			name: "java private method nobody calls",
			sources: map[string]string{
				"Service.java": "package app;\n\npublic class Service {\n    public int run(int x) {\n        return helper(x);\n    }\n\n" +
					"    private int helper(int x) {\n        return x + 1;\n    }\n\n    private int orphan(int x) {\n        return x * 2;\n    }\n}\n",
				"Main.java": "package app;\n\nclass Main {\n    public static void main(String[] args) {\n        new Service().run(1);\n    }\n}\n",
			},
			want: []string{"Service.java:orphan"},
		},
		{
			name: "java annotated, abstract and interface methods are not definitions to report",
			sources: map[string]string{
				"Thing.java": "package app;\n\ninterface Shape {\n    double area();\n}\n\nabstract class Base {\n    abstract void hook();\n}\n\n" +
					"class Thing extends Base implements Shape {\n    @Override\n    public double area() {\n        return 1;\n    }\n\n" +
					"    @Override\n    void hook() {\n    }\n\n    @Test\n    public void checksSomething() {\n    }\n\n" +
					"    @Bean(name = \"x\")\n    public Thing provider() {\n        return this;\n    }\n}\n",
			},
			want: []string{},
		},
		{
			name: "java control statements, calls and anonymous classes are not definitions",
			sources: map[string]string{
				"Flow.java": "package app;\n\nclass Flow {\n    private Runnable task = new Runnable() {\n        public void run() {\n        }\n    };\n\n" +
					"    static {\n        register(Flow.class);\n    }\n\n    public static void main(String[] args) {\n        if (args.length > 0) {\n            compute(args);\n        }\n" +
					"        for (String a : args) {\n            compute(args);\n        }\n    }\n\n    static void compute(String[] a) {\n    }\n\n    static void register(Class<?> c) {\n    }\n}\n",
			},
			want: []string{},
		},
		{
			name: "c static function nobody calls, with prototypes and macros around it",
			sources: map[string]string{
				"util.c": "#include \"util.h\"\n\nstatic int helper(int x);\n\nDEFINE_HANDLER(startup);\n\nstatic int helper(int x) {\n    return x + 1;\n}\n\n" +
					"static int orphan(int x) {\n    return x * 2;\n}\n\nint api(int x) {\n    return helper(x);\n}\n",
				"main.c": "#include \"util.h\"\n\nint main(void) {\n    return api(1);\n}\n",
			},
			want: []string{"util.c:orphan"},
		},
		{
			name: "c static function with the same name in another file does not hide a dead one",
			sources: map[string]string{
				"a.c": "static int parse(int x) {\n    return x;\n}\n",
				"b.c": "static int parse(int x) {\n    return x;\n}\n\nint run(void) {\n    return parse(1);\n}\n\nint main(void) {\n    return run();\n}\n",
			},
			want: []string{"a.c:parse"},
		},
		{
			name: "c prototype without a body is a declaration, not a definition",
			sources: map[string]string{
				"api.c": "int declared_elsewhere(int x);\n\nstruct point {\n    int x;\n};\n\nextern int also_declared(int x);\n\nenum mode { A, B };\n\nint used(int x) {\n    return x;\n}\n\nint main(void) {\n    return used(1);\n}\n",
			},
			want: []string{},
		},
		{
			name: "java private method with the same name in another class does not hide a dead one",
			sources: map[string]string{
				"A.java": "package app;\n\nclass A {\n    private int helper(int x) {\n        return x;\n    }\n}\n",
				"B.java": "package app;\n\nclass B {\n    private int helper(int x) {\n        return x;\n    }\n\n    public static void main(String[] args) {\n        new B().helper(1);\n    }\n}\n",
			},
			want: []string{"A.java:helper"},
		},
		{
			name: "typescript private method with the same name in another class does not hide a dead one",
			sources: map[string]string{
				"a.ts": "import { x } from 'x';\n\nexport class A {\n  private helper(v: number): number {\n    return v;\n  }\n}\n\nnew A();\n",
				"b.ts": "import { A } from './a';\n\nexport class B {\n  private helper(v: number): number {\n    return v;\n  }\n\n  run(): number {\n    return this.helper(1);\n  }\n}\n\nnew B().run();\n",
			},
			want: []string{"a.ts:helper"},
		},
		{
			// Found in redis: 500+ `static inline` helpers in headers were
			// reported, because a header's functions are used by its includers.
			name: "c static function in a header is used by the files that include it",
			sources: map[string]string{
				"sds.h":  "static inline int sds_len(const char *s) {\n    return s[0];\n}\n\nstatic inline int sds_orphan(const char *s) {\n    return s[1];\n}\n",
				"main.c": "#include \"sds.h\"\n\nint main(void) {\n    return sds_len(\"x\");\n}\n",
			},
			want: []string{"sds.h:sds_orphan"},
		},
		{
			// Found in redis: ae.c does `#include "ae_epoll.c"`, so the statics
			// of ae_epoll.c are called from ae.c.
			name: "c static function in a source file that another file includes",
			sources: map[string]string{
				"ae_epoll.c": "static int aeApiCreate(int n) {\n    return n;\n}\n\nstatic int aeApiOrphan(int n) {\n    return n;\n}\n",
				"ae.c":       "#include \"ae_epoll.c\"\n\nint main(void) {\n    return aeApiCreate(1);\n}\n",
			},
			want: []string{"ae_epoll.c:aeApiOrphan"},
		},
		{
			// Found in redis: a `static` on an earlier preprocessor line was
			// read as this function's modifier, and `__attribute__((…))` was
			// read as a function of that name.
			name: "c modifiers stop at preprocessor lines, and attributes are not functions",
			sources: map[string]string{
				"lzf.c": "#define LOCAL static inline\n#if !STATE_ARG\nsize_t\nlzf_compress (const void *in, size_t len)\n{\n    return len;\n}\n#endif\n\n" +
					"static inline __attribute__((always_inline))\nint fast_path(int x) {\n    return x;\n}\n\nint entry(int x) {\n    return fast_path(x);\n}\n",
				"main.c": "int main(void) {\n    return entry(1) + lzf_compress(0, 1);\n}\n",
			},
			want: []string{},
		},
		{
			// Found in guava: the JVM calls these private methods during
			// serialization; no source ever names them.
			name: "java serialization hooks",
			sources: map[string]string{
				"Box.java": "package app;\n\nclass Box implements java.io.Serializable {\n    private void writeObject(java.io.ObjectOutputStream s) {\n    }\n\n" +
					"    private void readObject(java.io.ObjectInputStream s) {\n    }\n\n    private Object readResolve() {\n        return this;\n    }\n\n" +
					"    private Object writeReplace() {\n        return this;\n    }\n\n    private void orphan() {\n    }\n}\n",
			},
			want: []string{"Box.java:orphan"},
		},
		{
			name: "c++ constructor initializer lists and overrides",
			sources: map[string]string{
				"w.cc": "class Widget : public Base {\n public:\n  Widget(int a, int b) : a_(a), b_(compute(b)) {\n  }\n\n  void Draw() override {\n  }\n\n" +
					" private:\n  static int compute(int v) {\n    return v;\n  }\n};\n\nint main() {\n  Widget w(1, 2);\n  return 0;\n}\n",
			},
			want: []string{},
		},
		{
			name: "csharp private method and attribute-marked tests",
			sources: map[string]string{
				"Svc.cs": "namespace App {\n    public class Svc {\n        public int Run(int x) {\n            return Helper(x);\n        }\n\n" +
					"        private int Helper(int x) {\n            return x + 1;\n        }\n\n        private int Orphan(int x) {\n            return x * 2;\n        }\n\n" +
					"        [Fact]\n        public void ChecksSomething() {\n        }\n\n        public override string ToString() {\n            return \"\";\n        }\n    }\n\n" +
					"    class Program {\n        static void Main() {\n            new Svc().Run(1);\n        }\n    }\n}\n",
			},
			want: []string{"Svc.cs:Orphan"},
		},
		{
			name: "typescript private method nobody calls",
			sources: map[string]string{
				"svc.ts": "import { x } from 'x';\n\nexport class Svc {\n  constructor(private dep: Dep) {}\n\n  run(v: number): number {\n    return this.helper(v);\n  }\n\n" +
					"  private helper(v: number): number {\n    return v + 1;\n  }\n\n  private orphan(v: number): number {\n    return v * 2;\n  }\n\n" +
					"  private handler = (v: number) => {\n    return v;\n  };\n\n  get size(): number {\n    return 1;\n  }\n}\n\nnew Svc(x).run(1);\n",
			},
			want: []string{"svc.ts:handler", "svc.ts:orphan"},
		},
		{
			name: "typescript decorated handlers and methods of derived classes are not reported",
			sources: map[string]string{
				"ctl.ts": "import { Controller, Get } from '@nestjs/common';\n\n@Controller({ path: 'cats' })\nexport class CatsController {\n" +
					"  @Get(':id')\n  findOne(id: string) {\n    return id;\n  }\n\n  @Post()\n  @HttpCode(204)\n  create(body: { name: string }) {\n    return body;\n  }\n}\n\n" +
					"export class Panel extends Component implements OnInit {\n  ngOnInit() {\n  }\n\n  render() {\n    return null;\n  }\n}\n",
			},
			want: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deadIn(t, tt.sources, nil); !slices.Equal(got, tt.want) {
				t.Errorf("dead = %q, want %q", got, tt.want)
			}
		})
	}
}

// Found in redis's bundled jemalloc: `CTL_PROTO(epoch)` expands to a use of
// `epoch_ctl` through `##`. A name assembled by the preprocessor never appears
// in the source, so in a file that pastes tokens an unused-looking static
// function is a guess, not a certainty.
func TestDeadCode_TokenPastingLowersConfidence(t *testing.T) {
	src := "#define CTL_PROTO(n) static int n##_ctl(int x);\n#define CALL(n) n##_ctl(1)\n\n" +
		"static int epoch_ctl(int x) {\n    return x;\n}\n\nint run(void) {\n    return CALL(epoch);\n}\n"
	files := []TokenizedFile{BuildTokenizedFile("ctl.c", src, LangForName("c"))}
	for _, d := range findDeadFunctions(files, files) {
		if d.Name == "epoch_ctl" && d.Confidence == "high" {
			t.Errorf("epoch_ctl reported with high confidence in a file that pastes tokens")
		}
	}

	plain := "static int orphan(int x) {\n    return x;\n}\n"
	files = []TokenizedFile{BuildTokenizedFile("plain.c", plain, LangForName("c"))}
	got := findDeadFunctions(files, files)
	if len(got) != 1 || got[0].Confidence != "high" {
		t.Errorf("an unused static function in a file without token pasting must stay high confidence, got %+v", got)
	}
}

// Defects found by running one real repository per language through the
// scanner: okhttp (Kotlin), swift-nio (Swift), cats (Scala), kong (Lua),
// phoenix (Elixir), webpack (JavaScript) and ohmyzsh (shell).
func TestDeadCode_RemainingLanguages(t *testing.T) {
	tests := []struct {
		name    string
		sources map[string]string
		want    []string
	}{
		{
			// 2,226 of okhttp's 2,273 findings were annotated functions.
			name: "kotlin annotated and overriding functions",
			sources: map[string]string{
				"CallTest.kt": "class CallTest {\n    @Test\n    fun getReturnsBody() {\n    }\n\n    @ParameterizedTest\n    @ValueSource(ints = [1, 2])\n    fun handlesSizes(n: Int) {\n    }\n\n" +
					"    @Test fun inline() {\n    }\n\n    override fun toString(): String {\n        return \"\"\n    }\n\n    private fun orphan() {\n    }\n}\n",
			},
			want: []string{"CallTest.kt:orphan"},
		},
		{
			name: "kotlin private function with the same name elsewhere does not hide a dead one",
			sources: map[string]string{
				"A.kt": "private fun helper(x: Int): Int {\n    return x\n}\n",
				"B.kt": "private fun helper(x: Int): Int {\n    return x\n}\n\nfun main() {\n    helper(1)\n}\n",
			},
			want: []string{"A.kt:helper"},
		},
		{
			// Found in okhttp: `fun Throwable.describe()` was recorded as a
			// function named Throwable.
			name: "kotlin extension function is named after the function, not its receiver",
			sources: map[string]string{
				"Ext.kt": "fun Throwable.describe(): String {\n    return \"\"\n}\n\nfun <T> List<T>.second(): T {\n    return this[1]\n}\n\n" +
					"private fun Char.isOrphan(): Boolean {\n    return true\n}\n\nfun main() {\n    Throwable().describe()\n    listOf(1, 2).second()\n}\n",
			},
			want: []string{"Ext.kt:isOrphan"},
		},
		{
			// Found in webpack's test cases: `function fn() {}` followed by
			// callbacks that call it.
			name: "function with an empty body called from a later callback",
			sources: map[string]string{
				"module.js": "import * as ns from './ns';\n\nfunction orphan() {}\n\nfunction fn() {}\n\nit('works', () => {\n  fn(ns.a);\n  fn(ns.b);\n});\n",
			},
			want: []string{"module.js:orphan"},
		},
		{
			// 2,064 of swift-nio's 2,416 findings were XCTest methods.
			name: "swift XCTest methods, overrides and objc entry points",
			sources: map[string]string{
				"Tests/ChannelTests.swift": "final class ChannelTests: XCTestCase {\n    func testWritesAreFlushed() throws {\n    }\n\n    override func setUp() {\n    }\n\n" +
					"    @objc func handleTap() {\n    }\n\n    @inlinable\n    func orphanHelper() {\n    }\n\n    private func privateOrphan() {\n    }\n}\n",
			},
			want: []string{"ChannelTests.swift:orphanHelper", "ChannelTests.swift:privateOrphan"},
		},
		{
			// 859 of cats' 1,094 findings were implicit definitions, which the
			// compiler resolves by type, never by name.
			name: "scala implicit and overriding definitions",
			sources: map[string]string{
				"IdT.scala": "object IdT {\n  implicit def catsDataShowForIdT[A](implicit F: Show[A]): Show[IdT[A]] = {\n    Show.show(_.toString)\n  }\n\n" +
					"  override def toString: String = {\n    \"\"\n  }\n\n  @Test\n  def checksLaw(): Unit = {\n  }\n\n  private def orphan(x: Int): Int = {\n    x\n  }\n}\n",
			},
			want: []string{"IdT.scala:orphan"},
		},
		{
			// `for … do … end` closes once, not twice: counting both `for` and
			// `do` left every function's body open to the end of the file.
			name: "lua local function called after a loop",
			sources: map[string]string{
				"listeners.lua": "local function parse_listeners(values)\n  for i, v in ipairs(values) do\n    print(v)\n  end\n  while false do\n    print(1)\n  end\n" +
					"  repeat\n    print(2)\n  until true\n  return values\nend\n\nlocal function orphan(x)\n  for i = 1, x do\n    print(i)\n  end\n  return 2\nend\n\n" +
					"local M = {}\n\nfunction M.load(conf)\n  return parse_listeners(conf)\nend\n\nreturn M\n",
				"init.lua": "local m = require(\"listeners\")\nm.load({})\n",
			},
			want: []string{"listeners.lua:orphan"},
		},
		{
			name: "elixir private function called from a later function",
			sources: map[string]string{
				"server.ex": "defmodule Server do\n  def run(args) do\n    if iex_running?() do\n      set_label(args)\n    end\n  end\n\n" +
					"  defp iex_running? do\n    true\n  end\n\n  defp set_label(x) do\n    x\n  end\n\n  defp orphan(x) do\n    x\n  end\n\n  def start do\n    run([])\n  end\nend\n\nServer.start()\n",
			},
			want: []string{"server.ex:orphan"},
		},
		{
			name: "ruby method called from a later method",
			sources: map[string]string{
				"job.rb": "class Job\n  def perform(x)\n    return prepare(x) if x\n    finish(x)\n  end\n\n  def prepare(x)\n    x\n  end\n\n  def finish(x)\n    x\n  end\n\n  def orphan\n    1\n  end\n\n  def start\n    perform(1)\n  end\nend\n\nJob.new.start\n",
			},
			want: []string{"job.rb:orphan"},
		},
		{
			// A named function expression's name is only visible inside it.
			name: "javascript named function expression is not a callable definition",
			sources: map[string]string{
				"extract.js": "module.exports = function extractUrlAndGlobal(value) {\n  return value;\n};\n\nconst cb = function onDone(err) {\n  return err;\n};\n\n" +
					"run(function handler() {\n  return 1;\n});\n\nfunction orphan() {\n  return 2;\n}\n\ncb();\n",
			},
			want: []string{"extract.js:orphan"},
		},
		{
			name: "shell completion function registered by name",
			sources: map[string]string{
				"cake.plugin.zsh": "_cakephp3 () {\n  compadd one two\n}\n\n__helper ()\n{\n  echo hi\n}\n\norphan_fn() {\n  echo no\n}\n\ncompdef _cakephp3 cake\n__helper\n",
			},
			want: []string{"cake.plugin.zsh:orphan_fn"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deadIn(t, tt.sources, nil); !slices.Equal(got, tt.want) {
				t.Errorf("dead = %q, want %q", got, tt.want)
			}
		})
	}
}
