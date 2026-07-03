package services

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

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
	sort.Slice(sorted, func(i, j int) bool {
		impactI := sorted[i].LineCount * max(len(sorted[i].Instances)-1, 1)
		impactJ := sorted[j].LineCount * max(len(sorted[j].Instances)-1, 1)
		if impactI != impactJ {
			return impactI > impactJ
		}
		if sorted[i].TokenCount != sorted[j].TokenCount {
			return sorted[i].TokenCount > sorted[j].TokenCount
		}
		return sorted[i].Hash < sorted[j].Hash // total order for stable output
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

// writeClonesTextSection writes a capped list of clones in text format and an
// overflow line. overflowLabel goes into "  ... N more <label> (use --verbose…)".
func writeClonesTextSection(b *strings.Builder, clones []domain.Clone, opts FormatOptions, overflowLabel string) {
	limit := topNWithOpt(len(clones), opts)
	for i := 0; i < limit; i++ {
		writeCloneTextWithTag(b, i+1, clones[i], opts, true, cloneBadges(clones[i]))
	}
	if len(clones) > limit {
		fmt.Fprintf(b, "  ... %d more %s (use --verbose or --top 0 to show all)\n\n", len(clones)-limit, overflowLabel)
	}
}

// writeClonesMdSection writes a capped list of clones in markdown format.
// overflowFmt must contain exactly one %d for the remaining count.
func writeClonesMdSection(b *strings.Builder, clones []domain.Clone, opts FormatOptions, overflowFmt string) {
	limit := topNWithOpt(len(clones), opts)
	for i := 0; i < limit; i++ {
		writeCloneMd(b, i+1, clones[i], opts)
	}
	if len(clones) > limit {
		fmt.Fprintf(b, overflowFmt, len(clones)-limit)
	}
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
		rows = append(rows, metricRow{
			text: fmt.Sprintf("Skipped files : %d (binary)", r.SkippedFiles),
			md:   fmt.Sprintf("Skipped (binary) | %d", r.SkippedFiles),
		})
	}
	if r.SkippedLargeFiles > 0 {
		rows = append(rows, metricRow{
			text: fmt.Sprintf("Skipped files : %d (over --max-file-size)", r.SkippedLargeFiles),
			md:   fmt.Sprintf("Skipped (oversized) | %d", r.SkippedLargeFiles),
		})
	}
	rows = append(rows, metricRow{
		text: fmt.Sprintf("Total lines   : %d", r.TotalLines),
		md:   fmt.Sprintf("Total lines | %d", r.TotalLines),
	})
	if r.SuppressedClones > 0 {
		rows = append(rows, metricRow{
			text: fmt.Sprintf("Clones found  : %d (%d suppressed)", r.TotalClones, r.SuppressedClones),
			md:   fmt.Sprintf("Clones found | %d (%d suppressed)", r.TotalClones, r.SuppressedClones),
		})
	} else {
		rows = append(rows, metricRow{
			text: fmt.Sprintf("Clones found  : %d", r.TotalClones),
			md:   fmt.Sprintf("Clones found | %d", r.TotalClones),
		})
	}
	rows = append(rows, metricRow{
		text: fmt.Sprintf("Duplicate lines: %d (%.1f%%)", r.DuplicateLines, r.DuplicationPct),
		md:   fmt.Sprintf("Duplicate lines | %d (%.1f%%)", r.DuplicateLines, r.DuplicationPct),
	})
	if r.SavedLines > 0 {
		rows = append(rows, metricRow{
			text: fmt.Sprintf("Refactor value: ~%d lines removable by deduplication", r.SavedLines),
			md:   fmt.Sprintf("Refactor value | ~%d lines removable", r.SavedLines),
		})
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

// --- text format ---

func formatText(report *domain.Report, opts FormatOptions) string {
	var b strings.Builder

	fmt.Fprintf(&b, "dupehound scan results\n")
	fmt.Fprintf(&b, "======================\n")
	if report.Partial {
		fmt.Fprintf(&b, "!! PARTIAL RESULT: %s\n", report.PartialReason)
	}
	if report.SinceDiffRef != "" {
		fmt.Fprintf(&b, "Scanning diff  : since %s  (%d files changed)\n", report.SinceDiffRef, report.SinceDiffFiles)
	}
	for _, row := range summaryRows(report) {
		fmt.Fprintf(&b, "%s\n", row.text)
	}

	if report.TotalClones == 0 && len(report.DeadFunctions) == 0 {
		fmt.Fprintf(&b, "\nNo duplicates found.\n")
		return b.String()
	}

	if len(report.Clones) > 0 {
		fmt.Fprintf(&b, "Breakdown     : %s\n", typeBreakdown(report.Clones))
	}

	if len(report.FileStats) > 0 {
		limit := topN(len(report.FileStats), defaultTopHotspots, opts.Verbose)
		fmt.Fprintf(&b, "\nHotspots (files by duplication %%):\n")
		for i := 0; i < limit; i++ {
			fs := report.FileStats[i]
			rel := relPath(fs.File, opts.ScanPath)
			fmt.Fprintf(&b, "  %5.1f%%  %4d/%4d lines  %s\n", fs.DuplicationPct, fs.DuplicateLines, fs.TotalLines, rel)
		}
		if len(report.FileStats) > limit {
			fmt.Fprintf(&b, "  ... %d more files (use --verbose to show all)\n", len(report.FileStats)-limit)
		}
	}

	if len(report.Clones) > 0 {
		testProdClones, normalClones := sortedPartition(report, opts)

		if len(testProdClones) > 0 {
			fmt.Fprintf(&b, "\nTest↔Prod clones (span test and production files):\n")
			writeClonesTextSection(&b, testProdClones, opts, "test↔prod clones")
		}

		fmt.Fprintf(&b, "\nTop clones (by impact):\n")
		writeClonesTextSection(&b, normalClones, opts, "clones")
	}

	// Dead function report.
	if len(report.DeadFunctions) > 0 {
		shown, overflow := deadFuncsShown(report, opts)
		fmt.Fprintf(&b, "\nDead functions : %d\n", len(report.DeadFunctions))
		fmt.Fprintf(&b, "Note: dead function detection is a heuristic. Cross-package calls, reflection, and interface implementations may produce false positives.\n")
		for _, df := range shown {
			fmt.Fprintf(&b, "  %s:%d\t%s\n", relPath(df.File, opts.ScanPath), df.Line, df.Name)
		}
		if overflow > 0 {
			fmt.Fprintf(&b, "  ... %d more (use --verbose or --top 0 to show all)\n", overflow)
		}
	}

	return b.String()
}

// cloneBadges builds the inline badge string for a clone in text output —
// includes scope, suppression, baseline, and churn signals (test↔prod is
// rendered separately inside writeCloneTextWithTag).
func cloneBadges(c domain.Clone) string {
	tag := ""
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

// instanceLabel renders the location of one clone instance with its enclosing
// function, e.g. "services/foo.go:10-42 (in parseConfig)".
func instanceLabel(inst domain.CloneInstance, scanPath string) string {
	s := fmt.Sprintf("%s:%d-%d", relPath(inst.File, scanPath), inst.StartLine, inst.EndLine)
	if inst.Function != "" {
		s += fmt.Sprintf(" (in %s)", inst.Function)
	}
	return s
}

func writeCloneTextWithTag(b *strings.Builder, num int, c domain.Clone, opts FormatOptions, showPreview bool, tag string) {
	testProdLabel := ""
	if c.TestProdSpan {
		testProdLabel = " [test↔prod]"
	}
	savesLabel := ""
	if c.SavedLines > 0 {
		savesLabel = fmt.Sprintf("  saves ~%d lines", c.SavedLines)
	}
	fmt.Fprintf(b, "  #%-4d %s  similarity: %.2f  %d lines  %d tokens  %d instances%s%s%s\n",
		num, c.Type, c.Similarity, c.LineCount, c.TokenCount, len(c.Instances), savesLabel, testProdLabel, tag)
	instLimit := len(c.Instances)
	if !opts.Verbose && instLimit > maxInstancesShown {
		instLimit = maxInstancesShown
	}
	for i := 0; i < instLimit; i++ {
		inst := c.Instances[i]
		testLabel := ""
		if inst.IsTest {
			testLabel = " [test]"
		}
		churnLabel := ""
		if inst.FileCommits > 0 {
			churnLabel = fmt.Sprintf(" (%d commits)", inst.FileCommits)
		}
		fmt.Fprintf(b, "        %s%s%s\n", instanceLabel(inst, opts.ScanPath), testLabel, churnLabel)
	}
	if len(c.Instances) > instLimit {
		fmt.Fprintf(b, "        ... %d more locations (use --verbose to show all)\n", len(c.Instances)-instLimit)
	}
	if showPreview && len(c.Instances) > 0 {
		lines := c.Instances[0].Lines
		limit := previewMaxLines
		if len(lines) < limit {
			limit = len(lines)
		}
		if limit > 0 {
			for _, line := range lines[:limit] {
				fmt.Fprintf(b, "        | %s\n", strings.TrimRight(line, " \t"))
			}
			if len(lines) > previewMaxLines {
				fmt.Fprintf(b, "        ... (%d more lines)\n", len(lines)-previewMaxLines)
			}
		}
	}
	fmt.Fprintf(b, "\n")
}

// --- markdown format (GitHub PR comments) ---

func formatMarkdown(report *domain.Report, opts FormatOptions) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## dupehound scan results\n\n")
	if report.Partial {
		fmt.Fprintf(&b, "> ⚠️ **Partial result:** %s\n\n", report.PartialReason)
	}
	if report.SinceDiffRef != "" {
		fmt.Fprintf(&b, "> Scanning diff since: **%s** (%d files changed)\n\n", report.SinceDiffRef, report.SinceDiffFiles)
	}
	fmt.Fprintf(&b, "| Metric | Value |\n")
	fmt.Fprintf(&b, "|--------|-------|\n")
	for _, row := range summaryRows(report) {
		fmt.Fprintf(&b, "| %s |\n", row.md)
	}

	if report.TotalClones == 0 && len(report.DeadFunctions) == 0 {
		fmt.Fprintf(&b, "\nNo duplicates found.\n")
		return b.String()
	}

	if len(report.Clones) > 0 {
		fmt.Fprintf(&b, "| Breakdown | %s |\n", typeBreakdown(report.Clones))
	}

	// --- Hotspots / clones (skip when there are no clones at all) ---
	if len(report.Clones) > 0 && len(report.FileStats) > 0 {
		limit := topN(len(report.FileStats), defaultTopHotspots, opts.Verbose)

		fmt.Fprintf(&b, "\n### Hotspots\n\n")
		fmt.Fprintf(&b, "| Dup %% | Dup Lines | Total Lines | File |\n")
		fmt.Fprintf(&b, "|------:|----------:|------------:|------|\n")
		for i := 0; i < limit; i++ {
			fs := report.FileStats[i]
			rel := relPath(fs.File, opts.ScanPath)
			fmt.Fprintf(&b, "| %.1f%% | %d | %d | `%s` |\n", fs.DuplicationPct, fs.DuplicateLines, fs.TotalLines, rel)
		}

		if len(report.FileStats) > limit {
			fmt.Fprintf(&b, "\n<details>\n<summary>%d more files...</summary>\n\n", len(report.FileStats)-limit)
			fmt.Fprintf(&b, "| Dup %% | Dup Lines | Total Lines | File |\n")
			fmt.Fprintf(&b, "|------:|----------:|------------:|------|\n")
			for i := limit; i < len(report.FileStats); i++ {
				fs := report.FileStats[i]
				rel := relPath(fs.File, opts.ScanPath)
				fmt.Fprintf(&b, "| %.1f%% | %d | %d | `%s` |\n", fs.DuplicationPct, fs.DuplicateLines, fs.TotalLines, rel)
			}
			fmt.Fprintf(&b, "\n</details>\n")
		}
	}

	// --- Test↔Prod / Top clones (only when clones exist) ---
	if len(report.Clones) > 0 {
		testProdClones, normalClones := sortedPartition(report, opts)

		if len(testProdClones) > 0 {
			fmt.Fprintf(&b, "\n### Test↔Prod clones\n\n")
			writeClonesMdSection(&b, testProdClones, opts, "\n> %d more test↔prod clones not shown.\n")
		}

		fmt.Fprintf(&b, "\n### Top clones (by impact)\n\n")
		writeClonesMdSection(&b, normalClones, opts, "\n> %d more clones not shown. Run `dupehound scan --verbose` or `--top 0` for full results.\n")
	}

	// --- Dead functions (capped to top-N, overflow in <details>) ---
	if len(report.DeadFunctions) > 0 {
		shown, overflow := deadFuncsShown(report, opts)
		writeDeadFunc := func(df domain.DeadFunc) {
			fmt.Fprintf(&b, "- `%s:%d` — `%s`\n", relPath(df.File, opts.ScanPath), df.Line, df.Name)
		}
		fmt.Fprintf(&b, "\n### Dead functions (%d)\n\n", len(report.DeadFunctions))
		fmt.Fprintf(&b, "> **Note:** dead function detection is a heuristic. Cross-package calls, reflection, and interface implementations may produce false positives.\n\n")
		for _, df := range shown {
			writeDeadFunc(df)
		}
		if overflow > 0 {
			fmt.Fprintf(&b, "\n<details>\n<summary>%d more dead functions...</summary>\n\n", overflow)
			for _, df := range report.DeadFunctions[len(shown):] {
				writeDeadFunc(df)
			}
			fmt.Fprintf(&b, "\n</details>\n")
		}
	}

	return b.String()
}

func writeCloneMd(b *strings.Builder, num int, c domain.Clone, opts FormatOptions) {
	// Detect language from first instance file extension for syntax highlighting.
	lang := ""
	if len(c.Instances) > 0 {
		lang = mdLangHint(c.Instances[0].File)
	}

	// Build inline badges so PR readers see scope / test↔prod / churn /
	// suppression / baseline state at a glance.
	badges := ""
	if c.SavedLines > 0 {
		badges += fmt.Sprintf(" &nbsp; 💾 ~%d lines", c.SavedLines)
	}
	if c.Scope != "" {
		badges += fmt.Sprintf(" &nbsp; 📁 %s", c.Scope)
	}
	if c.TestProdSpan {
		badges += " &nbsp; 🧪 test↔prod"
	}
	if c.ChurnScore > 0 {
		badges += fmt.Sprintf(" &nbsp; 🔥 churn %d", c.ChurnScore)
	}
	if c.Suppressed {
		badges += " &nbsp; 🚫 suppressed"
	}
	if c.Baseline {
		badges += " &nbsp; 📋 baseline"
	}

	summary := fmt.Sprintf("#%d &nbsp; <code>%s</code> &nbsp; similarity: %.2f &nbsp; %d lines &nbsp; %d instances%s",
		num, c.Type, c.Similarity, c.LineCount, len(c.Instances), badges)

	fmt.Fprintf(b, "<details>\n<summary>%s</summary>\n\n", summary)

	fmt.Fprintf(b, "**Locations:**\n")
	instLimit := len(c.Instances)
	if !opts.Verbose && instLimit > maxInstancesShown {
		instLimit = maxInstancesShown
	}
	for i := 0; i < instLimit; i++ {
		inst := c.Instances[i]
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
	if len(c.Instances) > instLimit {
		fmt.Fprintf(b, "- _… %d more locations (run with `--verbose` to show all)_\n", len(c.Instances)-instLimit)
	}

	if len(c.Instances) > 0 && len(c.Instances[0].Lines) > 0 {
		fmt.Fprintf(b, "\n**Preview:**\n")
		fmt.Fprintf(b, "```%s\n", lang)
		lines := c.Instances[0].Lines
		limit := previewMaxLines * 2
		if len(lines) < limit {
			limit = len(lines)
		}
		for _, line := range lines[:limit] {
			fmt.Fprintf(b, "%s\n", strings.TrimRight(line, " \t"))
		}
		if len(lines) > limit {
			fmt.Fprintf(b, "// ... %d more lines\n", len(lines)-limit)
		}
		fmt.Fprintf(b, "```\n")
	}

	fmt.Fprintf(b, "\n</details>\n\n")
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

// --- JSON format ---

func formatJSON(report *domain.Report) (string, error) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal report: %w", err)
	}
	return string(data), nil
}

// --- SARIF format ---

type sarifReport struct {
	Version string     `json:"version"`
	Schema  string     `json:"$schema"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool       sarifTool      `json:"tool"`
	Results    []sarifResult  `json:"results"`
	Properties map[string]any `json:"properties,omitempty"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string       `json:"id"`
	ShortDescription sarifMessage `json:"shortDescription"`
}

type sarifResult struct {
	RuleID     string                `json:"ruleId"`
	Message    sarifMessage          `json:"message"`
	Locations  []sarifLocation       `json:"locations"`
	Properties sarifResultProperties `json:"properties"`
}

type sarifResultProperties struct {
	CloneType   string  `json:"cloneType"`
	Similarity  float64 `json:"similarity"`
	Scope       string  `json:"scope,omitempty"`
	SavedLines  int     `json:"savedLines,omitempty"`
	Fingerprint string  `json:"fingerprint,omitempty"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           sarifRegion   `json:"region"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine int `json:"startLine"`
	EndLine   int `json:"endLine"`
}

func formatSARIF(report *domain.Report) (string, error) {
	results := make([]sarifResult, 0, len(report.Clones))

	for i, clone := range report.Clones {
		locs := make([]sarifLocation, 0, len(clone.Instances))
		for _, inst := range clone.Instances {
			locs = append(locs, sarifLocation{
				PhysicalLocation: sarifPhysical{
					ArtifactLocation: sarifArtifact{URI: inst.File},
					Region:           sarifRegion{StartLine: inst.StartLine, EndLine: inst.EndLine},
				},
			})
		}
		ruleID := "DUPE001"
		if clone.TestProdSpan {
			ruleID = "DUPE002"
		}
		msg := fmt.Sprintf("Clone #%d (%s): %d duplicate lines across %d locations", i+1, clone.Type, clone.LineCount, len(clone.Instances))
		if funcs := cloneFunctionList(clone); funcs != "" {
			msg += " (" + funcs + ")"
		}
		results = append(results, sarifResult{
			RuleID:    ruleID,
			Message:   sarifMessage{Text: msg},
			Locations: locs,
			Properties: sarifResultProperties{
				CloneType:   clone.Type,
				Similarity:  clone.Similarity,
				Scope:       clone.Scope,
				SavedLines:  clone.SavedLines,
				Fingerprint: clone.Hash,
			},
		})
	}

	sarif := sarifReport{
		Version: "2.1.0",
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Runs: []sarifRun{
			{
				Tool: sarifTool{
					Driver: sarifDriver{
						Name:           "dupehound",
						InformationURI: "https://github.com/AxeForging/dupehound",
						Rules: []sarifRule{
							{
								ID:               "DUPE001",
								ShortDescription: sarifMessage{Text: "Duplicate code block detected"},
							},
							{
								ID:               "DUPE002",
								ShortDescription: sarifMessage{Text: "Duplicate code block spanning test and production files"},
							},
						},
					},
				},
				Results:    results,
				Properties: sarifRunProperties(report),
			},
		},
	}

	data, err := json.MarshalIndent(sarif, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal sarif: %w", err)
	}
	return string(data), nil
}

// sarifRunProperties exposes scan-level context (partial coverage, baseline
// ratchet state) so SARIF consumers can tell a complete scan from a capped one.
func sarifRunProperties(report *domain.Report) map[string]any {
	props := map[string]any{}
	if report.Partial {
		props["partial"] = true
		props["partialReason"] = report.PartialReason
	}
	if report.BaselineFile != "" {
		props["baselineFile"] = report.BaselineFile
		props["baselineKnown"] = report.BaselineKnown
		props["baselineNew"] = report.BaselineNew
	}
	if len(props) == 0 {
		return nil
	}
	return props
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
