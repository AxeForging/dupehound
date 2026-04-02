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
)

// FormatOptions controls text output presentation.
type FormatOptions struct {
	ScanPath string // base path for relative file paths
	Verbose  bool   // show all clones with previews
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

type fileStats struct {
	path       string
	dupLines   int
	cloneCount int
}

func buildHotspots(clones []domain.Clone, scanPath string) []fileStats {
	fileMap := map[string]*fileStats{}
	for _, c := range clones {
		for _, inst := range c.Instances {
			rel := relPath(inst.File, scanPath)
			fs, ok := fileMap[rel]
			if !ok {
				fs = &fileStats{path: rel}
				fileMap[rel] = fs
			}
			fs.dupLines += inst.EndLine - inst.StartLine + 1
			fs.cloneCount++
		}
	}
	hotspots := make([]fileStats, 0, len(fileMap))
	for _, fs := range fileMap {
		hotspots = append(hotspots, *fs)
	}
	sort.Slice(hotspots, func(i, j int) bool {
		return hotspots[i].dupLines > hotspots[j].dupLines
	})
	return hotspots
}

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
	fmt.Fprintf(&b, "Files scanned : %d / %d\n", report.ScannedFiles, report.TotalFiles)
	fmt.Fprintf(&b, "Clones found  : %d\n", report.TotalClones)
	fmt.Fprintf(&b, "Duplicate lines: %d\n", report.DuplicateLines)

	if report.TotalClones == 0 {
		fmt.Fprintf(&b, "\nNo duplicates found.\n")
		return b.String()
	}

	fmt.Fprintf(&b, "Breakdown     : %s\n", typeBreakdown(report.Clones))

	hotspots := buildHotspots(report.Clones, opts.ScanPath)
	limit := defaultTopHotspots
	if opts.Verbose || len(hotspots) <= limit {
		limit = len(hotspots)
	}
	fmt.Fprintf(&b, "\nHotspots (files with most duplication):\n")
	for i := 0; i < limit; i++ {
		hs := hotspots[i]
		fmt.Fprintf(&b, "  %5d lines  %-60s (%d clones)\n", hs.dupLines, hs.path, hs.cloneCount)
	}
	if len(hotspots) > limit {
		fmt.Fprintf(&b, "  ... %d more files (use --verbose to show all)\n", len(hotspots)-limit)
	}

	sorted := sortClonesByImpact(report.Clones)

	cloneLimit := defaultTopClones
	if opts.Verbose || len(sorted) <= cloneLimit {
		cloneLimit = len(sorted)
	}

	fmt.Fprintf(&b, "\nTop clones (by impact):\n")
	for i := 0; i < cloneLimit; i++ {
		writeCloneText(&b, i+1, sorted[i], opts, true)
	}
	if len(sorted) > cloneLimit {
		fmt.Fprintf(&b, "  ... %d more clones (use --verbose to show all)\n\n", len(sorted)-cloneLimit)
	}

	fmt.Fprintf(&b, "All clones:\n")
	for i, c := range sorted {
		writeCloneText(&b, i+1, c, opts, opts.Verbose)
	}

	return b.String()
}

func writeCloneText(b *strings.Builder, num int, c domain.Clone, opts FormatOptions, showPreview bool) {
	fmt.Fprintf(b, "  #%-4d %s  similarity: %.2f  %d lines  %d tokens  %d instances\n",
		num, c.Type, c.Similarity, c.LineCount, c.TokenCount, len(c.Instances))
	for _, inst := range c.Instances {
		fmt.Fprintf(b, "        %s:%d-%d\n", relPath(inst.File, opts.ScanPath), inst.StartLine, inst.EndLine)
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
	fmt.Fprintf(&b, "| Metric | Value |\n")
	fmt.Fprintf(&b, "|--------|-------|\n")
	fmt.Fprintf(&b, "| Files scanned | %d / %d |\n", report.ScannedFiles, report.TotalFiles)
	fmt.Fprintf(&b, "| Clones found | %d |\n", report.TotalClones)
	fmt.Fprintf(&b, "| Duplicate lines | %d |\n", report.DuplicateLines)

	if report.TotalClones == 0 {
		fmt.Fprintf(&b, "\nNo duplicates found.\n")
		return b.String()
	}

	fmt.Fprintf(&b, "| Breakdown | %s |\n", typeBreakdown(report.Clones))

	// --- Hotspots ---
	hotspots := buildHotspots(report.Clones, opts.ScanPath)
	limit := defaultTopHotspots
	if opts.Verbose || len(hotspots) <= limit {
		limit = len(hotspots)
	}

	fmt.Fprintf(&b, "\n### Hotspots\n\n")
	fmt.Fprintf(&b, "| Dup Lines | File | Clones |\n")
	fmt.Fprintf(&b, "|----------:|------|-------:|\n")
	for i := 0; i < limit; i++ {
		hs := hotspots[i]
		fmt.Fprintf(&b, "| %d | `%s` | %d |\n", hs.dupLines, hs.path, hs.cloneCount)
	}

	if len(hotspots) > limit {
		fmt.Fprintf(&b, "\n<details>\n<summary>%d more files...</summary>\n\n", len(hotspots)-limit)
		fmt.Fprintf(&b, "| Dup Lines | File | Clones |\n")
		fmt.Fprintf(&b, "|----------:|------|-------:|\n")
		for i := limit; i < len(hotspots); i++ {
			hs := hotspots[i]
			fmt.Fprintf(&b, "| %d | `%s` | %d |\n", hs.dupLines, hs.path, hs.cloneCount)
		}
		fmt.Fprintf(&b, "\n</details>\n")
	}

	// --- Top clones ---
	sorted := sortClonesByImpact(report.Clones)

	cloneLimit := defaultTopClones
	if opts.Verbose || len(sorted) <= cloneLimit {
		cloneLimit = len(sorted)
	}

	fmt.Fprintf(&b, "\n### Top clones (by impact)\n\n")
	for i := 0; i < cloneLimit; i++ {
		writeCloneMd(&b, i+1, sorted[i], opts)
	}

	if len(sorted) > cloneLimit {
		fmt.Fprintf(&b, "\n> %d more clones not shown. Run `dupehound scan --verbose` for full results.\n", len(sorted)-cloneLimit)
	}

	return b.String()
}

func writeCloneMd(b *strings.Builder, num int, c domain.Clone, opts FormatOptions) {
	// Detect language from first instance file extension for syntax highlighting.
	lang := ""
	if len(c.Instances) > 0 {
		lang = mdLangHint(c.Instances[0].File)
	}

	summary := fmt.Sprintf("#%d &nbsp; <code>%s</code> &nbsp; similarity: %.2f &nbsp; %d lines &nbsp; %d instances",
		num, c.Type, c.Similarity, c.LineCount, len(c.Instances))

	fmt.Fprintf(b, "<details>\n<summary>%s</summary>\n\n", summary)

	fmt.Fprintf(b, "**Locations:**\n")
	for _, inst := range c.Instances {
		fmt.Fprintf(b, "- `%s:%d-%d`\n", relPath(inst.File, opts.ScanPath), inst.StartLine, inst.EndLine)
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
		results = append(results, sarifResult{
			RuleID:    "DUPE001",
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
