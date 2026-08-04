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
	"runtime"
	"strings"

	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
	"github.com/RodBarenco/fluxa-builder/internal/runtimeprotocol"
)

// Request describes a package execution using the existing Fluxa script runtime.
type Request struct {
	PackagePath     string
	RuntimePath     string
	DistributionDir string
	PackagedRuntime bool
	Stdout          io.Writer
	Stderr          io.Writer
	Stdin           io.Reader
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
	if err := os.Chmod(workspace, 0o700); err != nil { // #nosec G302 -- private temp directory needs the execute bit to be traversable.
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

	var command *exec.Cmd
	if request.PackagedRuntime {
		command = exec.CommandContext(ctx, request.RuntimePath, // #nosec G204 -- verified packaged runtime.
			runtimeprotocol.Command, entry, projectRoot)
		command.Env = append(os.Environ(),
			runtimeprotocol.AuthEnvVar+"="+runtimeprotocol.AuthValue)
	} else {
		command = exec.CommandContext(ctx, request.RuntimePath, "run", entry, "-proj", ".") // #nosec G204 -- caller-selected development runtime.
	}
	command.Dir = projectRoot
	command.Stdin = request.Stdin
	command.Stdout = request.Stdout
	command.Stderr = request.Stderr
	if err := exportVisibleData(projectRoot, request.DistributionDir,
		info.Manifest.Project.Name, info.Manifest.Build.Exported); err != nil {
		return fmt.Errorf("export visible application data before launch: %w", err)
	}
	runErr := command.Run()
	exportErr := exportVisibleData(projectRoot, request.DistributionDir,
		info.Manifest.Project.Name, info.Manifest.Build.Exported)
	if runErr != nil || exportErr != nil {
		return errors.Join(
			wrapError("run Fluxa project", runErr),
			wrapError("export visible application data", exportErr),
		)
	}
	return nil
}

func wrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
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
		return os.Rename(path, target) // #nosec G122 -- path is WalkDir's own entry within a just-extracted, verified package tree.
	})
}

func persistentProjectRoot(projectID string) (string, error) {
	dataRoot, err := platformDataRoot()
	if err != nil {
		return "", err
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

func platformDataRoot() (string, error) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		root, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("resolve platform application data directory: %w", err)
		}
		return root, nil
	}
	if root := os.Getenv("XDG_DATA_HOME"); root != "" {
		return root, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user data directory: %w", err)
	}
	return filepath.Join(home, ".local", "share"), nil
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
			return os.Remove(path) // #nosec G122 -- path is WalkDir's own entry within the private persistent project root.
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

func exportVisibleData(projectRoot, distributionDir, projectName string, patterns []string) error {
	if len(patterns) == 0 {
		return nil
	}
	exportRoot := distributionDir
	if !directoryWritable(exportRoot) {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		exportRoot = filepath.Join(home, "Documents", safeDirectoryName(projectName))
		if err := os.MkdirAll(exportRoot, 0o700); err != nil {
			return err
		}
	}
	return filepath.WalkDir(projectRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		logical, err := filepath.Rel(projectRoot, path)
		if err != nil {
			return err
		}
		logical = filepath.ToSlash(logical)
		if !matchesPersistent(patterns, logical) {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("exported data must contain only regular files")
		}
		target := filepath.Join(exportRoot, filepath.FromSlash(logical))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return copyVisibleFile(path, target)
	})
}

func directoryWritable(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false
	}
	probe, err := os.CreateTemp(path, ".fluxa-write-test-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	return errors.Join(probe.Close(), os.Remove(name)) == nil
}

func safeDirectoryName(name string) string {
	name = strings.TrimSpace(strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(name))
	if name == "" || name == "." || name == ".." {
		return "Fluxa Application"
	}
	return name
}

func copyVisibleFile(source, target string) error {
	input, err := os.Open(source) // #nosec G304 -- source is confined to the persistent project root.
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	temp, err := os.CreateTemp(filepath.Dir(target), ".fluxa-export-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	complete := false
	defer func() {
		_ = temp.Close()
		if !complete {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := io.Copy(temp, input); err != nil {
		return err
	}
	if err := errors.Join(temp.Sync(), temp.Close()); err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(tempPath, target); err != nil {
		return err
	}
	complete = true
	return nil
}
