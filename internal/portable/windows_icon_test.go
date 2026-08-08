package portable_test

import (
	"context"
	"debug/pe"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RodBarenco/fluxa-builder/internal/portable"
)

// TestWindowsIconEmbedding proves portable.Build's integration of
// windowspkg.EmbedIcon end to end, using a synthetic PE64 launcher fixture
// (this host has no real MinGW/MSVC-linked .exe or Windows host available;
// internal/windows's own tests already prove EmbedIcon's byte-level
// correctness against the same kind of fixture with a round-trip resource
// parser — this test only proves portable.Build wires it up correctly: the
// happy path leaves no warning and grows the section table, and the
// graceful-degrade path leaves the build successful with a warning and the
// loose .ico still shipped, per docs/adr/0026-file-manager-icon-association.md).
func TestWindowsIconEmbedding(t *testing.T) {
	tests := []struct {
		name          string
		sizeOfHeaders uint32
		wantSections  int
		wantWarning   bool
	}{
		{name: "adequate header slack embeds icon", sizeOfHeaders: 512, wantSections: 3, wantWarning: false},
		{name: "no header slack degrades gracefully", sizeOfHeaders: 472, wantSections: 2, wantWarning: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t, "Windows Icon Game", true)
			fixture.request.TargetOS = "windows"
			fixture.request.Runtime.Metadata.OS = "windows"

			launcherPath := filepath.Join(t.TempDir(), "launcher.exe")
			if err := os.WriteFile(launcherPath, buildMinimalPE(test.sizeOfHeaders), 0o700); err != nil { // #nosec G306 -- test fixture launcher needs the execute bit; not external input.
				t.Fatal(err)
			}
			fixture.request.LauncherPath = launcherPath

			iconPath := filepath.Join(t.TempDir(), "aplicação.ico")
			if err := os.WriteFile(iconPath, archiveTestICO(), 0o600); err != nil {
				t.Fatal(err)
			}
			fixture.request.WindowsIcon = iconPath

			result, err := portable.Build(context.Background(), fixture.request)
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}

			if test.wantWarning {
				if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "icon") {
					t.Fatalf("Warnings = %#v, want exactly one icon warning", result.Warnings)
				}
			} else if len(result.Warnings) != 0 {
				t.Fatalf("Warnings = %#v, want none", result.Warnings)
			}

			foundICO := false
			for _, extra := range result.ExtraFiles {
				if filepath.Ext(extra) == ".ico" {
					foundICO = true
				}
			}
			if !foundICO {
				t.Fatalf("ExtraFiles = %#v, want loose .ico still shipped", result.ExtraFiles)
			}

			file, err := pe.Open(result.Executable)
			if err != nil {
				t.Fatalf("reopen result executable as PE: %v", err)
			}
			defer func() { _ = file.Close() }()
			if len(file.Sections) != test.wantSections {
				t.Fatalf("section count = %d, want %d", len(file.Sections), test.wantSections)
			}
			optional, ok := file.OptionalHeader.(*pe.OptionalHeader64)
			if !ok {
				t.Fatalf("OptionalHeader is %T, want *pe.OptionalHeader64", file.OptionalHeader)
			}
			resourceDirectory := optional.DataDirectory[2]
			hasResources := resourceDirectory.VirtualAddress != 0 || resourceDirectory.Size != 0
			if hasResources == test.wantWarning {
				t.Fatalf("resource data directory = %#v, embedded mismatch (wantWarning=%v)", resourceDirectory, test.wantWarning)
			}
		})
	}
}

