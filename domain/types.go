package domain

// CloneInstance is a single occurrence of a duplicate code block.
type CloneInstance struct {
	File        string   `json:"file"`
	StartLine   int      `json:"start_line"`
	EndLine     int      `json:"end_line"`
	Function    string   `json:"function,omitempty"` // best-effort enclosing function/method name
	Lines       []string `json:"lines"`
	IsTest      bool     `json:"is_test,omitempty"`
	FileCommits int      `json:"file_commits_in_window,omitempty"`
}

// Clone type constants.
const (
	CloneType1 = "type-1" // token-for-token identical (same kinds AND same text)
	CloneType2 = "type-2" // identical token structure, differing only in identifier/literal values
	CloneType3 = "type-3" // near-miss: structurally similar with small differences (future)
)

// Clone scope constants: how far apart the instances of a clone live.
// Same-file clones are the easiest to refactor; cross-dir clones usually
// point at a missing shared package/module.
const (
	ScopeSameFile  = "same-file"
	ScopeCrossFile = "cross-file"
	ScopeCrossDir  = "cross-dir"
)

// Clone represents a group of identical code blocks found in multiple locations.
//
// Hash is a stable, content-based fingerprint of the clone: it is derived from
// the normalized token structure of the duplicated block(s), NOT from file
// positions, so it survives line shifts, file renames, and unrelated edits.
// This is what makes hash-based suppression rules and the baseline ratchet
// (--baseline / --write-baseline) reliable.
type Clone struct {
	Hash         string          `json:"hash"`
	Type         string          `json:"type"`
	Similarity   float64         `json:"similarity"`
	LineCount    int             `json:"line_count"`
	TokenCount   int             `json:"token_count"`
	SavedLines   int             `json:"saved_lines,omitempty"` // lines removable by deduplicating: (instances-1) × line_count
	Scope        string          `json:"scope,omitempty"`       // same-file | cross-file | cross-dir
	Instances    []CloneInstance `json:"instances"`
	TestProdSpan bool            `json:"test_prod_span,omitempty"`
	Suppressed   bool            `json:"suppressed,omitempty"`
	Baseline     bool            `json:"baseline,omitempty"` // true when covered by the --baseline file (known debt)
	ChurnScore   int             `json:"churn_score,omitempty"`
}

// FileStats holds per-file duplication metrics.
type FileStats struct {
	File           string  `json:"file"`
	TotalLines     int     `json:"total_lines"`
	DuplicateLines int     `json:"duplicate_lines"`
	DuplicationPct float64 `json:"duplication_pct"`
}

// Report is the full result of a dupehound scan.
type Report struct {
	TotalFiles        int     `json:"total_files"`
	ScannedFiles      int     `json:"scanned_files"`
	SkippedFiles      int     `json:"skipped_files,omitempty"`       // binary files skipped
	SkippedLargeFiles int     `json:"skipped_large_files,omitempty"` // files over --max-file-size
	TotalClones       int     `json:"total_clones"`
	TotalLines        int     `json:"total_lines"`
	DuplicateLines    int     `json:"duplicate_lines"`
	DuplicationPct    float64 `json:"duplication_pct"`
	SavedLines        int     `json:"saved_lines,omitempty"` // total lines removable by deduplicating all reported clones
	SuppressedClones  int     `json:"suppressed_clones,omitempty"`
	// Partial is true when detection was capped (--max-pairs hit or a fuzzy
	// bucket was truncated by --max-bucket): type-1/2 results are complete but
	// some type-3 clones may be missing. PartialReason says which guard fired.
	Partial       bool   `json:"partial,omitempty"`
	PartialReason string `json:"partial_reason,omitempty"`
	// Baseline ratchet (--baseline): known clones are recorded debt, new ones fail the scan.
	BaselineFile   string      `json:"baseline_file,omitempty"`
	BaselineKnown  int         `json:"baseline_known,omitempty"`
	BaselineNew    int         `json:"baseline_new,omitempty"`
	FileStats      []FileStats `json:"file_stats,omitempty"`
	Clones         []Clone     `json:"clones"`
	DeadFunctions  []DeadFunc  `json:"dead_functions,omitempty"`
	NewClones      []Clone     `json:"new_clones,omitempty"`
	SinceDiffRef   string      `json:"since_diff_ref,omitempty"`
	SinceDiffFiles int         `json:"since_diff_files,omitempty"`
}

// DeadFunc represents a function that appears to have no callers.
type DeadFunc struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Name     string `json:"name"`
	Language string `json:"language"`
}

// Config is the structure of a .dupehound.yml config file.
type Config struct {
	Scan ScanConfig `yaml:"scan"`
}

