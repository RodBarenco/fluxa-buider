package flxpkg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RodBarenco/fluxa-builder/internal/manifest"
)

func TestWriteRejectsEmptyPackage(t *testing.T) {
	value := baseManifest(nil)
	_, err := Write(context.Background(), Request{
		OutputPath: filepath.Join(t.TempDir(), "empty.flxpkg"),
		Manifest:   value,
		Sources:    map[string]string{},
	})
	assertPackageError(t, err)
}

func TestWriteAndVerifyMinimalCompressedAndUncompressed(t *testing.T) {
	for _, compress := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "compressed"}[compress], func(t *testing.T) {
			root := t.TempDir()
			source := writePackageSource(t, root, "main.flx", []byte(`print("hello")`))
			file := manifestFile("program/source/main.flx", "main.flx", "program", source)
			output := filepath.Join(root, "app.flxpkg")

			result, err := Write(context.Background(), Request{
				OutputPath: output,
				Manifest:   baseManifest([]manifest.File{file}),
				Sources:    map[string]string{file.Path: source},
				Compress:   compress,
			})
			if err != nil {
				t.Fatal(err)
			}
			info, err := Verify(output)
			if err != nil {
				t.Fatal(err)
			}
			if result.SHA256 != info.SHA256 || result.FileCount != 1 ||
				info.Entries[0].Path != file.Path || info.Manifest.Project.ID != "com.example.game" {
				t.Fatalf("result=%#v info=%#v", result, info)
			}
			wantCompression := "none"
			if compress {
				wantCompression = "zlib"
			}
			if info.Entries[0].Compression != wantCompression {
				t.Fatalf("compression = %q, want %q", info.Entries[0].Compression, wantCompression)
			}
		})
	}
}

func TestMultipleAndLargeFiles(t *testing.T) {
	root := t.TempDir()
	largeData := bytes.Repeat([]byte("Fluxa deterministic payload\n"), 100_000)
	sources := []struct {
		packagePath string
		logicalPath string
		kind        string
		name        string
		data        []byte
	}{
		{"program/source/main.flx", "main.flx", "program", "main.flx", []byte("main")},
		{"resources/assets/big.dat", "assets/big.dat", "asset", "big.dat", largeData},
		{"resources/assets/icon.png", "assets/icon.png", "asset", "icon.png", []byte("png")},
	}
	files := make([]manifest.File, 0, len(sources))
	mapping := make(map[string]string)
	for _, source := range sources {
		path := writePackageSource(t, root, source.name, source.data)
		file := manifestFile(source.packagePath, source.logicalPath, source.kind, path)
		files = append(files, file)
		mapping[file.Path] = path
	}
	output := filepath.Join(root, "large.flxpkg")
	if _, err := Write(context.Background(), Request{
		OutputPath: output, Manifest: baseManifest(files), Sources: mapping, Compress: true,
	}); err != nil {
		t.Fatal(err)
	}
	info, err := Verify(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Entries) != 3 || info.Entries[1].OriginalSize != uint64(len(largeData)) {
		t.Fatalf("entries = %#v", info.Entries)
	}
}

func TestPackageIsDeterministic(t *testing.T) {
	root := t.TempDir()
	source := writePackageSource(t, root, "main.flx", []byte("main"))
	file := manifestFile("program/source/main.flx", "main.flx", "program", source)
	assetSource := writePackageSource(t, root, "icon.png", []byte("png"))
	asset := manifestFile("resources/assets/icon.png", "assets/icon.png", "asset", assetSource)
	request := Request{
		Manifest: baseManifest([]manifest.File{file, asset}),
		Sources:  map[string]string{file.Path: source, asset.Path: assetSource},
		Compress: true,
	}
	request.OutputPath = filepath.Join(root, "first.flxpkg")
	if _, err := Write(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Manifest.Files = []manifest.File{asset, file}
	request.OutputPath = filepath.Join(root, "second.flxpkg")
	if _, err := Write(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(root, "first.flxpkg")) // #nosec G304 -- test path.
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(root, "second.flxpkg")) // #nosec G304 -- test path.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("packages built from identical inputs differ")
	}
}

