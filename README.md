# dupehound

Detect code duplication across multiple languages. Single binary, no runtime dependencies, fast.

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
  --path, -p      Path to scan (file or directory) [default: .]
  --min-lines, -l Minimum lines to consider a duplicate block [default: 5]
  --format, -f    Output format: text, json, sarif [default: text]
  --output, -o    Output file (default: stdout)
  --exclude, -e   Glob patterns to exclude (repeatable)
  --language, -L  Filter by language (e.g. go, python, javascript)
  --verbose, -v   Enable verbose logging
```

## Examples

Scan current directory:

```sh
dupehound scan
```

Scan with a lower threshold:

```sh
dupehound scan --path ./src --min-lines 3
```

JSON output for CI:

```sh
dupehound scan --format json --output report.json
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

## How it works

dupehound normalizes source files by stripping comments and collapsing whitespace, then uses a sliding window hash to find identical code blocks across files. Detection is purely structural — it catches copy-paste duplication regardless of variable names in the surrounding context.

## License

MIT
