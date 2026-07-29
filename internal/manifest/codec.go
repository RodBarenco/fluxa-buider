package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

const maxEncodedSize = 16 * 1024 * 1024

// Encode returns the canonical indented JSON representation with a final LF.
func Encode(value Manifest) ([]byte, error) {
	canonical := value
	canonical.Files = append([]File(nil), value.Files...)
	// New returns sorted files. Encode also canonicalizes direct callers.
	sortFiles(canonical.Files)
	if err := Validate(canonical); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(canonical, "", "  ")
	if err != nil {
		return nil, manifestError(ErrorInvalid, "encode", "", err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxEncodedSize {
		return nil, manifestError(ErrorTooLarge, "encode", "", fmt.Errorf("encoded size exceeds %d bytes", maxEncodedSize))
	}
	return encoded, nil
}

// Decode reads one strict JSON manifest with a bounded input size.
func Decode(reader io.Reader) (Manifest, error) {
	limited := io.LimitReader(reader, maxEncodedSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return Manifest{}, manifestError(ErrorIO, "decode", "", err)
	}
	if len(data) > maxEncodedSize {
		return Manifest{}, manifestError(ErrorTooLarge, "decode", "", fmt.Errorf("input exceeds %d bytes", maxEncodedSize))
	}
	if err := validateRequiredJSONFields(data); err != nil {
		return Manifest{}, manifestError(ErrorInvalid, "decode", "", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value Manifest
	if err := decoder.Decode(&value); err != nil {
		return Manifest{}, manifestError(ErrorInvalid, "decode", "", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Manifest{}, manifestError(ErrorInvalid, "decode", "", err)
	}
	if err := Validate(value); err != nil {
		return Manifest{}, err
	}
	return value, nil
}

func validateRequiredJSONFields(data []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}
	for _, field := range []string{"format_version", "project", "toolchain", "target", "build", "files"} {
		if _, exists := root[field]; !exists {
			return fmt.Errorf("required field %q is missing", field)
		}
	}
	requiredNested := map[string][]string{
		"project":   {"name", "id", "version", "entry", "type"},
		"toolchain": {"protocol", "fluxa_sha256", "libraries_sha256"},
		"target":    {"os", "arch", "terminal"},
		"build":     {"preflight", "program_format", "debug", "source_exposed"},
	}
	for parent, fields := range requiredNested {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(root[parent], &object); err != nil {
			return fmt.Errorf("field %q must be an object: %w", parent, err)
		}
		for _, field := range fields {
			if _, exists := object[field]; !exists {
				return fmt.Errorf("required field %q is missing", parent+"."+field)
			}
		}
	}
	var files []map[string]json.RawMessage
	if err := json.Unmarshal(root["files"], &files); err != nil {
		return fmt.Errorf("field %q must be an array: %w", "files", err)
	}
	for index, file := range files {
		for _, field := range []string{"path", "logical_path", "kind", "size", "sha256"} {
			if _, exists := file[field]; !exists {
				return fmt.Errorf("required field %q is missing", fmt.Sprintf("files[%d].%s", index, field))
			}
		}
	}
	return nil
}

// WriteFile atomically writes a canonical manifest to an existing directory.
func WriteFile(path string, value Manifest) error {
	encoded, err := Encode(value)
	if err != nil {
		return err
	}
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err != nil {
		return manifestError(ErrorIO, "inspect output directory", parent, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return manifestError(ErrorInvalid, "validate output directory", parent, errors.New("parent must be a non-symlink directory"))
	}
	temp, err := os.CreateTemp(parent, ".manifest-*.tmp")
	if err != nil {
		return manifestError(ErrorIO, "create temporary", path, err)
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return manifestError(ErrorIO, "secure temporary", tempPath, err)
	}
	if _, err := temp.Write(encoded); err != nil {
		return manifestError(ErrorIO, "write temporary", tempPath, err)
	}
	if err := temp.Sync(); err != nil {
		return manifestError(ErrorIO, "sync temporary", tempPath, err)
	}
	if err := temp.Close(); err != nil {
		return manifestError(ErrorIO, "close temporary", tempPath, err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return manifestError(ErrorInvalid, "publish", path, errors.New("destination already exists"))
		}
		return manifestError(ErrorIO, "inspect destination", path, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return manifestError(ErrorIO, "publish", path, err)
	}
	committed = true
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values are not allowed")
	}
	return err
}

func sortFiles(files []File) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
}
