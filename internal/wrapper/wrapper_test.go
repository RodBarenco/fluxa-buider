package wrapper_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
	"github.com/RodBarenco/fluxa-builder/internal/wrapper"
)

// TestEmbeddedBinariesMatchSource rebuilds cmd/fluxa-runtime-wrapper from
// source for every target `make wrapper` produces, with the exact same
// flags, and asserts each result matches its committed, embedded binary
// byte-for-byte. This is the guard against shipping a stale wrapper: CI
// runs `go build`/`go test` directly, so nothing else would notice a
// source change that wasn't followed by `make wrapper` and a commit of the
// regenerated binaries. The macOS builds are cross-compiled and verified
// this way on every host, including this one — cross-compilation succeeding
// and matching the committed binary is not the same as having run the
// binary on real macOS hardware; see docs/adr/0025-linux-adapted-runtime-wrapper.md
// for the exact scope of what is and is not verified for macOS.
func TestEmbeddedBinariesMatchSource(t *testing.T) {
	cases := []struct {
		name     string
		goos     string
		goarch   string
		embedded []byte
	}{
		{"linux/amd64", "linux", "amd64", wrapper.LinuxAMD64},
		{"darwin/amd64", "darwin", "amd64", wrapper.DarwinAMD64},
		{"darwin/arm64", "darwin", "arm64", wrapper.DarwinARM64},
	}
	repoRoot := findRepoRoot(t)

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if len(tt.embedded) == 0 {
				t.Fatalf("embedded %s wrapper is empty", tt.name)
			}

			outputPath := filepath.Join(t.TempDir(), "fluxa-runtime-wrapper-"+tt.goos+"-"+tt.goarch)
			execution, err := executor.Run(context.Background(), executor.Request{
				Path:    "go",
				Args:    []string{"build", "-trimpath", "-buildvcs=false", "-o", outputPath, "./cmd/fluxa-runtime-wrapper"},
				Dir:     repoRoot,
				Env:     append(os.Environ(), "GOOS="+tt.goos, "GOARCH="+tt.goarch, "CGO_ENABLED=0"),
				Timeout: 2 * time.Minute,
			})
			if err != nil {
				t.Fatalf("go build cmd/fluxa-runtime-wrapper failed: %v\n%s", err, execution.Stderr)
			}

			rebuilt, err := os.ReadFile(outputPath) // #nosec G304 -- path built from t.TempDir() above.
			if err != nil {
				t.Fatal(err)
			}
			if hashOf(rebuilt) != hashOf(tt.embedded) {
				t.Fatalf("internal/wrapper/bin/fluxa-runtime-wrapper-%s-%s does not match cmd/fluxa-runtime-wrapper source; run `make wrapper` and commit the result", tt.goos, tt.goarch)
			}
		})
	}
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("could not locate repository root (go.mod not found)")
		}
		directory = parent
	}
}
