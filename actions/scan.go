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

	// Load config file: explicit --config flag, then auto-discover from cwd.
	configPath := c.String("config")
	if configPath == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		configPath = services.FindConfig(wd)
	}
	cfg, err := services.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Build opts only from flags that were explicitly set by the caller;
	// leave others at zero so ApplyConfigDefaults can fill them from the file.
	var opts services.ScanOptions
	if c.IsSet("path") {
		opts.Path = c.String("path")
	}
	if c.IsSet("min-tokens") {
		opts.MinTokens = c.Int("min-tokens")
	} else if c.IsSet("min-lines") {
		opts.MinTokens = c.Int("min-lines") * 10
	}
	if c.IsSet("exclude") {
		opts.Exclude = c.StringSlice("exclude")
	}
	if c.IsSet("language") {
		opts.Language = c.String("language")
	}
	if c.IsSet("similarity") {
		opts.MinSimilarity = c.Float64("similarity")
	}

	services.ApplyConfigDefaults(&opts, cfg)

	// Final fallback: nothing set in CLI or config → use built-in defaults.
	if opts.Path == "" {
		opts.Path = "."
	}
	if opts.MinTokens <= 0 {
		return helpers.ErrInvalidMinTokens
	}

	// Format and output: CLI wins, then config, then built-in default ("text").
	format := c.String("format")
	if !c.IsSet("format") && cfg.Scan.Format != "" {
		format = cfg.Scan.Format
	}
	validFormats := map[string]bool{"text": true, "json": true, "sarif": true}
	if !validFormats[format] {
		return helpers.ErrInvalidFormat
	}

	outFile := c.String("output")
	if !c.IsSet("output") && cfg.Scan.Output != "" {
		outFile = cfg.Scan.Output
	}

	exitZero := c.Bool("exit-zero") || cfg.Scan.ExitZero

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

	if outFile == "" {
		fmt.Print(output)
	} else {
		if err := os.WriteFile(outFile, []byte(output), 0o644); err != nil {
			return fmt.Errorf("write output file: %w", err)
		}
		helpers.Log.Info().Str("file", outFile).Msg("results written")
	}

	if report.TotalClones > 0 && !exitZero {
		return helpers.ErrClonesFound
	}
	return nil
}
