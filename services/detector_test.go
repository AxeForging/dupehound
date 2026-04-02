package services

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

// makeFile is a helper that tokenizes a Go source string into a TokenizedFile.
// Uses a synthetic path — preview lines won't load (use makeRealFile for that).
func makeFile(path, src string) TokenizedFile {
	return BuildTokenizedFile(path, src, goL())
}

// makeFileLang tokenizes with a specific language.
func makeFileLang(path, src string, lang string) TokenizedFile {
	return BuildTokenizedFile(path, src, LangForName(lang))
}

// makeRealFile writes src to a temp file and returns a TokenizedFile with a real path.
func makeRealFile(t *testing.T, dir, name, src string) TokenizedFile {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return BuildTokenizedFile(p, src, goL())
}

// --- Type-1 clones (exact structural copy) ---

func TestDetect_Type1Clone(t *testing.T) {
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	files := []TokenizedFile{
		makeFile("a.go", "package main\n\n"+block),
		makeFile("b.go", "package main\n\n"+block),
	}
	clones := Detect(files, 10, 1.0)
	if len(clones) == 0 {
		t.Fatal("expected at least one clone, got 0")
	}
	assertAllHaveTwoPlus(t, clones)
}

// --- Type-2 clones (renamed variables — key new capability) ---

func TestDetect_Type2Clone_RenamedVariable(t *testing.T) {
	a := makeFile("a.go", `package main
func process() {
	result := compute()
	validate(result)
	store(result)
	notify(result)
	return result
}
`)
	b := makeFile("b.go", `package main
func process() {
	output := compute()
	validate(output)
	store(output)
	notify(output)
	return output
}
`)
	clones := Detect([]TokenizedFile{a, b}, 10, 1.0)
	if len(clones) == 0 {
		t.Fatal("type-2 clone not detected: renamed variable 'result' → 'output' should still match")
	}
}

func TestDetect_Type2Clone_MultipleRenames(t *testing.T) {
	a := makeFile("a.go", `package main
func calc(x int, y int) int {
	sum := x + y
	diff := x - y
	product := sum * diff
	return product
}
`)
	b := makeFile("b.go", `package main
func calc(a int, b int) int {
	total := a + b
	delta := a - b
	result := total * delta
	return result
}
`)
	clones := Detect([]TokenizedFile{a, b}, 10, 1.0)
	if len(clones) == 0 {
		t.Fatal("type-2 clone not detected: multiple renamed variables should still match")
	}
}

// --- Structural differences should NOT match ---

func TestDetect_KeywordsAreDistinguishedInHash(t *testing.T) {
	// Keywords carry their verbatim text in the hash, so `if` ≠ `for`.
	// Identifier/number/string values are stripped, so only structure matters for them.
	ifToks := TokenizeFile("if x > 0 { process(x) }\n", goL())
	forToks := TokenizeFile("for x > 0 { process(x) }\n", goL())
	if len(ifToks) != len(forToks) {
		t.Fatalf("token count differs: if=%d for=%d", len(ifToks), len(forToks))
	}
	hIf := hashWindow(ifToks)
	hFor := hashWindow(forToks)
	if hIf == hFor {
		t.Error("'if' and 'for' blocks produced the same hash — keywords not distinguished")
	}
}

func TestDetect_IdentifiersAreNormalizedInHash(t *testing.T) {
	// Identifier names are stripped, so `result` and `output` hash identically.
	aToks := TokenizeFile("result := compute()\n", goL())
	bToks := TokenizeFile("output := compute()\n", goL())
	if len(aToks) != len(bToks) {
		t.Fatalf("token count differs: a=%d b=%d", len(aToks), len(bToks))
	}
	if hashWindow(aToks) != hashWindow(bToks) {
		t.Error("renamed variable produced different hash — type-2 detection broken")
	}
}

// --- Comment handling ---

