# Plan: Fix table-driven test literal false positives (issue #31)

**Spec of record:** [GitHub issue #31](https://github.com/AxeForging/dupehound/issues/31) —
no `docs/specs/` spec exists for this; the issue contains the full repro, expected
behavior, and suggested fixes, so it serves as the spec (spec review waived as a bugfix).

**Problem:** Idiomatic Go table-driven tests (`tests := []struct{…}{…}` inside a test
function) are reported as type-2 clones. The reported "instances" are overlapping
sliding windows over the rows of a single composite literal. This contradicts the
README's documented exclusion of data/config literals — the exclusion only works
today because struct/config blocks usually sit *outside* function bodies, and
`markFunctionBodies` masking is the only data filter.

**Root causes (two independent defects):**

1. `services/detector.go` `detectExact`: seed windows are spaced `>= minTokens`
   apart, but greedy extension grows each block to `totalTokens > minTokens`, so
   emitted instances of one clone group overlap each other. Overlapping windows are
   never distinct duplicates.
2. There is no notion of composite literals *inside* function bodies, so a data
   table is treated as logic. Even with defect 1 fixed, a uniform 10-row table still
   yields disjoint identical windows and gets flagged.

## Design change during implementation

The plan originally chose the issue's suggestion **1** (exclude literal tokens from
detection, by clearing `InFunc`). That was implemented, then **rejected on evidence**
and replaced with the issue's suggestion **2** (suppress clone groups confined to one
literal).

Why: masking tokens punches a hole through the middle of surrounding code. A short
inline `[]TokenizedFile{a, b}` sitting inside an otherwise-duplicated block split that
block in two and the finding was lost. Measured on this repo's own test suite, the
masking approach removed **58 clones, ~50 of them genuine** duplication. The
span-based rule removes **5, of which 4 are overlapping-window artifacts and 1 is a
real data table** — with zero new findings and no real duplication lost.

Final design:

- **Overlap guard** (`dropOverlappingStarts`): after extension, same-file instances
  must be spaced `>= totalTokens`; groups left with < 2 instances are dropped.
- **Data-literal spans** (`findDataLiteralSpans`): composite literals are recorded as
  token spans on `TokenizedFile`, *not* excluded from detection. `detectExact` drops a
  clone group when every instance lies inside one span; `detectFuzzy` skips candidate
  pairs whose blocks share a span (resolved once per block, not per pair).
- **Func-literal exemption** (`findFuncLiteralBodies`): a window touching a function
  literal body is logic, never data — so a `run func(t *testing.T)` column is still
  checked. Distinguishing a func literal from a struct field's func *type* needed its
  own parse (`goFuncLiteralBody`); `markFunctionBodies` keys off the `func` keyword
  alone and cannot tell them apart.

## Steps

- [x] **TDD repro first (must fail):** detector-level test reproducing issue #31
      verbatim, plus a gofmt-style multi-line-row table.
      Files: `services/table_literal_test.go`.
- [x] **Overlap guard in `detectExact`.** Files: `services/detector.go`,
      `services/overlap_test.go`.
- [x] **Data-literal span discovery + suppression rule**, wired into both the exact
      and fuzzy detectors. Files: `services/tokenizer.go`, `services/detector.go`,
      `services/composite_literal_test.go`, `services/testhelpers_test.go`.
- [x] **Extend beyond Go** to every language whose collection literals are
      unambiguous, with per-language positive, negative (indexing) and callback tests.
      Files: `services/tokenizer.go`, `services/data_literal_langs_test.go`.
- [x] **Fix the bugs surfaced along the way** (see below): Python function
      boundaries, Dart body misclassification, nil-language panic.
      Files: `services/tokenizer.go`, `services/pyfuncboundary_test.go`.
- [x] **Repro green + full regression:** `go test -race ./...` (469 pass),
      `make lint` (0 issues), `gofumpt -w`. No golden-file churn.
