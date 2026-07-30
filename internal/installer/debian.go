package installer

import (
	"archive/tar"
	"bytes"
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

// Debian builds a Debian binary package without external tooling.
type Debian struct{}

// Name returns the stable installer format identifier.
func (Debian) Name() string { return "deb" }

type debEntry struct {
	name      string
	mode      int64
	path      string
	data      []byte
	directory bool
}

// Build creates a deterministic Debian 2.0 ar archive.
func (Debian) Build(ctx context.Context, request Request) (Result, error) {
	if request.OutputDir == "" || request.ProjectName == "" || request.ProjectID == "" ||
		request.Version == "" || request.Portable.TargetOS != "linux" {
		return Result{}, installError("invalid", "validate Debian request", "", errors.New("complete Linux portable metadata is required"))
	}
	if !validDebianPackageName(request.ProjectID) {
		return Result{}, installError("invalid", "validate Debian package name", request.ProjectID, errors.New("must use lowercase Debian package characters"))
	}
	if request.Portable.Directory == "" || request.Portable.Executable == "" ||
		request.Portable.Package == "" || request.Portable.BuildInfo == "" {
		return Result{}, installError("invalid", "validate Debian portable result", request.Portable.Directory, errors.New("portable paths are incomplete"))
	}
	if info, err := os.Lstat(request.OutputDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Result{}, installError("invalid", "validate Debian output", request.OutputDir, errors.New("must be a non-symlink directory"))
	}

	packageName := request.ProjectID
	applicationName := filepath.Base(request.Portable.Executable)
	dataEntries, err := debianDataEntries(request, packageName, applicationName)
	if err != nil {
		return Result{}, err
	}
	dataArchive, installedSize, err := writeDebianTarGZ(ctx, dataEntries)
	if err != nil {
		return Result{}, err
	}
	control := debianControl(request, packageName, installedSize)
	controlArchive, _, err := writeDebianTarGZ(ctx, []debEntry{
		{name: "control", mode: 0o644, data: control},
	})
	if err != nil {
		return Result{}, err
	}

	filename := fmt.Sprintf("%s_%s_amd64.deb", packageName, request.Version)
	outputPath := filepath.Join(request.OutputDir, filename)
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- validated output directory.
	if err != nil {
		return Result{}, installError("io", "create Debian package", outputPath, err)
	}
	complete := false
	defer func() {
		_ = output.Close()
		if !complete {
			_ = os.Remove(outputPath)
			_ = os.Remove(outputPath + ".sha256")
		}
	}()
	hash := sha256.New()
	writer := io.MultiWriter(output, hash)
	if _, err := io.WriteString(writer, "!<arch>\n"); err != nil {
		return Result{}, installError("io", "write Debian magic", outputPath, err)
	}
	for _, member := range []struct {
		name string
		data []byte
	}{
		{"debian-binary", []byte("2.0\n")},
		{"control.tar.gz", controlArchive},
		{"data.tar.gz", dataArchive},
	} {
		if err := writeARMember(writer, member.name, member.data); err != nil {
			return Result{}, installError("io", "write Debian member", member.name, err)
		}
	}
	if err := errors.Join(output.Sync(), output.Close()); err != nil {
		return Result{}, installError("io", "finish Debian package", outputPath, err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	checksumPath := outputPath + ".sha256"
	checksum := []byte(fmt.Sprintf("%s  %s\n", digest, filename))
	if err := os.WriteFile(checksumPath, checksum, 0o600); err != nil { // #nosec G304 -- sibling of validated output.
		return Result{}, installError("io", "write Debian checksum", checksumPath, err)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		return Result{}, installError("io", "inspect Debian package", outputPath, err)
	}
	complete = true
	return Result{
		Path: outputPath, ChecksumPath: checksumPath, SHA256: digest,
		Size: info.Size(), Format: "deb",
	}, nil
}

func debianDataEntries(request Request, packageName, applicationName string) ([]debEntry, error) {
	entries, err := os.ReadDir(request.Portable.Directory)
	if err != nil {
		return nil, installError("io", "read portable directory", request.Portable.Directory, err)
	}
	expected := make(map[string]bool, len(entries))
	for _, path := range append([]string{
		request.Portable.Executable, request.Portable.Package, request.Portable.BuildInfo,
		request.Portable.Signature,
	}, request.Portable.ExtraFiles...) {
		if path != "" {
			expected[filepath.Base(path)] = false
		}
	}
	result := make([]debEntry, 0, len(entries)+3)
	for _, entry := range entries {
		found, ok := expected[entry.Name()]
		if !ok || found {
			return nil, installError("invalid", "validate Debian portable input", entry.Name(), errors.New("unexpected or duplicate file"))
		}
		expected[entry.Name()] = true
		path := filepath.Join(request.Portable.Directory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, installError("invalid", "validate Debian portable input", path, errors.New("must be a non-symlink regular file"))
		}
		mode := int64(0o644)
		if path == request.Portable.Executable {
			mode = 0o755
		}
		result = append(result, debEntry{
			name: filepath.ToSlash(filepath.Join("opt", packageName, entry.Name())),
			mode: mode, path: path,
		})
	}
	for name, found := range expected {
		if !found {
			return nil, installError("invalid", "validate Debian portable input", name, errors.New("required file is missing"))
		}
	}
	launcher := fmt.Sprintf("#!/bin/sh\nexec '/opt/%s/%s' \"$@\"\n", packageName, applicationName)
	result = append(result, debEntry{
		name: filepath.ToSlash(filepath.Join("usr", "bin", applicationName)),
		mode: 0o755, data: []byte(launcher),
	})
	iconName := ""
	for _, extra := range request.Portable.ExtraFiles {
		if strings.EqualFold(filepath.Ext(extra), ".png") {
			iconName = packageName
			result = append(result, debEntry{
				name: filepath.ToSlash(filepath.Join("usr", "share", "pixmaps", packageName+".png")),
				mode: 0o644, path: extra,
			})
			break
		}
	}
	desktop := debianDesktop(request, applicationName, iconName)
	result = append(result, debEntry{
		name: filepath.ToSlash(filepath.Join("usr", "share", "applications", packageName+".desktop")),
		mode: 0o644, data: desktop,
	})
	directories := make(map[string]bool)
	for _, entry := range result {
		parent := filepath.ToSlash(filepath.Dir(entry.name))
		for parent != "." && parent != "/" && parent != "" {
			directories[parent] = true
			parent = filepath.ToSlash(filepath.Dir(parent))
		}
	}
	for directory := range directories {
		result = append(result, debEntry{name: directory, mode: 0o755, directory: true})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result, nil
}

func debianControl(request Request, packageName string, installedSize int64) []byte {
	description := strings.ReplaceAll(strings.TrimSpace(request.ProjectName), "\n", " ")
	return []byte(fmt.Sprintf(
		"Package: %s\nVersion: %s\nSection: utils\nPriority: optional\nArchitecture: amd64\n"+
			"Maintainer: Fluxa Builder <noreply@invalid>\nInstalled-Size: %d\n"+
			"Description: %s\n Built with Fluxa Builder. User data follows XDG directories.\n",
		packageName, request.Version, installedSize, description,
	))
}

func debianDesktop(request Request, executable, icon string) []byte {
	iconLine := ""
	if icon != "" {
		iconLine = "Icon=" + icon + "\n"
	}
	return []byte(fmt.Sprintf(
		"[Desktop Entry]\nType=Application\nName=%s\nExec=/usr/bin/%s\n%sTerminal=%t\nCategories=Utility;\n",
		strings.ReplaceAll(request.ProjectName, "\n", " "), executable, iconLine, request.Terminal,
	))
}

func writeDebianTarGZ(ctx context.Context, entries []debEntry) ([]byte, int64, error) {
	var output bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&output, gzip.BestCompression)
	if err != nil {
		return nil, 0, err
	}
	gzipWriter.ModTime = time.Unix(0, 0).UTC()
	gzipWriter.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	var installed int64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, 0, installError("canceled", "write Debian archive", entry.name, err)
		}
		var size int64
		if entry.directory {
			size = 0
		} else if entry.path != "" {
			info, err := os.Stat(entry.path)
			if err != nil {
				return nil, 0, installError("io", "inspect Debian input", entry.path, err)
			}
			size = info.Size()
		} else {
			size = int64(len(entry.data))
		}
		header := &tar.Header{
			Name: entry.name, Mode: entry.mode, Size: size, Typeflag: tar.TypeReg,
			ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatGNU,
		}
		if entry.directory {
			header.Name += "/"
			header.Typeflag = tar.TypeDir
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return nil, 0, installError("io", "write Debian tar header", entry.name, err)
		}
		if entry.directory {
			continue
		} else if entry.path != "" {
			file, err := os.Open(entry.path) // #nosec G304 -- validated portable input.
			if err != nil {
				return nil, 0, installError("io", "open Debian input", entry.path, err)
			}
			_, copyErr := io.Copy(tarWriter, file)
			closeErr := file.Close()
			if copyErr != nil || closeErr != nil {
				return nil, 0, installError("io", "copy Debian input", entry.path, errors.Join(copyErr, closeErr))
			}
		} else if _, err := tarWriter.Write(entry.data); err != nil {
			return nil, 0, installError("io", "write Debian data", entry.name, err)
		}
		installed += (size + 1023) / 1024
	}
	if err := errors.Join(tarWriter.Close(), gzipWriter.Close()); err != nil {
		return nil, 0, installError("io", "finish Debian tar", "", err)
	}
	return output.Bytes(), installed, nil
}

func writeARMember(writer io.Writer, name string, data []byte) error {
	if len(name) > 15 {
		return errors.New("ar member name is too long")
	}
	header := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8o%-10d`\n", name+"/", 0, 0, 0, 0o100644, len(data))
	if len(header) != 60 {
		return errors.New("invalid ar header size")
	}
	if _, err := io.WriteString(writer, header); err != nil {
		return err
	}
	if _, err := writer.Write(data); err != nil {
		return err
	}
	if len(data)%2 != 0 {
		_, err := writer.Write([]byte{'\n'})
		return err
	}
	return nil
}

func validDebianPackageName(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '+' || character == '-' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func installError(kind, operation, path string, err error) *Error {
	return &Error{Kind: kind, Operation: operation, Path: path, Err: err}
}
