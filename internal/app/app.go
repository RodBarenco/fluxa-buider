// Package app implements the Fluxa Builder command-line application.
package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/project"
	"github.com/RodBarenco/fluxa-builder/internal/toolchain"
)

const (
	// Name is the public CLI name.
	Name = "fluxa-builder"

	// Version is the current development version.
	Version = "0.1.0-dev"
)

// Run executes the CLI and returns a process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_ = printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "build":
		return runBuild(args[1:], stdout, stderr, defaultBuildDependencies())
	case "version":
		if len(args) != 1 {
			writeString(stderr, "error: version does not accept arguments\n")
			_ = printUsage(stderr)
			return 2
		}
		if _, err := fmt.Fprintf(stdout, "%s %s\n", Name, Version); err != nil {
			writeString(stderr, "error: failed to write version output\n")
			return 1
		}
		return 0
	case "help", "-h", "--help":
		if len(args) != 1 {
			writeString(stderr, "error: help does not accept arguments\n")
			_ = printUsage(stderr)
			return 2
		}
		if err := printUsage(stdout); err != nil {
			writeString(stderr, "error: failed to write help output\n")
			return 1
		}
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "error: unknown command %q\n", args[0])
		_ = printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) error {
	_, err := fmt.Fprintf(w, `Fluxa Builder %s

Usage:
  fluxa-builder build [project] [--fluxa <path>]
  fluxa-builder version
  fluxa-builder help
`, Version)
	return err
}

func writeString(w io.Writer, value string) {
	_, _ = io.WriteString(w, value)
}

type buildOptions struct {
	projectPath string
	fluxaPath   string
}

type buildDependencies struct {
	resolve func(toolchain.ResolveOptions) (toolchain.Candidate, error)
	probe   func(context.Context, string, time.Duration) (toolchain.Identity, error)
}

func defaultBuildDependencies() buildDependencies {
	return buildDependencies{
		resolve: toolchain.Resolve,
		probe:   toolchain.Probe,
	}
}

func runBuild(args []string, stdout, stderr io.Writer, dependencies buildDependencies) int {
	options, err := parseBuildOptions(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		_ = printUsage(stderr)
		return 2
	}

	cfg, err := project.Load(options.projectPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: failed to load project\ncaused by: %v\n", err)
		return 1
	}

	candidate, err := dependencies.resolve(toolchain.ResolveOptions{
		ExplicitPath: options.fluxaPath,
		ConfigPath:   cfg.Toolchain.Path,
		FluxaHome:    os.Getenv("FLUXA_HOME"),
		PathEnv:      os.Getenv("PATH"),
		ProjectRoot:  cfg.Root,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: failed to locate Fluxa toolchain\ncaused by: %v\n", err)
		return 1
	}

	identity, err := dependencies.probe(context.Background(), candidate.Path, 5*time.Second)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: failed to identify Fluxa toolchain\ncaused by: %v\n", err)
		return 1
	}
	if err := toolchain.CheckCompatibility(cfg.Toolchain.Fluxa, identity); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: incompatible Fluxa toolchain\ncaused by: %v\n", err)
		return 1
	}

	version := identity.Version
	if version == "" {
		version = "not reported"
	}
	_, err = fmt.Fprintf(
		stdout,
		"Project configuration valid\nProject: %s\nVersion: %s\nEntry: %s\nTarget: %s\nTerminal: %t\n"+
			"Fluxa toolchain selected\nPath: %s\nSource: %s\nVersion: %s\nSHA-256: %s\n",
		cfg.Project.Name,
		cfg.Project.Version,
		cfg.Project.Entry,
		cfg.Build.Target,
		cfg.Build.Terminal,
		candidate.Path,
		candidate.Source,
		version,
		identity.SHA256,
	)
	if err != nil {
		writeString(stderr, "error: failed to write build output\n")
		return 1
	}

	writeString(stderr, "error: build stopped after secure command execution; project preflight is not implemented yet\n")
	return 1
}

func parseBuildOptions(args []string) (buildOptions, error) {
	options := buildOptions{projectPath: "."}
	projectSet := false

	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--fluxa":
			if index+1 >= len(args) {
				return buildOptions{}, fmt.Errorf("--fluxa requires an executable path")
			}
			index++
			options.fluxaPath = args[index]
		default:
			if len(arg) > 0 && arg[0] == '-' {
				return buildOptions{}, fmt.Errorf("unknown build option %q", arg)
			}
			if projectSet {
				return buildOptions{}, fmt.Errorf("build accepts at most one project path")
			}
			options.projectPath = arg
			projectSet = true
		}
	}
	return options, nil
}
