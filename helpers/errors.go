package helpers

import "errors"

var (
	ErrNoFilesFound      = errors.New("no supported source files found in path")
	ErrInvalidFormat     = errors.New("invalid output format: must be text, json, sarif, or md")
	ErrThresholdExceeded = errors.New("duplication percentage exceeds threshold")
	ErrInvalidMinLines   = errors.New("min-lines must be greater than 0")
	ErrInvalidMinTokens  = errors.New("min-tokens must be greater than 0")
	ErrPathNotFound      = errors.New("path does not exist")
	// ErrClonesFound is returned by the scan action when duplicates are detected.
	// main() maps this to exit code 1 (distinct from exit code 2 for tool errors).
	ErrClonesFound = errors.New("duplicate code detected")
	// ErrTooManyFiles is returned when the file collector hits the --max-files
	// safety cap. This is the fail-fast guard for runaway scans on huge trees.
	ErrTooManyFiles = errors.New("too many files to scan: raise --max-files or narrow --include")
	// ErrTooManyPairs is returned when the fuzzy detector would build more
	// candidate pairs than --max-pairs allows. The fix is one of:
	// raise --max-pairs, lower --max-bucket, or disable fuzzy with --similarity 1.0.
	ErrTooManyPairs = errors.New("fuzzy detector exceeded --max-pairs cap: lower --max-bucket, raise --max-pairs, or disable fuzzy with --similarity 1.0")
)
