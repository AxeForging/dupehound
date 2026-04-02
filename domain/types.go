package domain

// CloneInstance is a single occurrence of a duplicate code block.
type CloneInstance struct {
	File      string   `json:"file"`
	StartLine int      `json:"start_line"`
	EndLine   int      `json:"end_line"`
	Lines     []string `json:"lines"`
}

// Clone type constants.
const (
	CloneType1 = "type-1" // token-for-token identical (same kinds AND same text)
	CloneType2 = "type-2" // identical token structure, differing only in identifier/literal values
	CloneType3 = "type-3" // near-miss: structurally similar with small differences (future)
)

// Clone represents a group of identical code blocks found in multiple locations.
type Clone struct {
	Hash       string          `json:"hash"`
	Type       string          `json:"type"`
	Similarity float64         `json:"similarity"`
	LineCount  int             `json:"line_count"`
	TokenCount int             `json:"token_count"`
	Instances  []CloneInstance `json:"instances"`
}

// Report is the full result of a dupehound scan.
type Report struct {
	TotalFiles     int     `json:"total_files"`
	ScannedFiles   int     `json:"scanned_files"`
	TotalClones    int     `json:"total_clones"`
	DuplicateLines int     `json:"duplicate_lines"`
	Clones         []Clone `json:"clones"`
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
	Language  string   `yaml:"language"`
	Format    string   `yaml:"format"`
	Output    string   `yaml:"output"`
	ExitZero  bool     `yaml:"exit-zero"`
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
		Extensions:  []string{".js", ".mjs", ".cjs"},
		LineComment: "//",
		BlockStart:  "/*",
		BlockEnd:    "*/",
	},
	{
		Name:        "typescript",
		Extensions:  []string{".ts", ".tsx"},
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
