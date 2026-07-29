package project_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RodBarenco/fluxa-builder/internal/project"
)

const minimalConfig = `
[project]
name = "Minha Aplicação"
id = "com.exemplo.minha-aplicacao"
version = "1.0.0"
entry = "main.flx"
`

func TestLoadMinimalConfigAppliesDefaults(t *testing.T) {
	t.Parallel()

	root := writeProject(t, minimalConfig)
	cfg, err := project.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Root != root {
		t.Errorf("Root = %q, want %q", cfg.Root, root)
	}
	if cfg.EntryPath != filepath.Join(root, "main.flx") {
		t.Errorf("EntryPath = %q, want project entry", cfg.EntryPath)
	}
	if cfg.Project.Type != "desktop" {
		t.Errorf("Project.Type = %q, want desktop", cfg.Project.Type)
	}
	if cfg.Build.Output != "dist" || cfg.OutputPath != filepath.Join(root, "dist") {
		t.Errorf("Build output defaults = %q (%q), want dist", cfg.Build.Output, cfg.OutputPath)
	}
	if cfg.Build.Target != "host" {
		t.Errorf("Build.Target = %q, want host", cfg.Build.Target)
	}
	if !cfg.Build.Terminal {
		t.Error("Build.Terminal = false, want default true")
	}
	if cfg.Package.Format != "portable" || !cfg.Package.Compress {
		t.Errorf("Package defaults = %#v, want portable and compressed", cfg.Package)
	}
}

func TestLoadCompleteConfig(t *testing.T) {
	t.Parallel()

	root := writeProject(t, `
[project]
name = "Aplicação Completa"
id = "br.dev.exemplo.completa"
version = "2.3.4-beta.1"
entry = "src/main.flx"
type = "cli"
module_root = "src"

[toolchain]
path = "tools/fluxa"

[build]
output = "release"
target = "linux-x64"
terminal = false
assets = ["assets/**", "data/*.json"]
exclude = ["tests/**", "*.log"]

[package]
format = "zip"
compress = false
sign = true
embed = true
include_source = true

[targets.windows]
icon = "assets/app.ico"

[targets.linux]
icon = "assets/app.png"

[targets.macos]
icon = "assets/app.icns"
bundle_id = "br.dev.exemplo.completa"

[runtime]
gc_cap = 1024

[libs]
std.math = "1.0"
`)
	mustWriteFile(t, filepath.Join(root, "src", "main.flx"), `print("ok")`)

	cfg, err := project.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Build.Terminal {
		t.Error("Build.Terminal = true, want explicit false")
	}
	if cfg.Toolchain.Path != "tools/fluxa" {
		t.Errorf("Toolchain.Path = %q", cfg.Toolchain.Path)
	}
	if len(cfg.Build.Assets) != 2 || len(cfg.Build.Exclude) != 2 {
		t.Errorf("patterns not loaded: assets=%v exclude=%v", cfg.Build.Assets, cfg.Build.Exclude)
	}
	if !cfg.Package.Sign || !cfg.Package.Embed || !cfg.Package.IncludeSource {
		t.Errorf("package flags not loaded: %#v", cfg.Package)
	}
	if cfg.Targets.MacOS.BundleID != "br.dev.exemplo.completa" {
		t.Errorf("macOS bundle ID = %q", cfg.Targets.MacOS.BundleID)
	}
}

func TestLoadProjectPathsWithSpacesAndUnicode(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "Projeto Olá 世界")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(root, "fluxa.toml"), minimalConfig)
	mustWriteFile(t, filepath.Join(root, "main.flx"), `print("olá")`)

	cfg, err := project.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Root != root {
		t.Errorf("Root = %q, want %q", cfg.Root, root)
	}
}

