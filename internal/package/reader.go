package flxpkg

import (
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/RodBarenco/fluxa-builder/internal/manifest"
)

// EntryInfo is verified metadata from the package file table.
type EntryInfo struct {
	Path         string
	Kind         string
	Compression  string
	Offset       uint64
	StoredSize   uint64
	OriginalSize uint64
	SHA256       string
}

// Info describes a fully verified package.
type Info struct {
	FormatVersion uint32
	Manifest      manifest.Manifest
	Entries       []EntryInfo
	Size          int64
	SHA256        string
}

// Verify treats a package as untrusted and validates all bytes and payloads.
func Verify(path string) (Info, error) {
	file, err := os.Open(path) // #nosec G304 -- caller-selected package is validated as untrusted input.
	if err != nil {
		return Info{}, packageError(ErrorIO, "open", path, err)
	}
	defer func() { _ = file.Close() }()
	stat, err := file.Stat()
	if err != nil {
		return Info{}, packageError(ErrorIO, "inspect", path, err)
	}
	if !stat.Mode().IsRegular() {
		return Info{}, packageError(ErrorInvalid, "validate", path, errors.New("package is not a regular file"))
	}
	if stat.Size() < int64(headerSize) {
		return Info{}, packageError(ErrorInvalid, "read header", path, errors.New("package is truncated"))
	}
	if stat.Size() < 0 || uint64(stat.Size()) > maxPackageSize { // #nosec G115 -- negativity checked first.
		return Info{}, packageError(ErrorLimit, "validate size", path, errors.New("package exceeds size limit"))
	}

	var header packageHeader
	if err := binary.Read(io.NewSectionReader(file, 0, int64(headerSize)), binary.LittleEndian, &header); err != nil {
		return Info{}, packageError(ErrorInvalid, "read header", path, err)
	}
	if header.Magic != packageMagic {
		return Info{}, packageError(ErrorInvalid, "validate magic", path, errors.New("not a Fluxa package"))
	}
	if header.FormatVersion != formatVersion {
		return Info{}, packageError(ErrorInvalid, "validate version", path, fmt.Errorf("unsupported package version %d", header.FormatVersion))
	}
	if header.Flags != 0 || header.Signature != [64]byte{} {
		return Info{}, packageError(ErrorInvalid, "validate flags", path, errors.New("unsupported flags or signature"))
	}
	fileSize := uint64(stat.Size()) // #nosec G115 -- nonnegative and bounded above.
	if err := validateLayout(header, fileSize); err != nil {
		return Info{}, err
	}

	actualBodyHash, err := hashSection(file, header.ManifestOffset, header.ManifestSize+header.TableSize+header.PayloadSize)
	if err != nil {
		return Info{}, packageError(ErrorIO, "hash package body", path, err)
	}
	if actualBodyHash != header.PackageHash {
		return Info{}, packageError(ErrorIntegrity, "verify package hash", path, errors.New("global SHA-256 mismatch"))
	}

	// Package size is bounded well below MaxInt64.
	manifestValue, err := manifest.Decode(io.NewSectionReader(file, int64(header.ManifestOffset), int64(header.ManifestSize))) // #nosec G115
	if err != nil {
		return Info{}, packageError(ErrorInvalid, "decode manifest", path, err)
	}
	entries, err := readTable(file, header, manifestValue)
	if err != nil {
		return Info{}, err
	}
	for _, entry := range entries {
		if err := verifyPayload(file, entry); err != nil {
			return Info{}, err
		}
	}
	fullHash, err := hashSection(file, 0, fileSize)
	if err != nil {
		return Info{}, packageError(ErrorIO, "hash package file", path, err)
	}
	infoEntries := make([]EntryInfo, len(entries))
	for index, entry := range entries {
		kind := "asset"
		if entry.Kind == 1 {
			kind = "program"
		}
		compression := "none"
		if entry.Compression == compressionZlib {
			compression = "zlib"
		}
		infoEntries[index] = EntryInfo{
			Path: entry.Path, Kind: kind, Compression: compression,
			Offset: entry.Offset, StoredSize: entry.StoredSize, OriginalSize: entry.OriginalSize,
			SHA256: hex.EncodeToString(entry.Hash[:]),
		}
	}
	return Info{
		FormatVersion: header.FormatVersion,
		Manifest:      manifestValue,
		Entries:       infoEntries,
		Size:          stat.Size(),
		SHA256:        hex.EncodeToString(fullHash[:]),
	}, nil
}

