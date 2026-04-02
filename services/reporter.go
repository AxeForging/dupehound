package services

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

// FormatReport formats a Report as text, json, or sarif.
func FormatReport(report *domain.Report, format string) (string, error) {
	switch strings.ToLower(format) {
	case "text", "":
		return formatText(report), nil
	case "json":
		return formatJSON(report)
	case "sarif":
		return formatSARIF(report)
	default:
		return "", fmt.Errorf("unknown format %q: must be text, json, or sarif", format)
	}
}

func formatText(report *domain.Report) string {
	var b strings.Builder

	fmt.Fprintf(&b, "dupehound scan results\n")
	fmt.Fprintf(&b, "======================\n")
	fmt.Fprintf(&b, "Files scanned : %d / %d\n", report.ScannedFiles, report.TotalFiles)
	fmt.Fprintf(&b, "Clones found  : %d\n", report.TotalClones)
	fmt.Fprintf(&b, "Duplicate lines: %d\n\n", report.DuplicateLines)

	if report.TotalClones == 0 {
		fmt.Fprintf(&b, "No duplicates found.\n")
		return b.String()
	}

	for i, clone := range report.Clones {
		fmt.Fprintf(&b, "Clone #%d  %s  similarity: %.2f  %d lines · %d tokens · %d instances\n",
			i+1, clone.Type, clone.Similarity, clone.LineCount, clone.TokenCount, len(clone.Instances))
		for _, inst := range clone.Instances {
			fmt.Fprintf(&b, "  %s:%d-%d\n", inst.File, inst.StartLine, inst.EndLine)
		}
		if len(clone.Instances) > 0 {
			fmt.Fprintf(&b, "  Preview:\n")
			for _, line := range clone.Instances[0].Lines {
				fmt.Fprintf(&b, "    %s\n", strings.TrimRight(line, " \t"))
			}
		}
		fmt.Fprintf(&b, "\n")
	}

	return b.String()
}

func formatJSON(report *domain.Report) (string, error) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal report: %w", err)
	}
	return string(data), nil
}

// sarifReport is a minimal SARIF 2.1.0 structure.
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
