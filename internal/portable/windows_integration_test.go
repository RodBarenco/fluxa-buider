//go:build windows

package portable_test

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
	"github.com/RodBarenco/fluxa-builder/internal/portable"
	windowspkg "github.com/RodBarenco/fluxa-builder/internal/windows"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--fluxa-package-self-test" {
		executable, err := os.Executable()
		if err != nil {
			os.Exit(90)
		}
		packagePath := strings.TrimSuffix(executable, filepath.Ext(executable)) + ".flxpkg"
		info, err := flxpkg.Verify(packagePath)
		if err != nil {
			os.Exit(91)
		}
		response := map[string]any{
			"protocol": "fluxa-package-self-test-v1", "package_sha256": info.SHA256,
			"package_opened": true, "vm_compatible": true, "ui_opened": false,
		}
		if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
			os.Exit(92)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestWindowsX64OfficialPortablePipeline(t *testing.T) {
	fixture := newFixture(t, "Ação Espacial Windows", true)
	runtimePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runtimeData, err := os.ReadFile(runtimePath) // #nosec G304 -- current test executable is the PE fixture.
	if err != nil {
		t.Fatal(err)
	}
	runtimeHash := sha256.Sum256(runtimeData)
	fixture.request.TargetOS = "windows"
	fixture.request.TargetArch = "amd64"
	fixture.request.Runtime.BinaryPath = runtimePath
	fixture.request.Runtime.Metadata.OS = "windows"
	fixture.request.Runtime.Metadata.Arch = "amd64"
	fixture.request.Runtime.Metadata.BinarySHA256 = hex.EncodeToString(runtimeHash[:])

	longUnicodeRoot := filepath.Join(
		t.TempDir(),
		"Usuário Fluxa com espaços",
		strings.Repeat("diretório-longo-", 12),
	)
	if err := os.MkdirAll(longUnicodeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture.request.OutputRoot = longUnicodeRoot
	iconPath := filepath.Join(t.TempDir(), "ícone oficial.ico")
	if err := os.WriteFile(iconPath, windowsIntegrationICO(), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.request.WindowsIcon = iconPath

	result, err := portable.Build(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(result.Executable) != ".exe" {
		t.Fatalf("executable = %q", result.Executable)
	}
	if err := windowspkg.ValidatePEAMD64(result.Executable); err != nil {
		t.Fatalf("copied runtime is not a valid Windows x64 PE: %v", err)
	}
	if _, err := flxpkg.Verify(result.Package); err != nil {
		t.Fatalf("copied package verification failed: %v", err)
	}
	if _, err := portable.SmokeDetailed(context.Background(), result, 10*time.Second); err != nil {
		t.Fatalf("Windows self-test failed: %v", err)
	}

	archive, err := portable.Archive(context.Background(), result, "windows")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(archive.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	if len(reader.File) != 6 {
		t.Fatalf("ZIP entries = %d, want root plus five regular files", len(reader.File))
	}
	for _, entry := range reader.File {
		if strings.Contains(entry.Name, `\`) || strings.Contains(entry.Name, "..") {
			t.Fatalf("antivirus-friendly ZIP contains unsafe name %q", entry.Name)
		}
	}

	if err := os.WriteFile(result.Package, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := portable.Smoke(context.Background(), result, time.Second); err == nil {
		t.Fatal("Smoke() accepted tampered Windows package")
	}
}

func windowsIntegrationICO() []byte {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	data := make([]byte, 22+len(png))
	binary.LittleEndian.PutUint16(data[2:4], 1)
	binary.LittleEndian.PutUint16(data[4:6], 1)
	data[6], data[7] = 32, 32
	binary.LittleEndian.PutUint16(data[10:12], 1)
	binary.LittleEndian.PutUint16(data[12:14], 32)
	binary.LittleEndian.PutUint32(data[14:18], 8)
	binary.LittleEndian.PutUint32(data[18:22], 22)
	copy(data[22:], png)
	return data
}
