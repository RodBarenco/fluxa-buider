// Package app implements the Fluxa Builder command-line application.
package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	buildpkg "github.com/RodBarenco/fluxa-builder/internal/build"
	"github.com/RodBarenco/fluxa-builder/internal/collector"
	"github.com/RodBarenco/fluxa-builder/internal/compiler"
	"github.com/RodBarenco/fluxa-builder/internal/manifest"
	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
	"github.com/RodBarenco/fluxa-builder/internal/project"
	runtimepkg "github.com/RodBarenco/fluxa-builder/internal/runtime"
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
	case "inspect":
		return runInspect(args[1:], stdout, stderr)
	case "verify":
		return runVerify(args[1:], stdout, stderr)
	case "runtime":
		return runRuntime(args[1:], stdout, stderr)
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
  fluxa-builder build [project] [--fluxa <path>] [--runtime-registry <path>]
                        [--include-source] [--keep-work]
  fluxa-builder inspect <file.flxpkg>
  fluxa-builder verify <file.flxpkg>
  fluxa-builder runtime list [--registry <path>]
  fluxa-builder runtime add <binary> --metadata <runtime.json> [--registry <path>]
  fluxa-builder version
  fluxa-builder help
`, Version)
	return err
}

func writeString(w io.Writer, value string) {
	_, _ = io.WriteString(w, value)
}

type buildOptions struct {
	projectPath     string
	fluxaPath       string
	keepWork        bool
	includeSource   bool
	runtimeRegistry string
}

type buildDependencies struct {
	resolve        func(toolchain.ResolveOptions) (toolchain.Candidate, error)
	probe          func(context.Context, string, time.Duration) (toolchain.Identity, error)
	newWorkspace   func(context.Context, string, buildpkg.WorkspaceOptions) (*buildpkg.Workspace, error)
	collect        func(context.Context, *project.Config) (collector.Result, error)
	compile        func(context.Context, compiler.Request) (compiler.Result, error)
	newManifest    func(context.Context, manifest.Input) (manifest.Manifest, error)
	writeManifest  func(string, manifest.Manifest) error
	writePackage   func(context.Context, flxpkg.Request) (flxpkg.Result, error)
	resolveRuntime func(string, runtimepkg.Requirement) (runtimepkg.Runtime, error)
}

func defaultBuildDependencies() buildDependencies {
	return buildDependencies{
		resolve:        toolchain.Resolve,
		probe:          toolchain.Probe,
		newWorkspace:   buildpkg.NewWorkspace,
		collect:        collector.CollectProject,
		compile:        compiler.Compile,
		newManifest:    manifest.New,
		writeManifest:  manifest.WriteFile,
		writePackage:   flxpkg.Write,
		resolveRuntime: runtimepkg.Resolve,
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

	workspace, err := dependencies.newWorkspace(context.Background(), cfg.Root, buildpkg.WorkspaceOptions{
		KeepWork: options.keepWork,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: failed to create transactional workspace\ncaused by: %v\n", err)
		return 1
	}

	collection, err := dependencies.collect(context.Background(), cfg)
	if err != nil {
		_ = workspace.Cleanup()
		_, _ = fmt.Fprintf(stderr, "error: failed to collect project files\ncaused by: %v\n", err)
		return 1
	}

	compilation, err := dependencies.compile(context.Background(), compiler.Request{
		Files:         collection.Entries,
		OutputDir:     workspace.CompiledDir,
		IncludeSource: options.includeSource || cfg.Package.IncludeSource,
	})
	if err != nil {
		_ = workspace.Cleanup()
		_, _ = fmt.Fprintf(stderr, "error: failed to compile Fluxa project\ncaused by: %v\n", err)
		return 1
	}

	targetOS, targetArch, err := resolveManifestTarget(cfg.Build.Target)
	if err != nil {
		_ = workspace.Cleanup()
		_, _ = fmt.Fprintf(stderr, "error: failed to resolve build target\ncaused by: %v\n", err)
		return 1
	}
	packageManifest, err := dependencies.newManifest(context.Background(), manifest.Input{
		Project:     cfg,
		Toolchain:   identity,
		Compilation: compilation,
		Collection:  collection,
		TargetOS:    targetOS,
		TargetArch:  targetArch,
	})
	if err != nil {
		_ = workspace.Cleanup()
		_, _ = fmt.Fprintf(stderr, "error: failed to create package manifest\ncaused by: %v\n", err)
		return 1
	}
	manifestPath := filepath.Join(workspace.PackageDir, "manifest.json")
	if err := dependencies.writeManifest(manifestPath, packageManifest); err != nil {
		_ = workspace.Cleanup()
		_, _ = fmt.Fprintf(stderr, "error: failed to write package manifest\ncaused by: %v\n", err)
		return 1
	}
	packagePath := filepath.Join(workspace.PackageDir, cfg.Project.ID+"-"+cfg.Project.Version+".flxpkg")
	packageResult, err := dependencies.writePackage(context.Background(), flxpkg.Request{
		OutputPath: packagePath,
		Manifest:   packageManifest,
		Sources:    packageSources(workspace.CompiledDir, collection, compilation),
		Compress:   cfg.Package.Compress,
	})
	if err != nil {
		_ = workspace.Cleanup()
		_, _ = fmt.Fprintf(stderr, "error: failed to write Fluxa package\ncaused by: %v\n", err)
		return 1
	}
	registryRoot, err := buildRegistryRoot(options.runtimeRegistry)
	if err != nil {
		_ = workspace.Cleanup()
		_, _ = fmt.Fprintf(stderr, "error: failed to resolve runtime registry\ncaused by: %v\n", err)
		return 1
	}
	selectedRuntime, err := dependencies.resolveRuntime(registryRoot, runtimepkg.Requirement{
		FluxaVersion:         packageManifest.Toolchain.FluxaVersion,
		ToolchainSHA256:      packageManifest.Toolchain.FluxaSHA256,
		PackageFormatVersion: 1,
		BytecodeVersion:      packageManifest.Toolchain.BytecodeVersion,
		BytecodeABI:          packageManifest.Toolchain.BytecodeABI,
		LibrariesSHA256:      packageManifest.Toolchain.LibrariesSHA256,
		ProgramFormat:        packageManifest.Build.ProgramFormat,
		OS:                   packageManifest.Target.OS,
		Arch:                 packageManifest.Target.Arch,
		Terminal:             packageManifest.Target.Terminal,
	})
	if err != nil {
		_ = workspace.Cleanup()
		_, _ = fmt.Fprintf(stderr, "error: failed to select compatible Fluxa runtime\ncaused by: %v\n", err)
		return 1
	}

	version := identity.Version
	if version == "" {
		version = "not reported"
	}
	_, err = fmt.Fprintf(
		stdout,
		"Project configuration valid\nProject: %s\nVersion: %s\nEntry: %s\nTarget: %s\nTerminal: %t\n"+
			"Fluxa toolchain selected\nPath: %s\nSource: %s\nVersion: %s\nSHA-256: %s\n"+
			"Files collected: %d\nCollected bytes: %d\n",
		cfg.Project.Name,
		cfg.Project.Version,
		cfg.Project.Entry,
		cfg.Build.Target,
		cfg.Build.Terminal,
		candidate.Path,
		candidate.Source,
		version,
		identity.SHA256,
		len(collection.Entries),
		collection.TotalSize,
	)
	if err != nil {
		_ = workspace.Cleanup()
		writeString(stderr, "error: failed to write build output\n")
		return 1
	}
	if compilation.SourceExposed {
		_, err = fmt.Fprintf(
			stdout,
			"Compilation mode: development/source-exposed\nProgram artifacts: %d\n"+
				"WARNING: this artifact contains Fluxa source and is not a secure release\n",
			len(compilation.Artifacts),
		)
		if err != nil {
			_ = workspace.Cleanup()
			writeString(stderr, "error: failed to write compilation output\n")
			return 1
		}
	}
	if _, err = fmt.Fprintf(stdout, "Manifest schema: %d\nManifest files: %d\n", packageManifest.FormatVersion, len(packageManifest.Files)); err != nil {
		_ = workspace.Cleanup()
		writeString(stderr, "error: failed to write manifest output\n")
		return 1
	}
	if _, err = fmt.Fprintf(
		stdout,
		"Fluxa package: %s\nPackage bytes: %d\nPackage SHA-256: %s\n",
		filepath.Base(packageResult.Path),
		packageResult.Size,
		packageResult.SHA256,
	); err != nil {
		_ = workspace.Cleanup()
		writeString(stderr, "error: failed to write package output\n")
		return 1
	}
	if _, err = fmt.Fprintf(
		stdout,
		"Runtime selected: %s\nRuntime SHA-256: %s\n",
		selectedRuntime.BinaryPath,
		selectedRuntime.Metadata.BinarySHA256,
	); err != nil {
		_ = workspace.Cleanup()
		writeString(stderr, "error: failed to write runtime output\n")
		return 1
	}
	if options.keepWork {
		_, _ = fmt.Fprintf(stdout, "Workspace retained: %s\n", workspace.Root)
	} else {
		if err := workspace.Cleanup(); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: failed to clean transactional workspace\ncaused by: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, "Transactional workspace: created and cleaned")
	}

	writeString(stderr, "error: build stopped after verified runtime selection; portable output is not implemented yet\n")
	return 1
}

func packageSources(compiledDir string, collection collector.Result, compilation compiler.Result) map[string]string {
	sources := make(map[string]string, len(compilation.Artifacts)+len(collection.Entries))
	for _, artifact := range compilation.Artifacts {
		sources["program/"+artifact.Path] = filepath.Join(compiledDir, filepath.FromSlash(artifact.Path))
	}
	for _, entry := range collection.Entries {
		if entry.Kind == collector.KindAsset {
			sources["resources/"+entry.Path] = entry.SourcePath
		}
	}
	return sources
}

func runVerify(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		writeString(stderr, "error: verify requires exactly one .flxpkg path\n")
		return 2
	}
	info, err := flxpkg.Verify(args[0])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: package verification failed\ncaused by: %v\n", err)
		return 1
	}
	if _, err := fmt.Fprintf(stdout, "valid Fluxa package\nFiles: %d\nBytes: %d\nSHA-256: %s\n", len(info.Entries), info.Size, info.SHA256); err != nil {
		writeString(stderr, "error: failed to write verification output\n")
		return 1
	}
	return 0
}

func runInspect(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		writeString(stderr, "error: inspect requires exactly one .flxpkg path\n")
		return 2
	}
	info, err := flxpkg.Verify(args[0])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: package inspection failed\ncaused by: %v\n", err)
		return 1
	}
	if _, err := fmt.Fprintf(
		stdout,
		"Fluxa Package v%d\nProject: %s\nID: %s\nVersion: %s\nTarget: %s/%s\n"+
			"Program format: %s\nSource exposed: %t\nFiles: %d\nBytes: %d\nSHA-256: %s\n",
		info.FormatVersion,
		info.Manifest.Project.Name,
		info.Manifest.Project.ID,
		info.Manifest.Project.Version,
		info.Manifest.Target.OS,
		info.Manifest.Target.Arch,
		info.Manifest.Build.ProgramFormat,
		info.Manifest.Build.SourceExposed,
		len(info.Entries),
		info.Size,
		info.SHA256,
	); err != nil {
		writeString(stderr, "error: failed to write inspection output\n")
		return 1
	}
	return 0
}

func buildRegistryRoot(explicit string) (string, error) {
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	return runtimepkg.DefaultRoot(os.Getenv("FLUXA_BUILDER_HOME"))
}

func runRuntime(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeString(stderr, "error: runtime requires list or add\n")
		return 2
	}
	switch args[0] {
	case "list":
		return runRuntimeList(args[1:], stdout, stderr)
	case "add":
		return runRuntimeAdd(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "error: unknown runtime command %q\n", args[0])
		return 2
	}
}

func runRuntimeList(args []string, stdout, stderr io.Writer) int {
	registry, err := parseRegistryOnly(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	root, err := buildRegistryRoot(registry)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: failed to resolve runtime registry\ncaused by: %v\n", err)
		return 1
	}
	values, err := runtimepkg.List(root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: failed to list runtimes\ncaused by: %v\n", err)
		return 1
	}
	if len(values) == 0 {
		writeString(stdout, "No Fluxa runtimes registered\n")
		return 0
	}
	for _, value := range values {
		_, err := fmt.Fprintf(
			stdout,
			"%s  %s/%s  terminal=%t  formats=%s  sha256=%s\n",
			value.Metadata.FluxaVersion,
			value.Metadata.OS,
			value.Metadata.Arch,
			value.Metadata.Terminal,
			strings.Join(value.Metadata.ProgramFormats, ","),
			value.Metadata.BinarySHA256,
		)
		if err != nil {
			writeString(stderr, "error: failed to write runtime list\n")
			return 1
		}
	}
	return 0
}

func runRuntimeAdd(args []string, stdout, stderr io.Writer) int {
	binary, metadataPath, registry, err := parseRuntimeAdd(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	root, err := buildRegistryRoot(registry)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: failed to resolve runtime registry\ncaused by: %v\n", err)
		return 1
	}
	metadata, err := runtimepkg.ReadMetadata(metadataPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: failed to read runtime metadata\ncaused by: %v\n", err)
		return 1
	}
	added, err := runtimepkg.Add(root, binary, metadata)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: failed to add runtime\ncaused by: %v\n", err)
		return 1
	}
	if _, err := fmt.Fprintf(stdout, "Runtime added: %s\nSHA-256: %s\n", added.BinaryPath, added.Metadata.BinarySHA256); err != nil {
		writeString(stderr, "error: failed to write runtime result\n")
		return 1
	}
	return 0
}

func parseRegistryOnly(args []string) (string, error) {
	registry := ""
	for index := 0; index < len(args); index++ {
		if args[index] != "--registry" || index+1 >= len(args) {
			return "", fmt.Errorf("runtime list accepts only --registry <path>")
		}
		index++
		registry = args[index]
	}
	return registry, nil
}

func parseRuntimeAdd(args []string) (string, string, string, error) {
	binary := ""
	metadata := ""
	registry := ""
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--metadata":
			if index+1 >= len(args) {
				return "", "", "", fmt.Errorf("--metadata requires a file path")
			}
			index++
			metadata = args[index]
		case "--registry":
			if index+1 >= len(args) {
				return "", "", "", fmt.Errorf("--registry requires a directory path")
			}
			index++
			registry = args[index]
		default:
			if strings.HasPrefix(args[index], "-") || binary != "" {
				return "", "", "", fmt.Errorf("runtime add accepts exactly one binary path")
			}
			binary = args[index]
		}
	}
	if binary == "" || metadata == "" {
		return "", "", "", fmt.Errorf("runtime add requires <binary> and --metadata <runtime.json>")
	}
	return binary, metadata, registry, nil
}

func resolveManifestTarget(configured string) (string, string, error) {
	if configured == "host" {
		osName := runtime.GOOS
		if osName == "darwin" {
			osName = "macos"
		}
		return osName, runtime.GOARCH, nil
	}
	parts := strings.Split(configured, "-")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("target %q must be host or <os>-<arch>", configured)
	}
	osName := parts[0]
	if osName != "windows" && osName != "linux" && osName != "macos" {
		return "", "", fmt.Errorf("unsupported target operating system %q", osName)
	}
	arch := parts[1]
	switch arch {
	case "x64":
		arch = "amd64"
	case "arm64":
	default:
		return "", "", fmt.Errorf("unsupported target architecture %q", arch)
	}
	return osName, arch, nil
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
		case "--keep-work":
			options.keepWork = true
		case "--include-source":
			options.includeSource = true
		case "--runtime-registry":
			if index+1 >= len(args) {
				return buildOptions{}, fmt.Errorf("--runtime-registry requires a directory path")
			}
			index++
			options.runtimeRegistry = args[index]
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
