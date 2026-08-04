package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/portable"
	"github.com/RodBarenco/fluxa-builder/internal/project"
	runtimepkg "github.com/RodBarenco/fluxa-builder/internal/runtime"
	"github.com/RodBarenco/fluxa-builder/internal/toolchain"
)

func newTestWizard(t *testing.T, stdin string) (*wizard, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	hostOS, hostArch := hostTargetOS()
	w := &wizard{
		reader:   bufio.NewReader(strings.NewReader(stdin)),
		stdout:   &stdout,
		stderr:   &stderr,
		deps:     defaultBuildDependencies(),
		hostOS:   hostOS,
		hostArch: hostArch,
	}
	return w, &stdout, &stderr
}

func writeWizardProject(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fluxa.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.flx"), []byte(`print("ok")`), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestHostTargetOS(t *testing.T) {
	t.Parallel()

	osName, arch := hostTargetOS()
	if arch != runtime.GOARCH {
		t.Errorf("arch = %q, want %q", arch, runtime.GOARCH)
	}
	if runtime.GOOS == "darwin" {
		if osName != "macos" {
			t.Errorf("osName = %q, want macos", osName)
		}
	} else if osName != runtime.GOOS {
		t.Errorf("osName = %q, want %q", osName, runtime.GOOS)
	}
}

func TestWizardResolveProjectFillsMissingID(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
version = "1.0.0"
entry = "main.flx"
`)

	stdin := strings.Join([]string{root, "", "com.example.wizard-test", ""}, "\n") + "\n"
	w, stdout, _ := newTestWizard(t, stdin)

	cfg, err := w.resolveProject()
	if err != nil {
		t.Fatalf("resolveProject() error = %v; stdout=%q", err, stdout.String())
	}
	if cfg.Project.ID != "com.example.wizard-test" {
		t.Errorf("Project.ID = %q, want com.example.wizard-test", cfg.Project.ID)
	}

	data, readErr := os.ReadFile(filepath.Join(root, "fluxa.toml")) // #nosec G304 -- test fixture path.
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(data), `id = "com.example.wizard-test"`) {
		t.Errorf("fluxa.toml = %q, want id field written", string(data))
	}
}

func TestWizardResolveProjectRetriesInvalidID(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
version = "1.0.0"
entry = "main.flx"
`)

	stdin := strings.Join([]string{root, "", "Not A Valid ID!!", "com.example.retry-ok", ""}, "\n") + "\n"
	w, stdout, _ := newTestWizard(t, stdin)

	cfg, err := w.resolveProject()
	if err != nil {
		t.Fatalf("resolveProject() error = %v; stdout=%q", err, stdout.String())
	}
	if cfg.Project.ID != "com.example.retry-ok" {
		t.Errorf("Project.ID = %q, want com.example.retry-ok", cfg.Project.ID)
	}
	if !strings.Contains(stdout.String(), "reverse-domain identifier") {
		t.Errorf("stdout = %q, want a validation hint after the invalid attempt", stdout.String())
	}
}

func TestWizardResolveProjectCreatesMissingFluxaToml(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.flx"), []byte(`print("ok")`), 0o600); err != nil {
		t.Fatal(err)
	}

	stdin := strings.Join([]string{
		root, "", // directory, "fill in fields?" yes
		"Wizard App", "", // name, write? yes
		"com.example.wizard-app", "", // id, write? yes
		"", "", // version (default 0.1.0), write? yes
		"", "", // entry (default main.flx), write? yes
	}, "\n") + "\n"
	w, stdout, _ := newTestWizard(t, stdin)

	cfg, err := w.resolveProject()
	if err != nil {
		t.Fatalf("resolveProject() error = %v; stdout=%q", err, stdout.String())
	}
	if cfg.Project.Name != "Wizard App" || cfg.Project.ID != "com.example.wizard-app" ||
		cfg.Project.Version != "0.1.0" || cfg.Project.Entry != "main.flx" {
		t.Errorf("loaded project = %+v, want all four fields filled", cfg.Project)
	}
}

