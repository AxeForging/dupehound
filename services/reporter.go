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

// FormatReport formats a Report as text, json, sarif, or md.
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
	default:
		return "", fmt.Errorf("unknown format %q: must be text, json, sarif, or md", format)
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
		return sorted[i].TokenCount > sorted[j].TokenCount
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
	if report.SinceDiffRef != "" {
		fmt.Fprintf(&b, "Scanning diff  : since %s  (%d files changed)\n", report.SinceDiffRef, report.SinceDiffFiles)
	}
	fmt.Fprintf(&b, "Files scanned : %d / %d\n", report.ScannedFiles, report.TotalFiles)
	if report.SkippedFiles > 0 {
		fmt.Fprintf(&b, "Skipped files : %d (binary)\n", report.SkippedFiles)
	}
	fmt.Fprintf(&b, "Total lines   : %d\n", report.TotalLines)
	if report.SuppressedClones > 0 {
		fmt.Fprintf(&b, "Clones found  : %d (%d suppressed)\n", report.TotalClones, report.SuppressedClones)
	} else {
		fmt.Fprintf(&b, "Clones found  : %d\n", report.TotalClones)
	}
	fmt.Fprintf(&b, "Duplicate lines: %d (%.1f%%)\n", report.DuplicateLines, report.DuplicationPct)

	if report.SinceDiffRef != "" {
		fmt.Fprintf(&b, "New clones    : %d since %s\n", len(report.NewClones), report.SinceDiffRef)
	}

	if report.TotalClones == 0 && len(report.DeadFunctions) == 0 {
		fmt.Fprintf(&b, "\nNo duplicates found.\n")
		return b.String()
	}

	if report.TotalClones > 0 {
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

	if report.TotalClones > 0 {
		sorted := sortClonesByImpact(report.Clones)

		// Partition: test↔prod clones go in their own section, the rest are
		// shown under "Top clones". This avoids double-listing.
		var testProdClones, normalClones []domain.Clone
		for _, c := range sorted {
			if c.Suppressed && !opts.ShowSuppressed {
				continue
			}
			if c.TestProdSpan {
				testProdClones = append(testProdClones, c)
			} else {
				normalClones = append(normalClones, c)
			}
		}

		if len(testProdClones) > 0 {
			tpLimit := topNWithOpt(len(testProdClones), opts)
			fmt.Fprintf(&b, "\nTest↔Prod clones (span test and production files):\n")
			for i := 0; i < tpLimit; i++ {
				writeCloneTextWithTag(&b, i+1, testProdClones[i], opts, true, cloneBadges(testProdClones[i]))
			}
			if len(testProdClones) > tpLimit {
				fmt.Fprintf(&b, "  ... %d more test↔prod clones (use --verbose or --top 0 to show all)\n\n", len(testProdClones)-tpLimit)
			}
		}

		cloneLimit := topNWithOpt(len(normalClones), opts)
		fmt.Fprintf(&b, "\nTop clones (by impact):\n")
		for i := 0; i < cloneLimit; i++ {
			writeCloneTextWithTag(&b, i+1, normalClones[i], opts, true, cloneBadges(normalClones[i]))
		}
		if len(normalClones) > cloneLimit {
			fmt.Fprintf(&b, "  ... %d more clones (use --verbose or --top 0 to show all)\n\n", len(normalClones)-cloneLimit)
		}
	}

	// Dead function report.
	if len(report.DeadFunctions) > 0 {
		fmt.Fprintf(&b, "\nDead functions : %d\n", len(report.DeadFunctions))
		fmt.Fprintf(&b, "Note: dead function detection is a heuristic. Cross-package calls, reflection, and interface implementations may produce false positives.\n")
		dfLimit := topNWithOpt(len(report.DeadFunctions), opts)
		for i := 0; i < dfLimit; i++ {
			df := report.DeadFunctions[i]
			fmt.Fprintf(&b, "  %s:%d\t%s\n", relPath(df.File, opts.ScanPath), df.Line, df.Name)
		}
		if len(report.DeadFunctions) > dfLimit {
			fmt.Fprintf(&b, "  ... %d more (use --verbose or --top 0 to show all)\n", len(report.DeadFunctions)-dfLimit)
		}
	}

	return b.String()
}

// cloneBadges builds the inline badge string for a clone in text output —
// includes suppressed and churn signals (test↔prod is rendered separately
// inside writeCloneTextWithTag).
func cloneBadges(c domain.Clone) string {
	tag := ""
	if c.Suppressed {
		tag += " [suppressed]"
	}
	if c.ChurnScore > 0 {
		tag += fmt.Sprintf(" [churn: %d commits]", c.ChurnScore)
	}
	return tag
}

