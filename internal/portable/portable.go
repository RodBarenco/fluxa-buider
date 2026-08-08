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
	"github.com/RodBarenco/fluxa-builder/internal/mesafallback"
	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
	runtimepkg "github.com/RodBarenco/fluxa-builder/internal/runtime"
	windowspkg "github.com/RodBarenco/fluxa-builder/internal/windows"
	"github.com/RodBarenco/fluxa-builder/internal/wrapper"
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
	// WindowsMesaFallbackDir, when set, is a local directory already
	// populated with the Mesa3D Windows software-OpenGL fallback DLLs
	// (internal/mesafallback.EnsureCached's job, not this package's —
	// Build stays a pure, no-network function and only copies from an
	// already-ready local source, the same as WindowsIcon/LinuxIcon).
	// Bundling it is always attempted for a Windows target when set, but
	// failure only produces a Warning, never a build failure: it is a
	// documented, optional compatibility enhancement (fluxa-lang's own
	// docs/WINDOWS.md), not a functional requirement.
	WindowsMesaFallbackDir string
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
	// Warnings are non-fatal, expected degradations the caller should
	// surface (e.g. Windows icon embedding skipped because the target PE
	// had no header room for it) — the build still succeeded and
	// published normally.
	Warnings []string
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

// LinuxInfo mirrors linux-runtime.json's schema. It is exported so the
// installed launcher itself (internal/app.RunInstalled) can read back its
// own project name, icon, and terminal mode at run time — it needs them to
// (re)register its own desktop entry on every launch, since a portable
// directory has no package-manager install step of its own to hook into.
// See ReadLinuxInfo and docs/adr/0026-file-manager-icon-association.md.
type LinuxInfo struct {
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
	Terminal      bool   `json:"terminal"`
}

// maxLinuxInfoSize bounds ReadLinuxInfo's input: linux-runtime.json is a
// small, Builder-generated metadata file, never anywhere close to this.
const maxLinuxInfoSize = 64 * 1024