func TestWizardResolveProjectAbortsWhenUserDeclines(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
version = "1.0.0"
entry = "main.flx"
`)

	stdin := strings.Join([]string{root, "n", "n"}, "\n") + "\n"
	w, _, _ := newTestWizard(t, stdin)

	if _, err := w.resolveProject(); !errors.Is(err, errWizardAbort) {
		t.Fatalf("resolveProject() error = %v, want errWizardAbort", err)
	}
}

func TestWizardChooseOutputPersistsWhenConfirmed(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
id = "com.example.wizard-test"
version = "1.0.0"
entry = "main.flx"
`)
	cfg, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	w, _, _ := newTestWizard(t, "custom-out\ny\n")
	override, err := w.chooseOutput(cfg)
	if err != nil {
		t.Fatalf("chooseOutput() error = %v", err)
	}
	if override != "custom-out" {
		t.Errorf("override = %q, want custom-out", override)
	}

	data, readErr := os.ReadFile(filepath.Join(root, "fluxa.toml")) // #nosec G304 -- test fixture path.
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(data), `output = "custom-out"`) {
		t.Errorf("fluxa.toml = %q, want output field written", string(data))
	}
}

func TestWizardChooseOutputDeclinesPersist(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
id = "com.example.wizard-test"
version = "1.0.0"
entry = "main.flx"
`)
	cfg, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	original, readErr := os.ReadFile(filepath.Join(root, "fluxa.toml")) // #nosec G304 -- test fixture path.
	if readErr != nil {
		t.Fatal(readErr)
	}

	w, _, _ := newTestWizard(t, "custom-out\nn\n")
	override, err := w.chooseOutput(cfg)
	if err != nil {
		t.Fatalf("chooseOutput() error = %v", err)
	}
	if override != "custom-out" {
		t.Errorf("override = %q, want custom-out (still used for this run)", override)
	}

	data, readErr := os.ReadFile(filepath.Join(root, "fluxa.toml")) // #nosec G304 -- test fixture path.
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != string(original) {
		t.Errorf("fluxa.toml changed = %q, want unchanged %q", string(data), string(original))
	}
}

func TestWizardChooseTargetExplainsNonHostLimit(t *testing.T) {
	t.Parallel()

	hostOS, _ := hostTargetOS()
	nonHostChoice := "2" // windows
	if hostOS == "windows" {
		nonHostChoice = "3" // linux
	}

	w, stdout, _ := newTestWizard(t, nonHostChoice+"\n")
	if err := w.chooseTarget(); err != nil {
		t.Fatalf("chooseTarget() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "can only build and verify") {
		t.Errorf("stdout = %q, want an explanation of the host-only limitation", stdout.String())
	}
}

func TestWizardOptionalSettingsFillsAssetsExcludePersistentExport(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
id = "com.example.wizard-test"
version = "1.0.0"
entry = "main.flx"
`)
	cfg, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	stdin := strings.Join([]string{
		"", "", // build.terminal: accept default, write it
		"assets/**, data/**", "y", // build.assets
		"*.log, *.tmp", "y", // build.exclude
		"save.db, cards/**", "y", // build.persistent
		"cards/**", "y", // build.export (subset of persistent)
		"n", // decline package.include_source
		"",  // skip host icon
	}, "\n") + "\n"
	w, stdout, _ := newTestWizard(t, stdin)

	if err := w.optionalProjectSettings(cfg); err != nil {
		t.Fatalf("optionalProjectSettings() error = %v; stdout=%q", err, stdout.String())
	}

	reloaded, err := project.Load(root)
	if err != nil {
		t.Fatalf("reload after optionalProjectSettings: %v", err)
	}
	if len(reloaded.Build.Assets) != 2 || len(reloaded.Build.Exclude) != 2 ||
		len(reloaded.Build.Persistent) != 2 || len(reloaded.Build.Exported) != 1 {
		t.Fatalf("reloaded build config = %+v, want all four array fields written", reloaded.Build)
	}
	if reloaded.Package.IncludeSource {
		t.Errorf("Package.IncludeSource = true, want false (declined)")
	}
	if cfg.Build.Assets[0] != "assets/**" || cfg.Build.Persistent[1] != "cards/**" {
		t.Errorf("in-memory cfg not updated: %+v", cfg.Build)
	}
}

