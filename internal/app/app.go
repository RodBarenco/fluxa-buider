// Package app implements the Fluxa Builder command-line application.
package app

import (
	"context"
	"errors"
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
	"github.com/RodBarenco/fluxa-builder/internal/embedded"
	"github.com/RodBarenco/fluxa-builder/internal/installer"
	"github.com/RodBarenco/fluxa-builder/internal/manifest"
	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
	"github.com/RodBarenco/fluxa-builder/internal/portable"
	"github.com/RodBarenco/fluxa-builder/internal/project"
	"github.com/RodBarenco/fluxa-builder/internal/runner"
	runtimepkg "github.com/RodBarenco/fluxa-builder/internal/runtime"
	"github.com/RodBarenco/fluxa-builder/internal/signing"
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
	case "run-package":
		return runPackage(args[1:], stdout, stderr)
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
                        [--sign-key <path>] [--embed] [--include-source] [--keep-work]
  fluxa-builder inspect <file.flxpkg>
  fluxa-builder verify <file.flxpkg> [--signature <file.sig> --public-key <signing.pub>]
  fluxa-builder run-package <file.flxpkg> --fluxa <path>
  fluxa-builder runtime list [--registry <path>]
  fluxa-builder runtime add <binary> --metadata <runtime.json> [--registry <path>]
  fluxa-builder version
  fluxa-builder help
`, Version)
	return err
}

func runPackage(args []string, stdout, stderr io.Writer) int {
	if len(args) != 3 || args[1] != "--fluxa" || args[0] == "" || args[2] == "" {
		writeString(stderr, "error: usage: fluxa-builder run-package <file.flxpkg> --fluxa <path>\n")
		return 2
	}
	if err := runner.Run(context.Background(), runner.Request{
		PackagePath: args[0],
		RuntimePath: args[2],
		Stdin:       os.Stdin,
		Stdout:      stdout,
		Stderr:      stderr,
	}); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: failed to run Fluxa package\ncaused by: %v\n", err)
		return 1
	}
	return 0
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
	signKeyPath     string
	embed           bool
}

type buildDependencies struct {
	resolve         func(toolchain.ResolveOptions) (toolchain.Candidate, error)
	probe           func(context.Context, string, time.Duration) (toolchain.Identity, error)
	newWorkspace    func(context.Context, string, buildpkg.WorkspaceOptions) (*buildpkg.Workspace, error)
	collect         func(context.Context, *project.Config) (collector.Result, error)
	compile         func(context.Context, compiler.Request) (compiler.Result, error)
	newManifest     func(context.Context, manifest.Input) (manifest.Manifest, error)
	writeManifest   func(string, manifest.Manifest) error
	writePackage    func(context.Context, flxpkg.Request) (flxpkg.Result, error)
	signPackage     func(string, string, string) (signing.Result, error)
	resolveRuntime  func(string, runtimepkg.Requirement) (runtimepkg.Runtime, error)
	buildPortable   func(context.Context, portable.Request) (portable.Result, error)
	smokePortable   func(context.Context, portable.Result, time.Duration) error
	archivePortable func(context.Context, portable.Result, string) (portable.ArchiveResult, error)
	buildDebian     func(context.Context, installer.Request) (installer.Result, error)
	buildEmbedded   func(context.Context, embedded.Request) (embedded.Info, error)
	smokeExecutable func(context.Context, string, string, string, time.Duration) (portable.SmokeReport, error)
	executablePath  func() (string, error)
	getenv          func(string) string
}

func defaultBuildDependencies() buildDependencies {
	return buildDependencies{
		resolve:         toolchain.Resolve,
		probe:           toolchain.Probe,
		newWorkspace:    buildpkg.NewWorkspace,
		collect:         collector.CollectProject,
		compile:         compiler.Compile,
		newManifest:     manifest.New,
		writeManifest:   manifest.WriteFile,
		writePackage:    flxpkg.Write,
		signPackage:     signing.Sign,
		resolveRuntime:  runtimepkg.Resolve,
		buildPortable:   portable.Build,
		smokePortable:   portable.Smoke,
		archivePortable: portable.Archive,
		buildDebian:     installer.Debian{}.Build,
		buildEmbedded:   embedded.Build,
		smokeExecutable: portable.SmokeExecutable,
		executablePath:  os.Executable,
		getenv:          os.Getenv,
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
	if cfg.Package.Format != "portable" {
		_, _ = fmt.Fprintf(stderr, "error: package format %q is not implemented yet; use portable\n", cfg.Package.Format)
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
	signKeyPath := resolveSignKeyPath(options.signKeyPath, dependencies.getenv)
	if options.embed && signKeyPath != "" {
		_ = workspace.Cleanup()
		writeString(stderr, "error: --embed cannot yet be combined with package signing; no artifact was published\n")
		return 1
	}
	var signatureResult signing.Result
	if signKeyPath != "" {
		signatureResult, err = dependencies.signPackage(packageResult.Path, signKeyPath, packageResult.Path+".sig")
		if err != nil {
			_ = workspace.Cleanup()
			_, _ = fmt.Fprintf(stderr, "error: failed to sign Fluxa package\ncaused by: %v\n", err)
			return 1
		}
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
	targetName := targetDirectoryName(packageManifest.Target.OS, packageManifest.Target.Arch)
	targetStage := filepath.Join(workspace.OutputDir, targetName)
	if err := os.Mkdir(targetStage, 0o700); err != nil {
		_ = workspace.Cleanup()
		_, _ = fmt.Fprintf(stderr, "error: failed to create target staging directory\ncaused by: %v\n", err)
		return 1
	}
	publishTarget := filepath.Join(cfg.OutputPath, targetName)
	publishDestination := ""
	var portableResult portable.Result
	var archiveResult portable.ArchiveResult
	var installerResult installer.Result
	var embeddedResult embedded.Info
	if options.embed {
		applicationName := portable.ArtifactName(cfg.Project.Name, cfg.Project.ID)
		if packageManifest.Target.OS == "windows" {
			applicationName += ".exe"
		}
		embeddedResult, err = dependencies.buildEmbedded(context.Background(), embedded.Request{
			RuntimePath: selectedRuntime.BinaryPath, PackagePath: packageResult.Path,
			OutputPath:  filepath.Join(targetStage, applicationName),
			PackageHash: packageResult.SHA256, ExecutableOS: packageManifest.Target.OS,
		})
		if err != nil {
			_ = workspace.Cleanup()
			_, _ = fmt.Fprintf(stderr, "error: failed to build embedded executable\ncaused by: %v\n", err)
			return 1
		}
		if !hostCanExecuteTarget(packageManifest.Target.OS, packageManifest.Target.Arch) {
			_ = workspace.Cleanup()
			_, _ = fmt.Fprintf(
				stderr,
				"error: cannot smoke-test target %s/%s on this host; artifact was not published\n",
				packageManifest.Target.OS,
				packageManifest.Target.Arch,
			)
			return 1
		}
		if _, err := dependencies.smokeExecutable(
			context.Background(), embeddedResult.Path, targetStage, embeddedResult.PackageSHA256, 10*time.Second,
		); err != nil {
			_ = workspace.Cleanup()
			_, _ = fmt.Fprintf(stderr, "error: embedded executable smoke test failed\ncaused by: %v\n", err)
			return 1
		}
		publishDestination = filepath.Join(publishTarget, applicationName)
		if err := workspace.Publish(context.Background(), embeddedResult.Path, publishDestination); err != nil {
			_ = workspace.Cleanup()
			_, _ = fmt.Fprintf(stderr, "error: failed to publish embedded executable\ncaused by: %v\n", err)
			return 1
		}
	} else {
		windowsIcon := ""
		linuxIcon := ""
		macOSIcon := ""
		if packageManifest.Target.OS == "windows" && cfg.Targets.Windows.Icon != "" {
			windowsIcon = filepath.Join(cfg.Root, filepath.FromSlash(cfg.Targets.Windows.Icon))
		}
		if packageManifest.Target.OS == "linux" && cfg.Targets.Linux.Icon != "" {
			linuxIcon = filepath.Join(cfg.Root, filepath.FromSlash(cfg.Targets.Linux.Icon))
		}
		if packageManifest.Target.OS == "macos" && cfg.Targets.MacOS.Icon != "" {
			macOSIcon = filepath.Join(cfg.Root, filepath.FromSlash(cfg.Targets.MacOS.Icon))
		}
		portableResult, err = dependencies.buildPortable(context.Background(), portable.Request{
			OutputRoot:    targetStage,
			ProjectName:   cfg.Project.Name,
			ProjectID:     cfg.Project.ID,
			Version:       cfg.Project.Version,
			TargetOS:      packageManifest.Target.OS,
			TargetArch:    packageManifest.Target.Arch,
			Terminal:      packageManifest.Target.Terminal,
			PackagePath:   packageResult.Path,
			PackageSHA256: packageResult.SHA256,
			Runtime:       selectedRuntime,
			LauncherPath:  currentExecutable(dependencies.executablePath),
			SourceExposed: packageManifest.Build.SourceExposed,
			SignaturePath: signatureResult.Path,
			SignatureHash: signatureResult.SHA256,
			SigningKeyID:  signatureResult.KeyID,
			WindowsIcon:   windowsIcon,
			LinuxIcon:     linuxIcon,
			MacOSIcon:     macOSIcon,
			BundleID:      cfg.Targets.MacOS.BundleID,
		})
		if err != nil {
			_ = workspace.Cleanup()
			_, _ = fmt.Fprintf(stderr, "error: failed to assemble portable application\ncaused by: %v\n", err)
			return 1
		}
		if !hostCanExecuteTarget(packageManifest.Target.OS, packageManifest.Target.Arch) {
			_ = workspace.Cleanup()
			_, _ = fmt.Fprintf(
				stderr,
				"error: cannot smoke-test target %s/%s on this host; artifact was not published\n",
				packageManifest.Target.OS,
				packageManifest.Target.Arch,
			)
			return 1
		}
		if err := dependencies.smokePortable(context.Background(), portableResult, 10*time.Second); err != nil {
			_ = workspace.Cleanup()
			_, _ = fmt.Fprintf(stderr, "error: portable application smoke test failed\ncaused by: %v\n", err)
			return 1
		}
		archiveResult, err = dependencies.archivePortable(context.Background(), portableResult, packageManifest.Target.OS)
		if err != nil {
			_ = workspace.Cleanup()
			_, _ = fmt.Fprintf(stderr, "error: failed to create distribution archive\ncaused by: %v\n", err)
			return 1
		}
		if packageManifest.Target.OS == "linux" {
			installerResult, err = dependencies.buildDebian(context.Background(), installer.Request{
				OutputDir: targetStage, ProjectName: cfg.Project.Name,
				ProjectID: cfg.Project.ID, Version: cfg.Project.Version,
				Terminal: cfg.Build.Terminal, Portable: portableResult,
			})
			if err != nil {
				_ = workspace.Cleanup()
				_, _ = fmt.Fprintf(stderr, "error: failed to create Debian installer\ncaused by: %v\n", err)
				return 1
			}
		}
		if err := workspace.Publish(context.Background(), targetStage, publishTarget); err != nil {
			_ = workspace.Cleanup()
			_, _ = fmt.Fprintf(stderr, "error: failed to publish portable application and archive\ncaused by: %v\n", err)
			return 1
		}
		publishDestination = filepath.Join(publishTarget, portableResult.Name)
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
	if signatureResult.Path != "" {
		if _, err = fmt.Fprintf(
			stdout,
			"Package signature: %s\nSigning key ID: %s\nSignature SHA-256: %s\n",
			filepath.Base(signatureResult.Path),
			signatureResult.KeyID,
			signatureResult.SHA256,
		); err != nil {
			_ = workspace.Cleanup()
			writeString(stderr, "error: failed to write signature output\n")
			return 1
		}
		if installerResult.Path != "" {
			if _, err = fmt.Fprintf(
				stdout,
				"Native installer: %s\nInstaller format: %s\nInstaller bytes: %d\nInstaller SHA-256: %s\n",
				filepath.Join(publishTarget, filepath.Base(installerResult.Path)),
				installerResult.Format,
				installerResult.Size,
				installerResult.SHA256,
			); err != nil {
				_ = workspace.Cleanup()
				writeString(stderr, "error: failed to write installer output\n")
				return 1
			}
		}
	}
	if options.embed {
		if _, err = fmt.Fprintf(
			stdout,
			"Embedded executable verified\nOutput: %s\nExecutable bytes: %d\nExecutable SHA-256: %s\nPackage offset: %d\n",
			publishDestination, embeddedResult.Size, embeddedResult.SHA256, embeddedResult.PackageOffset,
		); err != nil {
			_ = workspace.Cleanup()
			writeString(stderr, "error: failed to write embedded output\n")
			return 1
		}
	} else {
		if _, err = fmt.Fprintf(
			stdout,
			"Portable application verified\nOutput: %s\nExecutable: %s\nPackage: %s\n",
			publishDestination,
			filepath.Base(portableResult.Executable),
			filepath.Base(portableResult.Package),
		); err != nil {
			_ = workspace.Cleanup()
			writeString(stderr, "error: failed to write portable output\n")
			return 1
		}
		if _, err = fmt.Fprintf(
			stdout,
			"Distribution archive: %s\nArchive format: %s\nArchive bytes: %d\nArchive SHA-256: %s\nChecksum: %s\n",
			filepath.Join(publishTarget, filepath.Base(archiveResult.Path)),
			archiveResult.Format,
			archiveResult.Size,
			archiveResult.SHA256,
			filepath.Join(publishTarget, filepath.Base(archiveResult.ChecksumPath)),
		); err != nil {
			_ = workspace.Cleanup()
			writeString(stderr, "error: failed to write archive output\n")
			return 1
		}
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

	return 0
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

func currentExecutable(resolve func() (string, error)) string {
	if resolve == nil {
		return ""
	}
	path, err := resolve()
	if err != nil {
		return ""
	}
	return path
}

func runVerify(args []string, stdout, stderr io.Writer) int {
	packagePath, signaturePath, publicKeyPath, err := parseVerifyOptions(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	info, err := flxpkg.Verify(packagePath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: package verification failed\ncaused by: %v\n", err)
		return 1
	}
	var signatureResult signing.Result
	if publicKeyPath != "" {
		if signaturePath == "" {
			signaturePath = packagePath + ".sig"
		}
		signatureResult, err = signing.Verify(packagePath, signaturePath, publicKeyPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "error: package signature verification failed\ncaused by: %v\n", err)
			return 1
		}
	}
	if _, err := fmt.Fprintf(stdout, "valid Fluxa package\nFiles: %d\nBytes: %d\nSHA-256: %s\n", len(info.Entries), info.Size, info.SHA256); err != nil {
		writeString(stderr, "error: failed to write verification output\n")
		return 1
	}
	if signatureResult.Path != "" {
		if _, err := fmt.Fprintf(stdout, "valid Ed25519 signature\nKey ID: %s\nSignature SHA-256: %s\n", signatureResult.KeyID, signatureResult.SHA256); err != nil {
			writeString(stderr, "error: failed to write signature verification output\n")
			return 1
		}
	}
	return 0
}

func parseVerifyOptions(args []string) (packagePath, signaturePath, publicKeyPath string, err error) {
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--signature":
			if index+1 >= len(args) {
				return "", "", "", errors.New("--signature requires a file path")
			}
			index++
			signaturePath = args[index] // #nosec G602 -- bounds checked before increment.
		case "--public-key":
			if index+1 >= len(args) {
				return "", "", "", errors.New("--public-key requires a file path")
			}
			index++
			publicKeyPath = args[index] // #nosec G602 -- bounds checked before increment.
		default:
			if strings.HasPrefix(args[index], "-") {
				return "", "", "", fmt.Errorf("unknown verify option %q", args[index])
			}
			if packagePath != "" {
				return "", "", "", errors.New("verify accepts exactly one .flxpkg path")
			}
			packagePath = args[index]
		}
	}
	if packagePath == "" {
		return "", "", "", errors.New("verify requires exactly one .flxpkg path")
	}
	if signaturePath != "" && publicKeyPath == "" {
		return "", "", "", errors.New("--signature requires --public-key")
	}
	return packagePath, signaturePath, publicKeyPath, nil
}

func resolveSignKeyPath(explicit string, getenv func(string) string) string {
	if explicit != "" {
		return explicit
	}
	return getenv("FLUXA_SIGN_KEY")
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
	if osName == "windows" && arch != "amd64" {
		return "", "", errors.New("official Windows target currently supports x64 only")
	}
	if osName == "linux" && arch != "amd64" {
		return "", "", errors.New("official Linux target currently supports x64 only")
	}
	return osName, arch, nil
}

func hostCanExecuteTarget(osName, arch string) bool {
	hostOS := runtime.GOOS
	if hostOS == "darwin" {
		hostOS = "macos"
	}
	return osName == hostOS && arch == runtime.GOARCH
}

func targetDirectoryName(osName, arch string) string {
	if arch == "amd64" {
		arch = "x64"
	}
	return osName + "-" + arch
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
		case "--embed":
			options.embed = true
		case "--runtime-registry":
			if index+1 >= len(args) {
				return buildOptions{}, fmt.Errorf("--runtime-registry requires a directory path")
			}
			index++
			options.runtimeRegistry = args[index]
		case "--sign-key":
			if index+1 >= len(args) {
				return buildOptions{}, fmt.Errorf("--sign-key requires a private key path")
			}
			index++
			options.signKeyPath = args[index]
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
