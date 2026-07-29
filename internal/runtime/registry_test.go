package runtimepkg_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"

	runtimepkg "github.com/RodBarenco/fluxa-builder/internal/runtime"
)

const (
	toolchainHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	librariesHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestAddListAndResolveVerifiedRuntime(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtimes")
	binary := writeRuntime(t, t.TempDir(), 0o700)
	metadata := validMetadata(t, binary)

	added, err := runtimepkg.Add(root, binary, metadata)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if added.Metadata.BinarySHA256 != metadata.BinarySHA256 ||
		filepath.Base(added.BinaryPath) != "fluxa-runtime" {
		t.Fatalf("added runtime = %#v", added)
	}

	values, err := runtimepkg.List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Directory != added.Directory {
		t.Fatalf("List() = %#v", values)
	}

	resolved, err := runtimepkg.Resolve(root, validRequirement())
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.BinaryPath != added.BinaryPath {
		t.Fatalf("resolved = %#v, want %#v", resolved, added)
	}

	if _, err := runtimepkg.Add(root, binary, metadata); err == nil {
		t.Fatal("Add() duplicate error = nil")
	}
}

func TestResolveMissingAndIncompatibleRuntime(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	_, err := runtimepkg.Resolve(root, validRequirement())
	assertRuntimeKind(t, err, runtimepkg.ErrorNotFound)

	binary := writeRuntime(t, t.TempDir(), 0o700)
	if _, err := runtimepkg.Add(root, binary, validMetadata(t, binary)); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		edit func(*runtimepkg.Requirement)
	}{
		{"target", func(value *runtimepkg.Requirement) { value.OS = "windows" }},
		{"version", func(value *runtimepkg.Requirement) { value.FluxaVersion = "9.9.9" }},
		{"package ABI", func(value *runtimepkg.Requirement) { value.PackageFormatVersion = 2 }},
		{"bytecode ABI", func(value *runtimepkg.Requirement) { value.BytecodeABI = "other" }},
		{"libraries", func(value *runtimepkg.Requirement) { value.LibrariesSHA256 = toolchainHash }},
		{"program format", func(value *runtimepkg.Requirement) { value.ProgramFormat = "fluxa-bytecode" }},
		{"terminal", func(value *runtimepkg.Requirement) { value.Terminal = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requirement := validRequirement()
			tt.edit(&requirement)
			_, err := runtimepkg.Resolve(root, requirement)
			assertRuntimeKind(t, err, runtimepkg.ErrorIncompatible)
		})
	}
}

func TestRegistryKeepsTerminalModesSeparate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtimes")
	binary := writeRuntime(t, t.TempDir(), 0o700)
	terminal := validMetadata(t, binary)
	windowed := terminal
	windowed.Terminal = false
	if _, err := runtimepkg.Add(root, binary, terminal); err != nil {
		t.Fatal(err)
	}
	if _, err := runtimepkg.Add(root, binary, windowed); err != nil {
		t.Fatal(err)
	}
	values, err := runtimepkg.List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || !values[0].Metadata.Terminal || values[1].Metadata.Terminal {
		t.Fatalf("terminal variants = %#v", values)
	}
}

func TestAddRejectsInvalidHashAndPermission(t *testing.T) {
	binary := writeRuntime(t, t.TempDir(), 0o700)
	metadata := validMetadata(t, binary)
	metadata.BinarySHA256 = toolchainHash
	_, err := runtimepkg.Add(filepath.Join(t.TempDir(), "hash"), binary, metadata)
	assertRuntimeKind(t, err, runtimepkg.ErrorIntegrity)

	if goruntime.GOOS == "windows" {
		return
	}
	noPermission := writeRuntime(t, t.TempDir(), 0o600)
	metadata = validMetadata(t, noPermission)
	_, err = runtimepkg.Add(filepath.Join(t.TempDir(), "permission"), noPermission, metadata)
	assertRuntimeKind(t, err, runtimepkg.ErrorPermission)
}

func TestLoadRejectsMissingMetadataAndTamperedBinary(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "1.0.0", "linux-x64")
	if err := os.MkdirAll(missing, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := runtimepkg.Load(missing)
	assertRuntimeKind(t, err, runtimepkg.ErrorInvalid)

	root := filepath.Join(t.TempDir(), "runtimes")
	binary := writeRuntime(t, t.TempDir(), 0o700)
	added, err := runtimepkg.Add(root, binary, validMetadata(t, binary))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(added.BinaryPath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = runtimepkg.Load(added.Directory)
	assertRuntimeKind(t, err, runtimepkg.ErrorIntegrity)
	if _, err := runtimepkg.List(root); err == nil {
		t.Fatal("List() accepted tampered runtime")
	}
}

func TestReadMetadataRejectsMissingAndUnknownFields(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing.json")
	if err := os.WriteFile(missing, []byte(`{"format_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runtimepkg.ReadMetadata(missing)
	assertRuntimeKind(t, err, runtimepkg.ErrorInvalid)

	unknown := filepath.Join(root, "unknown.json")
	data := `{
  "format_version": 1,
  "fluxa_version": "unreported",
  "toolchain_sha256": "` + toolchainHash + `",
  "package_format_version": 1,
  "bytecode_version": "",
  "bytecode_abi": "",
  "libraries_sha256": "` + librariesHash + `",
  "program_formats": ["fluxa-source"],
  "os": "linux",
  "arch": "amd64",
  "terminal": true,
  "binary_name": "fluxa-runtime",
  "binary_sha256": "` + toolchainHash + `",
  "unknown": true
}`
	if err := os.WriteFile(unknown, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = runtimepkg.ReadMetadata(unknown)
	assertRuntimeKind(t, err, runtimepkg.ErrorInvalid)
}

func validMetadata(t *testing.T, binary string) runtimepkg.Metadata {
	t.Helper()
	hash, err := fileHash(binary)
	if err != nil {
		t.Fatal(err)
	}
	return runtimepkg.Metadata{
		FormatVersion:        runtimepkg.CurrentMetadataVersion,
		FluxaVersion:         "unreported",
		ToolchainSHA256:      toolchainHash,
		PackageFormatVersion: 1,
		LibrariesSHA256:      librariesHash,
		ProgramFormats:       []string{"fluxa-source"},
		OS:                   "linux",
		Arch:                 "amd64",
		Terminal:             true,
		BinaryName:           "fluxa-runtime",
		BinarySHA256:         hash,
	}
}

func validRequirement() runtimepkg.Requirement {
	return runtimepkg.Requirement{
		ToolchainSHA256:      toolchainHash,
		PackageFormatVersion: 1,
		LibrariesSHA256:      librariesHash,
		ProgramFormat:        "fluxa-source",
		OS:                   "linux",
		Arch:                 "amd64",
		Terminal:             true,
	}
}

func writeRuntime(t *testing.T, root string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(root, "fluxa-runtime")
	if err := os.WriteFile(path, []byte("runtime binary"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func fileHash(path string) (string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- test helper path.
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func assertRuntimeKind(t *testing.T, err error, want runtimepkg.ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %q", want)
	}
	var runtimeError *runtimepkg.Error
	if !errors.As(err, &runtimeError) {
		t.Fatalf("error type = %T, want *runtimepkg.Error: %v", err, err)
	}
	if runtimeError.Kind != want {
		t.Fatalf("error kind = %q, want %q: %v", runtimeError.Kind, want, err)
	}
}
