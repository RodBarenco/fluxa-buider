package manifest_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RodBarenco/fluxa-builder/internal/collector"
	"github.com/RodBarenco/fluxa-builder/internal/compiler"
	"github.com/RodBarenco/fluxa-builder/internal/manifest"
	"github.com/RodBarenco/fluxa-builder/internal/project"
	"github.com/RodBarenco/fluxa-builder/internal/toolchain"
)

const validHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestEncodeDecodeRoundTripAndDeterminism(t *testing.T) {
	value := validManifest()
	value.Files = []manifest.File{value.Files[1], value.Files[0]}

	first, err := manifest.Encode(value)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manifest.Encode(value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("encoding is not deterministic:\n%s\n%s", first, second)
	}
	if bytes.Index(first, []byte(`"program/source/main.flx"`)) > bytes.Index(first, []byte(`"resources/assets/icon.png"`)) {
		t.Fatalf("files not canonically ordered:\n%s", first)
	}

	decoded, err := manifest.Decode(bytes.NewReader(first))
	if err != nil {
		t.Fatal(err)
	}
	canonical := value
	canonical.Files = []manifest.File{value.Files[1], value.Files[0]}
	if !reflect.DeepEqual(decoded, canonical) {
		t.Fatalf("round trip = %#v, want %#v", decoded, canonical)
	}
}

func TestValidateRejectsMissingUnknownVersionDuplicateAndHash(t *testing.T) {
	tests := []struct {
		name string
		edit func(*manifest.Manifest)
		kind manifest.ErrorKind
	}{
		{
			name: "missing field",
			edit: func(value *manifest.Manifest) { value.Project.Name = "" },
			kind: manifest.ErrorInvalid,
		},
		{
			name: "unknown version",
			edit: func(value *manifest.Manifest) { value.FormatVersion = 99 },
			kind: manifest.ErrorUnknownVersion,
		},
		{
			name: "duplicate path",
			edit: func(value *manifest.Manifest) {
				value.Files[1].Path = value.Files[0].Path
			},
			kind: manifest.ErrorInvalid,
		},
		{
			name: "case collision",
			edit: func(value *manifest.Manifest) {
				value.Files[1].Path = strings.ToUpper(value.Files[0].Path)
			},
			kind: manifest.ErrorInvalid,
		},
		{
			name: "invalid hash",
			edit: func(value *manifest.Manifest) { value.Files[0].SHA256 = "xyz" },
			kind: manifest.ErrorInvalid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := validManifest()
			tt.edit(&value)
			err := manifest.Validate(value)
			assertKind(t, err, tt.kind)
		})
	}
}

func TestDecodeRejectsTooLargeAndUnknownFields(t *testing.T) {
	_, err := manifest.Decode(strings.NewReader(strings.Repeat("x", 16*1024*1024+1)))
	assertKind(t, err, manifest.ErrorTooLarge)

	encoded, err := manifest.Encode(validManifest())
	if err != nil {
		t.Fatal(err)
	}
	altered := bytes.Replace(encoded, []byte(`"format_version": 1,`), []byte(`"format_version": 1, "unknown": true,`), 1)
	_, err = manifest.Decode(bytes.NewReader(altered))
	assertKind(t, err, manifest.ErrorInvalid)

	missingBoolean := bytes.Replace(encoded, []byte(`    "terminal": true`), []byte(`    "other": true`), 1)
	_, err = manifest.Decode(bytes.NewReader(missingBoolean))
	assertKind(t, err, manifest.ErrorInvalid)
}

