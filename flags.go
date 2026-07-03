package main

import "github.com/urfave/cli"

var pathFlag = cli.StringFlag{
	Name:  "path, p",
	Value: ".",
	Usage: "Path to scan (file or directory)",
}

var minTokensFlag = cli.IntFlag{
	Name:  "min-tokens, t",
	Value: 50,
	Usage: "Minimum number of tokens to consider a duplicate block (~5 lines of average code)",
}

var minLinesFlag = cli.IntFlag{
	Name:  "min-lines, l",
	Value: 0,
	Usage: "[deprecated, use --min-tokens] Minimum lines; converted internally to tokens (×10)",
}

var formatFlag = cli.StringFlag{
	Name:  "format, f",
	Value: "text",
	Usage: "Output format: text, json, sarif, md",
}

var outputFlag = cli.StringFlag{
	Name:  "output, o",
	Value: "",
	Usage: "Output file (default: stdout)",
}

var verboseFlag = cli.BoolFlag{
	Name:  "verbose, v",
	Usage: "Enable verbose logging",
}

var quietFlag = cli.BoolFlag{
	Name:  "quiet, q",
	Usage: "Suppress info-level progress logs (errors and warnings still print); useful for hooks and CI",
}

var excludeFlag = cli.StringSliceFlag{
	Name:  "exclude, e",
	Usage: "Glob patterns to exclude (can be repeated)",
}

var languageFlag = cli.StringFlag{
	Name:  "language, L",
	Value: "",
	Usage: "Filter by language (e.g. go, python, javascript)",
}

var exitZeroFlag = cli.BoolFlag{
	Name:  "exit-zero",
	Usage: "Always exit 0 even when clones are found (useful for reporting pipelines)",
}

var maxBucketFlag = cli.IntFlag{
	Name:  "max-bucket",
	Value: 5000,
	Usage: "Max candidates per fuzzy hash bucket (higher = slower but more thorough type-3 detection, 0 = unlimited)",
}

var similarityFlag = cli.Float64Flag{
	Name:  "similarity",
	Value: 0.70,
	Usage: "Minimum similarity for type-3 detection (0.50–1.00; 1.0 disables type-3)",
}

var stagedFlag = cli.BoolFlag{
	Name:  "staged",
	Usage: "Only report clones involving git-staged files (for pre-commit hooks)",
}

var minDuplicationFlag = cli.Float64Flag{
	Name:  "min-duplication",
	Value: 0,
	Usage: "Fail (exit 1) if global duplication percentage exceeds this value (0 = disabled)",
}

var configFlag = cli.StringFlag{
	Name:  "config, c",
	Value: "",
	Usage: "Path to config file (default: auto-discover .dupehound.yml walking up from cwd)",
}

var includeFlag = cli.StringSliceFlag{
	Name:  "include, i",
	Usage: "Glob patterns to include (can be repeated; if set, only matching files are scanned; supports **)",
}

var topFlag = cli.IntFlag{
	Name:  "top",
	Value: 10,
	Usage: "Max clones to show in text/md output (0 = show all)",
}

var sinceFlag = cli.StringFlag{
	Name:  "since",
	Value: "",
	Usage: "Git ref for diff-aware scanning (e.g. main, HEAD~5, v1.0.0); only reports clones touching changed lines",
}

var showSuppressedFlag = cli.BoolFlag{
	Name:  "show-suppressed",
	Usage: "Include suppressed clones in output (tagged as [suppressed])",
}

var deadCodeFlag = cli.BoolFlag{
	Name:  "dead-code",
	Usage: "Detect likely-dead functions (functions with no external callers); heuristic only",
}

var gitChurnFlag = cli.BoolFlag{
	Name:  "git-churn",
	Usage: "Annotate clones with git commit churn scores and re-sort by churn",
}

var churnDaysFlag = cli.IntFlag{
	Name:  "churn-days",
	Value: 90,
	Usage: "Rolling window in days for git churn counting (used with --git-churn)",
}

var maxFilesFlag = cli.IntFlag{
	Name:  "max-files",
	Value: 0,
	Usage: "Hard cap on the number of source files to scan (0 = no cap); fail fast on runaway trees",
}

var scanGeneratedFlag = cli.BoolFlag{
	Name:  "scan-generated",
	Usage: "Include machine-generated files (*.pb.go, *_gen.go, 'DO NOT EDIT' headers, etc.); skipped by default as they inflate duplication",
}

var maxPairsFlag = cli.IntFlag{
	Name:  "max-pairs",
	Value: 50000000,
	Usage: "Runaway backstop: cap type-3 detection past this many fuzzy candidate pairs, returning partial results (default 50M; 0 = unlimited). Guards CPU time on pathological inputs",
}

var maxFileSizeFlag = cli.Int64Flag{
	Name:  "max-file-size",
	Value: 5 * 1024 * 1024,
	Usage: "Skip files larger than this many bytes before reading them (default 5MiB, 0 = unlimited). Oversized files are almost always minified/generated and would dominate memory",
}

var baselineFlag = cli.StringFlag{
	Name:  "baseline",
	Value: "",
	Usage: "Compare against a baseline file: recorded clones are accepted debt, only NEW clones (or grown instance counts) fail the scan",
}

var writeBaselineFlag = cli.StringFlag{
	Name:  "write-baseline",
	Value: "",
	Usage: "Write the current clones to a baseline file (accepted debt) and exit 0; commit it and use --baseline in hooks/CI to block only new duplication",
}
