// Package macos validates untrusted inputs used by official macOS bundles.
package macos

import (
	"debug/macho"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	maxMachOSize = 1 << 30
	maxICNSSize  = 64 << 20
)

// ValidateMachO requires a thin 64-bit executable for the requested architecture.
func ValidateMachO(path, arch string) error {
	info, err := regularFile(path)
	if err != nil {
		return macError("validate Mach-O", path, err)
	}
	if info.Size() <= 0 || info.Size() > maxMachOSize {
		return macError("validate Mach-O", path, errors.New("file size is outside supported bounds"))
	}
	file, err := macho.Open(path)
	if err != nil {
		return macError("parse Mach-O", path, err)
	}
	defer func() { _ = file.Close() }()
	expected := macho.CpuAmd64
	if arch == "arm64" {
		expected = macho.CpuArm64
	} else if arch != "amd64" {
		return macError("validate Mach-O", path, fmt.Errorf("unsupported architecture %q", arch))
	}
	if file.Cpu != expected || file.Type != macho.TypeExec {
		return macError("validate Mach-O", path, fmt.Errorf("must be a thin %s executable", arch))
	}
	if len(file.Loads) == 0 || len(file.Loads) > 1024 {
		return macError("validate Mach-O", path, errors.New("invalid load command count"))
	}
	return nil
}

// ValidateICNS checks the bounded ICNS container and every chunk boundary.
func ValidateICNS(path string) error {
	info, err := regularFile(path)
	if err != nil {
		return macError("validate ICNS", path, err)
	}
	if info.Size() < 8 || info.Size() > maxICNSSize {
		return macError("validate ICNS", path, errors.New("file size is outside supported bounds"))
	}
	file, err := os.Open(path) // #nosec G304 -- caller-selected untrusted icon.
	if err != nil {
		return macError("open ICNS", path, err)
	}
	defer func() { _ = file.Close() }()
	header := make([]byte, 8)
	if _, err := io.ReadFull(file, header); err != nil {
		return macError("read ICNS", path, err)
	}
	if string(header[:4]) != "icns" || int64(binary.BigEndian.Uint32(header[4:])) != info.Size() {
		return macError("validate ICNS", path, errors.New("invalid header or declared size"))
	}
	offset := int64(8)
	chunks := 0
	iconChunks := 0
	for offset < info.Size() {
		if _, err := io.ReadFull(file, header); err != nil {
			return macError("read ICNS chunk", path, err)
		}
		size := int64(binary.BigEndian.Uint32(header[4:]))
		if size <= 8 || size > info.Size()-offset {
			return macError("validate ICNS chunk", path, errors.New("chunk escapes container"))
		}
		chunkType := string(header[:4])
		switch chunkType {
		case "icp4", "icp5", "icp6", "ic07", "ic08", "ic09", "ic10", "ic11", "ic12", "ic13", "ic14":
			iconChunks++
		}
		if _, err := file.Seek(size-8, io.SeekCurrent); err != nil {
			return macError("skip ICNS chunk", path, err)
		}
		offset += size
		chunks++
	}
	if chunks == 0 || iconChunks == 0 || offset != info.Size() {
		return macError("validate ICNS", path, errors.New("container has no complete icon chunks"))
	}
	return nil
}

func regularFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("must be a non-symlink regular file")
	}
	return info, nil
}

func macError(operation, path string, err error) *Error {
	return &Error{Operation: operation, Path: path, Err: err}
}
