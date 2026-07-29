package embedded

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
)

const (
	// CurrentVersion is the embedded footer format version.
	CurrentVersion uint32 = 1
	// FooterSize is the exact encoded FluxaEmbeddedFooter size.
	FooterSize int64 = 8 + 4 + 8 + 8 + 32

	maxRuntimeSize    int64 = 1 << 30
	maxPackageSize    int64 = 2 << 30
	maxExecutableSize       = maxRuntimeSize + maxPackageSize + FooterSize
)

var footerMagic = [8]byte{'F', 'L', 'X', 'E', 'M', 'B', 'E', 'D'}

// Request contains verified inputs for a single-file executable.
type Request struct {
	RuntimePath  string
	PackagePath  string
	OutputPath   string
	PackageHash  string
	ExecutableOS string
}

// Info describes a fully verified embedded executable.
type Info struct {
	Path          string
	Size          int64
	SHA256        string
	RuntimeSize   int64
	PackageOffset uint64
	PackageSize   uint64
	PackageSHA256 string
	Package       flxpkg.Info
}

type footer struct {
	Magic         [8]byte
	Version       uint32
	PackageOffset uint64
	PackageSize   uint64
	PackageHash   [32]byte
}

// Build appends a verified FLXPKG and footer to an unmodified runtime copy.
func Build(ctx context.Context, request Request) (Info, error) {
	if request.RuntimePath == "" || request.PackagePath == "" || request.OutputPath == "" ||
		request.PackageHash == "" || request.ExecutableOS == "" {
		return Info{}, embeddedError(ErrorInvalid, "validate build request", "", errors.New("runtime, package, output, hash, and target OS are required"))
	}
	if request.ExecutableOS != "windows" && request.ExecutableOS != "linux" && request.ExecutableOS != "macos" {
		return Info{}, embeddedError(ErrorInvalid, "validate target OS", request.ExecutableOS, errors.New("unsupported target OS"))
	}
	runtimeInfo, err := regularFile(request.RuntimePath, "runtime")
	if err != nil {
		return Info{}, err
	}
	if runtimeInfo.Size() == 0 || runtimeInfo.Size() > maxRuntimeSize {
		return Info{}, embeddedError(ErrorLimit, "validate runtime size", request.RuntimePath, errors.New("runtime size is outside supported bounds"))
	}
	if request.ExecutableOS != "windows" && runtimeInfo.Mode().Perm()&0o111 == 0 {
		return Info{}, embeddedError(ErrorInvalid, "validate runtime permission", request.RuntimePath, errors.New("runtime is not executable"))
	}
	packageInfo, err := flxpkg.Verify(request.PackagePath)
	if err != nil {
		return Info{}, embeddedError(ErrorIntegrity, "verify package", request.PackagePath, err)
	}
	if packageInfo.SHA256 != request.PackageHash {
		return Info{}, embeddedError(ErrorIntegrity, "verify package identity", request.PackagePath, errors.New("package hash differs from build result"))
	}
	if packageInfo.Size <= 0 || packageInfo.Size > maxPackageSize {
		return Info{}, embeddedError(ErrorLimit, "validate package size", request.PackagePath, errors.New("package size is outside supported bounds"))
	}
	if runtimeInfo.Size() > maxExecutableSize-packageInfo.Size-FooterSize {
		return Info{}, embeddedError(ErrorLimit, "validate executable size", request.OutputPath, errors.New("embedded executable exceeds size limit"))
	}
	packageHash, err := hex.DecodeString(packageInfo.SHA256)
	if err != nil || len(packageHash) != sha256.Size {
		return Info{}, embeddedError(ErrorIntegrity, "decode package hash", request.PackagePath, errors.New("invalid package SHA-256"))
	}

	parentInfo, err := os.Lstat(filepath.Dir(request.OutputPath))
	if err != nil || parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return Info{}, embeddedError(ErrorInvalid, "validate output directory", filepath.Dir(request.OutputPath), errors.New("must be an existing non-symlink directory"))
	}
	mode := os.FileMode(0o700)
	if request.ExecutableOS == "windows" {
		mode = 0o600
	}
	output, err := os.OpenFile(request.OutputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode) // #nosec G304 -- validated output parent.
	if err != nil {
		return Info{}, embeddedError(ErrorIO, "create executable", request.OutputPath, err)
	}
	complete := false
	defer func() {
		_ = output.Close()
		if !complete {
			_ = os.Remove(request.OutputPath)
		}
	}()
	if err := appendFile(ctx, output, request.RuntimePath); err != nil {
		return Info{}, err
	}
	if err := appendFile(ctx, output, request.PackagePath); err != nil {
		return Info{}, err
	}
	var packageDigest [32]byte
	copy(packageDigest[:], packageHash)
	if err := writeFooter(output, footer{
		Magic: footerMagic, Version: CurrentVersion,
		PackageOffset: uint64(runtimeInfo.Size()), // #nosec G115 -- positive and bounded.
		PackageSize:   uint64(packageInfo.Size),   // #nosec G115 -- positive and bounded.
		PackageHash:   packageDigest,
	}); err != nil {
		return Info{}, embeddedError(ErrorIO, "write footer", request.OutputPath, err)
	}
	if err := errors.Join(output.Sync(), output.Close()); err != nil {
		return Info{}, embeddedError(ErrorIO, "finish executable", request.OutputPath, err)
	}
	result, err := Verify(request.OutputPath)
	if err != nil {
		return Info{}, err
	}
	complete = true
	return result, nil
}

