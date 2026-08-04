package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestEnsureStringFieldCreatesMissingFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	changed, err := EnsureStringField(root, "project", "id", "com.example.app")
	if err != nil {
		t.Fatalf("EnsureStringField() error = %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}

	want := "[project]\nid = \"com.example.app\"\n"
	assertFileContent(t, filepath.Join(root, "fluxa.toml"), want)
}

func TestEnsureStringFieldAppendsMissingTable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "fluxa.toml"), "[project]\nname = \"App\"\n")

	changed, err := EnsureStringField(root, "build", "output", "dist")
	if err != nil {
		t.Fatalf("EnsureStringField() error = %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}

	want := "[project]\nname = \"App\"\n\n[build]\noutput = \"dist\"\n"
	assertFileContent(t, filepath.Join(root, "fluxa.toml"), want)
}

func TestEnsureStringFieldInsertsMissingKeyPreservingUnrelatedContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	original := "# top comment\n[project]\nname = \"App\"\nversion = \"1.0.0\"\n\n" +
		"[runtime]\nscope_cap = 1024\n\n[libs]\nstd.fs = \"1.0\"\n"
	writeFile(t, filepath.Join(root, "fluxa.toml"), original)

	changed, err := EnsureStringField(root, "project", "id", "com.example.app")
	if err != nil {
		t.Fatalf("EnsureStringField() error = %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}

	want := "# top comment\n[project]\nid = \"com.example.app\"\nname = \"App\"\nversion = \"1.0.0\"\n\n" +
		"[runtime]\nscope_cap = 1024\n\n[libs]\nstd.fs = \"1.0\"\n"
	assertFileContent(t, filepath.Join(root, "fluxa.toml"), want)
}

func TestEnsureStringFieldNoOpWhenKeyExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	original := "[project]\nid = \"com.example.already\"\nname = \"App\"\n"
	writeFile(t, filepath.Join(root, "fluxa.toml"), original)

	changed, err := EnsureStringField(root, "project", "id", "com.example.different")
	if err != nil {
		t.Fatalf("EnsureStringField() error = %v", err)
	}
	if changed {
		t.Fatalf("changed = true, want false")
	}

	assertFileContent(t, filepath.Join(root, "fluxa.toml"), original)
}

func TestEnsureStringFieldEscapesValue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	changed, err := EnsureStringField(root, "project", "name", `App "quoted" \ back`)
	if err != nil {
		t.Fatalf("EnsureStringField() error = %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}

	want := "[project]\nname = \"App \\\"quoted\\\" \\\\ back\"\n"
	assertFileContent(t, filepath.Join(root, "fluxa.toml"), want)

	// The escaped value must round-trip through a real TOML decoder.
	cfg := struct {
		Project struct {
			Name string `toml:"name"`
		} `toml:"project"`
	}{}
	data := readFile(t, filepath.Join(root, "fluxa.toml"))
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		t.Fatalf("decode written file: %v", err)
	}
	if want := `App "quoted" \ back`; cfg.Project.Name != want {
		t.Fatalf("decoded name = %q, want %q", cfg.Project.Name, want)
	}
}

func TestEnsureStringFieldPreservesFileMode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "fluxa.toml")
	writeFile(t, path, "[project]\nname = \"App\"\n")
	if err := os.Chmod(path, 0o640); err != nil { // #nosec G302 -- exercising mode preservation, not a secrecy requirement.
		t.Fatalf("chmod: %v", err)
	}

	if _, err := EnsureStringField(root, "project", "id", "com.example.app"); err != nil {
		t.Fatalf("EnsureStringField() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode = %v, want 0640", got)
	}
}

func TestEnsureBoolField(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	changed, err := EnsureBoolField(root, "package", "include_source", true)
	if err != nil {
		t.Fatalf("EnsureBoolField() error = %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	assertFileContent(t, filepath.Join(root, "fluxa.toml"), "[package]\ninclude_source = true\n")

	changed, err = EnsureBoolField(root, "package", "include_source", false)
	if err != nil {
		t.Fatalf("EnsureBoolField() error = %v", err)
	}
	if changed {
		t.Fatalf("changed = true, want false (existing key must not be overwritten)")
	}
	assertFileContent(t, filepath.Join(root, "fluxa.toml"), "[package]\ninclude_source = true\n")
}

func TestEnsureStringArrayField(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	changed, err := EnsureStringArrayField(root, "build", "assets", []string{"assets/**", "data/**"})
	if err != nil {
		t.Fatalf("EnsureStringArrayField() error = %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	want := "[build]\nassets = [\"assets/**\", \"data/**\"]\n"
	assertFileContent(t, filepath.Join(root, "fluxa.toml"), want)

	var cfg struct {
		Build struct {
			Assets []string `toml:"assets"`
		} `toml:"build"`
	}
	data := readFile(t, filepath.Join(root, "fluxa.toml"))
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		t.Fatalf("decode written file: %v", err)
	}
	if len(cfg.Build.Assets) != 2 || cfg.Build.Assets[0] != "assets/**" || cfg.Build.Assets[1] != "data/**" {
		t.Fatalf("decoded assets = %v, want [assets/** data/**]", cfg.Build.Assets)
	}
}

func TestEnsureFieldSupportsDottedNestedTable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "fluxa.toml"), "[project]\nname = \"App\"\n\n[targets.windows]\nicon = \"assets/app.ico\"\n")

	has, err := HasField(root, "targets.windows", "icon")
	if err != nil {
		t.Fatalf("HasField() error = %v", err)
	}
	if !has {
		t.Fatalf("HasField(targets.windows, icon) = false, want true")
	}

	changed, err := EnsureStringField(root, "targets.macos", "bundle_id", "com.example.app")
	if err != nil {
		t.Fatalf("EnsureStringField() error = %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	want := "[project]\nname = \"App\"\n\n[targets.windows]\nicon = \"assets/app.ico\"\n\n" +
		"[targets.macos]\nbundle_id = \"com.example.app\"\n"
	assertFileContent(t, filepath.Join(root, "fluxa.toml"), want)
}

func TestHasFieldMissingFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	has, err := HasField(root, "project", "id")
	if err != nil {
		t.Fatalf("HasField() error = %v", err)
	}
	if has {
		t.Fatalf("HasField() = true, want false for a missing file")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { // #nosec G306 -- test fixture fluxa.toml, not sensitive.
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test fixture path.
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got := string(readFile(t, path))
	if got != want {
		t.Fatalf("file content = %q, want %q", got, want)
	}
}
