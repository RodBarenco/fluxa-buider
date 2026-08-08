package portable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
)

const (
	defaultSmokeTimeout = 10 * time.Second
	maxSmokeStdout      = 64 * 1024
	maxSmokeStderr      = 1024 * 1024
	smokeProtocol       = "fluxa-package-self-test-v1"
)

// SmokeReport is the bounded diagnostic record returned by a package self-test.
type SmokeReport struct {
	Protocol      string        `json:"protocol"`
	PackageSHA256 string        `json:"package_sha256"`
	PackageOpened bool          `json:"package_opened"`
	VMCompatible  bool          `json:"vm_compatible"`
	UIOpened      bool          `json:"ui_opened"`
	ExitCode      int           `json:"exit_code"`
	Duration      time.Duration `json:"duration"`
	Stdout        string        `json:"stdout,omitempty"`
	Stderr        string        `json:"stderr,omitempty"`
}

type smokeResponse struct {
	Protocol      string `json:"protocol"`
	PackageSHA256 string `json:"package_sha256"`
	PackageOpened bool   `json:"package_opened"`
	VMCompatible  bool   `json:"vm_compatible"`
	UIOpened      bool   `json:"ui_opened"`
}

// Smoke executes the runtime's non-interactive package self-test contract.
func Smoke(ctx context.Context, result Result, timeout time.Duration) error {
	_, err := SmokeDetailed(ctx, result, timeout)
	return err
}

// SmokeDetailed runs the self-test and returns its bounded output even on failure.
func SmokeDetailed(ctx context.Context, result Result, timeout time.Duration) (SmokeReport, error) {
	packageInfo, err := flxpkg.Verify(result.Package)
	if err != nil {
		return SmokeReport{}, portableError(ErrorIntegrity, "pre-smoke package verification", result.Package, err)
	}
	directory := result.Directory
	if result.TargetOS == "macos" {
		directory = filepath.Dir(result.Package)
	}
	return SmokeExecutable(ctx, result.Executable, directory, packageInfo.SHA256, timeout)
}

// SmokeExecutable runs the self-test contract for a sibling or embedded
// package. The self-test always runs against a disposable, hard-linked
// copy of directory, never directory itself: a produced application's own
// first-run behavior is outside this project's control, and anything it
// happens to write (a generated key, a config file, a save slot) must
// never leak into the exact directory that gets archived and published
// moments later. See docs/adr/0028.
func SmokeExecutable(ctx context.Context, executable, directory, expectedPackageHash string, timeout time.Duration) (SmokeReport, error) {
	if executable == "" || directory == "" || len(expectedPackageHash) != 64 {
		return SmokeReport{}, portableError(ErrorInvalid, "validate package self-test request", executable,
			errors.New("executable, directory, and package SHA-256 are required"))
	}

	isolatedDirectory, isolatedExecutable, cleanup, err := isolateForSmoke(ctx, directory, executable)
	if err != nil {
		return SmokeReport{}, portableError(ErrorIO, "isolate package self-test directory", directory, err)
	}
	defer cleanup()

	if timeout <= 0 {
		timeout = defaultSmokeTimeout
	}
	execution, runErr := executor.Run(ctx, executor.Request{
		Path:      isolatedExecutable,
		Args:      []string{"--fluxa-package-self-test"},
		Dir:       isolatedDirectory,
		Timeout:   timeout,
		MaxStdout: maxSmokeStdout,
		MaxStderr: maxSmokeStderr,
	})
	return ValidateSmokeExecution(execution, runErr, executable, expectedPackageHash)
}

