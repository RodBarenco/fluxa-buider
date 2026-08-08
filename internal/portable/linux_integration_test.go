//go:build linux && amd64

package portable_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
	"github.com/RodBarenco/fluxa-builder/internal/installer"
	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
	"github.com/RodBarenco/fluxa-builder/internal/portable"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--fluxa-package-self-test" {
		runLinuxSelfTest()
	}
	os.Exit(m.Run())
}

func TestLinuxX64OfficialPortablePipeline(t *testing.T) {
	fixture := newFixture(t, "Ação Espacial Linux", true)
	runtimePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runtimeData, err := os.ReadFile(runtimePath) // #nosec G304 -- current test executable is the ELF fixture.
	if err != nil {
		t.Fatal(err)
	}
	runtimeHash := sha256.Sum256(runtimeData)
	fixture.request.Runtime.BinaryPath = runtimePath
	fixture.request.Runtime.Metadata.FormatVersion = 1
	fixture.request.Runtime.Metadata.BinarySHA256 = hex.EncodeToString(runtimeHash[:])

	outputRoot := filepath.Join(t.TempDir(), "Usuário Fluxa com espaços", "instalação portátil")
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture.request.OutputRoot = outputRoot
	iconPath := filepath.Join(t.TempDir(), "ícone oficial.png")
	writeLinuxPNG(t, iconPath)
	fixture.request.LinuxIcon = iconPath

	result, err := portable.Build(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(result.Executable) != "" {
		t.Fatalf("Linux executable has an extension: %q", result.Executable)
	}
	infoData, err := os.ReadFile(filepath.Join(result.Directory, "linux-runtime.json")) // #nosec G304 -- test-owned result.
	if err != nil {
		t.Fatal(err)
	}
	var info map[string]any
	if err := json.Unmarshal(infoData, &info); err != nil {
		t.Fatal(err)
	}
	if info["data_policy"] != "xdg" || info["libc_policy"] != "runtime-defined" {
		t.Fatalf("Linux metadata = %#v", info)
	}

	before := hashPortableTree(t, result.Directory)
	makeInstallationReadOnly(t, result)
	xdgData := filepath.Join(t.TempDir(), "xdg data com espaços")
	t.Setenv("XDG_DATA_HOME", xdgData)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home sem escrita"))
	if _, err := portable.SmokeDetailed(context.Background(), result, 10*time.Second); err != nil {
		t.Fatalf("Linux read-only self-test failed: %v", err)
	}
	marker := filepath.Join(xdgData, "fluxa-builder-test", "self-test.json")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("runtime did not use XDG_DATA_HOME: %v", err)
	}
	after := hashPortableTree(t, result.Directory)
	if before != after {
		t.Fatal("runtime changed the read-only installation tree")
	}

	archive, err := portable.Archive(context.Background(), result, "linux")
	if err != nil {
		t.Fatal(err)
	}
	assertLinuxArchive(t, archive.Path, result.Name)

	debianResult, err := (installer.Debian{}).Build(context.Background(), installer.Request{
		OutputDir: outputRoot, ProjectName: fixture.request.ProjectName,
		ProjectID: fixture.request.ProjectID, Version: fixture.request.Version,
		Terminal: fixture.request.Terminal, Portable: result,
	})
	if err != nil {
		t.Fatal(err)
	}
	validateDebianWithDPKG(t, debianResult.Path)
	if os.Getenv("FLUXA_DEB_INSTALL_TEST") == "1" {
		installAndRemoveDebian(t, debianResult.Path, fixture.request.ProjectID, result, marker)
	}
}