func TestDetect_CommentInsideBlock_StillMatches(t *testing.T) {
	// Same logic, but one file has comments interspersed.
	// After tokenization, comments are stripped, so they should match.
	a := makeFile("a.go", `package main
func process() {
	x := compute()
	// normalize the result
	validate(x)
	// store it
	store(x)
	return x
}
`)
	b := makeFile("b.go", `package main
func process() {
	x := compute()
	validate(x)
	store(x)
	return x
}
`)
	clones := Detect([]TokenizedFile{a, b}, 10, 1.0)
	if len(clones) == 0 {
		t.Fatal("blocks that differ only by comments should be detected as clones")
	}
}

// --- Window merging (no overlap noise) ---

func TestDetect_MergeAdjacentWindows_SingleCloneReported(t *testing.T) {
	// A large duplicate block should be reported as ONE clone, not many overlapping windows.
	block := `package main
func bigHelper() {
	a := compute()
	b := validate(a)
	c := transform(b)
	d := store(c)
	e := notify(d)
	f := log(e)
	g := audit(f)
	return g
}
`
	files := []TokenizedFile{makeFile("a.go", block), makeFile("b.go", block)}
	clones := Detect(files, 5, 1.0)

	// Count clones that span the full helper function (≥8 lines).
	// There should be exactly 1, not one per overlapping window.
	largeClonesA := 0
	for _, c := range clones {
		for _, inst := range c.Instances {
			if inst.File == "a.go" && c.LineCount >= 8 {
				largeClonesA++
			}
		}
	}
	if largeClonesA > 1 {
		t.Errorf("expected 1 large clone for the helper body, got %d — overlapping windows not merged", largeClonesA)
	}
	if largeClonesA == 0 {
		t.Error("expected at least 1 clone spanning the duplicate function body")
	}
}

// --- Three-way clones ---

func TestDetect_ThreeWayClone(t *testing.T) {
	block := `package main
func dup() {
	x := compute()
	validate(x)
	store(x)
	notify(x)
	return x
}
`
	files := []TokenizedFile{
		makeFile("a.go", block),
		makeFile("b.go", block),
		makeFile("c.go", block),
	}
	clones := Detect(files, 10, 1.0)
	if len(clones) == 0 {
		t.Fatal("expected at least one 3-way clone")
	}
	// Find a clone with 3 instances
	found := false
	for _, c := range clones {
		if len(c.Instances) == 3 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a clone with 3 instances for three identical files")
	}
}

// --- Minimum token threshold ---

func TestDetect_MinTokensThreshold(t *testing.T) {
	// A 3-token block should not be reported with minTokens=20.
	a := makeFile("a.go", "package main\nfunc a() { x := 1 }\n")
	b := makeFile("b.go", "package main\nfunc b() { x := 1 }\n")
	clones := Detect([]TokenizedFile{a, b}, 20, 1.0)
	// No clone should span more than the full file if there aren't 20 matching tokens.
	for _, c := range clones {
		if c.TokenCount > 20 {
			t.Errorf("unexpected large clone with minTokens=20: %d tokens", c.TokenCount)
		}
	}
}

// --- No duplicates ---

func TestDetect_NoDuplicates(t *testing.T) {
	a := makeFile("a.go", "package main\nfunc foo() { a()\nb()\nc() }\n")
	b := makeFile("b.go", "package main\nfunc bar() { x()\ny()\nz() }\n")
	// These have very different structure; with a large minTokens they shouldn't match.
	clones := Detect([]TokenizedFile{a, b}, 15, 1.0)
	_ = clones // small files may share tiny windows; we just ensure no panic
}

// --- Same-file internal duplicate ---

