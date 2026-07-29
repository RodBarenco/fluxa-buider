package portable

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"unicode"

	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
	runtimepkg "github.com/RodBarenco/fluxa-builder/internal/runtime"
)

// Request contains verified inputs for one portable directory.
type Request struct {
	OutputRoot    string
	ProjectName   string
	ProjectID     string
	Version       string
	TargetOS      string
	TargetArch    string
	Terminal      bool
	PackagePath   string
	PackageSHA256 string
	Runtime       runtimepkg.Runtime
	SourceExposed bool
	SignaturePath string
	SignatureHash string
	SigningKeyID  string
}

// Result describes a staged portable directory.
type Result struct {
	Directory   string
	Name        string
	TargetOS    string
	Executable  string
	Package     string
	BuildInfo   string
	Signature   string
	PackageHash string
	RuntimeHash string
}

type buildInfo struct {
	FormatVersion int    `json:"format_version"`
	Name          string `json:"name"`
	ProjectID     string `json:"project_id"`
	Version       string `json:"version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	Terminal      bool   `json:"terminal"`
	Executable    string `json:"executable"`
	Package       string `json:"package"`
	PackageSHA256 string `json:"package_sha256"`
	RuntimeSHA256 string `json:"runtime_sha256"`
	SourceExposed bool   `json:"source_exposed"`
	Signature     string `json:"signature,omitempty"`
	SignatureHash string `json:"signature_sha256,omitempty"`
	SigningKeyID  string `json:"signing_key_id,omitempty"`
}

// Build assembles and verifies a private portable directory.
func Build(ctx context.Context, request Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, portableError(ErrorCanceled, "build", "", err)
	}
	if request.OutputRoot == "" || request.ProjectName == "" || request.ProjectID == "" ||
		request.Version == "" || request.TargetOS == "" || request.TargetArch == "" {
		return Result{}, portableError(ErrorInvalid, "validate request", "", errors.New("project, target, and output fields are required"))
	}
	rootInfo, err := os.Lstat(request.OutputRoot)
	if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return Result{}, portableError(ErrorInvalid, "validate output root", request.OutputRoot, errors.New("must be an existing non-symlink directory"))
	}
	packageInfo, err := flxpkg.Verify(request.PackagePath)
	if err != nil {
		return Result{}, portableError(ErrorIntegrity, "verify package", request.PackagePath, err)
	}
	if packageInfo.SHA256 != request.PackageSHA256 {
		return Result{}, portableError(ErrorIntegrity, "verify package identity", request.PackagePath, errors.New("package hash differs from build result"))
	}
	if err := validateRuntime(request.Runtime, request.TargetOS, request.TargetArch, request.Terminal); err != nil {
		return Result{}, err
	}

	name := artifactName(request.ProjectName, request.ProjectID)
	directory := filepath.Join(request.OutputRoot, name)
	if err := os.Mkdir(directory, 0o700); err != nil {
		return Result{}, portableError(ErrorIO, "create directory", directory, err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(directory)
		}
	}()

	executableName := name
	if request.TargetOS == "windows" {
		executableName += ".exe"
	}
	packageName := name + ".flxpkg"
	executablePath := filepath.Join(directory, executableName)
	packagePath := filepath.Join(directory, packageName)
	executableMode := os.FileMode(0o700)
	if request.TargetOS == "windows" {
		executableMode = 0o600
	}
	runtimeHash, err := copyAndHash(ctx, request.Runtime.BinaryPath, executablePath, executableMode)
	if err != nil {
		return Result{}, err
	}
	if runtimeHash != request.Runtime.Metadata.BinarySHA256 {
		return Result{}, portableError(ErrorIntegrity, "verify copied runtime", executablePath, errors.New("runtime SHA-256 mismatch"))
	}
	packageHash, err := copyAndHash(ctx, request.PackagePath, packagePath, 0o600)
	if err != nil {
		return Result{}, err
	}
	if packageHash != request.PackageSHA256 {
		return Result{}, portableError(ErrorIntegrity, "verify copied package", packagePath, errors.New("package SHA-256 mismatch"))
	}
	if _, err := flxpkg.Verify(packagePath); err != nil {
		return Result{}, portableError(ErrorIntegrity, "verify copied package", packagePath, err)
	}
	if request.TargetOS != "windows" {
		info, err := os.Stat(executablePath)
		if err != nil || info.Mode().Perm()&0o111 == 0 {
			return Result{}, portableError(ErrorPermission, "verify executable permission", executablePath, errors.New("runtime is not executable"))
		}
	}

	infoPath := filepath.Join(directory, "build-info.json")
	signaturePath := ""
	signatureName := ""
	if request.SignaturePath != "" {
		if request.SignatureHash == "" || request.SigningKeyID == "" {
			return Result{}, portableError(ErrorInvalid, "validate signature metadata", request.SignaturePath, errors.New("signature hash and key ID are required"))
		}
		signaturePath = packagePath + ".sig"
		signatureName = filepath.Base(signaturePath)
		signatureHash, err := copyAndHash(ctx, request.SignaturePath, signaturePath, 0o600)
		if err != nil {
			return Result{}, err
		}
		if signatureHash != request.SignatureHash {
			return Result{}, portableError(ErrorIntegrity, "verify copied signature", signaturePath, errors.New("signature SHA-256 mismatch"))
		}
	}
	if err := writeBuildInfo(infoPath, buildInfo{
		FormatVersion: 1,
		Name:          request.ProjectName, ProjectID: request.ProjectID, Version: request.Version,
		OS: request.TargetOS, Arch: request.TargetArch, Terminal: request.Terminal,
		Executable: executableName, Package: packageName,
		PackageSHA256: packageHash, RuntimeSHA256: runtimeHash,
		SourceExposed: request.SourceExposed,
		Signature:     signatureName, SignatureHash: request.SignatureHash,
		SigningKeyID: request.SigningKeyID,
	}); err != nil {
		return Result{}, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return Result{}, portableError(ErrorIO, "inspect directory", directory, err)
	}
	expectedEntries := 3
	if signaturePath != "" {
		expectedEntries++
	}
	if len(entries) != expectedEntries {
		return Result{}, portableError(ErrorInvalid, "validate directory", directory, fmt.Errorf("contains %d entries, expected %d", len(entries), expectedEntries))
	}
	complete = true
	return Result{
		Directory: directory, Name: name, TargetOS: request.TargetOS, Executable: executablePath,
		Package: packagePath, BuildInfo: infoPath, Signature: signaturePath,
		PackageHash: packageHash, RuntimeHash: runtimeHash,
	}, nil
}

func validateRuntime(value runtimepkg.Runtime, osName, arch string, terminal bool) error {
	if value.BinaryPath == "" {
		return portableError(ErrorInvalid, "validate runtime", "", errors.New("runtime binary is required"))
	}
	if value.Metadata.OS != osName || value.Metadata.Arch != arch || value.Metadata.Terminal != terminal {
		return portableError(ErrorInvalid, "validate runtime", value.BinaryPath, errors.New("runtime target or terminal mode differs"))
	}
	info, err := os.Lstat(value.BinaryPath)
	if err != nil {
		return portableError(ErrorIO, "inspect runtime", value.BinaryPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return portableError(ErrorInvalid, "validate runtime", value.BinaryPath, errors.New("runtime must be a non-symlink regular file"))
	}
	if goruntime.GOOS != "windows" && osName != "windows" && info.Mode().Perm()&0o111 == 0 {
		return portableError(ErrorPermission, "validate runtime", value.BinaryPath, errors.New("runtime is not executable"))
	}
	return nil
}

func copyAndHash(ctx context.Context, sourcePath, destinationPath string, mode os.FileMode) (string, error) {
	source, err := os.Open(sourcePath) // #nosec G304 -- verified build input.
	if err != nil {
		return "", portableError(ErrorIO, "open source", sourcePath, err)
	}
	defer func() { _ = source.Close() }()
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode) // #nosec G304 -- confined output path.
	if err != nil {
		return "", portableError(ErrorIO, "create destination", destinationPath, err)
	}
	hash := sha256.New()
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			_ = destination.Close()
			return "", portableError(ErrorCanceled, "copy", sourcePath, err)
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			if _, err := destination.Write(buffer[:read]); err != nil {
				_ = destination.Close()
				return "", portableError(ErrorIO, "write destination", destinationPath, err)
			}
			_, _ = hash.Write(buffer[:read])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = destination.Close()
			return "", portableError(ErrorIO, "read source", sourcePath, readErr)
		}
	}
	syncErr := destination.Sync()
	closeErr := destination.Close()
	if syncErr != nil || closeErr != nil {
		return "", portableError(ErrorIO, "finish destination", destinationPath, errors.Join(syncErr, closeErr))
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeBuildInfo(path string, value buildInfo) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return portableError(ErrorInvalid, "encode build info", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil { // #nosec G304 -- confined output path.
		return portableError(ErrorIO, "write build info", path, err)
	}
	return nil
}

func artifactName(name, projectID string) string {
	var output strings.Builder
	dash := false
	for _, character := range strings.TrimSpace(name) {
		switch {
		case unicode.IsLetter(character) || unicode.IsDigit(character):
			if dash && output.Len() > 0 {
				output.WriteByte('-')
			}
			dash = false
			output.WriteRune(unicode.ToLower(character))
		case unicode.IsSpace(character) || character == '-' || character == '_':
			dash = true
		}
	}
	result := strings.Trim(output.String(), "-")
	if result == "" || windowsReservedName(result) {
		parts := strings.Split(projectID, ".")
		result = parts[len(parts)-1]
	}
	return result
}

// ArtifactName returns the deterministic filesystem name used for an application.
func ArtifactName(name, projectID string) string {
	return artifactName(name, projectID)
}

func windowsReservedName(value string) bool {
	switch strings.ToLower(value) {
	case "con", "prn", "aux", "nul",
		"com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
		"lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9":
		return true
	default:
		return false
	}
}

func portableError(kind ErrorKind, operation, path string, err error) *Error {
	return &Error{Kind: kind, Operation: operation, Path: path, Err: err}
}
