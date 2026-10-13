# dupehound

A Go CLI that finds duplicated code (type-1/2/3 clones) and dead functions across
20 languages using its own lexer. No parsers, no CGO, one static binary.
Scope is exactly that: duplicates and dead code.

## Where things live

`main.go` and `main.flags.go` define the CLI. `actions/` turns flags and config into
options. `domain/` holds plain types. `helpers/` holds errors, logging and
memory sizing. Everything else is in `services/`, one concern per file:

| File | Owns |
|---|---|
| `tokenizer.lexer.go` | The lexer: source text to tokens |
| `tokenizer.keywords.go` | Per-language keyword tables |
| `tokenizer.strings.go` | Multi-line strings, heredocs, Rust char literals, JS regexes |
| `tokenizer.funcbodies.go` | Which tokens are inside a function, per language family |
| `tokenizer.funcnames.go` | The name of the function a body belongs to |
| `tokenizer.dataliterals.go` | Data-table spans (so a table is not reported as logic) |
| `tokenizer.imports.go` | Import/include/use statements (skipped in file scope) |
| `tokenizer.ignore.go` | Inline `dupehound:ignore` markers |
| `detector.detect.go` | Detection entry point, options, completeness stats |
| `detector.file.go` | `TokenizedFile` and its per-token masks |
| `detector.exact.go` | Type-1/2: rolling hashes, grouping, extension |
| `detector.fuzzy.go` | Type-3: Jaccard over mini-window hashes |
| `detector.instances.go` | Clone instances: lines, function name, preview |
| `scanner.scan.go` | The scan pipeline |
| `scanner.files.go` | File walk, globs, language by extension |
| `scanner.generated.go` | Generated and minified file detection |
| `scanner.results.go` | De-duplication, filters, per-file stats |
| `scanner.memory.go` | `--max-memory` estimate and enforcement |
| `scanner.config.go` | `.dupehound.yml` loading and defaults |
| `scanner.testprod.go` | Test-file recognition |
| `deadcode.analysis.go` | Dead-function analysis (definitions in, usages streamed) |
| `deadcode.defs.go` | Finding definitions in keyword languages |
| `deadcode.members.go` | Finding definitions in C-family languages and JS/TS classes |
| `deadcode.entrypoints.go` | Functions a framework calls; visibility rules |
| `reporter.format.go` | Report entry point and what every format shows |
| `reporter.text.go`, `.markdown.go`, `.json.go`, `.sarif.go`, `.github.go` | One output format each |
| `baseline.ratchet.go`, `suppression.rules.go` | Baseline ratchet, `.dupehound-ignore` |
| `git.staged.go`, `git.diff.go`, `git.churn.go` | Staged files, diff ranges, churn |

## Naming

Every Go file is `<feature>.<concern>.go`; its tests are
`<feature>.<concern>_test.go`, or `<feature>.<concern>.<topic>_test.go` when one
concern has several test files. `main.go` is the only exception. structlint
enforces this in the pre-commit hook.

Use dots, not underscores, between the parts. Go compiles a file ending in
`_js.go`, `_windows.go`, `_arm.go` or `_wasm.go` only for that platform, with no
warning, so `tokenizer_js.go` would silently drop out of the build.

New code goes in the file that owns the concern. If none does, add
`<feature>.<concern>.go` and a row above. Keep files under about 600 lines.

## Do

- Write the failing test first, and name it for the behaviour
  (`TestWholeFile_FindsCopiedClassMadeOfSmallMethods`).
- Build fixtures from the real code that exposed the bug, and say where it came
  from in the test's comment.
- Check that a new test fails without the fix. A test that passes either way is
  worth nothing; this has happened here more than once.
- Test CLI behaviour through the built binary (`integration/`, `buildBinary`).
- Measure changes to detection on real repositories, before and after, against
  an independent tool: SonarQube for clones; `staticcheck`, `rustc`, `vulture`
  for dead code. Record the numbers in the spec or PR.
- Prove a refactor or optimisation by byte-identical JSON output on real repos.
- Run every scan of a large repository under a memory cap, for example
  `systemd-run --user --scope -p MemoryMax=3G -p MemorySwapMax=0 dupehound …`.
- Explain in a comment why a heuristic exists and which way it errs.
- Format with `gofumpt -w`. Before pushing: `go test -race ./...`,
  `golangci-lint run`, `govulncheck ./...`.

## Don't

- Don't trust a green test suite as proof that detection is right. It has passed
  while 36 of 37 dead-code findings were wrong.
- Don't return substrings of a file you read into long-lived results; copy them.
  That one mistake cost 7 GB.
- Don't add a per-token or per-window structure without measuring peak memory.
  An index that looked smaller on paper used 800 MB more.
- Don't make a heuristic report more when unsure. Dead code errs toward "used",
  generated-file detection toward "hand-written".
- Don't make results depend on timing or machine state. The memory budget works
  from an estimate of the input for that reason.
- Don't change `heapHeadroom` or `fuzzyBytesPerMiniHash` without re-measuring
  estimate against peak RSS on real repositories.
- Don't commit benchmark or crosscheck harnesses; they are local tooling.
- Don't leave duplication in this repository: `dupehound scan` on itself reports
  none, and the PR gate fails on any that a change touches.
- Don't bypass git hooks, and don't add AI attribution to commits or PRs.
- Don't widen scope beyond duplicates and dead code.

## Traps

- `InFunc` is not "inside a function" for Java, C, C++, C# and Dart: it also
  covers class bodies. Use `Logic` when you mean executable code.
- A window hash of 0 means "no window here". Hashes are not verified against
  the tokens.
- `.dupehound.yml` is discovered from the working directory, not from `--path`.
  Scanning another repository from here applies this repository's excludes.
- Python function spans are approximate (the token stream has no indentation).
- Type-3 detection only looks inside function bodies, in either scope.
- Directories named `build`, `dist`, `target`, `testdata` and `vendor` are
  always skipped.

## Conventions

Conventional Commits (`type(scope): what`), short bullet body. PR titles follow
the same form; `!` marks a breaking change. Non-trivial features start with a
spec in `docs/specs/NNN-name.md`.