func TestDetect_SameFileInternalDuplicate(t *testing.T) {
	src := `package main
func first() {
	x := compute()
	validate(x)
	store(x)
	notify(x)
	return x
}
func second() {
	x := compute()
	validate(x)
	store(x)
	notify(x)
	return x
}
`
	files := []TokenizedFile{makeFile("a.go", src)}
	clones := Detect(files, 10, 1.0)
	if len(clones) == 0 {
		t.Fatal("expected clone for repeated function body within same file")
	}
	// The clone should have 2 instances in the same file
	found := false
	for _, c := range clones {
		if len(c.Instances) >= 2 {
			filesInClone := make(map[string]int)
			for _, inst := range c.Instances {
				filesInClone[inst.File]++
			}
			if filesInClone["a.go"] >= 2 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("expected a clone with 2+ instances in the same file")
	}
}

// --- Line range accuracy ---

func TestDetect_LineRangeAccurate(t *testing.T) {
	src := `package main
func helper() {
	x := compute()
	validate(x)
	store(x)
	return x
}
`
	a := makeFile("a.go", src)
	b := makeFile("b.go", src)
	clones := Detect([]TokenizedFile{a, b}, 10, 1.0)
	if len(clones) == 0 {
		t.Fatal("expected clones")
	}
	for _, c := range clones {
		for _, inst := range c.Instances {
			if inst.StartLine < 1 {
				t.Errorf("StartLine should be ≥1, got %d", inst.StartLine)
			}
			if inst.EndLine < inst.StartLine {
				t.Errorf("EndLine (%d) < StartLine (%d)", inst.EndLine, inst.StartLine)
			}
		}
	}
}

// --- Preview lines are original source ---

func TestDetect_PreviewLinesAreOriginalSource(t *testing.T) {
	dir := t.TempDir()
	src := `package main
func helper() {
	// this comment should appear in preview
	x := compute()
	return x
}
`
	a := makeRealFile(t, dir, "a.go", src)
	b := makeRealFile(t, dir, "b.go", src)
	clones := Detect([]TokenizedFile{a, b}, 5, 1.0)
	if len(clones) == 0 {
		t.Fatal("expected clones")
	}
	// Check that at least one instance has non-empty Lines
	for _, c := range clones {
		for _, inst := range c.Instances {
			if len(inst.Lines) == 0 {
				t.Error("clone instance has no preview lines")
			}
		}
	}
}

// --- Python type-2 ---

func TestDetect_Python_Type2Clone(t *testing.T) {
	a := makeFileLang("a.py", `def process():
    result = compute()
    validate(result)
    store(result)
    notify(result)
    return result
`, "python")
	b := makeFileLang("b.py", `def process():
    output = compute()
    validate(output)
    store(output)
    notify(output)
    return output
`, "python")
	clones := Detect([]TokenizedFile{a, b}, 10, 1.0)
	if len(clones) == 0 {
		t.Fatal("Python type-2 clone not detected")
	}
}

// --- Edge cases: empty / tiny files ---

func TestDetect_ZeroTokenFile_NoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic on zero-token files: %v", r)
		}
	}()
	files := []TokenizedFile{
		makeFile("empty1.go", ""),
		makeFile("empty2.go", ""),
	}
	clones := Detect(files, 5, 1.0)
	_ = clones // nil is expected; what matters is no panic
}

func TestDetect_MinTokensLargerThanFile(t *testing.T) {
	// When minTokens > token count of every file, no window can be formed.
	a := makeFile("a.go", "package main\n")
	b := makeFile("b.go", "package main\n")
	clones := Detect([]TokenizedFile{a, b}, 1000, 1.0)
	if len(clones) != 0 {
		t.Errorf("expected 0 clones when minTokens > file size, got %d", len(clones))
	}
}

// --- N-way clones ---

func TestDetect_FiveWayClone(t *testing.T) {
	block := `package main
func dup() {
	x := compute()
	validate(x)
	store(x)
	notify(x)
	return x
}
`
	files := []TokenizedFile{
		makeFile("a.go", block), makeFile("b.go", block), makeFile("c.go", block),
		makeFile("d.go", block), makeFile("e.go", block),
	}
	clones := Detect(files, 10, 1.0)
	found := false
	for _, c := range clones {
		if len(c.Instances) == 5 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a clone with 5 instances for five identical files")
	}
}

// --- Three copies in one file ---

func TestDetect_ThreeCopiesInOneFile(t *testing.T) {
	body := "\tx := compute()\n\tvalidate(x)\n\tstore(x)\n\tnotify(x)\n\treturn x\n"
	src := "package main\nfunc a() {\n" + body + "}\nfunc b() {\n" + body + "}\nfunc c() {\n" + body + "}\n"
	files := []TokenizedFile{makeFile("a.go", src)}
	clones := Detect(files, 10, 1.0)
	found := false
	for _, c := range clones {
		if len(c.Instances) >= 2 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected at least one clone with 2+ instances for three repeated bodies in one file")
	}
}

