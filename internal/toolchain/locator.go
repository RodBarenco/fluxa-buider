package toolchain

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Source identifies which discovery layer selected the executable.
type Source string

const (
	// SourceExplicit is the --fluxa command-line option.
	SourceExplicit Source = "command line"
	// SourceConfig is toolchain.path in fluxa.toml.
	SourceConfig Source = "project configuration"
	// SourceFluxaHome is the FLUXA_HOME environment variable.
	SourceFluxaHome Source = "FLUXA_HOME"
	// SourcePath is the process PATH.
	SourcePath Source = "PATH"
)

// Candidate is a resolved executable and its discovery source.
type Candidate struct {
	Path   string
	Source Source
}

// ResolveOptions provides all deterministic discovery inputs.
type ResolveOptions struct {
	ExplicitPath string
	ConfigPath   string
	FluxaHome    string
	PathEnv      string
	ProjectRoot  string
}

// Resolve locates Fluxa in command-line, config, FLUXA_HOME, PATH order.
func Resolve(options ResolveOptions) (Candidate, error) {
	if options.ExplicitPath != "" {
		path := resolveConfiguredPath(options.ExplicitPath, "")
		return validateCandidate(path, SourceExplicit)
	}
	if options.ConfigPath != "" {
		path := resolveConfiguredPath(options.ConfigPath, options.ProjectRoot)
		return validateCandidate(path, SourceConfig)
	}
	if options.FluxaHome != "" {
		path, err := resolveFluxaHome(options.FluxaHome)
		if err != nil {
			return Candidate{}, err
		}
		return validateCandidate(path, SourceFluxaHome)
	}

	path, err := findOnPath(options.PathEnv)
	if err != nil {
		return Candidate{}, err
	}
	return validateCandidate(path, SourcePath)
}

func resolveConfiguredPath(path, projectRoot string) string {
	if filepath.IsAbs(path) || projectRoot == "" {
		return path
	}
	return filepath.Join(projectRoot, path)
}

func resolveFluxaHome(value string) (string, error) {
	info, err := os.Stat(value)
	if err != nil {
		return "", &Error{
			Kind:      ErrorInvalidExecutable,
			Operation: "resolve",
			Path:      value,
			Err:       err,
		}
	}
	if info.IsDir() {
		return filepath.Join(value, executableName()), nil
	}
	return value, nil
}

func findOnPath(pathEnv string) (string, error) {
	for _, directory := range filepath.SplitList(pathEnv) {
		if directory == "" {
			continue
		}
		path := filepath.Join(directory, executableName())
		if isExecutable(path) {
			return path, nil
		}
	}
	return "", &Error{
		Kind:      ErrorNotFound,
		Operation: "locate",
		Detail:    "executable not found; use --fluxa, toolchain.path, FLUXA_HOME, or PATH",
	}
}

func validateCandidate(path string, source Source) (Candidate, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Candidate{}, &Error{
			Kind:      ErrorInvalidExecutable,
			Operation: "resolve",
			Path:      path,
			Err:       err,
		}
	}
	if !isExecutable(absolute) {
		return Candidate{}, &Error{
			Kind:      ErrorInvalidExecutable,
			Operation: "validate executable",
			Path:      absolute,
			Detail:    "path is missing, not a regular file, or not executable",
		}
	}

	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return Candidate{}, &Error{
			Kind:      ErrorInvalidExecutable,
			Operation: "resolve executable",
			Path:      absolute,
			Err:       err,
		}
	}
	return Candidate{Path: filepath.Clean(resolved), Source: source}, nil
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Ext(path), ".exe")
	}
	return info.Mode().Perm()&0o111 != 0
}

func executableName() string {
	if runtime.GOOS == "windows" {
		return "fluxa.exe"
	}
	return "fluxa"
}