// ReadLinuxInfo reads and parses a linux-runtime.json written by Build. The
// file is Builder's own prior output, not arbitrary untrusted input, but
// this still fails closed on anything that isn't a small, regular,
// well-formed JSON file rather than trusting it blindly.
func ReadLinuxInfo(path string) (LinuxInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return LinuxInfo{}, portableError(ErrorIO, "inspect linux-runtime.json", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxLinuxInfoSize {
		return LinuxInfo{}, portableError(ErrorInvalid, "validate linux-runtime.json", path, errors.New("must be a small, non-symlink regular file"))
	}
	data, err := os.ReadFile(path) // #nosec G304 -- caller-selected path beside the running launcher.
	if err != nil {
		return LinuxInfo{}, portableError(ErrorIO, "read linux-runtime.json", path, err)
	}
	var value LinuxInfo
	if err := json.Unmarshal(data, &value); err != nil {
		return LinuxInfo{}, portableError(ErrorInvalid, "parse linux-runtime.json", path, err)
	}
	return value, nil
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
	var runtimeExtraFiles []string
	if request.LauncherPath == "" {
		runtimeHash, err = copyAndHash(ctx, request.Runtime.BinaryPath, executablePath, executableMode)
		if err != nil {
			return Result{}, err
		}
	} else {
		if _, err = copyAndHash(ctx, request.LauncherPath, executablePath, executableMode); err != nil {
			return Result{}, portableError(ErrorIO, "copy application launcher", request.LauncherPath, err)
		}
		if request.TargetOS == "windows" {
			if err := windowspkg.ConfigureTerminal(executablePath, request.Terminal); err != nil {
				return Result{}, portableError(ErrorInvalid, "configure Windows terminal mode", executablePath, err)
			}
		}
		if request.TargetOS == "linux" {
			var privateRuntimePath, interpreterPath string
			privateRuntimePath, interpreterPath, runtimeHash, err = assembleLinuxRuntime(ctx, directory, request.Runtime)
			if err != nil {
				return Result{}, err
			}
			runtimeExtraFiles = []string{privateRuntimePath, interpreterPath}
		} else {
			privateRuntimeName := ".fluxa-runtime"
			if request.TargetOS == "windows" {
				privateRuntimeName += ".exe"
			}
			privateRuntimePath := filepath.Join(directory, privateRuntimeName)
			runtimeMode := os.FileMode(0o700)
			if request.TargetOS == "windows" {
				runtimeMode = 0o600
			}
			runtimeHash, err = copyAndHash(ctx, request.Runtime.BinaryPath, privateRuntimePath, runtimeMode)
			if err != nil {
				return Result{}, err
			}
			runtimeExtraFiles = []string{privateRuntimePath}
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

	extraFiles := make([]string, 0, 2+len(runtimeExtraFiles))
	extraFiles = append(extraFiles, runtimeExtraFiles...)
	var warnings []string
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

			// Best-effort: associate the icon with the executable itself
			// (Explorer/taskbar), on top of the loose .ico file already
			// shipped above. Only attempted when executablePath is a real
			// application launcher copy (request.LauncherPath != "") — the
			// same precondition ConfigureTerminal already requires above,
			// since that is the only branch where executablePath is
			// guaranteed to be a genuine PE (ConfigureTerminal's own
			// ValidatePEAMD64 already ran against it). When there is no
			// launcher, executablePath is the raw runtime binary itself, a
			// simpler publication mode that already skips other PE-specific
			// finalization for the same reason.
			//
			// A PE that has no header room for another section, or that
			// already carries embedded resources, is a known, expected
			// condition — not a build failure — so it degrades to a printed
			// warning instead of aborting publication.
			// See docs/adr/0026-file-manager-icon-association.md.
			if request.LauncherPath != "" {
				if err := windowspkg.EmbedIcon(executablePath, iconPath); err != nil {
					var windowsErr *windowspkg.Error
					if errors.As(err, &windowsErr) && windowsErr.Kind == windowspkg.ErrorUnsupported {
						warnings = append(warnings, fmt.Sprintf("Windows icon was not embedded in the executable (shipped as %s instead): %v", windowsIconName, err))
					} else {
						return Result{}, portableError(ErrorIO, "embed Windows icon", executablePath, err)
					}
				}
			}
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

		if request.WindowsMesaFallbackDir != "" {
			mesaFiles, mesaErr := bundleWindowsMesaFallback(request.WindowsMesaFallbackDir, directory, executableName)
			if mesaErr != nil {
				warnings = append(warnings, fmt.Sprintf("Mesa3D software-rendering fallback was not bundled: %v", mesaErr))
			} else {
				extraFiles = append(extraFiles, mesaFiles...)
			}
		}
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
		if err := writeJSONFile(linuxInfoPath, LinuxInfo{
			FormatVersion: 1, ProductName: request.ProjectName, ProjectID: request.ProjectID,
			Version: request.Version, Architecture: request.TargetArch,
			Executable: executableName, Package: packageName,
			RuntimeHash: runtimeHash, PackageHash: packageHash,
			Icon: linuxIconName, IconHash: iconHash,
			DataPolicy: "xdg", LibcPolicy: "runtime-defined",
			Terminal: request.Terminal,
		}); err != nil {
			return Result{}, err
		}
		extraFiles = append(extraFiles, linuxInfoPath)

		scriptPath, err := writeDesktopInstallScript(directory, request.ProjectID, request.ProjectName, executableName, linuxIconName, request.Terminal)
		if err != nil {
			return Result{}, err
		}
		extraFiles = append(extraFiles, scriptPath)
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
		Warnings:   append([]string(nil), warnings...),
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

// assembleLinuxRuntime writes the embedded Linux "adapted runtime" relay as
// .fluxa-runtime and copies the verified interpreter beside it as
// .fluxa-runtime.interpreter. The native Linux Fluxa interpreter has no
// private launcher protocol of its own (unlike the Windows
// FLUXA_PACKAGED_RUNTIME entrypoint), so the relay is what actually
// receives internal/runner.go's private call and translates it into the
// interpreter's already-working `run <entry> -proj .` command. See
// docs/adr/0025-linux-adapted-runtime-wrapper.md.
// writeDesktopInstallScript writes a POSIX shell script that registers the
// application in the current user's application menu, so a file manager
// (and the taskbar/launcher) shows the configured icon. It does not write a
// .desktop file directly: a .desktop file's Exec/Icon fields need an
// absolute path, and this portable directory's final location on disk is
// not known at build time (nothing else this Builder generates —
// build-info.json, linux-runtime.json, the manifest — embeds an
// absolute or build-machine-specific path either, and this keeps that
// property). The script resolves its own location at run time instead
// (`dirname "$0"`) and is safe to re-run.
func writeDesktopInstallScript(directory, projectID, projectName, executableName, iconName string, terminal bool) (string, error) {
	desktopName := projectID
	if desktopName == "" {
		desktopName = executableName
	}
	name := strings.ReplaceAll(projectName, "\n", " ")
	iconLine := ""
	if iconName != "" {
		iconLine = "Icon=$here/" + iconName + "\n"
	}
	script := fmt.Sprintf(
		"#!/bin/sh\n"+
			"# Registers %q in this user's application menu. Safe to re-run.\n"+
			"set -e\n"+
			"here=\"$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)\"\n"+
			"target=\"$HOME/.local/share/applications\"\n"+
			"mkdir -p \"$target\"\n"+
			"cat > \"$target/%s.desktop\" <<DESKTOP_EOF\n"+
			"[Desktop Entry]\n"+
			"Type=Application\n"+
			"Name=%s\n"+
			"Exec=$here/%s\n"+
			"%sTerminal=%t\n"+
			"Categories=Utility;\n"+
			"DESKTOP_EOF\n"+
			"echo \"Installed: $target/%s.desktop\"\n",
		name, desktopName, name, executableName, iconLine, terminal, desktopName,
	)
	path := filepath.Join(directory, "install-desktop-shortcut.sh")
	if err := writeBytesExclusive(path, []byte(script), 0o755); err != nil { // #nosec G306 -- script needs the execute bit; content has no secrets.
		return "", portableError(ErrorIO, "write desktop install script", path, err)
	}
	return path, nil
}

func assembleLinuxRuntime(ctx context.Context, directory string, runtime runtimepkg.Runtime) (privateRuntimePath, interpreterPath, runtimeHash string, err error) {
	privateRuntimePath = filepath.Join(directory, ".fluxa-runtime")
	if err := writeBytesExclusive(privateRuntimePath, wrapper.LinuxAMD64, 0o700); err != nil {
		return "", "", "", portableError(ErrorIO, "write Linux runtime relay", privateRuntimePath, err)
	}
	interpreterPath = filepath.Join(directory, ".fluxa-runtime.interpreter")
	runtimeHash, err = copyAndHash(ctx, runtime.BinaryPath, interpreterPath, 0o700)
	if err != nil {
		return "", "", "", err
	}
	return privateRuntimePath, interpreterPath, runtimeHash, nil
}

func writeBytesExclusive(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode) // #nosec G304 -- confined output path.
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		return err
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

// bundleWindowsMesaFallback copies the Mesa3D Windows software-OpenGL
// fallback DLLs (already cached locally by internal/mesafallback) beside
// the executable, plus a "<executable>.local" marker file that enables
// per-application DLL redirection so Windows loads these copies instead
// of the system opengl32.dll — see
// docs/adr/0027-automatic-toolchain-acquisition.md and fluxa-lang's own
// docs/WINDOWS.md, which documents this exact mechanism and file set.
func bundleWindowsMesaFallback(sourceDir, directory, executableName string) ([]string, error) {
	files := make([]string, 0, len(mesafallback.DLLNames)+1)
	for _, name := range mesafallback.DLLNames {
		destination := filepath.Join(directory, name)
		if _, err := copyAndHash(context.Background(), filepath.Join(sourceDir, name), destination, 0o600); err != nil {
			return nil, err
		}
		files = append(files, destination)
	}
	localMarkerPath := filepath.Join(directory, executableName+".local")
	if err := os.WriteFile(localMarkerPath, []byte("Enables application-local Mesa3D DLL redirection.\n"), 0o600); err != nil { // #nosec G304 -- confined output path.
		return nil, portableError(ErrorIO, "write Mesa DLL redirection marker", localMarkerPath, err)
	}
	files = append(files, localMarkerPath)
	return files, nil
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
