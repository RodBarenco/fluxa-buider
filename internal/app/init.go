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

	"github.com/chzyer/readline"

	"github.com/RodBarenco/fluxa-builder/internal/compiler"
	"github.com/RodBarenco/fluxa-builder/internal/project"
	runtimepkg "github.com/RodBarenco/fluxa-builder/internal/runtime"
	"github.com/RodBarenco/fluxa-builder/internal/toolchain"
	"github.com/RodBarenco/fluxa-builder/internal/toolchainbuild"
)

// maxWizardAttempts bounds every interactive retry loop below. In real
// interactive use a human simply keeps typing; the bound only matters as a
// defensive backstop and as what makes scripted-stdin tests deterministic.
const maxWizardAttempts = 6

// wizardSteps is how many numbered sections run() walks through, shown in
// every section header so a long interactive run always says where it is.
const wizardSteps = 5

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
	// Real line editing (arrow keys to move the cursor, up/down for
	// history) needs an actual terminal on the other end, not whatever
	// io.Reader happens to be passed in — a piped script, a test's
	// strings.Reader, or a real interactive session. Only the last of
	// those is both an *os.File and an attached terminal; the plain
	// bufio.Reader above stays the fallback for everything else,
	// unchanged, so no existing (or future) non-interactive caller is
	// affected by this at all.
	if file, ok := stdin.(*os.File); ok {
		w.stdinFile = file
	}
	return w.run()
}

type wizard struct {
	reader           *bufio.Reader
	stdinFile        *os.File
	rl               *readline.Instance
	stdout           io.Writer
	stderr           io.Writer
	deps             buildDependencies
	hostOS, hostArch string
	style            style
	errStyle         style
	spinner          *spinner
}

func (w *wizard) run() int {
	w.style = newStyle(w.stdout)
	w.errStyle = newStyle(w.stderr)
	w.spinner = newSpinner(w.stdout, w.style)
	if w.stdinFile != nil && isTerminalFile(w.stdinFile) && w.style.enabled {
		if rl, err := readline.NewEx(&readline.Config{
			Stdin:  io.NopCloser(w.stdinFile),
			Stdout: w.stdout,
			Stderr: w.stderr,
			// A shortcut-key history spanning separate `init` runs would
			// resurface a previous, unrelated project's paths — session-only
			// (in-memory, HistoryFile unset) matches what a fresh terminal's
			// own history would offer, no more.
		}); err == nil {
			w.rl = rl
			defer func() { _ = w.rl.Close() }()
		}
	}
	w.hostOS, w.hostArch = hostTargetOS()
	// The animated mark when there is a real terminal to draw it into,
	// the static box otherwise — drawSplash reports which happened, since
	// only it knows whether the terminal was big enough.
	opening := w.style.banner("Fluxa Builder", "interactive project setup")
	if drawSplash(w.stdout, w.style) {
		opening = splashCaption(w.style, "Fluxa Builder", "interactive project setup")
	}
	if _, err := fmt.Fprint(w.stdout, opening); err != nil {
		return w.fail(err)
	}
	if _, err := fmt.Fprintf(w.stdout, "%s\n", w.style.field("Host", targetDirectoryName(w.hostOS, w.hostArch))); err != nil {
		return w.fail(err)
	}

	cfg, err := w.resolveProject()
	if err != nil {
		return w.fail(err)
	}

	// The target has to be settled before any target-specific question is
	// asked. It used to be chosen *after* optionalProjectSettings, which
	// meant every per-platform answer was collected for whatever machine
	// happened to be running the wizard: a Windows build driven from Linux
	// was asked for a Linux .png icon, wrote targets.linux.icon, and never
	// asked for targets.windows.icon at all — so the .exe it then produced
	// silently had no icon, because runBuild only ever reads
	// targets.windows.icon for a Windows target.
	targetOverride, err := w.chooseTarget()
	if err != nil {
		return w.fail(err)
	}
	targetOS, _, err := resolveTargetOSArch(w.hostOS, w.hostArch, targetOverride)
	if err != nil {
		return w.fail(err)
	}

	if err := w.optionalProjectSettings(cfg, targetOS); err != nil {
		return w.fail(err)
	}

	outputOverride, err := w.chooseOutput(cfg)
	if err != nil {
		return w.fail(err)
	}

	return w.setupOrBuild(cfg, outputOverride, targetOverride)
}

func (w *wizard) fail(err error) int {
	if errors.Is(err, errWizardAbort) {
		_, _ = fmt.Fprintln(w.stdout, "Aborted.")
		return 1
	}
	if errors.Is(err, errWizardEOF) {
		_, _ = fmt.Fprintln(w.stderr, w.errStyle.bad("error: input ended unexpectedly"))
		return 1
	}
	_, _ = fmt.Fprintln(w.stderr, w.errStyle.bad(fmt.Sprintf("error: %v", err)))
	return 1
}

// askLine writes prompt and reads one line. It returns errWizardEOF only
// when stdin ended with nothing usable on the current line; a final line
// without a trailing newline is still returned normally.
//
// With a real interactive terminal (w.rl set), this goes through a real
// line editor instead of a raw byte scan: arrow keys move the cursor and
// recall history instead of being inserted as literal escape-sequence
// bytes into the answer, a real reported failure with the plain
// bufio.Reader path below.
func (w *wizard) askLine(prompt string) (string, error) {
	return w.askRawLine(w.style.question(prompt))
}

// askRawLine is askLine without the answer caret style.question adds, for
// the one prompt that renders its own complete decoration: the
// project-directory question's simulated shell prompt.
func (w *wizard) askRawLine(prompt string) (string, error) {
	if w.rl != nil {
		w.rl.SetPrompt(prompt)
		line, err := w.rl.Readline()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", errWizardEOF
			}
			if errors.Is(err, readline.ErrInterrupt) {
				return "", errWizardAbort
			}
			return "", err
		}
		return strings.TrimSpace(line), nil
	}

	if _, err := fmt.Fprint(w.stdout, prompt); err != nil {
		return "", err
	}
	line, err := w.reader.ReadString('\n')
	// Trimming only "\r\n" left stray trailing spaces (e.g. from a
	// copy-paste) baked into the value — for a path, that alone turns an
	// otherwise-correct answer into a silent "not found". Every prompt in
	// this wizard expects a meaningful value with no leading/trailing
	// whitespace of its own, so trimming it here is always correct, not
	// just for paths.
	line = strings.TrimSpace(line)
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
		label = prompt + " " + w.style.dim("["+def+"]")
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
	value, err := w.askLine(prompt + " " + w.style.dim("["+hint+"]") + ": ")
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

