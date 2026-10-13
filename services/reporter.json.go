package services

import (
	"encoding/json"
	"fmt"

	"github.com/AxeForging/dupehound/domain"
)

// JSON report.

// --- JSON format ---

func formatJSON(report *domain.Report) (string, error) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal report: %w", err)
	}
	return string(data), nil
}
