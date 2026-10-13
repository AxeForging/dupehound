package services

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

// FunctionDef holds extracted function definition metadata.
type FunctionDef struct {
	Name   string
	File   string
	Line   int
	Lang   string
	DefTok int // token index of the function name
	KwTok  int // token index of the introducing keyword, or -1 when there is none
	// BodyStart and BodyEnd bound the function's body in tokens (inclusive), or
	// are both -1 when it is unknown. A reference from inside the body is the
	// function calling itself, which does not make it used.
	BodyStart, BodyEnd int
	Visibility         defVisibility
}

func (d FunctionDef) containsToken(i int) bool {
	return d.BodyStart >= 0 && i >= d.BodyStart && i <= d.BodyEnd
}

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

// appendFuncDef records the identifier at tf.Tokens[tokIdx] as a function
// definition. Centralizes the FunctionDef struct literal used by every
// per-language scanner in extractFunctionDefs.
func appendFuncDef(defs []FunctionDef, tf TokenizedFile, langName string, kwIdx, tokIdx int) []FunctionDef {
	tok := tf.Tokens[tokIdx]
	bodyStart, bodyEnd := bodySpanAfter(tf.Funcs, tokIdx)
	// The span must be this function's own: a declaration without a body, or an
	// arrow function whose body is a bare expression, ends at a `;` or `=>`
	// before any block opens, and whatever block comes next belongs to
	// something else.
	for k := tokIdx + 1; k < bodyStart-1; k++ {
		if t := tf.Tokens[k]; t.Kind == TokOperator && (t.Text == ";" || t.Text == "=>") && tf.Tokens[k+1].Text != "{" {
			bodyStart, bodyEnd = -1, -1
			break
		}
	}
	return append(defs, FunctionDef{
		Name:   tok.OrigText,
		File:   tf.Path,
		Line:   tok.Line,
		Lang:   langName,
		DefTok: tokIdx,
		KwTok:  kwIdx,

		BodyStart: bodyStart,
		BodyEnd:   bodyEnd,
	})
}

// extractFunctionDefs extracts function name + location from a TokenizedFile.
func extractFunctionDefs(tf TokenizedFile, langName string) []FunctionDef {
	var defs []FunctionDef
	n := len(tf.Tokens)
	// bodySpanAfter binary-searches the spans, so they must be in source order.
	byStart := func(a, b FuncSpan) int { return a.Start - b.Start }
	if !slices.IsSortedFunc(tf.Funcs, byStart) {
		tf.Funcs = slices.Clone(tf.Funcs)
		slices.SortFunc(tf.Funcs, byStart)
	}
	if langName == "python" {
		// Python bodies are delimited by indentation, which the token stream
		// does not carry; the recorded spans are approximate. Without a
		// trustworthy body there is no telling a self-call from a real one.
		tf.Funcs = nil
	}
	switch langName {
	case "go", "rust", "kotlin", "swift", "scala", "php", "ruby", "lua", "python", "elixir", "javascript", "typescript":
		// Keyword-based: a func/fn/fun/def/function keyword followed by a name.
		funcKwSet := funcKeywords[langName]
		for i := 0; i < n-1; i++ {
			if tok := tf.Tokens[i]; tok.Kind != TokKeyword || !funcKwSet[tok.Text] {
				continue
			}
			if j := namedFuncIdent(tf.Tokens, i, langName == "go"); j >= 0 {
				defs = appendFuncDef(defs, tf, langName, i, j)
			}
		}
		if langName == "javascript" || langName == "typescript" {
			defs = append(defs, jsAssignedFunctionDefs(tf, langName)...)
			sort.Slice(defs, func(i, j int) bool { return defs[i].DefTok < defs[j].DefTok })
		}
	default:
		// For C, C++, Java, C#, Dart: heuristic — ident followed by `(` at function depth.
		for i := 0; i < n-1; i++ {
			tok := tf.Tokens[i]
			if tok.Kind == TokIdent && i+1 < n && tf.Tokens[i+1].Kind == TokOperator && tf.Tokens[i+1].Text == "(" {
				// Must be inside/at function scope boundary.
				if tf.InFunc != nil && i < len(tf.InFunc) && !tf.InFunc[i] {
					// Check if this token is at the boundary (the token before the func body).
					// Only include if the next matching { starts a new function body.
					// Heuristic: if token at position i is NOT inFunc but i+n tokens ARE, it's a def.
					// For simplicity, only take top-level idents followed by (.
					defs = appendFuncDef(defs, tf, langName, -1, i)
				}
			}
		}
	}

	return defs
}

