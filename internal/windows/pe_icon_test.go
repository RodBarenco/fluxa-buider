package windows_test

import (
	"bytes"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	windowspkg "github.com/RodBarenco/fluxa-builder/internal/windows"
)

// expectedIconImage is the per-image metadata and bytes this test file
// deliberately puts into a synthetic multi-image ICO, so the round-trip
// assertions below compare EmbedIcon's output against known-correct
// expectations instead of re-deriving them (parseICO is unexported and
// this is an external test package by design, matching every other test
// in this file).
type expectedIconImage struct {
	width, height, colorCount uint8
	planes, bitCount          uint16
	data                      []byte
}

// multiImageICO builds a small, minimally-valid multi-image ICO using the
// same "bare 8-byte PNG signature as image data" trick validICO already
// uses (ValidateICO only requires size >= 8 and a PNG/DIB-shaped prefix,
// not a real decodable image) — real icons ship several sizes, so this
// exercises EmbedIcon's per-image loop instead of just the single-image
// case validICO covers.
func multiImageICO() ([]byte, []expectedIconImage) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	images := []expectedIconImage{
		{width: 16, height: 16, colorCount: 0, planes: 1, bitCount: 32, data: png},
		{width: 32, height: 32, colorCount: 0, planes: 1, bitCount: 32, data: append(append([]byte{}, png...), 0xAA, 0xBB)},
	}
	count := len(images)
	data := make([]byte, 6+16*count)
	binary.LittleEndian.PutUint16(data[2:4], 1)             // Type: icon.
	binary.LittleEndian.PutUint16(data[4:6], uint16(count)) //nolint:gosec // count is len(images), a fixed small literal above.
	offset := uint32(6 + 16*count)                          //nolint:gosec // same bound as above.
	for i, image := range images {
		entry := data[6+i*16 : 6+(i+1)*16]
		entry[0] = image.width
		entry[1] = image.height
		entry[2] = image.colorCount
		binary.LittleEndian.PutUint16(entry[4:6], image.planes)
		binary.LittleEndian.PutUint16(entry[6:8], image.bitCount)
		binary.LittleEndian.PutUint32(entry[8:12], uint32(len(image.data))) //nolint:gosec // fixed small test fixture bytes.
		binary.LittleEndian.PutUint32(entry[12:16], offset)
		data = append(data, image.data...)
		offset += uint32(len(image.data)) //nolint:gosec // same bound as above.
	}
	return data, images
}

func writeSyntheticPE(t *testing.T, options syntheticPEOptions) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic.exe")
	if err := os.WriteFile(path, buildSyntheticPE(options), 0o755); err != nil { // #nosec G306 -- test fixture, not sensitive.
		t.Fatal(err)
	}
	return path
}

func writeICO(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.ico")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEmbedIconRoundTrip(t *testing.T) {
	exePath := writeSyntheticPE(t, syntheticPEOptions{sizeOfHeaders: 512})
	icoData, images := multiImageICO()
	icoPath := writeICO(t, icoData)

	before, err := os.ReadFile(exePath) // #nosec G304 -- test-owned fixture path.
	if err != nil {
		t.Fatal(err)
	}
	textStart, textEnd := sectionRaw(before, 0)
	dataStart, dataEnd := sectionRaw(before, 1)
	textBefore := hashRange(before, textStart, textEnd)
	dataBefore := hashRange(before, dataStart, dataEnd)

	if err := windowspkg.EmbedIcon(exePath, icoPath); err != nil {
		t.Fatalf("EmbedIcon() error = %v", err)
	}

	after, err := os.ReadFile(exePath) // #nosec G304 -- test-owned fixture path.
	if err != nil {
		t.Fatal(err)
	}

	// debug/pe must still parse the result, with one more section.
	file, err := pe.Open(exePath)
	if err != nil {
		t.Fatalf("debug/pe.Open() after EmbedIcon: %v", err)
	}
	if len(file.Sections) != 3 {
		t.Fatalf("len(Sections) = %d, want 3", len(file.Sections))
	}
	var rsrc *pe.Section
	for _, section := range file.Sections {
		if section.Name == ".rsrc" {
			rsrc = section
		}
	}
	if rsrc == nil {
		t.Fatal(".rsrc section missing")
	}
	header := file.OptionalHeader.(*pe.OptionalHeader64) //nolint:errcheck // asserted true by construction; a failure here is a fatal test bug, not a runtime path.
	if header.DataDirectory[2].VirtualAddress != rsrc.VirtualAddress || header.DataDirectory[2].Size == 0 {
		t.Fatalf("resource data directory = %+v, want it to reference %q", header.DataDirectory[2], rsrc.Name)
	}
	_ = file.Close()

	// Every existing byte of the pre-existing sections must be untouched.
	textStartAfter, textEndAfter := sectionRaw(after, 0)
	dataStartAfter, dataEndAfter := sectionRaw(after, 1)
	if got := hashRange(after, textStartAfter, textEndAfter); got != textBefore {
		t.Error(".text section bytes changed")
	}
	if got := hashRange(after, dataStartAfter, dataEndAfter); got != dataBefore {
		t.Error(".data section bytes changed")
	}

	// Round-trip: walk the resource directory tree back out and confirm it
	// decodes to exactly the images that went in.
	got := readEmbeddedIcons(t, after, rsrc.VirtualAddress, rsrc.Offset)
	if len(got) != len(images) {
		t.Fatalf("read back %d icon images, want %d", len(got), len(images))
	}
	for i, want := range images {
		if got[i].width != want.width || got[i].height != want.height ||
			got[i].bitCount != want.bitCount || !bytes.Equal(got[i].data, want.data) {
			t.Errorf("icon image %d = %+v, want %+v (data %x vs %x)", i, got[i], want, got[i].data, want.data)
		}
	}
}

