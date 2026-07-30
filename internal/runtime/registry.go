package runtimepkg

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"

	windowspkg "github.com/RodBarenco/fluxa-builder/internal/windows"
)

const metadataFilename = "runtime.json"

// Runtime is a verified registry entry.
type Runtime struct {
	Directory  string
	BinaryPath string
	Metadata   Metadata
}

// Requirement describes the runtime identity required by a package.
type Requirement struct {
	FluxaVersion         string
	ToolchainSHA256      string
	PackageFormatVersion uint32
	BytecodeVersion      string
	BytecodeABI          string
	LibrariesSHA256      string
	ProgramFormat        string
	OS                   string
	Arch                 string
	Terminal             bool
}

// DefaultRoot returns ~/.fluxa-builder/runtimes or an explicit Builder home.
func DefaultRoot(builderHome string) (string, error) {
	if builderHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", runtimeError(ErrorIO, "resolve home", "", err)
		}
		builderHome = filepath.Join(home, ".fluxa-builder")
	}
	absolute, err := filepath.Abs(builderHome)
	if err != nil {
		return "", runtimeError(ErrorIO, "resolve registry", builderHome, err)
	}
	return filepath.Join(absolute, "runtimes"), nil
}

// Add verifies and copies a runtime into an unoccupied version/target slot.
func Add(root, binaryPath string, metadata Metadata) (Runtime, error) {
	if err := metadata.Validate(); err != nil {
		return Runtime{}, err
	}
	sourceInfo, err := os.Stat(binaryPath)
	if err != nil {
		return Runtime{}, runtimeError(ErrorIO, "inspect binary", binaryPath, err)
	}
	if !sourceInfo.Mode().IsRegular() {
		return Runtime{}, runtimeError(ErrorInvalid, "validate binary", binaryPath, errors.New("binary must be a regular file"))
	}
	if goruntime.GOOS != "windows" && metadata.OS != "windows" && sourceInfo.Mode().Perm()&0o111 == 0 {
		return Runtime{}, runtimeError(ErrorPermission, "validate binary", binaryPath, errors.New("binary is not executable"))
	}
	if goruntime.GOOS == "windows" && metadata.OS == "windows" {
		if err := windowspkg.ValidatePEAMD64(binaryPath); err != nil {
			return Runtime{}, runtimeError(ErrorInvalid, "validate Windows runtime PE", binaryPath, err)
		}
	}
	actualHash, err := hashRuntimeFile(binaryPath)
	if err != nil {
		return Runtime{}, err
	}
	if actualHash != metadata.BinarySHA256 {
		return Runtime{}, runtimeError(ErrorIntegrity, "validate binary hash", binaryPath, errors.New("binary SHA-256 differs from metadata"))
	}

	root, err = ensureRegistryRoot(root)
	if err != nil {
		return Runtime{}, err
	}
	target := targetName(metadata.OS, metadata.Arch)
	versionDirectory := filepath.Join(root, metadata.FluxaVersion)
	if err := ensureRegistryDirectory(versionDirectory); err != nil {
		return Runtime{}, err
	}
	targetDirectory := filepath.Join(versionDirectory, target)
	if err := ensureRegistryDirectory(targetDirectory); err != nil {
		return Runtime{}, err
	}
	directory := filepath.Join(targetDirectory, terminalName(metadata.Terminal))
	if err := os.Mkdir(directory, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Runtime{}, runtimeError(ErrorExists, "add", directory, errors.New("runtime slot already exists"))
		}
		return Runtime{}, runtimeError(ErrorIO, "create runtime directory", directory, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(directory)
		}
	}()

	destination := filepath.Join(directory, metadata.BinaryName)
	if err := copyRuntimeFile(binaryPath, destination, metadata.OS != "windows"); err != nil {
		return Runtime{}, err
	}
	metadataBytes, err := encodeMetadata(metadata)
	if err != nil {
		return Runtime{}, err
	}
	if err := os.WriteFile(filepath.Join(directory, metadataFilename), metadataBytes, 0o600); err != nil { // #nosec G304 -- confined registry path.
		return Runtime{}, runtimeError(ErrorIO, "write metadata", directory, err)
	}
	committed = true
	return Load(directory)
}

