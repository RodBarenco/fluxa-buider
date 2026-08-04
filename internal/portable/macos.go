package portable

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	macospkg "github.com/RodBarenco/fluxa-builder/internal/macos"
	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
	"github.com/RodBarenco/fluxa-builder/internal/wrapper"
)

func buildMacOS(ctx context.Context, request Request) (Result, error) {
	if request.LauncherPath == "" {
		return Result{}, portableError(ErrorInvalid, "validate macOS launcher", "",
			errors.New("integrated application launcher is required"))
	}
	name := artifactName(request.ProjectName, request.ProjectID)
	bundleName := name + ".app"
	bundle := filepath.Join(request.OutputRoot, bundleName)
	contents := filepath.Join(bundle, "Contents")
	macOSDir := filepath.Join(contents, "MacOS")
	resources := filepath.Join(contents, "Resources")
	for _, directory := range []string{bundle, contents, macOSDir, resources} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			_ = os.RemoveAll(bundle)
			return Result{}, portableError(ErrorIO, "create macOS bundle directory", directory, err)
		}
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(bundle)
		}
	}()

	executable := filepath.Join(macOSDir, name)
	if _, err := copyAndHash(ctx, request.LauncherPath, executable, 0o700); err != nil {
		return Result{}, portableError(ErrorIO, "copy macOS application launcher", request.LauncherPath, err)
	}
	if request.Runtime.Metadata.FormatVersion != 0 {
		if err := macospkg.ValidateMachO(executable, request.TargetArch); err != nil {
			return Result{}, portableError(ErrorIntegrity, "verify copied macOS launcher", executable, err)
		}
	}

	// The native macOS Fluxa interpreter has no private launcher protocol of
	// its own, same as Linux (see docs/adr/0025-linux-adapted-runtime-wrapper.md):
	// .fluxa-runtime is the embedded relay, and the verified interpreter is
	// placed beside it as .fluxa-runtime.interpreter.
	privateRuntime := filepath.Join(macOSDir, ".fluxa-runtime")
	interpreterPath := filepath.Join(macOSDir, ".fluxa-runtime.interpreter")
	relayBinary, err := macOSWrapperBinary(request.TargetArch)
	if err != nil {
		return Result{}, portableError(ErrorInvalid, "select macOS runtime relay", request.TargetArch, err)
	}
	if err := writeBytesExclusive(privateRuntime, relayBinary, 0o700); err != nil {
		return Result{}, portableError(ErrorIO, "write macOS runtime relay", privateRuntime, err)
	}
	runtimeHash, err := copyAndHash(ctx, request.Runtime.BinaryPath, interpreterPath, 0o700)
	if err != nil {
		return Result{}, err
	}
	if runtimeHash != request.Runtime.Metadata.BinarySHA256 {
		return Result{}, portableError(ErrorIntegrity, "verify copied macOS runtime", interpreterPath, errors.New("runtime SHA-256 mismatch"))
	}
	if request.Runtime.Metadata.FormatVersion != 0 {
		if err := macospkg.ValidateMachO(interpreterPath, request.TargetArch); err != nil {
			return Result{}, portableError(ErrorIntegrity, "verify copied macOS runtime", interpreterPath, err)
		}
	}

	packagePath := filepath.Join(resources, name+".flxpkg")
	packageHash, err := copyAndHash(ctx, request.PackagePath, packagePath, 0o600)
	if err != nil {
		return Result{}, err
	}
	if packageHash != request.PackageSHA256 {
		return Result{}, portableError(ErrorIntegrity, "verify copied macOS package", packagePath, errors.New("package SHA-256 mismatch"))
	}
	if _, err := flxpkg.Verify(packagePath); err != nil {
		return Result{}, portableError(ErrorIntegrity, "verify copied macOS package", packagePath, err)
	}

	extra := []string{privateRuntime, interpreterPath}
	iconName := ""
	if request.MacOSIcon != "" {
		if err := macospkg.ValidateICNS(request.MacOSIcon); err != nil {
			return Result{}, portableError(ErrorInvalid, "validate macOS icon", request.MacOSIcon, err)
		}
		iconName = "AppIcon.icns"
		iconPath := filepath.Join(resources, iconName)
		if _, err := copyAndHash(ctx, request.MacOSIcon, iconPath, 0o600); err != nil {
			return Result{}, err
		}
		if err := macospkg.ValidateICNS(iconPath); err != nil {
			return Result{}, portableError(ErrorIntegrity, "verify copied macOS icon", iconPath, err)
		}
		extra = append(extra, iconPath)
	}

	signaturePath := ""
	signatureName := ""
	if request.SignaturePath != "" {
		if request.SignatureHash == "" || request.SigningKeyID == "" {
			return Result{}, portableError(ErrorInvalid, "validate macOS signature metadata", request.SignaturePath,
				errors.New("signature hash and key ID are required"))
		}
		signatureName = name + ".flxpkg.sig"
		signaturePath = filepath.Join(resources, signatureName)
		hash, err := copyAndHash(ctx, request.SignaturePath, signaturePath, 0o600)
		if err != nil {
			return Result{}, err
		}
		if hash != request.SignatureHash {
			return Result{}, portableError(ErrorIntegrity, "verify copied macOS signature", signaturePath, errors.New("signature SHA-256 mismatch"))
		}
	}

	bundleID := request.BundleID
	if bundleID == "" {
		bundleID = request.ProjectID
	}
	infoPath := filepath.Join(contents, "Info.plist")
	if err := writeInfoPlist(infoPath, plistInfo{
		Name: request.ProjectName, BundleID: bundleID, Version: request.Version,
		Executable: name, Icon: iconName,
	}); err != nil {
		return Result{}, err
	}
	buildInfoPath := filepath.Join(resources, "build-info.json")
	if err := writeBuildInfo(buildInfoPath, buildInfo{
		FormatVersion: 1, Name: request.ProjectName, ProjectID: request.ProjectID,
		Version: request.Version, OS: "macos", Arch: request.TargetArch,
		Terminal: request.Terminal, Executable: filepath.ToSlash(filepath.Join("Contents", "MacOS", name)),
		Package:       filepath.ToSlash(filepath.Join("Contents", "Resources", name+".flxpkg")),
		PackageSHA256: packageHash, RuntimeSHA256: runtimeHash,
		SourceExposed: request.SourceExposed, Signature: signatureName,
		SignatureHash: request.SignatureHash, SigningKeyID: request.SigningKeyID,
	}); err != nil {
		return Result{}, err
	}
	extra = append(extra, infoPath)
	complete = true
	return Result{
		Directory: bundle, Name: bundleName, TargetOS: "macos",
		Executable: executable, Package: packagePath, BuildInfo: buildInfoPath,
		Signature: signaturePath, PackageHash: packageHash, RuntimeHash: runtimeHash,
		ExtraFiles: extra,
	}, nil
}

