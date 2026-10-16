package services

import (
	"fmt"
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

// GitHub Actions workflow-command annotations.

// --- github format (GitHub Actions workflow-command annotations) ---

// ghEscapeData escapes annotation message data per GitHub's workflow-command
// rules: % first, then CR/LF.
func ghEscapeData(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "\r", "%0D")
	s = strings.ReplaceAll(s, "\n", "%0A")
	return s
}

// ghEscapeProp escapes annotation property values (title, file), which
// additionally reserve `:` and `,`.
func ghEscapeProp(s string) string {
	s = ghEscapeData(s)
	s = strings.ReplaceAll(s, ":", "%3A")
	s = strings.ReplaceAll(s, ",", "%2C")
	return s
}

// formatGitHub renders the report as GitHub Actions workflow commands —
// `::warning file=…,line=…::…` — so a plain `dupehound scan --format github`
// step produces inline PR annotations with zero SARIF/upload setup.
// The clone cap (--top / --verbose) applies per the usual rules; every shown
// clone annotates its primary instance and lists the duplicates in the message.
func formatGitHub(report *domain.Report, opts FormatOptions) string {
	var b strings.Builder

	if report.Partial {
		fmt.Fprintf(&b, "::warning title=%s::%s\n",
			ghEscapeProp("dupehound: partial result"), ghEscapeData(report.PartialReason))
	}

	testProdClones, normalClones := sortedPartition(report, opts)
	all := append(append([]domain.Clone{}, testProdClones...), normalClones...)
	limit := topNWithOpt(len(all), opts)

	for i := 0; i < limit; i++ {
		c := all[i]
		primary := c.Instances[0]

		title := fmt.Sprintf("dupehound: %s clone", c.Type)
		if c.SavedLines > 0 {
			title += fmt.Sprintf(", ~%d lines saveable", c.SavedLines)
		}
		if c.TestProdSpan {
			title += ", test<->prod"
		}

		var dups []string
		for _, inst := range c.Instances[1:] {
			dups = append(dups, instanceLabel(inst, opts.ScanPath))
		}
		msg := fmt.Sprintf("%d duplicated lines (%d instances, similarity %.2f)", c.LineCount, len(c.Instances), c.Similarity)
		if primary.Function != "" {
			msg += fmt.Sprintf(" in %s", primary.Function)
		}
		if len(dups) > 0 {
			msg += "; duplicated at " + strings.Join(dups, ", ")
		}
		if c.Rule != "" {
			msg += fmt.Sprintf(" [rule %s]", ruleTag(c))
		}

		fmt.Fprintf(&b, "::warning file=%s,line=%d,endLine=%d,title=%s::%s\n",
			ghEscapeProp(relPath(primary.File, opts.ScanPath)), primary.StartLine, primary.EndLine,
			ghEscapeProp(title), ghEscapeData(msg))
	}
	if len(all) > limit {
		fmt.Fprintf(&b, "::notice title=%s::%s\n",
			ghEscapeProp("dupehound"),
			ghEscapeData(fmt.Sprintf("%d more clones not annotated (raise --top or use --verbose)", len(all)-limit)))
	}

	for _, df := range report.DeadFunctions {
		fmt.Fprintf(&b, "::warning file=%s,line=%d,title=%s::%s\n",
			ghEscapeProp(relPath(df.File, opts.ScanPath)), df.Line,
			ghEscapeProp("dupehound: likely-dead function"),
			ghEscapeData(fmt.Sprintf("%s appears to have no callers (heuristic; verify before removing)", df.Name)))
	}

	summary := fmt.Sprintf("%d clones, %.1f%% duplication", report.TotalClones, report.DuplicationPct)
	if report.SavedLines > 0 {
		summary += fmt.Sprintf(", ~%d lines removable", report.SavedLines)
	}
	if report.BaselineFile != "" {
		summary += fmt.Sprintf(" — baseline: %d known, %d new", report.BaselineKnown, report.BaselineNew)
	}
	fmt.Fprintf(&b, "::notice title=%s::%s\n", ghEscapeProp("dupehound summary"), ghEscapeData(summary))

	return b.String()
}
