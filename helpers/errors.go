package helpers

import "errors"

var (
	ErrNoFilesFound    = errors.New("no supported source files found in path")
	ErrInvalidFormat   = errors.New("invalid output format: must be text, json, or sarif")
	ErrInvalidMinLines = errors.New("min-lines must be greater than 0")
	ErrPathNotFound    = errors.New("path does not exist")
)
