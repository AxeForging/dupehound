package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

func mkClone(hash string, instances int) domain.Clone {
	c := domain.Clone{Hash: hash, Type: domain.CloneType2, Similarity: 1, LineCount: 10}
	for i := 0; i < instances; i++ {
		c.Instances = append(c.Instances, domain.CloneInstance{File: "/f.go", StartLine: 1 + i*20, EndLine: 10 + i*20})
	}
	return c
}

func TestBaseline_WriteLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "base.json")
	clones := []domain.Clone{mkClone("aaaa", 2), mkClone("bbbb", 3)}
	if err := WriteBaseline(path, clones, 50); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.MinTokens != 50 {
		t.Errorf("MinTokens: want 50, got %d", b.MinTokens)
	}
	if b.Fingerprints["aaaa"] != 2 || b.Fingerprints["bbbb"] != 3 {
		t.Errorf("fingerprints roundtrip mismatch: %v", b.Fingerprints)
	}
}

func TestBaseline_WriteExcludesSuppressed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "base.json")
	sup := mkClone("supp", 2)
	sup.Suppressed = true
	if err := WriteBaseline(path, []domain.Clone{sup, mkClone("keep", 2)}, 50); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Fingerprints["supp"]; ok {
		t.Error("suppressed clone must not be recorded in the baseline")
	}
	if _, ok := b.Fingerprints["keep"]; !ok {
		t.Error("non-suppressed clone missing from baseline")
	}
}

func TestBaseline_Apply_KnownVsNew(t *testing.T) {
	b := &BaselineFile{Version: 1, Fingerprints: map[string]int{"known": 2}}
	clones := []domain.Clone{mkClone("known", 2), mkClone("fresh", 2)}
	known, newCount := ApplyBaseline(clones, b)
	if known != 1 || newCount != 1 {
		t.Fatalf("want 1 known / 1 new, got %d / %d", known, newCount)
	}
	if !clones[0].Baseline {
		t.Error("known clone should be marked Baseline")
	}
	if clones[1].Baseline {
		t.Error("fresh clone must NOT be marked Baseline")
	}
}

// The ratchet must catch pasting ANOTHER copy of already-known duplicated
// code: same fingerprint, grown instance count → new.
func TestBaseline_Apply_GrownInstanceCountIsNew(t *testing.T) {
	b := &BaselineFile{Version: 1, Fingerprints: map[string]int{"grown": 2}}
	clones := []domain.Clone{mkClone("grown", 3)}
	known, newCount := ApplyBaseline(clones, b)
	if known != 0 || newCount != 1 {
		t.Errorf("clone with grown instance count must be new: known=%d new=%d", known, newCount)
	}
	if clones[0].Baseline {
		t.Error("grown clone must not be marked Baseline")
	}
}

func TestBaseline_Load_Missing(t *testing.T) {
	if _, err := LoadBaseline(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("missing baseline file must be a hard error")
	}
}

func TestBaseline_Load_Corrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBaseline(path); err == nil {
		t.Error("corrupt baseline file must be a hard error")
	}
}

func TestBaseline_Load_WrongVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v99.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"fingerprints":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadBaseline(path)
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Errorf("wrong-version baseline must fail with a version error, got %v", err)
	}
}

// --- fingerprint stability regressions -------------------------------------
//
// The whole baseline mechanism rests on clone hashes being CONTENT-based.
// These tests pin that property against the failure modes that broke the old
// position-based hashes.

const dupBodyA = "\tx := compute()\n\tvalidate(x)\n\ttransform(x)\n\tpersist(x)\n\tnotify(x)\n\treturn x\n"

func detectHashes(t *testing.T, files []TokenizedFile) map[string]bool {
	t.Helper()
	clones := Detect(files, 15, 1.0)
	if len(clones) == 0 {
		t.Fatal("fixture produced no clones")
	}
	hashes := map[string]bool{}
	for _, c := range clones {
		hashes[c.Hash] = true
	}
	return hashes
}

