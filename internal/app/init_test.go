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
	"github.com/RodBarenco/fluxa-builder/internal/toolchainbuild"
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
	// style/spinner are normally set up by run(); a bytes.Buffer is never a
	// real terminal, so this mirrors run()'s own outcome (styling
	// disabled) rather than actually replicating its logic.
	w.style = newStyle(w.stdout)
	w.errStyle = newStyle(w.stderr)
	w.spinner = newSpinner(w.stdout, w.style)
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

func TestWizardChooseTargetHonorsSupportedCrossTarget(t *testing.T) {
	t.Parallel()

	hostOS, _ := hostTargetOS()
	nonHostChoice, wantTarget := "2", "windows-x64" // windows
	if hostOS == "windows" {
		nonHostChoice, wantTarget = "3", "linux-x64" // linux
	}

	w, stdout, _ := newTestWizard(t, nonHostChoice+"\n")
	target, err := w.chooseTarget()
	if err != nil {
		t.Fatalf("chooseTarget() error = %v", err)
	}
	if target != wantTarget {
		t.Fatalf("chooseTarget() target = %q, want %q — windows-x64/linux-x64 are now verified via a container, see docs/adr/0028", target, wantTarget)
	}
	if !strings.Contains(stdout.String(), "network-isolated Docker container") {
		t.Errorf("stdout = %q, want an explanation of container-based verification", stdout.String())
	}
}

// TestWizardChooseTargetHostChoiceIsExplicit locks down the fix for a
// build that contradicted the menu it came from: picking "this machine"
// used to return "", which means "pass no --target" — and that is not
// "build for this machine", it is "defer to fluxa.toml's build.target".
// A project pinned to windows-x64 therefore produced Windows artifacts
// right after the wizard printed "Building for linux-x64 in this run."
func TestWizardChooseTargetHostChoiceIsExplicit(t *testing.T) {
	t.Parallel()

	w, _, _ := newTestWizard(t, "1\n")
	target, err := w.chooseTarget()
	if err != nil {
		t.Fatalf("chooseTarget() error = %v", err)
	}
	if target != "host" {
		t.Fatalf("chooseTarget() target = %q, want %q — an empty override silently defers to fluxa.toml's build.target", target, "host")
	}
}

// TestWizardChooseTargetAcceptsPlatformNames covers the answer everyone
// actually types. An unrecognized answer used to fall through to a host
// build, so typing "windows" instead of "2" produced Linux artifacts
// without a word about it.
func TestWizardChooseTargetAcceptsPlatformNames(t *testing.T) {
	t.Parallel()

	hostOS, _ := hostTargetOS()
	name, wantTarget := "windows", "windows-x64"
	if hostOS == "windows" {
		name, wantTarget = "linux", "linux-x64"
	}

	w, _, _ := newTestWizard(t, name+"\n")
	target, err := w.chooseTarget()
	if err != nil {
		t.Fatalf("chooseTarget() error = %v", err)
	}
	if target != wantTarget {
		t.Fatalf("chooseTarget(%q) target = %q, want %q", name, target, wantTarget)
	}
}

func TestWizardChooseTargetRejectsUnknownChoice(t *testing.T) {
	t.Parallel()

	w, stdout, _ := newTestWizard(t, "wrong-item\n1\n")
	target, err := w.chooseTarget()
	if err != nil {
		t.Fatalf("chooseTarget() error = %v", err)
	}
	if target != "host" {
		t.Fatalf("chooseTarget() target = %q, want %q after re-answering", target, "host")
	}
	if !strings.Contains(stdout.String(), "is not one of the choices above") {
		t.Errorf("stdout = %q, want the invalid answer called out instead of silently becoming a host build", stdout.String())
	}
}

func TestWizardChooseTargetExplainsMacOSHostOnlyLimit(t *testing.T) {
	t.Parallel()

	hostOS, _ := hostTargetOS()
	if hostOS == "macos" {
		t.Skip("this test exercises the non-host macOS branch")
	}

	// macos, then the host: the unbuildable choice must re-ask rather than
	// quietly build for something the user never picked.
	w, stdout, _ := newTestWizard(t, "4\n1\n")
	target, err := w.chooseTarget()
	if err != nil {
		t.Fatalf("chooseTarget() error = %v", err)
	}
	if target != "host" {
		t.Fatalf("chooseTarget() target = %q, want %q — macOS cross-building stays host-only and must be re-asked", target, "host")
	}
	if !strings.Contains(stdout.String(), "can only build and verify macOS on real macOS hardware") {
		t.Errorf("stdout = %q, want an explanation of the macOS host-only limitation", stdout.String())
	}
}

