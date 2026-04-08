package services

import (
	"path/filepath"
	"testing"
)

// TestInlineIgnore_GoFunction verifies that //dupehound:ignore before a Go function
// causes that function body to be excluded from detection.
func TestInlineIgnore_GoFunction(t *testing.T) {
	lang := LangForName("go")
	if lang == nil {
		t.Fatal("could not find go language")
	}

	content := `package main

//dupehound:ignore
func legacyValidate(x int) int {
	result := x * 2
	return result
}

func normalFunc(x int) int {
	result := x * 3
	return result
}
`
	tokens := TokenizeFileWithIgnore(content, lang)

	// Find the function body of legacyValidate and check tokens are marked.
	// The comment line is line 3, so function starts at line 4.
	// Tokens with IgnoreMark should be on line 4 (first token of the function signature).
	hasIgnoreMark := false
	for _, tok := range tokens {
		if tok.IgnoreMark {
			hasIgnoreMark = true
			break
		}
	}
	if !hasIgnoreMark {
		t.Error("expected some tokens to be marked with IgnoreMark after //dupehound:ignore")
	}
}

// TestInlineIgnore_PythonFunction verifies # dupehound:ignore before a Python function.
func TestInlineIgnore_PythonFunction(t *testing.T) {
	lang := LangForName("python")
	if lang == nil {
		t.Fatal("could not find python language")
	}

	content := `
# dupehound:ignore
def old_process():
    x = compute()
    return x

def new_process():
    y = compute()
    return y
`
	tokens := TokenizeFileWithIgnore(content, lang)

	hasIgnoreMark := false
	for _, tok := range tokens {
		if tok.IgnoreMark {
			hasIgnoreMark = true
			break
		}
	}
	if !hasIgnoreMark {
		t.Error("expected some tokens to be marked with IgnoreMark after # dupehound:ignore")
	}
}

// TestIsIgnoreComment verifies detection of ignore comment variants.
func TestIsIgnoreComment(t *testing.T) {
	goLang := LangForName("go")
	pyLang := LangForName("python")

	tests := []struct {
		line     string
		lang     interface{ getName() string }
		expected bool
	}{
		{"//dupehound:ignore", nil, false}, // nil lang
	}
	_ = tests

	// Go variants.
	if !isIgnoreComment("//dupehound:ignore", goLang) {
		t.Error("expected //dupehound:ignore to match in Go")
	}
	if !isIgnoreComment("// dupehound:ignore", goLang) {
		t.Error("expected '// dupehound:ignore' to match in Go")
	}
	if isIgnoreComment("// normal comment", goLang) {
		t.Error("expected normal comment to NOT match")
	}

	// Python variants.
	if !isIgnoreComment("# dupehound:ignore", pyLang) {
		t.Error("expected '# dupehound:ignore' to match in Python")
	}
	if isIgnoreComment("# regular comment", pyLang) {
		t.Error("expected regular comment to NOT match in Python")
	}
}

// TestMarkIgnoredBlocks_ExcludesFromDetection verifies that tokens following an
// inline ignore marker are actually excluded from clone detection. Three files:
// a.go and b.go are duplicates (should clone). c.go is also a duplicate but its
// body is preceded by //dupehound:ignore — the resulting clone group must contain
// only a.go and b.go, never c.go.
func TestMarkIgnoredBlocks_ExcludesFromDetection(t *testing.T) {
	dir := t.TempDir()

	body := `func compute() int {
	x := process()
	y := validate(x)
	z := transform(y)
	w := persist(z)
	notify(w)
	return w
}
`
	writeTestFile(t, dir, "a.go", "package main\n"+body)
	writeTestFile(t, dir, "b.go", "package main\n"+body)
	writeTestFile(t, dir, "c.go", "package main\n//dupehound:ignore\n"+body)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Clones) == 0 {
		t.Fatal("expected at least one clone between a.go and b.go")
	}

	// No clone instance may reference c.go.
	for _, c := range report.Clones {
		for _, inst := range c.Instances {
			if filepath.Base(inst.File) == "c.go" {
				t.Errorf("c.go appeared in a clone despite //dupehound:ignore marker (clone hash %s)", c.Hash)
			}
		}
	}
}

