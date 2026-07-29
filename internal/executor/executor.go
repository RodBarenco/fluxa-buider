// Package executor runs external commands without a shell and with bounded IO.
package executor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultTimeout   = 30 * time.Second
	defaultOutputMax = 1024 * 1024
)

// Request describes one direct process execution.
type Request struct {
	Path      string
	Args      []string
	Dir       string
	Env       []string
	Timeout   time.Duration
	MaxStdout int
	MaxStderr int
}

// Result contains bounded process output and termination information.
type Result struct {
	Stdout          string
	Stderr          string
	ExitCode        int
	Duration        time.Duration
	StdoutTruncated bool
	StderrTruncated bool
}

// Run executes a command directly, never through a shell.
func Run(parent context.Context, request Request) (Result, error) {
	if request.Path == "" {
		return Result{ExitCode: -1}, &Error{
			Kind:      ErrorInvalidRequest,
			Operation: "validate",
			Detail:    "executable path is required",
		}
	}
	if request.Timeout < 0 || request.MaxStdout < 0 || request.MaxStderr < 0 {
		return Result{ExitCode: -1}, &Error{
			Kind:      ErrorInvalidRequest,
			Operation: "validate",
			Path:      request.Path,
			Detail:    "timeout and output limits must not be negative",
		}
	}

	timeout := request.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	maxStdout := request.MaxStdout
	if maxStdout == 0 {
		maxStdout = defaultOutputMax
	}
	maxStderr := request.MaxStderr
	if maxStderr == 0 {
		maxStderr = defaultOutputMax
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	stdout := &limitedBuffer{limit: maxStdout}
	stderr := &limitedBuffer{limit: maxStderr}
	// Request.Path is passed as an executable and Args as a literal argv vector.
	cmd := exec.CommandContext(ctx, request.Path, request.Args...) // #nosec G204
	cmd.Dir = request.Dir
	cmd.Env = request.Env
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	started := time.Now()
	runErr := cmd.Run()
	result := Result{
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
		ExitCode:        exitCode(runErr),
		Duration:        time.Since(started),
		StdoutTruncated: stdout.truncated,
		StderrTruncated: stderr.truncated,
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.ExitCode = -1
		return result, &Error{
			Kind:      ErrorTimeout,
			Operation: "execute",
			Path:      request.Path,
			Detail:    fmt.Sprintf("timed out after %s", timeout),
			Err:       ctx.Err(),
		}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		result.ExitCode = -1
		return result, &Error{
			Kind:      ErrorCanceled,
			Operation: "execute",
			Path:      request.Path,
			Detail:    "command canceled",
			Err:       ctx.Err(),
		}
	}
	if stdout.truncated || stderr.truncated {
		return result, &Error{
			Kind:      ErrorOutputLimit,
			Operation: "capture output from",
			Path:      request.Path,
			Detail:    "stdout or stderr exceeded its configured limit",
			Err:       runErr,
		}
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return result, &Error{
				Kind:      ErrorExit,
				Operation: "execute",
				Path:      request.Path,
				ExitCode:  result.ExitCode,
				Detail:    strings.TrimSpace(result.Stderr),
				Err:       runErr,
			}
		}
		return result, &Error{
			Kind:      ErrorStart,
			Operation: "start",
			Path:      request.Path,
			Err:       runErr,
		}
	}

	return result, nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

type limitedBuffer struct {
	data      []byte
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		count := len(data)
		if count > remaining {
			count = remaining
		}
		b.data = append(b.data, data[:count]...)
	}
	if len(data) > remaining {
		b.truncated = true
	}
	return len(data), nil
}

func (b *limitedBuffer) String() string {
	return string(b.data)
}