func validateLayout(header packageHeader, fileSize uint64) error {
	if header.ManifestOffset != headerSize || header.ManifestSize == 0 || header.ManifestSize > maxManifestSize {
		return packageError(ErrorInvalid, "validate layout", "", errors.New("invalid manifest region"))
	}
	tableOffset, ok := checkedAdd(header.ManifestOffset, header.ManifestSize)
	if !ok || header.TableOffset != tableOffset || header.TableSize < 4 || header.TableSize > maxTableSize {
		return packageError(ErrorInvalid, "validate layout", "", errors.New("invalid table region"))
	}
	payloadOffset, ok := checkedAdd(header.TableOffset, header.TableSize)
	if !ok || header.PayloadOffset != payloadOffset || header.PayloadSize > maxPayloadSize {
		return packageError(ErrorInvalid, "validate layout", "", errors.New("invalid payload region"))
	}
	end, ok := checkedAdd(header.PayloadOffset, header.PayloadSize)
	if !ok || end != fileSize {
		return packageError(ErrorInvalid, "validate layout", "", errors.New("payload does not end at package boundary"))
	}
	return nil
}

func readTable(file *os.File, header packageHeader, value manifest.Manifest) ([]tableEntry, error) {
	// Header layout was bounded by validateLayout and maxPackageSize.
	reader := io.NewSectionReader(file, int64(header.TableOffset), int64(header.TableSize)) // #nosec G115
	var count uint32
	if err := binary.Read(reader, binary.LittleEndian, &count); err != nil {
		return nil, packageError(ErrorInvalid, "read table count", "", err)
	}
	if count == 0 || count > maxEntries || int(count) != len(value.Files) {
		return nil, packageError(ErrorInvalid, "validate table count", "", errors.New("table and manifest file counts differ or exceed limit"))
	}
	entries := make([]tableEntry, 0, count)
	nextOffset := header.PayloadOffset
	previousPath := ""
	for index := uint32(0); index < count; index++ {
		var pathLength uint16
		var entry tableEntry
		if err := binary.Read(reader, binary.LittleEndian, &pathLength); err != nil {
			return nil, packageError(ErrorInvalid, "read table entry", "", err)
		}
		if pathLength == 0 || int(pathLength) > maxPathBytes {
			return nil, packageError(ErrorInvalid, "validate table path", "", errors.New("invalid path length"))
		}
		if err := binary.Read(reader, binary.LittleEndian, &entry.Kind); err != nil {
			return nil, packageError(ErrorInvalid, "read table entry", "", err)
		}
		if err := binary.Read(reader, binary.LittleEndian, &entry.Compression); err != nil {
			return nil, packageError(ErrorInvalid, "read table entry", "", err)
		}
		for _, target := range []any{&entry.Flags, &entry.Offset, &entry.StoredSize, &entry.OriginalSize, &entry.Hash} {
			if err := binary.Read(reader, binary.LittleEndian, target); err != nil {
				return nil, packageError(ErrorInvalid, "read table entry", "", err)
			}
		}
		pathBytes := make([]byte, pathLength)
		if _, err := io.ReadFull(reader, pathBytes); err != nil {
			return nil, packageError(ErrorInvalid, "read table path", "", err)
		}
		entry.Path = string(pathBytes)
		if !safePackagePath(entry.Path) || entry.Path <= previousPath || entry.Path != value.Files[index].Path {
			return nil, packageError(ErrorInvalid, "validate table path", entry.Path, errors.New("path is unsafe, unsorted, duplicate, or differs from manifest"))
		}
		if entry.Kind != 1 && entry.Kind != 2 {
			return nil, packageError(ErrorInvalid, "validate entry kind", entry.Path, errors.New("unknown entry kind"))
		}
		expectedKind := uint8(2)
		if value.Files[index].Kind == "program" {
			expectedKind = 1
		}
		if entry.Kind != expectedKind || entry.Flags != 0 {
			return nil, packageError(ErrorInvalid, "validate entry metadata", entry.Path, errors.New("entry kind or flags differ from manifest"))
		}
		if entry.Compression != compressionNone && entry.Compression != compressionZlib {
			return nil, packageError(ErrorInvalid, "validate compression", entry.Path, errors.New("unknown compression"))
		}
		if entry.Offset != nextOffset || entry.StoredSize > maxFileSize || entry.OriginalSize > maxFileSize {
			return nil, packageError(ErrorInvalid, "validate entry bounds", entry.Path, errors.New("entry offset overlaps, has a gap, or exceeds limits"))
		}
		next, ok := checkedAdd(nextOffset, entry.StoredSize)
		if !ok || next > header.PayloadOffset+header.PayloadSize {
			return nil, packageError(ErrorInvalid, "validate entry bounds", entry.Path, errors.New("entry escapes payload"))
		}
		nextOffset = next
		if value.Files[index].Size < 0 ||
			entry.OriginalSize != uint64(value.Files[index].Size) || // #nosec G115 -- negativity checked first.
			hex.EncodeToString(entry.Hash[:]) != value.Files[index].SHA256 {
			return nil, packageError(ErrorIntegrity, "validate entry manifest", entry.Path, errors.New("entry size or hash differs from manifest"))
		}
		if entry.Compression == compressionNone && entry.StoredSize != entry.OriginalSize {
			return nil, packageError(ErrorInvalid, "validate uncompressed size", entry.Path, errors.New("stored and original sizes differ"))
		}
		entries = append(entries, entry)
		previousPath = entry.Path
	}
	if nextOffset != header.PayloadOffset+header.PayloadSize {
		return nil, packageError(ErrorInvalid, "validate payload coverage", "", errors.New("table entries do not cover payload"))
	}
	current, err := reader.Seek(0, io.SeekCurrent)
	if err != nil || current < 0 || uint64(current) != header.TableSize { // #nosec G115 -- negativity checked first.
		return nil, packageError(ErrorInvalid, "validate table size", "", errors.New("table contains trailing or missing bytes"))
	}
	return entries, nil
}

