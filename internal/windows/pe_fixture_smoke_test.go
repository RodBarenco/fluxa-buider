package windows_test

import (
	"debug/pe"
	"os"
	"path/filepath"
	"testing"

	windowspkg "github.com/RodBarenco/fluxa-builder/internal/windows"
)

// TestSyntheticPEIsSelfConsistent proves buildSyntheticPE's output is a
// genuinely valid PE32+ x86-64 image — parseable by the standard library's
// own debug/pe reader and accepted by this package's own ValidatePEAMD64 —
// before any EmbedIcon test relies on it as a realistic fixture.
func TestSyntheticPEIsSelfConsistent(t *testing.T) {
	for _, sizeOfHeaders := range []uint32{512, sectionTableEnd} {
		data := buildSyntheticPE(syntheticPEOptions{sizeOfHeaders: sizeOfHeaders})
		path := filepath.Join(t.TempDir(), "synthetic.exe")
		if err := os.WriteFile(path, data, 0o755); err != nil { // #nosec G306 -- test fixture, needs no real secrecy.
			t.Fatal(err)
		}

		file, err := pe.Open(path)
		if err != nil {
			t.Fatalf("debug/pe.Open() error = %v (sizeOfHeaders=%d)", err, sizeOfHeaders)
		}
		if file.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
			t.Errorf("Machine = %#x, want AMD64", file.Machine)
		}
		if len(file.Sections) != 2 {
			t.Errorf("len(Sections) = %d, want 2", len(file.Sections))
		}
		_ = file.Close()

		if err := windowspkg.ValidatePEAMD64(path); err != nil {
			t.Fatalf("ValidatePEAMD64() error = %v (sizeOfHeaders=%d)", err, sizeOfHeaders)
		}
	}
}
