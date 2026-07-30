// Package manifest defines the deterministic internal Fluxa package manifest.
package manifest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RodBarenco/fluxa-builder/internal/collector"
	"github.com/RodBarenco/fluxa-builder/internal/compiler"
	"github.com/RodBarenco/fluxa-builder/internal/project"
	"github.com/RodBarenco/fluxa-builder/internal/toolchain"
)

const (
	// CurrentFormatVersion is the only manifest schema accepted by this build.
	CurrentFormatVersion = 1
	maxFiles             = 100_000
)

// Manifest is free of timestamps, absolute paths, and workspace identifiers.
type Manifest struct {
	FormatVersion int       `json:"format_version"`
	Project       Project   `json:"project"`
	Toolchain     Toolchain `json:"toolchain"`
	Target        Target    `json:"target"`
	Build         Build     `json:"build"`
	Files         []File    `json:"files"`
}

// Project identifies the packaged application.
type Project struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	Version string `json:"version"`
	Entry   string `json:"entry"`
	Type    string `json:"type"`
}

// Toolchain identifies the exact Fluxa binary and program representation.
type Toolchain struct {
	Protocol        string `json:"protocol"`
	FluxaVersion    string `json:"fluxa_version,omitempty"`
	FluxaSHA256     string `json:"fluxa_sha256"`
	LibrariesSHA256 string `json:"libraries_sha256"`
	BytecodeVersion string `json:"bytecode_version,omitempty"`
	BytecodeABI     string `json:"bytecode_abi,omitempty"`
}

// Target identifies the resolved build platform and terminal behavior.
type Target struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Terminal bool   `json:"terminal"`
}

// Build records checks and security-relevant compilation properties.
type Build struct {
	Preflight     string   `json:"preflight"`
	ProgramFormat string   `json:"program_format"`
	Debug         bool     `json:"debug"`
	SourceExposed bool     `json:"source_exposed"`
	Persistent    []string `json:"persistent,omitempty"`
	Exported      []string `json:"export,omitempty"`
}