// Load validates metadata, placement, permissions, and binary hash.
func Load(directory string) (Runtime, error) {
	metadataPath := filepath.Join(directory, metadataFilename)
	metadataInfo, err := os.Lstat(metadataPath)
	if err == nil && metadataInfo.Mode()&os.ModeSymlink != 0 {
		return Runtime{}, runtimeError(ErrorInvalid, "validate metadata", metadataPath, errors.New("metadata must not be a symlink"))
	}
	file, err := os.Open(metadataPath) // #nosec G304 -- registry entry is treated as untrusted.
	if err != nil {
		kind := ErrorIO
		if errors.Is(err, os.ErrNotExist) {
			kind = ErrorInvalid
		}
		return Runtime{}, runtimeError(kind, "open metadata", metadataPath, err)
	}
	metadata, decodeErr := decodeMetadata(file)
	closeErr := file.Close()
	if decodeErr != nil || closeErr != nil {
		return Runtime{}, runtimeError(ErrorInvalid, "load metadata", metadataPath, errors.Join(decodeErr, closeErr))
	}
	registryRoot := filepath.Dir(filepath.Dir(filepath.Dir(directory)))
	expectedDirectory := filepath.Join(
		registryRoot,
		metadata.FluxaVersion,
		targetName(metadata.OS, metadata.Arch),
		terminalName(metadata.Terminal),
	)
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil || filepath.Clean(absoluteDirectory) != filepath.Clean(expectedDirectory) {
		return Runtime{}, runtimeError(ErrorInvalid, "validate metadata placement", directory, errors.New("directory does not match version and target"))
	}
	binaryPath := filepath.Join(directory, metadata.BinaryName)
	info, err := os.Lstat(binaryPath)
	if err != nil {
		return Runtime{}, runtimeError(ErrorIO, "inspect binary", binaryPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Runtime{}, runtimeError(ErrorInvalid, "validate binary", binaryPath, errors.New("binary must be a non-symlink regular file"))
	}
	if goruntime.GOOS != "windows" && metadata.OS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return Runtime{}, runtimeError(ErrorPermission, "validate binary", binaryPath, errors.New("binary is not executable"))
	}
	if goruntime.GOOS == "windows" && metadata.OS == "windows" {
		if err := windowspkg.ValidatePEAMD64(binaryPath); err != nil {
			return Runtime{}, runtimeError(ErrorInvalid, "validate Windows runtime PE", binaryPath, err)
		}
	}
	hash, err := hashRuntimeFile(binaryPath)
	if err != nil {
		return Runtime{}, err
	}
	if hash != metadata.BinarySHA256 {
		return Runtime{}, runtimeError(ErrorIntegrity, "verify binary", binaryPath, errors.New("binary SHA-256 mismatch"))
	}
	return Runtime{Directory: directory, BinaryPath: binaryPath, Metadata: metadata}, nil
}

// List returns all verified runtimes in deterministic order.
func List(root string) ([]Runtime, error) {
	var err error
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, runtimeError(ErrorIO, "resolve registry", root, err)
	}
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return []Runtime{}, nil
	}
	if err != nil {
		return nil, runtimeError(ErrorIO, "inspect registry", root, err)
	}
	if !info.IsDir() {
		return nil, runtimeError(ErrorInvalid, "validate registry", root, errors.New("registry root is not a directory"))
	}
	var runtimes []Runtime
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return runtimeError(ErrorIO, "walk registry", path, walkErr)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return runtimeError(ErrorInvalid, "walk registry", path, errors.New("symlinks are not allowed"))
		}
		if entry.Name() != metadataFilename {
			return nil
		}
		value, err := Load(filepath.Dir(path))
		if err != nil {
			return err
		}
		runtimes = append(runtimes, value)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(runtimes, func(i, j int) bool {
		left, right := runtimes[i].Metadata, runtimes[j].Metadata
		if left.FluxaVersion != right.FluxaVersion {
			return left.FluxaVersion < right.FluxaVersion
		}
		leftTarget := targetName(left.OS, left.Arch) + "/" + terminalName(left.Terminal)
		rightTarget := targetName(right.OS, right.Arch) + "/" + terminalName(right.Terminal)
		return leftTarget < rightTarget
	})
	return runtimes, nil
}

