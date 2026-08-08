package windows

import (
	"encoding/binary"
	"errors"
	"os"
)

// PE structure offsets and sizes this file reads or writes directly,
// independent of debug/pe (which exposes no resource- or section-table
// write path). All are from the Microsoft PE/COFF specification.
const (
	coffCharacteristicsOffset  = 18 // within the COFF file header.
	numberOfSectionsOffset     = 2  // within the COFF file header.
	optSizeOfImageOffset       = 56 // within OptionalHeader64.
	optSizeOfHeadersOffset     = 60 // within OptionalHeader64.
	optDataDirectoriesOffset   = 112
	resourceDataDirectoryIndex = 2
	dataDirectoryEntrySize     = 8
	sectionHeaderSize          = 40
	coffHeaderSize             = 20
	peSignatureSize            = 4

	imageResourceDirectorySize      = 16
	imageResourceDirectoryEntrySize = 8
	imageResourceDataEntrySize      = 16
	resourceHighBit                 = uint32(1) << 31

	resourceTypeIcon      = 3
	resourceTypeGroupIcon = 14
	resourceLanguageID    = 0x0409 // English (US); arbitrary but must be consistent top to bottom.
	groupIconResourceID   = 1

	imageScnCntInitializedData = 0x00000040
	imageScnMemRead            = 0x40000000
)

// EmbedIcon embeds icoPath as the given exePath's Windows Explorer /
// taskbar icon by appending a new .rsrc section containing RT_ICON and
// RT_GROUP_ICON resources, then pointing the PE's resource data directory
// at it. Both paths must already have passed ValidatePEAMD64/ValidateICO;
// this function re-derives everything it writes from the same parsing
// ValidateICO uses (parseICO), so it can never embed something ValidateICO
// itself would reject.
//
// This is purely additive: every existing byte of every existing section
// is left untouched. Only three existing fields are patched in place
// (NumberOfSections, SizeOfImage, and the resource data directory entry),
// plus one new IMAGE_SECTION_HEADER written into unused header padding
// that already existed in the file (never into space that had other data).
//
// It fails with ErrorUnsupported — a condition callers may treat as
// "skip embedding, ship the loose .ico as before" rather than aborting an
// entire build — when the PE already has a resource directory, or when its
// header has no room for one more section header. Any other failure
// indicates a real problem and should not be treated as skippable.
func EmbedIcon(exePath, icoPath string) error {
	images, err := parseICO(icoPath)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(exePath, os.O_RDWR, 0) // #nosec G304 -- validated application executable.
	if err != nil {
		return windowsError(ErrorIO, "open PE for icon embedding", exePath, err)
	}
	defer func() { _ = file.Close() }()

	layout, err := readPELayout(file, exePath)
	if err != nil {
		return err
	}
	if layout.resourceRVA != 0 || layout.resourceSize != 0 {
		return windowsError(ErrorUnsupported, "embed icon", exePath, errors.New("PE already has an embedded resource directory"))
	}
	headerSlack := layout.sizeOfHeaders - layout.sectionTableEnd
	if headerSlack < sectionHeaderSize {
		return windowsError(ErrorUnsupported, "embed icon", exePath, errors.New("PE header has no room for another section"))
	}

	sectionRVA := alignUp32(layout.lastSectionEndRVA, layout.sectionAlignment)
	content := buildIconResourceSection(images, sectionRVA)
	pointerToRawData := alignUp32(layout.fileSize, layout.fileAlignment)
	sizeOfRawData := alignUp32(uint32(len(content)), layout.fileAlignment)                //nolint:gosec // resource content is bounded by ValidateICO's own limits.
	newSizeOfImage := alignUp32(sectionRVA+uint32(len(content)), layout.sectionAlignment) //nolint:gosec // same bound as above.

	// 1. New section header, written into existing, previously-unused
	// header padding (already proven to exist above).
	header := make([]byte, sectionHeaderSize)
	copy(header[0:8], ".rsrc\x00\x00\x00")
	binary.LittleEndian.PutUint32(header[8:], uint32(len(content))) //nolint:gosec // bounded, see above.
	binary.LittleEndian.PutUint32(header[12:], sectionRVA)
	binary.LittleEndian.PutUint32(header[16:], sizeOfRawData)
	binary.LittleEndian.PutUint32(header[20:], pointerToRawData)
	binary.LittleEndian.PutUint32(header[36:], imageScnCntInitializedData|imageScnMemRead)
	if _, err := file.WriteAt(header, int64(layout.sectionTableEnd)); err != nil {
		return windowsError(ErrorIO, "write resource section header", exePath, err)
	}

	// 2. Patched existing fields.
	if err := writeUint16At(file, layout.numberOfSectionsOffset, layout.numberOfSections+1); err != nil {
		return windowsError(ErrorIO, "update section count", exePath, err)
	}
	if err := writeUint32At(file, layout.sizeOfImageOffset, newSizeOfImage); err != nil {
		return windowsError(ErrorIO, "update image size", exePath, err)
	}
	if err := writeUint32At(file, layout.resourceDirectoryOffset, sectionRVA); err != nil {
		return windowsError(ErrorIO, "update resource directory RVA", exePath, err)
	}
	if err := writeUint32At(file, layout.resourceDirectoryOffset+4, uint32(len(content))); err != nil { //nolint:gosec // bounded, see above.
		return windowsError(ErrorIO, "update resource directory size", exePath, err)
	}

	// 3. Appended section content, at the file-aligned offset the new
	// section header above already declared.
	padded := make([]byte, sizeOfRawData)
	copy(padded, content)
	if _, err := file.WriteAt(padded, int64(pointerToRawData)); err != nil {
		return windowsError(ErrorIO, "write resource section data", exePath, err)
	}
	if err := file.Sync(); err != nil {
		return windowsError(ErrorIO, "sync PE after icon embedding", exePath, err)
	}
	return nil
}

