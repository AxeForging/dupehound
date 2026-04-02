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
