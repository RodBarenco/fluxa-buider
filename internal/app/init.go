package app

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/project"
	runtimepkg "github.com/RodBarenco/fluxa-builder/internal/runtime"
	"github.com/RodBarenco/fluxa-builder/internal/toolchain"
)

// maxWizardAttempts bounds every interactive retry loop below. In real
// interactive use a human simply keeps typing; the bound only matters as a
// defensive backstop and as what makes scripted-stdin tests deterministic.
const maxWizardAttempts = 6

var (
	// errWizardEOF means stdin ended before the wizard had what it needed.
	errWizardEOF = errors.New("input ended unexpectedly")
	// errWizardAbort means the user explicitly chose to stop.
	errWizardAbort = errors.New("aborted")
)

// runInit drives the interactive `fluxa-builder init` wizard. It is a thin
// guidance layer in front of the existing, already-tested build pipeline:
// once a toolchain and a registered runtime are found it delegates straight
// into runBuild rather than re-implementing any build logic.
func runInit(stdin io.Reader, stdout, stderr io.Writer, deps buildDependencies) int {
	w := &wizard{
		reader: bufio.NewReader(stdin),
		stdout: stdout,
		stderr: stderr,
		deps:   deps,
	}
	return w.run()
}

type wizard struct {
	reader           *bufio.Reader
	stdout           io.Writer
	stderr           io.Writer
	deps             buildDependencies
	hostOS, hostArch string
}

func (w *wizard) run() int {
	w.hostOS, w.hostArch = hostTargetOS()
	if _, err := fmt.Fprintf(w.stdout, "Fluxa Builder init\nDetected host: %s\n\n", targetDirectoryName(w.hostOS, w.hostArch)); err != nil {
		return w.fail(err)
	}

	cfg, err := w.resolveProject()
	if err != nil {
		return w.fail(err)
	}

	if err := w.optionalProjectSettings(cfg); err != nil {
		return w.fail(err)
	}

	if err := w.chooseTarget(); err != nil {
		return w.fail(err)
	}

	outputOverride, err := w.chooseOutput(cfg)
	if err != nil {
		return w.fail(err)
	}

	return w.setupOrBuild(cfg, outputOverride)
}

func (w *wizard) fail(err error) int {
	if errors.Is(err, errWizardAbort) {
		_, _ = fmt.Fprintln(w.stdout, "Aborted.")
		return 1
	}
	if errors.Is(err, errWizardEOF) {
		_, _ = fmt.Fprintln(w.stderr, "error: input ended unexpectedly")
		return 1
	}
	_, _ = fmt.Fprintf(w.stderr, "error: %v\n", err)
	return 1
}

// askLine writes prompt and reads one line. It returns errWizardEOF only
// when stdin ended with nothing usable on the current line; a final line
// without a trailing newline is still returned normally.
func (w *wizard) askLine(prompt string) (string, error) {
	if _, err := fmt.Fprint(w.stdout, prompt); err != nil {
		return "", err
	}
	line, err := w.reader.ReadString('\n')
	line = strings.TrimRight(line, "\r\n")
	if err != nil {
		if errors.Is(err, io.EOF) {
			if line != "" {
				return line, nil
			}
			return "", errWizardEOF
		}
		return "", err
	}
	return line, nil
}

func (w *wizard) askWithDefault(prompt, def string) (string, error) {
	label := prompt
	if def != "" {
		label = fmt.Sprintf("%s [%s]", prompt, def)
	}
	value, err := w.askLine(label + ": ")
	if err != nil {
		return "", err
	}
	if value == "" {
		return def, nil
	}
	return value, nil
}

func (w *wizard) askYesNo(prompt string, def bool) (bool, error) {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	value, err := w.askLine(fmt.Sprintf("%s [%s]: ", prompt, hint))
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return def, nil
	}
}

// hostTargetOS reports the Builder target names for the running machine,
// mirroring the darwin->macos mapping already used inline by
// resolveManifestTarget and hostCanExecuteTarget.
func hostTargetOS() (osName, arch string) {
	osName = runtime.GOOS
	if osName == "darwin" {
		osName = "macos"
	}
	return osName, runtime.GOARCH
}

