package containersmoke

import (
	"errors"
	"path/filepath"
	"strings"
)

// containerExecutablePath resolves executable's location once directory is
// bind-mounted at /work inside the container: a POSIX-style path relative
// to /work, always using forward slashes regardless of host OS.
func containerExecutablePath(directory, executable string) (string, error) {
	relative, err := filepath.Rel(directory, executable)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("executable is not inside directory")
	}
	return "/work/" + filepath.ToSlash(relative), nil
}