// ScanConfig holds all scan-related settings from the config file.
// Fields map 1:1 to CLI flags; zero values mean "not set" (CLI flag wins).
type ScanConfig struct {
	Path      string   `yaml:"path"`
	MinTokens int      `yaml:"min-tokens"`
	Exclude   []string `yaml:"exclude"`
	Include   []string `yaml:"include"`
	Language  string   `yaml:"language"`
	Format    string   `yaml:"format"`
	Output    string   `yaml:"output"`
	ExitZero  bool     `yaml:"exit-zero"`
	Top       int      `yaml:"top"`
	Baseline  string   `yaml:"baseline"` // path to a baseline file written with --write-baseline
}

// Language defines a programming language and how to strip its comments.
type Language struct {
	Name        string
	Extensions  []string
	LineComment string
	BlockStart  string
	BlockEnd    string
}

// SupportedLanguages is the list of languages dupehound can scan.
var SupportedLanguages = []Language{
	{
		Name:        "go",
		Extensions:  []string{".go"},
		LineComment: "//",
		BlockStart:  "/*",
		BlockEnd:    "*/",
	},
	{
		Name:        "python",
		Extensions:  []string{".py"},
		LineComment: "#",
		BlockStart:  "",
		BlockEnd:    "",
	},
	{
		Name:        "javascript",
		Extensions:  []string{".js", ".mjs", ".cjs", ".jsx"},
		LineComment: "//",
		BlockStart:  "/*",
		BlockEnd:    "*/",
	},
	{
		Name:        "typescript",
		Extensions:  []string{".ts", ".tsx", ".mts", ".cts"},
		LineComment: "//",
		BlockStart:  "/*",
		BlockEnd:    "*/",
	},
	{
		Name:        "java",
		Extensions:  []string{".java"},
		LineComment: "//",
		BlockStart:  "/*",
		BlockEnd:    "*/",
	},
	{
		Name:        "kotlin",
		Extensions:  []string{".kt", ".kts"},
		LineComment: "//",
		BlockStart:  "/*",
		BlockEnd:    "*/",
	},
	{
		Name:        "rust",
		Extensions:  []string{".rs"},
		LineComment: "//",
		BlockStart:  "/*",
		BlockEnd:    "*/",
	},
	{
		Name:        "c",
		Extensions:  []string{".c", ".h"},
		LineComment: "//",
		BlockStart:  "/*",
		BlockEnd:    "*/",
	},
	{
		Name:        "cpp",
		Extensions:  []string{".cpp", ".cc", ".cxx", ".hpp", ".hxx"},
		LineComment: "//",
		BlockStart:  "/*",
		BlockEnd:    "*/",
	},
	{
		Name:        "csharp",
		Extensions:  []string{".cs"},
		LineComment: "//",
		BlockStart:  "/*",
		BlockEnd:    "*/",
	},
	{
		Name:        "swift",
		Extensions:  []string{".swift"},
		LineComment: "//",
		BlockStart:  "/*",
		BlockEnd:    "*/",
	},
	{
		Name:        "scala",
		Extensions:  []string{".scala"},
		LineComment: "//",
		BlockStart:  "/*",
		BlockEnd:    "*/",
	},
	{
		Name:        "php",
		Extensions:  []string{".php"},
		LineComment: "//",
		BlockStart:  "/*",
		BlockEnd:    "*/",
	},
	{
		Name:        "ruby",
		Extensions:  []string{".rb"},
		LineComment: "#",
		BlockStart:  "",
		BlockEnd:    "",
	},
	{
		Name:        "shell",
		Extensions:  []string{".sh", ".bash", ".zsh"},
		LineComment: "#",
		BlockStart:  "",
		BlockEnd:    "",
	},
	{
		Name:        "sql",
		Extensions:  []string{".sql"},
		LineComment: "--",
		BlockStart:  "/*",
		BlockEnd:    "*/",
	},
	{
		Name:        "lua",
		Extensions:  []string{".lua"},
		LineComment: "--",
		BlockStart:  "--[[",
		BlockEnd:    "]]",
	},
	{
		Name:        "elixir",
		Extensions:  []string{".ex", ".exs"},
		LineComment: "#",
		BlockStart:  "",
		BlockEnd:    "",
	},
	{
		Name:        "dart",
		Extensions:  []string{".dart"},
		LineComment: "//",
		BlockStart:  "/*",
		BlockEnd:    "*/",
	},
	{
		Name:        "r",
		Extensions:  []string{".r", ".R"},
		LineComment: "#",
		BlockStart:  "",
		BlockEnd:    "",
	},
}
