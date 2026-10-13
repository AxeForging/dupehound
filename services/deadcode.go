package services

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

// Dead-function analysis: definitions in, usages streamed through, unreferenced functions out.

// defVisibility is how far away a definition can be called from. Narrowing the
// search to that reach is what lets a common name be reported: an unexported Go
// `describe` is dead if its own package never mentions it, however many other
// packages have a `describe` of their own.
type defVisibility uint8

const (
	visRepo defVisibility = iota // callable from anywhere in the scanned tree
	visTree                      // callable from the definition's directory and below
	visDir                       // callable from the definition's directory only
	visFile                      // callable from the definition's file only
)

// pendingDef is a definition no usage has been seen for yet.
type pendingDef struct {
	def  FunctionDef
	dir  string
	used bool
}

// deadCodeAnalysis finds functions that nothing refers to.
//
// It is built from the definitions and then fed usages one file at a time, so
// the files that only contribute usages — generated code, excluded tests,
// templates — never have to be held in memory together. Memory is proportional
// to the number of definitions, not to the size of the repository.
type deadCodeAnalysis struct {
	all     []*pendingDef            // every definition, in report order
	pending map[string][]*pendingDef // name → definitions still unreferenced
}

// newDeadCodeAnalysis collects the function definitions of defFiles. Nothing is
// considered used until observeSource / observeText say so.
func newDeadCodeAnalysis(defFiles []TokenizedFile) *deadCodeAnalysis {
	a := &deadCodeAnalysis{pending: make(map[string][]*pendingDef)}
	classes := repoClassNames(defFiles)
	included := includedFileNames(defFiles)
	for _, tf := range defFiles {
		lang := DetectLanguage(tf.Path)
		if lang == nil {
			continue
		}
		defs := extractFunctionDefs(tf, lang.Name)
		for k, def := range defs {
			// A body-less definition (`func f() {}`, a declaration) has no span
			// of its own; do not let it borrow the next function's.
			if k+1 < len(defs) && def.BodyStart > defs[k+1].DefTok {
				def.BodyStart, def.BodyEnd = -1, -1
			}
			if isFrameworkInvoked(tf, def, classes) {
				continue
			}
			def.Visibility = visibilityOf(tf, def)
			if def.Visibility == visFile && included[filepath.Base(tf.Path)] {
				// The file is textually included elsewhere (a header, or a C
				// source pulled in with #include): its file-local functions
				// are the includer's too.
				def.Visibility = visRepo
			}
			p := &pendingDef{def: def, dir: filepath.Dir(tf.Path)}
			a.all = append(a.all, p)
			a.pending[def.Name] = append(a.pending[def.Name], p)
		}
	}
	return a
}

// observeSource records every reference to a pending definition made by the
// tokens of one source file: identifiers, and identifier-shaped words inside
// string literals (reflection, `getattr`, route tables and the like name their
// target in a string).
func (a *deadCodeAnalysis) observeSource(path string, tokens []Token) {
	if len(a.pending) == 0 {
		return
	}
	dir := filepath.Dir(path)
	for i, tok := range tokens {
		switch tok.Kind {
		case TokIdent:
			a.reference(tok.OrigText, path, dir, i, false)
		case TokString:
			forEachWord(tok.OrigText, func(word string) {
				a.reference(word, path, dir, -1, false)
			})
		}
	}
}

// observeText records references made from a non-source file — a template, a
// config file, documentation. Only definitions callable from anywhere can be
// reached this way.
func (a *deadCodeAnalysis) observeText(path, text string) {
	if len(a.pending) == 0 {
		return
	}
	forEachWord(text, func(word string) {
		a.reference(word, path, "", -1, true)
	})
}

// reference marks as used every pending definition of name that the given
// location can reach. tokIdx is the referring token's index in its file, used to
// tell a definition's own name token apart from a real reference.
func (a *deadCodeAnalysis) reference(name, path, dir string, tokIdx int, textOnly bool) {
	defs, ok := a.pending[name]
	if !ok {
		return
	}
	remaining := defs[:0]
	for _, p := range defs {
		if p.reachedFrom(path, dir, tokIdx, textOnly) {
			p.used = true
		} else {
			remaining = append(remaining, p)
		}
	}
	if len(remaining) == 0 {
		delete(a.pending, name)
	} else {
		a.pending[name] = remaining
	}
}

