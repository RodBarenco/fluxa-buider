package portable

import (
	"context"
	"errors"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
)

const (
	defaultSmokeTimeout = 10 * time.Second
	maxSmokeOutput      = 1024 * 1024
)

// Smoke executes the runtime's non-interactive package self-test contract.
func Smoke(ctx context.Context, result Result, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = defaultSmokeTimeout
	}
	if _, err := flxpkg.Verify(result.Package); err != nil {
		return portableError(ErrorIntegrity, "pre-smoke package verification", result.Package, err)
	}
	execution, err := executor.Run(ctx, executor.Request{
		Path:      result.Executable,
		Args:      []string{"--fluxa-package-self-test"},
		Dir:       result.Directory,
		Timeout:   timeout,
		MaxStdout: maxSmokeOutput,
		MaxStderr: maxSmokeOutput,
	})
	if err != nil {
		return portableError(ErrorSmoke, "run package self-test", result.Executable, errors.Join(err, errors.New(execution.Stderr)))
	}
	return nil
}