// Verify validates footer boundaries, package bytes, FLXPKG structure, and final hash.
func Verify(path string) (Info, error) {
	fileInfo, err := regularFile(path, "embedded executable")
	if err != nil {
		return Info{}, err
	}
	if fileInfo.Size() < FooterSize+1 {
		return Info{}, embeddedError(ErrorInvalid, "read footer", path, errors.New("footer is absent or executable is truncated"))
	}
	if fileInfo.Size() > maxExecutableSize {
		return Info{}, embeddedError(ErrorLimit, "validate executable size", path, errors.New("embedded executable exceeds size limit"))
	}
	file, err := os.Open(path) // #nosec G304 -- caller-selected untrusted executable.
	if err != nil {
		return Info{}, embeddedError(ErrorIO, "open executable", path, err)
	}
	defer func() { _ = file.Close() }()
	value, err := readFooter(file, fileInfo.Size()-FooterSize)
	if err != nil {
		return Info{}, embeddedError(ErrorInvalid, "read footer", path, err)
	}
	if value.Magic != footerMagic {
		return Info{}, embeddedError(ErrorInvalid, "validate footer magic", path, errors.New("embedded footer is absent or corrupt"))
	}
	if value.Version != CurrentVersion {
		return Info{}, embeddedError(ErrorInvalid, "validate footer version", path, fmt.Errorf("unsupported version %d", value.Version))
	}
	footerOffset := uint64(fileInfo.Size() - FooterSize) // #nosec G115 -- size checked positive.
	if value.PackageOffset == 0 || value.PackageOffset > uint64(maxRuntimeSize) {
		return Info{}, embeddedError(ErrorInvalid, "validate package offset", path, errors.New("package offset is outside supported bounds"))
	}
	if value.PackageSize == 0 || value.PackageSize > uint64(maxPackageSize) {
		return Info{}, embeddedError(ErrorInvalid, "validate package size", path, errors.New("package size is outside supported bounds"))
	}
	packageEnd, ok := checkedAdd(value.PackageOffset, value.PackageSize)
	if !ok || packageEnd != footerOffset {
		return Info{}, embeddedError(ErrorInvalid, "validate package boundaries", path, errors.New("package does not end exactly at footer"))
	}
	actualPackageHash, err := hashSection(file, value.PackageOffset, value.PackageSize)
	if err != nil {
		return Info{}, embeddedError(ErrorIO, "hash embedded package", path, err)
	}
	if actualPackageHash != value.PackageHash {
		return Info{}, embeddedError(ErrorIntegrity, "verify embedded package hash", path, errors.New("package SHA-256 mismatch"))
	}
	section := io.NewSectionReader(file, int64(value.PackageOffset), int64(value.PackageSize))  // #nosec G115 -- values bounded above.
	packageInfo, err := flxpkg.VerifyReader(section, int64(value.PackageSize), path+"#package") // #nosec G115 -- value bounded above.
	if err != nil {
		return Info{}, embeddedError(ErrorIntegrity, "verify embedded package", path, err)
	}
	executableHash, err := hashSection(file, 0, uint64(fileInfo.Size())) // #nosec G115 -- positive and bounded.
	if err != nil {
		return Info{}, embeddedError(ErrorIO, "hash executable", path, err)
	}
	return Info{
		Path: path, Size: fileInfo.Size(), SHA256: hex.EncodeToString(executableHash[:]),
		RuntimeSize:   int64(value.PackageOffset), // #nosec G115 -- bounded to maxRuntimeSize.
		PackageOffset: value.PackageOffset, PackageSize: value.PackageSize,
		PackageSHA256: hex.EncodeToString(actualPackageHash[:]), Package: packageInfo,
	}, nil
}

func regularFile(path, label string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, embeddedError(ErrorIO, "inspect "+label, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, embeddedError(ErrorInvalid, "validate "+label, path, errors.New("must be a non-symlink regular file"))
	}
	return info, nil
}

func appendFile(ctx context.Context, destination io.Writer, path string) error {
	source, err := os.Open(path) // #nosec G304 -- validated build input.
	if err != nil {
		return embeddedError(ErrorIO, "open input", path, err)
	}
	defer func() { _ = source.Close() }()
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return embeddedError(ErrorCanceled, "append input", path, err)
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			if _, err := destination.Write(buffer[:read]); err != nil {
				return embeddedError(ErrorIO, "write input", path, err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return embeddedError(ErrorIO, "read input", path, readErr)
		}
	}
}

func writeFooter(writer io.Writer, value footer) error {
	if _, err := writer.Write(value.Magic[:]); err != nil {
		return err
	}
	for _, number := range []any{value.Version, value.PackageOffset, value.PackageSize} {
		if err := binary.Write(writer, binary.LittleEndian, number); err != nil {
			return err
		}
	}
	_, err := writer.Write(value.PackageHash[:])
	return err
}

func readFooter(reader io.ReaderAt, offset int64) (footer, error) {
	var value footer
	section := io.NewSectionReader(reader, offset, FooterSize)
	if _, err := io.ReadFull(section, value.Magic[:]); err != nil {
		return value, err
	}
	for _, target := range []any{&value.Version, &value.PackageOffset, &value.PackageSize} {
		if err := binary.Read(section, binary.LittleEndian, target); err != nil {
			return value, err
		}
	}
	if _, err := io.ReadFull(section, value.PackageHash[:]); err != nil {
		return value, err
	}
	return value, nil
}

func hashSection(reader io.ReaderAt, offset, size uint64) ([32]byte, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, io.NewSectionReader(reader, int64(offset), int64(size))); err != nil { // #nosec G115 -- callers enforce bounds.
		return [32]byte{}, err
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func checkedAdd(left, right uint64) (uint64, bool) {
	result := left + right
	return result, result >= left
}
