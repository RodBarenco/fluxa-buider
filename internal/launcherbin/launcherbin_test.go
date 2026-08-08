package launcherbin_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
	"github.com/RodBarenco/fluxa-builder/internal/launcherbin"
	windowspkg "github.com/RodBarenco/fluxa-builder/internal/windows"
)

type target struct {
	name     string
	goos     string
	goarch   string
	os       string
	arch     string
	fileName string
}

var targets = []target{
	{"linux/amd64", "linux", "amd64", "linux", "amd64", "fluxa-launcher-linux-amd64"},
	{"windows/amd64", "windows", "amd64", "windows", "amd64", "fluxa-launcher-windows-amd64.exe"},
	{"darwin/amd64", "darwin", "amd64", "macos", "amd64", "fluxa-launcher-darwin-amd64"},
	{"darwin/arm64", "darwin", "arm64", "macos", "arm64", "fluxa-launcher-darwin-arm64"},
}

// TestEmbeddedLaunchersMatchSource rebuilds cmd/fluxa-launcher from source
// for every target `make launcher` produces, with the exact same flags, and
// asserts each result matches its committed, embedded binary byte for byte.
// Without it nothing would notice a launcher source change that was never
// followed by `make launcher` and a commit of the regenerated binaries: CI
// runs `go build`/`go test` directly, and a stale launcher still builds and
// still assembles into applications perfectly happily.
func TestEmbeddedLaunchersMatchSource(t *testing.T) {
	repoRoot := findRepoRoot(t)

	for _, tt := range targets {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			embedded, err := launcherbin.For(tt.os, tt.arch)
			if err != nil {
				t.Fatalf("For(%s, %s) error = %v", tt.os, tt.arch, err)
			}
			if len(embedded) == 0 {
				t.Fatalf("embedded %s launcher is empty", tt.name)
			}

			outputPath := filepath.Join(t.TempDir(), tt.fileName)
			execution, err := executor.Run(context.Background(), executor.Request{
				Path:    "go",
				Args:    []string{"build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", outputPath, "./cmd/fluxa-launcher"},
				Dir:     repoRoot,
				Env:     append(os.Environ(), "GOOS="+tt.goos, "GOARCH="+tt.goarch, "CGO_ENABLED=0"),
				Timeout: 2 * time.Minute,
			})
			if err != nil {
				t.Fatalf("go build cmd/fluxa-launcher failed: %v\n%s", err, execution.Stderr)
			}

			rebuilt, err := os.ReadFile(outputPath) // #nosec G304 -- path built from t.TempDir() above.
			if err != nil {
				t.Fatal(err)
			}
			if hashOf(rebuilt) != hashOf(embedded) {
				t.Fatalf("internal/launcherbin/bin/%s does not match cmd/fluxa-launcher source; run `make launcher` and commit the result", tt.fileName)
			}
		})
	}
}

// TestEmbeddedWindowsLauncherIsRealPE is the specific assertion the whole
// cross-target launcher exists for. Before this package, a Windows
// application assembled on Linux received the running Fluxa Builder ELF as
// its .exe, and the PE subsystem patch that follows rejected it with
// "unrecognized PE machine: 0x457f" — 0x457f being `\x7fE`, the ELF magic
// read as a PE machine field. Validating the committed binary structurally
// means that can never silently come back.
func TestEmbeddedWindowsLauncherIsRealPE(t *testing.T) {
	t.Parallel()

	embedded, err := launcherbin.For("windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fluxa-launcher.exe")
	if err := os.WriteFile(path, embedded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := windowspkg.ValidatePEAMD64(path); err != nil {
		t.Fatalf("ValidatePEAMD64() error = %v; the embedded Windows launcher is not a real amd64 PE", err)
	}
}

func TestForRejectsUnsupportedTarget(t *testing.T) {
	t.Parallel()

	if _, err := launcherbin.For("plan9", "amd64"); err == nil {
		t.Fatal("For(plan9, amd64) error = nil, want an error naming the unsupported target")
	}
	if _, err := launcherbin.For("linux", "arm64"); err == nil {
		t.Fatal("For(linux, arm64) error = nil, want an error: no launcher is committed for that target")
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
