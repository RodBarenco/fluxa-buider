# ADR 0029: Cross-target application launcher

- Status: Accepted
- Date: 2026-08-07

## Context

Every portable application Fluxa Builder assembles needs an executable the
end user actually double-clicks: something that finds the FLXPKG beside
itself, verifies it, and hands it to the packaged runtime. Until now that
executable was **the Fluxa Builder binary itself, renamed** —
`cmd/fluxa-builder`'s own `main` checks `app.IsInstalledInvocation(os.Args[0])`
and, when its basename is not `fluxa-builder`, behaves as an application
launcher instead of a CLI.

That works, and is a genuinely economical design, on one condition nobody
had stated: that the build target matches the host. It does not. ADR 0028
made cross-target builds a real, supported thing, and `internal/app`'s
`runBuild` was still passing `os.Executable()` as `portable.Request.LauncherPath`
for every target. Assembling a Windows application on a Linux machine
therefore copied a Linux ELF and named it `<app>.exe`. The very next step —
`internal/windows.ConfigureTerminal`, which patches the PE subsystem field —
rejected it:

```
unrecognized PE machine: 0x457f
```

`0x457f` is `\x7fE`: the ELF magic number, read as a PE machine field. This
surfaced only after ADR 0027's toolchain-acquisition fix, because before
that the Windows flow died earlier, at runtime resolution, and never
reached portable assembly at all.

No amount of toolchain or runtime work can fix this. The toolchain compiles
the project and the runtime executes it; the launcher is a third, separate
binary, and it has to be built for the target like anything else that ships
to a user's machine.

## Decision

### `cmd/fluxa-launcher`: the launcher as its own program

The launcher logic moves out of `internal/app` into `internal/launcher`
(unchanged, only renamed: `IsInstalledInvocation` → `IsInvocation`,
`RunInstalled` → `Run`), and `cmd/fluxa-launcher` is a ~20-line `main`
over it. `internal/app` keeps `IsInstalledInvocation`/`RunInstalled` as
thin delegating wrappers, and `cmd/fluxa-builder`'s renamed-binary entry
point stays exactly as it was, so **applications distributed before this
change keep working** — that path is now backwards compatibility rather
than the current mechanism.

Three alternatives were considered and rejected:

- **Embedding a cross-compiled `fluxa-builder.exe`** — simplest to write,
  but it puts the entire build pipeline (Docker orchestration, package
  signing, the init wizard) inside every application a user distributes,
  and nearly doubles the Builder's own binary size.
- **Cross-compiling at build time with the host's Go toolchain** — six
  seconds and zero repository weight, but it makes Go a prerequisite for
  using Fluxa Builder. Fluxa Builder's users are Fluxa developers; they
  have no reason to have Go installed.
- **Cross-compiling inside a container**, this project's usual answer
  (ADR 0027) — but a distributed Fluxa Builder does not ship its own
  source, so there would be nothing to compile.

### `internal/launcherbin`: committed, per-target binaries

`make launcher` cross-compiles `cmd/fluxa-launcher` for every supported
target and `internal/launcherbin` embeds the results, exactly the pattern
`internal/wrapper` already established for the runtime relay (ADR 0025),
including the drift test that rebuilds each from source and fails if a
committed copy is stale. `runBuild` writes the target's launcher into the
transactional workspace and passes *that* to `internal/portable`, which is
otherwise untouched — it still just copies the file it is given.

Targets committed: `linux-amd64`, `windows-amd64`, `darwin-amd64`,
`darwin-arm64`. `launcherbin.For` fails with a clear, target-naming error
rather than falling back to anything, since every silent fallback here
produces exactly the class of broken artifact this ADR exists to prevent.

One flag differs from `make wrapper`: `-ldflags="-s -w"`. Unlike the relay,
a copy of the launcher ships inside every application a user distributes,
where symbol tables and DWARF are weight nobody will ever debug with. It
cuts each launcher from ~6.5 MB to ~4.2 MB, in the repository and in every
distributed application alike. The drift test rebuilds with the same flags.

Every target now uses the embedded launcher, not only cross-target builds.
Making native builds keep copying the Builder executable would have left
two different launchers in circulation for the same platform depending on
where the build ran, and the uniform path is also the smaller one: a
distributed application's executable drops from ~19.7 MB (the whole Fluxa
Builder) to ~4.2 MB.

## Consequences

A Windows application can now be built end to end on a Linux machine. This
was verified for real, not inferred: a full `fluxa-builder build --target
windows-x64` from this Linux host produced a PE32+ console executable, the
packaged Windows runtime, the FLXPKG, and the four Mesa3D fallback DLLs
with their `.exe.local` redirection marker — and passed ADR 0028's Wine
container smoke test ("Portable application verified"), meaning the
produced `.exe` genuinely ran and answered its own self-test.

The repository carries ~18 MB of committed launcher binaries, regenerated
by `make launcher` (wired into `make build`, like `make wrapper`) and
guarded against drift by `internal/launcherbin`'s test.
`internal/launcherbin`'s Windows binary is additionally validated as a real
amd64 PE by `internal/windows.ValidatePEAMD64`, so the specific failure
that motivated this ADR cannot silently return.

Adding a new target platform now means adding it in three places that
already had to change together anyway: `make launcher`, `launcherbin.For`,
and the drift test's target table. A missing entry fails the build loudly
with "no embedded application launcher for <os>/<arch>" instead of
producing a subtly wrong artifact.