// --- TokenCount accuracy ---

func TestDetect_TokenCountPositive(t *testing.T) {
	src := `package main
func f() {
	x := compute()
	validate(x)
	store(x)
	return x
}
`
	a := makeFile("a.go", src)
	b := makeFile("b.go", src)
	clones := Detect([]TokenizedFile{a, b}, 5, 1.0)
	if len(clones) == 0 {
		t.Fatal("expected clones")
	}
	for _, c := range clones {
		if c.TokenCount <= 0 {
			t.Errorf("clone has non-positive TokenCount: %d", c.TokenCount)
		}
		if c.TokenCount < 5 {
			t.Errorf("TokenCount %d is below minTokens 5 — extend logic broken", c.TokenCount)
		}
	}
}

// --- Cross-language structural comparison ---

func TestDetect_CrossLanguage_NoFalseMatchOnKeyword(t *testing.T) {
	// Go uses `func` and `:=`; Python uses `def` and `=`.
	// Different keywords and operators mean their windows won't hash the same.
	goFile := makeFile("a.go", `package main
func process() {
	x := compute()
	validate(x)
	store(x)
	return x
}
`)
	pyFile := makeFileLang("b.py", `def process():
    x = compute()
    validate(x)
    store(x)
    return x
`, "python")
	clones := Detect([]TokenizedFile{goFile, pyFile}, 10, 1.0)
	// Go `func`/`:=` vs Python `def`/`=` differ in keyword text and operator text,
	// so a full-function window should not match. Any clone reported must have ≥2 instances.
	for _, c := range clones {
		if len(c.Instances) < 2 {
			t.Errorf("clone has fewer than 2 instances: %+v", c)
		}
	}
}

// --- Clone type classification ---

func TestDetect_Type1Clone_ClassifiedCorrectly(t *testing.T) {
	// Truly identical code (same identifiers, same literals) → type-1, similarity 1.0.
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	files := []TokenizedFile{
		makeFile("a.go", "package main\n\n"+block),
		makeFile("b.go", "package main\n\n"+block),
	}
	clones := Detect(files, 10, 1.0)
	if len(clones) == 0 {
		t.Fatal("expected at least one clone")
	}
	for _, c := range clones {
		if c.Type != "type-1" {
			t.Errorf("identical code should be type-1, got %q", c.Type)
		}
		if c.Similarity != 1.0 {
			t.Errorf("type-1 similarity should be 1.0, got %f", c.Similarity)
		}
	}
}

func TestDetect_Type2Clone_ClassifiedCorrectly(t *testing.T) {
	// Same structure, renamed variable → type-2, similarity 1.0.
	a := makeFile("a.go", `package main
func process() {
	result := compute()
	validate(result)
	store(result)
	notify(result)
	return result
}
`)
	b := makeFile("b.go", `package main
func process() {
	output := compute()
	validate(output)
	store(output)
	notify(output)
	return output
}
`)
	clones := Detect([]TokenizedFile{a, b}, 10, 1.0)
	if len(clones) == 0 {
		t.Fatal("expected type-2 clone")
	}
	for _, c := range clones {
		if c.Type != "type-2" {
			t.Errorf("renamed variable should be type-2, got %q", c.Type)
		}
		if c.Similarity != 1.0 {
			t.Errorf("type-2 similarity should be 1.0, got %f", c.Similarity)
		}
	}
}

func TestDetect_Type2Clone_DifferentLiterals(t *testing.T) {
	// Same structure, different numeric literals → type-2.
	a := makeFile("a.go", `package main
func config() {
	timeout := 30
	retries := 5
	delay := 100
	batch := 50
	return timeout
}
`)
	b := makeFile("b.go", `package main
func config() {
	timeout := 60
	retries := 10
	delay := 200
	batch := 100
	return timeout
}
`)
	clones := Detect([]TokenizedFile{a, b}, 10, 1.0)
	if len(clones) == 0 {
		t.Fatal("expected type-2 clone for different literals")
	}
	for _, c := range clones {
		if c.Type != "type-2" {
			t.Errorf("different literals should be type-2, got %q", c.Type)
		}
	}
}

