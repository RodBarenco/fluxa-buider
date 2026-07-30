package macos_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	macospkg "github.com/RodBarenco/fluxa-builder/internal/macos"
)

func TestValidateICNS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AppIcon.icns")
	data := make([]byte, 24)
	copy(data, "icns")
	binary.BigEndian.PutUint32(data[4:8], uint32(len(data))) // #nosec G115 -- fixed fixture size.
	copy(data[8:12], "ic07")
	binary.BigEndian.PutUint32(data[12:16], 16)
	copy(data[16:], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := macospkg.ValidateICNS(path); err != nil {
		t.Fatal(err)
	}
	data[7]++
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := macospkg.ValidateICNS(path); err == nil {
		t.Fatal("ValidateICNS accepted an inconsistent size")
	}
}

func TestValidateMachOHostExecutable(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires a native macOS test executable")
	}
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := macospkg.ValidateMachO(path, runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
}