func TestEmbedIconFailsClosedWhenResourcesAlreadyExist(t *testing.T) {
	exePath := writeSyntheticPE(t, syntheticPEOptions{sizeOfHeaders: 512, resourceDataDirectory: true})
	icoData, _ := multiImageICO()
	icoPath := writeICO(t, icoData)

	err := windowspkg.EmbedIcon(exePath, icoPath)
	assertUnsupported(t, err)
}

func TestEmbedIconFailsClosedWhenNoHeaderSlack(t *testing.T) {
	exePath := writeSyntheticPE(t, syntheticPEOptions{sizeOfHeaders: sectionTableEnd})
	icoData, _ := multiImageICO()
	icoPath := writeICO(t, icoData)

	err := windowspkg.EmbedIcon(exePath, icoPath)
	assertUnsupported(t, err)
}

func assertUnsupported(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("EmbedIcon() error = nil, want ErrorUnsupported")
	}
	var windowsErr *windowspkg.Error
	if !errors.As(err, &windowsErr) || windowsErr.Kind != windowspkg.ErrorUnsupported {
		t.Fatalf("EmbedIcon() error = %v, want kind %q", err, windowspkg.ErrorUnsupported)
	}
}

// sectionRaw returns the [start, end) file byte range of the index-th
// section header found in a buildSyntheticPE image (0 = .text, 1 = .data),
// read directly since these tests intentionally avoid depending on
// debug/pe for the "before" snapshot of a file EmbedIcon has not touched
// yet.
func sectionRaw(data []byte, index int) (int, int) {
	peOffset := int(binary.LittleEndian.Uint32(data[0x3c:0x40]))
	sectionTableOffset := peOffset + 4 + 20 + 240
	entry := data[sectionTableOffset+index*40 : sectionTableOffset+(index+1)*40]
	rawSize := int(binary.LittleEndian.Uint32(entry[16:20]))
	pointerToRawData := int(binary.LittleEndian.Uint32(entry[20:24]))
	return pointerToRawData, pointerToRawData + rawSize
}

func hashRange(data []byte, start, end int) string {
	sum := sha256.Sum256(data[start:end])
	return string(sum[:])
}