// File maps a package path to the logical path used by the Fluxa project.
type File struct {
	Path        string `json:"path"`
	LogicalPath string `json:"logical_path"`
	Kind        string `json:"kind"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}

// Input contains the validated outputs of earlier build phases.
type Input struct {
	Project     *project.Config
	Toolchain   toolchain.Identity
	Compilation compiler.Result
	Collection  collector.Result
	TargetOS    string
	TargetArch  string
}

// New constructs and validates a manifest, hashing selected assets.
func New(ctx context.Context, input Input) (Manifest, error) {
	if input.Project == nil {
		return Manifest{}, manifestError(ErrorInvalid, "build", "", errors.New("project configuration is required"))
	}

	files := make([]File, 0, len(input.Compilation.Artifacts)+len(input.Collection.Entries))
	for _, artifact := range input.Compilation.Artifacts {
		files = append(files, File{
			Path:        "program/" + artifact.Path,
			LogicalPath: artifact.LogicalPath,
			Kind:        "program",
			Size:        artifact.Size,
			SHA256:      artifact.SHA256,
		})
	}
	for _, entry := range input.Collection.Entries {
		if entry.Kind != collector.KindAsset {
			continue
		}
		if err := ctx.Err(); err != nil {
			return Manifest{}, manifestError(ErrorCanceled, "hash assets", entry.Path, err)
		}
		hash, err := hashSelectedFile(entry)
		if err != nil {
			return Manifest{}, err
		}
		files = append(files, File{
			Path:        "resources/" + entry.Path,
			LogicalPath: entry.Path,
			Kind:        "asset",
			Size:        entry.Size,
			SHA256:      hash,
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	librariesHash, err := hashOptionalProjectFile(filepath.Join(input.Project.Root, "fluxa.libs"))
	if err != nil {
		return Manifest{}, err
	}

	value := Manifest{
		FormatVersion: CurrentFormatVersion,
		Project: Project{
			Name:    input.Project.Project.Name,
			ID:      input.Project.Project.ID,
			Version: input.Project.Project.Version,
			Entry:   filepath.ToSlash(input.Project.Project.Entry),
			Type:    input.Project.Project.Type,
		},
		Toolchain: Toolchain{
			Protocol:        input.Toolchain.Protocol,
			FluxaVersion:    input.Toolchain.Version,
			FluxaSHA256:     input.Toolchain.SHA256,
			LibrariesSHA256: librariesHash,
			BytecodeVersion: input.Compilation.BytecodeVersion,
			BytecodeABI:     input.Compilation.BytecodeABI,
		},
		Target: Target{
			OS:       input.TargetOS,
			Arch:     input.TargetArch,
			Terminal: input.Project.Build.Terminal,
		},
		Build: Build{
			Preflight:     "not_run",
			ProgramFormat: string(input.Compilation.Format),
			Debug:         input.Compilation.Debug,
			SourceExposed: input.Compilation.SourceExposed,
			Persistent:    append([]string(nil), input.Project.Build.Persistent...),
			Exported:      append([]string(nil), input.Project.Build.Exported...),
		},
		Files: files,
	}
	if err := Validate(value); err != nil {
		return Manifest{}, err
	}
	return value, nil
}

// Validate checks schema, portability, ordering, duplicates, and hashes.
func Validate(value Manifest) error {
	if value.FormatVersion != CurrentFormatVersion {
		return manifestError(ErrorUnknownVersion, "validate", "format_version", fmt.Errorf("unsupported version %d", value.FormatVersion))
	}
	required := []struct {
		path  string
		value string
	}{
		{"project.name", value.Project.Name},
		{"project.id", value.Project.ID},
		{"project.version", value.Project.Version},
		{"project.entry", value.Project.Entry},
		{"project.type", value.Project.Type},
		{"toolchain.protocol", value.Toolchain.Protocol},
		{"toolchain.fluxa_sha256", value.Toolchain.FluxaSHA256},
		{"toolchain.libraries_sha256", value.Toolchain.LibrariesSHA256},
		{"target.os", value.Target.OS},
		{"target.arch", value.Target.Arch},
		{"build.preflight", value.Build.Preflight},
		{"build.program_format", value.Build.ProgramFormat},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return manifestError(ErrorInvalid, "validate", field.path, errors.New("required field is empty"))
		}
	}
	if value.Build.Preflight != "not_run" {
		return manifestError(ErrorInvalid, "validate", "build.preflight", errors.New("must be not_run while automatic preflight is deferred"))
	}
	if !validHash(value.Toolchain.FluxaSHA256) {
		return manifestError(ErrorInvalid, "validate", "toolchain.fluxa_sha256", errors.New("must be 64 lowercase hexadecimal characters"))
	}
	if !validHash(value.Toolchain.LibrariesSHA256) {
		return manifestError(ErrorInvalid, "validate", "toolchain.libraries_sha256", errors.New("must be 64 lowercase hexadecimal characters"))
	}
	if len(value.Files) == 0 {
		return manifestError(ErrorInvalid, "validate", "files", errors.New("must contain at least one file"))
	}
	if len(value.Files) > maxFiles {
		return manifestError(ErrorInvalid, "validate", "files", fmt.Errorf("exceeds maximum of %d files", maxFiles))
	}
	for field, patterns := range map[string][]string{
		"build.persistent": value.Build.Persistent,
		"build.export":     value.Build.Exported,
	} {
		for index, pattern := range patterns {
			if pattern == "" || strings.HasPrefix(pattern, "/") || strings.Contains(pattern, `\`) ||
				strings.Contains(pattern, "..") || strings.HasSuffix(strings.ToLower(pattern), ".flx") {
				return manifestError(ErrorInvalid, "validate", fmt.Sprintf("%s[%d]", field, index),
					errors.New("must be a safe non-source project-relative slash pattern"))
			}
			if _, err := filepath.Match(pattern, "validation-probe"); err != nil {
				return manifestError(ErrorInvalid, "validate", fmt.Sprintf("%s[%d]", field, index), err)
			}
		}
	}
	persistent := make(map[string]struct{}, len(value.Build.Persistent))
	for _, pattern := range value.Build.Persistent {
		persistent[pattern] = struct{}{}
	}
	for index, pattern := range value.Build.Exported {
		if _, ok := persistent[pattern]; !ok {
			return manifestError(ErrorInvalid, "validate", fmt.Sprintf("build.export[%d]", index),
				errors.New("must also appear exactly in build.persistent"))
		}
	}

	paths := make(map[string]string, len(value.Files))
	logicalProgramPaths := make(map[string]struct{})
	previous := ""
	for index, file := range value.Files {
		field := fmt.Sprintf("files[%d]", index)
		if !safeSlashPath(file.Path) || !safeSlashPath(file.LogicalPath) {
			return manifestError(ErrorInvalid, "validate", field, errors.New("paths must be normalized relative slash paths"))
		}
		if file.Kind != "program" && file.Kind != "asset" {
			return manifestError(ErrorInvalid, "validate", field+".kind", errors.New("must be program or asset"))
		}
		if file.Size < 0 {
			return manifestError(ErrorInvalid, "validate", field+".size", errors.New("must not be negative"))
		}
		if !validHash(file.SHA256) {
			return manifestError(ErrorInvalid, "validate", field+".sha256", errors.New("must be 64 lowercase hexadecimal characters"))
		}
		if index > 0 && file.Path <= previous {
			return manifestError(ErrorInvalid, "validate", field+".path", errors.New("files must be strictly sorted by path"))
		}
		folded := strings.ToLower(file.Path)
		if prior, exists := paths[folded]; exists {
			return manifestError(ErrorInvalid, "validate", field+".path", fmt.Errorf("duplicates or case-collides with %q", prior))
		}
		paths[folded] = file.Path
		if file.Kind == "program" {
			if _, exists := logicalProgramPaths[file.LogicalPath]; exists {
				return manifestError(ErrorInvalid, "validate", field+".logical_path", errors.New("duplicate program logical path"))
			}
			logicalProgramPaths[file.LogicalPath] = struct{}{}
		}
		previous = file.Path
	}
	return nil
}

func hashSelectedFile(entry collector.Entry) (string, error) {
	file, err := os.Open(entry.SourcePath) // #nosec G304 -- collector validated this path.
	if err != nil {
		return "", manifestError(ErrorIO, "open asset", entry.Path, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", manifestError(ErrorIO, "inspect asset", entry.Path, err)
	}
	if !info.Mode().IsRegular() || info.Size() != entry.Size {
		return "", manifestError(ErrorInvalid, "validate asset", entry.Path, errors.New("asset type or size changed after collection"))
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", manifestError(ErrorIO, "hash asset", entry.Path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func hashOptionalProjectFile(path string) (string, error) {
	linkInfo, err := os.Lstat(path)
	if err == nil && linkInfo.Mode()&os.ModeSymlink != 0 {
		return "", manifestError(ErrorInvalid, "validate libraries configuration", path, errors.New("fluxa.libs must not be a symlink"))
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", manifestError(ErrorIO, "inspect libraries configuration", path, err)
	}
	file, err := os.Open(path) // #nosec G304 -- fixed fluxa.libs name under validated project root.
	if errors.Is(err, os.ErrNotExist) {
		empty := sha256.Sum256(nil)
		return hex.EncodeToString(empty[:]), nil
	}
	if err != nil {
		return "", manifestError(ErrorIO, "open libraries configuration", path, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", manifestError(ErrorIO, "inspect libraries configuration", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", manifestError(ErrorInvalid, "validate libraries configuration", path, errors.New("fluxa.libs must be a regular file"))
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", manifestError(ErrorIO, "hash libraries configuration", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func safeSlashPath(value string) bool {
	if value == "" || value == "." || value == ".." || strings.Contains(value, `\`) ||
		strings.HasPrefix(value, "/") || strings.ContainsRune(value, 0) ||
		strings.Contains(strings.Split(value, "/")[0], ":") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(value))) == value
}

func manifestError(kind ErrorKind, operation, path string, err error) *Error {
	return &Error{Kind: kind, Operation: operation, Path: path, Err: err}
}
