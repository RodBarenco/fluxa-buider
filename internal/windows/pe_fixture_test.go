package windows_test

import (
	"encoding/binary"
)

// syntheticPEOptions controls the minimal, hand-built PE32+ fixture
// buildSyntheticPE produces. This is the only synthetic PE fixture in this
// package — every other PE test requires a real Windows host and
// os.Executable() as the fixture — because EmbedIcon's tests need full
// control over header slack and the resource data directory to exercise
// both the happy path and the graceful-degradation paths without a real
// Windows-linked binary.
type syntheticPEOptions struct {
	// sizeOfHeaders is the file-aligned header size recorded in the
	// optional header. The section table itself always ends at byte 472;
	// any slack beyond that is headroom for EmbedIcon to insert one more
	// IMAGE_SECTION_HEADER (40 bytes) without relocating existing section
	// data.
	sizeOfHeaders uint32
	// resourceDataDirectory, when true, pre-populates Data Directory[2]
	// (the resource table) with a nonzero placeholder, simulating a PE
	// that already carries embedded resources.
	resourceDataDirectory bool
}

const (
	fileAlignment    = 0x200
	sectionAlignment = 0x1000
	sectionTableEnd  = 472 // DOS header + PE header + 2 section headers, see buildSyntheticPE.
)

// buildSyntheticPE returns a minimal, self-consistent PE32+ x86-64
// executable image: DOS header/stub, COFF header, OptionalHeader64 with 16
// data directories, and two sections (.text, .data) with real, non-empty
// raw data. It is deliberately the smallest binary that debug/pe.Open,
// ValidatePEAMD64, and EmbedIcon's own section/slack arithmetic all accept,
// so every field below is load-bearing — this is not filler.
func buildSyntheticPE(options syntheticPEOptions) []byte {
	const peHeaderOffset = 0x80 // arbitrary, past a minimal 64-byte DOS header.

	sizeOfHeaders := options.sizeOfHeaders
	textRVA := uint32(sectionAlignment)
	textRaw := alignUp(sizeOfHeaders, fileAlignment)
	textSize := uint32(fileAlignment)
	dataRVA := alignUp(textRVA+textSize, sectionAlignment)
	dataRaw := textRaw + textSize
	dataSize := uint32(fileAlignment)
	sizeOfImage := alignUp(dataRVA+dataSize, sectionAlignment)
	fileSize := dataRaw + dataSize

	buf := make([]byte, fileSize)

	// DOS header: "MZ" signature and e_lfanew pointing at the PE header.
	buf[0], buf[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(buf[0x3c:], peHeaderOffset)

	offset := peHeaderOffset
	copy(buf[offset:], []byte("PE\x00\x00"))
	offset += 4

	// COFF file header (20 bytes).
	coff := buf[offset : offset+20]
	binary.LittleEndian.PutUint16(coff[0:], 0x8664) // Machine: AMD64.
	binary.LittleEndian.PutUint16(coff[2:], 2)      // NumberOfSections.
	// TimeDateStamp, PointerToSymbolTable, NumberOfSymbols left zero.
	binary.LittleEndian.PutUint16(coff[16:], 240) // SizeOfOptionalHeader (PE32+, 16 data directories).
	// Characteristics: EXECUTABLE_IMAGE(0x0002) | LARGE_ADDRESS_AWARE(0x0020).
	binary.LittleEndian.PutUint16(coff[18:], 0x0002|0x0020)
	offset += 20

	// Optional header (PE32+, 240 bytes: 24 standard + 88 Windows-specific + 128 data directories).
	opt := buf[offset : offset+240]
	binary.LittleEndian.PutUint16(opt[0:], 0x20b)        // Magic: PE32+.
	opt[2] = 14                                          // MajorLinkerVersion.
	opt[3] = 0                                           // MinorLinkerVersion.
	binary.LittleEndian.PutUint32(opt[4:], textSize)     // SizeOfCode.
	binary.LittleEndian.PutUint32(opt[8:], dataSize)     // SizeOfInitializedData.
	binary.LittleEndian.PutUint32(opt[16:], textRVA)     // AddressOfEntryPoint.
	binary.LittleEndian.PutUint32(opt[20:], textRVA)     // BaseOfCode.
	binary.LittleEndian.PutUint64(opt[24:], 0x140000000) // ImageBase.
	binary.LittleEndian.PutUint32(opt[32:], sectionAlignment)
	binary.LittleEndian.PutUint32(opt[36:], fileAlignment)
	binary.LittleEndian.PutUint16(opt[40:], 6) // MajorOperatingSystemVersion.
	binary.LittleEndian.PutUint16(opt[48:], 6) // MajorSubsystemVersion.
	binary.LittleEndian.PutUint32(opt[56:], sizeOfImage)
	binary.LittleEndian.PutUint32(opt[60:], sizeOfHeaders)
	binary.LittleEndian.PutUint16(opt[68:], 2)        // Subsystem: WINDOWS_GUI.
	binary.LittleEndian.PutUint64(opt[72:], 0x100000) // SizeOfStackReserve.
	binary.LittleEndian.PutUint64(opt[80:], 0x1000)   // SizeOfStackCommit.
	binary.LittleEndian.PutUint64(opt[88:], 0x100000) // SizeOfHeapReserve.
	binary.LittleEndian.PutUint64(opt[96:], 0x1000)   // SizeOfHeapCommit.
	binary.LittleEndian.PutUint32(opt[108:], 16)      // NumberOfRvaAndSizes.
	if options.resourceDataDirectory {
		// Data directory index 2 (Resource Table) starts at byte 112 +
		// 2*8 = 128 within the optional header.
		binary.LittleEndian.PutUint32(opt[128:], 0x9000)
		binary.LittleEndian.PutUint32(opt[132:], 0x100)
	}
	offset += 240

	// Section headers (40 bytes each).
	writeSection := func(at int, name string, rva, size, raw, rawSize, characteristics uint32) {
		section := buf[at : at+40]
		copy(section[0:8], name)
		binary.LittleEndian.PutUint32(section[8:], size)
		binary.LittleEndian.PutUint32(section[12:], rva)
		binary.LittleEndian.PutUint32(section[16:], rawSize)
		binary.LittleEndian.PutUint32(section[20:], raw)
		binary.LittleEndian.PutUint32(section[36:], characteristics)
	}
	writeSection(offset, ".text", textRVA, textSize, textRaw, textSize, 0x60000020) // CODE|EXECUTE|READ.
	offset += 40
	writeSection(offset, ".data", dataRVA, dataSize, dataRaw, dataSize, 0xc0000040) // INITIALIZED_DATA|READ|WRITE.
	offset += 40

	// offset must not exceed sizeOfHeaders (the file-aligned header size);
	// the gap up to sizeOfHeaders, and up to textRaw, is the zero-filled
	// slack EmbedIcon looks for.
	if uint32(offset) > sizeOfHeaders { //nolint:gosec // offset is a small, code-controlled fixture size.
		panic("buildSyntheticPE: section table overruns sizeOfHeaders")
	}

	// Fill .text/.data raw data with recognizable non-zero bytes so a
	// byte-for-byte "existing sections are untouched" comparison in tests
	// is meaningful rather than comparing zeros to zeros.
	for i := textRaw; i < textRaw+textSize; i++ {
		buf[i] = 0xCC // INT3 filler, plausible executable padding.
	}
	for i := dataRaw; i < dataRaw+dataSize; i++ {
		buf[i] = 0xAB
	}

	return buf
}

func alignUp(value, alignment uint32) uint32 {
	remainder := value % alignment
	if remainder == 0 {
		return value
	}
	return value + (alignment - remainder)
}