func TestWizardOptionalSettingsRejectsExportNotInPersistent(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
id = "com.example.wizard-test"
version = "1.0.0"
entry = "main.flx"
`)
	cfg, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	stdin := strings.Join([]string{
		"", "", // build.terminal: accept default, write it
		"", "", // skip assets, exclude
		"save.db", "y", // build.persistent
		"wrong-item",   // invalid export attempt: not in persistent
		"save.db", "y", // valid export attempt
		"n", // decline package.include_source
		"",  // skip host icon
	}, "\n") + "\n"
	w, stdout, _ := newTestWizard(t, stdin)

	if err := w.optionalProjectSettings(cfg); err != nil {
		t.Fatalf("optionalProjectSettings() error = %v; stdout=%q", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"wrong-item" must also appear in build.persistent`) {
		t.Errorf("stdout = %q, want a subset-violation message", stdout.String())
	}

	reloaded, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Build.Exported) != 1 || reloaded.Build.Exported[0] != "save.db" {
		t.Errorf("Build.Exported = %v, want [save.db]", reloaded.Build.Exported)
	}
}

func TestWizardOfferTerminalFieldWritesChosenValue(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
id = "com.example.wizard-test"
version = "1.0.0"
entry = "main.flx"
`)
	cfg, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Build.Terminal {
		t.Fatal("fixture default must start as terminal=true to prove the wizard actually flips it")
	}

	w, stdout, _ := newTestWizard(t, "n\ny\n")
	if err := w.offerTerminalField(cfg); err != nil {
		t.Fatalf("offerTerminalField() error = %v; stdout=%q", err, stdout.String())
	}
	if cfg.Build.Terminal {
		t.Error("cfg.Build.Terminal = true, want false after choosing no")
	}

	reloaded, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Build.Terminal {
		t.Error("reloaded Build.Terminal = true, want false")
	}
}

func TestWizardOptionalSettingsPersistsIncludeSource(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
id = "com.example.wizard-test"
version = "1.0.0"
entry = "main.flx"
`)
	cfg, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	stdin := strings.Join([]string{
		"", "", // build.terminal: accept default, write it
		"", "", "", // skip assets, exclude, persistent (export skipped automatically)
		"", // accept default (yes) for package.include_source
		"", // skip host icon
	}, "\n") + "\n"
	w, stdout, _ := newTestWizard(t, stdin)

	if err := w.optionalProjectSettings(cfg); err != nil {
		t.Fatalf("optionalProjectSettings() error = %v; stdout=%q", err, stdout.String())
	}
	if !cfg.Package.IncludeSource {
		t.Errorf("cfg.Package.IncludeSource = false, want true")
	}

	reloaded, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Package.IncludeSource {
		t.Errorf("reloaded Package.IncludeSource = false, want true")
	}
}

