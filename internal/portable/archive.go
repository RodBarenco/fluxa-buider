package portable

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var normalizedZIPTime = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

// ArchiveResult describes a deterministic distribution archive and checksum.
type ArchiveResult struct {
	Path         string
	ChecksumPath string
	SHA256       string
	Size         int64
	Format       string
}

// Archive creates a deterministic ZIP or tar.gz beside a staged portable directory.
func Archive(ctx context.Context, portable Result, targetOS string) (ArchiveResult, error) {
	if portable.TargetOS != targetOS {
		return ArchiveResult{}, portableError(ErrorInvalid, "validate archive target", targetOS,
			fmt.Errorf("portable target is %q", portable.TargetOS))
	}
	format, extension, err := archiveFormat(targetOS)
	if err != nil {
		return ArchiveResult{}, err
	}
	entries, err := archiveEntries(portable)
	if err != nil {
		return ArchiveResult{}, err
	}
	archivePath := filepath.Join(filepath.Dir(portable.Directory), portable.Name+extension)
	checksumPath := archivePath + ".sha256"
	if _, err := os.Lstat(archivePath); !errors.Is(err, os.ErrNotExist) {
		return ArchiveResult{}, portableError(ErrorInvalid, "create archive", archivePath, errors.New("destination already exists"))
	}
	if _, err := os.Lstat(checksumPath); !errors.Is(err, os.ErrNotExist) {
		return ArchiveResult{}, portableError(ErrorInvalid, "create checksum", checksumPath, errors.New("destination already exists"))
	}

	output, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- confined sibling output.
	if err != nil {
		return ArchiveResult{}, portableError(ErrorIO, "create archive", archivePath, err)
	}
	complete := false
	defer func() {
		_ = output.Close()
		if !complete {
			_ = os.Remove(archivePath)
			_ = os.Remove(checksumPath)
		}
	}()

	hash := sha256.New()
	writer := io.MultiWriter(output, hash)
	switch format {
	case "zip":
		err = writeZIP(ctx, writer, portable.Name, entries)
	case "tar.gz":
		err = writeTarGZ(ctx, writer, portable.Name, entries)
	}
	if err != nil {
		return ArchiveResult{}, err
	}
	if err := errors.Join(output.Sync(), output.Close()); err != nil {
		return ArchiveResult{}, portableError(ErrorIO, "finish archive", archivePath, err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	checksum := []byte(fmt.Sprintf("%s  %s\n", digest, filepath.Base(archivePath)))
	if err := os.WriteFile(checksumPath, checksum, 0o600); err != nil { // #nosec G304 -- confined sibling output.
		return ArchiveResult{}, portableError(ErrorIO, "write archive checksum", checksumPath, err)
	}
	info, err := os.Stat(archivePath)
	if err != nil {
		return ArchiveResult{}, portableError(ErrorIO, "inspect archive", archivePath, err)
	}
	complete = true
	return ArchiveResult{
		Path: archivePath, ChecksumPath: checksumPath, SHA256: digest,
		Size: info.Size(), Format: format,
	}, nil
}

type archiveEntry struct {
	path      string
	relative  string
	mode      os.FileMode
	directory bool
}

func archiveEntries(portable Result) ([]archiveEntry, error) {
	if portable.TargetOS == "macos" {
		return macOSArchiveEntries(portable)
	}
	info, err := os.Lstat(portable.Directory)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, portableError(ErrorInvalid, "inspect portable directory", portable.Directory, errors.New("must be a non-symlink directory"))
	}
	directoryEntries, err := os.ReadDir(portable.Directory)
	if err != nil {
		return nil, portableError(ErrorIO, "read portable directory", portable.Directory, err)
	}
	expected := map[string]bool{
		filepath.Base(portable.Executable): false,
		filepath.Base(portable.Package):    false,
		filepath.Base(portable.BuildInfo):  false,
	}
	expectedCount := 3
	if portable.Signature != "" {
		expected[filepath.Base(portable.Signature)] = false
		expectedCount++
	}
	for _, extra := range portable.ExtraFiles {
		expected[filepath.Base(extra)] = false
		expectedCount++
	}
	if len(expected) != expectedCount {
		return nil, portableError(ErrorInvalid, "validate archive input", portable.Directory,
			errors.New("portable result paths are missing or collide"))
	}
	if len(directoryEntries) != len(expected) {
		return nil, portableError(ErrorInvalid, "validate archive input", portable.Directory,
			fmt.Errorf("contains %d entries, expected %d", len(directoryEntries), len(expected)))
	}
	entries := make([]archiveEntry, 0, len(directoryEntries))
	for _, entry := range directoryEntries {
		if _, ok := expected[entry.Name()]; !ok {
			return nil, portableError(ErrorInvalid, "validate archive input", entry.Name(), errors.New("unexpected file"))
		}
		expected[entry.Name()] = true
		path := filepath.Join(portable.Directory, entry.Name())
		entryInfo, err := os.Lstat(path)
		if err != nil {
			return nil, portableError(ErrorIO, "inspect archive input", path, err)
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 || !entryInfo.Mode().IsRegular() {
			return nil, portableError(ErrorInvalid, "validate archive input", path, errors.New("only non-symlink regular files are allowed"))
		}
		mode := os.FileMode(0o600)
		if portable.TargetOS != "windows" && path == portable.Executable {
			mode = 0o700
		}
		entries = append(entries, archiveEntry{path: path, relative: filepath.Base(path), mode: mode})
	}
	for name, found := range expected {
		if !found {
			return nil, portableError(ErrorInvalid, "validate archive input", name, errors.New("required file is missing"))
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].relative < entries[j].relative
	})
	return entries, nil
}

func macOSArchiveEntries(result Result) ([]archiveEntry, error) {
	expected := map[string]bool{
		"Contents":           false,
		"Contents/MacOS":     false,
		"Contents/Resources": false,
		relativeBundlePath(result.Directory, result.Executable): false,
		relativeBundlePath(result.Directory, result.Package):    false,
		relativeBundlePath(result.Directory, result.BuildInfo):  false,
	}
	if result.Signature != "" {
		expected[relativeBundlePath(result.Directory, result.Signature)] = false
	}
	for _, extra := range result.ExtraFiles {
		expected[relativeBundlePath(result.Directory, extra)] = false
	}
	entries := make([]archiveEntry, 0, len(expected))
	err := filepath.WalkDir(result.Directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == result.Directory {
			return nil
		}
		relative, err := filepath.Rel(result.Directory, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if _, ok := expected[relative]; !ok {
			return fmt.Errorf("unexpected bundle entry %q", relative)
		}
		expected[relative] = true
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("bundle entries must not be symlinks")
		}
		mode := os.FileMode(0o600)
		if entry.IsDir() {
			mode = 0o755
		} else if path == result.Executable {
			mode = 0o700
		} else if !info.Mode().IsRegular() {
			return errors.New("bundle entries must be directories or regular files")
		}
		entries = append(entries, archiveEntry{
			path: path, relative: relative, mode: mode, directory: entry.IsDir(),
		})
		return nil
	})
	if err != nil {
		return nil, portableError(ErrorInvalid, "validate macOS bundle", result.Directory, err)
	}
	for path, found := range expected {
		if !found {
			return nil, portableError(ErrorInvalid, "validate macOS bundle", path, errors.New("required entry is missing"))
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].relative < entries[j].relative })
	return entries, nil
}