type plistInfo struct {
	Name       string
	BundleID   string
	Version    string
	Executable string
	Icon       string
}

// macOSWrapperBinary selects the embedded relay matching the target
// architecture. Both are cross-compiled from the same source as the Linux
// relay; see internal/wrapper.
func macOSWrapperBinary(arch string) ([]byte, error) {
	switch arch {
	case "amd64":
		return wrapper.DarwinAMD64, nil
	case "arm64":
		return wrapper.DarwinARM64, nil
	default:
		return nil, fmt.Errorf("unsupported macOS architecture %q", arch)
	}
}

func writeInfoPlist(path string, value plistInfo) error {
	escape := func(text string) string {
		var buffer bytes.Buffer
		_ = xml.EscapeText(&buffer, []byte(text))
		return buffer.String()
	}
	icon := ""
	if value.Icon != "" {
		icon = "\n  <key>CFBundleIconFile</key><string>" + escape(value.Icon) + "</string>"
	}
	data := []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>%s</string>
  <key>CFBundleDisplayName</key><string>%s</string>
  <key>CFBundleIdentifier</key><string>%s</string>
  <key>CFBundleExecutable</key><string>%s</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>%s</string>
  <key>CFBundleVersion</key><string>%s</string>%s
</dict>
</plist>
`, escape(value.Name), escape(value.Name), escape(value.BundleID), escape(value.Executable),
		escape(value.Version), escape(value.Version), icon))
	if err := os.WriteFile(path, data, 0o600); err != nil { // #nosec G304 -- confined bundle path.
		return portableError(ErrorIO, "write Info.plist", path, err)
	}
	return nil
}