// peLayout is every existing-file fact EmbedIcon needs, gathered once so
// its own writes never have to re-derive an offset a different way twice.
type peLayout struct {
	numberOfSections        uint16
	numberOfSectionsOffset  int64
	sizeOfImageOffset       int64
	sizeOfHeaders           uint32
	sectionAlignment        uint32
	fileAlignment           uint32
	resourceDirectoryOffset int64
	resourceRVA             uint32
	resourceSize            uint32
	sectionTableEnd         uint32
	lastSectionEndRVA       uint32
	fileSize                uint32
}

func readPELayout(file *os.File, path string) (peLayout, error) {
	var dos [64]byte
	if _, err := file.ReadAt(dos[:], 0); err != nil || dos[0] != 'M' || dos[1] != 'Z' {
		if err == nil {
			err = errors.New("invalid DOS signature")
		}
		return peLayout{}, windowsError(ErrorInvalid, "read DOS header", path, err)
	}
	peOffset := int64(binary.LittleEndian.Uint32(dos[0x3c:0x40]))
	coffOffset := peOffset + peSignatureSize
	optOffset := coffOffset + coffHeaderSize

	var coff [coffHeaderSize]byte
	if _, err := file.ReadAt(coff[:], coffOffset); err != nil {
		return peLayout{}, windowsError(ErrorIO, "read COFF header", path, err)
	}
	numberOfSections := binary.LittleEndian.Uint16(coff[numberOfSectionsOffset:])
	sizeOfOptionalHeader := binary.LittleEndian.Uint16(coff[16:])

	optional := make([]byte, sizeOfOptionalHeader)
	if _, err := file.ReadAt(optional, optOffset); err != nil {
		return peLayout{}, windowsError(ErrorIO, "read optional header", path, err)
	}
	sizeOfHeaders := binary.LittleEndian.Uint32(optional[optSizeOfHeadersOffset:])
	sectionAlignment := binary.LittleEndian.Uint32(optional[32:])
	fileAlignment := binary.LittleEndian.Uint32(optional[36:])
	resourceEntryOffset := optDataDirectoriesOffset + resourceDataDirectoryIndex*dataDirectoryEntrySize
	resourceRVA := binary.LittleEndian.Uint32(optional[resourceEntryOffset:])
	resourceSize := binary.LittleEndian.Uint32(optional[resourceEntryOffset+4:])

	sectionTableOffset := optOffset + int64(sizeOfOptionalHeader)
	sections := make([]byte, int(numberOfSections)*sectionHeaderSize)
	if _, err := file.ReadAt(sections, sectionTableOffset); err != nil {
		return peLayout{}, windowsError(ErrorIO, "read section table", path, err)
	}
	var lastSectionEndRVA uint32
	var fileSize uint32
	for i := 0; i < int(numberOfSections); i++ {
		entry := sections[i*sectionHeaderSize : (i+1)*sectionHeaderSize]
		virtualSize := binary.LittleEndian.Uint32(entry[8:])
		virtualAddress := binary.LittleEndian.Uint32(entry[12:])
		sizeOfRawData := binary.LittleEndian.Uint32(entry[16:])
		pointerToRawData := binary.LittleEndian.Uint32(entry[20:])
		if end := virtualAddress + virtualSize; end > lastSectionEndRVA {
			lastSectionEndRVA = end
		}
		if end := pointerToRawData + sizeOfRawData; end > fileSize {
			fileSize = end
		}
	}
	sectionTableEnd := uint32(sectionTableOffset) + uint32(numberOfSections)*sectionHeaderSize //nolint:gosec // header offsets are small by construction.

	return peLayout{
		numberOfSections:        numberOfSections,
		numberOfSectionsOffset:  coffOffset + numberOfSectionsOffset,
		sizeOfImageOffset:       optOffset + optSizeOfImageOffset,
		sizeOfHeaders:           sizeOfHeaders,
		sectionAlignment:        sectionAlignment,
		fileAlignment:           fileAlignment,
		resourceDirectoryOffset: optOffset + int64(resourceEntryOffset),
		resourceRVA:             resourceRVA,
		resourceSize:            resourceSize,
		sectionTableEnd:         sectionTableEnd,
		lastSectionEndRVA:       lastSectionEndRVA,
		fileSize:                fileSize,
	}, nil
}