func TestLoadInvalidConfigs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		config     string
		prepare    func(*testing.T, string)
		wantKind   project.ErrorKind
		wantField  string
		wantSubstr string
	}{
		{
			name:       "invalid TOML",
			config:     "[project\nname =",
			wantKind:   project.ErrorParse,
			wantSubstr: "parse",
		},
		{
			name:      "missing name",
			config:    strings.Replace(minimalConfig, `name = "Minha Aplicação"`+"\n", "", 1),
			wantKind:  project.ErrorValidation,
			wantField: "project.name",
		},
		{
			name:      "missing id",
			config:    strings.Replace(minimalConfig, `id = "com.exemplo.minha-aplicacao"`+"\n", "", 1),
			wantKind:  project.ErrorValidation,
			wantField: "project.id",
		},
		{
			name:      "invalid id",
			config:    strings.Replace(minimalConfig, "com.exemplo.minha-aplicacao", "Aplicação sem domínio", 1),
			wantKind:  project.ErrorValidation,
			wantField: "project.id",
		},
		{
			name:      "missing version",
			config:    strings.Replace(minimalConfig, `version = "1.0.0"`+"\n", "", 1),
			wantKind:  project.ErrorValidation,
			wantField: "project.version",
		},
		{
			name:      "invalid version",
			config:    strings.Replace(minimalConfig, "1.0.0", "version one", 1),
			wantKind:  project.ErrorValidation,
			wantField: "project.version",
		},
		{
			name:      "missing entry",
			config:    strings.Replace(minimalConfig, `entry = "main.flx"`+"\n", "", 1),
			wantKind:  project.ErrorValidation,
			wantField: "project.entry",
		},
		{
			name:      "entry does not exist",
			config:    strings.Replace(minimalConfig, "main.flx", "missing.flx", 1),
			wantKind:  project.ErrorValidation,
			wantField: "project.entry",
		},
		{
			name:      "absolute entry",
			config:    strings.Replace(minimalConfig, "main.flx", "/tmp/main.flx", 1),
			wantKind:  project.ErrorValidation,
			wantField: "project.entry",
		},
		{
			name:      "entry traversal",
			config:    strings.Replace(minimalConfig, "main.flx", "../main.flx", 1),
			wantKind:  project.ErrorValidation,
			wantField: "project.entry",
		},
		{
			name: "external output",
			config: minimalConfig + `
[build]
output = "../release"
`,
			wantKind:  project.ErrorValidation,
			wantField: "build.output",
		},
		{
			name: "Windows absolute output",
			config: minimalConfig + `
[build]
output = 'C:\Windows\Temp'
`,
			wantKind:  project.ErrorValidation,
			wantField: "build.output",
		},
		{
			name: "terminal has wrong type",
			config: minimalConfig + `
[build]
terminal = "false"
`,
			wantKind:   project.ErrorParse,
			wantSubstr: "terminal",
		},
		{
			name: "absolute asset",
			config: minimalConfig + `
[build]
assets = ["/tmp/**"]
`,
			wantKind:  project.ErrorValidation,
			wantField: "build.assets[0]",
		},
		{
			name: "invalid package format",
			config: minimalConfig + `
[package]
format = "dmg"
`,
			wantKind:  project.ErrorValidation,
			wantField: "package.format",
		},
		{
			name: "asset traversal",
			config: minimalConfig + `
[build]
assets = ["../assets/**"]
`,
			wantKind:  project.ErrorValidation,
			wantField: "build.assets[0]",
		},
		{
			name:   "symlink entry escapes root",
			config: strings.Replace(minimalConfig, "main.flx", "link.flx", 1),
			prepare: func(t *testing.T, root string) {
				t.Helper()
				outside := filepath.Join(t.TempDir(), "outside.flx")
				mustWriteFile(t, outside, `print("outside")`)
				if err := os.Symlink(outside, filepath.Join(root, "link.flx")); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
			wantKind:  project.ErrorValidation,
			wantField: "project.entry",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := writeProject(t, tt.config)
			if tt.prepare != nil {
				tt.prepare(t, root)
			}

			_, err := project.Load(root)
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}

			var projectErr *project.Error
			if !errors.As(err, &projectErr) {
				t.Fatalf("Load() error type = %T, want *project.Error", err)
			}
			if projectErr.Kind != tt.wantKind {
				t.Errorf("error kind = %q, want %q", projectErr.Kind, tt.wantKind)
			}
			if projectErr.Field != tt.wantField {
				t.Errorf("error field = %q, want %q", projectErr.Field, tt.wantField)
			}
			if tt.wantSubstr != "" && !strings.Contains(strings.ToLower(err.Error()), tt.wantSubstr) {
				t.Errorf("error = %q, want substring %q", err, tt.wantSubstr)
			}
		})
	}
}

func TestLoadMissingConfig(t *testing.T) {
	t.Parallel()

	_, err := project.Load(t.TempDir())
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}

	var projectErr *project.Error
	if !errors.As(err, &projectErr) {
		t.Fatalf("Load() error type = %T, want *project.Error", err)
	}
	if projectErr.Kind != project.ErrorNotFound {
		t.Errorf("error kind = %q, want %q", projectErr.Kind, project.ErrorNotFound)
	}
}

func writeProject(t *testing.T, config string) string {
	t.Helper()

	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "fluxa.toml"), config)
	mustWriteFile(t, filepath.Join(root, "main.flx"), `print("ok")`)
	return root
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
