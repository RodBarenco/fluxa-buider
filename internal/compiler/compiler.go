// Package compiler defines Fluxa compilation artifacts and the temporary,
// explicitly source-exposed development fallback.
package compiler

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RodBarenco/fluxa-builder/internal/collector"
)

// Format identifies the generated program representation.
type Format string

const (
	// FormatSource is the temporary development-only source representation.
	FormatSource Format = "fluxa-source"
)

// Request describes compilation inputs already validated by the collector.
type Request struct {
	Files         []collector.Entry
	OutputDir     string
	IncludeSource bool
}

// Artifact describes one staged program file.
type Artifact struct {
	Path        string
	LogicalPath string
	Size        int64
	SHA256      string
}

// Result records the security and compatibility properties of generated files.
type Result struct {
	Format          Format
	Artifacts       []Artifact
	Debug           bool
	SourceExposed   bool
	BytecodeVersion string
	BytecodeABI     string
}

// Compile refuses a normal release until Fluxa provides a stable compiler.
// IncludeSource enables only the explicit development fallback.
func Compile(ctx context.Context, request Request) (Result, error) {
	if !request.IncludeSource {
		return Result{}, compilerError(
			ErrorUnavailable,
			"compile release",
			"",
			errors.New("fluxa does not expose a stable compile command; use --include-source only for a development artifact"),
		)
	}
	if request.OutputDir == "" {
		return Result{}, compilerError(ErrorInvalidInput, "validate output", "", errors.New("output directory is required"))
	}

	outputRoot, err := prepareOutput(request.OutputDir)
	if err != nil {
		return Result{}, err
	}

	artifacts := make([]Artifact, 0, len(request.Files))
	for _, file := range request.Files {
		if err := ctx.Err(); err != nil {
			return Result{}, compilerError(ErrorCanceled, "stage source", file.Path, err)
		}
		if file.Kind != collector.KindEntry && file.Kind != collector.KindModule {
			continue
		}
		if strings.ToLower(filepath.Ext(file.Path)) != ".flx" {
			return Result{}, compilerError(ErrorInvalidInput, "validate source", file.Path, errors.New("program source must use the .flx extension"))
		}
		artifact, err := copySource(outputRoot, file)
		if err != nil {
			return Result{}, err
		}
		artifacts = append(artifacts, artifact)
	}
	if len(artifacts) == 0 {
		return Result{}, compilerError(ErrorInvalidInput, "stage source", "", errors.New("no Fluxa program sources were selected"))
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })

	return Result{
		Format:          FormatSource,
		Artifacts:       artifacts,
		Debug:           true,
		SourceExposed:   true,
		BytecodeVersion: "",
		BytecodeABI:     "",
	}, nil
}

func prepareOutput(output string) (string, error) {
	absolute, err := filepath.Abs(output)
	if err != nil {
		return "", compilerError(ErrorInvalidInput, "resolve output", output, err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", compilerError(ErrorIO, "inspect output", absolute, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", compilerError(ErrorUnsafePath, "validate output", absolute, errors.New("output must be an existing non-symlink directory"))
	}
	return filepath.Clean(absolute), nil
}

func copySource(outputRoot string, file collector.Entry) (Artifact, error) {
	logical := filepath.ToSlash(filepath.Clean(file.Path))
	if logical == "." || logical == ".." || strings.HasPrefix(logical, "../") || filepath.IsAbs(file.Path) {
		return Artifact{}, compilerError(ErrorUnsafePath, "validate logical path", file.Path, errors.New("path must remain relative"))
	}
	destination := filepath.Join(outputRoot, "source", filepath.FromSlash(logical))
	relative, err := filepath.Rel(outputRoot, destination)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return Artifact{}, compilerError(ErrorUnsafePath, "resolve destination", file.Path, errors.New("destination escapes output"))
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return Artifact{}, compilerError(ErrorIO, "create destination directory", file.Path, err)
	}

	source, err := os.Open(file.SourcePath) // #nosec G304 -- collector validated the exact source path.
	if err != nil {
		return Artifact{}, compilerError(ErrorIO, "open source", file.Path, err)
	}
	defer func() { _ = source.Close() }()
	sourceInfo, err := source.Stat()
	if err != nil {
		return Artifact{}, compilerError(ErrorIO, "inspect source", file.Path, err)
	}
	if !sourceInfo.Mode().IsRegular() {
		return Artifact{}, compilerError(ErrorUnsafePath, "validate source", file.Path, errors.New("source is not a regular file"))
	}
	if sourceInfo.Size() != file.Size {
		return Artifact{}, compilerError(ErrorInvalidInput, "validate source", file.Path, fmt.Errorf("size changed after collection: got %d, expected %d", sourceInfo.Size(), file.Size))
	}

	target, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- destination is confined above.
	if err != nil {
		return Artifact{}, compilerError(ErrorIO, "create artifact", file.Path, err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(target, hash), source)
	closeErr := target.Close()
	if copyErr != nil {
		return Artifact{}, compilerError(ErrorIO, "copy source", file.Path, copyErr)
	}
	if closeErr != nil {
		return Artifact{}, compilerError(ErrorIO, "close artifact", file.Path, closeErr)
	}
	if written != file.Size {
		return Artifact{}, compilerError(ErrorInvalidInput, "validate artifact", file.Path, fmt.Errorf("wrote %d bytes, expected %d", written, file.Size))
	}

	return Artifact{
		Path:        filepath.ToSlash(filepath.Join("source", filepath.FromSlash(logical))),
		LogicalPath: logical,
		Size:        written,
		SHA256:      fmt.Sprintf("%x", hash.Sum(nil)),
	}, nil
}

func compilerError(kind ErrorKind, operation, path string, err error) *Error {
	return &Error{Kind: kind, Operation: operation, Path: path, Err: err}
}