func TestDetect_Type2Clone_SameFileInternalDuplicate(t *testing.T) {
	// Same-file blocks with different function names → type-2 (func names differ).
	body := "\tx := compute()\n\tvalidate(x)\n\tstore(x)\n\tnotify(x)\n\treturn x\n"
	src := "package main\nfunc a() {\n" + body + "}\nfunc b() {\n" + body + "}\n"
	files := []TokenizedFile{makeFile("a.go", src)}
	clones := Detect(files, 10, 1.0)
	if len(clones) == 0 {
		t.Fatal("expected clone")
	}
	for _, c := range clones {
		if c.Type != "type-2" {
			t.Errorf("same-file blocks with different func names should be type-2, got %q", c.Type)
		}
	}
}

func TestDetect_AllClonesHaveType(t *testing.T) {
	// Every clone returned by Detect must have a non-empty Type.
	block := "func helper() {\n\tx := compute()\n\tprocess(x)\n\tlog(x)\n\treturn x\n}\n"
	files := []TokenizedFile{
		makeFile("a.go", "package main\n\n"+block),
		makeFile("b.go", "package main\n\n"+block),
		makeFile("c.go", "package main\n\n"+block),
	}
	clones := Detect(files, 5, 1.0)
	for _, c := range clones {
		if c.Type == "" {
			t.Error("clone has empty Type")
		}
		if c.Similarity <= 0 {
			t.Errorf("clone has non-positive Similarity: %f", c.Similarity)
		}
	}
}

// --- Type-3 fuzzy detection ---

func TestDetect_Type3_ExtraStatement(t *testing.T) {
	// Two functions with mostly same structure but one has an extra if-block
	// (different keyword structure breaks exact detection).
	dir := t.TempDir()
	a := makeRealFile(t, dir, "a.go", `package main
func process() {
	x := compute()
	validate(x)
	transform(x)
	store(x)
	notify(x)
	log(x)
	return x
}
`)
	b := makeRealFile(t, dir, "b.go", `package main
func process() {
	x := compute()
	validate(x)
	transform(x)
	if x > 0 {
		store(x)
	}
	notify(x)
	log(x)
	return x
}
`)
	clones := Detect([]TokenizedFile{a, b}, 10, 0.50)
	var found bool
	for _, c := range clones {
		if c.Type == "type-3" {
			found = true
			if c.Similarity >= 1.0 {
				t.Errorf("type-3 similarity should be < 1.0, got %f", c.Similarity)
			}
			if c.Similarity < 0.50 {
				t.Errorf("type-3 similarity should be >= threshold, got %f", c.Similarity)
			}
		}
	}
	if !found {
		t.Error("expected type-3 clone for near-miss blocks with extra statement")
	}
}

func TestDetect_Type3_SwappedStatement(t *testing.T) {
	// Two functions with a for-loop in one replaced by a switch in the other.
	// Structural difference breaks exact matching but most code is shared.
	dir := t.TempDir()
	a := makeRealFile(t, dir, "a.go", `package main
func process() {
	x := compute()
	validate(x)
	for i := range x {
		transform(i)
	}
	store(x)
	notify(x)
	log(x)
	return x
}
`)
	b := makeRealFile(t, dir, "b.go", `package main
func process() {
	x := compute()
	validate(x)
	switch x {
	case 0:
		transform(x)
	}
	store(x)
	notify(x)
	log(x)
	return x
}
`)
	clones := Detect([]TokenizedFile{a, b}, 10, 0.50)
	var found bool
	for _, c := range clones {
		if c.Type == "type-3" {
			found = true
		}
	}
	if !found {
		t.Error("expected type-3 clone for near-miss blocks with swapped statements")
	}
}

