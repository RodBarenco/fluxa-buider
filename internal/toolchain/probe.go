package toolchain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
)

const maxProbeOutput = 1024 * 1024

var versionLine = regexp.MustCompile(`(?m)^Version:\s*([0-9A-Za-z.+-]+)\s*$`)

// Identity is machine-readable information obtained from a Fluxa executable.
type Identity struct {
	Protocol string
	Version  string
	SHA256   string
}

// Probe runs the offline runtime-info command and hashes the executable.
func Probe(parent context.Context, path string, timeout time.Duration) (Identity, error) {
	result, err := executor.Run(parent, executor.Request{
		Path:      path,
		Args:      []string{"runtime", "info"},
		Timeout:   timeout,
		MaxStdout: maxProbeOutput,
		MaxStderr: maxProbeOutput,
	})
	if err != nil {
		var executorErr *executor.Error
		if errors.As(err, &executorErr) {
			switch executorErr.Kind {
			case executor.ErrorTimeout:
				return Identity{}, &Error{
					Kind:      ErrorTimeout,
					Operation: "probe",
					Path:      path,
					Detail:    fmt.Sprintf("timed out after %s", timeout),
					Err:       err,
				}
			case executor.ErrorCanceled:
				return Identity{}, &Error{
					Kind:      ErrorCanceled,
					Operation: "probe",
					Path:      path,
					Detail:    "probe canceled",
					Err:       err,
				}
			case executor.ErrorOutputLimit:
				return Identity{}, &Error{
					Kind:      ErrorInvalidOutput,
					Operation: "probe",
					Path:      path,
					Detail:    "runtime info output exceeds 1 MiB",
					Err:       err,
				}
			}
		}
		return Identity{}, &Error{
			Kind:      ErrorProbe,
			Operation: "probe",
			Path:      path,
			Detail:    strings.TrimSpace(result.Stderr),
			Err:       err,
		}
	}

	output := result.Stdout
	if !strings.HasPrefix(output, "Fluxa Runtime") {
		return Identity{}, &Error{
			Kind:      ErrorInvalidOutput,
			Operation: "probe",
			Path:      path,
			Detail:    "runtime info did not return the Fluxa Runtime signature",
		}
	}

	hash, err := hashExecutable(path)
	if err != nil {
		return Identity{}, err
	}

	identity := Identity{
		Protocol: "runtime-info-v1",
		SHA256:   hash,
	}
	if matches := versionLine.FindStringSubmatch(output); len(matches) == 2 {
		identity.Version = matches[1]
	}
	return identity, nil
}

// CheckCompatibility compares an optional exact required version.
func CheckCompatibility(required string, identity Identity) error {
	if required == "" {
		return nil
	}
	if identity.Version == "" {
		return &Error{
			Kind:      ErrorCompatibility,
			Operation: "verify",
			Detail:    fmt.Sprintf("project requires Fluxa %s, but this Fluxa CLI does not report a version", required),
		}
	}
	if required != identity.Version {
		return &Error{
			Kind:      ErrorCompatibility,
			Operation: "verify",
			Detail:    fmt.Sprintf("project requires Fluxa %s, found %s", required, identity.Version),
		}
	}
	return nil
}

func hashExecutable(path string) (string, error) {
	// path is the already validated toolchain executable.
	file, err := os.Open(path) // #nosec G304
	if err != nil {
		return "", &Error{
			Kind:      ErrorProbe,
			Operation: "hash",
			Path:      path,
			Err:       err,
		}
	}
	defer func() {
		_ = file.Close()
	}()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", &Error{
			Kind:      ErrorProbe,
			Operation: "hash",
			Path:      path,
			Err:       err,
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
