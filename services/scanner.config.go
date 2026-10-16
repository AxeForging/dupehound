package services

import (
	"fmt"
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
	// Rules are validated on the YAML tree first, so their errors name the
	// rule and line instead of a Go type.
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return domain.Config{}, err
	}
	if err := validateRulesNode(&doc); err != nil {
		return domain.Config{}, fmt.Errorf("%s: %w", path, err)
	}
	var cfg domain.Config
	if err := doc.Decode(&cfg); err != nil {
		return domain.Config{}, err
	}
	return cfg, nil
}

// ApplyConfigDefaults fills in ScanOptions fields from cfg where the caller
// has not already set them (zero value = not set by CLI flag).
// CLI flags always win; config is only a fallback.
func ApplyConfigDefaults(opts *ScanOptions, cfg domain.Config) {
	fallback(&opts.Path, cfg.Scan.Path)
	fallback(&opts.Language, cfg.Scan.Language)
	fallback(&opts.Baseline, cfg.Scan.Baseline)
	if cfg.Scan.MinTokens > 0 {
		fallback(&opts.MinTokens, cfg.Scan.MinTokens)
	}
	if cfg.Scan.Top > 0 {
		fallback(&opts.Top, cfg.Scan.Top)
	}
	if len(opts.Exclude) == 0 {
		opts.Exclude = append(opts.Exclude, cfg.Scan.Exclude...)
	}
	if len(opts.Include) == 0 {
		opts.Include = append(opts.Include, cfg.Scan.Include...)
	}
}

// fallback sets *dst to v when the caller left it at its zero value.
func fallback[T comparable](dst *T, v T) {
	var zero T
	if *dst == zero {
		*dst = v
	}
}