// resolveProject asks for a project directory until project.Load succeeds,
// offering to fill in missing or invalid required [project] fields along
// the way. Fixing a field retries project.Load against the same directory;
// only an explicit "try a different directory" re-asks for the path.
func (w *wizard) resolveProject() (*project.Config, error) {
	for attempt := 0; attempt < maxWizardAttempts; attempt++ {
		raw, err := w.askWithDefault("Project directory (full path)", ".")
		if err != nil {
			return nil, err
		}
		abs, absErr := filepath.Abs(raw)
		if absErr != nil {
			if _, err := fmt.Fprintf(w.stdout, "Could not resolve %q: %v\n\n", raw, absErr); err != nil {
				return nil, err
			}
			continue
		}
		if _, err := fmt.Fprintf(w.stdout, "Using project directory: %s\n", abs); err != nil {
			return nil, err
		}

		cfg, err := w.loadProjectWithFixes(abs)
		if err != nil {
			if errors.Is(err, errTryDifferentDirectory) {
				continue
			}
			return nil, err
		}
		return cfg, nil
	}
	return nil, fmt.Errorf("gave up locating a valid project after %d attempts", maxWizardAttempts)
}

// errTryDifferentDirectory signals resolveProject to re-prompt for a
// directory rather than retrying project.Load on the same path.
var errTryDifferentDirectory = errors.New("try a different project directory")

func (w *wizard) loadProjectWithFixes(abs string) (*project.Config, error) {
	for attempt := 0; attempt < maxWizardAttempts; attempt++ {
		cfg, loadErr := project.Load(abs)
		if loadErr == nil {
			if _, err := fmt.Fprintf(w.stdout,
				"\nProject loaded\n  Name:     %s\n  ID:       %s\n  Version:  %s\n  Entry:    %s\n  Target:   %s\n  Terminal: %t\n\n",
				cfg.Project.Name, cfg.Project.ID, cfg.Project.Version, cfg.Project.Entry, cfg.Build.Target, cfg.Build.Terminal,
			); err != nil {
				return nil, err
			}
			return cfg, nil
		}

		var perr *project.Error
		if errors.As(loadErr, &perr) && canOfferProjectFix(perr) {
			fixed, fixErr := w.offerProjectFieldFix(abs, perr)
			if fixErr != nil {
				return nil, fixErr
			}
			if fixed {
				continue
			}
		}

		if _, err := fmt.Fprintf(w.stderr, "error: failed to load project\ncaused by: %v\n\n", loadErr); err != nil {
			return nil, err
		}
		retry, err := w.askYesNo("Try a different project directory?", true)
		if err != nil {
			return nil, err
		}
		if !retry {
			return nil, errWizardAbort
		}
		return nil, errTryDifferentDirectory
	}
	return nil, fmt.Errorf("gave up fixing project configuration after %d attempts", maxWizardAttempts)
}

func canOfferProjectFix(perr *project.Error) bool {
	if perr.Kind == project.ErrorNotFound && perr.Operation == "load" {
		return true
	}
	return perr.Kind == project.ErrorValidation && strings.HasPrefix(perr.Field, "project.")
}

