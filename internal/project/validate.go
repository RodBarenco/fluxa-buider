package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	projectIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)+(?:-[a-z0-9]+)*$`)
	semverPattern    = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	windowsDrivePath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
)

// ValidProjectID reports whether id satisfies the same reverse-domain rule
// applied to project.id (and targets.macos.bundle_id) during validation.
func ValidProjectID(id string) bool {
	return projectIDPattern.MatchString(id)
}

// ValidSemVer reports whether version satisfies the same semantic version
// rule applied to project.version during validation.
func ValidSemVer(version string) bool {
	return semverPattern.MatchString(version)
}

// ValidPattern reports whether pattern satisfies the same safety and glob
// syntax rules applied to each entry of build.assets, build.exclude,
// build.persistent, and build.export during validation.
func ValidPattern(pattern string) bool {
	if pattern == "" || len(pattern) > maxPatternLength {
		return false
	}
	if !isSafeRelativePath(pattern) {
		return false
	}
	_, err := filepath.Match(pattern, "validation-probe")
	return err == nil
}

func validate(cfg *Config) error {
	if strings.TrimSpace(cfg.Project.Name) == "" {
		return validationError("project.name", cfg.Project.Name, "is required")
	}
	if len(cfg.Project.Name) > maxProjectNameBytes {
		return validationError("project.name", cfg.Project.Name, "exceeds 256 bytes")
	}
	if !projectIDPattern.MatchString(cfg.Project.ID) {
		return validationError("project.id", cfg.Project.ID, "must be a lowercase reverse-domain identifier")
	}
	if !semverPattern.MatchString(cfg.Project.Version) {
		return validationError("project.version", cfg.Project.Version, "must be a semantic version")
	}
	if cfg.Project.Entry == "" {
		return validationError("project.entry", cfg.Project.Entry, "is required")
	}

	entryPath, err := resolveProjectPath(cfg.Root, cfg.Project.Entry, true)
	if err != nil {
		return validationErrorWithCause("project.entry", cfg.Project.Entry, err)
	}
	info, err := os.Stat(entryPath)
	if err != nil {
		return validationErrorWithCause("project.entry", cfg.Project.Entry, err)
	}
	if !info.Mode().IsRegular() {
		return validationError("project.entry", cfg.Project.Entry, "must be a regular file")
	}
	cfg.EntryPath = entryPath

	outputPath, err := resolveProjectPath(cfg.Root, cfg.Build.Output, false)
	if err != nil {
		return validationErrorWithCause("build.output", cfg.Build.Output, err)
	}
	cfg.OutputPath = outputPath

	if cfg.Project.ModuleRoot != "" {
		if _, err := resolveProjectPath(cfg.Root, cfg.Project.ModuleRoot, false); err != nil {
			return validationErrorWithCause("project.module_root", cfg.Project.ModuleRoot, err)
		}
	}

	if err := validatePatterns("build.assets", cfg.Build.Assets); err != nil {
		return err
	}
	if err := validatePatterns("build.exclude", cfg.Build.Exclude); err != nil {
		return err
	}
	if err := validatePatterns("build.persistent", cfg.Build.Persistent); err != nil {
		return err
	}
	if err := validatePatterns("build.export", cfg.Build.Exported); err != nil {
		return err
	}
	persistent := make(map[string]struct{}, len(cfg.Build.Persistent))
	for _, pattern := range cfg.Build.Persistent {
		persistent[pattern] = struct{}{}
	}
	for index, pattern := range cfg.Build.Exported {
		if _, ok := persistent[pattern]; !ok {
			return validationError(fmt.Sprintf("build.export[%d]", index), pattern,
				"must also appear exactly in build.persistent")
		}
	}

	if cfg.Package.Format != "portable" && cfg.Package.Format != "zip" {
		return validationError("package.format", cfg.Package.Format, "must be portable or zip")
	}

	iconFields := []struct {
		name  string
		value string
	}{
		{"targets.windows.icon", cfg.Targets.Windows.Icon},
		{"targets.linux.icon", cfg.Targets.Linux.Icon},
		{"targets.macos.icon", cfg.Targets.MacOS.Icon},
	}
	for _, icon := range iconFields {
		if icon.value == "" {
			continue
		}
		if _, err := resolveProjectPath(cfg.Root, icon.value, false); err != nil {
			return validationErrorWithCause(icon.name, icon.value, err)
		}
	}

	if cfg.Targets.MacOS.BundleID != "" && !projectIDPattern.MatchString(cfg.Targets.MacOS.BundleID) {
		return validationError("targets.macos.bundle_id", cfg.Targets.MacOS.BundleID, "must be a lowercase reverse-domain identifier")
	}

	return nil
}

func validatePatterns(prefix string, patterns []string) error {
	if len(patterns) > maxPatterns {
		return validationError(prefix, "", "contains more than 256 patterns")
	}

	for index, pattern := range patterns {
		field := fmt.Sprintf("%s[%d]", prefix, index)
		if pattern == "" {
			return validationError(field, pattern, "must not be empty")
		}
		if len(pattern) > maxPatternLength {
			return validationError(field, pattern, "exceeds 4096 bytes")
		}
		if !isSafeRelativePath(pattern) {
			return validationError(field, pattern, "must be a relative pattern within the project")
		}
		if _, err := filepath.Match(pattern, "validation-probe"); err != nil {
			return validationErrorWithCause(field, pattern, err)
		}
	}
	return nil
}

func resolveProjectPath(root, relative string, mustExist bool) (string, error) {
	if !isSafeRelativePath(relative) {
		return "", fmt.Errorf("path must be relative and remain within the project")
	}

	portable := strings.ReplaceAll(relative, `\`, "/")
	candidate := filepath.Join(root, filepath.FromSlash(portable))
	resolved, err := resolveWithExistingParent(candidate)
	if err != nil {
		return "", err
	}
	if !pathWithin(root, resolved) {
		return "", fmt.Errorf("path escapes project root")
	}
	if mustExist {
		if _, err := os.Stat(resolved); err != nil {
			return "", err
		}
	}
	return filepath.Clean(resolved), nil
}

func resolveWithExistingParent(path string) (string, error) {
	current := filepath.Clean(path)
	var missing []string

	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func isSafeRelativePath(value string) bool {
	if value == "" || strings.IndexByte(value, 0) >= 0 {
		return false
	}
	if filepath.IsAbs(value) || filepath.VolumeName(value) != "" ||
		strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\`) ||
		windowsDrivePath.MatchString(value) {
		return false
	}

	normalized := strings.ReplaceAll(value, `\`, "/")
	for _, part := range strings.Split(normalized, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func validationError(field, value, message string) *Error {
	return validationErrorWithCause(field, value, errors.New(message))
}

func validationErrorWithCause(field, value string, err error) *Error {
	return &Error{
		Kind:      ErrorValidation,
		Operation: "validate",
		Field:     field,
		Value:     value,
		Err:       err,
	}
}
