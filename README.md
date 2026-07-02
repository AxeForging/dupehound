<p align="center">
  <img src="docs/dupehound-logo-wordmark.png" alt="dupehound logo" width="520">
</p>

# dupehound

Detect code duplication across multiple languages. Single binary, no runtime dependencies, fast.

Finds type-1 (identical), type-2 (renamed identifiers), and type-3 (near-miss) clones using token-based detection with function-level granularity — skips imports, declarations, and config blocks to focus on actual logic duplication.

![dupehound finding duplicates and dead code in tacomex-8bit-shop](docs/demo/dupehound-demo.gif)

> dupehound run against [`axeforging/tacomex-8bit-shop`](https://github.com/axeforging/tacomex-8bit-shop) — a real React + Fastify sample app. See [EXAMPLES.md](EXAMPLES.md) for the full cookbook.

## Contents

- [Supported languages](#supported-languages)
- [Install](#install)
- [Usage](#usage)
- [Quick examples](#quick-examples) — basic recipes
- [More examples & use cases](#more-examples--use-cases) → **[EXAMPLES.md](EXAMPLES.md)** for the full cookbook
- [Baseline ratchet](#baseline-ratchet-adopt-on-an-existing-codebase)
- [Quality gate](#quality-gate)
- [Pre-commit hook](#pre-commit-hook)
- [Configuration](#configuration)
- [Clone types](#clone-types)
- [How it works](#how-it-works)
- [Output formats](#output-formats)
- [Exit codes](#exit-codes)

## Supported languages

Go, Python, JavaScript, TypeScript, Java, Kotlin, Rust, C, C++, C#, Swift, Scala, PHP, Ruby, Shell, SQL, Lua, Elixir, Dart, R

## Install

Download the binary for your platform from [Releases](https://github.com/AxeForging/dupehound/releases) or install with the install script:

```sh
curl -fsSL https://raw.githubusercontent.com/AxeForging/dupehound/main/install.sh | sh
```

Or build from source:

```sh
make build-local
sudo mv dupehound /usr/local/bin/
```

## Usage

```
dupehound scan [options]

Options:
  --path, -p           Path to scan (file or directory) [default: .]
  --min-tokens, -t     Minimum tokens to consider a duplicate block [default: 50, ~5 lines]
  --format, -f         Output format: text, json, sarif, md, github [default: text]
                       github emits ::warning workflow commands for inline PR
                       annotations with zero SARIF/upload setup
  --output, -o         Output file (default: stdout)
  --include, -i        Glob patterns to include (repeatable, supports **)
  --exclude, -e        Glob patterns to exclude (repeatable, supports **)
  --language, -L       Filter by language (e.g. go, python, javascript)
  --similarity         Minimum similarity for type-3 detection (0.50–1.00; 1.0 disables) [default: 0.7]
  --max-bucket         Max candidates per fuzzy hash bucket [default: 5000]
  --min-duplication    Fail if global duplication % exceeds this value (0 = disabled) [default: 0]
  --staged             Only report clones involving git-staged files (for pre-commit hooks)
  --since              Git ref for diff-aware scanning (e.g. main, HEAD~5, v1.0.0).
                       The detector itself skips work for clones outside the diff —
                       this is a true cost reduction, not just a post-filter.
  --top                Max clones to show in text/md output (0 = show all) [default: 10]
  --git-churn          Annotate clones with git commit churn and re-sort by churn
  --churn-days         Rolling window in days for git churn (default 90)
  --dead-code          Detect likely-dead functions (heuristic only)
  --show-suppressed    Include suppressed clones in output (tagged [suppressed])
  --max-files          Hard cap on collected source files (0 = no cap); fail-fast safety net
  --max-pairs          Runaway backstop on fuzzy candidate pairs; past the limit, type-3
                       detection stops and returns a partial result [default: 50000000, 0 = unlimited]
  --max-file-size      Skip files larger than this many bytes BEFORE reading them
                       [default: 5242880 (5MiB), 0 = unlimited]; oversized files are
                       almost always minified/generated and would dominate memory
  --baseline           Compare against a baseline file: recorded clones are accepted
                       debt, only NEW clones (or grown instance counts) fail the scan
  --write-baseline     Write current clones to a baseline file (accepted debt) and
                       exit 0; commit it and use --baseline in hooks/CI
  --scan-generated     Include machine-generated files (*.pb.go, @generated / "DO NOT EDIT"
                       headers, etc.); skipped by default as they inflate duplication
  --exit-zero          Always exit 0 even when clones are found
  --config, -c         Path to config file (default: auto-discover .dupehound.yml)
  --verbose, -v        Enable verbose logging
  --quiet, -q          Suppress info-level progress logs (the clone report on stdout
                       is unaffected, so failing hooks still show the failure reason)
```

## Quick examples

Scan current directory:

```sh
dupehound scan
```

Scan with a lower threshold:

```sh
dupehound scan --path ./src --min-tokens 30
```

JSON output for CI:

```sh
dupehound scan --format json --output report.json
```

Markdown output for GitHub PR comments:

```sh
dupehound scan --format md --output report.md
```

SARIF output for GitHub Code Scanning:

```sh
dupehound scan --format sarif --output results.sarif
```

Generated files (`*.pb.go`, `@generated` / `DO NOT EDIT` headers, etc.) are skipped by
default; pass `--scan-generated` to include them:

```sh
dupehound scan --scan-generated
```

Scan only Python files:

```sh
dupehound scan --language python
```

## More examples & use cases

The recipes above cover the basics. For a full cookbook with copy-pasteable patterns for pre-commit hooks, PR comments, diff-aware CI, suppression rules, churn ranking, dead-code detection, and safe-mode profiles for large monorepos, see **[EXAMPLES.md](EXAMPLES.md)**.

Jump straight to a topic:

- [Pre-commit hook (lefthook + plain git)](EXAMPLES.md#pre-commit-hook)
- [CI on pull requests (GitHub Actions + sticky comments)](EXAMPLES.md#ci-on-pull-requests)
- [Baseline ratchet (`--write-baseline` / `--baseline`)](EXAMPLES.md#baseline-ratchet)
- [Inline PR annotations (`--format github`)](EXAMPLES.md#github-actions-inline-annotations)
- [Diff-aware scanning with `--since`](EXAMPLES.md#diff-aware-scanning)
- [Filtering files with `--include` / `--exclude`](EXAMPLES.md#filtering-files)
- [Suppressing known duplicates (`.dupehound-ignore` + inline markers)](EXAMPLES.md#suppressing-known-duplicates)
- [Test↔Prod leak detection](EXAMPLES.md#testprod-leak-detection)
- [Git churn ranking (`--git-churn`)](EXAMPLES.md#git-churn-ranking)
- [Dead function detection (`--dead-code`)](EXAMPLES.md#dead-function-detection)
- [Safe-mode profiles for large repos (`--max-files`, `--max-pairs`, `--similarity 1.0`)](EXAMPLES.md#safe-mode-profiles-for-large-repos)
- [Hook output and AI readability (`--quiet`)](EXAMPLES.md#hook-output-and-ai-readability)
- [Output formats (text, md, json, sarif)](EXAMPLES.md#output-formats)
- [Real-world trial on `cli/cli`](EXAMPLES.md#real-world-trial-clicli)

## Baseline ratchet (adopt on an existing codebase)

Most repos already have duplication; failing every scan on day one just gets the tool
removed. The baseline ratchet records today's clones as accepted debt and fails only
on **new** duplication — including pasting *yet another copy* of an already-known clone:

```sh
# once: record current duplication and commit the file
dupehound scan --write-baseline .dupehound-baseline.json
git add .dupehound-baseline.json

# in hooks / CI: fails (exit 1) only when NEW clones appear
dupehound scan --baseline .dupehound-baseline.json
```

Baseline entries are **content-based fingerprints** of the normalized token structure,
so they survive line shifts, file renames, and unrelated edits without churn. The file
is deterministic and diff-friendly. Known clones are hidden from the report (the summary
still counts them); refactor a known clone away, rewrite the baseline, and the debt
shrinks — it can only ratchet down.

## Quality gate

Fail if duplication exceeds a threshold:

```sh
dupehound scan --min-duplication 5.0
# exit 1 if more than 5% of lines are duplicated
```

## Pre-commit hook

Use `--staged` to only check files being committed — fast, focused, no existing-debt noise:

```sh
#!/bin/sh
dupehound scan --staged
```

The `--staged` flag scans all files for cross-comparison but only reports clones where at least one instance is in a staged file. This catches both within-staged and staged-vs-existing duplication.

Combine with a baseline for the strictest useful hook — block only *new* duplication touching the commit:

```sh
#!/bin/sh
dupehound scan --staged --baseline .dupehound-baseline.json --quiet
```

## Configuration

Create a `.dupehound.yml` in your project root (or run `dupehound init`):

```yaml
scan:
  path: ./src
  min-tokens: 50
  exclude:
    - "*_test.go"
    - "*.spec.ts"
  language: ""
  format: text
  exit-zero: false
```

Config is auto-discovered by walking up from cwd. CLI flags override config values.

## Clone types

| Type | Description | Similarity |
|------|-------------|------------|
| type-1 | Identical code (same tokens and text) | 1.00 |
| type-2 | Identical structure, different identifiers/literals | 1.00 |
| type-3 | Near-miss, structurally similar with small differences | 0.70–0.99 |

## How it works

dupehound tokenizes source files into a normalized token stream (stripping comments, collapsing whitespace), then uses sliding-window hashing to find identical (type-1/2) and near-miss (type-3, via mini-window Jaccard similarity) code blocks.

Detection is restricted to **function and method bodies** — imports, top-level declarations, struct definitions, config blocks, and keyword maps are automatically excluded. This dramatically reduces false positives and focuses on actionable logic duplication.

## Output formats

- **text** — human-readable with hotspots, top clones by impact, and full clone list
- **json** — machine-readable with per-file stats, duplication percentage, and all clone data
- **sarif** — SARIF 2.1.0 for GitHub Code Scanning and other SARIF-compatible tools
- **md** — GitHub-flavored Markdown with expandable `<details>` sections, designed for PR comments
- **github** — GitHub Actions workflow commands (`::warning file=…,line=…::`) for inline PR annotations; just run `dupehound scan --format github` in any Actions step, no SARIF upload needed

Every clone instance is attributed to its **enclosing function** (`api/user.go:42-63 (in loadUser)`) across all supported languages, and each clone carries:

- **saves ~N lines** — refactor value: lines removable by deduplicating ((instances−1) × block length); the summary shows the repo-wide total
- **scope** — `same-file` (trivial extraction), `cross-file` (same directory), or `cross-dir` (usually needs a shared package)
- a stable **content-based fingerprint** (`hash`) usable in `.dupehound-ignore` suppression rules and baselines

If a safety guard reduced type-3 coverage (`--max-pairs`, `--max-bucket`), every format carries an explicit **partial result** marker (`!! PARTIAL RESULT` banner, `partial`/`partial_reason` JSON fields, SARIF run properties) — type-1/2 results are always complete.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | No clones found (or `--exit-zero` set) |
| 1 | Clones found, or `--min-duplication` threshold exceeded |
| 2 | Tool error (invalid path, bad config, etc.) |

## License

MIT
