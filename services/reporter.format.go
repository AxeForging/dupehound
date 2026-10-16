package services

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

// Report formatting entry point and the helpers every output format shares.

const (
	defaultTopClones   = 10
	defaultTopHotspots = 10
	previewMaxLines    = 5
	maxInstancesShown  = 6 // cap clone instances printed per clone (rest summarized as "+N more")
)

// FormatOptions controls text output presentation.
type FormatOptions struct {
	ScanPath       string // base path for relative file paths
	Verbose        bool   // show all clones with previews
	Top            int    // max clones shown (0 = use defaultTopClones, -1 = all)
	ShowSuppressed bool   // include suppressed clones in output
}

// FormatReport formats a Report as text, json, sarif, md, or github.
func FormatReport(report *domain.Report, format string, opts FormatOptions) (string, error) {
	switch strings.ToLower(format) {
	case "text", "":
		return formatText(report, opts), nil
	case "json":
		return formatJSON(report)
	case "sarif":
		return formatSARIF(report)
	case "md":
		return formatMarkdown(report, opts), nil
	case "github":
		return formatGitHub(report, opts), nil
	default:
		return "", fmt.Errorf("unknown format %q: must be text, json, sarif, md, or github", format)
	}
}

// --- shared helpers ---

func sortClonesByImpact(clones []domain.Clone) []domain.Clone {
	sorted := make([]domain.Clone, len(clones))
	copy(sorted, clones)
	impact := func(c domain.Clone) int { return c.LineCount * max(len(c.Instances)-1, 1) }
	// Hash is the last key so the order is total and the output stable.
	slices.SortFunc(sorted, func(a, b domain.Clone) int {
		return cmp.Or(cmp.Compare(impact(b), impact(a)), cmp.Compare(b.TokenCount, a.TokenCount), cmp.Compare(a.Hash, b.Hash))
	})
	return sorted
}

func typeBreakdown(clones []domain.Clone) string {
	typeCounts := map[string]int{}
	for _, c := range clones {
		typeCounts[c.Type]++
	}
	var parts []string
	for _, t := range []string{domain.CloneType1, domain.CloneType2, domain.CloneType3} {
		if n := typeCounts[t]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, t))
		}
	}
	return strings.Join(parts, ", ")
}

func topN(total int, defaultN int, verbose bool) int {
	if verbose || total <= defaultN {
		return total
	}
	return defaultN
}

// topNWithOpt returns how many items to show, respecting the Top option.
// top == 0 means use defaultTopClones; top == -1 or verbose means show all.
func topNWithOpt(total int, opt FormatOptions) int {
	if opt.Verbose {
		return total
	}
	n := opt.Top
	if n == 0 {
		n = defaultTopClones
	}
	if n < 0 || n >= total {
		return total
	}
	return n
}

// partitionClones splits a sorted clone slice into test↔prod and normal
// clones, skipping suppressed ones unless showSuppressed is set.
func partitionClones(sorted []domain.Clone, showSuppressed bool) (testProd, normal []domain.Clone) {
	for _, c := range sorted {
		if c.Suppressed && !showSuppressed {
			continue
		}
		if c.TestProdSpan {
			testProd = append(testProd, c)
		} else {
			normal = append(normal, c)
		}
	}
	return
}

// metricRow is one row of the summary block. The text and markdown formatters
// emit the same rows under the same conditions but with different label text
// and layout, so each row carries both pre-rendered forms. Centralizing the
// "which rows, in what order, under what condition" decision here keeps the two
// formatters from drifting and removes what was a near-identical block
// duplicated across formatText and formatMarkdown.
type metricRow struct {
	text string // full text line, sans newline: "Files scanned : 17 / 17"
	md   string // markdown "label | value", rendered as a "| %s |" table row
}

