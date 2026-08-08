package mesafallback

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// archiveURL and archiveSHA256 are the exact release fluxa-lang's own
// docs/WINDOWS.md pins and documents as validated: mesa-dist-win 26.1.3,
// release-mingw, x64. Verified independently here too (downloaded and
// hashed for real) before being hardcoded.
const (
	archiveURL    = "https://github.com/pal1000/mesa-dist-win/releases/download/26.1.3/mesa3d-26.1.3-release-mingw.7z"
	archiveSHA256 = "80d5add64254c839b4c784bdab6a2b504e448675604b0fe54a9bce3c543303a7"

	// maxArchiveSize bounds the download: the real archive is ~57 MB;
	// this is a generous safety cap against a compromised or misredirected
	// download, not a realistic expected size.
	maxArchiveSize = 200 * 1024 * 1024

	downloadTimeout = 5 * time.Minute
)

// DLLNames are the files this package caches, exactly matching what
// docs/WINDOWS.md documents placing beside fluxa-runtime.exe.
// opengl32sw.dll is not a distinct file in the archive — the doc is
// explicit that it "is a second copy of Mesa's opengl32.dll" — so it is
// produced here by duplicating the real opengl32.dll under that name,
// not extracted separately.
var DLLNames = []string{"dxil.dll", "libgallium_wgl.dll", "opengl32.dll", "opengl32sw.dll"}

//go:embed docker/extract.Dockerfile
var extractDockerfile []byte

// EnsureCached returns cacheDir populated with every name in DLLNames,
// downloading, verifying, and extracting the pinned Mesa release into it
// first if it is not already there. The cache is flat and content-keyed
// only by the pinned version constants above, so it is safe to reuse
// across every project and every future build once populated.
//
// Failure here is always ErrorUnsupported: Mesa is optional companion
// distribution (fluxa-lang's own docs/WINDOWS.md says so explicitly), so
// a caller should treat any error as "skip bundling it, warn, keep
// building" rather than aborting a Windows build over a compatibility
// enhancement.
func EnsureCached(ctx context.Context, cacheDir string) (string, error) {
	if alreadyCached(cacheDir) {
		return cacheDir, nil
	}
	if !dockerAvailable(ctx) {
		return "", newError(ErrorUnsupported, "check prerequisites", "Docker is not installed or not reachable", nil)
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", newError(ErrorIO, "create Mesa cache directory", cacheDir, err)
	}

	workDir, err := os.MkdirTemp("", "fluxa-builder-mesa-*")
	if err != nil {
		return "", newError(ErrorIO, "create working directory", "", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	const archiveName = "mesa.7z"
	archivePath := filepath.Join(workDir, archiveName)
	if err := downloadAndVerify(ctx, archiveURL, archiveSHA256, archivePath); err != nil {
		return "", err
	}

	dockerfilePath, contextDir, err := writeEmbeddedDockerfile(extractDockerfile, "extract.Dockerfile")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(contextDir) }()
	if err := dockerBuildExtractImage(ctx, dockerfilePath, contextDir); err != nil {
		return "", err
	}
	if err := extractArchive(ctx, workDir, archiveName); err != nil {
		return "", err
	}

	extractedDir := filepath.Join(workDir, "out")
	found, err := locateX64DLLs(extractedDir)
	if err != nil {
		return "", err
	}
	for name, path := range found {
		if err := copyFile(path, filepath.Join(cacheDir, name)); err != nil {
			return "", newError(ErrorIO, "cache Mesa DLL", name, err)
		}
	}
	// opengl32sw.dll is a second copy of opengl32.dll by name, not a
	// distinct extracted file — see DLLNames' doc comment.
	if err := copyFile(filepath.Join(cacheDir, "opengl32.dll"), filepath.Join(cacheDir, "opengl32sw.dll")); err != nil {
		return "", newError(ErrorIO, "duplicate opengl32.dll as opengl32sw.dll", "", err)
	}

	if !alreadyCached(cacheDir) {
		return "", newError(ErrorUnsupported, "verify Mesa cache", cacheDir, errors.New("expected files are still missing after extraction"))
	}
	return cacheDir, nil
}

func alreadyCached(cacheDir string) bool {
	for _, name := range DLLNames {
		info, err := os.Stat(filepath.Join(cacheDir, name))
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return false
		}
	}
	return true
}

func downloadAndVerify(ctx context.Context, url, wantSHA256, destination string) error {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return newError(ErrorIO, "build download request", url, err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return newError(ErrorUnsupported, "download Mesa archive", url, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return newError(ErrorUnsupported, "download Mesa archive", url, fmt.Errorf("unexpected status %s", response.Status))
	}

	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- destination is a caller-controlled temp path.
	if err != nil {
		return newError(ErrorIO, "create archive file", destination, err)
	}
	defer func() { _ = file.Close() }()

	hasher := sha256.New()
	limited := io.LimitReader(response.Body, maxArchiveSize+1)
	written, err := io.Copy(io.MultiWriter(file, hasher), limited)
	if err != nil {
		return newError(ErrorUnsupported, "download Mesa archive", url, err)
	}
	if written > maxArchiveSize {
		return newError(ErrorUnsupported, "download Mesa archive", url, errors.New("archive exceeds the expected size limit"))
	}
	if got := hex.EncodeToString(hasher.Sum(nil)); got != wantSHA256 {
		return newError(ErrorUnsupported, "verify Mesa archive checksum", url, fmt.Errorf("sha256 = %s, want %s", got, wantSHA256))
	}
	return nil
}

// locateX64DLLs walks the extracted archive tree looking for an "x64"
// directory containing the three real files DLLNames needs (everything
// but the synthesized opengl32sw.dll), tolerant of the archive's exact
// internal layout changing between Mesa releases as long as an "x64"
// directory with these names exists somewhere in it.
func locateX64DLLs(root string) (map[string]string, error) {
	wanted := map[string]bool{"dxil.dll": true, "libgallium_wgl.dll": true, "opengl32.dll": true}
	found := make(map[string]string, len(wanted))
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) != "x64" {
			return nil
		}
		name := entry.Name()
		if wanted[name] {
			found[name] = path
		}
		return nil
	})
	if err != nil {
		return nil, newError(ErrorIO, "scan extracted Mesa archive", root, err)
	}
	for name := range wanted {
		if _, ok := found[name]; !ok {
			return nil, newError(ErrorUnsupported, "locate Mesa files", name, errors.New("not found under an x64/ directory in the extracted archive"))
		}
	}
	return found, nil
}

func copyFile(source, destination string) error {
	input, err := os.Open(source) // #nosec G304 -- source is a path this package itself just extracted.
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec G304 -- destination is the caller-controlled cache directory.
	if err != nil {
		return err
	}
	defer func() { _ = output.Close() }()
	_, err = io.Copy(output, input)
	return err
}

func writeEmbeddedDockerfile(content []byte, name string) (dockerfilePath, contextDir string, err error) {
	contextDir, err = os.MkdirTemp("", "fluxa-builder-mesa-docker-*")
	if err != nil {
		return "", "", newError(ErrorIO, "create Docker build context", "", err)
	}
	dockerfilePath = filepath.Join(contextDir, name)
	if err := os.WriteFile(dockerfilePath, content, 0o600); err != nil {
		_ = os.RemoveAll(contextDir)
		return "", "", newError(ErrorIO, "write Dockerfile", dockerfilePath, err)
	}
	return dockerfilePath, contextDir, nil
}
