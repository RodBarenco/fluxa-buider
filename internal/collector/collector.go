// Package collector deterministically inventories files selected for a build.
package collector

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RodBarenco/fluxa-builder/internal/project"
)

const (
	defaultMaxFiles     = 100_000
	defaultMaxDepth     = 64
	defaultMaxFileSize  = int64(1 << 30)
	defaultMaxTotalSize = int64(4 << 30)
)

// Kind is logical metadata. It does not change a file's project-relative path.
type Kind string

const (
	// KindEntry identifies the project's Fluxa entry file.
	KindEntry Kind = "entry"
	// KindModule identifies a Fluxa module selected from a module root.
	KindModule Kind = "module"
	// KindAsset identifies non-module content configured for packaging.
	KindAsset Kind = "asset"
)

// Entry is one selected regular file.
type Entry struct {
	Path       string
	SourcePath string
	Kind       Kind
	Size       int64
	Symlink    bool
}

// Limits bounds resource use while walking an untrusted project tree.
type Limits struct {
	MaxFiles     int
	MaxDepth     int
	MaxFileSize  int64
	MaxTotalSize int64
}

// Options describes paths in the source project. Patterns use slash separators.
type Options struct {
	Root           string
	Entry          string
	ModulePatterns []string
	AssetPatterns  []string
	Exclude        []string
	Limits         Limits
}

// Result is sorted by normalized project-relative path.
type Result struct {
	Entries   []Entry
	TotalSize int64
}

// CollectProject derives language source roots and configured asset patterns
// from a validated Fluxa project.
func CollectProject(ctx context.Context, cfg *project.Config) (Result, error) {
	modulePatterns := []string{"live/**/*.flx", "static/**/*.flx"}
	if cfg.Project.ModuleRoot != "" {
		root := filepath.ToSlash(filepath.Clean(cfg.Project.ModuleRoot))
		modulePatterns = append(modulePatterns, root+"/**/*.flx")
	}
	return Collect(ctx, Options{
		Root:           cfg.Root,
		Entry:          cfg.Project.Entry,
		ModulePatterns: modulePatterns,
		AssetPatterns:  cfg.Build.Assets,
		Exclude:        cfg.Build.Exclude,
	})
}

