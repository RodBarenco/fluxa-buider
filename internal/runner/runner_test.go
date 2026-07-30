package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/RodBarenco/fluxa-builder/internal/manifest"
	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
)

func TestRunPreservesDeclaredDataBetweenExecutions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell runtime fixture is POSIX-only")
	}
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	marker := filepath.Join(root, "second-run-ok")
	runtimePath := filepath.Join(root, "fluxa")
	script := "#!/bin/sh\n" +
		"if [ \"$(cat nave.db)\" = saved ] && [ -f cards/new-card.qoi ]; then\n" +
		"  : > \"" + marker + "\"\n" +
		"else\n" +
		"  printf saved > nave.db\n" +
		"  mkdir -p cards\n" +
		"  printf card > cards/new-card.qoi\n" +
		"fi\n"
	if err := os.WriteFile(runtimePath, []byte(script), 0o700); err != nil { // #nosec G306 -- executable test fixture.
		t.Fatal(err)
	}

	sources := map[string][]byte{
		"program/source/main.flx":  []byte("main"),
		"resources/fluxa.toml":     []byte("[project]\n"),
		"resources/nave.db":        []byte("seed"),
		"resources/cards/base.qoi": []byte("base"),
	}
	files := make([]manifest.File, 0, len(sources))
	sourcePaths := make(map[string]string, len(sources))
	for packagePath, data := range sources {
		sourcePath := filepath.Join(root, hex.EncodeToString(hashBytes([]byte(packagePath)))+".src")
		if err := os.WriteFile(sourcePath, data, 0o600); err != nil {
			t.Fatal(err)
		}
		logical := packagePath
		kind := "asset"
		if packagePath == "program/source/main.flx" {
			logical = "main.flx"
			kind = "program"
		} else {
			logical = packagePath[len("resources/"):]
		}
		files = append(files, manifest.File{
			Path: packagePath, LogicalPath: logical, Kind: kind,
			Size: int64(len(data)), SHA256: hex.EncodeToString(hashBytes(data)),
		})
		sourcePaths[packagePath] = sourcePath
	}
	value := manifest.Manifest{
		FormatVersion: manifest.CurrentFormatVersion,
		Project: manifest.Project{
			Name: "Persistence Test", ID: "com.example.persistence-test",
			Version: "1.0.0", Entry: "main.flx", Type: "desktop",
		},
		Toolchain: manifest.Toolchain{
			Protocol: "runtime-info-v1", FluxaSHA256: testHash('a'), LibrariesSHA256: testHash('b'),
		},
		Target: manifest.Target{OS: "linux", Arch: "amd64"},
		Build: manifest.Build{
			Preflight: "not_run", ProgramFormat: "fluxa-source",
			Debug: true, SourceExposed: true,
			Persistent: []string{"nave.db", "cards/**"}, Exported: []string{"cards/**"},
		},
		Files: files,
	}
	packagePath := filepath.Join(root, "app.flxpkg")
	if _, err := flxpkg.Write(context.Background(), flxpkg.Request{
		OutputPath: packagePath, Manifest: value, Sources: sourcePaths, Compress: true,
	}); err != nil {
		t.Fatal(err)
	}
	request := Request{
		PackagePath: packagePath, RuntimePath: runtimePath,
		DistributionDir: filepath.Join(root, "distribution"),
		Stdin:           nil, Stdout: os.Stdout, Stderr: os.Stderr,
	}
	if err := os.Mkdir(request.DistributionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("second execution did not observe the saved database and generated card")
	}
	exported := filepath.Join(request.DistributionDir, "cards", "new-card.qoi")
	if data, err := os.ReadFile(exported); err != nil || string(data) != "card" { // #nosec G304 -- test-owned path.
		t.Fatalf("exported card = %q, %v", data, err)
	}
}

func hashBytes(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func testHash(value byte) string {
	return string(make([]byte, 0)) + hex.EncodeToString(bytesOf(value, sha256.Size))
}

func bytesOf(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}