func relativeBundlePath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(relative)
}

func writeZIP(ctx context.Context, output io.Writer, root string, entries []archiveEntry) error {
	writer := zip.NewWriter(output)
	rootHeader := &zip.FileHeader{Name: root + "/", Method: zip.Store}
	rootHeader.SetMode(0o755 | os.ModeDir)
	rootHeader.Modified = normalizedZIPTime
	if _, err := writer.CreateHeader(rootHeader); err != nil {
		return portableError(ErrorIO, "write ZIP root", root, err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return portableError(ErrorCanceled, "write ZIP", entry.path, err)
		}
		name := root + "/" + entry.relative
		if entry.directory {
			name += "/"
		}
		header := &zip.FileHeader{
			Name:   name,
			Method: zip.Deflate,
		}
		if entry.directory {
			header.Method = zip.Store
		}
		header.SetMode(entry.mode)
		header.Modified = normalizedZIPTime
		destination, err := writer.CreateHeader(header)
		if err != nil {
			return portableError(ErrorIO, "create ZIP entry", entry.path, err)
		}
		if !entry.directory {
			if err := copyArchiveFile(ctx, destination, entry.path); err != nil {
				return err
			}
		}
	}
	if err := writer.Close(); err != nil {
		return portableError(ErrorIO, "finish ZIP", root, err)
	}
	return nil
}

func writeTarGZ(ctx context.Context, output io.Writer, root string, entries []archiveEntry) error {
	gzipWriter, err := gzip.NewWriterLevel(output, gzip.BestCompression)
	if err != nil {
		return portableError(ErrorIO, "create gzip writer", root, err)
	}
	gzipWriter.ModTime = time.Unix(0, 0).UTC()
	gzipWriter.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{
		Name: root + "/", Mode: 0o755, Typeflag: tar.TypeDir,
		ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatPAX,
	}); err != nil {
		return portableError(ErrorIO, "write tar root", root, err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return portableError(ErrorCanceled, "write tar.gz", entry.path, err)
		}
		size := int64(0)
		entryType := byte(tar.TypeDir)
		name := root + "/" + entry.relative + "/"
		if !entry.directory {
			info, err := os.Stat(entry.path)
			if err != nil {
				return portableError(ErrorIO, "inspect tar input", entry.path, err)
			}
			size = info.Size()
			entryType = tar.TypeReg
			name = root + "/" + entry.relative
		}
		header := &tar.Header{
			Name: name, Size: size,
			Mode: int64(entry.mode), Typeflag: entryType,
			ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatPAX,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return portableError(ErrorIO, "create tar entry", entry.path, err)
		}
		if !entry.directory {
			if err := copyArchiveFile(ctx, tarWriter, entry.path); err != nil {
				return err
			}
		}
	}
	if err := errors.Join(tarWriter.Close(), gzipWriter.Close()); err != nil {
		return portableError(ErrorIO, "finish tar.gz", root, err)
	}
	return nil
}

func copyArchiveFile(ctx context.Context, destination io.Writer, path string) error {
	source, err := os.Open(path) // #nosec G304 -- verified portable input.
	if err != nil {
		return portableError(ErrorIO, "open archive input", path, err)
	}
	defer func() { _ = source.Close() }()
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return portableError(ErrorCanceled, "archive", path, err)
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			if _, err := destination.Write(buffer[:read]); err != nil {
				return portableError(ErrorIO, "write archive entry", path, err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return portableError(ErrorIO, "read archive input", path, readErr)
		}
	}
}

func archiveFormat(targetOS string) (format, extension string, err error) {
	switch strings.ToLower(targetOS) {
	case "windows":
		return "zip", ".zip", nil
	case "linux", "macos":
		return "tar.gz", ".tar.gz", nil
	default:
		return "", "", portableError(ErrorInvalid, "select archive format", targetOS, errors.New("unsupported target OS"))
	}
}
