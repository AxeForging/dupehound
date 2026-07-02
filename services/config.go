package services

import (
	"os"
	"path/filepath"

	"github.com/AxeForging/dupehound/domain"
	"gopkg.in/yaml.v3"
)

const configFileName = ".dupehound.yml"

// FindConfig walks up from startDir looking for a .dupehound.yml file.
// Returns the path to the first one found, or "" if none exists.
func FindConfig(startDir string) string {
	dir := startDir
	for {
		candidate := filepath.Join(dir, configFileName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// LoadConfig reads and parses a .dupehound.yml file.
// Returns a zero-value Config (no error) if path is empty.
func LoadConfig(path string) (domain.Config, error) {
	if path == "" {
		return domain.Config{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.Config{}, err
	}
	var cfg domain.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return domain.Config{}, err
	}
	return cfg, nil
}

// ApplyConfigDefaults fills in ScanOptions fields from cfg where the caller
// has not already set them (zero value = not set by CLI flag).
// CLI flags always win; config is only a fallback.
func ApplyConfigDefaults(opts *ScanOptions, cfg domain.Config) {
	if opts.Path == "" && cfg.Scan.Path != "" {
		opts.Path = cfg.Scan.Path
	}
	if opts.MinTokens == 0 && cfg.Scan.MinTokens > 0 {
		opts.MinTokens = cfg.Scan.MinTokens
	}
	if len(opts.Exclude) == 0 && len(cfg.Scan.Exclude) > 0 {
		opts.Exclude = cfg.Scan.Exclude
	}
	if len(opts.Include) == 0 && len(cfg.Scan.Include) > 0 {
		opts.Include = cfg.Scan.Include
	}
	if opts.Language == "" && cfg.Scan.Language != "" {
		opts.Language = cfg.Scan.Language
	}
	if opts.Top == 0 && cfg.Scan.Top > 0 {
		opts.Top = cfg.Scan.Top
	}
	if opts.Baseline == "" && cfg.Scan.Baseline != "" {
		opts.Baseline = cfg.Scan.Baseline
	}
}
