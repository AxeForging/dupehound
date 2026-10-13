package services

import "github.com/AxeForging/dupehound/domain"

// Import, include, use and package statements, which whole-file detection skips.

// importStarters lists, per language, the words that open a statement which
// only brings names into scope or declares where the file lives.
var importStarters = map[string]map[string]bool{
	"go":         strSet("import", "package"),
	"python":     strSet("import", "from"),
	"javascript": strSet("import"),
	"typescript": strSet("import"),
	"java":       strSet("import", "package"),
	"kotlin":     strSet("import", "package"),
	"scala":      strSet("import", "package"),
	"swift":      strSet("import"),
	"dart":       strSet("import", "export", "part", "library"),
	"csharp":     strSet("using", "namespace"),
	"rust":       strSet("use", "extern", "mod"),
	"c":          strSet("#"),
	"cpp":        strSet("#", "using"),
	"php":        strSet("use", "namespace", "require", "require_once", "include", "include_once"),
	"ruby":       strSet("require", "require_relative", "load"),
	"elixir":     strSet("import", "alias", "require", "use"),
	"r":          strSet("library", "require", "source"),
	"shell":      strSet("source"),
}

// importModifiers may precede an import starter on its line: `pub use`,
// `export * from`, `export import`.
var importModifiers = strSet("pub", "export", "public", "internal", "private", "static", "global")

// markImports returns a per-token mask of import-like statements: the ones in
// importStarters that begin a line outside any function body. A statement runs
// to the end of its line, or further while a bracket it opened is still open
// (`import ( … )`, `use a::{ … };`, `from x import ( … )`), or to its `;`.
func markImports(tokens []Token, inFunc []bool, lang *domain.Language) []bool {
	mask := make([]bool, len(tokens))
	if lang == nil {
		return mask
	}
	starters := importStarters[lang.Name]
	if len(starters) == 0 {
		return mask
	}
	word := func(i int) string { return tokens[i].Text + tokens[i].OrigText }
	for i := 0; i < len(tokens); i++ {
		if inFunc[i] || (i > 0 && tokens[i-1].Line == tokens[i].Line) {
			continue
		}
		// Step over modifiers to the word that decides.
		k := i
		for k+1 < len(tokens) && importModifiers[word(k)] && tokens[k+1].Line == tokens[i].Line {
			k++
		}
		if !starters[word(k)] || !isImportStatement(tokens, k, lang.Name) {
			continue
		}
		depth := 0
		j := i
		for ; j < len(tokens); j++ {
			if j > i && depth == 0 && tokens[j].Line != tokens[j-1].Line {
				break
			}
			mask[j] = true
			if tokens[j].Kind != TokOperator {
				continue
			}
			switch tokens[j].Text {
			case "(", "{", "[":
				depth++
			case ")", "}", "]":
				depth--
			case ";":
				if depth == 0 {
					j++
					goto done
				}
			}
		}
	done:
		i = j - 1
	}
	return mask
}

// isImportStatement rules out the uses of an import keyword that are code:
// a C preprocessor line that is not an include, a Rust `mod name { … }` body, a
// JS re-export is an import but `export function` is not.
func isImportStatement(tokens []Token, k int, lang string) bool {
	word := func(i int) string {
		if i >= len(tokens) {
			return ""
		}
		return tokens[i].Text + tokens[i].OrigText
	}
	switch lang {
	case "c", "cpp":
		if word(k) == "#" {
			return word(k+1) == "include" || word(k+1) == "import" || word(k+1) == "pragma"
		}
		return word(k+1) == "namespace" // `using namespace std;`
	case "rust":
		if word(k) == "mod" {
			// `mod name;` declares a file; `mod name { … }` holds code.
			return word(k+2) == ";"
		}
		if word(k) == "extern" {
			return word(k+1) == "crate"
		}
	case "csharp":
		if word(k) == "namespace" {
			// File-scoped `namespace X;` only; a block namespace holds code.
			for j := k + 1; j < len(tokens) && tokens[j].Line == tokens[k].Line; j++ {
				if tokens[j].Text == "{" {
					return false
				}
			}
			return tokens[min(k+1, len(tokens)-1)].Line == tokens[k].Line
		}
	case "php":
		if word(k) == "namespace" {
			for j := k + 1; j < len(tokens) && tokens[j].Line == tokens[k].Line; j++ {
				if tokens[j].Text == "{" {
					return false
				}
			}
		}
	case "dart":
		if word(k) == "export" || word(k) == "part" || word(k) == "library" {
			// Directives take a string or a name and end the line.
			return word(k+1) != "" && tokens[k+1].Line == tokens[k].Line
		}
	}
	return true
}
