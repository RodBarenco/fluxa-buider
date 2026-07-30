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
)

func buildMacOS(ctx context.Context, request Request) (Result, error) {
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
	runtimeHash, err := copyAndHash(ctx, request.Runtime.BinaryPath, executable, 0o700)
	if err != nil {
		return Result{}, err
	}
	if runtimeHash != request.Runtime.Metadata.BinarySHA256 {
		return Result{}, portableError(ErrorIntegrity, "verify copied macOS runtime", executable, errors.New("runtime SHA-256 mismatch"))
	}
	if request.Runtime.Metadata.FormatVersion != 0 {
		if err := macospkg.ValidateMachO(executable, request.TargetArch); err != nil {
			return Result{}, portableError(ErrorIntegrity, "verify copied macOS runtime", executable, err)
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

	extra := make([]string, 0, 2)
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
