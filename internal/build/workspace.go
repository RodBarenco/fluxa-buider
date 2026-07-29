package build

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const defaultCreateAttempts = 10

var workspaceIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// WorkspaceOptions controls allocation and debug retention.
type WorkspaceOptions struct {
	KeepWork    bool
	MaxAttempts int
	IDGenerator func() (string, error)
}

// Workspace is one isolated build transaction.
type Workspace struct {
	ID          string
	Root        string
	CompiledDir string
	PackageDir  string
	RuntimeDir  string
	OutputDir   string
	ReportDir   string

	projectRoot string
	workBase    string
	keepWork    bool
}

// NewWorkspace creates a new isolated build directory.
func NewWorkspace(ctx context.Context, projectRoot string, options WorkspaceOptions) (*Workspace, error) {
	if err := contextError(ctx, "create", projectRoot); err != nil {
		return nil, err
	}

	root, err := canonicalDirectory(projectRoot)
	if err != nil {
		return nil, err
	}
	controlDir := filepath.Join(root, ".fluxa-builder")
	if err := ensurePrivateDirectory(controlDir); err != nil {
		return nil, err
	}
	workBase := filepath.Join(controlDir, "work")
	if err := ensurePrivateDirectory(workBase); err != nil {
		return nil, err
	}

	attempts := options.MaxAttempts
	if attempts == 0 {
		attempts = defaultCreateAttempts
	}
	if attempts < 0 {
		return nil, &Error{
			Kind:      ErrorCreate,
			Operation: "create",
			Path:      workBase,
			Detail:    "maximum attempts must not be negative",
		}
	}
	generator := options.IDGenerator
	if generator == nil {
		generator = randomWorkspaceID
	}

	for attempt := 0; attempt < attempts; attempt++ {
		if err := contextError(ctx, "create", workBase); err != nil {
			return nil, err
		}
		id, err := generator()
		if err != nil {
			return nil, &Error{
				Kind:      ErrorCreate,
				Operation: "generate ID for",
				Path:      workBase,
				Err:       err,
			}
		}
		if !workspaceIDPattern.MatchString(id) {
			return nil, &Error{
				Kind:      ErrorCreate,
				Operation: "validate ID for",
				Path:      workBase,
				Detail:    "workspace ID must be 32 lowercase hexadecimal characters",
			}
		}

		workspaceRoot := filepath.Join(workBase, id)
		err = os.Mkdir(workspaceRoot, 0o700)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, &Error{
				Kind:      ErrorCreate,
				Operation: "create",
				Path:      workspaceRoot,
				Err:       err,
			}
		}

		workspace := &Workspace{
			ID:          id,
			Root:        workspaceRoot,
			CompiledDir: filepath.Join(workspaceRoot, "compiled"),
			PackageDir:  filepath.Join(workspaceRoot, "package"),
			RuntimeDir:  filepath.Join(workspaceRoot, "runtime"),
			OutputDir:   filepath.Join(workspaceRoot, "output"),
			ReportDir:   filepath.Join(workspaceRoot, "report"),
			projectRoot: root,
			workBase:    workBase,
			keepWork:    options.KeepWork,
		}
		if err := workspace.createLayout(); err != nil {
			_ = os.RemoveAll(workspaceRoot)
			return nil, err
		}
		return workspace, nil
	}

	return nil, &Error{
		Kind:      ErrorCollision,
		Operation: "allocate unique",
		Path:      workBase,
		Detail:    fmt.Sprintf("failed after %d attempts", attempts),
	}
}

// Cleanup removes the transaction unless debug retention was requested.
func (w *Workspace) Cleanup() error {
	if w == nil || w.keepWork {
		return nil
	}
	if !pathWithin(w.workBase, w.Root) || filepath.Dir(w.Root) != w.workBase {
		return &Error{
			Kind:      ErrorUnsafePath,
			Operation: "clean",
			Path:      w.Root,
			Detail:    "workspace root is outside its work directory",
		}
	}
	resolvedBase, err := filepath.EvalSymlinks(w.workBase)
	if err != nil || filepath.Clean(resolvedBase) != filepath.Clean(w.workBase) {
		return &Error{
			Kind:      ErrorUnsafePath,
			Operation: "clean",
			Path:      w.Root,
			Detail:    "workspace control directory changed or became a symlink",
			Err:       err,
		}
	}
	if err := os.RemoveAll(w.Root); err != nil {
		return &Error{
			Kind:      ErrorCleanup,
			Operation: "clean",
			Path:      w.Root,
			Err:       err,
		}
	}
	return nil
}

