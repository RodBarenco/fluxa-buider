package mesafallback

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestEnsureCachedDownloadsVerifiesAndExtractsRealArchive is a real,
// opt-in, end-to-end test: it downloads the actual pinned Mesa release
// from GitHub, verifies its checksum for real, builds the extraction
// Docker image, extracts the real archive inside it, and confirms every
// expected DLL lands in the cache — then runs a second time and confirms
// the cache is reused without hitting the network again (by pointing at
// an unreachable URL is not practical here since the URL is a package
// constant, so reuse is instead confirmed by timing: a cache hit must be
// fast).
//
// Skipped by default: downloads a real ~57 MB file and needs network
// access. Run explicitly with:
//
//	FLUXA_BUILDER_DOCKER_TESTS=1 go test ./internal/mesafallback/... -v -timeout 10m
func TestEnsureCachedDownloadsVerifiesAndExtractsRealArchive(t *testing.T) {
	if os.Getenv("FLUXA_BUILDER_DOCKER_TESTS") != "1" {
		t.Skip("set FLUXA_BUILDER_DOCKER_TESTS=1 to run the real download/extraction (slow, needs network)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if !dockerAvailable(ctx) {
		t.Skip("docker is not installed or not reachable")
	}

	cacheDir := filepath.Join(t.TempDir(), "mesa-cache")
	result, err := EnsureCached(ctx, cacheDir)
	if err != nil {
		t.Fatalf("EnsureCached() error = %v", err)
	}
	if result != cacheDir {
		t.Fatalf("EnsureCached() = %q, want %q", result, cacheDir)
	}
	for _, name := range DLLNames {
		info, err := os.Stat(filepath.Join(cacheDir, name))
		if err != nil {
			t.Fatalf("%s missing from cache: %v", name, err)
		}
		if info.Size() == 0 {
			t.Fatalf("%s is empty", name)
		}
		t.Logf("%s: %d bytes", name, info.Size())
	}

	opengl32, err := os.ReadFile(filepath.Join(cacheDir, "opengl32.dll")) // #nosec G304 -- test-owned cache path.
	if err != nil {
		t.Fatal(err)
	}
	opengl32sw, err := os.ReadFile(filepath.Join(cacheDir, "opengl32sw.dll")) // #nosec G304 -- test-owned cache path.
	if err != nil {
		t.Fatal(err)
	}
	if string(opengl32) != string(opengl32sw) {
		t.Fatal("opengl32sw.dll must be byte-identical to opengl32.dll (a duplicate, not a distinct extracted file)")
	}

	start := time.Now()
	if _, err := EnsureCached(ctx, cacheDir); err != nil {
		t.Fatalf("second EnsureCached() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("second EnsureCached() took %s, want a fast cache hit with no network/Docker activity", elapsed)
	}
}