func verifyPayload(file *os.File, entry tableEntry) error {
	// Entry bounds are below maxPackageSize and therefore below MaxInt64.
	section := io.NewSectionReader(file, int64(entry.Offset), int64(entry.StoredSize)) // #nosec G115
	var reader io.Reader = section
	var compressed io.Closer
	if entry.Compression == compressionZlib {
		zlibReader, err := zlib.NewReader(section)
		if err != nil {
			return packageError(ErrorInvalid, "open compressed payload", entry.Path, err)
		}
		compressed = zlibReader
		reader = zlibReader
	}
	hash := sha256.New()
	written, copyErr := io.Copy(hash, io.LimitReader(reader, int64(entry.OriginalSize)+1)) // #nosec G115 -- maxFileSize is 1 GiB.
	closeErr := error(nil)
	if compressed != nil {
		closeErr = compressed.Close()
	}
	if copyErr != nil || closeErr != nil {
		return packageError(ErrorInvalid, "read payload", entry.Path, errors.Join(copyErr, closeErr))
	}
	if written < 0 || uint64(written) != entry.OriginalSize { // #nosec G115 -- negativity checked first.
		return packageError(ErrorIntegrity, "verify payload size", entry.Path, fmt.Errorf("got %d bytes, expected %d", written, entry.OriginalSize))
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	if digest != entry.Hash {
		return packageError(ErrorIntegrity, "verify payload hash", entry.Path, errors.New("SHA-256 mismatch"))
	}
	return nil
}

func hashSection(file *os.File, offset, size uint64) ([32]byte, error) {
	hash := sha256.New()
	// All callers pass regions validated below maxPackageSize.
	if _, err := io.Copy(hash, io.NewSectionReader(file, int64(offset), int64(size))); err != nil { // #nosec G115
		return [32]byte{}, err
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}
