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
	for _, directory := range []string{extracted} {
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
	projectRoot, err := persistentProjectRoot(info.Manifest.Project.ID)
	if err != nil {
		return err
	}
	if err := removeProgramSources(projectRoot); err != nil {
		return fmt.Errorf("refresh packaged program: %w", err)
	}
	for _, source := range []struct {
		path    string
		prefix  string
		program bool
	}{
		{filepath.Join(extracted, "program", "source"), "program/source", true},
		{filepath.Join(extracted, "resources"), "resources", false},
	} {
		if err := mergeTree(source.path, projectRoot, info.Manifest.Build.Persistent, source.program); err != nil {
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

func mergeTree(sourceRoot, destinationRoot string, persistent []string, program bool) error {
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
		logical := filepath.ToSlash(relative)
		if !program && matchesPersistent(persistent, logical) {
			if _, err := os.Lstat(target); err == nil {
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		} else if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return os.Rename(path, target)
	})
}

func persistentProjectRoot(projectID string) (string, error) {
	dataRoot := os.Getenv("XDG_DATA_HOME")
	if dataRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user data directory: %w", err)
		}
		dataRoot = filepath.Join(home, ".local", "share")
	}
	root := filepath.Join(dataRoot, "fluxa", projectID, "project")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create persistent application data: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("persistent application data path is unsafe")
	}
	return root, nil
}

func removeProgramSources(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".flx") {
			return os.Remove(path)
		}
		return nil
	})
}

func matchesPersistent(patterns []string, logical string) bool {
	for _, pattern := range patterns {
		matched, _ := filepath.Match(filepath.FromSlash(pattern), filepath.FromSlash(logical))
		if matched {
			return true
		}
		if strings.HasSuffix(pattern, "/**") {
			prefix := strings.TrimSuffix(pattern, "**")
			if strings.HasPrefix(logical, prefix) {
				return true
			}
		}
	}
	return false
}
