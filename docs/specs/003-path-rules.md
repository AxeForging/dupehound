# 003 — Per-path rules for scan thresholds

Spec issue: https://github.com/AxeForging/dupehound/issues/36 (the plan is tracked there).

**Status:** draft, awaiting review. Implementation PR is opened as a draft against this spec.

## Context

`.dupehound.yml` has one `scan` block, so `min-tokens`, `similarity` and friends apply to the whole repository. Real projects need different thresholds by path or naming convention. Measured on a Python uv workspace:

- at `min-tokens: 50`, the small duplicated test fakes (`EchoModel`, `FixedModel`, … 20–40 tokens each) are invisible;
- lowering to 25 globally floods production code with false positives (proxy classes that share a shape by design).

Users want, in one config and one scan: tests at a lower threshold, production at 50, generated code skipped, migrations at a higher threshold.

## Requirements

- **R1** A top-level `rules:` list in `.dupehound.yml`. Each rule has `paths` (globs) and overrides for the files it matches. First matching rule wins; a file no rule matches uses the `scan` settings.
- **R2** A rule may set `min-tokens`, `similarity` (type-3 floor) and `skip: true` (file is not scanned at all). Optional `name` labels the rule in output. Nothing else is per-file in v1.
- **R3** A clone whose instances live in files under different rules is judged by one documented policy (see Design), deterministically, independent of instance order.
- **R4** Precedence: CLI flags set the base (`scan`) values exactly as today; rules still apply on top. `--no-rules` ignores `rules:` for one run.
- **R5** Every output format (text, md, json, sarif, github) shows which rule judged a clone when rules are in use.
- **R6** No `rules:` (or `--no-rules`) gives byte-identical output to today.
- **R7** Baseline fingerprints of clones are unchanged by adding rules (a clone found under the base settings keeps its hash); `--baseline`, `--write-baseline`, `--staged`, `--since`, `.dupehound-ignore` and inline `dupehound:ignore` keep working unchanged.
- **R8** Invalid rules are rejected with a clear error naming the rule and line: unknown keys, missing/empty `paths`, empty or malformed glob, `min-tokens <= 0`, `similarity` outside 0.50–1.00, `skip` combined with an override, duplicate `name`.
- **R9** `dupehound init`, README and EXAMPLES document an example (tests stricter, migrations looser, generated skipped).

## Non-goals

- Per-rule `exclude`/`include` (that is what `skip` and `scan.exclude` are), `scope`, `min-clone-lines`, `format`, etc.
- Making unknown keys under `scan:` an error (would break existing configs; only `rules:` entries are strict).
- Rules relative to the config file's directory (patterns are relative to the scan root; see Design).

## Design

### Config shape

```yaml
scan:
  min-tokens: 50
  exclude: [".venv/**"]
rules:                          # first matching rule wins; unmatched files use `scan`
  - name: tests
    paths: ["**/*_test.py", "**/tests/**", "**/*.test.ts", "**/*_test.go"]
    min-tokens: 25
  - name: migrations
    paths: ["**/migrations/versions/*.py"]
    min-tokens: 120
  - name: generated
    paths: ["**/generated/**"]
    skip: true
```

Patterns are matched against the path **relative to the scan root** (`--path`), with `/` separators. Same glob dialect as `exclude` (a pattern with no `/` also matches the base name; `**` crosses directories), plus: a leading `**/` also matches zero directories, so `**/tests/**` matches `tests/x.py` at the root. Unnamed rules are labelled `rules[N]` (0-based).

### Cross-rule clones: the strictest threshold wins

A clone's **effective threshold** is the highest `min-tokens` (and, for type-3, the highest `similarity`) among the files its instances live in. The rule that set it is the clone's `rule` (ties: the earliest rule; `scan` ranks after all rules).

Why this and not "the first instance's rule":
- Instance order is an artefact of file order; a policy that depends on it would make a clone appear or vanish when a file is renamed.
- Production stays protected: a 30-token snippet shared by a test and a production file is not reported when production is at 50. That is the point of having a higher production threshold, and it errs toward reporting less (CLAUDE.md: "Don't make a heuristic report more when unsure").
- Test-only duplication is still found: the lower threshold is detected among the test files alone (below), so a fake copied into three tests and also into one production file is reported with its three test instances.
- It allows an exact implementation (below) that keeps R6/R7 trivially true.

