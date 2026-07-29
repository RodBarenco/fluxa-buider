package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Load reads, defaults, normalizes, and validates a project at root.
func Load(root string) (*Config, error) {
	normalizedRoot, err := normalizeRoot(root)
	if err != nil {
		return nil, err
	}

	configPath := filepath.Join(normalizedRoot, "fluxa.toml")
	// configPath is derived from a canonical project root and a fixed filename.
	file, err := os.Open(configPath) // #nosec G304
	if err != nil {
		kind := ErrorIO
		if errors.Is(err, os.ErrNotExist) {
			kind = ErrorNotFound
		}
		return nil, &Error{
			Kind:      kind,
			Operation: "load",
			Path:      configPath,
			Err:       err,
		}
	}
	defer func() {
		_ = file.Close()
	}()

	var cfg Config
	if _, err := toml.NewDecoder(file).Decode(&cfg); err != nil {
		return nil, &Error{
			Kind:      ErrorParse,
			Operation: "parse",
			Path:      configPath,
			Err:       err,
		}
	}

	cfg.Root = normalizedRoot
	applyDefaults(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func normalizeRoot(root string) (string, error) {
	if root == "" {
		root = "."
	}

	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", &Error{
			Kind:      ErrorIO,
			Operation: "resolve project root",
			Path:      root,
			Err:       err,
		}
	}

	info, err := os.Stat(absolute)
	if err != nil {
		kind := ErrorIO
		if errors.Is(err, os.ErrNotExist) {
			kind = ErrorNotFound
		}
		return "", &Error{
			Kind:      kind,
			Operation: "resolve project root",
			Path:      absolute,
			Err:       err,
		}
	}
	if !info.IsDir() {
		return "", &Error{
			Kind:      ErrorValidation,
			Operation: "validate",
			Path:      absolute,
			Err:       fmt.Errorf("project root is not a directory"),
		}
	}

	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", &Error{
			Kind:      ErrorIO,
			Operation: "resolve project root",
			Path:      absolute,
			Err:       err,
		}
	}
	return filepath.Clean(resolved), nil
}

func applyDefaults(cfg *Config) {
	if cfg.Project.Type == "" {
		cfg.Project.Type = defaultProjectType
	}
	if cfg.Build.Output == "" {
		cfg.Build.Output = defaultOutput
	}
	if cfg.Build.Target == "" {
		cfg.Build.Target = defaultTarget
	}
	if cfg.Build.terminal == nil {
		cfg.Build.Terminal = true
	} else {
		cfg.Build.Terminal = *cfg.Build.terminal
	}
	if cfg.Package.Format == "" {
		cfg.Package.Format = defaultPackage
	}
	if cfg.Package.compress == nil {
		cfg.Package.Compress = true
	} else {
		cfg.Package.Compress = *cfg.Package.compress
	}
}
