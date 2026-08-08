package windows

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

const (
	maxPESize         int64 = 512 * 1024 * 1024
	maxICOSize        int64 = 16 * 1024 * 1024
	maxICOImages            = 256
	peSubsystemOffset       = int64(68)
	subsystemGUI            = uint16(2)
	subsystemConsole        = uint16(3)
)

// ConfigureTerminal selects the Windows PE subsystem used by the application
// launcher. GUI applications do not create a console window; CLI applications do.
func ConfigureTerminal(path string, terminal bool) error {
	if err := ValidatePEAMD64(path); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0) // #nosec G304 -- validated application launcher.
	if err != nil {
		return windowsError(ErrorIO, "open PE for subsystem update", path, err)
	}
	defer func() { _ = file.Close() }()
	var dos [64]byte
	if _, err := file.ReadAt(dos[:], 0); err != nil || dos[0] != 'M' || dos[1] != 'Z' {
		if err == nil {
			err = errors.New("invalid DOS signature")
		}
		return windowsError(ErrorInvalid, "read DOS header", path, err)
	}
	peOffset := int64(binary.LittleEndian.Uint32(dos[0x3c:0x40]))
	offset := peOffset + 4 + 20 + peSubsystemOffset
	subsystem := subsystemGUI
	if terminal {
		subsystem = subsystemConsole
	}
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], subsystem)
	if _, err := file.WriteAt(encoded[:], offset); err != nil {
		return windowsError(ErrorIO, "write PE subsystem", path, err)
	}
	if err := file.Sync(); err != nil {
		return windowsError(ErrorIO, "sync PE subsystem", path, err)
	}
	return nil
}

var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

// ValidatePEAMD64 validates the structural identity of an official Windows x64 runtime.
func ValidatePEAMD64(path string) error {
	info, err := regularFile(path)
	if err != nil {
		return err
	}
	if info.Size() <= 0 || info.Size() > maxPESize {
		return windowsError(ErrorLimit, "validate PE size", path, errors.New("PE size is outside supported bounds"))
	}
	file, err := pe.Open(path)
	if err != nil {
		return windowsError(ErrorInvalid, "open PE", path, err)
	}
	defer func() { _ = file.Close() }()
	if file.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return windowsError(ErrorInvalid, "validate PE machine", path,
			fmt.Errorf("machine is %#x, expected AMD64", file.Machine))
	}
	if file.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 ||
		file.Characteristics&pe.IMAGE_FILE_DLL != 0 {
		return windowsError(ErrorInvalid, "validate PE characteristics", path, errors.New("file must be an executable image and not a DLL"))
	}
	if _, ok := file.OptionalHeader.(*pe.OptionalHeader64); !ok {
		return windowsError(ErrorInvalid, "validate PE optional header", path, errors.New("x64 runtime must use PE32+"))
	}
	if len(file.Sections) == 0 || len(file.Sections) > 96 {
		return windowsError(ErrorInvalid, "validate PE sections", path, errors.New("section count is outside supported bounds"))
	}
	return nil
}

// ValidateICO validates directory bounds and the basic shape of every ICO image.
func ValidateICO(path string) error {
	_, err := parseICO(path)
	return err
}

// icoImage is one parsed, bounds-checked ICO directory entry: the raw
// header fields a GRPICONDIRENTRY needs verbatim, plus the extracted image
// bytes (PNG or DIB, exactly as ValidateICO already accepted).
type icoImage struct {
	width, height, colorCount uint8
	planes, bitCount          uint16
	data                      []byte
}

// parseICO is ValidateICO's validation logic, refactored to also return the
// parsed per-image records EmbedIcon needs — the two must never disagree
// about what is a valid ICO, so there is exactly one parser.
func parseICO(path string) ([]icoImage, error) {
	info, err := regularFile(path)
	if err != nil {
		return nil, err
	}
	if info.Size() < 6+16 || info.Size() > maxICOSize {
		return nil, windowsError(ErrorLimit, "validate ICO size", path, errors.New("ICO size is outside supported bounds"))
	}
	data, err := os.ReadFile(path) // #nosec G304 -- caller-selected ICO is validated as untrusted input.
	if err != nil {
		return nil, windowsError(ErrorIO, "read ICO", path, err)
	}
	reserved := binary.LittleEndian.Uint16(data[0:2])
	imageType := binary.LittleEndian.Uint16(data[2:4])
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if reserved != 0 || imageType != 1 || count == 0 || count > maxICOImages {
		return nil, windowsError(ErrorInvalid, "validate ICO header", path, errors.New("invalid reserved, type, or image count"))
	}
	tableEnd := 6 + count*16
	if tableEnd > len(data) {
		return nil, windowsError(ErrorInvalid, "validate ICO directory", path, errors.New("image directory is truncated"))
	}
	images := make([]icoImage, 0, count)
	for index := 0; index < count; index++ {
		entry := data[6+index*16 : 6+(index+1)*16]
		size := uint64(binary.LittleEndian.Uint32(entry[8:12]))
		offset := uint64(binary.LittleEndian.Uint32(entry[12:16]))
		end, ok := checkedAdd(offset, size)
		if size < 8 || offset < uint64(tableEnd) || !ok || end > uint64(len(data)) {
			return nil, windowsError(ErrorInvalid, "validate ICO image", path, fmt.Errorf("image %d has invalid bounds", index))
		}
		image := data[offset:end]
		if !bytes.HasPrefix(image, pngSignature) {
			dibSize := binary.LittleEndian.Uint32(image[0:4])
			if dibSize != 40 && dibSize != 108 && dibSize != 124 {
				return nil, windowsError(ErrorInvalid, "validate ICO image", path, fmt.Errorf("image %d is neither PNG nor supported DIB", index))
			}
		}
		images = append(images, icoImage{
			width: entry[0], height: entry[1], colorCount: entry[2],
			planes:   binary.LittleEndian.Uint16(entry[4:6]),
			bitCount: binary.LittleEndian.Uint16(entry[6:8]),
			data:     image,
		})
	}
	return images, nil
}

func regularFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, windowsError(ErrorIO, "inspect", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, windowsError(ErrorInvalid, "validate", path, errors.New("must be a non-symlink regular file"))
	}
	return info, nil
}

func checkedAdd(left, right uint64) (uint64, bool) {
	result := left + right
	return result, result >= left
}