// bodySpanAfter returns the body of the function whose name token is nameIdx:
// the first recorded function span that opens after it. Spans are produced in
// source order by markFunctionBodies.
func bodySpanAfter(funcs []FuncSpan, nameIdx int) (start, end int) {
	i := sort.Search(len(funcs), func(i int) bool { return funcs[i].Start > nameIdx })
	if i == len(funcs) {
		return -1, -1
	}
	return funcs[i].Start, funcs[i].End
}

// jsAssignedFunctionDefs finds module-level functions written as assignments:
// `const name = (…) => …`, `const name = async x => …`, `const name = function…`.
// Functions assigned inside another function are locals and are left alone.
func jsAssignedFunctionDefs(tf TokenizedFile, langName string) []FunctionDef {
	toks := tf.Tokens
	isOp := func(i int, text string) bool {
		return i < len(toks) && toks[i].Kind == TokOperator && toks[i].Text == text
	}
	isWord := func(i int, word string) bool {
		return i >= 0 && i < len(toks) && (toks[i].Text == word || toks[i].OrigText == word)
	}
	var defs []FunctionDef
	for i := 1; i+2 < len(toks); i++ {
		if toks[i].Kind != TokIdent || !isOp(i+1, "=") || tf.InFunc[i] {
			continue
		}
		if !isWord(i-1, "const") && !isWord(i-1, "let") && !isWord(i-1, "var") {
			continue
		}
		j := i + 2
		if isWord(j, "async") {
			j++
		}
		isFunc := isWord(j, "function")
		switch {
		case isFunc:
		case toks[j].Kind == TokIdent && isOp(j+1, "=>"):
			isFunc = true
		case isOp(j, "("):
			if closeIdx := matchDelimiter(toks, j, "(", ")"); closeIdx > 0 {
				// `) =>` or a TS return type `): T =>`.
				isFunc = isOp(closeIdx+1, "=>") || isOp(closeIdx+1, ":")
			}
		}
		if isFunc {
			defs = appendFuncDef(defs, tf, langName, i-1, i)
		}
	}
	return defs
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

// namedFuncIdent returns the index of the name introduced by the function
// keyword at kwIdx, or -1 when the keyword opens an anonymous function or a
// function type: `func(x int) bool {`, `fn(i32) -> i32`, `function () use ($x)`.
// With goReceiver set, one parenthesised receiver may precede the name.
func namedFuncIdent(tokens []Token, kwIdx int, goReceiver bool) int {
	isOp := func(i int, text string) bool {
		return i < len(tokens) && tokens[i].Kind == TokOperator && tokens[i].Text == text
	}
	j := kwIdx + 1
	if goReceiver {
		if isOp(j, "(") {
			if j = matchDelimiter(tokens, j, "(", ")") + 1; j == 0 {
				return -1
			}
		}
		// A Go declaration is `name(` or, with type parameters, `name[`.
		if j < len(tokens) && tokens[j].Kind == TokIdent && (isOp(j+1, "(") || isOp(j+1, "[")) {
			return j
		}
		return -1
	}
	for ; j < len(tokens); j++ {
		tok := tokens[j]
		if tok.Kind == TokIdent && tok.OrigText != "" {
			return j
		}
		if tok.Kind == TokOperator && (tok.Text == "(" || tok.Text == "{") {
			return -1
		}
	}
	return -1
}