func TestWizardChooseTargetDoesNotSupportAllThreeYet(t *testing.T) {
	t.Parallel()

	w, stdout, _ := newTestWizard(t, "5\n1\n") // all three, then the host
	target, err := w.chooseTarget()
	if err != nil {
		t.Fatalf("chooseTarget() error = %v", err)
	}
	if target != "host" {
		t.Fatalf("chooseTarget() target = %q, want %q — building all three in one run is not supported yet, so it must re-ask", target, "host")
	}
	if !strings.Contains(stdout.String(), "not supported") {
		t.Errorf("stdout = %q, want an honest explanation that this isn't supported yet", stdout.String())
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

	if err := w.optionalProjectSettings(cfg, w.hostOS); err != nil {
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

	if err := w.optionalProjectSettings(cfg, w.hostOS); err != nil {
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
	if err := w.offerTerminalField(cfg, w.hostOS); err != nil {
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

	if err := w.optionalProjectSettings(cfg, w.hostOS); err != nil {
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

	if err := w.optionalProjectSettings(cfg, w.hostOS); err != nil {
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

	if err := w.offerTargetSettings(cfg, "linux"); err != nil {
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

// TestWizardOfferTargetSettingsFollowsChosenTargetNotHost is the
// regression test for the wizard collecting the wrong platform's icon on
// every cross-target run. runBuild reads exactly one icon field, the one
// matching the target it is producing, so a Windows build driven from
// Linux that was asked for targets.linux.icon shipped a .exe with no icon
// at all — and was never asked for the .ico it actually needed.
func TestWizardOfferTargetSettingsFollowsChosenTargetNotHost(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
id = "com.example.wizard-test"
version = "1.0.0"
entry = "main.flx"
`)
	if err := os.WriteFile(filepath.Join(root, "app.ico"), []byte("fake-ico"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	w, stdout, _ := newTestWizard(t, "app.ico\ny\n")
	w.hostOS, w.hostArch = "linux", "amd64"

	if err := w.offerTargetSettings(cfg, "windows"); err != nil {
		t.Fatalf("offerTargetSettings() error = %v", err)
	}

	reloaded, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Targets.Windows.Icon != "app.ico" {
		t.Errorf("Targets.Windows.Icon = %q, want app.ico — the chosen target's icon, not the host's", reloaded.Targets.Windows.Icon)
	}
	if reloaded.Targets.Linux.Icon != "" {
		t.Errorf("Targets.Linux.Icon = %q, want empty — the host's platform is not what this run builds", reloaded.Targets.Linux.Icon)
	}
	if !strings.Contains(stdout.String(), ".ico") {
		t.Errorf("stdout = %q, want the prompt to name the container Windows icons must be in", stdout.String())
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

	if err := w.offerTargetSettings(cfg, "macos"); err != nil {
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

// TestResolveProjectResolvesRelativePathsAgainstHomeDirectory is a
// regression test for two converging real reports: typing a plain
// relative path, or one with a leading "./", was silently resolved
// relative to wherever the fluxa-builder process itself happened to be
// running — never anywhere the user was actually looking — and an
// after-the-fact hint trying to guess the intended absolute path could
// not fully close that expectation gap on its own. The wizard now treats
// the question like a fresh terminal already sitting at $HOME: a bare
// relative answer, a "./"-prefixed one, and a "~/"-prefixed one must all
// resolve to the same real project directory under home.
func TestResolveProjectResolvesRelativePathsAgainstHomeDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the home-relative shell prompt is POSIX-specific")
	}
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	projectDir := filepath.Join(fakeHome, "nave")
	if err := os.Mkdir(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := `[project]
name = "Nave"
id = "com.example.nave"
version = "1.0.0"
entry = "main.flx"
`
	if err := os.WriteFile(filepath.Join(projectDir, "fluxa.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "main.flx"), []byte(`print("ok")`), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, raw := range []string{"nave", "./nave", "~/nave"} {
		t.Run(raw, func(t *testing.T) {
			w, stdout, _ := newTestWizard(t, raw+"\n")
			cfg, err := w.resolveProject()
			if err != nil {
				t.Fatalf("resolveProject(%q) error = %v; stdout=%q", raw, err, stdout.String())
			}
			if cfg.Root != projectDir {
				t.Errorf("resolveProject(%q) root = %q, want %q", raw, cfg.Root, projectDir)
			}
		})
	}
}

// TestResolveProjectReportsMissingDirectoryCleanly proves a nonexistent
// directory (resolved, per the above, against $HOME) is reported with no
// raw stat/errno text leaking through.
func TestResolveProjectReportsMissingDirectoryCleanly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the home-relative shell prompt is POSIX-specific")
	}
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	w, stdout, _ := newTestWizard(t, "does-not-exist\n")
	if _, err := w.resolveProject(); !errors.Is(err, errWizardEOF) {
		t.Fatalf("resolveProject() error = %v, want errWizardEOF once scripted input is exhausted", err)
	}

	output := stdout.String()
	wantPath := filepath.Join(fakeHome, "does-not-exist")
	if !strings.Contains(output, "Directory not found: "+wantPath) {
		t.Errorf("output = %q, want a clean message naming %q", output, wantPath)
	}
	if strings.Contains(output, "no such file or directory") || strings.Contains(output, "stat ") {
		t.Errorf("output = %q, want no raw stat/errno text leaking through", output)
	}
}

// TestRunInitOnlyUsesReadlineForARealTerminal proves the readline-backed
// line editor (real arrow-key/history support — see
// docs "NADA FUNCIONA" report on raw escape bytes leaking into typed
// answers) is wired in only when stdin is an actual *os.File, and stays
// nil — falling back to the existing, already-tested bufio.Reader path
// — for everything else: piped scripts, and every test in this package,
// none of which pass a real terminal. Actual arrow-key editing behavior
// itself is chzyer/readline's own well-established, independently
// tested responsibility, not re-verified here; this test only proves
// this wizard's own decision of *when* to use it.
func TestRunInitOnlyUsesReadlineForARealTerminal(t *testing.T) {
	w := &wizard{
		reader: bufio.NewReader(strings.NewReader("\n")),
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
	}
	if w.rl != nil {
		t.Fatal("a wizard constructed without a *os.File stdin must never have a readline instance")
	}

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devNull.Close() }()
	// /dev/null is a real *os.File but not a terminal (not a char
	// device in the sense isTerminalFile checks for interactively) —
	// runInit's type assertion alone must not be sufficient; run()'s own
	// isTerminalFile gate must also reject it.
	if isTerminalFile(devNull) {
		t.Skip("this environment's /dev/null unexpectedly reports as a terminal")
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
		"",   // choose target -> this machine (default)
		"",   // optional: build.terminal -> accept default
		"",   // optional: write build.terminal -> accept default
		"",   // optional: build.assets -> skip
		"",   // optional: build.exclude -> skip
		"",   // optional: build.persistent -> skip
		"",   // optional: save package.include_source? -> default yes
		"",   // optional: target platform icon -> skip
		"",   // output directory -> keep default
		"",   // path to local fluxa executable -> skip
		"n",  // download and build automatically? -> no, use the manual guide
		"",   // path to local fluxa-lang checkout -> skip
		"",   // path to the built binary -> skip
		templatePath,
	}, "\n") + "\n"

	var stdout, stderr bytes.Buffer
	code := runInit(strings.NewReader(stdin), &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("runInit() code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Here is what to do manually") {
		t.Errorf("stdout = %q, want the manual guide", stdout.String())
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

// TestRunInitHostChoiceOverridesPinnedProjectTarget is the end-to-end
// regression test for a build that produced a platform the wizard had
// just ruled out. Picking "this machine" used to pass no --target at all,
// which leaves runBuild reading fluxa.toml's own build.target — so a
// project pinned to windows-x64 kept building Windows immediately after
// the menu printed "Building for linux-x64 in this run." The menu answer
// has to win over the pinned value, or the menu is decoration.
func TestRunInitHostChoiceOverridesPinnedProjectTarget(t *testing.T) {
	t.Parallel()

	hostOS, hostArch := hostTargetOS()
	pinned := "windows-x64"
	if hostOS == "windows" {
		pinned = "linux-x64"
	}
	root := writeWizardProject(t, `[project]
name = "Wizard Test"
id = "com.example.wizard-test"
version = "1.0.0"
entry = "main.flx"

[build]
target = "`+pinned+`"
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
		return []runtimepkg.Runtime{{Metadata: runtimepkg.Metadata{OS: hostOS, Arch: hostArch}}}, nil
	}

	stdin := strings.Join([]string{
		root, // project directory
		"1",  // choose target -> this machine, despite the pinned build.target
		"",   // optional: build.terminal -> accept default
		"",   // optional: write build.terminal -> accept default
		"",   // optional: build.assets -> skip
		"",   // optional: build.exclude -> skip
		"",   // optional: build.persistent -> skip
		"",   // optional: save package.include_source? -> default yes
		"",   // optional: target platform icon -> skip
		"",   // output directory -> keep default
		"",   // build now? -> default yes
	}, "\n") + "\n"

	var stdout, stderr bytes.Buffer
	if code := runInit(strings.NewReader(stdin), &stdout, &stderr, deps); code != 0 {
		t.Fatalf("runInit() code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	hostTarget := targetDirectoryName(hostOS, hostArch)
	if _, err := os.Stat(filepath.Join(root, "dist", hostTarget)); err != nil {
		t.Fatalf("no %s artifacts were produced: %v — fluxa.toml's build.target=%q overrode the menu answer", hostTarget, err, pinned)
	}
	if _, err := os.Stat(filepath.Join(root, "dist", pinned)); err == nil {
		t.Errorf("built %s, the pinned build.target, even though the menu answer was this machine (%s)", pinned, hostTarget)
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
		return []runtimepkg.Runtime{{Metadata: runtimepkg.Metadata{OS: hostOS, Arch: hostArch}}}, nil
	}

	stdin := strings.Join([]string{
		root, // project directory
		"",   // choose target -> this machine (default)
		"",   // optional: build.terminal -> accept default
		"",   // optional: write build.terminal -> accept default
		"",   // optional: build.assets -> skip
		"",   // optional: build.exclude -> skip
		"",   // optional: build.persistent -> skip
		"",   // optional: save package.include_source? -> default yes
		"",   // optional: target platform icon -> skip
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

// TestRunInitOffersSetupWhenRegisteredRuntimeDoesNotMatchChosenTarget is a
// regression test for a real bug real end-to-end use of this project's
// own docs/adr/0028 work caught: setupOrBuild's "is a runtime already
// registered" check used to accept *any* registered runtime at all,
// regardless of OS/arch. A host with only a Linux runtime registered,
// choosing windows-x64 in the target menu, would then wrongly report
// "a toolchain and at least one registered runtime were found" and
// attempt the real build immediately — which always failed later, deep
// inside runtime resolution, with a confusing
// "no verified runtime matches windows/amd64" error instead of ever
// offering automatic acquisition for the actually-missing target.
func TestRunInitOffersSetupWhenRegisteredRuntimeDoesNotMatchChosenTarget(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
id = "com.example.wizard-test"
version = "1.0.0"
entry = "main.flx"
`)
	hostOS, hostArch := hostTargetOS()
	nonHostChoice, wantTargetOS := "2", "windows" // windows
	if hostOS == "windows" {
		nonHostChoice, wantTargetOS = "3", "linux" // linux
	}

	deps := defaultBuildDependencies()
	deps.resolve = func(toolchain.ResolveOptions) (toolchain.Candidate, error) {
		return toolchain.Candidate{Path: "/opt/fluxa/bin/fluxa", Source: toolchain.SourceExplicit}, nil
	}
	deps.probe = func(context.Context, string, time.Duration) (toolchain.Identity, error) {
		return toolchain.Identity{Protocol: "runtime-info-v1", SHA256: strings.Repeat("a", 64)}, nil
	}
	// Only a runtime matching the *host*, never the chosen target, is
	// registered — exactly the real-world state that triggered the bug.
	deps.listRuntimes = func(string) ([]runtimepkg.Runtime, error) {
		return []runtimepkg.Runtime{{Metadata: runtimepkg.Metadata{OS: hostOS, Arch: hostArch}}}, nil
	}
	deps.resolveRuntime = func(_ string, requirement runtimepkg.Requirement) (runtimepkg.Runtime, error) {
		if requirement.OS == hostOS && requirement.Arch == hostArch {
			return runtimepkg.Runtime{}, nil
		}
		return runtimepkg.Runtime{}, errors.New("no verified runtime matches " + requirement.OS + "/" + requirement.Arch)
	}

	templatePath := filepath.Join(t.TempDir(), "runtime.json")
	stdin := strings.Join([]string{
		root,          // project directory
		nonHostChoice, // choose target -> the non-host one
		"",            // optional: build.terminal -> accept default
		"",            // optional: write build.terminal -> accept default
		"",            // optional: build.assets -> skip
		"",            // optional: build.exclude -> skip
		"",            // optional: build.persistent -> skip
		"",            // optional: save package.include_source? -> default yes
		"",            // optional: target platform icon -> skip
		"",            // output directory -> keep default
		"n",           // download and build automatically? -> no, use the manual guide
		"",            // path to local fluxa-lang checkout -> skip
		"",            // path to the built binary -> skip
		templatePath,
	}, "\n") + "\n"

	var stdout, stderr bytes.Buffer
	code := runInit(strings.NewReader(stdin), &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("runInit() code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "development/source-exposed mode?") {
		t.Fatalf("stdout = %q, wrongly attempted a real build with a runtime that doesn't match the chosen target", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Setup is not complete yet") {
		t.Errorf("stdout = %q, want the setup/manual-guide entry point instead", stdout.String())
	}
	data, err := os.ReadFile(templatePath) // #nosec G304 -- test-controlled path.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"os": "`+wantTargetOS+`"`) {
		t.Errorf("runtime.json template = %s, want os %q (the chosen target, not the host)", data, wantTargetOS)
	}
}

// TestRunInitCrossAcquireRegistersHostToolchainIdentity locks down the
// defect that made the whole Windows flow unusable from a Linux host.
//
// A cross-target acquisition produces two Windows PEs, neither of which
// this machine can execute; the identity autoAcquire records for the
// registered runtime must therefore come from the host-native compiler
// Acquire builds alongside them, because that is the binary `build` will
// probe and record in the package manifest, and internal/runtime demands
// the two match exactly. Recording the cross-compiled fluxa-toolchain.exe
// instead — the previous behavior — registered a runtime that could never
// be selected, and every following build died with "no verified runtime
// matches windows/amd64".
//
// The fake probe below refuses any path but the host toolchain, standing
// in for the real "exec format error" a Linux host gives a PE, so a
// regression fails loudly here instead of only in the opt-in Docker test.
func TestRunInitCrossAcquireRegistersHostToolchainIdentity(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the cross-acquisition path under test builds a Linux host toolchain")
	}

	builderHome := t.TempDir()
	t.Setenv("FLUXA_BUILDER_HOME", builderHome)
	t.Setenv("XDG_DATA_HOME", filepath.Join(builderHome, "data"))

	root := writeWizardProject(t, `[project]
name = "Cross Acquire Test"
id = "com.example.cross-acquire-test"
version = "1.0.0"
entry = "main.flx"
`)

	outputDir := t.TempDir()
	hostToolchain := filepath.Join(outputDir, "fluxa-host-toolchain")
	crossToolchain := filepath.Join(outputDir, "fluxa-toolchain.exe")
	runtimeBinary := filepath.Join(outputDir, "fluxa-runtime.exe")
	for _, path := range []string{hostToolchain, crossToolchain, runtimeBinary} {
		if err := os.WriteFile(path, []byte("fake "+filepath.Base(path)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const hostSHA = "1111111111111111111111111111111111111111111111111111111111111111"

	deps := defaultBuildDependencies()
	deps.acquire = func(context.Context, toolchainbuild.Request, toolchainbuild.Confirmer) (toolchainbuild.Result, error) {
		return toolchainbuild.Result{
			ToolchainPath:     crossToolchain,
			RuntimePath:       runtimeBinary,
			HostToolchainPath: hostToolchain,
		}, nil
	}
	deps.resolve = func(options toolchain.ResolveOptions) (toolchain.Candidate, error) {
		if options.ConfigPath == "" {
			return toolchain.Candidate{}, errors.New("no fluxa toolchain found")
		}
		return toolchain.Candidate{Path: options.ConfigPath, Source: toolchain.SourceConfig}, nil
	}
	deps.probe = func(_ context.Context, path string, _ time.Duration) (toolchain.Identity, error) {
		if path != hostToolchain {
			return toolchain.Identity{}, errors.New("exec format error: " + path)
		}
		return toolchain.Identity{Protocol: "runtime-info-v1", SHA256: hostSHA}, nil
	}

	stdin := strings.Join([]string{
		root, // project directory
		"2",  // choose target -> windows-x64
		"",   // optional: build.terminal -> accept default
		"",   // optional: write build.terminal -> accept default
		"",   // optional: build.assets -> skip
		"",   // optional: build.exclude -> skip
		"",   // optional: build.persistent -> skip
		"",   // optional: save package.include_source? -> default yes
		"",   // optional: windows icon -> skip
		"",   // output directory -> keep default
		"y",  // download and build automatically? -> yes
		"",   // save toolchain path to fluxa.toml? -> default yes
		"n",  // build now in development/source-exposed mode? -> no
	}, "\n") + "\n"

	var stdout, stderr bytes.Buffer
	code := runInit(strings.NewReader(stdin), &stdout, &stderr, deps)
	if !strings.Contains(stdout.String(), "Built and registered a Fluxa toolchain and runtime automatically") {
		t.Fatalf("acquisition did not complete; code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	registered, err := runtimepkg.List(filepath.Join(builderHome, "runtimes"))
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 1 {
		t.Fatalf("registered %d runtimes, want exactly 1", len(registered))
	}
	metadata := registered[0].Metadata
	if metadata.OS != "windows" {
		t.Errorf("registered runtime os = %q, want windows", metadata.OS)
	}
	if metadata.ToolchainSHA256 != hostSHA {
		t.Errorf("registered toolchain_sha256 = %q, want the host toolchain's %q — a build probing the local compiler can never match anything else",
			metadata.ToolchainSHA256, hostSHA)
	}

	updatedConfig, err := os.ReadFile(filepath.Join(root, "fluxa.toml")) // #nosec G304 -- test-owned fixture path.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updatedConfig), hostToolchain) {
		t.Errorf("fluxa.toml = %q, want the host toolchain persisted as [toolchain] path", string(updatedConfig))
	}
}

// TestRunInitOffersSetupWhenRegisteredRuntimeIsIncompatible is the
// regression test for the failure that ended a real Windows run: a
// windows-x64 runtime *was* registered, so the wizard's OS/arch-only
// pre-check announced that everything was ready, the user confirmed the
// build — and it died on the resolver with "no verified runtime matches
// windows/amd64 format fluxa-source", because that runtime had been built
// against a different project's fluxa.libs. Whether a runtime is usable is
// exactly what runtime.Resolve decides, so the wizard has to ask it, and a
// mismatch has to route into acquisition instead of into a doomed build.
func TestRunInitOffersSetupWhenRegisteredRuntimeIsIncompatible(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
id = "com.example.wizard-test"
version = "1.0.0"
entry = "main.flx"
`)
	hostOS, hostArch := hostTargetOS()

	deps := defaultBuildDependencies()
	deps.resolve = func(toolchain.ResolveOptions) (toolchain.Candidate, error) {
		return toolchain.Candidate{Path: "/opt/fluxa/bin/fluxa", Source: toolchain.SourceExplicit}, nil
	}
	deps.probe = func(context.Context, string, time.Duration) (toolchain.Identity, error) {
		return toolchain.Identity{Protocol: "runtime-info-v1", SHA256: strings.Repeat("a", 64)}, nil
	}
	// A runtime for the right platform is registered — it just belongs to
	// another project, which is precisely what the old check could not see.
	deps.listRuntimes = func(string) ([]runtimepkg.Runtime, error) {
		return []runtimepkg.Runtime{{Metadata: runtimepkg.Metadata{
			OS: hostOS, Arch: hostArch, LibrariesSHA256: strings.Repeat("b", 64),
		}}}, nil
	}
	deps.resolveRuntime = func(string, runtimepkg.Requirement) (runtimepkg.Runtime, error) {
		return runtimepkg.Runtime{}, errors.New("no verified runtime matches " + hostOS + "/" + hostArch + " format fluxa-source")
	}

	templatePath := filepath.Join(t.TempDir(), "runtime.json")
	stdin := strings.Join([]string{
		root, // project directory
		"",   // choose target -> this machine
		"",   // optional: build.terminal -> accept default
		"",   // optional: write build.terminal -> accept default
		"",   // optional: build.assets -> skip
		"",   // optional: build.exclude -> skip
		"",   // optional: build.persistent -> skip
		"",   // optional: save package.include_source? -> default yes
		"",   // optional: target platform icon -> skip
		"",   // output directory -> keep default
		"n",  // download and build automatically? -> no, use the manual guide
		"",   // path to local fluxa-lang checkout -> skip
		"",   // path to the built binary -> skip
		templatePath,
	}, "\n") + "\n"

	var stdout, stderr bytes.Buffer
	if code := runInit(strings.NewReader(stdin), &stdout, &stderr, deps); code != 0 {
		t.Fatalf("runInit() code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "development/source-exposed mode?") {
		t.Fatalf("stdout = %q, wrongly offered a build against a runtime the resolver rejects", stdout.String())
	}
	if !strings.Contains(stdout.String(), "does not match this") {
		t.Errorf("stdout = %q, want the incompatible-runtime case explained rather than reported as nothing being registered", stdout.String())
	}
}

// TestRegisterAcquiredRuntimeReplacesOccupiedSlot covers the wall waiting
// right after a successful rebuild. Registry slots are keyed on the Fluxa
// version a toolchain reports, fluxa-lang reports none, so every runtime
// built today lands in the same "unreported" slot for a target — and Add
// refuses to write into an occupied one. Without replacing the occupant,
// the whole acquisition produces a runtime that can never be registered.
func TestRegisterAcquiredRuntimeReplacesOccupiedSlot(t *testing.T) {
	t.Parallel()

	registryRoot := filepath.Join(t.TempDir(), "runtimes")
	binary := filepath.Join(t.TempDir(), "fluxa-runtime")
	if err := os.WriteFile(binary, []byte("fresh runtime"), 0o700); err != nil { // #nosec G302 -- executable runtime fixture.
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("fresh runtime"))

	occupantBinary := filepath.Join(t.TempDir(), "fluxa-runtime")
	if err := os.WriteFile(occupantBinary, []byte("stale runtime"), 0o700); err != nil { // #nosec G302 -- executable runtime fixture.
		t.Fatal(err)
	}
	occupantSum := sha256.Sum256([]byte("stale runtime"))

	metadata := func(binarySHA, librariesSHA string) runtimepkg.Metadata {
		return runtimepkg.Metadata{
			FormatVersion:        runtimepkg.CurrentMetadataVersion,
			FluxaVersion:         "unreported",
			ToolchainSHA256:      strings.Repeat("a", 64),
			PackageFormatVersion: 1,
			ProgramFormats:       []string{"fluxa-source"},
			Packaged:             true,
			OS:                   "linux",
			Arch:                 "amd64",
			Terminal:             false,
			LibrariesSHA256:      librariesSHA,
			BinaryName:           "fluxa-runtime",
			BinarySHA256:         binarySHA,
		}
	}
	// The occupant: same slot, different project (different fluxa.libs).
	if _, err := runtimepkg.Add(registryRoot, occupantBinary, metadata(hex.EncodeToString(occupantSum[:]), strings.Repeat("b", 64))); err != nil {
		t.Fatal(err)
	}

	w, stdout, _ := newTestWizard(t, "y\n") // confirm the replacement
	fresh := metadata(hex.EncodeToString(sum[:]), strings.Repeat("c", 64))
	if err := w.registerAcquiredRuntime(registryRoot, binary, fresh); err != nil {
		t.Fatalf("registerAcquiredRuntime() error = %v; stdout=%q", err, stdout.String())
	}

	registered, err := runtimepkg.List(registryRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 1 {
		t.Fatalf("registered %d runtimes, want exactly 1 — the stale one must be gone", len(registered))
	}
	if registered[0].Metadata.LibrariesSHA256 != strings.Repeat("c", 64) {
		t.Errorf("registered libraries_sha256 = %q, want this project's %q", registered[0].Metadata.LibrariesSHA256, strings.Repeat("c", 64))
	}
}

// TestRegisterAcquiredRuntimeKeepsSlotWhenDeclined: replacing is a
// deletion, so declining must leave the registry exactly as it was.
func TestRegisterAcquiredRuntimeKeepsSlotWhenDeclined(t *testing.T) {
	t.Parallel()

	registryRoot := filepath.Join(t.TempDir(), "runtimes")
	occupantBinary := filepath.Join(t.TempDir(), "fluxa-runtime")
	if err := os.WriteFile(occupantBinary, []byte("stale runtime"), 0o700); err != nil { // #nosec G302 -- executable runtime fixture.
		t.Fatal(err)
	}
	occupantSum := sha256.Sum256([]byte("stale runtime"))
	occupant := runtimepkg.Metadata{
		FormatVersion:        runtimepkg.CurrentMetadataVersion,
		FluxaVersion:         "unreported",
		ToolchainSHA256:      strings.Repeat("a", 64),
		PackageFormatVersion: 1,
		ProgramFormats:       []string{"fluxa-source"},
		Packaged:             true,
		OS:                   "linux",
		Arch:                 "amd64",
		LibrariesSHA256:      strings.Repeat("b", 64),
		BinaryName:           "fluxa-runtime",
		BinarySHA256:         hex.EncodeToString(occupantSum[:]),
	}
	if _, err := runtimepkg.Add(registryRoot, occupantBinary, occupant); err != nil {
		t.Fatal(err)
	}

	fresh := occupant
	fresh.LibrariesSHA256 = strings.Repeat("c", 64)
	w, _, _ := newTestWizard(t, "n\n")
	if err := w.registerAcquiredRuntime(registryRoot, occupantBinary, fresh); err == nil {
		t.Fatal("registerAcquiredRuntime() error = nil, want a failure reporting that nothing was registered")
	}

	registered, err := runtimepkg.List(registryRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 1 || registered[0].Metadata.LibrariesSHA256 != strings.Repeat("b", 64) {
		t.Errorf("registry = %+v, want the declined replacement to have changed nothing", registered)
	}
}

// TestResolveProjectFileInputAcceptsEveryReasonableForm covers the icon
// prompt's real failure: a pasted absolute path was joined onto the
// project root, producing project_root + absolute_path — a directory that
// cannot exist — and reported as "not found" for a file sitting right
// there.
func TestResolveProjectFileInputAcceptsEveryReasonableForm(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o750); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "assets", "icone.ico")
	if err := os.WriteFile(want, []byte("fake-ico"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, raw := range []string{
		"assets/icone.ico",
		"./assets/icone.ico",
		want,
		"  " + want + "  ",
	} {
		relative, absolute, err := resolveProjectFileInput(root, raw)
		if err != nil {
			t.Fatalf("resolveProjectFileInput(%q) error = %v", raw, err)
		}
		if relative != "assets/icone.ico" {
			t.Errorf("resolveProjectFileInput(%q) relative = %q, want assets/icone.ico", raw, relative)
		}
		if absolute != want {
			t.Errorf("resolveProjectFileInput(%q) absolute = %q, want %q", raw, absolute, want)
		}
		if _, statErr := os.Stat(absolute); statErr != nil {
			t.Errorf("resolveProjectFileInput(%q) absolute path does not exist: %v", raw, statErr)
		}
	}
}

// TestResolveProjectFileInputRejectsPathsOutsideProject: project.Load
// enforces the same rule on every icon field, so accepting one here would
// only move the failure to the next load.
func TestResolveProjectFileInputRejectsPathsOutsideProject(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "icone.ico")
	for _, raw := range []string{outside, "../icone.ico"} {
		if _, _, err := resolveProjectFileInput(root, raw); err == nil {
			t.Errorf("resolveProjectFileInput(%q) error = nil, want a rejection naming the project directory", raw)
		}
	}
}

// TestWizardOfferIconFieldAcceptsAbsolutePath is the end-to-end version of
// the reported failure: an absolute path to a real icon inside the project
// must be accepted and stored in its project-relative form.
func TestWizardOfferIconFieldAcceptsAbsolutePath(t *testing.T) {
	t.Parallel()

	root := writeWizardProject(t, `[project]
name = "Wizard Test"
id = "com.example.wizard-test"
version = "1.0.0"
entry = "main.flx"
`)
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "icone.ico"), []byte("fake-ico"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	w, stdout, _ := newTestWizard(t, filepath.Join(root, "assets", "icone.ico")+"\ny\n")
	w.hostOS, w.hostArch = "linux", "amd64"
	if err := w.offerTargetSettings(cfg, "windows"); err != nil {
		t.Fatalf("offerTargetSettings() error = %v; stdout=%q", err, stdout.String())
	}

	reloaded, err := project.Load(root)
	if err != nil {
		t.Fatalf("reload after writing the icon: %v", err)
	}
	if reloaded.Targets.Windows.Icon != "assets/icone.ico" {
		t.Errorf("Targets.Windows.Icon = %q, want assets/icone.ico stored from the absolute answer", reloaded.Targets.Windows.Icon)
	}
}
