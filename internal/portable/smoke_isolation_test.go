package portable_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
	"github.com/RodBarenco/fluxa-builder/internal/portable"
)

// smokeScriptThatLeaves is like smokeScript, but also writes a marker file
// into its own CWD before responding successfully — simulating a runtime
// that generates state (a key, a config file, a save slot) on first run.
func smokeScriptThatLeaves(hash string) string {
	response := map[string]any{
		"protocol": "fluxa-package-self-test-v1", "package_sha256": hash,
		"package_opened": true, "vm_compatible": true, "ui_opened": false,
	}
	data, err := json.Marshal(response)
	if err != nil {
		panic(err)
	}
	return "#!/bin/sh\nif [ \"$1\" = \"--fluxa-package-self-test\" ] && [ -f \"$0.flxpkg\" ]; then\n" +
		"echo leaked > marker.txt\n" +
		"printf '%s\\n' '" + string(data) + "'\nexit 0\nfi\nexit 9\n"
}

func TestSmokeNeverLeavesSelfTestSideEffectsInTheOriginalDirectory(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("portable shell runtime fixture is Unix-only")
	}
	fixture := newFixtureWithScript(t, "Leaky", smokeScriptThatLeaves)
	result, err := portable.Build(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	report, err := portable.SmokeDetailed(context.Background(), result, 2*time.Second)
	if err != nil {
		t.Fatalf("SmokeDetailed() error = %v, report = %#v", err, report)
	}
	if !report.PackageOpened {
		t.Fatalf("SmokeDetailed() report = %#v, want a successful self-test", report)
	}
	if _, statErr := os.Stat(filepath.Join(result.Directory, "marker.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("marker.txt leaked into the published directory (stat err = %v), want the self-test isolated from it", statErr)
	}
	entries, err := os.ReadDir(filepath.Dir(result.Directory))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != filepath.Base(result.Directory) {
			t.Errorf("isolation copy %q was not cleaned up after a successful smoke test", entry.Name())
		}
	}
}

func TestSmokeExecutableContainerIsolatesAndValidatesViaFakeRunner(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("portable shell runtime fixture is Unix-only")
	}
	fixture := newFixture(t, "Containerized", true)
	result, err := portable.Build(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	packageInfo := result.PackageHash

	var capturedDirectory, capturedExecutable string
	runner := func(ctx context.Context, executable, directory string, timeout time.Duration) (executor.Result, error) {
		capturedExecutable, capturedDirectory = executable, directory
		if directory == result.Directory {
			t.Errorf("runner received the original directory %q, want an isolated copy", directory)
		}
		if _, statErr := os.Stat(executable); statErr != nil {
			t.Errorf("runner's executable %q does not exist in the isolated copy: %v", executable, statErr)
		}
		response := map[string]any{
			"protocol": "fluxa-package-self-test-v1", "package_sha256": packageInfo,
			"package_opened": true, "vm_compatible": true, "ui_opened": false,
		}
		data, marshalErr := json.Marshal(response)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return executor.Result{Stdout: string(data) + "\n", ExitCode: 0}, nil
	}

	report, err := portable.SmokeExecutableContainer(context.Background(), result.Executable, result.Directory, packageInfo, runner, 2*time.Second)
	if err != nil {
		t.Fatalf("SmokeExecutableContainer() error = %v, report = %#v", err, report)
	}
	if !report.PackageOpened || !report.VMCompatible || report.UIOpened {
		t.Fatalf("SmokeExecutableContainer() report = %#v", report)
	}
	if capturedDirectory == "" || capturedExecutable == "" {
		t.Fatal("runner was never invoked")
	}
	if _, statErr := os.Stat(capturedDirectory); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("isolation copy %q was not cleaned up after SmokeExecutableContainer returned", capturedDirectory)
	}
}

func TestSmokeExecutableContainerRequiresARunner(t *testing.T) {
	hash := strings.Repeat("a", 64)
	_, err := portable.SmokeExecutableContainer(context.Background(), "/exe", "/dir", hash, nil, time.Second)
	assertPortableKind(t, err, portable.ErrorInvalid)
}
