package services

import (
	"fmt"
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

// Plain-text report for terminals.

var textLayout = reportLayout{
	title:     "dupehound scan results\n======================\n",
	partial:   "!! PARTIAL RESULT: %s\n",
	since:     "Scanning diff  : since %s  (%d files changed)\n",
	row:       func(r metricRow) string { return r.text + "\n" },
	breakdown: "Breakdown     : %s\n",
	clones: sectionWording{
		title:    "\nTop clones (by impact):\n",
		overflow: "  ... %d more clones (use --verbose or --top 0 to show all)\n\n",
	},
	testProd: sectionWording{
		title:    "\nTest↔Prod clones (span test and production files):\n",
		overflow: "  ... %d more test↔prod clones (use --verbose or --top 0 to show all)\n\n",
	},
}

// formatText renders the report for a terminal.
func formatText(report *domain.Report, opts FormatOptions) string {
	var b strings.Builder
	if !writePreamble(&b, report, textLayout) {
		return b.String()
	}
	writeHotspotsText(&b, report, opts)
	writeCloneSections(&b, report, opts, textLayout, writeCloneText)
	writeDeadFunctionsText(&b, report, opts)
	return b.String()
}

func writeHotspotsText(b *strings.Builder, report *domain.Report, opts FormatOptions) {
	if len(report.FileStats) == 0 {
		return
	}
	shown, rest := shownHotspots(report, opts)
	b.WriteString("\nHotspots (files by duplication %):\n")
	for _, fs := range shown {
		fmt.Fprintf(b, "  %5.1f%%  %4d/%4d lines  %s\n", fs.DuplicationPct, fs.DuplicateLines, fs.TotalLines, relPath(fs.File, opts.ScanPath))
	}
	if len(rest) > 0 {
		fmt.Fprintf(b, "  ... %d more files (use --verbose to show all)\n", len(rest))
	}
}

func writeDeadFunctionsText(b *strings.Builder, report *domain.Report, opts FormatOptions) {
	if len(report.DeadFunctions) == 0 {
		return
	}
	shown, overflow := deadFuncsShown(report, opts)
	fmt.Fprintf(b, "\nDead functions : %d\n", len(report.DeadFunctions))
	b.WriteString("Note: dead function detection is a heuristic. Cross-package calls, reflection, and interface implementations may produce false positives.\n")
	for _, df := range shown {
		fmt.Fprintf(b, "  %s:%d\t%s\n", relPath(df.File, opts.ScanPath), df.Line, df.Name)
	}
	if overflow > 0 {
		fmt.Fprintf(b, "  ... %d more (use --verbose or --top 0 to show all)\n", overflow)
	}
}

// cloneBadges builds the inline badge string for a clone in text output:
// refactor savings, test↔prod span, scope, suppression, baseline and churn.
func cloneBadges(c domain.Clone) string {
	tag := ""
	if c.SavedLines > 0 {
		tag += fmt.Sprintf("  saves ~%d lines", c.SavedLines)
	}
	if c.TestProdSpan {
		tag += " [test↔prod]"
	}
	if c.Scope != "" {
		tag += fmt.Sprintf(" [%s]", c.Scope)
	}
	if c.Suppressed {
		tag += " [suppressed]"
	}
	if c.Baseline {
		tag += " [baseline]"
	}
	if c.ChurnScore > 0 {
		tag += fmt.Sprintf(" [churn: %d commits]", c.ChurnScore)
	}
	return tag
}

func writeCloneText(b *strings.Builder, num int, c domain.Clone, opts FormatOptions) {
	fmt.Fprintf(b, "  #%-4d %s  similarity: %.2f  %d lines  %d tokens  %d instances%s\n",
		num, c.Type, c.Similarity, c.LineCount, c.TokenCount, len(c.Instances), cloneBadges(c))

	shown, hidden := shownInstances(c, opts)
	for _, inst := range shown {
		label := instanceLabel(inst, opts.ScanPath)
		if inst.IsTest {
			label += " [test]"
		}
		if inst.FileCommits > 0 {
			label += fmt.Sprintf(" (%d commits)", inst.FileCommits)
		}
		b.WriteString("        " + label + "\n")
	}
	if hidden > 0 {
		fmt.Fprintf(b, "        ... %d more locations (use --verbose to show all)\n", hidden)
	}

	lines, more := previewOf(c, previewMaxLines)
	for _, line := range lines {
		b.WriteString("        | " + line + "\n")
	}
	if more > 0 {
		fmt.Fprintf(b, "        ... (%d more lines)\n", more)
	}
	b.WriteString("\n")
}