func TestVerifyRejectsCorruption(t *testing.T) {
	root := t.TempDir()
	validPath := createTwoFilePackage(t, root)
	valid, err := os.ReadFile(validPath) // #nosec G304 -- test path.
	if err != nil {
		t.Fatal(err)
	}
	var header packageHeader
	if err := binary.Read(bytes.NewReader(valid[:headerSize]), binary.LittleEndian, &header); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func([]byte)
	}{
		{"truncated", func(data []byte) { data = data[:headerSize-1]; writeMutation(t, root, "truncated", data) }},
		{"invalid manifest offset", func(data []byte) {
			binary.LittleEndian.PutUint64(data[16:24], headerSize+1)
		}},
		{"invalid magic", func(data []byte) {
			data[0] ^= 0xff
		}},
		{"unknown version", func(data []byte) {
			binary.LittleEndian.PutUint32(data[8:12], formatVersion+1)
		}},
		{"unknown flags", func(data []byte) {
			binary.LittleEndian.PutUint32(data[12:16], 1)
		}},
		{"invalid entry offset", func(data []byte) {
			offsetPosition := int(header.TableOffset) + 12 // #nosec G115 -- valid small test fixture.
			binary.LittleEndian.PutUint64(data[offsetPosition:offsetPosition+8], header.PayloadOffset+1)
			refreshBodyHash(t, data)
		}},
		{"invalid stored size", func(data []byte) {
			position := int(header.TableOffset) + 20 // #nosec G115 -- valid small test fixture.
			binary.LittleEndian.PutUint64(data[position:position+8], header.PayloadSize+1)
			refreshBodyHash(t, data)
		}},
		{"invalid entry hash", func(data []byte) {
			position := int(header.TableOffset) + 36 // #nosec G115 -- valid small test fixture.
			data[position] ^= 0xff
			refreshBodyHash(t, data)
		}},
		{"unknown compression", func(data []byte) {
			position := int(header.TableOffset) + 7 // #nosec G115 -- valid small test fixture.
			data[position] = 0xff
			refreshBodyHash(t, data)
		}},
		{"corrupt manifest", func(data []byte) {
			data[header.ManifestOffset] = '!'
			refreshBodyHash(t, data)
		}},
		{"duplicate table entry", func(data []byte) {
			firstPathStart := int(header.TableOffset) + 4 + int(entryFixedSize) // #nosec G115 -- valid small test fixture.
			firstPathLength := int(binary.LittleEndian.Uint16(data[header.TableOffset+4 : header.TableOffset+6]))
			secondStart := firstPathStart + firstPathLength
			secondPathLength := int(binary.LittleEndian.Uint16(data[secondStart : secondStart+2]))
			secondPathStart := secondStart + int(entryFixedSize)
			if firstPathLength != secondPathLength {
				t.Skip("fixture paths must have equal lengths")
			}
			copy(data[secondPathStart:secondPathStart+secondPathLength], data[firstPathStart:firstPathStart+firstPathLength])
			refreshBodyHash(t, data)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := append([]byte(nil), valid...)
			if tt.name == "truncated" {
				path := filepath.Join(root, "truncated.flxpkg")
				if err := os.WriteFile(path, data[:headerSize-1], 0o600); err != nil { // #nosec G703 -- test-controlled temporary path.
					t.Fatal(err)
				}
				_, err := Verify(path)
				assertPackageError(t, err)
				return
			}
			tt.mutate(data)
			path := filepath.Join(root, strings.ReplaceAll(tt.name, " ", "-")+".flxpkg")
			if err := os.WriteFile(path, data, 0o600); err != nil { // #nosec G703 -- test-controlled temporary path.
				t.Fatal(err)
			}
			_, err := Verify(path)
			assertPackageError(t, err)
		})
	}
}