func TestNewHashesAssetsAndOmitsMachinePaths(t *testing.T) {
	root := t.TempDir()
	assetPath := filepath.Join(root, "assets", "ação.png")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetPath, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	librariesData := []byte("raylib = true\n")
	if err := os.WriteFile(filepath.Join(root, "fluxa.libs"), librariesData, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &project.Config{
		Root: root,
		Project: project.Metadata{
			Name: "Game", ID: "com.example.game", Version: "1.0.0",
			Entry: "main.flx", Type: "desktop",
		},
		Build: project.BuildConfig{Terminal: false},
	}
	value, err := manifest.New(context.Background(), manifest.Input{
		Project: cfg,
		Toolchain: toolchain.Identity{
			Protocol: "runtime-info-v1", SHA256: validHash,
		},
		Compilation: compiler.Result{
			Format: compiler.FormatSource, Debug: true, SourceExposed: true,
			Artifacts: []compiler.Artifact{{
				Path: "source/main.flx", LogicalPath: "main.flx", Size: 4, SHA256: validHash,
			}},
		},
		Collection: collector.Result{Entries: []collector.Entry{{
			Path: "assets/ação.png", SourcePath: assetPath, Kind: collector.KindAsset, Size: 5,
		}}},
		TargetOS: "linux", TargetArch: "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Files) != 2 || value.Files[1].Path != "resources/assets/ação.png" {
		t.Fatalf("files = %#v", value.Files)
	}
	librariesDigest := sha256.Sum256(librariesData)
	if value.Toolchain.LibrariesSHA256 != hex.EncodeToString(librariesDigest[:]) {
		t.Fatalf("libraries SHA-256 = %q", value.Toolchain.LibrariesSHA256)
	}
	encoded, err := manifest.Encode(value)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(root)) {
		t.Fatalf("manifest leaked machine path:\n%s", encoded)
	}
	if !bytes.Contains(encoded, []byte(`"preflight": "not_run"`)) ||
		!bytes.Contains(encoded, []byte(`"source_exposed": true`)) {
		t.Fatalf("manifest omitted build safety state:\n%s", encoded)
	}
}

func TestNewDetectsChangedAssetAndCancellation(t *testing.T) {
	root := t.TempDir()
	asset := filepath.Join(root, "asset.bin")
	if err := os.WriteFile(asset, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := validInput()
	input.Collection = collector.Result{Entries: []collector.Entry{{
		Path: "asset.bin", SourcePath: asset, Kind: collector.KindAsset, Size: 1,
	}}}
	_, err := manifest.New(context.Background(), input)
	assertKind(t, err, manifest.ErrorInvalid)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input.Collection.Entries[0].Size = 7
	_, err = manifest.New(ctx, input)
	assertKind(t, err, manifest.ErrorCanceled)
}

func TestWriteFileIsAtomicAndRefusesExistingDestination(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "manifest.json")
	value := validManifest()
	if err := manifest.WriteFile(path, value); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- test-controlled temporary path.
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("written manifest invalid: %v", err)
	}
	if err := manifest.WriteFile(path, value); err == nil {
		t.Fatal("WriteFile() overwrote existing destination")
	}
}

func validManifest() manifest.Manifest {
	return manifest.Manifest{
		FormatVersion: manifest.CurrentFormatVersion,
		Project: manifest.Project{
			Name: "Game", ID: "com.example.game", Version: "1.0.0", Entry: "main.flx", Type: "desktop",
		},
		Toolchain: manifest.Toolchain{
			Protocol: "runtime-info-v1", FluxaSHA256: validHash, LibrariesSHA256: validHash,
		},
		Target: manifest.Target{OS: "linux", Arch: "amd64", Terminal: true},
		Build: manifest.Build{
			Preflight: "not_run", ProgramFormat: "fluxa-source", Debug: true, SourceExposed: true,
		},
		Files: []manifest.File{
			{Path: "program/source/main.flx", LogicalPath: "main.flx", Kind: "program", Size: 4, SHA256: validHash},
			{Path: "resources/assets/icon.png", LogicalPath: "assets/icon.png", Kind: "asset", Size: 3, SHA256: validHash},
		},
	}
}

func validInput() manifest.Input {
	return manifest.Input{
		Project: &project.Config{
			Project: project.Metadata{Name: "Game", ID: "com.example.game", Version: "1.0.0", Entry: "main.flx", Type: "desktop"},
		},
		Toolchain: toolchain.Identity{Protocol: "runtime-info-v1", SHA256: validHash},
		Compilation: compiler.Result{
			Format: compiler.FormatSource,
			Artifacts: []compiler.Artifact{{
				Path: "source/main.flx", LogicalPath: "main.flx", Size: 4, SHA256: validHash,
			}},
			Debug: true, SourceExposed: true,
		},
		TargetOS: "linux", TargetArch: "amd64",
	}
}

func assertKind(t *testing.T, err error, want manifest.ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %q", want)
	}
	var manifestError *manifest.Error
	if !errors.As(err, &manifestError) {
		t.Fatalf("error type = %T, want *manifest.Error: %v", err, err)
	}
	if manifestError.Kind != want {
		t.Fatalf("error kind = %q, want %q: %v", manifestError.Kind, want, err)
	}
}
