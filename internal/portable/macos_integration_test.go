//go:build darwin

package portable_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
	"github.com/RodBarenco/fluxa-builder/internal/portable"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--fluxa-package-self-test" {
		runMacOSSelfTest()
	}
	os.Exit(m.Run())
}

func TestMacOSOfficialAppBundlePipeline(t *testing.T) {
	fixture := newFixture(t, "Minha Aplicação Espacial", true)
	runtimePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runtimeData, err := os.ReadFile(runtimePath) // #nosec G304 -- current test executable is the Mach-O fixture.
	if err != nil {
		t.Fatal(err)
	}
	runtimeHash := sha256.Sum256(runtimeData)
	fixture.request.TargetOS = "macos"
	fixture.request.TargetArch = runtime.GOARCH
	fixture.request.Runtime.BinaryPath = runtimePath
	fixture.request.Runtime.Metadata.FormatVersion = 1
	fixture.request.Runtime.Metadata.OS = "macos"
	fixture.request.Runtime.Metadata.Arch = runtime.GOARCH
	fixture.request.Runtime.Metadata.BinarySHA256 = hex.EncodeToString(runtimeHash[:])
	fixture.request.LauncherPath = runtimePath
	fixture.request.OutputRoot = filepath.Join(t.TempDir(), "Usuário macOS com espaços")
	if err := os.MkdirAll(fixture.request.OutputRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture.request.BundleID = "com.example.minha-aplicacao"
	iconPath := filepath.Join(t.TempDir(), "Ícone Oficial.icns")
	writeTestICNS(t, iconPath)
	fixture.request.MacOSIcon = iconPath

	result, err := portable.Build(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(result.Directory) != ".app" {
		t.Fatalf("bundle = %q", result.Directory)
	}
	wantExecutable := filepath.Join(result.Directory, "Contents", "MacOS", "minha-aplicação-espacial")
	wantPackage := filepath.Join(result.Directory, "Contents", "Resources", "minha-aplicação-espacial.flxpkg")
	if result.Executable != wantExecutable || result.Package != wantPackage {
		t.Fatalf("bundle result = %#v", result)
	}
	privateRuntime := filepath.Join(result.Directory, "Contents", "MacOS", ".fluxa-runtime")
	if _, err := os.Stat(privateRuntime); err != nil {
		t.Fatalf("private runtime is missing: %v", err)
	}
	plistPath := filepath.Join(result.Directory, "Contents", "Info.plist")
	plist, err := os.ReadFile(plistPath) // #nosec G304 -- test-owned bundle.
	if err != nil {
		t.Fatal(err)
	}
	decoder := xml.NewDecoder(bytes.NewReader(plist))
	for {
		_, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Info.plist is not XML: %v", err)
		}
	}
	plistText := string(plist)
	for _, value := range []string{
		"CFBundleExecutable", "minha-aplicação-espacial",
		"CFBundleIdentifier", fixture.request.BundleID,
		"CFBundleIconFile", "AppIcon.icns",
	} {
		if !strings.Contains(plistText, value) {
			t.Fatalf("Info.plist missing %q", value)
		}
	}
	if _, err := portable.SmokeDetailed(context.Background(), result, 10*time.Second); err != nil {
		t.Fatalf("macOS bundle self-test failed: %v", err)
	}

	archive, err := portable.Archive(context.Background(), result, "macos")
	if err != nil {
		t.Fatal(err)
	}
	assertMacOSArchive(t, archive.Path, result.Name)
}

func runMacOSSelfTest() {
	executable, err := os.Executable()
	if err != nil {
		os.Exit(90)
	}
	contents := filepath.Dir(filepath.Dir(executable))
	name := filepath.Base(executable)
	packagePath := filepath.Join(contents, "Resources", name+".flxpkg")
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

func writeTestICNS(t *testing.T, path string) {
	t.Helper()
	data := make([]byte, 24)
	copy(data, "icns")
	binary.BigEndian.PutUint32(data[4:8], uint32(len(data))) // #nosec G115 -- fixed fixture size.
	copy(data[8:12], "ic07")
	binary.BigEndian.PutUint32(data[12:16], 16)
	copy(data[16:], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertMacOSArchive(t *testing.T, archivePath, root string) {
	t.Helper()
	file, err := os.Open(archivePath) // #nosec G304 -- test-owned archive.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gzipReader.Close() }()
	reader := tar.NewReader(gzipReader)
	seenExecutables := 0
	count := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
		if !strings.HasPrefix(header.Name, root+"/") || strings.Contains(header.Name, "..") {
			t.Fatalf("unsafe tar entry %q", header.Name)
		}
		if strings.Contains(header.Name, "/Contents/MacOS/") && header.Typeflag == tar.TypeReg {
			if header.Mode != 0o700 {
				t.Fatalf("Mach-O mode = %o, want 700", header.Mode)
			}
			seenExecutables++
		}
	}
	if count != 10 || seenExecutables != 2 {
		t.Fatalf("tar entries = %d, executable Mach-O files = %d", count, seenExecutables)
	}
}