// Publish atomically renames a completed output tree into its final path.
func (w *Workspace) Publish(ctx context.Context, source, destination string) error {
	if err := contextError(ctx, "publish", destination); err != nil {
		return err
	}

	resolvedSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		return &Error{
			Kind:      ErrorUnsafePath,
			Operation: "validate publish source in",
			Path:      source,
			Err:       err,
		}
	}
	if !pathWithin(w.OutputDir, resolvedSource) || resolvedSource == w.OutputDir {
		return &Error{
			Kind:      ErrorUnsafePath,
			Operation: "validate publish source in",
			Path:      resolvedSource,
			Detail:    "source must be inside workspace output",
		}
	}

	absoluteDestination, err := filepath.Abs(destination)
	if err != nil {
		return &Error{
			Kind:      ErrorUnsafePath,
			Operation: "resolve publish destination in",
			Path:      destination,
			Err:       err,
		}
	}
	absoluteDestination = filepath.Clean(absoluteDestination)
	controlDir := filepath.Join(w.projectRoot, ".fluxa-builder")
	if !pathWithin(w.projectRoot, absoluteDestination) ||
		pathWithin(controlDir, absoluteDestination) {
		return &Error{
			Kind:      ErrorUnsafePath,
			Operation: "validate publish destination in",
			Path:      absoluteDestination,
			Detail:    "destination must be inside the project and outside Builder control directories",
		}
	}

	parent := filepath.Dir(absoluteDestination)
	if err := secureMkdirAll(w.projectRoot, parent); err != nil {
		return err
	}
	if _, err := os.Lstat(absoluteDestination); err == nil {
		return &Error{
			Kind:      ErrorOutputExists,
			Operation: "publish",
			Path:      absoluteDestination,
			Detail:    "destination already exists",
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return &Error{
			Kind:      ErrorPublish,
			Operation: "inspect destination for",
			Path:      absoluteDestination,
			Err:       err,
		}
	}
	if err := contextError(ctx, "publish", absoluteDestination); err != nil {
		return err
	}

	if err := os.Rename(resolvedSource, absoluteDestination); err != nil {
		return &Error{
			Kind:      ErrorPublish,
			Operation: "atomically publish",
			Path:      absoluteDestination,
			Err:       err,
		}
	}
	return nil
}

func (w *Workspace) createLayout() error {
	directories := []string{
		w.CompiledDir,
		w.PackageDir,
		w.RuntimeDir,
		w.OutputDir,
		w.ReportDir,
	}
	for _, directory := range directories {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return &Error{
				Kind:      ErrorCreate,
				Operation: "create layout for",
				Path:      directory,
				Err:       err,
			}
		}
	}
	return nil
}

func canonicalDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", &Error{Kind: ErrorUnsafePath, Operation: "resolve", Path: path, Err: err}
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", &Error{Kind: ErrorUnsafePath, Operation: "resolve", Path: absolute, Err: err}
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("path is not a directory")
		}
		return "", &Error{Kind: ErrorUnsafePath, Operation: "validate", Path: resolved, Err: err}
	}
	return filepath.Clean(resolved), nil
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o700); err != nil {
			return &Error{Kind: ErrorCreate, Operation: "create", Path: path, Err: err}
		}
		return nil
	}
	if err != nil {
		return &Error{Kind: ErrorCreate, Operation: "inspect", Path: path, Err: err}
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return &Error{
			Kind:      ErrorUnsafePath,
			Operation: "validate",
			Path:      path,
			Detail:    "Builder control path must be a real directory",
		}
	}
	// Builder control directories intentionally require owner traversal access.
	if err := os.Chmod(path, 0o700); err != nil { // #nosec G302
		return &Error{Kind: ErrorCreate, Operation: "secure", Path: path, Err: err}
	}
	return nil
}

func secureMkdirAll(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return &Error{Kind: ErrorUnsafePath, Operation: "validate output parent in", Path: target, Err: err}
	}

	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return &Error{Kind: ErrorPublish, Operation: "create output parent in", Path: current, Err: err}
			}
			continue
		}
		if err != nil {
			return &Error{Kind: ErrorPublish, Operation: "inspect output parent in", Path: current, Err: err}
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return &Error{
				Kind:      ErrorUnsafePath,
				Operation: "validate output parent in",
				Path:      current,
				Detail:    "output parent must not contain symlinks or non-directories",
			}
		}
	}
	return nil
}

func randomWorkspaceID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func contextError(ctx context.Context, operation, path string) error {
	if err := ctx.Err(); err != nil {
		return &Error{
			Kind:      ErrorCanceled,
			Operation: operation,
			Path:      path,
			Err:       err,
		}
	}
	return nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relative)
}
