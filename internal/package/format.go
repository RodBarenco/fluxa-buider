package flxpkg

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	formatVersion   = uint32(1)
	headerSize      = uint64(160)
	maxEntries      = uint32(100_000)
	maxPathBytes    = 4096
	maxManifestSize = uint64(16 * 1024 * 1024)
	maxTableSize    = uint64(512 * 1024 * 1024)
	maxFileSize     = uint64(1 << 30)
	maxPayloadSize  = uint64(16 << 30)
	maxPackageSize  = headerSize + maxManifestSize + maxTableSize + maxPayloadSize
	compressionNone = uint8(0)
	compressionZlib = uint8(1)
	entryFixedSize  = uint64(64)
)

var packageMagic = [8]byte{'F', 'L', 'X', 'P', 'K', 'G', 0x0d, 0x0a}

type packageHeader struct {
	Magic          [8]byte
	FormatVersion  uint32
	Flags          uint32
	ManifestOffset uint64
	ManifestSize   uint64
	TableOffset    uint64
	TableSize      uint64
	PayloadOffset  uint64
	PayloadSize    uint64
	PackageHash    [32]byte
	Signature      [64]byte
}

type tableEntry struct {
	Path         string
	Kind         uint8
	Compression  uint8
	Flags        uint32
	Offset       uint64
	StoredSize   uint64
	OriginalSize uint64
	Hash         [32]byte
}

func encodeHeader(header packageHeader) ([]byte, error) {
	var output bytes.Buffer
	if err := binary.Write(&output, binary.LittleEndian, header); err != nil {
		return nil, packageError(ErrorInvalid, "encode header", "", err)
	}
	if output.Len() != int(headerSize) {
		return nil, packageError(ErrorInvalid, "encode header", "", fmt.Errorf("internal size %d, expected %d", output.Len(), headerSize))
	}
	return output.Bytes(), nil
}

func encodeTable(entries []tableEntry) ([]byte, error) {
	if len(entries) == 0 {
		return nil, packageError(ErrorInvalid, "encode table", "", errors.New("package must contain at least one file"))
	}
	if len(entries) > int(maxEntries) {
		return nil, packageError(ErrorLimit, "encode table", "", fmt.Errorf("file count exceeds %d", maxEntries))
	}
	var output bytes.Buffer
	count := uint32(len(entries)) // #nosec G115 -- bounded by maxEntries above.
	if err := binary.Write(&output, binary.LittleEndian, count); err != nil {
		return nil, packageError(ErrorInvalid, "encode table", "", err)
	}
	for _, entry := range entries {
		if !safePackagePath(entry.Path) || len(entry.Path) > maxPathBytes {
			return nil, packageError(ErrorInvalid, "encode table", entry.Path, errors.New("invalid package path"))
		}
		pathLength := uint16(len(entry.Path)) // #nosec G115 -- bounded by maxPathBytes above.
		if err := binary.Write(&output, binary.LittleEndian, pathLength); err != nil {
			return nil, packageError(ErrorInvalid, "encode table", entry.Path, err)
		}
		if err := output.WriteByte(entry.Kind); err != nil {
			return nil, packageError(ErrorInvalid, "encode table", entry.Path, err)
		}
		if err := output.WriteByte(entry.Compression); err != nil {
			return nil, packageError(ErrorInvalid, "encode table", entry.Path, err)
		}
		for _, value := range []any{entry.Flags, entry.Offset, entry.StoredSize, entry.OriginalSize, entry.Hash} {
			if err := binary.Write(&output, binary.LittleEndian, value); err != nil {
				return nil, packageError(ErrorInvalid, "encode table", entry.Path, err)
			}
		}
		if _, err := output.WriteString(entry.Path); err != nil {
			return nil, packageError(ErrorInvalid, "encode table", entry.Path, err)
		}
	}
	if output.Len() > int(maxTableSize) {
		return nil, packageError(ErrorLimit, "encode table", "", errors.New("table exceeds size limit"))
	}
	return output.Bytes(), nil
}

func tableEncodedSize(paths []string) (uint64, error) {
	size := uint64(4)
	for _, path := range paths {
		if !safePackagePath(path) || len(path) > maxPathBytes {
			return 0, packageError(ErrorInvalid, "size table", path, errors.New("invalid package path"))
		}
		addition := entryFixedSize + uint64(len(path))
		if size > maxTableSize-addition {
			return 0, packageError(ErrorLimit, "size table", "", errors.New("table size overflow"))
		}
		size += addition
	}
	return size, nil
}

func safePackagePath(value string) bool {
	if value == "" || value == "." || value == ".." || strings.Contains(value, `\`) ||
		strings.HasPrefix(value, "/") || strings.ContainsRune(value, 0) ||
		strings.Contains(strings.Split(value, "/")[0], ":") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(value))) == value
}

func packageError(kind ErrorKind, operation, path string, err error) *Error {
	return &Error{Kind: kind, Operation: operation, Path: path, Err: err}
}