// buildMinimalPE returns a minimal, self-consistent PE32+ x86-64 executable
// image with two sections (.text, .data) and sizeOfHeaders bytes of
// file-aligned header room — enough, at 512, for EmbedIcon to insert one
// more IMAGE_SECTION_HEADER (40 bytes) past the 472-byte section table this
// layout always produces; at exactly 472, none. This mirrors
// internal/windows/pe_fixture_test.go's buildSyntheticPE, duplicated here
// (an unexported test helper in another package's _test.go isn't
// importable) rather than exported from internal/windows for testing alone.
func buildMinimalPE(sizeOfHeaders uint32) []byte {
	const (
		peHeaderOffset   = 0x80
		fileAlignment    = 0x200
		sectionAlignment = 0x1000
	)

	textRVA := uint32(sectionAlignment)
	textRaw := alignUpPE(sizeOfHeaders, fileAlignment)
	textSize := uint32(fileAlignment)
	dataRVA := alignUpPE(textRVA+textSize, sectionAlignment)
	dataRaw := textRaw + textSize
	dataSize := uint32(fileAlignment)
	sizeOfImage := alignUpPE(dataRVA+dataSize, sectionAlignment)
	fileSize := dataRaw + dataSize

	buf := make([]byte, fileSize)

	buf[0], buf[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(buf[0x3c:], peHeaderOffset)

	offset := peHeaderOffset
	copy(buf[offset:], []byte("PE\x00\x00"))
	offset += 4

	coff := buf[offset : offset+20]
	binary.LittleEndian.PutUint16(coff[0:], 0x8664) // Machine: AMD64.
	binary.LittleEndian.PutUint16(coff[2:], 2)      // NumberOfSections.
	binary.LittleEndian.PutUint16(coff[16:], 240)   // SizeOfOptionalHeader (PE32+, 16 data directories).
	binary.LittleEndian.PutUint16(coff[18:], 0x0002|0x0020)
	offset += 20

	opt := buf[offset : offset+240]
	binary.LittleEndian.PutUint16(opt[0:], 0x20b) // Magic: PE32+.
	opt[2] = 14                                   // MajorLinkerVersion.
	binary.LittleEndian.PutUint32(opt[4:], textSize)
	binary.LittleEndian.PutUint32(opt[8:], dataSize)
	binary.LittleEndian.PutUint32(opt[16:], textRVA)
	binary.LittleEndian.PutUint32(opt[20:], textRVA)
	binary.LittleEndian.PutUint64(opt[24:], 0x140000000)
	binary.LittleEndian.PutUint32(opt[32:], sectionAlignment)
	binary.LittleEndian.PutUint32(opt[36:], fileAlignment)
	binary.LittleEndian.PutUint16(opt[40:], 6)
	binary.LittleEndian.PutUint16(opt[48:], 6)
	binary.LittleEndian.PutUint32(opt[56:], sizeOfImage)
	binary.LittleEndian.PutUint32(opt[60:], sizeOfHeaders)
	binary.LittleEndian.PutUint16(opt[68:], 2) // Subsystem: WINDOWS_GUI.
	binary.LittleEndian.PutUint64(opt[72:], 0x100000)
	binary.LittleEndian.PutUint64(opt[80:], 0x1000)
	binary.LittleEndian.PutUint64(opt[88:], 0x100000)
	binary.LittleEndian.PutUint64(opt[96:], 0x1000)
	binary.LittleEndian.PutUint32(opt[108:], 16) // NumberOfRvaAndSizes.
	offset += 240

	writeSection := func(at int, name string, rva, size, raw, rawSize, characteristics uint32) {
		section := buf[at : at+40]
		copy(section[0:8], name)
		binary.LittleEndian.PutUint32(section[8:], size)
		binary.LittleEndian.PutUint32(section[12:], rva)
		binary.LittleEndian.PutUint32(section[16:], rawSize)
		binary.LittleEndian.PutUint32(section[20:], raw)
		binary.LittleEndian.PutUint32(section[36:], characteristics)
	}
	writeSection(offset, ".text", textRVA, textSize, textRaw, textSize, 0x60000020)
	offset += 40
	writeSection(offset, ".data", dataRVA, dataSize, dataRaw, dataSize, 0xc0000040)
	offset += 40

	if uint32(offset) > sizeOfHeaders { //nolint:gosec // offset is a small, code-controlled fixture size.
		panic("buildMinimalPE: section table overruns sizeOfHeaders")
	}

	for i := textRaw; i < textRaw+textSize; i++ {
		buf[i] = 0xCC
	}
	for i := dataRaw; i < dataRaw+dataSize; i++ {
		buf[i] = 0xAB
	}

	return buf
}

func alignUpPE(value, alignment uint32) uint32 {
	remainder := value % alignment
	if remainder == 0 {
		return value
	}
	return value + (alignment - remainder)
}