func TestWizardOptionalSettingsSkipsAlreadySetFields(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
id = "com.example.wizard-test"
version = "1.0.0"
entry = "main.flx"

[build]
terminal = true
assets = ["assets/**"]

[package]
include_source = true
`)
	cfg, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(root, "fluxa.toml")) // #nosec G304 -- test fixture path.
	if err != nil {
		t.Fatal(err)
	}

	// No answer is provided for build.terminal, build.assets, or
	// package.include_source: if the wizard tried to prompt for any of
	// them, this stdin would run out and the call would fail with an EOF
	// error.
	stdin := strings.Join([]string{
		"", // build.exclude -> skip
		"", // build.persistent -> skip
		"", // host icon -> skip
	}, "\n") + "\n"
	w, stdout, _ := newTestWizard(t, stdin)

	if err := w.optionalProjectSettings(cfg); err != nil {
		t.Fatalf("optionalProjectSettings() error = %v; stdout=%q", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), "build.terminal is already set in fluxa.toml; skipping.") {
		t.Errorf("stdout = %q, want a terminal skip notice", stdout.String())
	}
	if !strings.Contains(stdout.String(), "build.assets is already set in fluxa.toml; skipping.") {
		t.Errorf("stdout = %q, want an assets skip notice", stdout.String())
	}
	if !strings.Contains(stdout.String(), "package.include_source is already set in fluxa.toml; skipping.") {
		t.Errorf("stdout = %q, want an include_source skip notice", stdout.String())
	}

	current, err := os.ReadFile(filepath.Join(root, "fluxa.toml")) // #nosec G304 -- test fixture path.
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(original) {
		t.Errorf("fluxa.toml changed = %q, want unchanged %q", string(current), string(original))
	}
}

func TestWizardOfferIconFieldWritesValidPath(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
id = "com.example.wizard-test"
version = "1.0.0"
entry = "main.flx"
`)
	if err := os.WriteFile(filepath.Join(root, "app.png"), []byte("fake-png"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	w, _, _ := newTestWizard(t, "does-not-exist.png\napp.png\ny\n")
	w.hostOS, w.hostArch = "linux", "amd64"

	if err := w.offerTargetSettings(cfg); err != nil {
		t.Fatalf("offerTargetSettings() error = %v", err)
	}

	reloaded, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Targets.Linux.Icon != "app.png" {
		t.Errorf("Targets.Linux.Icon = %q, want app.png", reloaded.Targets.Linux.Icon)
	}
}

func TestWizardOfferTargetSettingsMacOSBundleID(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
id = "com.example.wizard-test"
version = "1.0.0"
entry = "main.flx"
`)
	cfg, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	// icon prompt (skip), then bundle ID different from project.id.
	w, _, _ := newTestWizard(t, "\ncom.example.custom-bundle\ny\n")
	w.hostOS, w.hostArch = "macos", "amd64"

	if err := w.offerTargetSettings(cfg); err != nil {
		t.Fatalf("offerTargetSettings() error = %v", err)
	}

	reloaded, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Targets.MacOS.BundleID != "com.example.custom-bundle" {
		t.Errorf("Targets.MacOS.BundleID = %q, want com.example.custom-bundle", reloaded.Targets.MacOS.BundleID)
	}
}

func TestRunInitAbortsOnEOF(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := runInit(strings.NewReader(""), &stdout, &stderr, defaultBuildDependencies())
	if code != 1 {
		t.Fatalf("runInit() code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "input ended unexpectedly") {
		t.Errorf("stderr = %q, want EOF message", stderr.String())
	}
}

func TestRunInitPrintsManualGuideWhenToolchainMissing(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
id = "com.example.wizard-test"
version = "1.0.0"
entry = "main.flx"
`)
	templatePath := filepath.Join(t.TempDir(), "runtime.json")

	deps := defaultBuildDependencies()
	deps.resolve = func(toolchain.ResolveOptions) (toolchain.Candidate, error) {
		return toolchain.Candidate{}, &toolchain.Error{Kind: toolchain.ErrorNotFound, Operation: "locate"}
	}

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
		"",   // download automatically? -> default no
		"",   // path to local fluxa-lang checkout -> skip
		"",   // path to the built binary -> skip
		templatePath,
	}, "\n") + "\n"

	var stdout, stderr bytes.Buffer
	code := runInit(strings.NewReader(stdin), &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("runInit() code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "not available yet") {
		t.Errorf("stdout = %q, want automatic-mode explanation", stdout.String())
	}
	if !strings.Contains(stdout.String(), "git clone https://github.com/RodBarenco/fluxa-lang") {
		t.Errorf("stdout = %q, want clone instructions", stdout.String())
	}
	hostOS, _ := hostTargetOS()
	wantMakeCommand := "make build"
	if hostOS == "windows" {
		wantMakeCommand = "make build-windows-packaged"
	}
	if !strings.Contains(stdout.String(), wantMakeCommand) {
		t.Errorf("stdout = %q, want make instructions %q", stdout.String(), wantMakeCommand)
	}

	data, readErr := os.ReadFile(templatePath) // #nosec G304 -- test fixture path.
	if readErr != nil {
		t.Fatalf("runtime.json template was not written: %v", readErr)
	}
	var metadata runtimepkg.Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatalf("runtime.json template is not valid JSON: %v", err)
	}
	hostOS, hostArch := hostTargetOS()
	if metadata.OS != hostOS || metadata.Arch != hostArch {
		t.Errorf("template OS/Arch = %s/%s, want %s/%s", metadata.OS, metadata.Arch, hostOS, hostArch)
	}
	if !metadata.Packaged || len(metadata.ProgramFormats) != 1 || metadata.ProgramFormats[0] != "fluxa-source" {
		t.Errorf("template = %+v, want packaged fluxa-source defaults", metadata)
	}
	if metadata.BinarySHA256 != "" || metadata.BinaryName != "" {
		t.Errorf("template = %+v, want empty binary fields (no binary path was given)", metadata)
	}
}

