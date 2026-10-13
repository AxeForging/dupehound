package services

import (
	"slices"
	"sort"
	"testing"
)

// Regression tests for dead-function detection on functions that are never
// called by name because a runtime or framework invokes them: program entry
// points, test functions, interface methods, decorated handlers.
//
// Found by scanning real repositories, where these were nearly the entire
// output: 3,779 of 3,938 findings on hashicorp/terraform and 14,837 of 14,967
// on django/django were test functions.

// deadNames runs dead-function detection over a single file and returns the
// reported names, sorted.
func deadNames(t *testing.T, path, lang, src string) []string {
	t.Helper()
	files := []TokenizedFile{BuildTokenizedFile(path, src, LangForName(lang))}
	names := []string{}
	for _, d := range findDeadFunctions(files, files) {
		names = append(names, d.Name)
	}
	sort.Strings(names)
	return names
}

func TestDeadCode_FrameworkInvokedFunctionsAreNotDead(t *testing.T) {
	tests := []struct {
		name string
		path string
		lang string
		src  string
		want []string // names that must still be reported
	}{
		{
			name: "go main and init",
			path: "main.go", lang: "go",
			src:  "package app\n\nfunc main() {}\n\nfunc init() {}\n\nfunc orphan() {}\n",
			want: []string{"orphan"},
		},
		{
			name: "go test, benchmark, fuzz and example functions",
			path: "thing_test.go", lang: "go",
			src: "package app\n\nfunc TestThing(t *testing.T) {}\n\nfunc BenchmarkThing(b *testing.B) {}\n\n" +
				"func FuzzThing(f *testing.F) {}\n\nfunc ExampleThing() {}\n\nfunc TestMain(m *testing.M) {}\n\nfunc orphanHelper() {}\n",
			want: []string{"orphanHelper"},
		},
		{
			name: "go Test-prefixed function outside a test file is ordinary code",
			path: "thing.go", lang: "go",
			src:  "package app\n\nfunc TestConnection() {}\n",
			want: []string{"TestConnection"},
		},
		{
			name: "go exported method may satisfy an interface",
			path: "thing.go", lang: "go",
			src:  "package app\n\ntype S struct{}\n\nfunc (s S) String() string { return \"\" }\n\nfunc (s *S) orphanMethod() {}\n",
			want: []string{"orphanMethod"},
		},
		{
			name: "python test functions and dunder methods",
			path: "tests/test_thing.py", lang: "python",
			src: "class ThingTests:\n    def __init__(self):\n        pass\n\n    def test_adds(self):\n        pass\n\n" +
				"    def setUp(self):\n        pass\n\n    def tearDown(self):\n        pass\n\ndef orphan_helper():\n    pass\n",
			want: []string{"orphan_helper"},
		},
		{
			name: "python decorated function is registered, not called",
			path: "app/views.py", lang: "python",
			src: "@app.route(\"/\")\ndef index():\n    return 1\n\n@pytest.fixture\ndef client():\n    return 2\n\n" +
				"@decorator\nasync def handler():\n    return 3\n\ndef orphan():\n    return 4\n",
			want: []string{"orphan"},
		},
		{
			name: "python test-prefixed function outside a test file is ordinary code",
			path: "app/checks.py", lang: "python",
			src:  "def test_connection():\n    return 1\n",
			want: []string{"test_connection"},
		},
		{
			name: "rust main and attribute-marked tests",
			path: "src/lib.rs", lang: "rust",
			src: "fn main() {}\n\n#[test]\nfn adds() {}\n\n#[tokio::test]\nasync fn adds_async() {}\n\n" +
				"#[test]\n#[should_panic]\nfn panics() {}\n\n#[bench]\nfn speed(b: &mut Bencher) {}\n\n#[inline]\nfn orphan() {}\n",
			want: []string{"orphan"},
		},
		{
			name: "ruby minitest methods",
			path: "test/thing_test.rb", lang: "ruby",
			src:  "class ThingTest\n  def test_adds\n    1\n  end\n\n  def setup\n    2\n  end\n\n  def orphan_helper\n    3\n  end\nend\n",
			want: []string{"orphan_helper"},
		},
		{
			name: "php phpunit methods",
			path: "tests/ThingTest.php", lang: "php",
			src:  "<?php\nclass ThingTest {\n  public function testAdds() { return 1; }\n  protected function setUp() { return 2; }\n  private function orphanHelper() { return 3; }\n}\n",
			want: []string{"orphanHelper"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deadNames(t, tt.path, tt.lang, tt.src); !slices.Equal(got, tt.want) {
				t.Errorf("dead functions = %q, want %q", got, tt.want)
			}
		})
	}
}
