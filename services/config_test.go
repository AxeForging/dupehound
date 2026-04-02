package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AxeForging/dupehound/domain"
)

func TestLoadConfig_ParsesAllFields(t *testing.T) {
	dir := t.TempDir()
	content := `
scan:
  path: ./src
  min-tokens: 30
  exclude:
    - "*.gen.go"
    - "vendor/**"
  language: go
  format: json
  output: report.json
  exit-zero: true
`
	path := filepath.Join(dir, ".dupehound.yml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if cfg.Scan.Path != "./src" {
		t.Errorf("Path: want ./src, got %q", cfg.Scan.Path)
	}
	if cfg.Scan.MinTokens != 30 {
		t.Errorf("MinTokens: want 30, got %d", cfg.Scan.MinTokens)
	}
	if len(cfg.Scan.Exclude) != 2 {
		t.Errorf("Exclude: want 2 entries, got %d", len(cfg.Scan.Exclude))
	}
	if cfg.Scan.Language != "go" {
		t.Errorf("Language: want go, got %q", cfg.Scan.Language)
	}
	if cfg.Scan.Format != "json" {
		t.Errorf("Format: want json, got %q", cfg.Scan.Format)
	}
	if cfg.Scan.Output != "report.json" {
		t.Errorf("Output: want report.json, got %q", cfg.Scan.Output)
	}
	if !cfg.Scan.ExitZero {
		t.Error("ExitZero: want true, got false")
	}
}

func TestLoadConfig_EmptyPath_ReturnsDefaults(t *testing.T) {
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error for empty path: %v", err)
	}
	if cfg.Scan.MinTokens != 0 {
		t.Errorf("want zero MinTokens, got %d", cfg.Scan.MinTokens)
	}
	if cfg.Scan.Path != "" {
		t.Errorf("want empty Path, got %q", cfg.Scan.Path)
	}
}

func TestLoadConfig_MissingFile_ReturnsError(t *testing.T) {
	_, err := LoadConfig("/nonexistent/.dupehound.yml")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestLoadConfig_InvalidYAML_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dupehound.yml")
	if err := os.WriteFile(path, []byte("scan: [invalid: yaml: {"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestFindConfig_FindsInCurrentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dupehound.yml")
	if err := os.WriteFile(path, []byte("scan:\n  min-tokens: 10\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	found := FindConfig(dir)
	if found != path {
		t.Errorf("want %q, got %q", path, found)
	}
}

func TestFindConfig_WalksUpToParent(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "sub", "deep")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(parent, ".dupehound.yml")
	if err := os.WriteFile(cfgPath, []byte("scan:\n  min-tokens: 10\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	found := FindConfig(child)
	if found != cfgPath {
		t.Errorf("want %q, got %q", cfgPath, found)
	}
}

func TestFindConfig_NoneExists_ReturnsEmpty(t *testing.T) {
	// Use a temp dir with no config file and no parent config.
	dir := t.TempDir()
	found := FindConfig(dir)
	// May find one in a parent if the test environment has one — just ensure no panic.
	_ = found
}

func TestApplyConfigDefaults_CLIWins(t *testing.T) {
	opts := ScanOptions{
		Path:      "./cli-path",
		MinTokens: 100,
		Language:  "go",
	}
	cfg := mustParseConfig(t, "scan:\n  path: ./config-path\n  min-tokens: 20\n  language: python\n")
	ApplyConfigDefaults(&opts, cfg)

	// CLI values must not be overwritten.
	if opts.Path != "./cli-path" {
		t.Errorf("Path: CLI value overwritten, got %q", opts.Path)
	}
	if opts.MinTokens != 100 {
		t.Errorf("MinTokens: CLI value overwritten, got %d", opts.MinTokens)
	}
	if opts.Language != "go" {
		t.Errorf("Language: CLI value overwritten, got %q", opts.Language)
	}
}

func TestApplyConfigDefaults_FillsZeroValues(t *testing.T) {
	opts := ScanOptions{}
	cfg := mustParseConfig(t, "scan:\n  path: ./src\n  min-tokens: 40\n  language: python\n  exclude:\n    - \"*.gen.go\"\n")
	ApplyConfigDefaults(&opts, cfg)

	if opts.Path != "./src" {
		t.Errorf("Path: want ./src, got %q", opts.Path)
	}
	if opts.MinTokens != 40 {
		t.Errorf("MinTokens: want 40, got %d", opts.MinTokens)
	}
	if opts.Language != "python" {
		t.Errorf("Language: want python, got %q", opts.Language)
	}
	if len(opts.Exclude) != 1 {
		t.Errorf("Exclude: want 1, got %d", len(opts.Exclude))
	}
}

// mustParseConfig writes a temp YAML config and loads it.
func mustParseConfig(t *testing.T, content string) domain.Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".dupehound.yml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("mustParseConfig: %v", err)
	}
	return cfg
}
