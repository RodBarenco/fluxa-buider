package toolchainbuild

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
)

// fluxaLangRepo is the exact URL the project owner gave for automatic
// acquisition — the same one internal/app/init.go's manual guide already
// tells users to clone by hand.
const fluxaLangRepo = "https://github.com/RodBarenco/fluxa-lang"

var errStatNotDirectory = errors.New("path exists and is not a directory")

const gitTimeout = 5 * time.Minute

// ensureSource clones fluxaLangRepo into directory if it is not already a
// checkout there, or fetches and fast-forwards origin/main if it is —
// mirroring the existing persistent runtime registry at
// ~/.fluxa-builder/runtimes rather than a throwaway temp dir, so repeat
// `init` runs reuse and update instead of re-cloning from scratch.
func ensureSource(ctx context.Context, directory string) error {
	info, err := os.Stat(directory)
	switch {
	case err == nil && info.IsDir():
		return updateSource(ctx, directory)
	case err == nil:
		return newError(ErrorIO, "prepare fluxa-lang checkout", directory, errStatNotDirectory)
	case os.IsNotExist(err):
		return cloneSource(ctx, directory)
	default:
		return newError(ErrorIO, "inspect fluxa-lang checkout", directory, err)
	}
}

func cloneSource(ctx context.Context, directory string) error {
	if err := os.MkdirAll(filepath.Dir(directory), 0o700); err != nil {
		return newError(ErrorIO, "create toolchain-src cache", directory, err)
	}
	result, err := executor.Run(ctx, executor.Request{
		Path:      "git",
		Args:      []string{"clone", "--depth", "1", fluxaLangRepo, directory},
		Timeout:   gitTimeout,
		MaxStdout: 1024 * 1024,
		MaxStderr: 1024 * 1024,
	})
	if err != nil {
		return newError(ErrorIO, "clone fluxa-lang", result.Stderr, err)
	}
	return nil
}

func updateSource(ctx context.Context, directory string) error {
	if _, err := executor.Run(ctx, executor.Request{
		Path: "git", Args: []string{"fetch", "--depth", "1", "origin", "main"},
		Dir: directory, Timeout: gitTimeout,
	}); err != nil {
		return newError(ErrorIO, "fetch fluxa-lang updates", directory, err)
	}
	if _, err := executor.Run(ctx, executor.Request{
		Path: "git", Args: []string{"reset", "--hard", "origin/main"},
		Dir: directory, Timeout: gitTimeout,
	}); err != nil {
		return newError(ErrorIO, "update fluxa-lang checkout", directory, err)
	}
	return nil
}
