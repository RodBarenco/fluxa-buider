package portable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// SmokeExecutable runs the self-test contract for a sibling or embedded package.
func SmokeExecutable(ctx context.Context, executable, directory, expectedPackageHash string, timeout time.Duration) (SmokeReport, error) {
	if executable == "" || directory == "" || len(expectedPackageHash) != 64 {
		return SmokeReport{}, portableError(ErrorInvalid, "validate package self-test request", executable,
			errors.New("executable, directory, and package SHA-256 are required"))
	}
	if timeout <= 0 {
		timeout = defaultSmokeTimeout
	}
	execution, err := executor.Run(ctx, executor.Request{
		Path:      executable,
		Args:      []string{"--fluxa-package-self-test"},
		Dir:       directory,
		Timeout:   timeout,
		MaxStdout: maxSmokeStdout,
		MaxStderr: maxSmokeStderr,
	})
	report := SmokeReport{
		ExitCode: execution.ExitCode,
		Duration: execution.Duration,
		Stdout:   execution.Stdout,
		Stderr:   execution.Stderr,
	}
	if err != nil {
		kind := ErrorSmoke
		var executionError *executor.Error
		if errors.As(err, &executionError) {
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
		detail := err
		if executionError == nil || executionError.Kind != executor.ErrorExit {
			detail = errors.Join(err, textError(execution.Stderr))
		}
		return report, portableError(kind, "run package self-test", executable, detail)
	}

	response, err := decodeSmokeResponse(execution.Stdout)
	if err != nil {
		return report, portableError(ErrorSmokeProtocol, "decode package self-test response", executable, err)
	}
	report.Protocol = response.Protocol
	report.PackageSHA256 = response.PackageSHA256
	report.PackageOpened = response.PackageOpened
	report.VMCompatible = response.VMCompatible
	report.UIOpened = response.UIOpened

	if response.Protocol != smokeProtocol {
		return report, portableError(ErrorSmokeProtocol, "validate package self-test response", executable,
			fmt.Errorf("unsupported protocol %q", response.Protocol))
	}
	if !response.PackageOpened {
		return report, portableError(ErrorSmokeProtocol, "validate package self-test response", executable,
			errors.New("runtime did not confirm that the package was opened"))
	}
	if response.PackageSHA256 != expectedPackageHash {
		return report, portableError(ErrorSmokeProtocol, "validate package self-test response", executable,
			fmt.Errorf("runtime opened package sha256 %q, expected %q", response.PackageSHA256, expectedPackageHash))
	}
	if !response.VMCompatible {
		return report, portableError(ErrorSmokeIncompatible, "validate package self-test response", executable,
			errors.New("runtime reported incompatible VM or package"))
	}
	if response.UIOpened {
		return report, portableError(ErrorSmokeProtocol, "validate package self-test response", executable,
			errors.New("self-test opened the application UI"))
	}
	return report, nil
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
