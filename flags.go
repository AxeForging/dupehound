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
	Usage: "Output format: text, json, sarif",
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