func TestDetect_Type3_CompletelyDifferent_NotDetected(t *testing.T) {
	// Completely different code → no type-3 clone.
	dir := t.TempDir()
	a := makeRealFile(t, dir, "a.go", `package main
func mathStuff() {
	x := add(1, 2)
	y := multiply(x, 3)
	z := divide(y, 4)
	result := subtract(z, 5)
	return result
}
`)
	b := makeRealFile(t, dir, "b.go", `package main
func ioStuff() {
	f := openFile("data.txt")
	defer closeFile(f)
	lines := readLines(f)
	for _, line := range lines {
		processLine(line)
	}
}
`)
	clones := Detect([]TokenizedFile{a, b}, 10, 0.70)
	for _, c := range clones {
		if c.Type == "type-3" {
			t.Error("completely different code should not produce a type-3 clone")
		}
	}
}

func TestDetect_Similarity1_DisablesFuzzy(t *testing.T) {
	// With minSimilarity=1.0, only exact matches are returned (no type-3).
	dir := t.TempDir()
	a := makeRealFile(t, dir, "a.go", `package main
func process() {
	x := compute()
	validate(x)
	transform(x)
	store(x)
	notify(x)
	log(x)
	return x
}
`)
	b := makeRealFile(t, dir, "b.go", `package main
func process() {
	x := compute()
	validate(x)
	transform(x)
	if x > 0 {
		store(x)
	}
	notify(x)
	log(x)
	return x
}
`)
	clones := Detect([]TokenizedFile{a, b}, 10, 1.0)
	for _, c := range clones {
		if c.Type == "type-3" {
			t.Error("similarity=1.0 should disable type-3 detection")
		}
	}
}

func TestDetect_Type3_SimilarityInRange(t *testing.T) {
	// All type-3 clones must have similarity in [threshold, 1.0).
	dir := t.TempDir()
	a := makeRealFile(t, dir, "a.go", `package main
func process() {
	x := compute()
	validate(x)
	transform(x)
	store(x)
	notify(x)
	log(x)
	return x
}
`)
	b := makeRealFile(t, dir, "b.go", `package main
func process() {
	x := compute()
	validate(x)
	transform(x)
	if x > 0 {
		store(x)
	}
	notify(x)
	log(x)
	return x
}
`)
	threshold := 0.50
	clones := Detect([]TokenizedFile{a, b}, 10, threshold)
	for _, c := range clones {
		if c.Type == "type-3" {
			if c.Similarity < threshold || c.Similarity >= 1.0 {
				t.Errorf("type-3 similarity %f should be in [%f, 1.0)", c.Similarity, threshold)
			}
		}
	}
}

// --- Fuzzy bucket handling ---

func TestDetect_Type3_LargeBucketNotSkipped(t *testing.T) {
	// Generate many files that each have a unique structural middle section
	// (different keywords) so they don't match exactly, but share enough
	// mini-windows in the prologue/epilogue to create large fuzzy buckets.
	// With the old >100 skip, these would be silently dropped.
	dir := t.TempDir()

	keywords := []string{
		"if", "for", "switch", "select", "defer",
		"go", "if", "for", "switch", "select",
	}

	var files []TokenizedFile
	for i := 0; i < len(keywords); i++ {
		// Each file has a unique keyword in the middle, breaking exact matches,
		// but the surrounding code (prologue + epilogue) shares mini-window hashes.
		src := fmt.Sprintf(`package main
func process%d() {
	a := compute()
	b := validate(a)
	c := transform(b)
	%s {
		d := store(c)
		notify(d)
	}
	e := finalize(c)
	log(e)
	archive(e)
	return e
}
`, i, keywords[i])
		files = append(files, makeRealFile(t, dir, fmt.Sprintf("f%d.go", i), src))
	}

	clones := Detect(files, 10, 0.50)

	// Should find type-3 clones between files that share prologue/epilogue
	// but differ in the keyword section.
	var foundType3 bool
	for _, c := range clones {
		if c.Type == "type-3" {
			foundType3 = true
			break
		}
	}
	if !foundType3 {
		t.Error("large bucket should not prevent type-3 detection — expected at least one type-3 clone")
	}
}

// --- helpers ---

func assertAllHaveTwoPlus(t *testing.T, clones []domain.Clone) {
	t.Helper()
	for _, c := range clones {
		if len(c.Instances) < 2 {
			t.Errorf("clone has fewer than 2 instances: %+v", c)
		}
	}
}