// offerProjectFieldFix asks for the value(s) the loader is missing and, on
// confirmation, writes them with project.EnsureStringField. It reports
// fixed=true only when at least one field was written, so the caller knows
// whether retrying project.Load is worthwhile.
func (w *wizard) offerProjectFieldFix(root string, perr *project.Error) (bool, error) {
	fields := []string{perr.Field}
	if perr.Kind == project.ErrorNotFound {
		if _, err := fmt.Fprintln(w.stdout, "No fluxa.toml was found in that directory."); err != nil {
			return false, err
		}
		fields = []string{"project.name", "project.id", "project.version", "project.entry"}
	}

	proceed, err := w.askYesNo("Fill in the required Fluxa Builder project fields now?", true)
	if err != nil {
		return false, err
	}
	if !proceed {
		return false, nil
	}

	for _, field := range fields {
		value, valueErr := w.askProjectFieldValue(root, field)
		if valueErr != nil {
			return false, valueErr
		}
		key := strings.TrimPrefix(field, "project.")
		if _, err := fmt.Fprintf(w.stdout, "Will add to fluxa.toml: %s = %q\n", key, value); err != nil {
			return false, err
		}
		write, err := w.askYesNo("Write this line to fluxa.toml?", true)
		if err != nil {
			return false, err
		}
		if !write {
			if _, err := fmt.Fprintln(w.stdout, "Skipped — edit fluxa.toml yourself, then re-run fluxa-builder init."); err != nil {
				return false, err
			}
			return false, nil
		}
		if _, err := project.EnsureStringField(root, "project", key, value); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (w *wizard) askProjectFieldValue(root, field string) (string, error) {
	for attempt := 0; attempt < maxWizardAttempts; attempt++ {
		switch field {
		case "project.name":
			value, err := w.askLine("Project display name: ")
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(value) != "" {
				return value, nil
			}
			if _, err := fmt.Fprintln(w.stdout, "Name must not be empty."); err != nil {
				return "", err
			}
		case "project.id":
			value, err := w.askLine("Reverse-domain project ID (e.g. com.example.myapp): ")
			if err != nil {
				return "", err
			}
			if project.ValidProjectID(value) {
				return value, nil
			}
			if _, err := fmt.Fprintln(w.stdout, "Must be a lowercase reverse-domain identifier, e.g. com.example.myapp."); err != nil {
				return "", err
			}
		case "project.version":
			value, err := w.askWithDefault("Project version (semantic version)", "0.1.0")
			if err != nil {
				return "", err
			}
			if project.ValidSemVer(value) {
				return value, nil
			}
			if _, err := fmt.Fprintln(w.stdout, "Must be a semantic version, e.g. 0.1.0."); err != nil {
				return "", err
			}
		case "project.entry":
			def := ""
			if info, statErr := os.Stat(filepath.Join(root, "main.flx")); statErr == nil && info.Mode().IsRegular() {
				def = "main.flx"
			}
			value, err := w.askWithDefault("Entry file (relative to the project)", def)
			if err != nil {
				return "", err
			}
			if value == "" {
				if _, err := fmt.Fprintln(w.stdout, "Entry file is required."); err != nil {
					return "", err
				}
				continue
			}
			if info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(value))); statErr == nil && info.Mode().IsRegular() {
				return value, nil
			}
			if _, err := fmt.Fprintf(w.stdout, "%s was not found in %s.\n", value, root); err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("unsupported field %q", field)
		}
	}
	return "", fmt.Errorf("gave up collecting %s after %d attempts", field, maxWizardAttempts)
}

// chooseTarget is informational/expectation-setting only: this machine can
// only build and smoke-test its own OS/architecture (see
// hostCanExecuteTarget), so there is no --target flag to plug a different
// answer into yet.
func (w *wizard) chooseTarget() error {
	menu := fmt.Sprintf(
		"Select the target platform to build now:\n"+
			"  1) %s (this machine) [default]\n"+
			"  2) windows-x64\n"+
			"  3) linux-x64\n"+
			"  4) macos\n"+
			"  5) all three\n",
		targetDirectoryName(w.hostOS, w.hostArch),
	)
	if _, err := fmt.Fprint(w.stdout, menu); err != nil {
		return err
	}
	choice, err := w.askWithDefault("Choice", "1")
	if err != nil {
		return err
	}

	chosenOS := w.hostOS
	label := targetDirectoryName(w.hostOS, w.hostArch)
	switch strings.TrimSpace(choice) {
	case "2":
		chosenOS, label = "windows", "windows-x64"
	case "3":
		chosenOS, label = "linux", "linux-x64"
	case "4":
		chosenOS, label = "macos", "macos"
	case "5":
		chosenOS, label = "all", "all three platforms"
	}

	if chosenOS == w.hostOS {
		_, err := fmt.Fprintf(w.stdout, "Building for %s in this run.\n\n", targetDirectoryName(w.hostOS, w.hostArch))
		return err
	}
	_, err = fmt.Fprintf(w.stdout,
		"This machine can only build and verify %s: the smoke test that must\n"+
			"pass before anything is published runs the produced application,\n"+
			"which only works for the host's own OS/architecture. Run fluxa-builder\n"+
			"init (or build) again on a matching machine or CI runner to produce\n"+
			"%s. Continuing with %s for this run.\n\n",
		targetDirectoryName(w.hostOS, w.hostArch), label, targetDirectoryName(w.hostOS, w.hostArch),
	)
	return err
}

// chooseOutput returns a non-empty override only when the user picked
// something other than the project's current default.
func (w *wizard) chooseOutput(cfg *project.Config) (string, error) {
	for attempt := 0; attempt < maxWizardAttempts; attempt++ {
		value, err := w.askWithDefault("Output directory (relative to the project)", cfg.Build.Output)
		if err != nil {
			return "", err
		}
		if value == cfg.Build.Output {
			return "", nil
		}
		if setErr := cfg.SetOutput(value); setErr != nil {
			if _, err := fmt.Fprintf(w.stdout, "%v\n", setErr); err != nil {
				return "", err
			}
			continue
		}
		if _, err := fmt.Fprintf(w.stdout, "Output directory set to: %s\n", cfg.OutputPath); err != nil {
			return "", err
		}
		persist, err := w.askYesNo(fmt.Sprintf("Save %q as this project's default output directory in fluxa.toml?", value), false)
		if err != nil {
			return "", err
		}
		if persist {
			changed, ensureErr := project.EnsureStringField(cfg.Root, "build", "output", value)
			if ensureErr != nil {
				return "", ensureErr
			}
			message := "Saved."
			if !changed {
				message = "build.output is already set in fluxa.toml; leaving it unchanged."
			}
			if _, err := fmt.Fprintln(w.stdout, message); err != nil {
				return "", err
			}
		}
		return value, nil
	}
	return "", fmt.Errorf("gave up choosing an output directory after %d attempts", maxWizardAttempts)
}