// Resolve selects exactly one compatible verified runtime.
func Resolve(root string, requirement Requirement) (Runtime, error) {
	values, err := List(root)
	if err != nil {
		return Runtime{}, err
	}
	var compatible []Runtime
	for _, value := range values {
		if compatibleRuntime(value.Metadata, requirement) {
			compatible = append(compatible, value)
		}
	}
	if len(compatible) == 0 {
		kind := ErrorNotFound
		if len(values) > 0 {
			kind = ErrorIncompatible
		}
		return Runtime{}, runtimeError(kind, "resolve", root, fmt.Errorf("no verified runtime matches %s/%s format %s", requirement.OS, requirement.Arch, requirement.ProgramFormat))
	}
	if len(compatible) > 1 {
		return Runtime{}, runtimeError(ErrorInvalid, "resolve", root, errors.New("multiple compatible runtimes found"))
	}
	return compatible[0], nil
}

func compatibleRuntime(metadata Metadata, requirement Requirement) bool {
	if metadata.OS != requirement.OS || metadata.Arch != requirement.Arch ||
		metadata.Terminal != requirement.Terminal ||
		metadata.PackageFormatVersion != requirement.PackageFormatVersion ||
		metadata.LibrariesSHA256 != requirement.LibrariesSHA256 ||
		metadata.BytecodeVersion != requirement.BytecodeVersion ||
		metadata.BytecodeABI != requirement.BytecodeABI ||
		!contains(metadata.ProgramFormats, requirement.ProgramFormat) {
		return false
	}
	if requirement.FluxaVersion != "" {
		return metadata.FluxaVersion == requirement.FluxaVersion
	}
	return metadata.ToolchainSHA256 == requirement.ToolchainSHA256
}

func ensureRegistryRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", runtimeError(ErrorIO, "resolve registry", root, err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return "", runtimeError(ErrorIO, "create registry", absolute, err)
	}
	info, err := os.Lstat(absolute)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", runtimeError(ErrorInvalid, "validate registry", absolute, errors.New("registry root must be a non-symlink directory"))
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", runtimeError(ErrorInvalid, "validate registry", absolute, errors.New("registry path cannot be resolved"))
	}
	// The root itself must be a real directory (checked with Lstat above), but
	// an ancestor may be a system alias. macOS exposes /var through
	// /private/var, including the directory used by testing.TempDir.
	return filepath.Clean(resolved), nil
}

func ensureRegistryDirectory(path string) error {
	err := os.Mkdir(path, 0o700)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrExist) {
		return runtimeError(ErrorIO, "create registry directory", path, err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return runtimeError(ErrorIO, "inspect registry directory", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return runtimeError(ErrorInvalid, "validate registry directory", path, errors.New("must be a non-symlink directory"))
	}
	return nil
}

func hashRuntimeFile(path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 -- runtime binary is hashed before trust.
	if err != nil {
		return "", runtimeError(ErrorIO, "open binary", path, err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", runtimeError(ErrorIO, "hash binary", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyRuntimeFile(source, destination string, executable bool) error {
	input, err := os.Open(source) // #nosec G304 -- caller-selected source already validated.
	if err != nil {
		return runtimeError(ErrorIO, "open source binary", source, err)
	}
	defer func() { _ = input.Close() }()
	mode := fs.FileMode(0o600)
	if executable {
		mode = 0o700
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode) // #nosec G304 -- confined registry destination.
	if err != nil {
		return runtimeError(ErrorIO, "create runtime binary", destination, err)
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		return runtimeError(ErrorIO, "copy runtime binary", destination, errors.Join(copyErr, syncErr, closeErr))
	}
	return nil
}

func targetName(osName, arch string) string {
	if arch == "amd64" {
		arch = "x64"
	}
	return osName + "-" + arch
}

func terminalName(terminal bool) string {
	if terminal {
		return "terminal"
	}
	return "windowed"
}
