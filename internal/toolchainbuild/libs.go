package toolchainbuild

import (
	"os"

	"github.com/BurntSushi/toml"
)

// windowsEssentialLibs is the fixed library set fluxa-lang's own Windows
// static build targets support (docs/WINDOWS.md in that repository) —
// this restriction is specific to Windows's two curated
// build-windows-essential-static/build-windows-packaged targets, not a
// general limitation of this package. Linux's plain `make build` has no
// such restriction: it compiles whatever fluxa.libs actually declares,
// full stop, so this set is only consulted by acquireWindows.
var windowsEssentialLibs = map[string]bool{
	"graph": true, "image": true, "strings": true, "sqlite": true,
	"sound": true, "crypto": true, "json2": true, "fs": true,
	"httpc": true, "https": true,
	"math": true, "csv": true, "json": true, "pid": true, "libdsp": true,
}

// libsFile mirrors fluxa.libs's [libs.build] table. Bare library names
// (e.g. "graph", not "std.graph") are nested one level under "std"
// because unquoted dotted TOML keys such as "std.graph = true" decode as
// nested tables, not a single "std.graph" key.
type libsFile struct {
	Libs struct {
		Build map[string]map[string]bool `toml:"build"`
	} `toml:"libs"`
}

// readEnabledLibs reads fluxa.libs (if present — a project without one has
// nothing to check) and returns the bare names of every library set to
// true. A missing file is not an error: fluxa.libs is optional in this
// project's own model (internal/app/init.go's hashOptionalFile treats it
// the same way).
func readEnabledLibs(path string) ([]string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- caller-selected project-relative fluxa.libs path.
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, newError(ErrorIO, "read fluxa.libs", path, err)
	}
	var parsed libsFile
	if _, err := toml.Decode(string(data), &parsed); err != nil {
		return nil, newError(ErrorIO, "parse fluxa.libs", path, err)
	}
	std, ok := parsed.Libs.Build["std"]
	if !ok {
		return nil, nil
	}
	enabled := make([]string, 0, len(std))
	for name, on := range std {
		if on {
			enabled = append(enabled, name)
		}
	}
	return enabled, nil
}

// unsupportedForWindows reports every enabled library outside
// windowsEssentialLibs, so acquireWindows can fail with a specific,
// actionable reason instead of attempting an incomplete build. Linux has
// no equivalent restriction.
func unsupportedForWindows(enabled []string) []string {
	var unsupported []string
	for _, name := range enabled {
		if !windowsEssentialLibs[name] {
			unsupported = append(unsupported, name)
		}
	}
	return unsupported
}
