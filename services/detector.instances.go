package services

import (
	"bytes"
	"os"
	"strings"

	"github.com/AxeForging/dupehound/domain"
)

// Turning token positions into reported clone instances: lines, function attribution, previews.

// buildInstances creates CloneInstance structs with lazy-loaded preview lines.
func buildInstances(files []TokenizedFile, starts []globalPos, totalTokens int) []domain.CloneInstance {
	instances := make([]domain.CloneInstance, 0, len(starts))
	for _, s := range starts {
		instances = append(instances, buildSingleInstance(files, s.FileIdx, s.Pos, totalTokens))
	}
	return instances
}

// buildSingleInstance creates a single CloneInstance with lazy preview and
// best-effort enclosing-function attribution.
func buildSingleInstance(files []TokenizedFile, fileIdx, pos, totalTokens int) domain.CloneInstance {
	toks := files[fileIdx].Tokens
	startLine := toks[pos].Line
	endIdx := pos + totalTokens - 1
	if endIdx >= len(toks) {
		endIdx = len(toks) - 1
	}
	// A string literal can span lines (heredocs, raw and triple-quoted strings);
	// the instance ends where its last token ends, not where it starts.
	endLine := toks[endIdx].Line
	if last := toks[endIdx]; last.Kind == TokString {
		endLine += strings.Count(last.OrigText, "\n")
	}
	preview := loadPreviewLines(files[fileIdx].Path, startLine, endLine)

	return domain.CloneInstance{
		File:      files[fileIdx].Path,
		StartLine: startLine,
		EndLine:   endLine,
		Function:  funcNameForRange(files[fileIdx].Funcs, pos, endIdx),
		Lines:     preview,
	}
}

// loadPreviewLines reads the specified line range from a file on disk.
// This avoids holding all source lines in memory during detection.
//
// Each returned line is its own copy. Slicing them out of the file contents
// instead would keep the whole file alive for as long as the preview is — once
// per clone instance, which is gigabytes on a large repository.
func loadPreviewLines(path string, startLine, endLine int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	preview := make([]string, 0, max(endLine-startLine+1, 0))
	for ln := 1; ln <= endLine; ln++ {
		nl := bytes.IndexByte(data, '\n')
		last := nl < 0
		if last {
			nl = len(data)
		}
		if ln >= startLine {
			preview = append(preview, string(data[:nl]))
		}
		if last {
			break
		}
		data = data[nl+1:]
	}
	return preview
}
