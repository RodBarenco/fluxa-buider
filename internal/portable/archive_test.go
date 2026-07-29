package portable_test

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/portable"
)

func TestTarGZArchiveIsReproducibleExtractableAndExecutable(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("portable shell runtime fixture is Unix-only")
	}
	first := buildAndArchive(t, "linux")
	second := buildAndArchive(t, "linux")
	if first.archive.SHA256 != second.archive.SHA256 {
		t.Fatalf("archive hashes differ: %s != %s", first.archive.SHA256, second.archive.SHA256)
	}
	assertChecksum(t, first.archive)

	extracted := t.TempDir()
	extractTarGZ(t, first.archive.Path, extracted)
	assertExtractedMatches(t, first.portable, filepath.Join(extracted, first.portable.Name))
	executable := filepath.Join(extracted, first.portable.Name, filepath.Base(first.portable.Executable))
	info, err := os.Stat(executable)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("executable mode = %o", info.Mode().Perm())
	}
	extractedResult := first.portable
	extractedResult.Directory = filepath.Dir(executable)
	extractedResult.Executable = executable
	extractedResult.Package = filepath.Join(extractedResult.Directory, filepath.Base(first.portable.Package))
	if _, err := portable.SmokeDetailed(context.Background(), extractedResult, time.Second); err != nil {
		t.Fatalf("extracted application failed: %v", err)
	}
}

func TestZIPArchiveIsReproducibleAndPreservesContentAndModes(t *testing.T) {
	first := buildAndArchive(t, "windows")
	second := buildAndArchive(t, "windows")
	if first.archive.Format != "zip" || first.archive.SHA256 != second.archive.SHA256 {
		t.Fatalf("ZIP results differ: %#v %#v", first.archive, second.archive)
	}
	assertChecksum(t, first.archive)

	extracted := t.TempDir()
	extractZIP(t, first.archive.Path, extracted)
	assertExtractedMatches(t, first.portable, filepath.Join(extracted, first.portable.Name))
	archive, err := zip.OpenReader(first.archive.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()
	for _, file := range archive.File {
		if !file.FileInfo().IsDir() && file.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", file.Name, file.Mode().Perm())
		}
	}
}

func TestSafeZIPExtractorRejectsZipSlip(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "hostile.zip")
	output, err := os.Create(archivePath) // #nosec G304 -- test-controlled temporary path.
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(output)
	entry, err := writer.Create("../escape")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("bad")); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(writer.Close(), output.Close()); err != nil {
		t.Fatal(err)
	}
	err = extractZIPError(archivePath, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unsafe archive path") {
		t.Fatalf("extract hostile ZIP error = %v", err)
	}
}

type archivedFixture struct {
	portable portable.Result
	archive  portable.ArchiveResult
}

func buildAndArchive(t *testing.T, targetOS string) archivedFixture {
	t.Helper()
	fixture := newFixture(t, "Archive Game", true)
	fixture.request.TargetOS = targetOS
	fixture.request.Runtime.Metadata.OS = targetOS
	result, err := portable.Build(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := portable.Archive(context.Background(), result, targetOS)
	if err != nil {
		t.Fatal(err)
	}
	return archivedFixture{portable: result, archive: archive}
}

func assertChecksum(t *testing.T, result portable.ArchiveResult) {
	t.Helper()
	data, err := os.ReadFile(result.Path) // #nosec G304 -- test-controlled result.
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != result.SHA256 {
		t.Fatalf("archive checksum mismatch")
	}
	checksum, err := os.ReadFile(result.ChecksumPath) // #nosec G304 -- test-controlled result.
	if err != nil {
		t.Fatal(err)
	}
	expected := result.SHA256 + "  " + filepath.Base(result.Path) + "\n"
	if string(checksum) != expected {
		t.Fatalf("checksum file = %q, want %q", checksum, expected)
	}
}

func assertExtractedMatches(t *testing.T, original portable.Result, extracted string) {
	t.Helper()
	entries, err := os.ReadDir(original.Directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		want, err := os.ReadFile(filepath.Join(original.Directory, entry.Name())) // #nosec G304 -- test-controlled path.
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(extracted, entry.Name())) // #nosec G304 -- safe test extraction root.
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s content differs after extraction", entry.Name())
		}
	}
}

func extractZIP(t *testing.T, archivePath, destination string) {
	t.Helper()
	if err := extractZIPError(archivePath, destination); err != nil {
		t.Fatal(err)
	}
}

func extractZIPError(archivePath, destination string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	for _, entry := range reader.File {
		output, err := safeExtractPath(destination, entry.Name)
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(output, entry.Mode().Perm()); err != nil { // #nosec G301 -- verifies archived directory mode.
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil { // #nosec G301 -- extracted archive parent mode.
			return err
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		destinationFile, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, entry.Mode().Perm()) // #nosec G304 -- validated extraction path.
		if err != nil {
			_ = source.Close()
			return err
		}
		err = errors.Join(copyAndClose(destinationFile, source), source.Close())
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGZ(t *testing.T, archivePath, destination string) {
	t.Helper()
	source, err := os.Open(archivePath) // #nosec G304 -- test-controlled archive.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	gzipReader, err := gzip.NewReader(source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gzipReader.Close() }()
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		output, err := safeExtractPath(destination, header.Name)
		if err != nil {
			t.Fatal(err)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(output, os.FileMode(header.Mode)) // #nosec G115,G301 -- test archive mode is bounded by production writer.
		case tar.TypeReg:
			if err = os.MkdirAll(filepath.Dir(output), 0o755); err == nil { // #nosec G301 -- extracted archive parent mode.
				var file *os.File
				// #nosec G115,G304 -- production writer bounds mode; output passed safeExtractPath.
				file, err = os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode))
				if err == nil {
					err = copyAndClose(file, reader)
				}
			}
		default:
			err = errors.New("unexpected tar entry type")
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func safeExtractPath(root, name string) (string, error) {
	if name == "" || strings.Contains(name, "\\") || path.IsAbs(name) {
		return "", errors.New("unsafe archive path")
	}
	clean := path.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("unsafe archive path")
	}
	output := filepath.Join(root, filepath.FromSlash(clean))
	relative, err := filepath.Rel(root, output)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("unsafe archive path")
	}
	return output, nil
}

func copyAndClose(destination *os.File, source io.Reader) error {
	_, copyErr := io.Copy(destination, source)
	return errors.Join(copyErr, destination.Sync(), destination.Close())
}
