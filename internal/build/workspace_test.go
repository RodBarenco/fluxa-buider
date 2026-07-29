package build_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	buildpkg "github.com/RodBarenco/fluxa-builder/internal/build"
)

func TestNewWorkspaceCreatesIsolatedLayout(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	workspace, err := buildpkg.NewWorkspace(context.Background(), projectRoot, buildpkg.WorkspaceOptions{
		IDGenerator: fixedIDs("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		_ = workspace.Cleanup()
	})

	if workspace.ID != "0123456789abcdef0123456789abcdef" {
		t.Errorf("ID = %q", workspace.ID)
	}
	wantRoot := filepath.Join(projectRoot, ".fluxa-builder", "work", workspace.ID)
	if workspace.Root != wantRoot {
		t.Errorf("Root = %q, want %q", workspace.Root, wantRoot)
	}

	directories := []string{
		workspace.Root,
		workspace.CompiledDir,
		workspace.PackageDir,
		workspace.RuntimeDir,
		workspace.OutputDir,
		workspace.ReportDir,
	}
	for _, directory := range directories {
		info, err := os.Stat(directory)
		if err != nil {
			t.Errorf("Stat(%q) error = %v", directory, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%q is not a directory", directory)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%q permissions = %o, want no group/other access", directory, info.Mode().Perm())
		}
	}
}

func TestWorkspaceCleanup(t *testing.T) {
	t.Parallel()

	workspace, err := buildpkg.NewWorkspace(context.Background(), t.TempDir(), buildpkg.WorkspaceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(workspace.CompiledDir, "partial.bin"), "partial")

	if err := workspace.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if _, err := os.Stat(workspace.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace still exists or unexpected error: %v", err)
	}
	if err := workspace.Cleanup(); err != nil {
		t.Fatalf("second Cleanup() error = %v", err)
	}
}

func TestWorkspaceKeepWork(t *testing.T) {
	t.Parallel()

	workspace, err := buildpkg.NewWorkspace(context.Background(), t.TempDir(), buildpkg.WorkspaceOptions{
		KeepWork: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if _, err := os.Stat(workspace.Root); err != nil {
		t.Fatalf("kept workspace missing: %v", err)
	}
	if err := os.RemoveAll(workspace.Root); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceCleanupRejectsReplacedWorkDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}

	projectRoot := t.TempDir()
	workspace, err := buildpkg.NewWorkspace(context.Background(), projectRoot, buildpkg.WorkspaceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	workBase := filepath.Dir(workspace.Root)
	originalBase := workBase + "-original"
	if err := os.Rename(workBase, originalBase); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, workBase); err != nil {
		t.Fatal(err)
	}
	outsideWorkspace := filepath.Join(outside, workspace.ID)
	mustWriteFile(t, filepath.Join(outsideWorkspace, "must-survive"), "safe")

	err = workspace.Cleanup()
	assertBuildErrorKind(t, err, buildpkg.ErrorUnsafePath)
	if _, err := os.Stat(filepath.Join(outsideWorkspace, "must-survive")); err != nil {
		t.Fatalf("cleanup followed replaced symlink: %v", err)
	}
	if err := os.RemoveAll(originalBase); err != nil {
		t.Fatal(err)
	}
}

func TestNewWorkspaceRetriesIDCollision(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	collidingID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	nextID := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	existing := filepath.Join(projectRoot, ".fluxa-builder", "work", collidingID)
	if err := os.MkdirAll(existing, 0o700); err != nil {
		t.Fatal(err)
	}

	workspace, err := buildpkg.NewWorkspace(context.Background(), projectRoot, buildpkg.WorkspaceOptions{
		IDGenerator: fixedIDs(collidingID, nextID),
	})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		_ = workspace.Cleanup()
	})
	if workspace.ID != nextID {
		t.Errorf("ID = %q, want %q", workspace.ID, nextID)
	}
}