// readEmbeddedIcons walks the resource directory tree EmbedIcon wrote
// (type -> RT_ICON ID -> language -> data entry) and returns each RT_ICON
// image in ascending ID order. It is deliberately independent of
// pe_icon.go's own writer logic — a from-scratch reader implementing the
// real PE resource directory format — so a bug shared between writer and
// reader cannot hide behind a false-positive round trip.
func readEmbeddedIcons(t *testing.T, data []byte, sectionRVA, sectionFileOffset uint32) []expectedIconImage {
	t.Helper()
	rsrc := data[sectionFileOffset:]

	readDir := func(offset uint32) (idCount uint16, entriesOffset uint32) {
		idCount = binary.LittleEndian.Uint16(rsrc[offset+14 : offset+16])
		return idCount, offset + 16
	}
	readEntry := func(offset uint32) (id uint32, value uint32, isSubdir bool) {
		id = binary.LittleEndian.Uint32(rsrc[offset : offset+4])
		raw := binary.LittleEndian.Uint32(rsrc[offset+4 : offset+8])
		return id, raw &^ (1 << 31), raw&(1<<31) != 0
	}

	typeCount, typeEntries := readDir(0)
	var iconTypeOffset uint32
	found := false
	for i := uint16(0); i < typeCount; i++ {
		id, value, isSubdir := readEntry(typeEntries + uint32(i)*8)
		if id == 3 && isSubdir { // RT_ICON.
			iconTypeOffset = value
			found = true
		}
	}
	if !found {
		t.Fatal("RT_ICON type directory not found")
	}

	iconCount, iconEntries := readDir(iconTypeOffset)
	type indexed struct {
		id    uint32
		image expectedIconImage
	}
	results := make([]indexed, 0, iconCount)
	for i := uint16(0); i < iconCount; i++ {
		id, langDirOffset, isSubdir := readEntry(iconEntries + uint32(i)*8)
		if !isSubdir {
			t.Fatalf("RT_ICON entry %d is not a subdirectory", id)
		}
		_, langEntries := readDir(langDirOffset)
		_, dataEntryOffset, leafIsSubdir := readEntry(langEntries)
		if leafIsSubdir {
			t.Fatalf("RT_ICON %d language entry unexpectedly points to a subdirectory", id)
		}
		entryRVA := binary.LittleEndian.Uint32(rsrc[dataEntryOffset : dataEntryOffset+4])
		size := binary.LittleEndian.Uint32(rsrc[dataEntryOffset+4 : dataEntryOffset+8])
		sectionRelative := entryRVA - sectionRVA
		imageData := append([]byte{}, rsrc[sectionRelative:sectionRelative+size]...)
		results = append(results, indexed{id: id, image: expectedIconImage{data: imageData}})
	}

	// Fill width/height/bitCount from the RT_GROUP_ICON's GRPICONDIR,
	// which is the authoritative source for that metadata (RT_ICON's own
	// resource data is only the raw image bytes).
	_, groupTypeEntries := readDir(0)
	var groupTypeOffset uint32
	found = false
	for i := uint16(0); i < typeCount; i++ {
		id, value, isSubdir := readEntry(groupTypeEntries + uint32(i)*8)
		if id == 14 && isSubdir { // RT_GROUP_ICON.
			groupTypeOffset = value
			found = true
		}
	}
	if !found {
		t.Fatal("RT_GROUP_ICON type directory not found")
	}
	_, groupIDEntries := readDir(groupTypeOffset)
	_, groupLangDirOffset, _ := readEntry(groupIDEntries)
	_, groupLangEntries := readDir(groupLangDirOffset)
	_, groupDataEntryOffset, _ := readEntry(groupLangEntries)
	groupRVA := binary.LittleEndian.Uint32(rsrc[groupDataEntryOffset : groupDataEntryOffset+4])
	groupSectionRelative := groupRVA - sectionRVA
	group := rsrc[groupSectionRelative:]
	groupCount := binary.LittleEndian.Uint16(group[4:6])
	if int(groupCount) != len(results) {
		t.Fatalf("GRPICONDIR count = %d, want %d", groupCount, len(results))
	}
	byID := make(map[uint32]int, len(results))
	for i, r := range results {
		byID[r.id] = i
	}
	for i := 0; i < int(groupCount); i++ {
		entry := group[6+i*14 : 6+(i+1)*14]
		id := uint32(binary.LittleEndian.Uint16(entry[12:14]))
		index, ok := byID[id]
		if !ok {
			t.Fatalf("GRPICONDIRENTRY references unknown RT_ICON id %d", id)
		}
		results[index].image.width = entry[0]
		results[index].image.height = entry[1]
		results[index].image.colorCount = entry[2]
		results[index].image.planes = binary.LittleEndian.Uint16(entry[4:6])
		results[index].image.bitCount = binary.LittleEndian.Uint16(entry[6:8])
	}

	ordered := make([]expectedIconImage, len(results))
	for _, r := range results {
		if r.id == 0 || int(r.id) > len(results) {
			t.Fatalf("unexpected RT_ICON id %d", r.id)
		}
		ordered[r.id-1] = r.image
	}
	return ordered
}
