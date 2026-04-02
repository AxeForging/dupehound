package helpers

import "errors"

var (
	ErrNoFilesFound     = errors.New("no supported source files found in path")
	ErrInvalidFormat    = errors.New("invalid output format: must be text, json, or sarif")
	ErrInvalidMinLines  = errors.New("min-lines must be greater than 0")
	ErrInvalidMinTokens = errors.New("min-tokens must be greater than 0")
	ErrPathNotFound     = errors.New("path does not exist")
	// ErrClonesFound is returned by the scan action when duplicates are detected.
	// main() maps this to exit code 1 (distinct from exit code 2 for tool errors).
	ErrClonesFound = errors.New("duplicate code detected")
)