func runLinuxSelfTest() {
	executable, err := os.Executable()
	if err != nil {
		os.Exit(90)
	}
	packagePath := executable + ".flxpkg"
	info, err := flxpkg.Verify(packagePath)
	if err != nil {
		os.Exit(91)
	}
	dataRoot := os.Getenv("XDG_DATA_HOME")
	if dataRoot == "" {
		os.Exit(92)
	}
	markerDir := filepath.Join(dataRoot, "fluxa-builder-test")
	// XDG_DATA_HOME is deliberately injected by the parent test and points to
	// its private TempDir; the child process is validating the runtime contract.
	if err := os.MkdirAll(markerDir, 0o700); err != nil { // #nosec G703 -- test-owned XDG root.
		os.Exit(93)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "self-test.json"), []byte("{}\n"), 0o600); err != nil { // #nosec G703 -- path is confined to the test-owned XDG root.
		os.Exit(94)
	}
	response := map[string]any{
		"protocol": "fluxa-package-self-test-v1", "package_sha256": info.SHA256,
		"package_opened": true, "vm_compatible": true, "ui_opened": false,
	}
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		os.Exit(95)
	}
	os.Exit(0)
}

func writeLinuxPNG(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path) // #nosec G304 -- test-owned path.
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 64, 64))); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func makeInstallationReadOnly(t *testing.T, result portable.Result) {
	t.Helper()
	entries, err := os.ReadDir(result.Directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		mode := os.FileMode(0o444)
		if filepath.Join(result.Directory, entry.Name()) == result.Executable {
			mode = 0o555
		}
		if err := os.Chmod(filepath.Join(result.Directory, entry.Name()), mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(result.Directory, 0o555); err != nil { // #nosec G302 -- read-only test installation directory.
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(result.Directory, 0o755) }) // #nosec G302 -- restore traversal for TempDir cleanup.
}

func hashPortableTree(t *testing.T, root string) string {
	t.Helper()
	hash := sha256.New()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("unexpected directory %q", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name())) // #nosec G304 -- test-owned tree.
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(hash, entry.Name())
		_, _ = hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func assertLinuxArchive(t *testing.T, path, root string) {
	t.Helper()
	file, err := os.Open(path) // #nosec G304 -- test-owned archive.
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
	}
	// root + executable + package + build-info.json + linux-runtime.json +
	// icon + install-desktop-shortcut.sh.
	if count != 7 {
		t.Fatalf("tar entries = %d, want root plus six regular files", count)
	}
}

func validateDebianWithDPKG(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat("/usr/bin/dpkg-deb"); err != nil {
		t.Skip("dpkg-deb is unavailable")
	}
	execution, err := executor.Run(context.Background(), executor.Request{
		Path: "/usr/bin/dpkg-deb", Args: []string{"--info", path}, Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("dpkg-deb rejected package: %v\n%s", err, execution.Stderr)
	}
}

func installAndRemoveDebian(t *testing.T, path, packageName string, result portable.Result, marker string) {
	t.Helper()
	command := "/usr/bin/sudo"
	prefix := []string{"/usr/bin/dpkg"}
	if os.Geteuid() == 0 {
		command = "/usr/bin/dpkg"
		prefix = nil
	} else if _, err := os.Stat(command); err != nil {
		t.Skip("root or sudo is required for the isolated install test")
	}
	run := func(args ...string) {
		t.Helper()
		execution, err := executor.Run(context.Background(), executor.Request{
			Path: command, Args: append(append([]string(nil), prefix...), args...), Timeout: 30 * time.Second,
			MaxStdout: 1 << 20, MaxStderr: 1 << 20,
		})
		if err != nil {
			t.Fatalf("dpkg %v failed: %v\n%s", args, err, execution.Stderr)
		}
	}
	run("--install", path)
	installed := true
	installedExecutable := filepath.Join("/usr", "bin", filepath.Base(result.Executable))
	t.Cleanup(func() {
		if !installed {
			return
		}
		execution, err := executor.Run(context.Background(), executor.Request{
			Path: command, Args: append(append([]string(nil), prefix...), "--remove", packageName),
			Timeout: 30 * time.Second,
		})
		if err != nil {
			t.Errorf("failed to remove Debian test package: %v\n%s", err, execution.Stderr)
		}
	})
	if _, err := portable.SmokeExecutable(
		context.Background(), installedExecutable, filepath.Dir(installedExecutable),
		result.PackageHash, 10*time.Second,
	); err != nil {
		t.Fatalf("installed Debian launcher failed: %v", err)
	}
	run("--remove", packageName)
	installed = false
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("Debian removal deleted XDG user data: %v", err)
	}
}
