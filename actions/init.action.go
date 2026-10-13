package actions

import (
	"fmt"
	"os"

	"github.com/urfave/cli"
)

const starterConfig = `# .dupehound.yml — dupehound configuration
# All fields are optional. CLI flags override these values when both are set.
scan:
  # Path to scan (file or directory). Default: current directory.
  path: .

  # Minimum number of tokens to consider a block a duplicate (~5 lines of code).
  min-tokens: 50

  # Glob patterns to exclude from scanning (can list multiple).
  exclude:
    - "*.gen.go"
    - "*.pb.go"
    - "vendor/**"
    - "testdata/**"

  # Filter to a single language (e.g. go, python, javascript). Empty = all.
  language: ""

  # Output format: text | json | sarif
  format: text

  # Write output to a file instead of stdout. Empty = stdout.
  output: ""

  # Always exit 0 even when clones are found (useful for reporting pipelines).
  exit-zero: false
`

// InitAction handles the init command.
type InitAction struct{}

// NewInitAction creates a new InitAction.
func NewInitAction() *InitAction {
	return &InitAction{}
}

// Execute writes a starter .dupehound.yml to the current directory.
func (a *InitAction) Execute(c *cli.Context) error {
	const filename = ".dupehound.yml"

	if _, err := os.Stat(filename); err == nil {
		if !c.Bool("force") {
			return fmt.Errorf("%s already exists (use --force to overwrite)", filename)
		}
	}

	if err := os.WriteFile(filename, []byte(starterConfig), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", filename, err)
	}

	fmt.Printf("Created %s\n", filename)
	return nil
}
