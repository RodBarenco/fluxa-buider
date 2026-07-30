package installer_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
	"github.com/RodBarenco/fluxa-builder/internal/installer"
	"github.com/RodBarenco/fluxa-builder/internal/portable"
)

func TestDebianBuildIsDeterministicAndComplete(t *testing.T) {
	portableResult := debianFixture(t)
	first := buildDebian(t, t.TempDir(), portableResult)
	second := buildDebian(t, t.TempDir(), portableResult)
	if first.SHA256 != second.SHA256 || first.Size != second.Size {
		t.Fatalf("Debian packages differ: %#v %#v", first, second)
	}
	members := readAR(t, first.Path)
	if string(members["debian-binary"]) != "2.0\n" {
		t.Fatalf("debian-binary = %q", members["debian-binary"])
	}
	control := readTarGZ(t, members["control.tar.gz"])
	controlText := string(control["control"])
	for _, wanted := range []string{
		"Package: com.example.game\n", "Version: 1.2.3\n",
		"Architecture: amd64\n", "Installed-Size:",
	} {
		if !strings.Contains(controlText, wanted) {
			t.Fatalf("control missing %q:\n%s", wanted, controlText)
		}
	}
	data := readTarGZ(t, members["data.tar.gz"])
	for _, wanted := range []string{
		"opt/com.example.game/game",
		"opt/com.example.game/game.flxpkg",
		"usr/bin/game",
		"usr/share/applications/com.example.game.desktop",
		"usr/share/pixmaps/com.example.game.png",
	} {
		if _, ok := data[wanted]; !ok {
			t.Fatalf("data archive missing %q; entries=%v", wanted, mapKeys(data))
		}
	}
	if !strings.Contains(string(data["usr/bin/game"]), `exec '/opt/com.example.game/game' "$@"`) {
		t.Fatalf("launcher = %q", data["usr/bin/game"])
	}
	if !strings.Contains(string(data["usr/share/applications/com.example.game.desktop"]), "Terminal=true") {
		t.Fatal("desktop entry lost terminal=true")
	}
}

func TestDebianPackageAcceptedAndExtractedByDPKGDeb(t *testing.T) {
	if _, err := os.Stat("/usr/bin/dpkg-deb"); err != nil {
		t.Skip("dpkg-deb is unavailable")
	}
	result := buildDebian(t, t.TempDir(), debianFixture(t))
	extracted := t.TempDir()
	for _, args := range [][]string{
		{"--info", result.Path},
		{"--extract", result.Path, extracted},
	} {
		execution, err := executor.Run(context.Background(), executor.Request{
			Path: "/usr/bin/dpkg-deb", Args: args, Timeout: 10 * time.Second,
		})
		if err != nil {
			t.Fatalf("dpkg-deb %v failed: %v\n%s", args, err, execution.Stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(extracted, "opt", "com.example.game", "game")); err != nil {
		t.Fatalf("extracted executable missing: %v", err)
	}
}

func debianFixture(t *testing.T) portable.Result {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "game"), "runtime", 0o700)
	writeFixture(t, filepath.Join(root, "game.flxpkg"), "package", 0o600)
	writeFixture(t, filepath.Join(root, "build-info.json"), "{}\n", 0o600)
	writeFixture(t, filepath.Join(root, "linux-runtime.json"), "{}\n", 0o600)
	writeFixture(t, filepath.Join(root, "game.png"), "png", 0o600)
	return portable.Result{
		Directory: root, Name: "game", TargetOS: "linux",
		Executable: filepath.Join(root, "game"),
		Package:    filepath.Join(root, "game.flxpkg"),
		BuildInfo:  filepath.Join(root, "build-info.json"),
		ExtraFiles: []string{
			filepath.Join(root, "game.png"),
			filepath.Join(root, "linux-runtime.json"),
		},
	}
}

func buildDebian(t *testing.T, output string, value portable.Result) installer.Result {
	t.Helper()
	result, err := (installer.Debian{}).Build(context.Background(), installer.Request{
		OutputDir: output, ProjectName: "Game", ProjectID: "com.example.game",
		Version: "1.2.3", Terminal: true, Portable: value,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func writeFixture(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // #nosec G302 -- private test fixture modes.
		t.Fatal(err)
	}
}

func readAR(t *testing.T, path string) map[string][]byte {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test-owned package.
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 8 || string(data[:8]) != "!<arch>\n" {
		t.Fatal("invalid ar magic")
	}
	result := make(map[string][]byte)
	offset := 8
	for offset < len(data) {
		if offset+60 > len(data) {
			t.Fatal("truncated ar header")
		}
		header := data[offset : offset+60]
		offset += 60
		name := strings.TrimSuffix(strings.TrimSpace(string(header[:16])), "/")
		var size int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(header[48:58])), "%d", &size); err != nil {
			t.Fatal(err)
		}
		if size < 0 || offset+size > len(data) {
			t.Fatal("invalid ar member size")
		}
		result[name] = append([]byte(nil), data[offset:offset+size]...)
		offset += size
		if size%2 != 0 {
			offset++
		}
	}
	return result
}

func readTarGZ(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gzipReader.Close() }()
	reader := tar.NewReader(gzipReader)
	result := make(map[string][]byte)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		result[strings.TrimPrefix(header.Name, "./")] = content
	}
	return result
}

func mapKeys(values map[string][]byte) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	return result
}