// optionalProjectSettings offers to fill in the other Builder-specific
// fluxa.toml fields that are not required to load a project but are
// commonly needed for a real release: assets, exclude, persistent, export,
// package.include_source, and the host platform's icon/bundle metadata.
// Every field is skippable (blank input) and, like the required-field fix
// flow, every write is previewed and confirmed first. A field already
// present in fluxa.toml is never re-asked or overwritten.
func (w *wizard) optionalProjectSettings(cfg *project.Config) error {
	if _, err := fmt.Fprintln(w.stdout, "Optional project settings (leave any prompt blank to skip):"); err != nil {
		return err
	}

	if err := w.offerTerminalField(cfg); err != nil {
		return err
	}

	assets, err := w.offerArrayField(cfg.Root, "build", "assets",
		"Asset patterns to include, comma-separated (e.g. assets/**, data/**): ")
	if err != nil {
		return err
	}
	if len(assets) > 0 {
		cfg.Build.Assets = assets
	}

	exclude, err := w.offerArrayField(cfg.Root, "build", "exclude",
		"Patterns to exclude, comma-separated (e.g. *.log, *.tmp): ")
	if err != nil {
		return err
	}
	if len(exclude) > 0 {
		cfg.Build.Exclude = exclude
	}

	persistent, err := w.offerArrayField(cfg.Root, "build", "persistent",
		"Files/folders that must survive between runs, comma-separated (e.g. save.db, cards/**): ")
	if err != nil {
		return err
	}
	if len(persistent) > 0 {
		cfg.Build.Persistent = persistent
	}

	if len(cfg.Build.Persistent) > 0 {
		if err := w.offerExportField(cfg); err != nil {
			return err
		}
	}

	if err := w.offerIncludeSourceField(cfg); err != nil {
		return err
	}
	if err := w.offerTargetSettings(cfg); err != nil {
		return err
	}

	_, err = fmt.Fprintln(w.stdout)
	return err
}

// offerArrayField skips fields already present in fluxa.toml, otherwise
// collects and validates a comma-separated pattern list and writes it on
// confirmation. It returns the collected values (nil if skipped, left
// empty, or declined) so callers can use them in-session, e.g. to validate
// build.export against a build.persistent list just entered.
func (w *wizard) offerArrayField(root, table, key, prompt string) ([]string, error) {
	has, err := project.HasField(root, table, key)
	if err != nil {
		return nil, err
	}
	if has {
		if _, err := fmt.Fprintf(w.stdout, "%s.%s is already set in fluxa.toml; skipping.\n", table, key); err != nil {
			return nil, err
		}
		return nil, nil
	}

	for attempt := 0; attempt < maxWizardAttempts; attempt++ {
		raw, err := w.askLine(prompt)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(raw) == "" {
			return nil, nil
		}
		values := splitCommaList(raw)
		if invalid := firstInvalidPattern(values); invalid != "" {
			if _, err := fmt.Fprintf(w.stdout, "%q is not a valid relative pattern; try again.\n", invalid); err != nil {
				return nil, err
			}
			continue
		}
		if _, err := fmt.Fprintf(w.stdout, "Will add to fluxa.toml: %s = %s\n", key, formatTOMLArrayPreview(values)); err != nil {
			return nil, err
		}
		write, err := w.askYesNo("Write this line to fluxa.toml?", true)
		if err != nil {
			return nil, err
		}
		if write {
			if _, err := project.EnsureStringArrayField(root, table, key, values); err != nil {
				return nil, err
			}
		}
		return values, nil
	}
	return nil, fmt.Errorf("gave up collecting %s.%s after %d attempts", table, key, maxWizardAttempts)
}

