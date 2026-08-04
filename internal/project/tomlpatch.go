package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// EnsureStringField ensures fluxa.toml below root declares table.key as a
// TOML basic string. See ensureField for the shared additive-edit contract.
// table may be a dotted nested-table header exactly as it would be written
// in the file, e.g. "targets.macos".
func EnsureStringField(root, table, key, value string) (changed bool, err error) {
	return ensureField(root, table, key, fmt.Sprintf("%s = %s", key, quoteTOMLString(value)))
}

// EnsureBoolField ensures fluxa.toml below root declares table.key as a
// TOML boolean. See ensureField for the shared additive-edit contract.
func EnsureBoolField(root, table, key string, value bool) (changed bool, err error) {
	return ensureField(root, table, key, fmt.Sprintf("%s = %t", key, value))
}

// EnsureStringArrayField ensures fluxa.toml below root declares table.key as
// a single-line TOML array of strings. See ensureField for the shared
// additive-edit contract.
func EnsureStringArrayField(root, table, key string, values []string) (changed bool, err error) {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = quoteTOMLString(value)
	}
	return ensureField(root, table, key, fmt.Sprintf("%s = [%s]", key, strings.Join(quoted, ", ")))
}

// HasField reports whether fluxa.toml below root already declares table.key,
// regardless of its value or type. A missing file reports false, not an
// error. table may be a dotted nested-table header, e.g. "targets.macos".
func HasField(root, table, key string) (bool, error) {
	generic, _, _, err := readGenericConfig(root)
	if err != nil {
		return false, err
	}
	tableValue, ok := lookupTable(generic, table)
	if !ok {
		return false, nil
	}
	_, ok = tableValue[key]
	return ok, nil
}

// ensureField is the shared additive editor behind EnsureStringField,
// EnsureBoolField, and EnsureStringArrayField. It is strictly additive: an
// existing key is never modified or removed, regardless of its current
// value, and no other byte of the file is touched. A missing key is
// appended immediately after its table header; a missing table is appended
// at end of file; a missing file is created containing only the requested
// table and key.
//
// The file is rewritten atomically (temporary file, fsync, rename) and its
// original permissions are preserved when it already existed. Detection of
// an existing key is done with a full TOML decode, so it is correct
// regardless of formatting; the edit itself is a line-based text insertion
// that assumes LF line endings, matching every fluxa.toml this repository
// generates or documents.
func ensureField(root, table, key, line string) (changed bool, err error) {
	generic, original, exists, err := readGenericConfig(root)
	if err != nil {
		return false, err
	}
	if tableValue, ok := lookupTable(generic, table); ok {
		if _, ok := tableValue[key]; ok {
			return false, nil
		}
	}

	path := filepath.Join(root, "fluxa.toml")
	mode := os.FileMode(0o644)
	if exists {
		if info, statErr := os.Stat(path); statErr == nil {
			mode = info.Mode().Perm()
		}
	}

	updated := insertField(original, table, line)
	if err := writeFileAtomic(path, updated, mode); err != nil {
		return false, err
	}
	return true, nil
}

// readGenericConfig reads and generically decodes fluxa.toml below root. A
// missing file reports exists=false with no error.
func readGenericConfig(root string) (generic map[string]any, original []byte, exists bool, err error) {
	path := filepath.Join(root, "fluxa.toml")

	// path is root/fluxa.toml; root is a caller-controlled project directory.
	original, readErr := os.ReadFile(path) // #nosec G304
	exists = readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, nil, false, fmt.Errorf("read %s: %w", path, readErr)
	}
	if !exists {
		return nil, original, false, nil
	}
	if _, decodeErr := toml.Decode(string(original), &generic); decodeErr != nil {
		return nil, nil, false, fmt.Errorf("parse %s: %w", path, decodeErr)
	}
	return generic, original, true, nil
}

// lookupTable walks a dotted table path (e.g. "targets.macos") through a
// generically-decoded TOML document.
func lookupTable(generic map[string]any, table string) (map[string]any, bool) {
	current := generic
	for _, part := range strings.Split(table, ".") {
		next, ok := current[part]
		if !ok {
			return nil, false
		}
		nextMap, ok := next.(map[string]any)
		if !ok {
			return nil, false
		}
		current = nextMap
	}
	return current, true
}

// insertField appends line to the [table] block of original, creating the
// table (or the whole document) if it is absent. The result always ends
// with exactly one trailing newline.
func insertField(original []byte, table, line string) []byte {
	header := "[" + table + "]"
	hasContent := len(original) > 0
	hasTrailingNewline := hasContent && original[len(original)-1] == '\n'

	lines := strings.Split(string(original), "\n")
	if hasTrailingNewline && len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	for index, raw := range lines {
		if strings.TrimSpace(raw) == header {
			result := make([]string, 0, len(lines)+1)
			result = append(result, lines[:index+1]...)
			result = append(result, line)
			result = append(result, lines[index+1:]...)
			return []byte(strings.Join(result, "\n") + "\n")
		}
	}

	var builder strings.Builder
	builder.Write(original)
	if hasContent && !hasTrailingNewline {
		builder.WriteByte('\n')
	}
	if hasContent {
		builder.WriteByte('\n')
	}
	builder.WriteString(header)
	builder.WriteByte('\n')
	builder.WriteString(line)
	builder.WriteByte('\n')
	return []byte(builder.String())
}

func quoteTOMLString(value string) string {
	var builder strings.Builder
	builder.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\':
			builder.WriteString(`\\`)
		case '"':
			builder.WriteString(`\"`)
		case '\t':
			builder.WriteString(`\t`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		default:
			builder.WriteRune(r)
		}
	}
	builder.WriteByte('"')
	return builder.String()
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "fluxa.toml.tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tempPath := temp.Name()
	published := false
	defer func() {
		if !published {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Chmod(tempPath, mode); err != nil {
		return fmt.Errorf("set file mode: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("rename temporary file: %w", err)
	}
	published = true
	return nil
}
