//go:build linux

package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunInitAcquiresToolchainAutomaticallyOnLinux is a real, opt-in,
// end-to-end test of the whole `fluxa-builder init` wizard flow through
// automatic acquisition (docs/adr/0027-automatic-toolchain-acquisition.md):
// it answers "yes" to "download and build automatically", lets the wizard
// actually build the real fluxa-lang toolchain inside a Docker container,
// register it, persist the toolchain path into fluxa.toml, and register
// the runtime — the same real flow internal/toolchainbuild's own
// TestAcquireLinuxBuildsRealFluxaBinary exercises directly, but here
// through the interactive wizard exactly as a real user would drive it.
//
// It is skipped by default for the same reason: a full build compiles
// raylib and fluxa-lang from scratch on first run, taking several
// minutes and needing network access. Run explicitly with:
//
//	FLUXA_BUILDER_DOCKER_TESTS=1 go test ./internal/app/... -run TestRunInitAcquiresToolchainAutomaticallyOnLinux -v -timeout 25m
func TestRunInitAcquiresToolchainAutomaticallyOnLinux(t *testing.T) {
	if os.Getenv("FLUXA_BUILDER_DOCKER_TESTS") != "1" {
		t.Skip("set FLUXA_BUILDER_DOCKER_TESTS=1 to run the real Docker build (slow, needs network)")
	}

	builderHome := t.TempDir()
	t.Setenv("FLUXA_BUILDER_HOME", builderHome)
	t.Setenv("XDG_DATA_HOME", filepath.Join(builderHome, "data"))

	root := writeWizardProject(t, `[project]
name = "Docker Acquire Test"
id = "com.example.docker-acquire-test"
version = "1.0.0"
entry = "main.flx"
`)

	stdin := strings.Join([]string{
		root, // project directory
		"",   // optional: build.terminal -> accept default
		"",   // optional: write build.terminal -> accept default
		"",   // optional: build.assets -> skip
		"",   // optional: build.exclude -> skip
		"",   // optional: build.persistent -> skip
		"",   // optional: save package.include_source? -> default yes
		"",   // optional: host platform icon -> skip
		"",   // choose target -> host default
		"",   // output directory -> keep default
		"",   // path to local fluxa executable -> skip
		"y",  // download and build automatically? -> yes
		"",   // confirm: build the Linux toolchain image
		"",   // confirm: download fluxa-lang and build it
		"",   // save toolchain path to fluxa.toml? -> default yes
		"",   // remove cached checkout/image now? -> default no, keep cache
		"n",  // build now in development/source-exposed mode? -> no
	}, "\n") + "\n"

	// Declining the final "build now" prompt stops the wizard right after
	// proving the part this test actually targets — automatic
	// acquisition, registration, and re-resolving the freshly persisted
	// toolchain path — without going on to exercise the full build/smoke
	// pipeline, which os.Executable() cannot support meaningfully from
	// inside `go test` (it resolves to the test binary itself, not a
	// real fluxa-builder executable) and which internal/portable's own
	// tests already cover with a proper fake launcher.
	var stdout, stderr bytes.Buffer
	code := runInit(strings.NewReader(stdin), &stdout, &stderr, defaultBuildDependencies())
	if code != 1 {
		t.Fatalf("runInit() code = %d, want 1 (aborted after declining the final build prompt); stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Aborted.") {
		t.Errorf("stdout = %q, want the wizard to abort cleanly after declining to build", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Built and registered a Fluxa toolchain and runtime automatically") {
		t.Errorf("stdout = %q, want confirmation that acquisition succeeded", stdout.String())
	}

	updatedConfig, err := os.ReadFile(filepath.Join(root, "fluxa.toml")) // #nosec G304 -- test-owned fixture path.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updatedConfig), "[toolchain]") {
		t.Errorf("fluxa.toml = %q, want a persisted [toolchain] path", string(updatedConfig))
	}
}
