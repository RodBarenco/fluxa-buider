package flxpkg

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/RodBarenco/fluxa-builder/internal/manifest"
)

// Request contains canonical metadata and exact source paths for package files.
type Request struct {
	OutputPath string
	Manifest   manifest.Manifest
	Sources    map[string]string
	Compress   bool
}

// Result describes a verified, atomically published package.
type Result struct {
	Path       string
	Size       int64
	SHA256     string
	FileCount  int
	Compressed bool
}

type preparedFile struct {
	entry tableEntry
	path  string
}

// Write creates, syncs, reopens, verifies, and atomically publishes a package.
func Write(ctx context.Context, request Request) (Result, error) {
	if request.OutputPath == "" {
		return Result{}, packageError(ErrorInvalid, "validate output", "", errors.New("output path is required"))
	}
	manifestBytes, err := manifest.Encode(request.Manifest)
	if err != nil {
		return Result{}, packageError(ErrorInvalid, "encode manifest", "", err)
	}
	canonicalManifest, err := manifest.Decode(bytes.NewReader(manifestBytes))
	if err != nil {
		return Result{}, packageError(ErrorInvalid, "canonicalize manifest", "", err)
	}
	request.Manifest = canonicalManifest
	if len(request.Manifest.Files) == 0 {
		return Result{}, packageError(ErrorInvalid, "validate manifest", "", errors.New("empty package is not allowed"))
	}
	if len(request.Sources) != len(request.Manifest.Files) {
		return Result{}, packageError(ErrorInvalid, "validate sources", "", errors.New("source mapping must exactly match manifest files"))
	}

	parent := filepath.Dir(request.OutputPath)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return Result{}, packageError(ErrorIO, "inspect output directory", parent, err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return Result{}, packageError(ErrorInvalid, "validate output directory", parent, errors.New("must be an existing non-symlink directory"))
	}
	if _, err := os.Lstat(request.OutputPath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return Result{}, packageError(ErrorInvalid, "publish", request.OutputPath, errors.New("destination already exists"))
		}
		return Result{}, packageError(ErrorIO, "inspect destination", request.OutputPath, err)
	}

	prepared, cleanup, err := prepareFiles(ctx, parent, request)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()

	tableSize, err := tableEncodedSize(manifestPaths(request.Manifest))
	if err != nil {
		return Result{}, err
	}
	manifestOffset := headerSize
	tableOffset, ok := checkedAdd(manifestOffset, uint64(len(manifestBytes)))
	if !ok {
		return Result{}, packageError(ErrorLimit, "layout", "", errors.New("manifest offset overflow"))
	}
	payloadOffset, ok := checkedAdd(tableOffset, tableSize)
	if !ok {
		return Result{}, packageError(ErrorLimit, "layout", "", errors.New("table offset overflow"))
	}
	payloadSize := uint64(0)
	for index := range prepared {
		prepared[index].entry.Offset, ok = checkedAdd(payloadOffset, payloadSize)
		if !ok || payloadSize > maxPayloadSize-prepared[index].entry.StoredSize {
			return Result{}, packageError(ErrorLimit, "layout", prepared[index].entry.Path, errors.New("payload size overflow"))
		}
		payloadSize += prepared[index].entry.StoredSize
	}
	tableBytes, err := encodeTable(entriesOf(prepared))
	if err != nil {
		return Result{}, err
	}
	if uint64(len(tableBytes)) != tableSize {
		return Result{}, packageError(ErrorInvalid, "layout", "", errors.New("table size changed during encoding"))
	}

	packageDigest, err := hashBody(manifestBytes, tableBytes, prepared)
	if err != nil {
		return Result{}, err
	}
	header := packageHeader{
		Magic:          packageMagic,
		FormatVersion:  formatVersion,
		ManifestOffset: manifestOffset,
		ManifestSize:   uint64(len(manifestBytes)),
		TableOffset:    tableOffset,
		TableSize:      tableSize,
		PayloadOffset:  payloadOffset,
		PayloadSize:    payloadSize,
		PackageHash:    packageDigest,
	}
	headerBytes, err := encodeHeader(header)
	if err != nil {
		return Result{}, err
	}

	temp, err := os.CreateTemp(parent, ".flxpkg-*.tmp")
	if err != nil {
		return Result{}, packageError(ErrorIO, "create temporary package", request.OutputPath, err)
	}
	tempPath := temp.Name()
	published := false
	defer func() {
		_ = temp.Close()
		if !published {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return Result{}, packageError(ErrorIO, "secure temporary package", tempPath, err)
	}
	for _, chunk := range [][]byte{headerBytes, manifestBytes, tableBytes} {
		if _, err := temp.Write(chunk); err != nil {
			return Result{}, packageError(ErrorIO, "write package metadata", tempPath, err)
		}
	}
	for _, file := range prepared {
		if err := copyPrepared(temp, file); err != nil {
			return Result{}, err
		}
	}
	if err := temp.Sync(); err != nil {
		return Result{}, packageError(ErrorIO, "sync package", tempPath, err)
	}
	if err := temp.Close(); err != nil {
		return Result{}, packageError(ErrorIO, "close package", tempPath, err)
	}
	info, err := Verify(tempPath)
	if err != nil {
		return Result{}, packageError(ErrorIntegrity, "verify written package", tempPath, err)
	}
	if err := os.Link(tempPath, request.OutputPath); err != nil {
		return Result{}, packageError(ErrorIO, "publish package", request.OutputPath, err)
	}
	if err := syncDirectory(parent); err != nil {
		_ = os.Remove(request.OutputPath)
		return Result{}, packageError(ErrorIO, "sync output directory", parent, err)
	}
	if err := os.Remove(tempPath); err != nil {
		_ = os.Remove(request.OutputPath)
		return Result{}, packageError(ErrorIO, "remove published temporary", tempPath, err)
	}
	published = true
	return Result{
		Path:       request.OutputPath,
		Size:       info.Size,
		SHA256:     info.SHA256,
		FileCount:  len(info.Entries),
		Compressed: request.Compress,
	}, nil
}