// skipNotice reports that field is already configured in fluxa.toml, so
// the wizard is neither asking about it nor touching it. These are the
// wizard's most repetitive lines; rendering them dim keeps a project that
// is already fully configured from looking like a wall of warnings.
func (w *wizard) skipNotice(field string) error {
	_, err := fmt.Fprintln(w.stdout, w.style.dim("  "+field+" is already set in fluxa.toml; skipping."))
	return err
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
//
// On Linux and macOS the question is deliberately rendered and resolved
// like a fresh terminal sitting at the user's home directory — an actual
// "user@host:~$ " prompt, with a bare relative answer (or one prefixed
// with "~" or even a needless "./") resolved against $HOME rather than
// wherever the fluxa-builder process itself happens to be running from.
// Two real reports converged on this: resolving against the process's
// own directory is technically correct but never what anyone typing a
// path expects, and no amount of after-the-fact explaining a "./" typo
// (a prior, narrower fix here) changes that expectation. Matching how a
// real shell already behaves removes the mismatch entirely instead of
// explaining around it. Windows keeps the plain prompt: it has no
// equivalent "user@host:~$" convention to mirror.
func (w *wizard) resolveProject() (*project.Config, error) {
	if _, err := fmt.Fprint(w.stdout, w.style.step(1, wizardSteps, "Project")); err != nil {
		return nil, err
	}

	home, homeErr := os.UserHomeDir()
	useShellPrompt := w.hostOS != "windows" && homeErr == nil && home != ""
	if useShellPrompt {
		if _, err := fmt.Fprintln(w.stdout, w.style.dim(
			"  Type the project directory like you would at a fresh terminal in\n"+
				"  your home directory; relative paths resolve from there, not from\n"+
				"  wherever fluxa-builder itself happens to be running.",
		)); err != nil {
			return nil, err
		}
	}

	for attempt := 0; attempt < maxWizardAttempts; attempt++ {
		var raw string
		var err error
		if useShellPrompt {
			raw, err = w.askRawLine(w.style.shellPrompt(shellUserAtHost(), "~"))
			if err != nil {
				return nil, err
			}
			if raw == "" {
				raw = "."
			}
		} else {
			raw, err = w.askWithDefault("Project directory (full path)", ".")
			if err != nil {
				return nil, err
			}
		}

		abs, resolveErr := resolveProjectDirectoryInput(raw, home, useShellPrompt)
		if resolveErr != nil {
			if _, err := fmt.Fprintf(w.stdout, "Could not resolve %q: %v\n\n", raw, resolveErr); err != nil {
				return nil, err
			}
			continue
		}
		if info, statErr := os.Stat(abs); statErr != nil || !info.IsDir() {
			if _, err := fmt.Fprintf(w.stdout, "\n%s\n\n", w.style.bad(fmt.Sprintf("Directory not found: %s", abs))); err != nil {
				return nil, err
			}
			continue
		}
		if _, err := fmt.Fprintf(w.stdout, "%s\n", w.style.field("Directory", abs)); err != nil {
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

// resolveProjectDirectoryInput turns one raw project-directory answer
// into an absolute path. When homeRelative is set, a non-absolute answer
// (after expanding a leading "~") resolves against home — matching a
// real shell sitting at "~" — with the sole exception of the bare "."
// default, kept relative to the real process directory so "just press
// enter" still means "the directory I already cd'd into," the common
// case every other CLI tool's own directory default already assumes.
func resolveProjectDirectoryInput(raw, home string, homeRelative bool) (string, error) {
	expanded := expandHome(raw)
	if !homeRelative || filepath.IsAbs(expanded) || expanded == "." {
		return filepath.Abs(expanded)
	}
	return filepath.Abs(filepath.Join(home, expanded))
}

// expandHome expands a leading "~" or "~/..." to the user's home
// directory. The wizard reads raw stdin lines directly, not through a
// shell, so "~" is never otherwise special — but it is such a common,
// expected shorthand for a path that leaving it unexpanded turns an
// intended-correct answer into a literal, nonexistent "~" path entry.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/"))
}

// shellUserAtHost returns "user@host" the way a real shell prompt shows
// it, trimming any domain suffix os.Hostname() reports (e.g.
// "pop-os.localdomain") down to the short form an actual prompt uses.
func shellUserAtHost() string {
	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("LOGNAME")
	}
	if username == "" {
		username = "user"
	}
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "localhost"
	}
	if dot := strings.IndexByte(hostname, '.'); dot != -1 {
		hostname = hostname[:dot]
	}
	return username + "@" + hostname
}

// errTryDifferentDirectory signals resolveProject to re-prompt for a
// directory rather than retrying project.Load on the same path.
var errTryDifferentDirectory = errors.New("try a different project directory")

func (w *wizard) loadProjectWithFixes(abs string) (*project.Config, error) {
	for attempt := 0; attempt < maxWizardAttempts; attempt++ {
		cfg, loadErr := project.Load(abs)
		if loadErr == nil {
			fields := strings.Join([]string{
				w.style.field("Name", cfg.Project.Name),
				w.style.field("ID", cfg.Project.ID),
				w.style.field("Version", cfg.Project.Version),
				w.style.field("Entry", cfg.Project.Entry),
				w.style.field("Terminal", fmt.Sprintf("%t", cfg.Build.Terminal)),
			}, "\n")
			if _, err := fmt.Fprintf(w.stdout, "\n%s\n%s\n", w.style.ok("Project loaded"), fields); err != nil {
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
			// Same resolution as the icon question, for the same reason: an
			// absolute or "~/" answer is a normal thing to paste, and
			// joining it onto the project root turns it into a path that
			// cannot exist.
			relative, absolute, resolveErr := resolveProjectFileInput(root, value)
			if resolveErr != nil {
				if _, err := fmt.Fprintf(w.stdout, "%s\n", w.style.bad(resolveErr.Error())); err != nil {
					return "", err
				}
				continue
			}
			if info, statErr := os.Stat(absolute); statErr == nil && info.Mode().IsRegular() {
				return relative, nil
			}
			if _, err := fmt.Fprintf(w.stdout, "%s\n", w.style.bad("Not found: "+absolute)); err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("unsupported field %q", field)
		}
	}
	return "", fmt.Errorf("gave up collecting %s after %d attempts", field, maxWizardAttempts)
}

// chooseTarget lets the user pick which platform this run builds for and
// returns the value to hand runBuild as --target — plumbed through
// setupOrBuild exactly like chooseOutput's override already flows through
// --output, since runBuild reloads the project from disk and would
// otherwise never see an in-memory-only change.
//
// Two rules make the answer trustworthy, and both exist because breaking
// them produced builds that contradicted what this menu had just printed:
//
//   - The return value is never empty. "" used to mean "pass no --target",
//     which does not mean "build for this machine" — it defers to whatever
//     build.target fluxa.toml carries. A project pinned to windows-x64
//     therefore built Windows even after the menu announced "Building for
//     linux-x64 in this run." Choosing this machine now returns the
//     explicit "host" sentinel, which runBuild resolves against the real
//     host (and, unlike a literal "linux-x64", stays correct on an arm64
//     host that resolveManifestTarget would otherwise reject).
//   - An answer that cannot be honored re-asks instead of quietly falling
//     back to the host. Unrecognized input, "all three", and a macOS
//     target from a non-Mac all used to end with a host build the user
//     never asked for.
//
// See docs/adr/0028-container-verified-cross-platform-builds.md:
// windows-x64 and linux-x64 can be verified from any host with Docker (a
// produced Windows build inside a Wine container, a produced Linux build
// inside a plain Linux container), which is what makes honoring a
// cross-target choice safe. macOS stays host-only — Docker cannot run
// macOS at all — and building all three platforms in one run is not
// implemented yet.
func (w *wizard) chooseTarget() (string, error) {
	hostName := targetDirectoryName(w.hostOS, w.hostArch)
	if _, err := fmt.Fprint(w.stdout, w.style.step(2, wizardSteps, "Target platform")); err != nil {
		return "", err
	}

	for attempt := 0; attempt < maxWizardAttempts; attempt++ {
		// The full menu once, then a one-line reminder: re-printing five
		// rows after every rejected answer buries the explanation of *why*
		// it was rejected under the same list that was just misread.
		if attempt == 0 {
			if err := w.printTargetMenu(hostName); err != nil {
				return "", err
			}
		} else if _, err := fmt.Fprintln(w.stdout,
			w.style.dim("  Answer 1-5, or a platform name (windows, linux, macos).")); err != nil {
			return "", err
		}
		choice, err := w.askWithDefault("Choice", "1")
		if err != nil {
			return "", err
		}

		chosenOS := ""
		switch normalizeTargetChoice(choice) {
		case "host":
			chosenOS = w.hostOS
		case "windows":
			chosenOS = "windows"
		case "linux":
			chosenOS = "linux"
		case "macos":
			chosenOS = "macos"
		case "all":
			if _, err := fmt.Fprintf(w.stdout,
				"\n%s\n%s\n\n",
				w.style.warn("Building all three platforms in a single init run is not supported yet."),
				w.style.dim("  Pick one platform now, then run fluxa-builder init again for each\n  of the others."),
			); err != nil {
				return "", err
			}
			continue
		default:
			if _, err := fmt.Fprintf(w.stdout, "\n%s\n\n",
				w.style.bad(fmt.Sprintf("%q is not one of the choices above.", choice)),
			); err != nil {
				return "", err
			}
			continue
		}

		if chosenOS == w.hostOS {
			if _, err := fmt.Fprintf(w.stdout, "\n%s\n",
				w.style.ok("Building for "+w.style.accent(hostName)+" in this run."),
			); err != nil {
				return "", err
			}
			return "host", nil
		}
		if chosenOS == "macos" {
			// Docker cannot run macOS at all (Apple does not license it for
			// containers), so there is no container fallback — this stays
			// host-only, unlike windows-x64/linux-x64 below.
			if _, err := fmt.Fprintf(w.stdout,
				"\n%s\n%s\n\n",
				w.style.warn("This machine can only build and verify macOS on real macOS hardware."),
				w.style.dim("  Unlike Windows or Linux, there is no way to verify a macOS build in\n"+
					"  a container. Run fluxa-builder init (or build) again on a Mac to\n"+
					"  produce it, and pick another target for this run."),
			); err != nil {
				return "", err
			}
			continue
		}

		target := chosenOS + "-x64"
		if _, err := fmt.Fprintf(w.stdout,
			"\n%s\n%s\n",
			w.style.ok("Building for "+w.style.accent(target)+" in this run."),
			w.style.dim(fmt.Sprintf(
				"  This machine cannot run a %s application directly, so it\n"+
					"  will be verified inside a network-isolated Docker container\n"+
					"  rather than natively (see docs/adr/0028) — Docker must be\n"+
					"  installed and reachable for this to work.", target)),
		); err != nil {
			return "", err
		}
		return target, nil
	}
	return "", fmt.Errorf("gave up choosing a target platform after %d attempts", maxWizardAttempts)
}

func (w *wizard) printTargetMenu(hostName string) error {
	rows := []struct{ key, label, note string }{
		{"1", hostName, "this machine — verified natively (default)"},
		{"2", "windows-x64", "cross-built, verified in a Wine container"},
		{"3", "linux-x64", "cross-built, verified in a Linux container"},
		{"4", "macos", "real Mac hardware only"},
		{"5", "all three", "not supported in one run yet"},
	}
	// Row 1 already *is* one of rows 2-4 — whichever matches this host —
	// and listing the same platform twice with two different descriptions
	// reads as two different builds. Both answers do resolve to the same
	// host build; say so instead of leaving it to be guessed.
	const sameAsHost = "same as choice 1 on this machine"
	switch w.hostOS {
	case "windows":
		rows[1].note = sameAsHost
	case "linux":
		rows[2].note = sameAsHost
	case "macos":
		rows[3].note = sameAsHost
	}
	if _, err := fmt.Fprintln(w.stdout, w.style.dim("  Which platform should this run produce artifacts for?")); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintln(w.stdout, w.style.menuItem(row.key, row.label, row.note)); err != nil {
			return err
		}
	}
	return nil
}

// normalizeTargetChoice maps one raw menu answer to a canonical platform
// key, or "" when it matches nothing. It accepts the platform names
// themselves alongside the menu numbers: typing "windows" instead of "2"
// is the single most likely way to answer this question, and it used to
// fall through to a silent host build.
func normalizeTargetChoice(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "host", "this", "this machine":
		return "host"
	case "2", "windows", "win", "windows-x64":
		return "windows"
	case "3", "linux", "linux-x64":
		return "linux"
	case "4", "macos", "mac", "osx", "darwin", "macos-x64":
		return "macos"
	case "5", "all", "all three":
		return "all"
	default:
		return ""
	}
}