// summaryRows builds the report's summary metrics once for both formatters.
func summaryRows(r *domain.Report) []metricRow {
	rows := []metricRow{{
		text: fmt.Sprintf("Files scanned : %d / %d", r.ScannedFiles, r.TotalFiles),
		md:   fmt.Sprintf("Files scanned | %d / %d", r.ScannedFiles, r.TotalFiles),
	}}
	if r.SkippedFiles > 0 {
		rows = append(rows, newRow("Skipped files : %d (binary)", "Skipped (binary) | %d", r.SkippedFiles))
	}
	if r.SkippedLargeFiles > 0 {
		rows = append(rows, newRow("Skipped files : %d (over --max-file-size)", "Skipped (oversized) | %d", r.SkippedLargeFiles))
	}
	if r.SkippedByRule > 0 {
		rows = append(rows, newRow("Skipped files : %d (skip rule)", "Skipped (skip rule) | %d", r.SkippedByRule))
	}
	rows = append(rows, newRow("Total lines   : %d", "Total lines | %d", r.TotalLines))
	if r.SuppressedClones > 0 {
		rows = append(rows, newRow("Clones found  : %d (%d suppressed)", "Clones found | %d (%d suppressed)", r.TotalClones, r.SuppressedClones))
	} else {
		rows = append(rows, newRow("Clones found  : %d", "Clones found | %d", r.TotalClones))
	}
	rows = append(rows, newRow("Duplicate lines: %d (%.1f%%)", "Duplicate lines | %d (%.1f%%)", r.DuplicateLines, r.DuplicationPct))
	if r.SavedLines > 0 {
		rows = append(rows, newRow("Refactor value: ~%d lines removable by deduplication", "Refactor value | ~%d lines removable", r.SavedLines))
	}
	if r.BaselineFile != "" {
		rows = append(rows, metricRow{
			text: fmt.Sprintf("Baseline      : %d known (accepted debt), %d NEW", r.BaselineKnown, r.BaselineNew),
			md:   fmt.Sprintf("Baseline (%s) | %d known, **%d new**", r.BaselineFile, r.BaselineKnown, r.BaselineNew),
		})
	}
	if r.SinceDiffRef != "" {
		rows = append(rows, metricRow{
			text: fmt.Sprintf("New clones    : %d since %s", len(r.NewClones), r.SinceDiffRef),
			md:   fmt.Sprintf("New clones since %s | %d", r.SinceDiffRef, len(r.NewClones)),
		})
	}
	return rows
}

// sortedPartition is the shared first step of both clone sections: sort by
// impact, then split into test↔prod and normal (honoring suppression).
func sortedPartition(r *domain.Report, opts FormatOptions) (testProd, normal []domain.Clone) {
	return partitionClones(sortClonesByImpact(r.Clones), opts.ShowSuppressed)
}

// deadFuncsShown returns the capped slice of dead functions to display and the
// count hidden by the cap — the selection logic both formatters share.
func deadFuncsShown(r *domain.Report, opts FormatOptions) (shown []domain.DeadFunc, overflow int) {
	limit := topNWithOpt(len(r.DeadFunctions), opts)
	return r.DeadFunctions[:limit], len(r.DeadFunctions) - limit
}

func relPath(absPath, basePath string) string {
	if basePath == "" {
		return absPath
	}
	rel, err := filepath.Rel(basePath, absPath)
	if err != nil {
		return absPath
	}
	return rel
}

// instanceLabel renders the location of one clone instance with its enclosing
// function, e.g. "services/foo.go:10-42 (in parseConfig)".
func instanceLabel(inst domain.CloneInstance, scanPath string) string {
	s := fmt.Sprintf("%s:%d-%d", relPath(inst.File, scanPath), inst.StartLine, inst.EndLine)
	if inst.Function != "" {
		s += fmt.Sprintf(" (in %s)", inst.Function)
	}
	return s
}

