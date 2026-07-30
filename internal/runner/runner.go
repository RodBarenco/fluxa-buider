// Package runner executes a verified source-format FLXPKG with a Fluxa runtime.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
)

// Request describes a package execution using the existing Fluxa script runtime.
type Request struct {
	PackagePath string
	RuntimePath string
	Stdout      io.Writer
	Stderr      io.Writer
	Stdin       io.Reader
}

// Run verifies and materializes the package in a private temporary directory,
// then executes Fluxa using the project-mode CLI contract.
func Run(ctx context.Context, request Request) error {
	if request.PackagePath == "" || request.RuntimePath == "" {
		return errors.New("package and runtime paths are required")
	}
	workspace, err := os.MkdirTemp("", "fluxa-app-*")
	if err != nil {
		return fmt.Errorf("create private runtime workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(workspace) }()
	if err := os.Chmod(workspace, 0o700); err != nil {
		return fmt.Errorf("secure runtime workspace: %w", err)
	}

	extracted := filepath.Join(workspace, "package")
	projectRoot := filepath.Join(workspace, "project")
	for _, directory := range []string{extracted, projectRoot} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return fmt.Errorf("create runtime directory: %w", err)
		}
	}
	info, err := flxpkg.Extract(request.PackagePath, extracted)
	if err != nil {
		return fmt.Errorf("extract verified Fluxa package: %w", err)
	}
	if info.Manifest.Build.ProgramFormat != "fluxa-source" {
		return fmt.Errorf("unsupported program format %q", info.Manifest.Build.ProgramFormat)
	}
	for _, source := range []struct {
		path   string
		prefix string
	}{
		{filepath.Join(extracted, "program", "source"), "program/source"},
		{filepath.Join(extracted, "resources"), "resources"},
	} {
		if err := mergeTree(source.path, projectRoot); err != nil {
			return fmt.Errorf("materialize %s: %w", source.prefix, err)
		}
	}

	entry := filepath.FromSlash(info.Manifest.Project.Entry)
	if entry == "" || filepath.IsAbs(entry) || strings.HasPrefix(filepath.Clean(entry), "..") {
		return errors.New("package manifest contains an unsafe project entry")
	}
	if fileInfo, err := os.Stat(filepath.Join(projectRoot, entry)); err != nil || !fileInfo.Mode().IsRegular() {
		return fmt.Errorf("project entry %q is missing from package", info.Manifest.Project.Entry)
	}

	command := exec.CommandContext(ctx, request.RuntimePath, "run", entry, "-proj", ".") // #nosec G204 -- verified caller-selected runtime.
	command.Dir = projectRoot
	command.Stdin = request.Stdin
	command.Stdout = request.Stdout
	command.Stderr = request.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("run Fluxa project: %w", err)
	}
	return nil
}

func mergeTree(sourceRoot, destinationRoot string) error {
	info, err := os.Stat(sourceRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() {
		return errors.New("package section is not a directory")
	}
	return filepath.WalkDir(sourceRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil || relative == "." {
			return err
		}
		target := filepath.Join(destinationRoot, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				return fmt.Errorf("package sections collide at %q", filepath.ToSlash(relative))
			}
			return err
		}
		return os.Rename(path, target)
	})
}
