package project_test

import (
	"path/filepath"
	"testing"

	"github.com/RodBarenco/fluxa-builder/internal/project"
)

func TestConfigSetOutput(t *testing.T) {
	t.Parallel()

	root := writeProject(t, minimalConfig)
	cfg, err := project.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if err := cfg.SetOutput("build-output"); err != nil {
		t.Fatalf("SetOutput() error = %v", err)
	}
	root = canonicalPath(t, root)
	if cfg.Build.Output != "build-output" {
		t.Errorf("Build.Output = %q, want build-output", cfg.Build.Output)
	}
	if want := filepath.Join(root, "build-output"); cfg.OutputPath != want {
		t.Errorf("OutputPath = %q, want %q", cfg.OutputPath, want)
	}
}

func TestConfigSetOutputRejectsUnsafePaths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value string
	}{
		{"absolute", filepath.Join(t.TempDir(), "dist")},
		{"traversal", "../outside"},
		{"empty", ""},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := writeProject(t, minimalConfig)
			cfg, err := project.Load(root)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			originalOutput, originalPath := cfg.Build.Output, cfg.OutputPath

			if err := cfg.SetOutput(tt.value); err == nil {
				t.Fatalf("SetOutput(%q) error = nil, want error", tt.value)
			}
			if cfg.Build.Output != originalOutput || cfg.OutputPath != originalPath {
				t.Errorf("SetOutput(%q) mutated cfg on failure", tt.value)
			}
		})
	}
}

func TestValidProjectID(t *testing.T) {
	t.Parallel()

	valid := []string{"com.example.app", "com.example.my-app", "io.github.rod-barenco.fluxa"}
	invalid := []string{"", "Com.Example.App", "com", "com..example", "-com.example"}

	for _, id := range valid {
		if !project.ValidProjectID(id) {
			t.Errorf("ValidProjectID(%q) = false, want true", id)
		}
	}
	for _, id := range invalid {
		if project.ValidProjectID(id) {
			t.Errorf("ValidProjectID(%q) = true, want false", id)
		}
	}
}

func TestValidSemVer(t *testing.T) {
	t.Parallel()

	valid := []string{"0.1.0", "1.2.3", "1.0.0-beta.1", "1.0.0+build.5"}
	invalid := []string{"", "1.0", "v1.0.0", "01.0.0"}

	for _, version := range valid {
		if !project.ValidSemVer(version) {
			t.Errorf("ValidSemVer(%q) = false, want true", version)
		}
	}
	for _, version := range invalid {
		if project.ValidSemVer(version) {
			t.Errorf("ValidSemVer(%q) = true, want false", version)
		}
	}
}