// chooseOutput returns a non-empty override only when the user picked
// something other than the project's current default.
func (w *wizard) chooseOutput(cfg *project.Config) (string, error) {
	if _, err := fmt.Fprint(w.stdout, w.style.step(4, wizardSteps, "Output directory")); err != nil {
		return "", err
	}
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
// package.include_source, and the icon/bundle metadata of targetOS — the
// platform this run actually builds for, which chooseTarget has already
// settled and which is not necessarily the host. Every field is skippable
// (blank input) and, like the required-field fix flow, every write is
// previewed and confirmed first. A field already present in fluxa.toml is
// never re-asked or overwritten.
func (w *wizard) optionalProjectSettings(cfg *project.Config, targetOS string) error {
	if _, err := fmt.Fprint(w.stdout, w.style.step(3, wizardSteps, "Project settings")); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w.stdout, w.style.dim("  Optional — leave any prompt blank to skip it.")); err != nil {
		return err
	}

	if err := w.offerTerminalField(cfg, targetOS); err != nil {
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
	if err := w.offerTargetSettings(cfg, targetOS); err != nil {
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
		if err := w.skipNotice(fmt.Sprintf("%s.%s", table, key)); err != nil {
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
		return w.skipNotice("build.export")
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
func (w *wizard) offerTerminalField(cfg *project.Config, targetOS string) error {
	has, err := project.HasField(cfg.Root, "build", "terminal")
	if err != nil {
		return err
	}
	if has {
		return w.skipNotice("build.terminal")
	}
	consequence := "It sets the Windows executable's subsystem and the Linux\n  desktop entry's Terminal= value."
	switch targetOS {
	case "windows":
		consequence = "For this windows build it selects the executable's PE\n  subsystem."
	case "linux":
		consequence = "For this linux build it selects the desktop entry's\n  Terminal= value."
	}
	if _, err := fmt.Fprintln(w.stdout, w.style.dim(
		"  Should the application open a terminal/console window when launched?\n"+
			"  "+consequence+" Choose no for a graphical desktop app, yes for a\n"+
			"  CLI tool.")); err != nil {
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
		return w.skipNotice("package.include_source")
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

// offerTargetSettings asks for the icon of targetOS — the platform this
// run builds for, per chooseTarget — and, for a macOS target, an optional
// non-default bundle ID.
//
// targetOS, not the host: runBuild reads exactly one icon field per build,
// the one matching the target it is producing (see runBuild's
// cfg.Targets.Windows/Linux/MacOS.Icon selection), so asking about the
// host's platform on a cross-target run collected an icon that build could
// not use and never asked for the one it needed.
func (w *wizard) offerTargetSettings(cfg *project.Config, targetOS string) error {
	table := targetsTableFor(targetOS)
	if table == "" {
		return nil
	}

	has, err := project.HasField(cfg.Root, table, "icon")
	if err != nil {
		return err
	}
	if has {
		if err := w.skipNotice(table + ".icon"); err != nil {
			return err
		}
	} else if err := w.offerIconField(cfg, table, targetOS); err != nil {
		return err
	}

	if targetOS != "macos" {
		return nil
	}
	hasBundleID, err := project.HasField(cfg.Root, table, "bundle_id")
	if err != nil {
		return err
	}
	if hasBundleID {
		return w.skipNotice("targets.macos.bundle_id")
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

func (w *wizard) offerIconField(cfg *project.Config, table, targetOS string) error {
	if _, err := fmt.Fprintln(w.stdout, w.style.dim(fmt.Sprintf(
		"  Icon file for %s — must be a %s file. Type it like you would at a\n"+
			"  terminal sitting in the project directory; an absolute path or a ~/\n"+
			"  path works too, as long as the file itself lives inside the project.\n"+
			"  Leave it blank to skip.", targetOS, iconFormatFor(targetOS)))); err != nil {
		return err
	}
	for attempt := 0; attempt < maxWizardAttempts; attempt++ {
		raw, err := w.askProjectFile(cfg.Root)
		if err != nil {
			return err
		}
		if strings.TrimSpace(raw) == "" {
			return nil
		}
		relative, absolute, resolveErr := resolveProjectFileInput(cfg.Root, raw)
		if resolveErr != nil {
			if _, err := fmt.Fprintf(w.stdout, "%s\n", w.style.bad(resolveErr.Error())); err != nil {
				return err
			}
			continue
		}
		info, statErr := os.Stat(absolute)
		if statErr != nil || !info.Mode().IsRegular() {
			if _, err := fmt.Fprintf(w.stdout, "%s\n", w.style.bad("Not found: "+absolute)); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w.stdout, "Will add to fluxa.toml: icon = %q\n", relative); err != nil {
			return err
		}
		write, err := w.askYesNo("Write this line to fluxa.toml?", true)
		if err != nil {
			return err
		}
		if write {
			_, err := project.EnsureStringField(cfg.Root, table, "icon", relative)
			return err
		}
		return nil
	}
	return fmt.Errorf("gave up collecting %s.icon after %d attempts", table, maxWizardAttempts)
}

// askProjectFile asks for a path inside the project, rendered as the same
// simulated shell prompt the project-directory question uses — except
// anchored at the project directory rather than at home, which is what
// this question's paths are actually relative to. The project question
// established that mirroring a real prompt is what makes path answers
// behave the way people expect; a plain labeled prompt here quietly went
// back on that, and the mismatch is what made a pasted absolute path look
// like the wizard simply could not find an existing file.
func (w *wizard) askProjectFile(root string) (string, error) {
	home, err := os.UserHomeDir()
	if w.hostOS == "windows" || err != nil || home == "" {
		return w.askLine("Path (leave empty to skip): ")
	}
	return w.askRawLine(w.style.shellPrompt(shellUserAtHost(), abbreviateHome(root, home)))
}

// abbreviateHome renders path the way a shell prompt does, with the user's
// home directory collapsed to "~".
func abbreviateHome(path, home string) string {
	if path == home {
		return "~"
	}
	if prefix := home + string(filepath.Separator); strings.HasPrefix(path, prefix) {
		return "~" + string(filepath.Separator) + strings.TrimPrefix(path, prefix)
	}
	return path
}

// resolveProjectFileInput turns one raw answer naming a file that has to
// live inside the project into the project-relative, slash-separated form
// fluxa.toml stores, plus the absolute path to check on disk.
//
// It accepts everything someone would reasonably type or paste: a bare
// relative path, a "./"-prefixed one, "~/...", and — the case that used
// to fail outright — a full absolute path. The previous code joined the
// answer onto the project root unconditionally, so an absolute answer
// became project_root + absolute_path, a directory that cannot exist,
// reported as "not found" for a file the user was looking straight at.
//
// Paths outside the project are rejected rather than silently accepted:
// project.Load enforces the same rule on every icon field, so storing one
// would only move the failure to the next load.
func resolveProjectFileInput(root, raw string) (relative, absolute string, err error) {
	expanded := expandHome(strings.TrimSpace(raw))
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(root, filepath.FromSlash(expanded))
	}
	absolute, err = filepath.Abs(expanded)
	if err != nil {
		return "", "", err
	}
	relative, err = filepath.Rel(root, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%s is outside the project directory %s; the file has to live inside the project", absolute, root)
	}
	return filepath.ToSlash(relative), absolute, nil
}

// iconFormatFor names the container each platform's icon must actually be
// in, since internal/portable validates it before assembling anything and
// rejects the build outright otherwise. Naming it in the prompt turns a
// late "validate Windows icon" failure into an answerable question.
func iconFormatFor(targetOS string) string {
	switch targetOS {
	case "windows":
		return ".ico"
	case "linux":
		return ".png"
	case "macos":
		return ".icns"
	default:
		return "platform icon"
	}
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

// resolveTargetOSArch turns targetOverride (chooseTarget's return value:
// "host", or an already-validated "<os>-<arch>" string) into the OS/arch
// this run is actually building and acquiring for. An empty override
// still means the host, for callers that have no menu answer at all.
// Every "what are we building for" decision in
// setupOrBuild/manualGuide/autoAcquire must use this, not w.hostOS/
// w.hostArch directly — those name the machine running Fluxa Builder,
// which docs/adr/0028 made meaningfully different from the build target.
func resolveTargetOSArch(hostOS, hostArch, targetOverride string) (string, string, error) {
	if targetOverride == "" {
		return hostOS, hostArch, nil
	}
	return resolveManifestTarget(targetOverride)
}

// plannedRuntimeRequirement is the runtime.Requirement runBuild will
// compute for the build this wizard is about to start, reconstructed
// before anything is compiled so the "is setup complete" question is
// answered by real resolution rather than by a guess.
//
// It is exact for the only build mode the wizard drives — runBuild is
// always invoked with --include-source — which is what pins the two
// fields that cannot be read off the config: compiler.Compile reports
// FormatSource with an empty bytecode version and ABI for a
// source-exposed compilation, and manifest.New copies both straight
// through. Any future drift fails safe: a requirement that is wrong here
// resolves nothing, which offers acquisition rather than promising a
// build that cannot happen.
func plannedRuntimeRequirement(identity toolchain.Identity, cfg *project.Config, targetOS, targetArch, librariesHash string) runtimepkg.Requirement {
	return runtimepkg.Requirement{
		FluxaVersion:         identity.Version,
		ToolchainSHA256:      identity.SHA256,
		PackageFormatVersion: 1,
		BytecodeVersion:      "",
		BytecodeABI:          "",
		LibrariesSHA256:      librariesHash,
		ProgramFormat:        string(compiler.FormatSource),
		Packaged:             true,
		OS:                   targetOS,
		Arch:                 targetArch,
		Terminal:             cfg.Build.Terminal,
	}
}

// setupOrBuild resolves the toolchain and checks whether a runtime
// registered for targetOverride's actual target (not just any runtime at
// all — a real bug this project's own end-to-end testing caught: an
// already-registered Linux runtime made this step wrongly believe a
// Windows build was ready too) is registered (a coarse pre-check; the
// real runBuild call still does exact compatibility matching). When both
// are ready it delegates straight into runBuild; otherwise it prints the
// manual setup guide.
func (w *wizard) setupOrBuild(cfg *project.Config, outputOverride, targetOverride string) int {
	targetOS, targetArch, targetErr := resolveTargetOSArch(w.hostOS, w.hostArch, targetOverride)
	if targetErr != nil {
		return w.fail(targetErr)
	}
	if _, err := fmt.Fprint(w.stdout, w.style.step(5, wizardSteps, "Toolchain and build")); err != nil {
		return w.fail(err)
	}

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
			explicit = expandHome(explicit)
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

	// Whether a usable runtime exists has to be decided by the very same
	// resolution runBuild will perform, not by an approximation of it.
	// Matching only OS/arch here was an approximation, and it failed in the
	// worst possible way: a Windows runtime registered for a *different*
	// project (different fluxa.libs) or a different toolchain passed the
	// check, the wizard announced everything was ready, the user confirmed
	// the build — and it died with "no verified runtime matches
	// windows/amd64 format fluxa-source" with no way forward offered. The
	// mismatch is exactly the condition automatic acquisition exists to
	// fix, so it has to route there instead.
	haveRuntime := false
	registeredForTarget := false
	if haveToolchain {
		registryRoot, err := buildRegistryRoot("")
		if err != nil {
			return w.fail(err)
		}
		librariesHash, hashErr := hashOptionalFile(filepath.Join(cfg.Root, "fluxa.libs"))
		if hashErr != nil {
			return w.fail(hashErr)
		}
		_, resolveErr := w.deps.resolveRuntime(registryRoot, plannedRuntimeRequirement(identity, cfg, targetOS, targetArch, librariesHash))
		haveRuntime = resolveErr == nil
		// Kept purely to tell "nothing for this platform at all" apart from
		// "something for this platform, but not usable here" — two states
		// that need very different explanations, and only the second one
		// looks like a bug from the outside.
		if runtimes, listErr := w.deps.listRuntimes(registryRoot); listErr == nil {
			for _, registered := range runtimes {
				if registered.Metadata.OS == targetOS && registered.Metadata.Arch == targetArch {
					registeredForTarget = true
					break
				}
			}
		}
	}

	if !haveToolchain || !haveRuntime {
		return w.manualGuide(cfg, identity, haveToolchain, haveRuntime, registeredForTarget,
			outputOverride, targetOverride, targetOS, targetArch)
	}

	targetName := targetDirectoryName(targetOS, targetArch)
	summary := strings.Join([]string{
		w.style.field("Project", cfg.Project.Name+" "+cfg.Project.Version),
		w.style.field("Target", targetName),
		w.style.field("Output", filepath.Join(cfg.OutputPath, targetName)),
		w.style.field("Toolchain", candidate.Path),
	}, "\n")
	if _, err := fmt.Fprintf(w.stdout,
		"\n%s\n%s\n\n%s\n%s\n\n",
		w.style.ok("A Fluxa toolchain and a runtime registered for "+w.style.accent(targetName)+" were found."),
		summary,
		w.style.warn("This build will be development/source-exposed."),
		w.style.dim("  Fluxa does not yet export protected bytecode, so today's only working\n"+
			"  build mode stages readable .flx source in the package and marks it\n"+
			"  development/source-exposed. That is documented, expected behavior."),
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
	// Always passed, never conditionally: omitting --target does not mean
	// "build for this machine", it means "use whatever build.target
	// fluxa.toml says" — which is how a project pinned to one platform
	// used to override the target the menu had just confirmed out loud.
	if targetOverride != "" {
		buildArgs = append(buildArgs, "--target", targetOverride)
	}
	return runBuild(buildArgs, w.stdout, w.stderr, w.deps)
}

// manualGuide walks the user through obtaining a toolchain/runtime by
// hand, for targetOS/targetArch (see resolveTargetOSArch — this is the
// build's actual target, which docs/adr/0028 made meaningfully different
// from w.hostOS/w.hostArch, the machine running Fluxa Builder itself).
// It first offers real automatic acquisition
// (docs/adr/0027-automatic-toolchain-acquisition.md) — cloning fluxa-lang
// and building it inside a pinned Docker container, which works for a
// linux or windows target from any host, not just a matching one —
// falling back to this manual guide only if the user declines, Docker is
// unavailable, or the project needs something automatic acquisition does
// not support yet (an unsupported Windows library, or a macos target,
// still on hold regardless of host — see that ADR).
func (w *wizard) manualGuide(cfg *project.Config, identity toolchain.Identity, haveToolchain, haveRuntime, registeredForTarget bool, outputOverride, targetOverride, targetOS, targetArch string) int {
	targetName := targetDirectoryName(targetOS, targetArch)
	// Naming the missing piece, and naming it per target, is the whole
	// point: "setup is not complete" alone reads as "nothing is installed"
	// even on a machine whose toolchain and host runtime are both fine and
	// where the only gap is a runtime for the chosen cross target.
	missing := "No Fluxa toolchain was found, and no runtime is registered\n  for " + targetName + " yet."
	switch {
	case haveToolchain && !haveRuntime && registeredForTarget:
		// The confusing case, and the one that used to reach the user as a
		// raw resolver error after they had already confirmed the build: a
		// runtime for this platform is sitting in the registry, it just
		// isn't this project's. Saying which inputs decide that is what
		// makes rebuilding look deliberate rather than redundant.
		missing = "A " + targetName + " runtime is registered, but it does not match this\n" +
			"  project: a registered runtime is only usable with the exact toolchain,\n" +
			"  fluxa.libs, and terminal mode it was built against. A matching pair\n" +
			"  has to be built for this project."
	case haveToolchain && !haveRuntime:
		missing = "A Fluxa toolchain was found, but no runtime is registered for\n  " + targetName + " yet."
	case !haveToolchain && haveRuntime:
		missing = "A runtime for " + targetName + " is registered, but no Fluxa toolchain\n  was found."
	}
	if _, err := fmt.Fprintf(w.stdout, "\n%s\n%s\n\n", w.style.warn("Setup is not complete yet."), w.style.dim("  "+missing)); err != nil {
		return w.fail(err)
	}

	canAcquire := targetOS == "linux" || targetOS == "windows"
	if canAcquire {
		if _, err := fmt.Fprintln(w.stdout, w.style.dim(
			"  Fluxa Builder can clone fluxa-lang and build both the toolchain\n"+
				"  and the "+targetName+" runtime for you inside a pinned Docker\n"+
				"  container, confirming each step first (see docs/adr/0027). This\n"+
				"  is the supported route for this target; the manual guide below\n"+
				"  is the fallback. It can take a while on the first run.")); err != nil {
			return w.fail(err)
		}
	}
	// Defaulted to yes wherever acquisition actually works: for a cross
	// target the manual guide below is not a realistic alternative (it
	// asks for a Windows build from a Linux machine), and every
	// consequential step inside acquisition — building an image, running a
	// container, deleting a cache — is confirmed separately by
	// wizardConfirmer, so a stray Enter here still commits to nothing.
	wantsAuto, err := w.askYesNo("Download and build the Fluxa toolchain and runtime automatically now?", canAcquire)
	if err != nil {
		return w.fail(err)
	}
	if wantsAuto && canAcquire {
		if code, handled := w.autoAcquire(cfg, outputOverride, targetOverride, targetOS, targetArch, identity, haveToolchain); handled {
			return code
		}
		if _, err := fmt.Fprint(w.stdout, "\nFalling back to the manual guide.\n\n"); err != nil {
			return w.fail(err)
		}
	} else if wantsAuto {
		if _, err := fmt.Fprintf(w.stdout,
			"Automatic download/build is not available yet for %s. Here is what to do manually.\n\n", targetOS,
		); err != nil {
			return w.fail(err)
		}
	} else if _, err := fmt.Fprint(w.stdout, "Here is what to do manually.\n\n"); err != nil {
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
	checkoutPath = expandHome(checkoutPath)
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
	if targetOS == "windows" {
		makeCommand = "make build-windows-packaged"
		builtBinaryName = "fluxa-runtime.exe"
	}
	if _, err := fmt.Fprintf(w.stdout,
		"\n  cd %s && %s\n\n%s\n\n",
		makeDir, makeCommand,
		w.style.dim("  Match the optional FLUXA_* backend flags (FLUXA_GRAPH_RAYLIB=1, etc.)\n"+
			"  to what this project's fluxa.libs actually needs; see that repository's\n"+
			"  own documentation and this repo's docs/distribution.md for the full\n"+
			"  list."),
	); err != nil {
		return w.fail(err)
	}
	if targetOS != w.hostOS {
		// The command above produces a binary for targetOS, which this
		// machine is not. Saying so here is the difference between an
		// instruction someone can follow and one that quietly assumes a
		// cross-toolchain they may not have.
		if _, err := fmt.Fprintf(w.stdout, "%s\n\n", w.style.dim(fmt.Sprintf(
			"  Run that on a %s machine, or with a %s cross-toolchain\n"+
				"  installed — this host is %s. The automatic route above does\n"+
				"  exactly this inside a container, which is why it is the\n"+
				"  supported one.",
			targetOS, targetOS, w.hostOS))); err != nil {
			return w.fail(err)
		}
	}

	binaryPath, err := w.askLine(fmt.Sprintf("Path to the %s binary you just built (leave empty to fill in later): ", builtBinaryName))
	if err != nil {
		return w.fail(err)
	}
	binaryPath = expandHome(binaryPath)

	template := runtimepkg.Metadata{
		FormatVersion:        runtimepkg.CurrentMetadataVersion,
		FluxaVersion:         "unreported",
		PackageFormatVersion: 1,
		ProgramFormats:       []string{"fluxa-source"},
		Packaged:             true,
		OS:                   targetOS,
		Arch:                 targetArch,
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
	// The build command printed below carries --target, and does so
	// unconditionally for a non-host target: without it `build` falls back
	// to fluxa.toml's own build.target, so a copy-pasted instruction that
	// omitted it silently produced a different platform's artifacts than
	// the one this whole guide was written for.
	buildHint := fmt.Sprintf("fluxa-builder build %s --include-source", cfg.Root)
	if targetOverride != "" && targetOverride != "host" {
		buildHint += " --target " + targetOverride
	}
	if _, err := fmt.Fprintf(w.stdout,
		"\n%s\n%s\n\n  fluxa-builder runtime add <binary> --metadata %s\n\n%s\n",
		w.style.ok("Wrote a starting point to "+w.style.accent(templatePath)+"."),
		w.style.dim("  Fields that cannot be known yet (bytecode_version, bytecode_abi, and\n"+
			"  binary_name/binary_sha256 if you did not give a binary path above) are\n"+
			"  left empty — fill them in, then:"),
		templatePath,
		w.style.dim("  and re-run fluxa-builder init (or "+buildHint+")\n"+
			"  once the toolchain and runtime are ready."),
	); err != nil {
		return w.fail(err)
	}
	return 0
}

// wizardConfirmer adapts the wizard's existing askYesNo into
// toolchainbuild.Confirmer, so every consequential automatic-acquisition
// step (building/running a container, deleting a cache) goes through the
// same interactive confirmation as everything else in this wizard.
type wizardConfirmer struct{ w *wizard }

// Confirm stops any spinner left animating from a previous confirmed step
// (so its carriage-return redraws never collide with the question being
// printed), asks, and — once confirmed — starts a new spinner labeled with
// the same action text, since that is exactly the work about to run
// silently in the background until the next confirmation or Acquire
// itself returns.
func (c wizardConfirmer) Confirm(action string) (bool, error) {
	c.w.spinner.finish(true)
	ok, err := c.w.askYesNo(action+"?", true)
	if err != nil || !ok {
		return ok, err
	}
	c.w.spinner.start(action)
	return true, nil
}

// autoAcquire drives real automatic toolchain/runtime acquisition
// (docs/adr/0027-automatic-toolchain-acquisition.md) for a linux or
// windows targetOS/targetArch (see resolveTargetOSArch) — acquisition
// itself is Docker-based and works for either target from any host, not
// only a matching one; that is the whole point of docs/adr/0028's
// container-verified cross-platform builds. handled is true when the
// caller should return code directly; handled is false when acquisition
// was declined or hit an unsupported condition and the caller should
// fall back to the manual guide instead.
func (w *wizard) autoAcquire(cfg *project.Config, outputOverride, targetOverride, targetOS, targetArch string, hostIdentity toolchain.Identity, hostHaveToolchain bool) (code int, handled bool) {
	registryRoot, err := buildRegistryRoot("")
	if err != nil {
		return w.fail(err), true
	}
	builderHome, err := builderCacheRoot()
	if err != nil {
		return w.fail(err), true
	}

	result, err := w.deps.acquire(context.Background(), toolchainbuild.Request{
		ProjectRoot: cfg.Root,
		Terminal:    cfg.Build.Terminal,
		TargetOS:    targetOS,
		OutputDir:   filepath.Join(builderHome, "toolchain-built", targetOS),
		CacheRoot:   filepath.Join(builderHome, "toolchain-src"),
	}, wizardConfirmer{w})
	w.spinner.finish(err == nil)
	if err != nil {
		var acquireErr *toolchainbuild.Error
		if errors.As(err, &acquireErr) && (acquireErr.Kind == toolchainbuild.ErrorDeclined || acquireErr.Kind == toolchainbuild.ErrorUnsupported) {
			if _, printErr := fmt.Fprintf(w.stdout, "\nAutomatic acquisition did not run: %s\n", acquireErr.Detail); printErr != nil {
				return w.fail(printErr), true
			}
			return 0, false
		}
		return w.fail(err), true
	}

	// The identity recorded here is not bookkeeping: internal/runtime's
	// compatibility model requires a registered runtime's ToolchainSHA256
	// to exactly match whatever toolchain identity ends up in a package's
	// own manifest at build time, and runBuild always probes
	// cfg.Toolchain.Path — the *local*, host-native compiler, whatever the
	// build's target happens to be. So the only identity worth recording
	// is a host-executable one.
	//
	// result.ToolchainPath alone cannot supply that on a cross-target
	// acquisition: a Windows toolchain built from a Linux host is a PE,
	// which this machine can neither run nor ever probe. Hashing that
	// foreign binary anyway — what this code did before — is worse than
	// unexecutable, it is wrong: a cross-compiled binary and the local
	// compiler are different files by construction, so their hashes can
	// never coincide, and registration "succeeded" only for the very next
	// build to fail with "no verified runtime matches windows/amd64"
	// (reproduced end to end). Acquire now builds a real host-native
	// compiler alongside the target binaries and reports it as
	// HostToolchainPath, so the common case is a genuinely probed
	// identity again rather than a correlation guess.
	//
	// The two fallbacks below stay for hosts Acquire cannot build a native
	// compiler for at all (macOS, still on hold — docs/adr/0027): reuse an
	// already-resolved local toolchain's identity when the user has one,
	// and only otherwise fall back to the cross-compiled binary's own
	// hash. None of this is a full version-compatibility guarantee —
	// fluxa-lang still reports no machine-readable version — but it
	// reflects this project's actual guarantee: the local compiler and
	// this freshly acquired runtime are what this acquisition run paired
	// together. See docs/adr/0028.
	var identity toolchain.Identity
	switch {
	case result.HostToolchainPath != "":
		identity, err = w.deps.probe(context.Background(), result.HostToolchainPath, 10*time.Second)
		if err != nil {
			return w.fail(fmt.Errorf("built a toolchain but could not identify it: %w", err)), true
		}
		if err := w.offerPersistToolchainPath(cfg, result.HostToolchainPath); err != nil {
			return w.fail(err), true
		}
		// offerPersistToolchainPath wrote fluxa.toml on disk; cfg is
		// already loaded in memory and would otherwise still show the
		// old (usually empty) toolchain path when setupOrBuild
		// re-resolves it below.
		cfg.Toolchain.Path = result.HostToolchainPath
	case hostHaveToolchain:
		identity = hostIdentity
	default:
		// No local toolchain exists yet either, so there is nothing to
		// correlate against — record the cross-compiled binary's own
		// hash, the best available fallback, though it will not match a
		// package manifest's own toolchain identity until a local
		// toolchain is resolved too.
		hash, hashErr := hashFile(result.ToolchainPath)
		if hashErr != nil {
			return w.fail(fmt.Errorf("built a toolchain but could not hash it: %w", hashErr)), true
		}
		identity = toolchain.Identity{SHA256: hash}
	}

	librariesHash, err := hashOptionalFile(filepath.Join(cfg.Root, "fluxa.libs"))
	if err != nil {
		return w.fail(err), true
	}
	runtimeHash, err := hashFile(result.RuntimePath)
	if err != nil {
		return w.fail(err), true
	}
	// runtime.Add copies the source binary to a destination named
	// exactly binaryName, regardless of what the source file itself was
	// called — Metadata.Validate requires this fixed convention.
	binaryName := "fluxa-runtime"
	if targetOS == "windows" {
		binaryName += ".exe"
	}
	metadata := runtimepkg.Metadata{
		FormatVersion:        runtimepkg.CurrentMetadataVersion,
		FluxaVersion:         "unreported",
		ToolchainSHA256:      identity.SHA256,
		PackageFormatVersion: 1,
		ProgramFormats:       []string{"fluxa-source"},
		Packaged:             true,
		OS:                   targetOS,
		Arch:                 targetArch,
		Terminal:             cfg.Build.Terminal,
		LibrariesSHA256:      librariesHash,
		BinaryName:           binaryName,
		BinarySHA256:         runtimeHash,
	}
	if identity.Version != "" {
		metadata.FluxaVersion = identity.Version
	}
	if err := w.registerAcquiredRuntime(registryRoot, result.RuntimePath, metadata); err != nil {
		return w.fail(err), true
	}
	if _, err := fmt.Fprintf(w.stdout, "\n%s\n", w.style.ok("Built and registered a Fluxa toolchain and runtime automatically.")); err != nil {
		return w.fail(err), true
	}

	if result.SourceCacheDir != "" || len(result.ImageTags) > 0 {
		remove, err := w.askYesNo("Remove the cached fluxa-lang checkout and Docker image now? (keeping them speeds up future builds)", false)
		if err != nil {
			return w.fail(err), true
		}
		if remove {
			if result.SourceCacheDir != "" {
				_ = os.RemoveAll(result.SourceCacheDir)
			}
			for _, tag := range result.ImageTags {
				_ = toolchainbuild.RemoveImage(context.Background(), tag)
			}
		}
	}

	return w.setupOrBuild(cfg, outputOverride, targetOverride), true
}

// registerAcquiredRuntime adds a freshly built runtime to the registry,
// resolving the one collision that is guaranteed to happen on a second
// project: registry slots are keyed on the *reported* Fluxa version, and
// fluxa-lang reports none, so every runtime built today lands in the same
// "unreported" slot for a given target and terminal mode. The occupant is
// a runtime this project has already been shown to be unable to use —
// setupOrBuild resolved against it and failed, which is the only reason
// acquisition ran at all — so keeping it means the 25 minutes just spent
// rebuilding produce nothing. Replacing it is still a deletion, so it is
// asked, never assumed, and declining leaves the registry untouched.
func (w *wizard) registerAcquiredRuntime(registryRoot, runtimePath string, metadata runtimepkg.Metadata) error {
	_, err := runtimepkg.Add(registryRoot, runtimePath, metadata)
	var addErr *runtimepkg.Error
	if !errors.As(err, &addErr) || addErr.Kind != runtimepkg.ErrorExists {
		return err
	}

	occupant, findErr := findRegisteredSlot(w.deps.listRuntimes, registryRoot, metadata)
	if findErr != nil {
		return findErr
	}
	if _, printErr := fmt.Fprintf(w.stdout, "\n%s\n%s\n",
		w.style.warn("Another "+targetDirectoryName(metadata.OS, metadata.Arch)+" runtime already occupies this registry slot."),
		w.style.dim("  It is the one this project could not use — runtimes are keyed by the\n"+
			"  Fluxa version they report, and fluxa-lang reports none, so every\n"+
			"  build lands in the same slot. Keeping it means the runtime just\n"+
			"  built cannot be registered or used."),
	); printErr != nil {
		return printErr
	}
	replace, askErr := w.askYesNo("Replace the registered runtime with the one just built?", true)
	if askErr != nil {
		return askErr
	}
	if !replace {
		return fmt.Errorf("the built runtime was not registered: %w", err)
	}
	if removeErr := runtimepkg.Remove(registryRoot, occupant); removeErr != nil {
		return removeErr
	}
	_, err = runtimepkg.Add(registryRoot, runtimePath, metadata)
	return err
}

// findRegisteredSlot locates the runtime occupying metadata's slot, so it
// can be removed by identity rather than by a path assembled from the
// registry's internal layout.
func findRegisteredSlot(list func(string) ([]runtimepkg.Runtime, error), registryRoot string, metadata runtimepkg.Metadata) (runtimepkg.Runtime, error) {
	runtimes, err := list(registryRoot)
	if err != nil {
		return runtimepkg.Runtime{}, err
	}
	for _, registered := range runtimes {
		if registered.Metadata.FluxaVersion == metadata.FluxaVersion &&
			registered.Metadata.OS == metadata.OS &&
			registered.Metadata.Arch == metadata.Arch &&
			registered.Metadata.Terminal == metadata.Terminal {
			return registered, nil
		}
	}
	return runtimepkg.Runtime{}, fmt.Errorf("the registry reported an occupied %s slot but no runtime is registered there",
		targetDirectoryName(metadata.OS, metadata.Arch))
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
