package actions

import (
	"fmt"
	"os"

	"github.com/AxeForging/dupehound/helpers"
	"github.com/AxeForging/dupehound/services"
	"github.com/urfave/cli"
)

// ScanAction handles the scan command.
type ScanAction struct {
	svc *services.ScannerService
}

// NewScanAction creates a new ScanAction with the given ScannerService.
func NewScanAction(svc *services.ScannerService) *ScanAction {
	return &ScanAction{svc: svc}
}

// Execute runs the scan command.
func (a *ScanAction) Execute(c *cli.Context) error {
	if c.Bool("verbose") {
		helpers.SetupLogger("debug")
	}

	minTokens := c.Int("min-tokens")
	minLines := c.Int("min-lines")

	// Resolve: min-tokens takes precedence; min-lines is the legacy fallback.
	if minTokens <= 0 && minLines > 0 {
		minTokens = minLines * 10
	}
	if minTokens <= 0 {
		return helpers.ErrInvalidMinTokens
	}

	format := c.String("format")
	validFormats := map[string]bool{"text": true, "json": true, "sarif": true}
	if !validFormats[format] {
		return helpers.ErrInvalidFormat
	}

	opts := services.ScanOptions{
		Path:      c.String("path"),
		MinTokens: minTokens,
		Exclude:   c.StringSlice("exclude"),
		Language:  c.String("language"),
	}

	helpers.Log.Info().
		Str("path", opts.Path).
		Int("min-tokens", opts.MinTokens).
		Str("format", format).
		Msg("starting scan")

	report, err := a.svc.Scan(opts)
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	helpers.Log.Info().
		Int("clones", report.TotalClones).
		Int("duplicate_lines", report.DuplicateLines).
		Msg("scan complete")

	output, err := services.FormatReport(report, format)
	if err != nil {
		return fmt.Errorf("format report: %w", err)
	}

	outFile := c.String("output")
	if outFile == "" {
		fmt.Print(output)
	} else {
		if err := os.WriteFile(outFile, []byte(output), 0o644); err != nil {
			return fmt.Errorf("write output file: %w", err)
		}
		helpers.Log.Info().Str("file", outFile).Msg("results written")
	}

	if report.TotalClones > 0 && !c.Bool("exit-zero") {
		return helpers.ErrClonesFound
	}
	return nil
}