// Collect inventories the selected files without following directory symlinks.
func Collect(ctx context.Context, options Options) (Result, error) {
	root, err := canonicalRoot(options.Root)
	if err != nil {
		return Result{}, err
	}
	limits := withDefaults(options.Limits)
	if options.Entry == "" {
		return Result{}, collectionError(ErrorInvalidInput, "validate", "", errors.New("entry is required"))
	}

	selected := make(map[string]Entry)
	casePaths := make(map[string]string)
	total := int64(0)
	err = filepath.WalkDir(root, func(sourcePath string, directoryEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return collectionError(ErrorIO, "walk", sourcePath, walkErr)
		}
		if err := ctx.Err(); err != nil {
			return collectionError(ErrorCanceled, "walk", sourcePath, err)
		}
		if sourcePath == root {
			return nil
		}

		relative, err := filepath.Rel(root, sourcePath)
		if err != nil {
			return collectionError(ErrorIO, "relativize", sourcePath, err)
		}
		logical := filepath.ToSlash(relative)
		if hardExcluded(logical) {
			if directoryEntry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if depth(logical) > limits.MaxDepth {
			return collectionError(ErrorLimit, "check depth", logical, fmt.Errorf("exceeds maximum depth of %d", limits.MaxDepth))
		}

		isEntry := logical == filepath.ToSlash(filepath.Clean(options.Entry))
		module, err := matchesAny(options.ModulePatterns, logical)
		if err != nil {
			return collectionError(ErrorInvalidInput, "match module pattern", logical, err)
		}
		asset, err := matchesAny(options.AssetPatterns, logical)
		if err != nil {
			return collectionError(ErrorInvalidInput, "match asset pattern", logical, err)
		}
		excluded, err := matchesAny(options.Exclude, logical)
		if err != nil {
			return collectionError(ErrorInvalidInput, "match exclude pattern", logical, err)
		}

		if directoryEntry.IsDir() {
			return nil
		}
		if !isEntry && ((!module && !asset) || excluded) {
			return nil
		}

		resolvedPath, info, symlink, err := resolveSelected(root, sourcePath, directoryEntry)
		if err != nil {
			return err
		}
		if info.Size() > limits.MaxFileSize {
			return collectionError(ErrorLimit, "check file size", logical, fmt.Errorf("size %d exceeds maximum %d", info.Size(), limits.MaxFileSize))
		}
		if len(selected) >= limits.MaxFiles {
			return collectionError(ErrorLimit, "check file count", logical, fmt.Errorf("exceeds maximum file count of %d", limits.MaxFiles))
		}
		if info.Size() > limits.MaxTotalSize-total {
			return collectionError(ErrorLimit, "check total size", logical, fmt.Errorf("exceeds maximum total size of %d", limits.MaxTotalSize))
		}

		folded := strings.ToLower(logical)
		if previous, exists := casePaths[folded]; exists && previous != logical {
			return collectionError(ErrorCollision, "check case collision", logical, fmt.Errorf("collides with %q", previous))
		}
		casePaths[folded] = logical

		kind := KindAsset
		if module {
			kind = KindModule
		}
		if isEntry {
			kind = KindEntry
		}
		selected[logical] = Entry{
			Path:       logical,
			SourcePath: resolvedPath,
			Kind:       kind,
			Size:       info.Size(),
			Symlink:    symlink,
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return Result{}, err
	}

	entryPath := filepath.ToSlash(filepath.Clean(options.Entry))
	if _, exists := selected[entryPath]; !exists {
		return Result{}, collectionError(ErrorInvalidInput, "select entry", entryPath, errors.New("entry was not found"))
	}

	entries := make([]Entry, 0, len(selected))
	for _, entry := range selected {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return Result{Entries: entries, TotalSize: total}, nil
}

func canonicalRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", collectionError(ErrorInvalidInput, "resolve root", root, err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", collectionError(ErrorIO, "resolve root", absolute, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", collectionError(ErrorIO, "inspect root", resolved, err)
	}
	if !info.IsDir() {
		return "", collectionError(ErrorInvalidInput, "validate root", resolved, errors.New("root is not a directory"))
	}
	return filepath.Clean(resolved), nil
}

func resolveSelected(root, source string, entry fs.DirEntry) (string, fs.FileInfo, bool, error) {
	if entry.Type()&os.ModeSymlink == 0 {
		info, err := entry.Info()
		if err != nil {
			return "", nil, false, collectionError(ErrorIO, "inspect", source, err)
		}
		if !info.Mode().IsRegular() {
			return "", nil, false, collectionError(ErrorUnsafePath, "validate file type", source, errors.New("selected path is not a regular file"))
		}
		return source, info, false, nil
	}

	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return "", nil, true, collectionError(ErrorUnsafePath, "resolve symlink", source, err)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", nil, true, collectionError(ErrorUnsafePath, "validate symlink", source, errors.New("symlink target escapes project root"))
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, true, collectionError(ErrorIO, "inspect symlink target", source, err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, true, collectionError(ErrorUnsafePath, "validate symlink target", source, errors.New("symlink target is not a regular file"))
	}
	return filepath.Clean(resolved), info, true, nil
}

func matchesAny(patterns []string, logical string) (bool, error) {
	for _, pattern := range patterns {
		ok, err := match(filepath.ToSlash(pattern), logical)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func hardExcluded(logical string) bool {
	for _, name := range []string{".git", ".fluxa-builder"} {
		if logical == name || strings.HasPrefix(logical, name+"/") {
			return true
		}
	}
	return false
}

func depth(logical string) int {
	if logical == "" {
		return 0
	}
	return strings.Count(logical, "/") + 1
}

func withDefaults(limits Limits) Limits {
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = defaultMaxFiles
	}
	if limits.MaxDepth <= 0 {
		limits.MaxDepth = defaultMaxDepth
	}
	if limits.MaxFileSize <= 0 {
		limits.MaxFileSize = defaultMaxFileSize
	}
	if limits.MaxTotalSize <= 0 {
		limits.MaxTotalSize = defaultMaxTotalSize
	}
	return limits
}

func collectionError(kind ErrorKind, operation, path string, err error) *Error {
	return &Error{Kind: kind, Operation: operation, Path: path, Err: err}
}
