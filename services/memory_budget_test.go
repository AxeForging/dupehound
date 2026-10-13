package services

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/AxeForging/dupehound/helpers"
)

// goFunc renders a Go function with enough tokens to form a clone window.
// variant changes one statement, producing a near-miss rather than a copy.
func goFunc(name string, variant int) string {
	extra := ""
	if variant > 0 {
		extra = fmt.Sprintf("\ttotal += %d\n", variant)
	}
	return "func " + name + "(items []int, limit int) int {\n" +
		"\ttotal := 0\n\tfor _, item := range items {\n\t\tif item > limit {\n\t\t\ttotal += item * 2\n" +
		"\t\t} else if item < 0 {\n\t\t\ttotal -= item\n\t\t} else {\n\t\t\ttotal++\n\t\t}\n\t}\n" +
		extra + "\tif total > limit {\n\t\treturn limit\n\t}\n\treturn total\n}\n\n"
}

// A scan that cannot fit its budget must say so and stop, not be OOM-killed.
func TestScan_FailsCleanlyWhenInputExceedsMemoryBudget(t *testing.T) {
	dir := t.TempDir()
	var src strings.Builder
	src.WriteString("package big\n\n")
	for i := range 400 {
		src.WriteString(goFunc(fmt.Sprintf("f%d", i), i))
	}
	writeFile(t, dir, "big.go", src.String())

	_, err := NewScannerService().Scan(ScanOptions{Path: dir, MinTokens: 30, MaxMemory: 64 << 10})
	if !errors.Is(err, helpers.ErrMemoryBudget) {
		t.Fatalf("scan error = %v, want ErrMemoryBudget", err)
	}
	if !strings.Contains(err.Error(), "--max-memory") {
		t.Errorf("error should name the flag to raise, got: %v", err)
	}

	// The same input fits comfortably in a realistic budget.
	if _, err := NewScannerService().Scan(ScanOptions{Path: dir, MinTokens: 30, MaxMemory: 256 << 20}); err != nil {
		t.Errorf("scan within budget failed: %v", err)
	}
}

// When the budget has room for exact detection but not for near-miss
// detection, exact results are complete and the report says type-3 was limited.
func TestScan_MemoryBudgetLimitsType3ButKeepsExactResults(t *testing.T) {
	dir := t.TempDir()
	// alpha/beta are copies (type-1/2). nearA/nearB differ by one statement in
	// the middle, leaving no 40-token run in common: only type-3 can pair them.
	near := func(name, middle string) string {
		return "func " + name + "(rows [][]string, sep string) string {\n" +
			"\tout := \"\"\n\tcount := 0\n\tlimit := len(rows) * 2\n" +
			"\tfor i, row := range rows {\n\t\tif i > 0 {\n\t\t\tout += sep\n\t\t}\n\t\tcount += i\n" +
			middle +
			"\t\tfor j, cell := range row {\n\t\t\tif j > 0 {\n\t\t\t\tout += \",\"\n\t\t\t}\n\t\t\tout += cell\n\t\t\tcount += j\n\t\t}\n" +
			"\t\tif count > limit {\n\t\t\tout += sep\n\t\t}\n\t}\n\treturn out\n}\n\n"
	}
	srcA := "package p\n\n" + goFunc("alpha", 0) + near("nearA", "")
	srcB := "package p\n\n" + goFunc("beta", 0) + near("nearB", "\t\tout += row[0]\n")
	writeFile(t, dir, "a.go", srcA)
	writeFile(t, dir, "b.go", srcB)

	// A budget with room for exactly these two files and nothing more.
	fits := newMemoryBudget(0)
	for _, src := range []string{srcA, srcB} {
		fits.add(len(src), len(TokenizeFile(src, LangForName("go"))))
	}

	count := func(opts ScanOptions) (exact, fuzzy int, partial bool, reason string) {
		t.Helper()
		opts.Path, opts.MinTokens, opts.MinSimilarity = dir, 40, 0.6
		report, err := NewScannerService().Scan(opts)
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		for _, c := range report.Clones {
			if c.Type == "type-3" {
				fuzzy++
			} else {
				exact++
			}
		}
		return exact, fuzzy, report.Partial, report.PartialReason
	}

	exactFull, fuzzyFull, partial, _ := count(ScanOptions{})
	if exactFull == 0 || fuzzyFull == 0 || partial {
		t.Fatalf("fixture must yield exact and type-3 clones unrestricted: exact=%d type-3=%d partial=%v", exactFull, fuzzyFull, partial)
	}

	exact, fuzzy, partial, reason := count(ScanOptions{MaxMemory: fits.estimate + 1024})
	if exact != exactFull {
		t.Errorf("exact clones under budget = %d, want %d (budget must never reduce type-1/2)", exact, exactFull)
	}
	if fuzzy != 0 {
		t.Errorf("type-3 clones under budget = %d, want 0", fuzzy)
	}
	if !partial || !strings.Contains(reason, "--max-memory") {
		t.Errorf("report must flag the limitation: partial=%v reason=%q", partial, reason)
	}
}

// The rolling window hash must induce exactly the same equivalence classes as
// hashing each window from scratch, and mark the same windows as out of scope.
func TestWindowHashes_AgreeWithDirectHashing(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	vocab := []Token{
		{Kind: TokKeyword, Text: "if"},
		{Kind: TokKeyword, Text: "for"},
		{Kind: TokOperator, Text: "{"},
		{Kind: TokOperator, Text: "}"},
		{Kind: TokOperator, Text: "+"},
		{Kind: TokIdent, OrigText: "a"},
		{Kind: TokIdent, OrigText: "b"},
		{Kind: TokNumber, OrigText: "1"},
		{Kind: TokString, OrigText: `"s"`},
	}
	for _, minTokens := range []int{1, 2, 5, 13} {
		for trial := range 20 {
			n := rng.Intn(120)
			tf := TokenizedFile{Tokens: make([]Token, n), InFunc: make([]bool, n)}
			for i := range tf.Tokens {
				tf.Tokens[i] = vocab[rng.Intn(len(vocab))]
				tf.InFunc[i] = rng.Intn(10) > 0 // an occasional gap between functions
			}
			got := windowHashes(tf, minTokens, false)
			if len(got) != n {
				t.Fatalf("len = %d, want %d", len(got), n)
			}
			direct := make([]uint64, n)
			for i := 0; i+minTokens <= n; i++ {
				if windowInFunc(tf.InFunc, i, minTokens) {
					direct[i] = hashWindow(tf.Tokens[i : i+minTokens])
				}
			}
			for i := range n {
				if (got[i] == 0) != (direct[i] == 0) {
					t.Fatalf("minTokens=%d trial=%d pos=%d: in-scope mismatch (rolling=%d direct=%d)", minTokens, trial, i, got[i], direct[i])
				}
				for j := i + 1; j < n; j++ {
					if direct[i] != 0 && direct[j] != 0 && (got[i] == got[j]) != (direct[i] == direct[j]) {
						t.Fatalf("minTokens=%d trial=%d: windows %d and %d grouped differently", minTokens, trial, i, j)
					}
				}
			}
		}
	}
}