// TestIsIgnoreComment_AllSupportedLanguages parametrizes over every comment
// syntax dupehound supports. This is the multi-language barrier test for the
// inline-ignore feature (#18): a regression in any language's marker handling
// will fail here, not silently degrade detection.
func TestIsIgnoreComment_AllSupportedLanguages(t *testing.T) {
	cases := []struct {
		lang   string
		marker string
	}{
		// // line comments
		{"go", "//dupehound:ignore"},
		{"go", "// dupehound:ignore"},
		{"javascript", "// dupehound:ignore"},
		{"typescript", "// dupehound:ignore"},
		{"java", "// dupehound:ignore"},
		{"kotlin", "// dupehound:ignore"},
		{"rust", "// dupehound:ignore"},
		{"c", "// dupehound:ignore"},
		{"cpp", "// dupehound:ignore"},
		{"csharp", "// dupehound:ignore"},
		{"swift", "// dupehound:ignore"},
		{"scala", "// dupehound:ignore"},
		{"php", "// dupehound:ignore"},
		{"dart", "// dupehound:ignore"},
		// # line comments
		{"python", "# dupehound:ignore"},
		{"ruby", "# dupehound:ignore"},
		{"shell", "# dupehound:ignore"},
		{"elixir", "# dupehound:ignore"},
		{"r", "# dupehound:ignore"},
		// -- line comments
		{"sql", "-- dupehound:ignore"},
		{"lua", "-- dupehound:ignore"},
		// /* block comment on a single line
		{"go", "/* dupehound:ignore */"},
		{"java", "/* dupehound:ignore */"},
	}

	for _, tc := range cases {
		t.Run(tc.lang+"/"+tc.marker, func(t *testing.T) {
			lang := LangForName(tc.lang)
			if lang == nil {
				t.Fatalf("LangForName(%q) returned nil — language no longer registered?", tc.lang)
			}
			if !isIgnoreComment(tc.marker, lang) {
				t.Errorf("isIgnoreComment(%q, %s) = false, want true", tc.marker, tc.lang)
			}
		})
	}
}

// TestIsIgnoreComment_NegativeCases ensures unrelated comments are NOT treated
// as ignore markers. Catches over-eager matching.
func TestIsIgnoreComment_NegativeCases(t *testing.T) {
	cases := []struct {
		lang string
		line string
	}{
		{"go", "// just a regular comment"},
		{"go", "// TODO dupehound (no colon, no ignore)"},
		{"python", "# regular python comment"},
		{"sql", "-- normal sql comment"},
		{"java", "/* generic block comment */"},
		// Looks like a comment but is actually code (no leading marker).
		{"go", "x := 1 // dupehound:ignore"}, // not at start of line → not a comment line
	}
	for _, tc := range cases {
		t.Run(tc.lang+"/"+tc.line, func(t *testing.T) {
			lang := LangForName(tc.lang)
			if lang == nil {
				t.Fatalf("LangForName(%q) nil", tc.lang)
			}
			if isIgnoreComment(tc.line, lang) {
				t.Errorf("isIgnoreComment(%q, %s) = true, want false", tc.line, tc.lang)
			}
		})
	}
}

// TestInlineIgnore_PythonExcludesFromDetection is the multi-language end-to-end
// counterpart to TestMarkIgnoredBlocks_ExcludesFromDetection: it verifies that
// `# dupehound:ignore` works through the full Scan pipeline for Python (which
// uses different function-body marking than Go).
func TestInlineIgnore_PythonExcludesFromDetection(t *testing.T) {
	dir := t.TempDir()

	body := `def compute():
    x = process()
    y = validate(x)
    z = transform(y)
    w = persist(z)
    notify(w)
    return w
`
	writeTestFile(t, dir, "a.py", body)
	writeTestFile(t, dir, "b.py", body)
	writeTestFile(t, dir, "c.py", "# dupehound:ignore\n"+body)

	svc := NewScannerService()
	report, err := svc.Scan(ScanOptions{Path: dir, MinTokens: 10})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(report.Clones) == 0 {
		t.Fatal("expected clone between a.py and b.py")
	}
	for _, c := range report.Clones {
		for _, inst := range c.Instances {
			if filepath.Base(inst.File) == "c.py" {
				t.Errorf("c.py appeared despite # dupehound:ignore (clone %s)", c.Hash)
			}
		}
	}
}