// offerExportField is separate from offerArrayField because each entry must
// also appear in cfg.Build.Persistent (the same rule project.Load enforces),
// which is validated against the in-memory list optionalProjectSettings has
// already assembled for this session.
func (w *wizard) offerExportField(cfg *project.Config) error {
	has, err := project.HasField(cfg.Root, "build", "export")
	if err != nil {
		return err
	}
	if has {
		_, err := fmt.Fprintln(w.stdout, "build.export is already set in fluxa.toml; skipping.")
		return err
	}

	for attempt := 0; attempt < maxWizardAttempts; attempt++ {
		raw, err := w.askLine("Which of those paths should also be user-visible, comma-separated (leave empty to skip): ")
		if err != nil {
			return err
		}
		if strings.TrimSpace(raw) == "" {
			return nil
		}
		values := splitCommaList(raw)
		invalid := ""
		for _, value := range values {
			if !containsString(cfg.Build.Persistent, value) {
				invalid = value
				break
			}
		}
		if invalid != "" {
			if _, err := fmt.Fprintf(w.stdout, "%q must also appear in build.persistent; try again.\n", invalid); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w.stdout, "Will add to fluxa.toml: export = %s\n", formatTOMLArrayPreview(values)); err != nil {
			return err
		}
		write, err := w.askYesNo("Write this line to fluxa.toml?", true)
		if err != nil {
			return err
		}
		if write {
			_, err := project.EnsureStringArrayField(cfg.Root, "build", "export", values)
			return err
		}
		return nil
	}
	return fmt.Errorf("gave up collecting build.export after %d attempts", maxWizardAttempts)
}

// offerTerminalField asks whether the generated application should run
// attached to a terminal/console window. This directly changes what gets
// built: Linux desktop entries copy the value, macOS uses the bundle launch
// model either way, and the Windows launcher is patched to the GUI PE
// subsystem when false or the console subsystem when true (see
// docs/architecture.md's "Terminal mode" section). Unlike include_source,
// there is no --terminal build flag — the choice only takes effect for this
// build if it is actually written to fluxa.toml, since runBuild reloads the
// project from disk.
func (w *wizard) offerTerminalField(cfg *project.Config) error {
	has, err := project.HasField(cfg.Root, "build", "terminal")
	if err != nil {
		return err
	}
	if has {
		_, err := fmt.Fprintln(w.stdout, "build.terminal is already set in fluxa.toml; skipping.")
		return err
	}
	if _, err := fmt.Fprint(w.stdout,
		"Should the application open a terminal/console window when launched?\n"+
			"This affects the Windows executable's subsystem and the Linux desktop\n"+
			"entry; choose no for a graphical desktop app, yes for a CLI tool.\n"); err != nil {
		return err
	}
	terminal, err := w.askYesNo("Run attached to a terminal/console window?", cfg.Build.Terminal)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w.stdout, "Will add to fluxa.toml: terminal = %t\n", terminal); err != nil {
		return err
	}
	write, err := w.askYesNo("Write this line to fluxa.toml?", true)
	if err != nil {
		return err
	}
	if !write {
		_, err := fmt.Fprintln(w.stdout, "Not written — this choice will not take effect until build.terminal is set in fluxa.toml.")
		return err
	}
	if _, err := project.EnsureBoolField(cfg.Root, "build", "terminal", terminal); err != nil {
		return err
	}
	cfg.Build.Terminal = terminal
	return nil
}

// offerIncludeSourceField explains why package.include_source is needed
// today (see README's "Current scope") and offers to persist it so future
// builds do not need the --include-source flag.
func (w *wizard) offerIncludeSourceField(cfg *project.Config) error {
	has, err := project.HasField(cfg.Root, "package", "include_source")
	if err != nil {
		return err
	}
	if has {
		_, err := fmt.Fprintln(w.stdout, "package.include_source is already set in fluxa.toml; skipping.")
		return err
	}
	if _, err := fmt.Fprintln(w.stdout,
		"Fluxa cannot yet export protected bytecode, so every working build today\n"+
			"needs --include-source (or package.include_source = true)."); err != nil {
		return err
	}
	persist, err := w.askYesNo("Save package.include_source = true in fluxa.toml so future builds don't need the flag?", true)
	if err != nil {
		return err
	}
	if persist {
		if _, err := project.EnsureBoolField(cfg.Root, "package", "include_source", true); err != nil {
			return err
		}
		cfg.Package.IncludeSource = true
	}
	return nil
}