// cloneFunctionList renders the distinct enclosing-function names of a clone's
// instances, e.g. "in parseConfig, loadSettings", or "" when none are known.
func cloneFunctionList(c domain.Clone) string {
	var names []string
	seen := map[string]bool{}
	for _, inst := range c.Instances {
		if inst.Function != "" && !seen[inst.Function] {
			seen[inst.Function] = true
			names = append(names, inst.Function)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "in " + strings.Join(names, ", ")
}

// ruleTag names the rule that judged a clone and its threshold, for example
// "tests ≥25", as every format shows it.
func ruleTag(c domain.Clone) string {
	return fmt.Sprintf("%s ≥%d", c.Rule, c.RuleMinTokens)
}

// newRow renders one summary metric for both the text and the markdown report.
func newRow(textFormat, mdFormat string, args ...any) metricRow {
	return metricRow{text: fmt.Sprintf(textFormat, args...), md: fmt.Sprintf(mdFormat, args...)}
}

// The helpers below decide WHAT a report shows — how many clones, files,
// locations and preview lines, and what is folded away. Each output format then
// only decides how to write it, so the caps cannot drift apart between formats.

// shownClones returns the clones to list under --top and how many are left out.
func shownClones(clones []domain.Clone, opts FormatOptions) (shown []domain.Clone, hidden int) {
	limit := topNWithOpt(len(clones), opts)
	return clones[:limit], len(clones) - limit
}

// shownHotspots splits the per-file statistics into the files listed up front
// and the rest.
func shownHotspots(report *domain.Report, opts FormatOptions) (shown, rest []domain.FileStats) {
	limit := topN(len(report.FileStats), defaultTopHotspots, opts.Verbose)
	return report.FileStats[:limit], report.FileStats[limit:]
}

// shownInstances returns the locations of a clone to list and how many are
// left out.
func shownInstances(c domain.Clone, opts FormatOptions) (shown []domain.CloneInstance, hidden int) {
	limit := len(c.Instances)
	if !opts.Verbose && limit > maxInstancesShown {
		limit = maxInstancesShown
	}
	return c.Instances[:limit], len(c.Instances) - limit
}

// previewOf returns up to maxLines of a clone's first instance, trailing
// whitespace removed, and how many lines are left out.
func previewOf(c domain.Clone, maxLines int) (lines []string, hidden int) {
	if len(c.Instances) == 0 {
		return nil, 0
	}
	all := c.Instances[0].Lines
	limit := min(len(all), maxLines)
	for _, line := range all[:limit] {
		lines = append(lines, strings.TrimRight(line, " \t"))
	}
	return lines, len(all) - limit
}

// cloneSection is one titled group of clones in a report.
type cloneSection struct {
	testProd bool
	clones   []domain.Clone
}

// cloneSections returns the groups a report lists, in order: clones spanning
// test and production code first when there are any, then everything else.
func cloneSections(report *domain.Report, opts FormatOptions) []cloneSection {
	if len(report.Clones) == 0 {
		return nil
	}
	testProd, normal := sortedPartition(report, opts)
	var sections []cloneSection
	if len(testProd) > 0 {
		sections = append(sections, cloneSection{testProd: true, clones: testProd})
	}
	return append(sections, cloneSection{clones: normal})
}

// reportLayout holds what differs between the text and the markdown report in
// the parts they share: the wording. The structure — which notices appear,
// in what order, when the report stops early — is written once below.
type reportLayout struct {
	title     string // heading
	partial   string // format: partial-result reason
	since     string // format: diff ref, changed file count
	tableHead string // opens the summary metrics
	row       func(metricRow) string
	breakdown string // format: clone type breakdown

	// Section heading and overflow format, for the ordinary clones and for
	// those spanning test and production code.
	clones, testProd sectionWording
}

type sectionWording struct{ title, overflow string }

// writePreamble writes the heading, notices and summary metrics. It reports
// false when there is nothing further to list.
func writePreamble(b *strings.Builder, report *domain.Report, l reportLayout) bool {
	b.WriteString(l.title)
	if report.Partial {
		fmt.Fprintf(b, l.partial, report.PartialReason)
	}
	if report.SinceDiffRef != "" {
		fmt.Fprintf(b, l.since, report.SinceDiffRef, report.SinceDiffFiles)
	}
	b.WriteString(l.tableHead)
	for _, row := range summaryRows(report) {
		b.WriteString(l.row(row))
	}
	if report.TotalClones == 0 && len(report.DeadFunctions) == 0 {
		b.WriteString("\nNo duplicates found.\n")
		return false
	}
	if len(report.Clones) > 0 {
		fmt.Fprintf(b, l.breakdown, typeBreakdown(report.Clones))
	}
	return true
}

// writeCloneSections writes each group of clones under its heading, capped by
// --top, using writeClone for the individual entries.
func writeCloneSections(b *strings.Builder, report *domain.Report, opts FormatOptions, l reportLayout,
	writeClone func(*strings.Builder, int, domain.Clone, FormatOptions),
) {
	for _, section := range cloneSections(report, opts) {
		wording := l.clones
		if section.testProd {
			wording = l.testProd
		}
		b.WriteString(wording.title)
		shown, hidden := shownClones(section.clones, opts)
		for i, c := range shown {
			writeClone(b, i+1, c, opts)
		}
		if hidden > 0 {
			fmt.Fprintf(b, wording.overflow, hidden)
		}
	}
}
