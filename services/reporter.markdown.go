package services

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

// Markdown report for PR comments.

const (
	mdHotspotHeader  = "| Dup % | Dup Lines | Total Lines | File |\n|------:|----------:|------------:|------|\n"
	mdDetailsClose   = "\n</details>\n"
	mdDeadCodeNotice = "> **Note:** dead function detection is a heuristic. Cross-package calls, reflection, and interface implementations may produce false positives.\n\n"
)

var markdownLayout = reportLayout{
	title:     "## dupehound scan results\n\n",
	partial:   "> ⚠️ **Partial result:** %s\n\n",
	since:     "> Scanning diff since: **%s** (%d files changed)\n\n",
	tableHead: "| Metric | Value |\n|--------|-------|\n",
	row:       func(r metricRow) string { return "| " + r.md + " |\n" },
	breakdown: "| Breakdown | %s |\n",
	clones: sectionWording{
		title:    "\n### Top clones (by impact)\n\n",
		overflow: "\n> %d more clones not shown. Run `dupehound scan --verbose` or `--top 0` for full results.\n",
	},
	testProd: sectionWording{
		title:    "\n### Test↔Prod clones\n\n",
		overflow: "\n> %d more test↔prod clones not shown.\n",
	},
}

// formatMarkdown renders the report for a GitHub pull-request comment.
func formatMarkdown(report *domain.Report, opts FormatOptions) string {
	var b strings.Builder
	if !writePreamble(&b, report, markdownLayout) {
		return b.String()
	}
	if len(report.Clones) > 0 {
		writeHotspotsMd(&b, report, opts)
	}
	writeCloneSections(&b, report, opts, markdownLayout, writeCloneMd)
	writeDeadFunctionsMd(&b, report, opts)
	return b.String()
}

// writeHotspotsMd writes the per-file table, with the files beyond the cap
// folded into a <details> block.
func writeHotspotsMd(b *strings.Builder, report *domain.Report, opts FormatOptions) {
	if len(report.FileStats) == 0 {
		return
	}
	rows := func(files []domain.FileStats) {
		b.WriteString(mdHotspotHeader)
		for _, fs := range files {
			fmt.Fprintf(b, "| %.1f%% | %d | %d | `%s` |\n", fs.DuplicationPct, fs.DuplicateLines, fs.TotalLines, relPath(fs.File, opts.ScanPath))
		}
	}
	shown, rest := shownHotspots(report, opts)
	b.WriteString("\n### Hotspots\n\n")
	rows(shown)
	if len(rest) > 0 {
		fmt.Fprintf(b, "\n<details>\n<summary>%d more files...</summary>\n\n", len(rest))
		rows(rest)
		b.WriteString(mdDetailsClose)
	}
}

// writeDeadFunctionsMd lists dead functions, with those beyond the cap folded
// into a <details> block.
func writeDeadFunctionsMd(b *strings.Builder, report *domain.Report, opts FormatOptions) {
	if len(report.DeadFunctions) == 0 {
		return
	}
	items := func(funcs []domain.DeadFunc) {
		for _, df := range funcs {
			fmt.Fprintf(b, "- `%s:%d` — `%s`\n", relPath(df.File, opts.ScanPath), df.Line, df.Name)
		}
	}
	shown, overflow := deadFuncsShown(report, opts)
	fmt.Fprintf(b, "\n### Dead functions (%d)\n\n", len(report.DeadFunctions))
	b.WriteString(mdDeadCodeNotice)
	items(shown)
	if overflow > 0 {
		fmt.Fprintf(b, "\n<details>\n<summary>%d more dead functions...</summary>\n\n", overflow)
		items(report.DeadFunctions[len(shown):])
		b.WriteString(mdDetailsClose)
	}
}

// mdBadges builds the inline badges of a clone's summary line, so PR readers
// see savings, scope, test↔prod, churn, suppression and baseline at a glance.
func mdBadges(c domain.Clone) string {
	badges := ""
	add := func(on bool, format string, args ...any) {
		if on {
			badges += " &nbsp; " + fmt.Sprintf(format, args...)
		}
	}
	add(c.SavedLines > 0, "💾 ~%d lines", c.SavedLines)
	add(c.Scope != "", "📁 %s", c.Scope)
	add(c.TestProdSpan, "🧪 test↔prod")
	add(c.ChurnScore > 0, "🔥 churn %d", c.ChurnScore)
	add(c.Suppressed, "🚫 suppressed")
	add(c.Baseline, "📋 baseline")
	add(c.Rule != "", "📐 rule %s", ruleTag(c))
	return badges
}

func writeCloneMd(b *strings.Builder, num int, c domain.Clone, opts FormatOptions) {
	fmt.Fprintf(b, "<details>\n<summary>#%d &nbsp; <code>%s</code> &nbsp; similarity: %.2f &nbsp; %d lines &nbsp; %d instances%s</summary>\n\n",
		num, c.Type, c.Similarity, c.LineCount, len(c.Instances), mdBadges(c))

	b.WriteString("**Locations:**\n")
	shown, hidden := shownInstances(c, opts)
	for _, inst := range shown {
		extra := ""
		if inst.Function != "" {
			extra += fmt.Sprintf(" — in `%s`", inst.Function)
		}
		if inst.IsTest {
			extra += " _(test)_"
		}
		if inst.FileCommits > 0 {
			extra += fmt.Sprintf(" — %d commits in window", inst.FileCommits)
		}
		fmt.Fprintf(b, "- `%s:%d-%d`%s\n", relPath(inst.File, opts.ScanPath), inst.StartLine, inst.EndLine, extra)
	}
	if hidden > 0 {
		fmt.Fprintf(b, "- _… %d more locations (run with `--verbose` to show all)_\n", hidden)
	}

	if lines, more := previewOf(c, previewMaxLines*2); len(lines) > 0 {
		// The fence is tagged with the language of the first instance.
		fmt.Fprintf(b, "\n**Preview:**\n```%s\n", mdLangHint(c.Instances[0].File))
		for _, line := range lines {
			b.WriteString(line + "\n")
		}
		if more > 0 {
			fmt.Fprintf(b, "// ... %d more lines\n", more)
		}
		b.WriteString("```\n")
	}
	b.WriteString(mdDetailsClose + "\n")
}

func mdLangHint(file string) string {
	ext := strings.ToLower(filepath.Ext(file))
	switch ext {
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".rb":
		return "ruby"
	case ".swift":
		return "swift"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".cxx", ".hpp":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".php":
		return "php"
	case ".sh", ".bash", ".zsh":
		return "bash"
	case ".sql":
		return "sql"
	case ".lua":
		return "lua"
	case ".ex", ".exs":
		return "elixir"
	case ".dart":
		return "dart"
	case ".r", ".R":
		return "r"
	case ".scala":
		return "scala"
	default:
		return ""
	}
}
