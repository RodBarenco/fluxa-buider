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

	linuxpkg "github.com/RodBarenco/fluxa-builder/internal/linux"
	macospkg "github.com/RodBarenco/fluxa-builder/internal/macos"
	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
	runtimepkg "github.com/RodBarenco/fluxa-builder/internal/runtime"
	windowspkg "github.com/RodBarenco/fluxa-builder/internal/windows"
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
	LauncherPath  string
	SourceExposed bool
	SignaturePath string
	SignatureHash string
	SigningKeyID  string
	WindowsIcon   string
	LinuxIcon     string
	MacOSIcon     string
	BundleID      string
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
	ExtraFiles  []string
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
	WindowsIcon   string `json:"windows_icon,omitempty"`
	WindowsInfo   string `json:"windows_metadata,omitempty"`
	LinuxIcon     string `json:"linux_icon,omitempty"`
	LinuxInfo     string `json:"linux_metadata,omitempty"`
}

type windowsInfo struct {
	FormatVersion int    `json:"format_version"`
	ProductName   string `json:"product_name"`
	ProjectID     string `json:"project_id"`
	FileVersion   string `json:"file_version"`
	Architecture  string `json:"architecture"`
	Terminal      bool   `json:"terminal"`
	Executable    string `json:"executable"`
	Package       string `json:"package"`
	RuntimeHash   string `json:"runtime_sha256"`
	PackageHash   string `json:"package_sha256"`
	Icon          string `json:"icon,omitempty"`
	IconHash      string `json:"icon_sha256,omitempty"`
}