func (p *pendingDef) reachedFrom(path, dir string, tokIdx int, textOnly bool) bool {
	if textOnly {
		return p.def.Visibility == visRepo
	}
	if path == p.def.File && (tokIdx == p.def.DefTok || p.def.containsToken(tokIdx)) {
		return false // the definition itself, or the function calling itself
	}
	switch p.def.Visibility {
	case visFile:
		return path == p.def.File
	case visDir:
		return dir == p.dir
	case visTree:
		return dir == p.dir || strings.HasPrefix(dir, p.dir+string(filepath.Separator))
	default:
		return true
	}
}

// dead returns the definitions nothing referred to.
func (a *deadCodeAnalysis) dead() []domain.DeadFunc {
	var dead []domain.DeadFunc
	for _, p := range a.all {
		if p.used {
			continue
		}
		confidence := domain.ConfidenceHigh
		if p.def.Visibility == visRepo {
			confidence = domain.ConfidenceLow
		}
		dead = append(dead, domain.DeadFunc{
			File: p.def.File, Line: p.def.Line, Name: p.def.Name, Language: p.def.Lang, Confidence: confidence,
		})
	}
	// Certain findings first; definition order is kept within each group.
	sort.SliceStable(dead, func(i, j int) bool {
		return dead[i].Confidence == domain.ConfidenceHigh && dead[j].Confidence != domain.ConfidenceHigh
	})
	return dead
}

// findDeadFunctions analyses tokenized files and returns the functions defined
// in defFiles that nothing in usageFiles refers to.
//
// defFiles is the set the user wants findings reported for; usageFiles is the
// (usually broader) set searched for references. This is a heuristic — it
// matches names, it does not resolve calls — so every rule errs toward calling
// a function used.
func findDeadFunctions(defFiles, usageFiles []TokenizedFile) []domain.DeadFunc {
	a := newDeadCodeAnalysis(defFiles)
	for _, tf := range usageFiles {
		a.observeSource(tf.Path, tf.Tokens)
	}
	return a.dead()
}

// forEachWord calls fn for every identifier-shaped word in text.
func forEachWord(text string, fn func(string)) {
	start := -1
	for i := 0; i <= len(text); i++ {
		if i < len(text) && isIdentChar(text[i]) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			if c := text[start]; c == '_' || isLetter(c) {
				fn(text[start:i])
			}
			start = -1
		}
	}
}

// referenceFileExts are non-source files that can name a function: templates
// that call helpers, config that wires handlers, documentation of a public API.
var referenceFileExts = strSet(
	".html", ".htm", ".erb", ".haml", ".slim", ".jinja", ".jinja2", ".j2", ".twig", ".ejs", ".hbs",
	".mustache", ".tmpl", ".tpl", ".gotmpl", ".gohtml", ".vue", ".svelte", ".astro",
	".yml", ".yaml", ".json", ".toml", ".xml", ".ini", ".cfg", ".md", ".mdx", ".rst", ".txt", ".s",
)

// scanDeadCode reports the functions defined in defFiles that nothing under
// root refers to.
//
// References are gathered from every file under root, not just the ones being
// scanned for clones: excluded tests, generated code and templates all call
// hand-written functions. Those extra files are read, searched and released one
// at a time, so they add nothing to peak memory.
func scanDeadCode(root string, defFiles []TokenizedFile, maxFileSize int64) []domain.DeadFunc {
	a := newDeadCodeAnalysis(defFiles)
	seen := make(map[string]bool, len(defFiles))
	for _, tf := range defFiles {
		seen[tf.Path] = true
		a.observeSource(tf.Path, tf.Tokens)
	}

	// filepath.Walk, like collectFiles: the two must agree on what the tree is.
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || len(a.pending) == 0 {
			return nil
		}
		if info.IsDir() {
			if path != root && isHiddenOrVendored(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if seen[path] {
			return nil
		}
		lang := DetectLanguage(path)
		if lang == nil && !referenceFileExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		if maxFileSize > 0 && info.Size() > maxFileSize {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || isBinaryData(data) {
			return nil
		}
		if lang != nil {
			a.observeSource(path, TokenizeFile(string(data), lang))
		} else {
			a.observeText(path, string(data))
		}
		return nil
	})
	return a.dead()
}