// ValidateSmokeExecution interprets one already-completed self-test process
// execution (native or, via a ContainerRunner, containerized) against the
// self-test contract: exactly one JSON response document on stdout,
// confirming the expected package was opened, the runtime reports
// VM-compatible, and the UI was never actually opened. executablePath is
// used only to label errors, matching what SmokeExecutable already
// reported before this validation was extracted out of it.
func ValidateSmokeExecution(execution executor.Result, runErr error, executablePath, expectedPackageHash string) (SmokeReport, error) {
	report := SmokeReport{
		ExitCode: execution.ExitCode,
		Duration: execution.Duration,
		Stdout:   execution.Stdout,
		Stderr:   execution.Stderr,
	}
	if runErr != nil {
		kind := ErrorSmoke
		var executionError *executor.Error
		if errors.As(runErr, &executionError) {
			switch executionError.Kind {
			case executor.ErrorTimeout:
				kind = ErrorSmokeTimeout
			case executor.ErrorCanceled:
				kind = ErrorCanceled
			case executor.ErrorOutputLimit:
				kind = ErrorSmokeProtocol
			case executor.ErrorExit:
				if execution.ExitCode < 0 {
					kind = ErrorSmokeCrash
				}
			}
		}
		detail := runErr
		if executionError == nil || executionError.Kind != executor.ErrorExit {
			detail = errors.Join(runErr, textError(execution.Stderr))
		}
		return report, portableError(kind, "run package self-test", executablePath, detail)
	}

	response, err := decodeSmokeResponse(execution.Stdout)
	if err != nil {
		return report, portableError(ErrorSmokeProtocol, "decode package self-test response", executablePath, err)
	}
	report.Protocol = response.Protocol
	report.PackageSHA256 = response.PackageSHA256
	report.PackageOpened = response.PackageOpened
	report.VMCompatible = response.VMCompatible
	report.UIOpened = response.UIOpened

	if response.Protocol != smokeProtocol {
		return report, portableError(ErrorSmokeProtocol, "validate package self-test response", executablePath,
			fmt.Errorf("unsupported protocol %q", response.Protocol))
	}
	if !response.PackageOpened {
		return report, portableError(ErrorSmokeProtocol, "validate package self-test response", executablePath,
			errors.New("runtime did not confirm that the package was opened"))
	}
	if response.PackageSHA256 != expectedPackageHash {
		return report, portableError(ErrorSmokeProtocol, "validate package self-test response", executablePath,
			fmt.Errorf("runtime opened package sha256 %q, expected %q", response.PackageSHA256, expectedPackageHash))
	}
	if !response.VMCompatible {
		return report, portableError(ErrorSmokeIncompatible, "validate package self-test response", executablePath,
			errors.New("runtime reported incompatible VM or package"))
	}
	if response.UIOpened {
		return report, portableError(ErrorSmokeProtocol, "validate package self-test response", executablePath,
			errors.New("self-test opened the application UI"))
	}
	return report, nil
}

// ContainerRunner executes executable inside directory using some isolated,
// non-native mechanism (typically a Docker container, e.g.
// internal/containersmoke.RunWindows/RunLinux) and returns the same
// bounded-output shape internal/executor already produces, so
// ValidateSmokeExecution interprets native and containerized runs
// identically. internal/portable never imports anything Docker- or
// network-related itself (see docs/adr/0027's "pure, no-network"
// principle for this package) — the caller supplies how to run.
type ContainerRunner func(ctx context.Context, executable, directory string, timeout time.Duration) (executor.Result, error)

// SmokeContainer is SmokeContainerDetailed discarding its report.
func SmokeContainer(ctx context.Context, result Result, run ContainerRunner, timeout time.Duration) error {
	_, err := SmokeContainerDetailed(ctx, result, run, timeout)
	return err
}

// SmokeContainerDetailed is SmokeDetailed's containerized counterpart:
// same package pre-verification and directory resolution, but the actual
// self-test process runs via run instead of natively.
func SmokeContainerDetailed(ctx context.Context, result Result, run ContainerRunner, timeout time.Duration) (SmokeReport, error) {
	packageInfo, err := flxpkg.Verify(result.Package)
	if err != nil {
		return SmokeReport{}, portableError(ErrorIntegrity, "pre-smoke package verification", result.Package, err)
	}
	directory := result.Directory
	if result.TargetOS == "macos" {
		directory = filepath.Dir(result.Package)
	}
	return SmokeExecutableContainer(ctx, result.Executable, directory, packageInfo.SHA256, run, timeout)
}

