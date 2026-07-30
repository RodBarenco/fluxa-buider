package windows_test

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	windowspkg "github.com/RodBarenco/fluxa-builder/internal/windows"
)

func TestValidateICOAcceptsBoundedPNGIcon(t *testing.T) {
	path := filepath.Join(t.TempDir(), "application.ico")
	if err := os.WriteFile(path, validICO(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := windowspkg.ValidateICO(path); err != nil {
		t.Fatalf("ValidateICO() error = %v", err)
	}
}

func TestValidateICORejectsMalformedTruncatedAndSymlink(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string][]byte{
		"truncated.ico": {0, 0, 1, 0, 1, 0},
		"wrong-type.ico": func() []byte {
			data := validICO()
			data[2] = 2
			return data
		}(),
		"bad-offset.ico": func() []byte {
			data := validICO()
			binary.LittleEndian.PutUint32(data[18:22], 0xffffffff)
			return data
		}(),
	} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := windowspkg.ValidateICO(path); err == nil {
			t.Fatalf("ValidateICO(%s) accepted invalid file", name)
		}
	}
	target := filepath.Join(root, "valid.ico")
	if err := os.WriteFile(target, validICO(), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.ico")
	if err := os.Symlink(target, link); err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skip("symlinks unavailable")
		}
		t.Fatal(err)
	}
	if err := windowspkg.ValidateICO(link); err == nil {
		t.Fatal("ValidateICO() accepted symlink")
	}
}

func validICO() []byte {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	data := make([]byte, 6+16+len(png))
	binary.LittleEndian.PutUint16(data[2:4], 1)
	binary.LittleEndian.PutUint16(data[4:6], 1)
	data[6] = 16
	data[7] = 16
	binary.LittleEndian.PutUint16(data[10:12], 1)
	binary.LittleEndian.PutUint16(data[12:14], 32)
	binary.LittleEndian.PutUint32(data[14:18], 8)
	binary.LittleEndian.PutUint32(data[18:22], 22)
	copy(data[22:], png)
	return data
}