type linuxInfo struct {
	FormatVersion int    `json:"format_version"`
	ProductName   string `json:"product_name"`
	ProjectID     string `json:"project_id"`
	Version       string `json:"version"`
	Architecture  string `json:"architecture"`
	Executable    string `json:"executable"`
	Package       string `json:"package"`
	RuntimeHash   string `json:"runtime_sha256"`
	PackageHash   string `json:"package_sha256"`
	Icon          string `json:"icon,omitempty"`
	IconHash      string `json:"icon_sha256,omitempty"`
	DataPolicy    string `json:"data_policy"`
	LibcPolicy    string `json:"libc_policy"`
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
	if request.TargetOS == "macos" {
		return buildMacOS(ctx, request)
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
	runtimeHash := ""
	privateRuntimePath := ""
	if request.LauncherPath == "" {
		runtimeHash, err = copyAndHash(ctx, request.Runtime.BinaryPath, executablePath, executableMode)
		if err != nil {
			return Result{}, err
		}
	} else {
		if _, err = copyAndHash(ctx, request.LauncherPath, executablePath, executableMode); err != nil {
			return Result{}, portableError(ErrorIO, "copy application launcher", request.LauncherPath, err)
		}
		privateRuntimeName := ".fluxa-runtime"
		if request.TargetOS == "windows" {
			privateRuntimeName += ".exe"
		}
		privateRuntimePath = filepath.Join(directory, privateRuntimeName)
		runtimeMode := os.FileMode(0o700)
		if request.TargetOS == "windows" {
			runtimeMode = 0o600
		}
		runtimeHash, err = copyAndHash(ctx, request.Runtime.BinaryPath, privateRuntimePath, runtimeMode)
		if err != nil {
			return Result{}, err
		}
	}
	if runtimeHash != request.Runtime.Metadata.BinarySHA256 {
		return Result{}, portableError(ErrorIntegrity, "verify copied runtime", request.Runtime.BinaryPath, errors.New("runtime SHA-256 mismatch"))
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
	if goruntime.GOOS != "windows" && request.TargetOS != "windows" {
		info, err := os.Stat(executablePath)
		if err != nil || info.Mode().Perm()&0o111 == 0 {
			return Result{}, portableError(ErrorPermission, "verify executable permission", executablePath, errors.New("runtime is not executable"))
		}
	}

	extraFiles := make([]string, 0, 2)
	if privateRuntimePath != "" {
		extraFiles = append(extraFiles, privateRuntimePath)
	}
	windowsIconName := ""
	windowsInfoName := ""
	linuxIconName := ""
	linuxInfoName := ""
	if request.TargetOS == "windows" {
		iconHash := ""
		if request.WindowsIcon != "" {
			if err := windowspkg.ValidateICO(request.WindowsIcon); err != nil {
				return Result{}, portableError(ErrorInvalid, "validate Windows icon", request.WindowsIcon, err)
			}
			windowsIconName = name + ".ico"
			iconPath := filepath.Join(directory, windowsIconName)
			iconHash, err = copyAndHash(ctx, request.WindowsIcon, iconPath, 0o600)
			if err != nil {
				return Result{}, err
			}
			if err := windowspkg.ValidateICO(iconPath); err != nil {
				return Result{}, portableError(ErrorIntegrity, "verify copied Windows icon", iconPath, err)
			}
			extraFiles = append(extraFiles, iconPath)
		}
		windowsInfoName = "windows-version.json"
		windowsInfoPath := filepath.Join(directory, windowsInfoName)
		if err := writeJSONFile(windowsInfoPath, windowsInfo{
			FormatVersion: 1, ProductName: request.ProjectName, ProjectID: request.ProjectID,
			FileVersion: request.Version, Architecture: request.TargetArch, Terminal: request.Terminal,
			Executable: executableName, Package: packageName,
			RuntimeHash: runtimeHash, PackageHash: packageHash,
			Icon: windowsIconName, IconHash: iconHash,
		}); err != nil {
			return Result{}, err
		}
		extraFiles = append(extraFiles, windowsInfoPath)
	}
	if request.TargetOS == "linux" {
		iconHash := ""
		if request.LinuxIcon != "" {
			if err := linuxpkg.ValidatePNG(request.LinuxIcon); err != nil {
				return Result{}, portableError(ErrorInvalid, "validate Linux icon", request.LinuxIcon, err)
			}
			linuxIconName = name + ".png"
			iconPath := filepath.Join(directory, linuxIconName)
			iconHash, err = copyAndHash(ctx, request.LinuxIcon, iconPath, 0o600)
			if err != nil {
				return Result{}, err
			}
			if err := linuxpkg.ValidatePNG(iconPath); err != nil {
				return Result{}, portableError(ErrorIntegrity, "verify copied Linux icon", iconPath, err)
			}
			extraFiles = append(extraFiles, iconPath)
		}
		linuxInfoName = "linux-runtime.json"
		linuxInfoPath := filepath.Join(directory, linuxInfoName)
		if err := writeJSONFile(linuxInfoPath, linuxInfo{
			FormatVersion: 1, ProductName: request.ProjectName, ProjectID: request.ProjectID,
			Version: request.Version, Architecture: request.TargetArch,
			Executable: executableName, Package: packageName,
			RuntimeHash: runtimeHash, PackageHash: packageHash,
			Icon: linuxIconName, IconHash: iconHash,
			DataPolicy: "xdg", LibcPolicy: "runtime-defined",
		}); err != nil {
			return Result{}, err
		}
		extraFiles = append(extraFiles, linuxInfoPath)
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
		SigningKeyID: request.SigningKeyID, WindowsIcon: windowsIconName,
		WindowsInfo: windowsInfoName, LinuxIcon: linuxIconName, LinuxInfo: linuxInfoName,
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
	expectedEntries += len(extraFiles)
	if len(entries) != expectedEntries {
		return Result{}, portableError(ErrorInvalid, "validate directory", directory, fmt.Errorf("contains %d entries, expected %d", len(entries), expectedEntries))
	}
	complete = true
	return Result{
		Directory: directory, Name: name, TargetOS: request.TargetOS, Executable: executablePath,
		Package: packagePath, BuildInfo: infoPath, Signature: signaturePath,
		PackageHash: packageHash, RuntimeHash: runtimeHash,
		ExtraFiles: append([]string(nil), extraFiles...),
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
	if goruntime.GOOS == "windows" && osName == "windows" {
		if err := windowspkg.ValidatePEAMD64(value.BinaryPath); err != nil {
			return portableError(ErrorInvalid, "validate Windows runtime PE", value.BinaryPath, err)
		}
	}
	// Registry runtimes always carry the current metadata version. A zero
	// version is reserved for lightweight internal test doubles.
	if goruntime.GOOS == "linux" && osName == "linux" &&
		value.Metadata.FormatVersion == runtimepkg.CurrentMetadataVersion {
		if arch != "amd64" {
			return portableError(ErrorInvalid, "validate Linux runtime ELF", value.BinaryPath, errors.New("official Linux target currently supports x64 only"))
		}
		if err := linuxpkg.ValidateELFAMD64(value.BinaryPath); err != nil {
			return portableError(ErrorInvalid, "validate Linux runtime ELF", value.BinaryPath, err)
		}
	}
	if goruntime.GOOS == "darwin" && osName == "macos" &&
		value.Metadata.FormatVersion == runtimepkg.CurrentMetadataVersion {
		if err := macospkg.ValidateMachO(value.BinaryPath, arch); err != nil {
			return portableError(ErrorInvalid, "validate macOS runtime Mach-O", value.BinaryPath, err)
		}
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
	return writeJSONFile(path, value)
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return portableError(ErrorInvalid, "encode metadata", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil { // #nosec G304 -- confined output path.
		return portableError(ErrorIO, "write metadata", path, err)
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
