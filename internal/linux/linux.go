// Package linux validates untrusted inputs used by official Linux releases.
package linux

import (
	"debug/elf"
	"errors"
	"fmt"
	"image/png"
	"os"
)

const (
	maxELFSize  = 1 << 30
	maxPNGSize  = 32 << 20
	maxIconSide = 4096
)

// ValidateELFAMD64 requires a bounded ELF64 x86-64 executable or PIE.
func ValidateELFAMD64(path string) error {
	info, err := regularFile(path)
	if err != nil {
		return linuxError("validate ELF", path, err)
	}
	if info.Size() <= 0 || info.Size() > maxELFSize {
		return linuxError("validate ELF", path, errors.New("file size is outside supported bounds"))
	}
	file, err := elf.Open(path)
	if err != nil {
		return linuxError("parse ELF", path, err)
	}
	defer func() { _ = file.Close() }()
	if file.Class != elf.ELFCLASS64 || file.Machine != elf.EM_X86_64 {
		return linuxError("validate ELF", path, fmt.Errorf("must be ELF64 x86-64"))
	}
	if file.Type != elf.ET_EXEC && file.Type != elf.ET_DYN {
		return linuxError("validate ELF", path, fmt.Errorf("must be an executable or PIE"))
	}
	if len(file.Progs) == 0 || len(file.Progs) > 256 {
		return linuxError("validate ELF", path, errors.New("invalid program header count"))
	}
	loadable := false
	for _, program := range file.Progs {
		if program.Type == elf.PT_LOAD && program.Flags&elf.PF_X != 0 {
			loadable = true
			break
		}
	}
	if !loadable {
		return linuxError("validate ELF", path, errors.New("no executable load segment"))
	}
	return nil
}

// ValidatePNG requires one bounded, decodable PNG suitable as an application icon.
func ValidatePNG(path string) error {
	info, err := regularFile(path)
	if err != nil {
		return linuxError("validate PNG", path, err)
	}
	if info.Size() <= 0 || info.Size() > maxPNGSize {
		return linuxError("validate PNG", path, errors.New("file size is outside supported bounds"))
	}
	file, err := os.Open(path) // #nosec G304 -- caller-selected untrusted icon.
	if err != nil {
		return linuxError("open PNG", path, err)
	}
	decoded, decodeErr := png.Decode(file)
	closeErr := file.Close()
	if decodeErr != nil || closeErr != nil {
		return linuxError("decode PNG", path, errors.Join(decodeErr, closeErr))
	}
	bounds := decoded.Bounds()
	if bounds.Dx() < 1 || bounds.Dy() < 1 ||
		bounds.Dx() > maxIconSide || bounds.Dy() > maxIconSide {
		return linuxError("validate PNG", path, errors.New("image dimensions are outside supported bounds"))
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

func linuxError(operation, path string, err error) *Error {
	return &Error{Operation: operation, Path: path, Err: err}
}