func prepareFiles(ctx context.Context, parent string, request Request) ([]preparedFile, func(), error) {
	prepared := make([]preparedFile, 0, len(request.Manifest.Files))
	cleanup := func() {
		for _, file := range prepared {
			_ = os.Remove(file.path)
		}
	}
	for _, declared := range request.Manifest.Files {
		if err := ctx.Err(); err != nil {
			cleanup()
			return nil, func() {}, packageError(ErrorCanceled, "prepare", declared.Path, err)
		}
		sourcePath, exists := request.Sources[declared.Path]
		if !exists {
			cleanup()
			return nil, func() {}, packageError(ErrorInvalid, "resolve source", declared.Path, errors.New("source mapping is missing"))
		}
		temp, err := os.CreateTemp(parent, ".flxpkg-entry-*.tmp")
		if err != nil {
			cleanup()
			return nil, func() {}, packageError(ErrorIO, "create prepared file", declared.Path, err)
		}
		tempPath := temp.Name()
		if err := temp.Chmod(0o600); err != nil {
			_ = temp.Close()
			_ = os.Remove(tempPath)
			cleanup()
			return nil, func() {}, packageError(ErrorIO, "secure prepared file", declared.Path, err)
		}
		source, err := os.Open(sourcePath) // #nosec G304 -- source mapping comes from validated build outputs.
		if err != nil {
			_ = temp.Close()
			_ = os.Remove(tempPath)
			cleanup()
			return nil, func() {}, packageError(ErrorIO, "open source", declared.Path, err)
		}
		sourceInfo, statErr := source.Stat()
		if statErr != nil || !sourceInfo.Mode().IsRegular() {
			_ = source.Close()
			_ = temp.Close()
			_ = os.Remove(tempPath)
			cleanup()
			if statErr == nil {
				statErr = errors.New("source is not a regular file")
			}
			return nil, func() {}, packageError(ErrorInvalid, "inspect source", declared.Path, statErr)
		}
		hash := sha256.New()
		var writer io.Writer = temp
		compression := compressionNone
		var compressor *zlib.Writer
		if request.Compress {
			compressor, err = zlib.NewWriterLevel(temp, zlib.BestCompression)
			if err != nil {
				_ = source.Close()
				_ = temp.Close()
				_ = os.Remove(tempPath)
				cleanup()
				return nil, func() {}, packageError(ErrorIO, "initialize compression", declared.Path, err)
			}
			writer = compressor
			compression = compressionZlib
		}
		written, copyErr := io.Copy(writer, io.TeeReader(source, hash))
		sourceCloseErr := source.Close()
		compressCloseErr := error(nil)
		if compressor != nil {
			compressCloseErr = compressor.Close()
		}
		tempCloseErr := temp.Close()
		if copyErr != nil || sourceCloseErr != nil || compressCloseErr != nil || tempCloseErr != nil {
			_ = os.Remove(tempPath)
			cleanup()
			return nil, func() {}, packageError(ErrorIO, "prepare source", declared.Path, errors.Join(copyErr, sourceCloseErr, compressCloseErr, tempCloseErr))
		}
		storedInfo, err := os.Stat(tempPath)
		if err != nil {
			_ = os.Remove(tempPath)
			cleanup()
			return nil, func() {}, packageError(ErrorIO, "inspect prepared file", declared.Path, err)
		}
		actualHash := hex.EncodeToString(hash.Sum(nil))
		if written != declared.Size || sourceInfo.Size() != declared.Size || actualHash != declared.SHA256 {
			_ = os.Remove(tempPath)
			cleanup()
			return nil, func() {}, packageError(ErrorIntegrity, "validate source", declared.Path, errors.New("source size or SHA-256 differs from manifest"))
		}
		if written < 0 || storedInfo.Size() < 0 ||
			uint64(written) > maxFileSize || uint64(storedInfo.Size()) > maxFileSize { // #nosec G115 -- negativity checked first.
			_ = os.Remove(tempPath)
			cleanup()
			return nil, func() {}, packageError(ErrorLimit, "prepare source", declared.Path, errors.New("file exceeds package size limit"))
		}
		var digest [32]byte
		copy(digest[:], hash.Sum(nil))
		kind := uint8(2)
		if declared.Kind == "program" {
			kind = 1
		}
		prepared = append(prepared, preparedFile{
			entry: tableEntry{
				Path: declared.Path, Kind: kind, Compression: compression,
				StoredSize: uint64(storedInfo.Size()), OriginalSize: uint64(written), Hash: digest, // #nosec G115 -- checked above.
			},
			path: tempPath,
		})
	}
	return prepared, cleanup, nil
}

