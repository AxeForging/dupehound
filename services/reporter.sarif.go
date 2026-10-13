package services

import (
	"encoding/json"
	"fmt"

	"github.com/AxeForging/dupehound/domain"
)

// SARIF report for code-scanning tools.

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
