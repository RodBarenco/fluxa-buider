package runtimepkg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	// CurrentMetadataVersion is the runtime registry metadata schema.
	CurrentMetadataVersion = 1
	maxMetadataSize        = 1024 * 1024
)

var runtimeVersionPattern = regexp.MustCompile(`^(?:unreported|(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?)$`)

// Metadata is the compatibility identity asserted when a runtime is added.
type Metadata struct {
	FormatVersion        int      `json:"format_version"`
	FluxaVersion         string   `json:"fluxa_version"`
	ToolchainSHA256      string   `json:"toolchain_sha256"`
	PackageFormatVersion uint32   `json:"package_format_version"`
	BytecodeVersion      string   `json:"bytecode_version"`
	BytecodeABI          string   `json:"bytecode_abi"`
	LibrariesSHA256      string   `json:"libraries_sha256"`
	ProgramFormats       []string `json:"program_formats"`
	OS                   string   `json:"os"`
	Arch                 string   `json:"arch"`
	Terminal             bool     `json:"terminal"`
	BinaryName           string   `json:"binary_name"`
	BinarySHA256         string   `json:"binary_sha256"`
}

// Validate checks compatibility metadata without trusting its directory.
func (m Metadata) Validate() error {
	if m.FormatVersion != CurrentMetadataVersion {
		return runtimeError(ErrorInvalid, "validate metadata", "format_version", fmt.Errorf("unsupported version %d", m.FormatVersion))
	}
	if !runtimeVersionPattern.MatchString(m.FluxaVersion) {
		return runtimeError(ErrorInvalid, "validate metadata", "fluxa_version", errors.New("must be semantic version or unreported"))
	}
	for name, value := range map[string]string{
		"toolchain_sha256": m.ToolchainSHA256,
		"libraries_sha256": m.LibrariesSHA256,
		"binary_sha256":    m.BinarySHA256,
	} {
		if !validRuntimeHash(value) {
			return runtimeError(ErrorInvalid, "validate metadata", name, errors.New("must be 64 lowercase hexadecimal characters"))
		}
	}
	if m.PackageFormatVersion != 1 {
		return runtimeError(ErrorInvalid, "validate metadata", "package_format_version", errors.New("must be 1"))
	}
	if m.OS != "windows" && m.OS != "linux" && m.OS != "macos" {
		return runtimeError(ErrorInvalid, "validate metadata", "os", errors.New("must be windows, linux, or macos"))
	}
	if m.Arch != "amd64" && m.Arch != "arm64" {
		return runtimeError(ErrorInvalid, "validate metadata", "arch", errors.New("must be amd64 or arm64"))
	}
	expectedBinary := "fluxa-runtime"
	if m.OS == "windows" {
		expectedBinary += ".exe"
	}
	if m.BinaryName != expectedBinary || filepath.Base(m.BinaryName) != m.BinaryName {
		return runtimeError(ErrorInvalid, "validate metadata", "binary_name", fmt.Errorf("must be %q", expectedBinary))
	}
	if len(m.ProgramFormats) == 0 {
		return runtimeError(ErrorInvalid, "validate metadata", "program_formats", errors.New("must not be empty"))
	}
	previous := ""
	for index, format := range m.ProgramFormats {
		if format != "fluxa-source" && format != "fluxa-bytecode" {
			return runtimeError(ErrorInvalid, "validate metadata", fmt.Sprintf("program_formats[%d]", index), errors.New("unknown program format"))
		}
		if format <= previous {
			return runtimeError(ErrorInvalid, "validate metadata", "program_formats", errors.New("must be sorted and unique"))
		}
		previous = format
	}
	if contains(m.ProgramFormats, "fluxa-bytecode") && (m.BytecodeVersion == "" || m.BytecodeABI == "") {
		return runtimeError(ErrorInvalid, "validate metadata", "bytecode_abi", errors.New("bytecode runtime requires version and ABI"))
	}
	return nil
}

func encodeMetadata(value Metadata) ([]byte, error) {
	canonical := value
	canonical.ProgramFormats = append([]string(nil), value.ProgramFormats...)
	sort.Strings(canonical.ProgramFormats)
	if err := canonical.Validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(canonical, "", "  ")
	if err != nil {
		return nil, runtimeError(ErrorInvalid, "encode metadata", "", err)
	}
	return append(data, '\n'), nil
}

func decodeMetadata(reader io.Reader) (Metadata, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxMetadataSize+1))
	if err != nil {
		return Metadata{}, runtimeError(ErrorIO, "read metadata", "", err)
	}
	if len(data) > maxMetadataSize {
		return Metadata{}, runtimeError(ErrorInvalid, "read metadata", "", errors.New("metadata exceeds 1 MiB"))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Metadata{}, runtimeError(ErrorInvalid, "decode metadata", "", err)
	}
	for _, field := range []string{
		"format_version", "fluxa_version", "toolchain_sha256",
		"package_format_version", "bytecode_version", "bytecode_abi",
		"libraries_sha256", "program_formats", "os", "arch", "terminal",
		"binary_name", "binary_sha256",
	} {
		if _, exists := fields[field]; !exists {
			return Metadata{}, runtimeError(ErrorInvalid, "decode metadata", field, errors.New("required field is missing"))
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value Metadata
	if err := decoder.Decode(&value); err != nil {
		return Metadata{}, runtimeError(ErrorInvalid, "decode metadata", "", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values are not allowed")
		}
		return Metadata{}, runtimeError(ErrorInvalid, "decode metadata", "", err)
	}
	if err := value.Validate(); err != nil {
		return Metadata{}, err
	}
	return value, nil
}

// ReadMetadata loads one strict runtime metadata JSON file.
func ReadMetadata(path string) (Metadata, error) {
	file, err := os.Open(path) // #nosec G304 -- caller-selected metadata is validated as untrusted input.
	if err != nil {
		return Metadata{}, runtimeError(ErrorIO, "open metadata", path, err)
	}
	value, decodeErr := decodeMetadata(file)
	closeErr := file.Close()
	if decodeErr != nil || closeErr != nil {
		return Metadata{}, runtimeError(ErrorInvalid, "read metadata", path, errors.Join(decodeErr, closeErr))
	}
	return value, nil
}

func validRuntimeHash(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func runtimeError(kind ErrorKind, operation, path string, err error) *Error {
	return &Error{Kind: kind, Operation: operation, Path: path, Err: err}
}