func TestVerifyRejectsTrailingPackageAndCompressedEntryBytes(t *testing.T) {
	root := t.TempDir()
	validPath := createTwoFilePackage(t, root)
	valid, err := os.ReadFile(validPath) // #nosec G304 -- test-controlled path.
	if err != nil {
		t.Fatal(err)
	}
	trailingPath := filepath.Join(root, "trailing.flxpkg")
	if err := os.WriteFile(trailingPath, append(valid, 0), 0o600); err != nil { // #nosec G703 -- test-controlled temporary path.
		t.Fatal(err)
	}
	_, err = Verify(trailingPath)
	assertPackageError(t, err)

	source := writePackageSource(t, root, "compressed.txt", []byte(strings.Repeat("compress me", 100)))
	file := manifestFile("resources/compressed.txt", "compressed.txt", "asset", source)
	compressedPath := filepath.Join(root, "compressed.flxpkg")
	if _, err := Write(context.Background(), Request{
		OutputPath: compressedPath,
		Manifest:   baseManifest([]manifest.File{file}),
		Sources:    map[string]string{file.Path: source},
		Compress:   true,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(compressedPath) // #nosec G304 -- test-controlled path.
	if err != nil {
		t.Fatal(err)
	}
	var header packageHeader
	if err := binary.Read(bytes.NewReader(data[:headerSize]), binary.LittleEndian, &header); err != nil {
		t.Fatal(err)
	}
	data = append(data, 0)
	binary.LittleEndian.PutUint64(data[56:64], header.PayloadSize+1)
	storedPosition := int(header.TableOffset) + 20 // #nosec G115 -- valid small test fixture.
	stored := binary.LittleEndian.Uint64(data[storedPosition : storedPosition+8])
	binary.LittleEndian.PutUint64(data[storedPosition:storedPosition+8], stored+1)
	refreshBodyHash(t, data)
	corruptPath := filepath.Join(root, "compressed-trailing.flxpkg")
	if err := os.WriteFile(corruptPath, data, 0o600); err != nil { // #nosec G703 -- test-controlled temporary path.
		t.Fatal(err)
	}
	_, err = Verify(corruptPath)
	assertPackageOperation(t, err, "validate compressed payload")
}

func TestVerifyRejectsCompressionBombDeclaration(t *testing.T) {
	entry := tableEntry{
		Path: "resources/bomb.bin", Compression: compressionZlib,
		StoredSize: 1, OriginalSize: 5 * 1024 * 1024,
	}
	err := verifyPayload(bytes.NewReader([]byte{0}), entry)
	assertPackageOperation(t, err, "validate compression ratio")
}

func TestWriteRejectsChangedSourceDuplicateDestinationAndCancellation(t *testing.T) {
	root := t.TempDir()
	source := writePackageSource(t, root, "main.flx", []byte("changed"))
	file := manifest.File{
		Path: "program/source/main.flx", LogicalPath: "main.flx", Kind: "program",
		Size: 4, SHA256: hex.EncodeToString(sha256.New().Sum(nil)),
	}
	_, err := Write(context.Background(), Request{
		OutputPath: filepath.Join(root, "changed.flxpkg"),
		Manifest:   baseManifest([]manifest.File{file}),
		Sources:    map[string]string{file.Path: source},
	})
	assertPackageError(t, err)

	validFile := manifestFile(file.Path, file.LogicalPath, file.Kind, source)
	output := filepath.Join(root, "exists.flxpkg")
	if err := os.WriteFile(output, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Write(context.Background(), Request{
		OutputPath: output, Manifest: baseManifest([]manifest.File{validFile}),
		Sources: map[string]string{validFile.Path: source},
	})
	assertPackageError(t, err)
	data, readErr := os.ReadFile(output) // #nosec G304 -- test path.
	if readErr != nil || !reflect.DeepEqual(data, []byte("existing")) {
		t.Fatalf("existing output changed: %q, %v", data, readErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Write(ctx, Request{
		OutputPath: filepath.Join(root, "canceled.flxpkg"),
		Manifest:   baseManifest([]manifest.File{validFile}),
		Sources:    map[string]string{validFile.Path: source},
	})
	assertPackageError(t, err)
}

func createTwoFilePackage(t *testing.T, root string) string {
	t.Helper()
	first := writePackageSource(t, root, "a.txt", []byte("aaaa"))
	second := writePackageSource(t, root, "b.txt", []byte("bbbb"))
	firstFile := manifestFile("resources/a.txt", "a.txt", "asset", first)
	secondFile := manifestFile("resources/b.txt", "b.txt", "asset", second)
	output := filepath.Join(root, "valid.flxpkg")
	_, err := Write(context.Background(), Request{
		OutputPath: output,
		Manifest:   baseManifest([]manifest.File{firstFile, secondFile}),
		Sources:    map[string]string{firstFile.Path: first, secondFile.Path: second},
	})
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func baseManifest(files []manifest.File) manifest.Manifest {
	return manifest.Manifest{
		FormatVersion: manifest.CurrentFormatVersion,
		Project: manifest.Project{
			Name: "Game", ID: "com.example.game", Version: "1.0.0", Entry: "main.flx", Type: "desktop",
		},
		Toolchain: manifest.Toolchain{
			Protocol:    "runtime-info-v1",
			FluxaSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		Target: manifest.Target{OS: "linux", Arch: "amd64", Terminal: true},
		Build: manifest.Build{
			Preflight: "not_run", ProgramFormat: "fluxa-source", Debug: true, SourceExposed: true,
		},
		Files: files,
	}
}

func manifestFile(packagePath, logicalPath, kind, source string) manifest.File {
	data, err := os.ReadFile(source) // #nosec G304 -- test source.
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(data)
	return manifest.File{
		Path: packagePath, LogicalPath: logicalPath, Kind: kind,
		Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]),
	}
}

func writePackageSource(t *testing.T, root, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func refreshBodyHash(t *testing.T, data []byte) {
	t.Helper()
	var header packageHeader
	if err := binary.Read(bytes.NewReader(data[:headerSize]), binary.LittleEndian, &header); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data[headerSize:])
	copy(data[64:96], digest[:])
}

func writeMutation(t *testing.T, root, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name+".flxpkg"), data, 0o600); err != nil { // #nosec G703 -- test-controlled path.
		t.Fatal(err)
	}
}

func assertPackageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want package error")
	}
	var packageErr *Error
	if !errors.As(err, &packageErr) {
		t.Fatalf("error type = %T, want *flxpkg.Error: %v", err, err)
	}
}

func assertPackageOperation(t *testing.T, err error, operation string) {
	t.Helper()
	assertPackageError(t, err)
	var packageErr *Error
	if !errors.As(err, &packageErr) || packageErr.Operation != operation {
		t.Fatalf("operation = %q, want %q: %v", packageErr.Operation, operation, err)
	}
}
