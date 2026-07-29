package flxpkg

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/RodBarenco/fluxa-builder/internal/manifest"
)

func FuzzPackageReader(f *testing.F) {
	minimal := fuzzSeedPackage(f, false, false)
	complete := fuzzSeedPackage(f, true, true)

	f.Add(minimal)
	f.Add(complete)
	f.Add(append([]byte(nil), minimal[:headerSize-1]...))
	f.Add(bytes.Repeat([]byte{0xa5}, int(headerSize)))

	tableCorrupt := append([]byte(nil), complete...)
	tableOffset := int(readSeedHeader(f, complete).TableOffset) // #nosec G115 -- generated test package is small.
	tableCorrupt[tableOffset] ^= 0xff
	f.Add(tableCorrupt)

	manifestCorrupt := append([]byte(nil), minimal...)
	manifestOffset := int(readSeedHeader(f, minimal).ManifestOffset) // #nosec G115 -- generated test package is small.
	manifestCorrupt[manifestOffset] = '!'
	f.Add(manifestCorrupt)

	f.Fuzz(func(t *testing.T, data []byte) {
		// The reader must reject or fully validate arbitrary input without panic.
		size := int64(len(data)) // #nosec G115 -- Go slice length always fits int64 on supported targets.
		info, err := verifyReader(bytes.NewReader(data), size, "fuzz input")
		if err == nil {
			if info.Size != size || len(info.Entries) == 0 {
				t.Fatalf("accepted package returned inconsistent info: %#v", info)
			}
		}
	})
}

func fuzzSeedPackage(f *testing.F, compress, withAsset bool) []byte {
	f.Helper()
	root := f.TempDir()
	mainPath := filepath.Join(root, "main.flx")
	if err := os.WriteFile(mainPath, []byte("main"), 0o600); err != nil {
		f.Fatal(err)
	}
	mainFile := manifestFile("program/source/main.flx", "main.flx", "program", mainPath)
	files := []manifest.File{mainFile}
	sources := map[string]string{mainFile.Path: mainPath}
	if withAsset {
		assetPath := filepath.Join(root, "asset.bin")
		if err := os.WriteFile(assetPath, []byte("asset payload"), 0o600); err != nil {
			f.Fatal(err)
		}
		assetFile := manifestFile("resources/assets/asset.bin", "assets/asset.bin", "asset", assetPath)
		files = append(files, assetFile)
		sources[assetFile.Path] = assetPath
	}
	output := filepath.Join(root, "seed.flxpkg")
	if _, err := Write(context.Background(), Request{
		OutputPath: output,
		Manifest:   baseManifest(files),
		Sources:    sources,
		Compress:   compress,
	}); err != nil {
		f.Fatal(err)
	}
	data, err := os.ReadFile(output) // #nosec G304 -- fuzz seed path is test-controlled.
	if err != nil {
		f.Fatal(err)
	}
	return data
}

func readSeedHeader(f *testing.F, data []byte) packageHeader {
	f.Helper()
	var header packageHeader
	if err := binary.Read(bytes.NewReader(data), binary.LittleEndian, &header); err != nil {
		f.Fatal(err)
	}
	return header
}