func TestNewWorkspaceStopsAfterRepeatedCollisions(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := os.MkdirAll(filepath.Join(projectRoot, ".fluxa-builder", "work", id), 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := buildpkg.NewWorkspace(context.Background(), projectRoot, buildpkg.WorkspaceOptions{
		IDGenerator: fixedIDs(id, id, id),
		MaxAttempts: 3,
	})
	assertBuildErrorKind(t, err, buildpkg.ErrorCollision)
}

func TestNewWorkspaceHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := buildpkg.NewWorkspace(ctx, t.TempDir(), buildpkg.WorkspaceOptions{})
	assertBuildErrorKind(t, err, buildpkg.ErrorCanceled)
}

func TestNewWorkspaceRejectsSymlinkedControlDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	t.Parallel()

	projectRoot := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(projectRoot, ".fluxa-builder")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err := buildpkg.NewWorkspace(context.Background(), projectRoot, buildpkg.WorkspaceOptions{})
	assertBuildErrorKind(t, err, buildpkg.ErrorUnsafePath)
}

func TestPublishAtomicallyMovesCompletedArtifact(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	workspace, err := buildpkg.NewWorkspace(context.Background(), projectRoot, buildpkg.WorkspaceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = workspace.Cleanup()
	})

	source := filepath.Join(workspace.OutputDir, "MyApp")
	mustWriteFile(t, filepath.Join(source, "MyApp.flxpkg"), "complete")
	destination := filepath.Join(projectRoot, "dist", "linux-x64", "MyApp")

	if err := workspace.Publish(context.Background(), source, destination); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("source still exists: %v", err)
	}
	// destination is a test-owned temporary directory.
	content, err := os.ReadFile(filepath.Join(destination, "MyApp.flxpkg")) // #nosec G304
	if err != nil {
		t.Fatalf("published artifact missing: %v", err)
	}
	if string(content) != "complete" {
		t.Errorf("published content = %q", content)
	}
}

func TestPublishRejectsExistingOutputWithoutChangingEitherTree(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	workspace, err := buildpkg.NewWorkspace(context.Background(), projectRoot, buildpkg.WorkspaceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = workspace.Cleanup()
	})

	source := filepath.Join(workspace.OutputDir, "MyApp")
	destination := filepath.Join(projectRoot, "dist", "MyApp")
	mustWriteFile(t, filepath.Join(source, "new.txt"), "new")
	mustWriteFile(t, filepath.Join(destination, "old.txt"), "old")

	err = workspace.Publish(context.Background(), source, destination)
	assertBuildErrorKind(t, err, buildpkg.ErrorOutputExists)
	if _, err := os.Stat(filepath.Join(source, "new.txt")); err != nil {
		t.Errorf("source changed after rejected publish: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "old.txt")); err != nil {
		t.Errorf("destination changed after rejected publish: %v", err)
	}
}

func TestPublishRejectsSourceOutsideWorkspace(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	workspace, err := buildpkg.NewWorkspace(context.Background(), projectRoot, buildpkg.WorkspaceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = workspace.Cleanup()
	})

	outside := t.TempDir()
	err = workspace.Publish(context.Background(), outside, filepath.Join(projectRoot, "dist", "bad"))
	assertBuildErrorKind(t, err, buildpkg.ErrorUnsafePath)
}

func TestPublishHonorsCancellation(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	workspace, err := buildpkg.NewWorkspace(context.Background(), projectRoot, buildpkg.WorkspaceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = workspace.Cleanup()
	})
	source := filepath.Join(workspace.OutputDir, "MyApp")
	mustWriteFile(t, filepath.Join(source, "file"), "data")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = workspace.Publish(ctx, source, filepath.Join(projectRoot, "dist", "MyApp"))
	assertBuildErrorKind(t, err, buildpkg.ErrorCanceled)
}

func fixedIDs(ids ...string) func() (string, error) {
	index := 0
	return func() (string, error) {
		if index >= len(ids) {
			return "", errors.New("no more IDs")
		}
		id := ids[index]
		index++
		return id, nil
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertBuildErrorKind(t *testing.T, err error, want buildpkg.ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want error")
	}
	var buildErr *buildpkg.Error
	if !errors.As(err, &buildErr) {
		t.Fatalf("error type = %T, want *build.Error", err)
	}
	if buildErr.Kind != want {
		t.Errorf("error kind = %q, want %q; error = %v", buildErr.Kind, want, err)
	}
}
