// Package project loads and validates Fluxa Builder project configuration.
package project

import "fmt"

const (
	defaultProjectType  = "desktop"
	defaultOutput       = "dist"
	defaultTarget       = "host"
	defaultPackage      = "portable"
	maxPatterns         = 256
	maxPatternLength    = 4096
	maxProjectNameBytes = 256
)

// Config is the validated configuration for a Fluxa project.
type Config struct {
	Root       string `toml:"-"`
	EntryPath  string `toml:"-"`
	OutputPath string `toml:"-"`

	Project   Metadata        `toml:"project"`
	Toolchain ToolchainConfig `toml:"toolchain"`
	Build     BuildConfig     `toml:"build"`
	Package   PackageConfig   `toml:"package"`
	Targets   TargetsConfig   `toml:"targets"`
}

// Metadata identifies the application being packaged.
type Metadata struct {
	Name       string `toml:"name"`
	ID         string `toml:"id"`
	Version    string `toml:"version"`
	Entry      string `toml:"entry"`
	Type       string `toml:"type"`
	ModuleRoot string `toml:"module_root"`
}

// ToolchainConfig controls explicit Fluxa toolchain selection.
type ToolchainConfig struct {
	Path  string `toml:"path"`
	Fluxa string `toml:"fluxa"`
}

// BuildConfig controls output collection and target selection.
type BuildConfig struct {
	Output   string   `toml:"output"`
	Target   string   `toml:"target"`
	Terminal bool     `toml:"-"`
	Assets   []string `toml:"assets"`
	Exclude  []string `toml:"exclude"`

	terminal *bool
}

// UnmarshalTOML preserves whether terminal was omitted so defaults can be
// applied without overriding an explicit false.
func (c *BuildConfig) UnmarshalTOML(data any) error {
	raw, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("build must be a TOML table")
	}

	if value, exists := raw["output"]; exists {
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("build.output must be a string")
		}
		c.Output = text
	}
	if value, exists := raw["target"]; exists {
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("build.target must be a string")
		}
		c.Target = text
	}
	if value, exists := raw["terminal"]; exists {
		flag, ok := value.(bool)
		if !ok {
			return fmt.Errorf("build.terminal must be a boolean")
		}
		c.terminal = &flag
	}
	var err error
	if c.Assets, err = stringSlice(raw, "assets"); err != nil {
		return err
	}
	if c.Exclude, err = stringSlice(raw, "exclude"); err != nil {
		return err
	}
	return nil
}

// PackageConfig controls the initial package format.
type PackageConfig struct {
	Format        string `toml:"format"`
	Compress      bool   `toml:"-"`
	Sign          bool   `toml:"sign"`
	Embed         bool   `toml:"embed"`
	IncludeSource bool   `toml:"include_source"`

	compress *bool
}

// UnmarshalTOML preserves whether compress was omitted.
func (c *PackageConfig) UnmarshalTOML(data any) error {
	raw, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("package must be a TOML table")
	}

	if value, exists := raw["format"]; exists {
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("package.format must be a string")
		}
		c.Format = text
	}
	if value, exists := raw["compress"]; exists {
		flag, ok := value.(bool)
		if !ok {
			return fmt.Errorf("package.compress must be a boolean")
		}
		c.compress = &flag
	}
	if value, exists := raw["sign"]; exists {
		flag, ok := value.(bool)
		if !ok {
			return fmt.Errorf("package.sign must be a boolean")
		}
		c.Sign = flag
	}
	if value, exists := raw["embed"]; exists {
		flag, ok := value.(bool)
		if !ok {
			return fmt.Errorf("package.embed must be a boolean")
		}
		c.Embed = flag
	}
	if value, exists := raw["include_source"]; exists {
		flag, ok := value.(bool)
		if !ok {
			return fmt.Errorf("package.include_source must be a boolean")
		}
		c.IncludeSource = flag
	}
	return nil
}

// TargetsConfig contains target-specific metadata.
type TargetsConfig struct {
	Windows WindowsTargetConfig `toml:"windows"`
	Linux   LinuxTargetConfig   `toml:"linux"`
	MacOS   MacOSTargetConfig   `toml:"macos"`
}

// WindowsTargetConfig contains Windows metadata.
type WindowsTargetConfig struct {
	Icon string `toml:"icon"`
}

// LinuxTargetConfig contains Linux metadata.
type LinuxTargetConfig struct {
	Icon string `toml:"icon"`
}

// MacOSTargetConfig contains macOS metadata.
type MacOSTargetConfig struct {
	Icon     string `toml:"icon"`
	BundleID string `toml:"bundle_id"`
}

func stringSlice(table map[string]any, key string) ([]string, error) {
	value, exists := table[key]
	if !exists {
		return nil, nil
	}
	raw, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("build.%s must be an array of strings", key)
	}

	result := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("build.%s must contain only strings", key)
		}
		result = append(result, text)
	}
	return result, nil
}
