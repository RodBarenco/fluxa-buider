package executor_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
)

func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_EXECUTOR_HELPER") == "1" {
		runHelper()
		return
	}
	os.Exit(m.Run())
}

func TestRunSuccessAndLiteralArguments(t *testing.T) {
	t.Parallel()

	args := []string{"argument with spaces", `$(not-a-shell)`, `semi;colon`, `quote"here`}
	result, err := executor.Run(context.Background(), executor.Request{
		Path: os.Args[0],
		Args: args,
		Env:  helperEnv("args"),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	for _, arg := range args {
		if !strings.Contains(result.Stdout, arg) {
			t.Errorf("Stdout = %q, want literal argument %q", result.Stdout, arg)
		}
	}
}

func TestRunCapturesStdoutAndStderr(t *testing.T) {
	t.Parallel()

	result, err := executor.Run(context.Background(), executor.Request{
		Path: os.Args[0],
		Env: append(
			helperEnv("output"),
			"EXECUTOR_HELPER_STDOUT=hello stdout",
			"EXECUTOR_HELPER_STDERR=hello stderr",
		),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Stdout != "hello stdout" {
		t.Errorf("Stdout = %q", result.Stdout)
	}
	if result.Stderr != "hello stderr" {
		t.Errorf("Stderr = %q", result.Stderr)
	}
}

func TestRunPropagatesExitCode(t *testing.T) {
	t.Parallel()

	result, err := executor.Run(context.Background(), executor.Request{
		Path: os.Args[0],
		Env: append(
			helperEnv("exit"),
			"EXECUTOR_HELPER_EXIT=7",
			"EXECUTOR_HELPER_STDERR=deliberate failure",
		),
	})
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
	if result.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", result.ExitCode)
	}
	assertErrorKind(t, err, executor.ErrorExit)
	if !strings.Contains(err.Error(), "deliberate failure") {
		t.Errorf("error = %q, want stderr context", err)
	}
}

func TestRunTimeout(t *testing.T) {
	t.Parallel()

	result, err := executor.Run(context.Background(), executor.Request{
		Path:    os.Args[0],
		Env:     append(helperEnv("delay"), "EXECUTOR_HELPER_DELAY=500ms"),
		Timeout: 20 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("Run() error = nil, want timeout")
	}
	if result.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1", result.ExitCode)
	}
	assertErrorKind(t, err, executor.ErrorTimeout)
}

func TestRunCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := executor.Run(ctx, executor.Request{
		Path: os.Args[0],
		Env:  helperEnv("output"),
	})
	assertErrorKind(t, err, executor.ErrorCanceled)
}

func TestRunHonorsWorkingDirectoryAndEnvironment(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "working directory")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := executor.Run(context.Background(), executor.Request{
		Path: os.Args[0],
		Dir:  dir,
		Env:  append(helperEnv("context"), "CUSTOM_VALUE=preserved exactly"),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(result.Stdout, dir) ||
		!strings.Contains(result.Stdout, "preserved exactly") {
		t.Errorf("Stdout = %q, want dir and environment", result.Stdout)
	}
}

func TestRunLimitsLargeOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		stream     string
		wantStdout bool
	}{
		{name: "stdout", stream: "stdout", wantStdout: true},
		{name: "stderr", stream: "stderr"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := executor.Run(context.Background(), executor.Request{
				Path:      os.Args[0],
				Env:       append(helperEnv("large"), "EXECUTOR_HELPER_STREAM="+tt.stream),
				MaxStdout: 128,
				MaxStderr: 128,
			})
			assertErrorKind(t, err, executor.ErrorOutputLimit)
			if tt.wantStdout {
				if !result.StdoutTruncated || len(result.Stdout) != 128 {
					t.Errorf("stdout truncated=%t len=%d", result.StdoutTruncated, len(result.Stdout))
				}
			} else if !result.StderrTruncated || len(result.Stderr) != 128 {
				t.Errorf("stderr truncated=%t len=%d", result.StderrTruncated, len(result.Stderr))
			}
		})
	}
}

func TestRunRejectsInvalidRequest(t *testing.T) {
	t.Parallel()

	_, err := executor.Run(context.Background(), executor.Request{})
	assertErrorKind(t, err, executor.ErrorInvalidRequest)
}

func helperEnv(mode string) []string {
	return append(os.Environ(),
		"GO_WANT_EXECUTOR_HELPER=1",
		"EXECUTOR_HELPER_MODE="+mode,
	)
}

func assertErrorKind(t *testing.T, err error, want executor.ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want error")
	}
	var executorErr *executor.Error
	if !errors.As(err, &executorErr) {
		t.Fatalf("error type = %T, want *executor.Error", err)
	}
	if executorErr.Kind != want {
		t.Errorf("error kind = %q, want %q; error = %v", executorErr.Kind, want, err)
	}
}

func runHelper() {
	switch os.Getenv("EXECUTOR_HELPER_MODE") {
	case "args":
		fmt.Print(strings.Join(os.Args[1:], "\n"))
	case "output":
		fmt.Print(os.Getenv("EXECUTOR_HELPER_STDOUT"))
		fmt.Fprint(os.Stderr, os.Getenv("EXECUTOR_HELPER_STDERR"))
	case "exit":
		fmt.Fprint(os.Stderr, os.Getenv("EXECUTOR_HELPER_STDERR"))
		code, _ := strconv.Atoi(os.Getenv("EXECUTOR_HELPER_EXIT"))
		os.Exit(code)
	case "delay":
		delay, _ := time.ParseDuration(os.Getenv("EXECUTOR_HELPER_DELAY"))
		time.Sleep(delay)
	case "context":
		dir, _ := os.Getwd()
		fmt.Printf("%s\n%s", dir, os.Getenv("CUSTOM_VALUE"))
	case "large":
		data := strings.Repeat("x", 4096)
		if os.Getenv("EXECUTOR_HELPER_STREAM") == "stderr" {
			fmt.Fprint(os.Stderr, data)
		} else {
			fmt.Print(data)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown helper mode")
		os.Exit(126)
	}
}
