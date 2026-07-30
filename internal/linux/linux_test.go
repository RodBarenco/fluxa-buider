package linux_test

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	linuxpkg "github.com/RodBarenco/fluxa-builder/internal/linux"
)

func TestValidateELFAMD64(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("requires a native Linux x64 test executable")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := linuxpkg.ValidateELFAMD64(executable); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(t.TempDir(), "invalid")
	if err := os.WriteFile(invalid, []byte("not ELF"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := linuxpkg.ValidateELFAMD64(invalid); err == nil {
		t.Fatal("ValidateELFAMD64 accepted invalid data")
	}
}

func TestValidatePNG(t *testing.T) {
	path := filepath.Join(t.TempDir(), "icon.png")
	file, err := os.Create(path) // #nosec G304 -- test-owned path.
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 32, 32))); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := linuxpkg.ValidatePNG(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not PNG"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := linuxpkg.ValidatePNG(path); err == nil {
		t.Fatal("ValidatePNG accepted invalid data")
	}
}