func hashBody(manifestBytes, tableBytes []byte, prepared []preparedFile) ([32]byte, error) {
	hash := sha256.New()
	_, _ = hash.Write(manifestBytes)
	_, _ = hash.Write(tableBytes)
	for _, file := range prepared {
		input, err := os.Open(file.path) // #nosec G304 -- private temporary path created above.
		if err != nil {
			return [32]byte{}, packageError(ErrorIO, "hash prepared file", file.entry.Path, err)
		}
		_, copyErr := io.Copy(hash, input)
		closeErr := input.Close()
		if copyErr != nil || closeErr != nil {
			return [32]byte{}, packageError(ErrorIO, "hash prepared file", file.entry.Path, errors.Join(copyErr, closeErr))
		}
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func copyPrepared(output io.Writer, file preparedFile) error {
	input, err := os.Open(file.path) // #nosec G304 -- private temporary path created above.
	if err != nil {
		return packageError(ErrorIO, "open prepared file", file.entry.Path, err)
	}
	_, copyErr := io.Copy(output, input)
	closeErr := input.Close()
	if copyErr != nil || closeErr != nil {
		return packageError(ErrorIO, "write payload", file.entry.Path, errors.Join(copyErr, closeErr))
	}
	return nil
}

func manifestPaths(value manifest.Manifest) []string {
	paths := make([]string, len(value.Files))
	for index, file := range value.Files {
		paths[index] = file.Path
	}
	return paths
}

func entriesOf(prepared []preparedFile) []tableEntry {
	entries := make([]tableEntry, len(prepared))
	for index := range prepared {
		entries[index] = prepared[index].entry
	}
	return entries
}

func checkedAdd(left, right uint64) (uint64, bool) {
	if left > ^uint64(0)-right {
		return 0, false
	}
	return left + right, true
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path) // #nosec G304 -- validated output directory.
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}