// buildIconResourceSection builds a complete .rsrc section body containing
// one RT_ICON resource per image plus one RT_GROUP_ICON resource
// referencing them, laid out as: type directory, RT_ICON ID directory,
// RT_GROUP_ICON ID directory, one language directory per RT_ICON entry,
// the RT_GROUP_ICON language directory, one data entry per RT_ICON entry,
// the RT_GROUP_ICON data entry, then the actual icon image bytes followed
// by the GRPICONDIR structure. sectionRVA is where this whole blob will
// live once appended to the PE — IMAGE_RESOURCE_DATA_ENTRY.OffsetToData
// fields are real image-base-relative RVAs, not section-relative offsets,
// so this function needs to know it up front.
func buildIconResourceSection(images []icoImage, sectionRVA uint32) []byte {
	n := uint32(len(images)) //nolint:gosec // ValidateICO already bounds image count well under uint32.

	typeDirOffset := uint32(0)
	typeDirSize := uint32(imageResourceDirectorySize + 2*imageResourceDirectoryEntrySize)

	iconIDDirOffset := typeDirOffset + typeDirSize
	iconIDDirSize := uint32(imageResourceDirectorySize) + n*imageResourceDirectoryEntrySize

	groupIDDirOffset := iconIDDirOffset + iconIDDirSize
	groupIDDirSize := uint32(imageResourceDirectorySize + imageResourceDirectoryEntrySize)

	iconLangDirSize := uint32(imageResourceDirectorySize + imageResourceDirectoryEntrySize)
	iconLangDirsOffset := groupIDDirOffset + groupIDDirSize

	groupLangDirOffset := iconLangDirsOffset + n*iconLangDirSize
	groupLangDirSize := uint32(imageResourceDirectorySize + imageResourceDirectoryEntrySize)

	iconDataEntriesOffset := groupLangDirOffset + groupLangDirSize
	groupDataEntryOffset := iconDataEntriesOffset + n*imageResourceDataEntrySize

	dataBlobsOffset := groupDataEntryOffset + imageResourceDataEntrySize

	groupIconDirSize := uint32(6 + 14*len(images)) //nolint:gosec // len(images) is bounded by parseICO's maxICOImages (256).

	buf := make([]byte, dataBlobsOffset)

	putDirHeader := func(at, idEntryCount uint32) {
		binary.LittleEndian.PutUint16(buf[at+12:], 0)                    //nolint:gosec // idEntryCount is small.
		binary.LittleEndian.PutUint16(buf[at+14:], uint16(idEntryCount)) //nolint:gosec // idEntryCount is small.
	}
	putDirEntry := func(at, id, value uint32, isSubdirectory bool) {
		binary.LittleEndian.PutUint32(buf[at:], id)
		if isSubdirectory {
			value |= resourceHighBit
		}
		binary.LittleEndian.PutUint32(buf[at+4:], value)
	}

	// Type directory: RT_ICON then RT_GROUP_ICON, numeric ID ascending.
	putDirHeader(typeDirOffset, 2)
	putDirEntry(typeDirOffset+imageResourceDirectorySize, resourceTypeIcon, iconIDDirOffset, true)
	putDirEntry(typeDirOffset+imageResourceDirectorySize+imageResourceDirectoryEntrySize, resourceTypeGroupIcon, groupIDDirOffset, true)

	// RT_ICON ID directory: one entry per image, ID = 1..N.
	putDirHeader(iconIDDirOffset, n)
	for i := range images {
		entryOffset := iconIDDirOffset + imageResourceDirectorySize + uint32(i)*imageResourceDirectoryEntrySize //nolint:gosec // i is bounded by ValidateICO's image-count limit.
		langDirOffset := iconLangDirsOffset + uint32(i)*iconLangDirSize                                         //nolint:gosec // same bound.
		putDirEntry(entryOffset, uint32(i+1), langDirOffset, true)                                              //nolint:gosec // same bound.
	}

	// RT_GROUP_ICON ID directory: one entry, fixed ID.
	putDirHeader(groupIDDirOffset, 1)
	putDirEntry(groupIDDirOffset+imageResourceDirectorySize, groupIconResourceID, groupLangDirOffset, true)

	// Per-icon language directories, each with one leaf entry.
	for i := range images {
		at := iconLangDirsOffset + uint32(i)*iconLangDirSize //nolint:gosec // bounded, see above.
		putDirHeader(at, 1)
		dataEntryOffset := iconDataEntriesOffset + uint32(i)*imageResourceDataEntrySize //nolint:gosec // bounded, see above.
		putDirEntry(at+imageResourceDirectorySize, resourceLanguageID, dataEntryOffset, false)
	}

	// RT_GROUP_ICON language directory, one leaf entry.
	putDirHeader(groupLangDirOffset, 1)
	putDirEntry(groupLangDirOffset+imageResourceDirectorySize, resourceLanguageID, groupDataEntryOffset, false)

	// Data entries + the actual bytes they describe.
	dataOffset := dataBlobsOffset
	for i, image := range images {
		at := iconDataEntriesOffset + uint32(i)*imageResourceDataEntrySize //nolint:gosec // bounded, see above.
		binary.LittleEndian.PutUint32(buf[at:], sectionRVA+dataOffset)
		binary.LittleEndian.PutUint32(buf[at+4:], uint32(len(image.data))) //nolint:gosec // ValidateICO bounds each image's size.
		buf = append(buf, image.data...)
		dataOffset += uint32(len(image.data)) //nolint:gosec // same bound.
	}
	groupIconDirRVA := sectionRVA + dataOffset
	binary.LittleEndian.PutUint32(buf[groupDataEntryOffset:], groupIconDirRVA)
	binary.LittleEndian.PutUint32(buf[groupDataEntryOffset+4:], groupIconDirSize)

	// GRPICONDIR + GRPICONDIRENTRY[] (the RT_GROUP_ICON resource's own data).
	group := make([]byte, groupIconDirSize)
	binary.LittleEndian.PutUint16(group[2:], 1)                   // Type.
	binary.LittleEndian.PutUint16(group[4:], uint16(len(images))) //nolint:gosec // ValidateICO bounds image count.
	for i, image := range images {
		entry := group[6+i*14 : 6+(i+1)*14]
		entry[0] = image.width
		entry[1] = image.height
		entry[2] = image.colorCount
		binary.LittleEndian.PutUint16(entry[4:], image.planes)
		binary.LittleEndian.PutUint16(entry[6:], image.bitCount)
		binary.LittleEndian.PutUint32(entry[8:], uint32(len(image.data))) //nolint:gosec // same bound.
		binary.LittleEndian.PutUint16(entry[12:], uint16(i+1))            //nolint:gosec // bounded, see above.
	}
	buf = append(buf, group...)

	return buf
}

func writeUint16At(file *os.File, offset int64, value uint16) error {
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], value)
	_, err := file.WriteAt(encoded[:], offset)
	return err
}

func writeUint32At(file *os.File, offset int64, value uint32) error {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	_, err := file.WriteAt(encoded[:], offset)
	return err
}

func alignUp32(value, alignment uint32) uint32 {
	if alignment == 0 {
		return value
	}
	remainder := value % alignment
	if remainder == 0 {
		return value
	}
	return value + (alignment - remainder)
}