// SmokeExecutableContainer is SmokeExecutable's containerized counterpart:
// the self-test still always runs against a disposable, isolated copy of
// directory (never directory itself), but the process itself is started
// by run rather than internal/executor directly.
func SmokeExecutableContainer(ctx context.Context, executable, directory, expectedPackageHash string, run ContainerRunner, timeout time.Duration) (SmokeReport, error) {
	if executable == "" || directory == "" || len(expectedPackageHash) != 64 {
		return SmokeReport{}, portableError(ErrorInvalid, "validate package self-test request", executable,
			errors.New("executable, directory, and package SHA-256 are required"))
	}
	if run == nil {
		return SmokeReport{}, portableError(ErrorInvalid, "validate package self-test request", executable,
			errors.New("a container runner is required"))
	}

	isolatedDirectory, isolatedExecutable, cleanup, err := isolateForSmoke(ctx, directory, executable)
	if err != nil {
		return SmokeReport{}, portableError(ErrorIO, "isolate package self-test directory", directory, err)
	}
	defer cleanup()

	if timeout <= 0 {
		timeout = defaultSmokeTimeout
	}
	execution, runErr := run(ctx, isolatedExecutable, isolatedDirectory, timeout)
	return ValidateSmokeExecution(execution, runErr, executable, expectedPackageHash)
}

// isolateForSmoke recreates directory (and, within it, executable) inside a
// fresh temp directory created alongside directory — guaranteeing the same
// filesystem, which matters for the hard-link fast path below, and giving
// the isolated copy a safety net: if the returned cleanup somehow never
// runs, it is still swept up by the workspace's own eventual full cleanup,
// since it lives next to directory rather than in the system temp root.
//
// Every entry is hard-linked when possible — directory can contain a
// multi-hundred-MB .flxpkg, and only files the self-test itself creates
// need a distinct inode — falling back to a real, permission-preserving
// copy when hard-linking isn't possible (crossing a filesystem boundary).
// Symlinks (macOS .app bundles can contain these) are always recreated as
// symlinks, never linked or followed.
func isolateForSmoke(ctx context.Context, directory, executable string) (isolatedDirectory, isolatedExecutable string, cleanup func(), err error) {
	relativeExecutable, err := filepath.Rel(directory, executable)
	if err != nil || strings.HasPrefix(relativeExecutable, "..") {
		return "", "", nil, fmt.Errorf("executable %q is not inside directory %q", executable, directory)
	}

	parent := filepath.Dir(directory)
	isolatedDirectory, err = os.MkdirTemp(parent, "fluxa-builder-smoke-*")
	if err != nil {
		return "", "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(isolatedDirectory) }

	if err := isolateTree(ctx, directory, isolatedDirectory); err != nil {
		cleanup()
		return "", "", nil, err
	}
	return isolatedDirectory, filepath.Join(isolatedDirectory, relativeExecutable), cleanup, nil
}

// isolateTree recreates every entry directly inside source as a sibling
// inside destination, which the caller must already have created.
func isolateTree(ctx context.Context, source, destination string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		sourcePath := filepath.Join(source, entry.Name())
		destinationPath := filepath.Join(destination, entry.Name())
		info, err := os.Lstat(sourcePath)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(sourcePath)
			if err != nil {
				return err
			}
			if err := os.Symlink(target, destinationPath); err != nil {
				return err
			}
		case info.IsDir():
			if err := os.Mkdir(destinationPath, info.Mode().Perm()); err != nil {
				return err
			}
			if err := isolateTree(ctx, sourcePath, destinationPath); err != nil {
				return err
			}
		default:
			if linkErr := os.Link(sourcePath, destinationPath); linkErr != nil {
				if _, err := copyAndHash(ctx, sourcePath, destinationPath, info.Mode()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func decodeSmokeResponse(output string) (smokeResponse, error) {
	var response smokeResponse
	decoder := json.NewDecoder(bytes.NewBufferString(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return response, fmt.Errorf("invalid JSON response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return response, errors.New("stdout must contain exactly one JSON document")
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &fields); err != nil {
		return response, err
	}
	for _, field := range []string{"protocol", "package_sha256", "package_opened", "vm_compatible", "ui_opened"} {
		if _, ok := fields[field]; !ok {
			return response, fmt.Errorf("required field %q is missing", field)
		}
	}
	return response, nil
}

func textError(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return errors.New(text)
}