### Detection: one pass per distinct threshold

Window size is fixed per detection run, so a lower threshold cannot be applied by filtering a 50-token run. For each distinct `min-tokens` T among the scanned files (ascending): detect on the files whose own threshold is ≤ T, with window T and type-3 floor = the lowest `similarity` among them; keep only clones whose effective threshold is exactly T, and type-3 clones whose similarity reaches the highest floor of their files. Every clone is kept by exactly one pass, then the usual overlap de-duplication, filters and annotations run once on the union.

- With no rules there is one pass, over every file, with today's options: identical output (R6).
- Clones among files that keep the base threshold are found by the base pass with the same window over a superset-equal file set, so their hashes are unchanged (R7).
- Cost: one exact pass per distinct threshold; tokens are held once. A typical config (base + tests + migrations) is three passes, the extra ones over subsets.
- `--max-pairs` is shared across passes (the remaining budget carries over); `--max-memory` is re-checked per pass. In low-memory mode a file is compacted at its own threshold; a pass with another window re-lexes it from disk (bounded memory, more I/O).
- A file with fewer tokens than its own threshold is skipped as today.

### Interactions

- `scan.exclude`/`include`/`--exclude` run first; `skip` rules after (counted as `skipped_by_rule`).
- `.dupehound-ignore`, inline markers, `--staged`, `--since`, `--baseline` run on the merged clone list exactly as before. `--write-baseline` records the base `min-tokens` (informative field only).
- `--min-tokens`/`--similarity` on the CLI replace the `scan` values; rules still override per file. `--no-rules` disables them.

## Data & API changes

- `domain.Config.Rules []domain.Rule` (`name`, `paths`, `min-tokens`, `similarity`, `skip`).
- `domain.Clone.Rule string` (`json:"rule,omitempty"`) and `RuleMinTokens int` (`json:"rule_min_tokens,omitempty"`), set only when rules are active.
- `domain.Report.SkippedByRule int` (`json:"skipped_by_rule,omitempty"`).
- Text: badge `[rule: tests ≥25]`; md: `📐 rule tests ≥25`; sarif: `properties.rule`, `properties.ruleMinTokens`; github: `rule tests ≥25` in the annotation message.
- New flag `--no-rules`.

## Test plan

Unit (`services/`): rule validation table (each R8 error, valid shapes); glob matching relative to root incl. leading `**/`; first-match precedence; threshold barrier (clone exactly at a rule's threshold reported, one token under not); cross-rule clone judged by the higher threshold; test-only clone still found when a prod copy exists; skip excludes the file entirely; type-3 similarity floor per rule; no-rules output identical to the pre-change scan; base-threshold clone hashes unchanged when a rule is added.

Integration (built binary): config with rules → JSON `rule` fields, text badge; `--no-rules` byte-identical to a config without rules; invalid config exits 2 with the rule and line in the message; `dupehound init` output parses and validates.

Measure: dupehound on itself and on a real Python workspace, before/after wall time.

## Measurements

| run | wall | peak RSS | result |
|---|---:|---:|---|
| Go `net` (378 files), main, no rules | 6.6 s | 430 MB | 987 clones |
| same, this branch, no rules | 6.2 s | 442 MB | JSON byte-identical to main |
| same, tests rule at 25 | 10.2 s | 457 MB | 225 clones judged by `scan` — identical (hash + locations) to main's clones touching production; 2,948 test-only clones under `tests` |
| dupehound itself, main vs no rules | 0.8 / 0.6 s | 78 MB | JSON byte-identical |
| Python uv workspace (switchyard, 148 files), production at 50 | 0.13 s | 29 MB | 0 clones (as main) |
| same, tests at 40 / 35 / 30 / 25 / 20 | 0.15–0.26 s | ≤ 29 MB | 5 / 7 / 13 / 23 / 28 test clones |

On the Python workspace, 25 is the lowest threshold that still reports copied fakes (a 27-token `SpokeChoice` model duplicated between two adapter tests, a 28-token `FakeModel/FixedRouter/EchoSpoke` hub setup) without being dominated by one-line arrange/assert repeats; 20 adds mostly those.

## Rollout

Additive, opt-in: no config change means no behaviour change. Ship in the next minor release; README/EXAMPLES document the feature and the cross-rule policy.