// offerTargetSettings asks for the host platform's icon (the only target
// this machine can build and verify, per chooseTarget) and, on macOS, an
// optional non-default bundle ID.
func (w *wizard) offerTargetSettings(cfg *project.Config) error {
	table := targetsTableFor(w.hostOS)
	if table == "" {
		return nil
	}

	has, err := project.HasField(cfg.Root, table, "icon")
	if err != nil {
		return err
	}
	if has {
		if _, err := fmt.Fprintf(w.stdout, "%s.icon is already set in fluxa.toml; skipping.\n", table); err != nil {
			return err
		}
	} else if err := w.offerIconField(cfg, table); err != nil {
		return err
	}

	if w.hostOS != "macos" {
		return nil
	}
	hasBundleID, err := project.HasField(cfg.Root, table, "bundle_id")
	if err != nil {
		return err
	}
	if hasBundleID {
		_, err := fmt.Fprintln(w.stdout, "targets.macos.bundle_id is already set in fluxa.toml; skipping.")
		return err
	}
	bundleID, err := w.askLine(fmt.Sprintf("macOS bundle ID (leave empty to default to %q): ", cfg.Project.ID))
	if err != nil {
		return err
	}
	if bundleID == "" || bundleID == cfg.Project.ID {
		return nil
	}
	if !project.ValidProjectID(bundleID) {
		if _, err := fmt.Fprintln(w.stdout, "Not a valid reverse-domain identifier; leaving targets.macos.bundle_id unset."); err != nil {
			return err
		}
		return nil
	}
	if _, err := fmt.Fprintf(w.stdout, "Will add to fluxa.toml: bundle_id = %q\n", bundleID); err != nil {
		return err
	}
	write, err := w.askYesNo("Write this line to fluxa.toml?", true)
	if err != nil {
		return err
	}
	if write {
		_, err := project.EnsureStringField(cfg.Root, table, "bundle_id", bundleID)
		return err
	}
	return nil
}

func (w *wizard) offerIconField(cfg *project.Config, table string) error {
	for attempt := 0; attempt < maxWizardAttempts; attempt++ {
		raw, err := w.askLine(fmt.Sprintf("Icon file for %s, relative to the project (leave empty to skip): ", w.hostOS))
		if err != nil {
			return err
		}
		if strings.TrimSpace(raw) == "" {
			return nil
		}
		info, statErr := os.Stat(filepath.Join(cfg.Root, filepath.FromSlash(raw)))
		if statErr != nil || !info.Mode().IsRegular() {
			if _, err := fmt.Fprintf(w.stdout, "%s was not found in %s.\n", raw, cfg.Root); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w.stdout, "Will add to fluxa.toml: icon = %q\n", raw); err != nil {
			return err
		}
		write, err := w.askYesNo("Write this line to fluxa.toml?", true)
		if err != nil {
			return err
		}
		if write {
			_, err := project.EnsureStringField(cfg.Root, table, "icon", raw)
			return err
		}
		return nil
	}
	return fmt.Errorf("gave up collecting %s.icon after %d attempts", table, maxWizardAttempts)
}

