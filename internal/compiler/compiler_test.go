package compiler_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RodBarenco/fluxa-builder/internal/collector"
	"github.com/RodBarenco/fluxa-builder/internal/compiler"
)

func TestCompileStagesMinimalDevelopmentSource(t *testing.T) {
	root := t.TempDir()
	output := makeOutput(t)
	entry := writeSource(t, root, "main.flx", `print("ok")`, collector.KindEntry)

	result, err := compiler.Compile(context.Background(), compiler.Request{
		Files: []collector.Entry{entry}, OutputDir: output, IncludeSource: true,
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.Format != compiler.FormatSource || !result.Debug || !result.SourceExposed || result.BytecodeABI != "" {
		t.Fatalf("Compile() result = %#v, want exposed debug source without bytecode ABI", result)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].Path != "source/main.flx" ||
		result.Artifacts[0].LogicalPath != "main.flx" {
		t.Fatalf("artifacts = %#v", result.Artifacts)
	}
	data, err := os.ReadFile(filepath.Join(output, "source", "main.flx")) // #nosec G304 -- test-controlled temporary path.
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `print("ok")` {
		t.Fatalf("staged source = %q", data)
	}
}

func TestCompileStagesMultipleModulesSortedAndSkipsAssets(t *testing.T) {
	root := t.TempDir()
	output := makeOutput(t)
	files := []collector.Entry{
		writeSource(t, root, "static/z.flx", "z", collector.KindModule),
		writeSource(t, root, "assets/image.png", "png", collector.KindAsset),
		writeSource(t, root, "main.flx", "main", collector.KindEntry),
		writeSource(t, root, "live/a.flx", "a", collector.KindModule),
	}

	result, err := compiler.Compile(context.Background(), compiler.Request{
		Files: files, OutputDir: output, IncludeSource: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(result.Artifacts))
	for _, artifact := range result.Artifacts {
		got = append(got, artifact.Path)
		if len(artifact.SHA256) != 64 {
			t.Errorf("SHA-256 = %q", artifact.SHA256)
		}
	}
	want := []string{"source/live/a.flx", "source/main.flx", "source/static/z.flx"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("artifact paths = %#v, want %#v", got, want)
	}
	if _, err := os.Stat(filepath.Join(output, "source", "assets", "image.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("asset unexpectedly staged by compiler: %v", err)
	}
}

func TestCompileReleaseIsBlocked(t *testing.T) {
	_, err := compiler.Compile(context.Background(), compiler.Request{
		OutputDir: makeOutput(t),
	})
	assertKind(t, err, compiler.ErrorUnavailable)
	if !strings.Contains(err.Error(), "--include-source") {
		t.Fatalf("error = %q, want explicit fallback guidance", err)
	}
}

func TestCompileRejectsMissingOrUnsafeOutput(t *testing.T) {
	root := t.TempDir()
	entry := writeSource(t, root, "main.flx", "main", collector.KindEntry)

	_, err := compiler.Compile(context.Background(), compiler.Request{
		Files: []collector.Entry{entry}, OutputDir: filepath.Join(root, "missing"), IncludeSource: true,
	})
	assertKind(t, err, compiler.ErrorIO)

	fileOutput := filepath.Join(root, "output-file")
	if err := os.WriteFile(fileOutput, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = compiler.Compile(context.Background(), compiler.Request{
		Files: []collector.Entry{entry}, OutputDir: fileOutput, IncludeSource: true,
	})
	assertKind(t, err, compiler.ErrorUnsafePath)
}

func TestCompileRejectsEmptySourcesAndChangedInput(t *testing.T) {
	output := makeOutput(t)
	_, err := compiler.Compile(context.Background(), compiler.Request{
		OutputDir: output, IncludeSource: true,
	})
	assertKind(t, err, compiler.ErrorInvalidInput)

	root := t.TempDir()
	entry := writeSource(t, root, "main.flx", "main", collector.KindEntry)
	entry.Size++
	_, err = compiler.Compile(context.Background(), compiler.Request{
		Files: []collector.Entry{entry}, OutputDir: makeOutput(t), IncludeSource: true,
	})
	assertKind(t, err, compiler.ErrorInvalidInput)
}

func TestCompileRejectsInvalidProgramExtensionAndDuplicateOutput(t *testing.T) {
	root := t.TempDir()
	output := makeOutput(t)
	invalid := writeSource(t, root, "main.txt", "main", collector.KindEntry)
	_, err := compiler.Compile(context.Background(), compiler.Request{
		Files: []collector.Entry{invalid}, OutputDir: output, IncludeSource: true,
	})
	assertKind(t, err, compiler.ErrorInvalidInput)

	entry := writeSource(t, root, "main.flx", "main", collector.KindEntry)
	request := compiler.Request{Files: []collector.Entry{entry}, OutputDir: output, IncludeSource: true}
	if _, err := compiler.Compile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	_, err = compiler.Compile(context.Background(), request)
	assertKind(t, err, compiler.ErrorIO)
}

func TestCompileCancellation(t *testing.T) {
	root := t.TempDir()
	entry := writeSource(t, root, "main.flx", "main", collector.KindEntry)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := compiler.Compile(ctx, compiler.Request{
		Files: []collector.Entry{entry}, OutputDir: makeOutput(t), IncludeSource: true,
	})
	assertKind(t, err, compiler.ErrorCanceled)
}

func makeOutput(t *testing.T) string {
	t.Helper()
	output := filepath.Join(t.TempDir(), "compiled")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	return output
}

func writeSource(t *testing.T, root, name, contents string, kind collector.Kind) collector.Entry {
	t.Helper()
	source := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return collector.Entry{Path: name, SourcePath: source, Kind: kind, Size: int64(len(contents))}
}

func assertKind(t *testing.T, err error, want compiler.ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %q", want)
	}
	var compilerError *compiler.Error
	if !errors.As(err, &compilerError) {
		t.Fatalf("error type = %T, want *compiler.Error: %v", err, err)
	}
	if compilerError.Kind != want {
		t.Fatalf("error kind = %q, want %q: %v", compilerError.Kind, want, err)
	}
}