func TestRunInitAttemptsRealBuildWhenReady(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
id = "com.example.wizard-test"
version = "1.0.0"
entry = "main.flx"
`)

	runtimePath := filepath.Join(root, "runtime-fixture")
	runtimeBytes := []byte("runtime fixture")
	if runtime.GOOS == "windows" {
		var err error
		runtimePath, err = os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		runtimeBytes, err = os.ReadFile(runtimePath) // #nosec G304 -- current test executable.
		if err != nil {
			t.Fatal(err)
		}
	} else {
		if err := os.WriteFile(runtimePath, runtimeBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(runtimePath, 0o700); err != nil { // #nosec G302 -- executable runtime fixture.
			t.Fatal(err)
		}
	}
	runtimeHash := sha256.Sum256(runtimeBytes)
	hostOS, hostArch := hostTargetOS()

	deps := defaultBuildDependencies()
	deps.resolve = func(toolchain.ResolveOptions) (toolchain.Candidate, error) {
		return toolchain.Candidate{Path: "/opt/fluxa/bin/fluxa", Source: toolchain.SourceExplicit}, nil
	}
	deps.probe = func(context.Context, string, time.Duration) (toolchain.Identity, error) {
		return toolchain.Identity{Protocol: "runtime-info-v1", SHA256: strings.Repeat("a", 64)}, nil
	}
	deps.resolveRuntime = func(string, runtimepkg.Requirement) (runtimepkg.Runtime, error) {
		return runtimepkg.Runtime{
			BinaryPath: runtimePath,
			Metadata: runtimepkg.Metadata{
				OS: hostOS, Arch: hostArch, Terminal: true,
				BinarySHA256: hex.EncodeToString(runtimeHash[:]),
			},
		}, nil
	}
	deps.smokePortable = func(context.Context, portable.Result, time.Duration) error { return nil }
	deps.listRuntimes = func(string) ([]runtimepkg.Runtime, error) {
		return []runtimepkg.Runtime{{}}, nil
	}

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
		"",   // build now? -> default yes
	}, "\n") + "\n"
	var stdout, stderr bytes.Buffer
	code := runInit(strings.NewReader(stdin), &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("runInit() code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Portable application verified") {
		t.Fatalf("stdout = %q, want evidence of a real build", stdout.String())
	}

	targetOutput := filepath.Join(root, "dist", targetDirectoryName(hostOS, hostArch))
	if _, err := os.Stat(targetOutput); err != nil {
		t.Fatalf("build output missing: %v", err)
	}
}