- [x] **Integration tests via the built binary.** Files:
      `integration/table_literal_test.go`. All four fail on `main`, pass on the branch.
- [x] **Docs:** README "How it works" documents the data-table exclusion and its
      limits. Files: `README.md`.
- [x] **Branch + PR:** `fix/table-literal-false-positive`, `Closes #31`.

## Verification performed

- Every new test was run against pristine `main` and **fails** there (4/4 integration,
  4/6 unit — the 2 that pass on main are the negative "real duplication is still
  reported" guards, which must pass in both).
- A/B scan of identical pristine `main` source with the pre-fix and post-fix binaries:
  157 → 152 clones, 0 new, and the only non-overlap removal is a genuine
  `[]domain.Clone{…}` fixture table.
- Self-scan under the repo's own `.dupehound.yml` is byte-identical before and after
  (13 clones, 5.1%), so the CI quality gate is unaffected.

## Risks

- **Heuristic misclassification:** token-level Go brace disambiguation is heuristic.
  Mitigated by a conservative trigger (only `]T{` / `map[…]V{` / `]struct{…}{`),
  Go-only gating, and negative tests covering `if m[k] {`, range statements, index
  expressions on call results, func literals, and plain `T{…}` struct literals.
- **Under-suppression is the deliberate failure mode:** anything ambiguous stays
  reported. A missed false positive is noise; a swallowed real clone is a silent
  correctness loss.
- **Baselines:** users with committed baselines may see previously reported table
  clones disappear. Stale entries are inert, not breaking.
- **Rollback:** revert the PR — no schema, config, or flag changes; behavior-only.

## Done means

- The exact repro from issue #31 scans to `Clones found: 0` (unit + binary-level).
- No clone group ever reports overlapping same-file instances (unit-tested invariant).
- Genuine duplication is still detected: in test files, in table func-literal columns,
  and in blocks containing short inline literals — each covered by a dedicated test.
- `go test -race ./...` and `make lint` clean; PR open referencing issue #31.

## Bugs found while working — all fixed in this PR

1. **Python function boundaries (`markPythonFunctions`)** — the body scan ended at the
   next blank-line-separated `def`, then resumed the outer scan *after* that keyword,
   stepping over it. Every definition following the first was never marked as a
   function body, and since detection only looks inside function bodies, all
   duplication in those functions was invisible. In a file of `n` blank-line separated
   defs, only the first was ever scanned. Fixed by resuming *on* the boundary keyword.
   Regression tests: `services/pyfuncboundary_test.go` (7 of 8 fail on `main`).
2. **Dart function bodies misread as literals** — bodies in languages without a `func`
   keyword come from a brace-depth heuristic that cannot tell a function body from a
   `{…}` map literal at the same depth, so every row of a Dart table looked like a
   function body. Fixed with `isRealFunctionBody`: a brace-delimited body follows `)`
   or an arrow, a data literal follows `=`, `,` or `[`.
3. **`TokenizeFile` nil-language panic** — it dereferenced `lang.Name` while
   `markFunctionBodies` explicitly documents nil as "no syntax knowledge". Unreachable
   today (`collectFiles` drops unknown-language files first), but the contract was
   incoherent. Fixed by substituting a zero-value language.

## Language coverage

The rule applies wherever a collection literal can be told from code by tokens alone:

- **Go** — typed composite literals: `[]T{…}`, `map[K]V{…}`, `[]struct{…}{…}`.
- **Bracket literals** (`[…]` in expression position) — Python, JavaScript, TypeScript,
  Ruby, Rust, PHP, Elixir, Swift, Dart.
- **Brace literals** (`{…}` where braces are never blocks) — Python, Lua, Elixir.

Deliberately excluded: Java, C, C++, C#, Kotlin (there `[` is an index or array type
and `{` is also a block) and Scala (`[` is type parameters). Ruby takes brackets only,
since `{` is either a hash or a block argument.
