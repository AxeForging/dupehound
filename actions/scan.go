package actions

import (
	"fmt"
	"os"
	"path/filepath"

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
	if c.IsSet("max-bucket") {
		opts.MaxBucket = c.Int("max-bucket")
	}
	opts.Staged = c.Bool("staged")
	if c.IsSet("include") {
		opts.Include = c.StringSlice("include")
	}
	if c.IsSet("top") {
		topVal := c.Int("top")
		if topVal == 0 {
			opts.Top = -1 // 0 means all in CLI, -1 means all in opts
		} else {
			opts.Top = topVal
		}
	}
	opts.Since = c.String("since")
	opts.ShowSuppressed = c.Bool("show-suppressed")
	opts.DeadCode = c.Bool("dead-code")
	opts.GitChurn = c.Bool("git-churn")
	if c.IsSet("churn-days") {
		opts.ChurnDays = c.Int("churn-days")
	}

	services.ApplyConfigDefaults(&opts, cfg)

	// Final fallback: nothing set in CLI or config → use built-in defaults.
	if opts.Path == "" {
		opts.Path = "."
	}
	if opts.MinTokens <= 0 {
		opts.MinTokens = 50
	}
	if opts.MinSimilarity == 0 {
		opts.MinSimilarity = 0.70
	}
	if opts.MaxBucket == 0 {
		opts.MaxBucket = 5000
	}

	// Format and output: CLI wins, then config, then built-in default ("text").
	format := c.String("format")
	if !c.IsSet("format") && cfg.Scan.Format != "" {
		format = cfg.Scan.Format
	}
	validFormats := map[string]bool{"text": true, "json": true, "sarif": true, "md": true}
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
		Int("total_lines", report.TotalLines).
		Float64("duplication_pct", report.DuplicationPct).
		Msg("scan complete")

	absPath, err := filepath.Abs(opts.Path)
	if err != nil {
		absPath = opts.Path
	}
	topVal := c.Int("top")
	if c.IsSet("top") && topVal == 0 {
		topVal = -1 // --top 0 means show all
	}
	output, err := services.FormatReport(report, format, services.FormatOptions{
		ScanPath:       absPath,
		Verbose:        c.Bool("verbose"),
		Top:            topVal,
		ShowSuppressed: c.Bool("show-suppressed"),
	})
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

	minDuplication := c.Float64("min-duplication")
	if minDuplication > 0 {
		// When --since is active, apply threshold only to new clones.
		dupPct := report.DuplicationPct
		if opts.Since != "" && len(report.NewClones) == 0 {
			dupPct = 0
		}
		if dupPct > minDuplication {
			return helpers.ErrThresholdExceeded
		}
	}

	// When --since is active, exit 1 only if new clones found.
	if opts.Since != "" {
		if len(report.NewClones) > 0 && !exitZero {
			return helpers.ErrClonesFound
		}
	} else if report.TotalClones > 0 && !exitZero {
		return helpers.ErrClonesFound
	}

	// Exit 1 if dead functions found.
	if len(report.DeadFunctions) > 0 && !exitZero {
		return helpers.ErrClonesFound
	}

	return nil
}