func targetsTableFor(hostOS string) string {
	switch hostOS {
	case "windows":
		return "targets.windows"
	case "linux":
		return "targets.linux"
	case "macos":
		return "targets.macos"
	default:
		return ""
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func splitCommaList(raw string) []string {
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func firstInvalidPattern(values []string) string {
	for _, value := range values {
		if !project.ValidPattern(value) {
			return value
		}
	}
	return ""
}

func formatTOMLArrayPreview(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = fmt.Sprintf("%q", value)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// offerPersistToolchainPath is only offered right after the user had to
// type an explicit toolchain path this session — that is precisely the case
// where a future run would otherwise need --fluxa again. If toolchain.path
// is already configured, nothing is asked or written.
func (w *wizard) offerPersistToolchainPath(cfg *project.Config, path string) error {
	has, err := project.HasField(cfg.Root, "toolchain", "path")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	persist, err := w.askYesNo("Save this path as fluxa.toml's [toolchain] path so future builds don't need --fluxa?", true)
	if err != nil {
		return err
	}
	if !persist {
		return nil
	}
	_, err = project.EnsureStringField(cfg.Root, "toolchain", "path", path)
	return err
}

// setupOrBuild resolves the toolchain and checks whether any runtime is
// registered at all (a coarse pre-check; the real runBuild call still does
// exact compatibility matching). When both are ready it delegates straight
// into runBuild; otherwise it prints the manual setup guide.
func (w *wizard) setupOrBuild(cfg *project.Config, outputOverride string) int {
	candidate, toolchainErr := w.deps.resolve(toolchain.ResolveOptions{
		ConfigPath:  cfg.Toolchain.Path,
		FluxaHome:   os.Getenv("FLUXA_HOME"),
		PathEnv:     os.Getenv("PATH"),
		ProjectRoot: cfg.Root,
	})
	if toolchainErr != nil {
		var terr *toolchain.Error
		if errors.As(toolchainErr, &terr) && terr.Kind == toolchain.ErrorNotFound {
			explicit, err := w.askLine("Path to your local fluxa executable (leave empty to skip): ")
			if err != nil {
				return w.fail(err)
			}
			if explicit != "" {
				candidate, toolchainErr = w.deps.resolve(toolchain.ResolveOptions{
					ExplicitPath: explicit,
					ConfigPath:   cfg.Toolchain.Path,
					FluxaHome:    os.Getenv("FLUXA_HOME"),
					PathEnv:      os.Getenv("PATH"),
					ProjectRoot:  cfg.Root,
				})
				if toolchainErr == nil {
					if err := w.offerPersistToolchainPath(cfg, explicit); err != nil {
						return w.fail(err)
					}
				}
			}
		}
	}

	haveToolchain := toolchainErr == nil
	var identity toolchain.Identity
	if haveToolchain {
		var probeErr error
		identity, probeErr = w.deps.probe(context.Background(), candidate.Path, 5*time.Second)
		if probeErr != nil {
			if _, err := fmt.Fprintf(w.stdout, "Found a toolchain at %s but could not identify it:\n  %v\n\n", candidate.Path, probeErr); err != nil {
				return w.fail(err)
			}
			haveToolchain = false
		}
	}

	haveRuntime := false
	if haveToolchain {
		registryRoot, err := buildRegistryRoot("")
		if err != nil {
			return w.fail(err)
		}
		if runtimes, listErr := w.deps.listRuntimes(registryRoot); listErr == nil && len(runtimes) > 0 {
			haveRuntime = true
		}
	}

	if !haveToolchain || !haveRuntime {
		return w.manualGuide(cfg, identity, haveToolchain)
	}

	if _, err := fmt.Fprintf(w.stdout,
		"\nA Fluxa toolchain (%s) and at least one registered runtime were found.\n"+
			"Fluxa does not yet export protected bytecode, so today's only working\n"+
			"build mode stages readable .flx source in the package and marks it\n"+
			"development/source-exposed. That is documented, expected behavior.\n\n",
		candidate.Path,
	); err != nil {
		return w.fail(err)
	}
	proceed, err := w.askYesNo("Build now in development/source-exposed mode?", true)
	if err != nil {
		return w.fail(err)
	}
	if !proceed {
		return w.fail(errWizardAbort)
	}

	buildArgs := []string{cfg.Root, "--fluxa", candidate.Path, "--include-source"}
	if outputOverride != "" {
		buildArgs = append(buildArgs, "--output", outputOverride)
	}
	return runBuild(buildArgs, w.stdout, w.stderr, w.deps)
}

// manualGuide walks the user through obtaining a toolchain/runtime by hand.
// Automatic download-and-build of the fluxa-lang source tree is
// deliberately out of scope for this version (see docs/adr/0024); the
// automatic option always explains that and falls through here.
func (w *wizard) manualGuide(cfg *project.Config, identity toolchain.Identity, haveToolchain bool) int {
	if _, err := fmt.Fprintln(w.stdout, "\nSetup is not complete yet."); err != nil {
		return w.fail(err)
	}
	if _, err := w.askYesNo("Download and build the Fluxa toolchain automatically now?", false); err != nil {
		return w.fail(err)
	}
	if _, err := fmt.Fprint(w.stdout,
		"Automatic download/build is not available yet in this version of "+
			"Fluxa Builder. Here is what to do manually.\n\n",
	); err != nil {
		return w.fail(err)
	}

	if !haveToolchain {
		if _, err := fmt.Fprint(w.stdout,
			"1. Get the fluxa toolchain executable itself (installed separately\n"+
				"   from the fluxa-lang source build below) and make it reachable via\n"+
				"   --fluxa, fluxa.toml's [toolchain] path, FLUXA_HOME, or PATH.\n\n",
		); err != nil {
			return w.fail(err)
		}
	}

	checkoutPath, err := w.askLine("Path to a local fluxa-lang checkout (leave empty if you don't have one): ")
	if err != nil {
		return w.fail(err)
	}
	makeDir := checkoutPath
	if checkoutPath == "" {
		if _, err := fmt.Fprintln(w.stdout, "\n  git clone https://github.com/RodBarenco/fluxa-lang"); err != nil {
			return w.fail(err)
		}
		makeDir = "fluxa-lang"
	}

	// Only Windows needs a separately-built, restricted interpreter
	// (FLUXA_PACKAGED_RUNTIME=1, produced by make build-windows-packaged,
	// per that repository's docs/WINDOWS.md): its native entrypoint has no
	// POSIX runtime services to fall back on and links every third-party
	// dependency statically. Linux's native interpreter is just `make
	// build`; Fluxa Builder supplies the private-entry restriction itself
	// as an embedded relay assembled at `build` time (ADR 0025), so no
	// special flags are needed there.
	makeCommand := "make build"
	builtBinaryName := "fluxa"
	if w.hostOS == "windows" {
		makeCommand = "make build-windows-packaged"
		builtBinaryName = "fluxa-runtime.exe"
	}
	if _, err := fmt.Fprintf(w.stdout,
		"\n  cd %s && %s\n\n"+
			"Match the optional FLUXA_* backend flags (FLUXA_GRAPH_RAYLIB=1, etc.) to\n"+
			"what this project's fluxa.libs actually needs; see that repository's own\n"+
			"documentation and this repo's docs/distribution.md for the full list.\n\n",
		makeDir, makeCommand,
	); err != nil {
		return w.fail(err)
	}

	binaryPath, err := w.askLine(fmt.Sprintf("Path to the %s binary you just built (leave empty to fill in later): ", builtBinaryName))
	if err != nil {
		return w.fail(err)
	}

	template := runtimepkg.Metadata{
		FormatVersion:        runtimepkg.CurrentMetadataVersion,
		FluxaVersion:         "unreported",
		PackageFormatVersion: 1,
		ProgramFormats:       []string{"fluxa-source"},
		Packaged:             true,
		OS:                   w.hostOS,
		Arch:                 w.hostArch,
		Terminal:             cfg.Build.Terminal,
	}
	if haveToolchain {
		if identity.Version != "" {
			template.FluxaVersion = identity.Version
		}
		template.ToolchainSHA256 = identity.SHA256
	}
	librariesHash, hashErr := hashOptionalFile(filepath.Join(cfg.Root, "fluxa.libs"))
	if hashErr != nil {
		return w.fail(hashErr)
	}
	template.LibrariesSHA256 = librariesHash

	if binaryPath != "" {
		hash, hashErr := hashFile(binaryPath)
		if hashErr != nil {
			if _, err := fmt.Fprintf(w.stdout, "Could not hash %s: %v\n", binaryPath, hashErr); err != nil {
				return w.fail(err)
			}
		} else {
			template.BinaryName = filepath.Base(binaryPath)
			template.BinarySHA256 = hash
		}
	}

	templatePath, err := w.askWithDefault("Where should I write the runtime.json template?", "./runtime.json")
	if err != nil {
		return w.fail(err)
	}
	data, marshalErr := json.MarshalIndent(template, "", "  ")
	if marshalErr != nil {
		return w.fail(marshalErr)
	}
	// templatePath is an interactive, user-chosen local path, matching how
	// `runtime add --metadata` already treats caller-selected paths.
	if err := os.WriteFile(templatePath, append(data, '\n'), 0o644); err != nil { // #nosec G304,G306
		return w.fail(err)
	}
	if _, err := fmt.Fprintf(w.stdout,
		"\nWrote a starting point to %s. Fields that cannot be known yet\n"+
			"(bytecode_version, bytecode_abi, and binary_name/binary_sha256 if you\n"+
			"did not give a binary path above) are left empty — fill them in, then:\n\n"+
			"  fluxa-builder runtime add <binary> --metadata %s\n\n"+
			"and re-run fluxa-builder init (or fluxa-builder build %s --include-source)\n"+
			"once the toolchain and runtime are ready.\n",
		templatePath, templatePath, cfg.Root,
	); err != nil {
		return w.fail(err)
	}
	return 0
}

func hashOptionalFile(path string) (string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- fixed project-relative fluxa.libs path.
	if err != nil {
		if os.IsNotExist(err) {
			empty := sha256.Sum256(nil)
			return hex.EncodeToString(empty[:]), nil
		}
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func hashFile(path string) (string, error) {
	// path is an interactive, user-provided local path: the same local
	// trust decision `runtime add` already makes for its own binary/
	// metadata arguments.
	file, err := os.Open(path) // #nosec G304
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
