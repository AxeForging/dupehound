# dupehound

Detect code duplication across multiple languages. Single binary, no runtime dependencies, fast.

Finds type-1 (identical), type-2 (renamed identifiers), and type-3 (near-miss) clones using token-based detection with function-level granularity — skips imports, declarations, and config blocks to focus on actual logic duplication.

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
  --format, -f         Output format: text, json, sarif, md [default: text]
  --output, -o         Output file (default: stdout)
  --exclude, -e        Glob patterns to exclude (repeatable)
  --language, -L       Filter by language (e.g. go, python, javascript)
  --similarity         Minimum similarity for type-3 detection (0.50–1.00; 1.0 disables) [default: 0.7]
  --max-bucket         Max candidates per fuzzy hash bucket [default: 5000]
  --min-duplication    Fail if global duplication % exceeds this value (0 = disabled) [default: 0]
  --staged             Only report clones involving git-staged files (for pre-commit hooks)
  --exit-zero          Always exit 0 even when clones are found
  --config, -c         Path to config file (default: auto-discover .dupehound.yml)
  --verbose, -v        Enable verbose logging
```

## Examples

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

Exclude generated files:

```sh
dupehound scan --exclude "*.pb.go" --exclude "*.gen.go"
```

Scan only Python files:

```sh
dupehound scan --language python
```

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

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | No clones found (or `--exit-zero` set) |
| 1 | Clones found, or `--min-duplication` threshold exceeded |
| 2 | Tool error (invalid path, bad config, etc.) |

## License

MIT
