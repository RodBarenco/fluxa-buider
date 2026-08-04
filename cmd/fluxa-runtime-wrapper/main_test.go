package main_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
	"github.com/RodBarenco/fluxa-builder/internal/runtimeprotocol"
)

var (
	wrapperPath     string
	interpreterPath string
)

// TestMain builds the real wrapper binary and a fake "interpreter" once,
// laid out exactly like internal/portable assembles a Linux portable
// directory (wrapper + a sibling .fluxa-runtime.interpreter), so every test
// below exercises the actual compiled relay as a subprocess rather than its
// Go source directly.
func TestMain(m *testing.M) {
	workDir, err := os.MkdirTemp("", "fluxa-runtime-wrapper-test-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	wrapperPath = filepath.Join(workDir, "fluxa-runtime-wrapper")
	if _, err := executor.Run(context.Background(), executor.Request{
		Path: "go", Args: []string{"build", "-o", wrapperPath, "."}, Timeout: 2 * time.Minute,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "build wrapper:", err)
		os.Exit(1)
	}

	interpreterSource := filepath.Join(workDir, "fake_interpreter.go")
	if err := os.WriteFile(interpreterSource, []byte(fakeInterpreterSource), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	interpreterPath = filepath.Join(workDir, ".fluxa-runtime.interpreter")
	if _, err := executor.Run(context.Background(), executor.Request{
		Path: "go", Args: []string{"build", "-o", interpreterPath, interpreterSource}, Timeout: 2 * time.Minute,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "build fake interpreter:", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// fakeInterpreterSource stands in for the real Fluxa interpreter: it prints
// the arguments it received and its own working directory, then exits with
// FAKE_INTERPRETER_EXIT_CODE (default 0), so tests can assert both correct
// argv translation and exit-code propagation without a real fluxa binary.
const fakeInterpreterSource = `package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func main() {
	wd, _ := os.Getwd()
	fmt.Println("argv:" + strings.Join(os.Args[1:], "|"))
	fmt.Println("cwd:" + wd)
	code, _ := strconv.Atoi(os.Getenv("FAKE_INTERPRETER_EXIT_CODE"))
	os.Exit(code)
}
`

// runWrapper always returns the process Result, whose ExitCode is what
// every test case below asserts on. executor.Run also returns a non-nil
// error whenever the process exits non-zero (its Kind distinguishes that
// from a start/timeout failure), which every case here expects, so the
// error itself is intentionally not treated as fatal.
func runWrapper(t *testing.T, dir string, args []string, env []string) executor.Result {
	t.Helper()
	result, _ := executor.Run(context.Background(), executor.Request{
		Path: wrapperPath, Args: args, Dir: dir, Env: env, Timeout: 10 * time.Second,
	})
	return result
}

func validEnv() []string {
	return []string{runtimeprotocol.AuthEnvVar + "=" + runtimeprotocol.AuthValue}
}

func TestWrapperRelaysValidCall(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	result := runWrapper(t, projectDir,
		[]string{runtimeprotocol.Command, "main.flx", projectDir}, validEnv())

	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "argv:run|main.flx|-proj|.") {
		t.Errorf("stdout = %q, want relayed run/-proj argv", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "cwd:"+projectDir) {
		t.Errorf("stdout = %q, want cwd %q", result.Stdout, projectDir)
	}
}

func TestWrapperPropagatesInterpreterExitCode(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	env := append(validEnv(), "FAKE_INTERPRETER_EXIT_CODE=7")
	result := runWrapper(t, projectDir,
		[]string{runtimeprotocol.Command, "main.flx", projectDir}, env)

	if result.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
}

func TestWrapperRefusesInvalidCalls(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	cases := []struct {
		name string
		args []string
		env  []string
	}{
		{"no arguments", nil, validEnv()},
		{"missing auth", []string{runtimeprotocol.Command, "main.flx", projectDir}, nil},
		{"wrong auth", []string{runtimeprotocol.Command, "main.flx", projectDir}, []string{runtimeprotocol.AuthEnvVar + "=wrong"}},
		{"wrong command", []string{"run", "main.flx", projectDir}, validEnv()},
		{"missing entry", []string{runtimeprotocol.Command, "", projectDir}, validEnv()},
		{"missing project root", []string{runtimeprotocol.Command, "main.flx", ""}, validEnv()},
		{"too few arguments", []string{runtimeprotocol.Command, "main.flx"}, validEnv()},
		{"too many arguments", []string{runtimeprotocol.Command, "main.flx", projectDir, "extra"}, validEnv()},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := runWrapper(t, projectDir, tt.args, tt.env)
			if result.ExitCode != 126 {
				t.Fatalf("exit code = %d, want 126; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
			}
			if strings.Contains(result.Stdout, "argv:") {
				t.Errorf("stdout = %q, interpreter must not have been invoked", result.Stdout)
			}
		})
	}
}