func writeCloneTextWithTag(b *strings.Builder, num int, c domain.Clone, opts FormatOptions, showPreview bool, tag string) {
	testProdLabel := ""
	if c.TestProdSpan {
		testProdLabel = " [test↔prod]"
	}
	fmt.Fprintf(b, "  #%-4d %s  similarity: %.2f  %d lines  %d tokens  %d instances%s%s\n",
		num, c.Type, c.Similarity, c.LineCount, c.TokenCount, len(c.Instances), testProdLabel, tag)
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
		fmt.Fprintf(b, "        %s:%d-%d%s%s\n", relPath(inst.File, opts.ScanPath), inst.StartLine, inst.EndLine, testLabel, churnLabel)
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
	if report.SinceDiffRef != "" {
		fmt.Fprintf(&b, "> Scanning diff since: **%s** (%d files changed)\n\n", report.SinceDiffRef, report.SinceDiffFiles)
	}
	fmt.Fprintf(&b, "| Metric | Value |\n")
	fmt.Fprintf(&b, "|--------|-------|\n")
	fmt.Fprintf(&b, "| Files scanned | %d / %d |\n", report.ScannedFiles, report.TotalFiles)
	if report.SkippedFiles > 0 {
		fmt.Fprintf(&b, "| Skipped (binary) | %d |\n", report.SkippedFiles)
	}
	fmt.Fprintf(&b, "| Total lines | %d |\n", report.TotalLines)
	if report.SuppressedClones > 0 {
		fmt.Fprintf(&b, "| Clones found | %d (%d suppressed) |\n", report.TotalClones, report.SuppressedClones)
	} else {
		fmt.Fprintf(&b, "| Clones found | %d |\n", report.TotalClones)
	}
	fmt.Fprintf(&b, "| Duplicate lines | %d (%.1f%%) |\n", report.DuplicateLines, report.DuplicationPct)
	if report.SinceDiffRef != "" {
		fmt.Fprintf(&b, "| New clones since %s | %d |\n", report.SinceDiffRef, len(report.NewClones))
	}

	if report.TotalClones == 0 && len(report.DeadFunctions) == 0 {
		fmt.Fprintf(&b, "\nNo duplicates found.\n")
		return b.String()
	}

	if report.TotalClones > 0 {
		fmt.Fprintf(&b, "| Breakdown | %s |\n", typeBreakdown(report.Clones))
	}

	// --- Hotspots / clones (skip when there are no clones at all) ---
	if report.TotalClones > 0 && len(report.FileStats) > 0 {
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
	if report.TotalClones > 0 {
		sortedAll := sortClonesByImpact(report.Clones)
		var testProdClones, normalClones []domain.Clone
		for _, c := range sortedAll {
			if c.Suppressed && !opts.ShowSuppressed {
				continue
			}
			if c.TestProdSpan {
				testProdClones = append(testProdClones, c)
			} else {
				normalClones = append(normalClones, c)
			}
		}

		if len(testProdClones) > 0 {
			tpLimit := topNWithOpt(len(testProdClones), opts)
			fmt.Fprintf(&b, "\n### Test↔Prod clones\n\n")
			for i := 0; i < tpLimit; i++ {
				writeCloneMd(&b, i+1, testProdClones[i], opts)
			}
			if len(testProdClones) > tpLimit {
				fmt.Fprintf(&b, "\n> %d more test↔prod clones not shown.\n", len(testProdClones)-tpLimit)
			}
		}

		// Top clones (excluding test↔prod, which were already listed above).
		cloneLimit := topNWithOpt(len(normalClones), opts)
		fmt.Fprintf(&b, "\n### Top clones (by impact)\n\n")
		for i := 0; i < cloneLimit; i++ {
			writeCloneMd(&b, i+1, normalClones[i], opts)
		}
		if len(normalClones) > cloneLimit {
			fmt.Fprintf(&b, "\n> %d more clones not shown. Run `dupehound scan --verbose` or `--top 0` for full results.\n", len(normalClones)-cloneLimit)
		}
	}

	// --- Dead functions (capped to top-N, overflow in <details>) ---
	if len(report.DeadFunctions) > 0 {
		fmt.Fprintf(&b, "\n### Dead functions (%d)\n\n", len(report.DeadFunctions))
		fmt.Fprintf(&b, "> **Note:** dead function detection is a heuristic. Cross-package calls, reflection, and interface implementations may produce false positives.\n\n")
		dfLimit := topNWithOpt(len(report.DeadFunctions), opts)
		for i := 0; i < dfLimit; i++ {
			df := report.DeadFunctions[i]
			fmt.Fprintf(&b, "- `%s:%d` — `%s`\n", relPath(df.File, opts.ScanPath), df.Line, df.Name)
		}
		if len(report.DeadFunctions) > dfLimit {
			fmt.Fprintf(&b, "\n<details>\n<summary>%d more dead functions...</summary>\n\n", len(report.DeadFunctions)-dfLimit)
			for i := dfLimit; i < len(report.DeadFunctions); i++ {
				df := report.DeadFunctions[i]
				fmt.Fprintf(&b, "- `%s:%d` — `%s`\n", relPath(df.File, opts.ScanPath), df.Line, df.Name)
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

	// Build inline badges so PR readers see test↔prod / churn / suppressed at a glance.
	badges := ""
	if c.TestProdSpan {
		badges += " &nbsp; 🧪 test↔prod"
	}
	if c.ChurnScore > 0 {
		badges += fmt.Sprintf(" &nbsp; 🔥 churn %d", c.ChurnScore)
	}
	if c.Suppressed {
		badges += " &nbsp; 🚫 suppressed"
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
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
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
	CloneType  string  `json:"cloneType"`
	Similarity float64 `json:"similarity"`
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
		results = append(results, sarifResult{
			RuleID:    ruleID,
			Message:   sarifMessage{Text: fmt.Sprintf("Clone #%d (%s): %d duplicate lines across %d locations", i+1, clone.Type, clone.LineCount, len(clone.Instances))},
			Locations: locs,
			Properties: sarifResultProperties{
				CloneType:  clone.Type,
				Similarity: clone.Similarity,
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
				Results: results,
			},
		},
	}

	data, err := json.MarshalIndent(sarif, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal sarif: %w", err)
	}
	return string(data), nil
}