// Shifting a clone down the file (new code above it) must not change its hash.
func TestFingerprint_StableAcrossLineShift(t *testing.T) {
	src := func(pad string) string {
		return "package main\n" + pad + "\nfunc A() int {\n" + dupBodyA + "}\n"
	}
	before := detectHashes(t, []TokenizedFile{
		BuildTokenizedFile("/a.go", src(""), LangForName("go")),
		BuildTokenizedFile("/b.go", "package main\n\nfunc B() int {\n"+dupBodyA+"}\n", LangForName("go")),
	})
	shifted := detectHashes(t, []TokenizedFile{
		BuildTokenizedFile("/a.go", src(strings.Repeat("// filler comment\n", 25)), LangForName("go")),
		BuildTokenizedFile("/b.go", "package main\n\nfunc B() int {\n"+dupBodyA+"}\n", LangForName("go")),
	})
	for h := range before {
		if !shifted[h] {
			t.Errorf("hash %s changed after a pure line shift — baseline would churn", h)
		}
	}
}

// Renaming the FILE (not the code) must not change the hash.
func TestFingerprint_StableAcrossFileRename(t *testing.T) {
	mk := func(pathA string) map[string]bool {
		return detectHashes(t, []TokenizedFile{
			BuildTokenizedFile(pathA, "package main\n\nfunc A() int {\n"+dupBodyA+"}\n", LangForName("go")),
			BuildTokenizedFile("/b.go", "package main\n\nfunc B() int {\n"+dupBodyA+"}\n", LangForName("go")),
		})
	}
	before, after := mk("/old_name.go"), mk("/totally_new_name.go")
	for h := range before {
		if !after[h] {
			t.Errorf("hash %s changed after a file rename — baseline would churn", h)
		}
	}
}

// Adding an unrelated file must not change existing clones' hashes. The old
// type-3 hash was built from file INDICES, so any new file shifted every
// fingerprint. This is the regression test for that failure mode.
func TestFingerprint_Type3_StableWhenUnrelatedFileAdded(t *testing.T) {
	// Two near-miss functions: operator flips SCATTERED so no full window is
	// exact-covered and the divergent windows fall to the type-3 path (same
	// fixture shape as the max-pairs cap test).
	mkSrc := func(variant int) string {
		ops := []string{"+", "+", "+", "+", "+", "+", "+", "+", "+", "+", "+", "+"}
		ops[variant%len(ops)] = "-"
		ops[(variant*2+3)%len(ops)] = "*"
		var sb strings.Builder
		sb.WriteString("package main\nfunc calc(items []int) int {\n\tacc := 0\n\tfor _, item := range items {\n")
		for _, op := range ops {
			sb.WriteString("\t\tacc = acc " + op + " item\n")
		}
		sb.WriteString("\t}\n\treturn acc\n}\n")
		return sb.String()
	}
	a := BuildTokenizedFile("/a.go", mkSrc(0), LangForName("go"))
	b := BuildTokenizedFile("/b.go", mkSrc(1), LangForName("go"))
	unrelated := BuildTokenizedFile("/zz.go", "package main\n\nfunc solo() { println(42) }\n", LangForName("go"))

	type3Hashes := func(files []TokenizedFile) map[string]bool {
		clones := Detect(files, 20, 0.6)
		hashes := map[string]bool{}
		for _, c := range clones {
			if c.Type == domain.CloneType3 {
				hashes[c.Hash] = true
			}
		}
		return hashes
	}

	before := type3Hashes([]TokenizedFile{a, b})
	if len(before) == 0 {
		t.Fatal("fixture produced no type-3 clones")
	}
	// Prepend an unrelated file so every file index shifts.
	after := type3Hashes([]TokenizedFile{unrelated, a, b})
	for h := range before {
		if !after[h] {
			t.Errorf("type-3 hash %s changed when an unrelated file was added — baseline would churn", h)
		}
	}
}
