package services

import (
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

var goLang = &domain.Language{
	Name:        "go",
	Extensions:  []string{".go"},
	LineComment: "//",
	BlockStart:  "/*",
	BlockEnd:    "*/",
}

var pyLang = &domain.Language{
	Name:        "python",
	Extensions:  []string{".py"},
	LineComment: "#",
	BlockStart:  "",
	BlockEnd:    "",
}

func TestNormalizeFile_StripLineComments(t *testing.T) {
	content := "x := 1 // set x\ny := 2 // set y\n"
	got := NormalizeFile(content, goLang)
	if len(got.Lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(got.Lines))
	}
	if got.Lines[0] != "x := 1" {
		t.Errorf("line 0: got %q, want %q", got.Lines[0], "x := 1")
	}
	if got.Lines[1] != "y := 2" {
		t.Errorf("line 1: got %q, want %q", got.Lines[1], "y := 2")
	}
}

func TestNormalizeFile_StripBlockComments(t *testing.T) {
	content := "/* header */\nfunc foo() {\n\treturn 1\n}\n"
	got := NormalizeFile(content, goLang)
	if len(got.Lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(got.Lines), got.Lines)
	}
	if got.Lines[0] != "func foo() {" {
		t.Errorf("line 0: got %q", got.Lines[0])
	}
}

func TestNormalizeFile_MultilineBlockComment(t *testing.T) {
	content := "code1\n/*\n  comment\n*/\ncode2\n"
	got := NormalizeFile(content, goLang)
	if len(got.Lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(got.Lines), got.Lines)
	}
	if got.Lines[0] != "code1" || got.Lines[1] != "code2" {
		t.Errorf("got %v", got.Lines)
	}
}

func TestNormalizeFile_SkipsBlankLines(t *testing.T) {
	content := "\n\nfoo()\n\nbar()\n\n"
	got := NormalizeFile(content, goLang)
	if len(got.Lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(got.Lines))
	}
}

func TestNormalizeFile_LineNumbers(t *testing.T) {
	content := "// comment\nfoo()\n\nbar()\n"
	got := NormalizeFile(content, goLang)
	if len(got.LineNumbers) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got.LineNumbers))
	}
	if got.LineNumbers[0] != 2 {
		t.Errorf("first code line should be line 2, got %d", got.LineNumbers[0])
	}
	if got.LineNumbers[1] != 4 {
		t.Errorf("second code line should be line 4, got %d", got.LineNumbers[1])
	}
}

func TestNormalizeFile_CollapseWhitespace(t *testing.T) {
	content := "x  =   1\n"
	got := NormalizeFile(content, goLang)
	if len(got.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(got.Lines))
	}
	if got.Lines[0] != "x = 1" {
		t.Errorf("got %q, want %q", got.Lines[0], "x = 1")
	}
}

func TestNormalizeFile_PythonHashComment(t *testing.T) {
	content := "# header\nx = 1 # inline\ny = 2\n"
	got := NormalizeFile(content, pyLang)
	if len(got.Lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(got.Lines))
	}
	if got.Lines[0] != "x = 1" {
		t.Errorf("got %q", got.Lines[0])
	}
}

func TestDetectLanguage_Go(t *testing.T) {
	lang := DetectLanguage("main.go")
	if lang == nil || lang.Name != "go" {
		t.Errorf("expected go, got %v", lang)
	}
}

func TestDetectLanguage_Unsupported(t *testing.T) {
	lang := DetectLanguage("file.xyz")
	if lang != nil {
		t.Errorf("expected nil for unsupported extension")
	}
}

func TestDetectLanguage_CaseInsensitive(t *testing.T) {
	lang := DetectLanguage("file.R")
	if lang == nil || lang.Name != "r" {
		t.Errorf("expected r, got %v", lang)
	}
}
