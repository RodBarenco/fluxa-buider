package collector_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/RodBarenco/fluxa-builder/internal/collector"
)

func TestCollectSortedFilesKindsExcludesAndUnicode(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.flx", "main")
	writeFile(t, root, "src/z.flx", "z")
	writeFile(t, root, "src/a.flx", "a")
	writeFile(t, root, "assets/images/ação.png", "png")
	writeFile(t, root, "assets/music/theme.ogg", "ogg")
	writeFile(t, root, "assets/source/theme.psd", "psd")

	result, err := collector.Collect(context.Background(), collector.Options{
		Root:           root,
		Entry:          "main.flx",
		ModulePatterns: []string{"src/**/*.flx"},
		AssetPatterns:  []string{"assets/**", "assets/images/**"},
		Exclude:        []string{"assets/source/**"},
	})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	got := make([]string, 0, len(result.Entries))
	for _, entry := range result.Entries {
		got = append(got, string(entry.Kind)+":"+entry.Path)
	}
	want := []string{
		"asset:assets/images/ação.png",
		"asset:assets/music/theme.ogg",
		"entry:main.flx",
		"module:src/a.flx",
		"module:src/z.flx",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %#v, want %#v", got, want)
	}
	if result.TotalSize != 12 {
		t.Errorf("TotalSize = %d, want 12", result.TotalSize)
	}
}

func TestCollectInternalSymlinkPreservesLogicalPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require privileges")
	}
	root := t.TempDir()
	writeFile(t, root, "main.flx", "main")
	writeFile(t, root, "shared/image.png", "image")
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "shared", "image.png"), filepath.Join(root, "assets", "image.png")); err != nil {
		t.Fatal(err)
	}

	result, err := collector.Collect(context.Background(), collector.Options{
		Root: root, Entry: "main.flx", AssetPatterns: []string{"assets/**"},
	})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(result.Entries) != 2 || result.Entries[0].Path != "assets/image.png" || !result.Entries[0].Symlink {
		t.Fatalf("unexpected entries: %#v", result.Entries)
	}
}

func TestCollectRejectsExternalSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require privileges")
	}
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, root, "main.flx", "main")
	writeFile(t, outside, "secret.txt", "secret")
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "assets", "secret.txt")); err != nil {
		t.Fatal(err)
	}

	_, err := collector.Collect(context.Background(), collector.Options{
		Root: root, Entry: "main.flx", AssetPatterns: []string{"assets/**"},
	})
	assertKind(t, err, collector.ErrorUnsafePath)
}

func TestCollectRejectsCaseCollision(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.flx", "main")
	writeFile(t, root, "assets/Icon.png", "one")
	writeFile(t, root, "assets/icon.png", "two")

	_, err := collector.Collect(context.Background(), collector.Options{
		Root: root, Entry: "main.flx", AssetPatterns: []string{"assets/**"},
	})
	assertKind(t, err, collector.ErrorCollision)
}

func TestCollectLimits(t *testing.T) {
	tests := []struct {
		name   string
		files  map[string]string
		limits collector.Limits
	}{
		{
			name: "depth",
			files: map[string]string{
				"assets/a/b/c/file.txt": "x",
			},
			limits: collector.Limits{MaxDepth: 3},
		},
		{
			name: "count",
			files: map[string]string{
				"assets/a.txt": "a",
				"assets/b.txt": "b",
			},
			limits: collector.Limits{MaxFiles: 2},
		},
		{
			name: "file size",
			files: map[string]string{
				"assets/large.bin": "large",
			},
			limits: collector.Limits{MaxFileSize: 4},
		},
		{
			name: "total size",
			files: map[string]string{
				"assets/a.bin": "aaa",
				"assets/b.bin": "bbb",
			},
			limits: collector.Limits{MaxTotalSize: 8},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "main.flx", "main")
			for name, contents := range tt.files {
				writeFile(t, root, name, contents)
			}
			_, err := collector.Collect(context.Background(), collector.Options{
				Root: root, Entry: "main.flx", AssetPatterns: []string{"assets/**"}, Limits: tt.limits,
			})
			assertKind(t, err, collector.ErrorLimit)
		})
	}
}

func TestCollectRejectsTraversalEntryAndCancellation(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.flx", "main")

	_, err := collector.Collect(context.Background(), collector.Options{Root: root, Entry: "../main.flx"})
	assertKind(t, err, collector.ErrorInvalidInput)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = collector.Collect(ctx, collector.Options{Root: root, Entry: "main.flx"})
	assertKind(t, err, collector.ErrorCanceled)
}

func TestCollectIsDeterministic(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.flx", "main")
	writeFile(t, root, "assets/c", "c")
	writeFile(t, root, "assets/a", "a")
	options := collector.Options{Root: root, Entry: "main.flx", AssetPatterns: []string{"assets/**"}}

	first, err := collector.Collect(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := collector.Collect(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("results differ:\n%#v\n%#v", first, second)
	}
}

func writeFile(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertKind(t *testing.T, err error, want collector.ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want kind %q", want)
	}
	var collectionError *collector.Error
	if !errors.As(err, &collectionError) {
		t.Fatalf("error type = %T, want *collector.Error: %v", err, err)
	}
	if collectionError.Kind != want {
		t.Fatalf("error kind = %q, want %q: %v", collectionError.Kind, want, err)
	}
}
