# ADR 0024: Interactive `init` wizard, scoped to guidance and safe TOML edits

- Status: Accepted
- Date: 2026-08-04

## Context

Every existing command is fully non-interactive: the user must already know
`fluxa.toml`'s Builder-specific fields, already have a Fluxa toolchain on
`PATH`/`FLUXA_HOME`/`toolchain.path`, and already have a compatible runtime
registered before `build` produces anything. Nothing in the CLI detects the
host platform for the user, asks where a project or its output live, or helps
someone go from "I have a Fluxa project" to "I have a working build" without
reading `docs/distribution.md` end to end.

The user asked for a guided front door, in the spirit of an installer wizard:
detect the host, ask for the project directory, ask which platform to target,
ask where to save output, help complete `fluxa.toml` when it is missing
Builder-required fields, and either walk the user through manual toolchain
setup or run the real build when everything is already in place.

Two things were considered and deliberately excluded from this pass:

1. **Automatic download-and-build of the Fluxa toolchain.** Fluxa is a C99
   project (`github.com/RodBarenco/fluxa-lang`) with no prebuilt binaries;
   obtaining a runtime means cloning that repository and running
   `make build-packaged`, which requires a C compiler and, depending on which
   `fluxa.libs` backends a project uses, system libraries such as libcurl,
   libsodium, libserialport, or raylib. Automating that clone-and-build safely
   (dependency detection, backend flag selection, sandboxing an arbitrary
   `make` invocation, cleanup) is its own project with its own security
   review — the same reasoning that already deferred automatic Fluxa preflight
   (ADR 0006) and put automatic runtime download behind explicit signed-index
   requirements in the original implementation plan (Phase 21). The wizard
   still asks the automatic-vs-manual question, because the user explicitly
   wanted that choice presented; answering "automatic" prints that it is not
   available yet and falls through to the manual guide. This is a real,
   temporary product limitation, not a bug to hide.
2. **A `--target` build flag.** `hostCanExecuteTarget` already restricts
   publishing to the host's own OS/architecture, because the smoke test that
   must pass before anything ships runs the produced application. A wizard
   flag that let someone "choose" `windows-x64` on a Linux machine would just
   fail later at that same check. The target-selection step is therefore
   informational/expectation-setting only.

## Decision

`fluxa-builder init` is a new, argument-free subcommand implemented in
`internal/app/init.go` as a thin interactive layer in front of the existing
build pipeline — it contains no new build logic. Once a toolchain and at
least one registered runtime are found, it constructs the equivalent
`build` arguments and calls `runBuild` directly, reusing 100% of the
already-tested pipeline and its output formatting.

Several small, general-purpose primitives were added to `internal/project` to
support it, all usable outside the wizard:

- `Config.SetOutput(relative string) error` applies the exact same
  relative/no-traversal/stays-inside-root check `validate` already applies to
  `build.output`, letting `build` accept a new `--output <dir>` flag (already
  sketched, but never implemented, in the original CLI design). `init` uses it
  to preview an output directory before persisting anything.
- `EnsureStringField`, `EnsureBoolField`, and `EnsureStringArrayField(root,
  table, key, ..., value) (changed bool, err error)` share one strictly
  additive `fluxa.toml` editor (`ensureField`): each decodes the file to
  check whether `table.key` already exists (never overwriting an existing
  value, regardless of what it is) and, if not, appends the missing key or
  table by text insertion — every other byte of the file, including Fluxa's
  own `[runtime]`/`[libs]`/`[ffi]`/`[security]` tables and any comments, is
  left untouched. `table` may be a dotted nested-table header exactly as
  written in the file (`"targets.macos"`), resolved against the decoded
  document by walking each dotted segment. Unlike `manifest.WriteFile`'s
  never-overwrite semantics (publishing an immutable build artifact), this
  editor *does* overwrite the file in place — deliberately, since it is
  editing a mutable project file the user is expected to keep iterating on —
  but always atomically (temp file, fsync, rename) and never destructively.
- `HasField(root, table, key) (bool, error)` exposes the same presence check
  standalone, so the wizard can decide whether to even ask a question before
  prompting for a value it would just discard.
- `ValidPattern(pattern string) bool` reuses the exact safety/glob-syntax
  rule `validate` applies to every entry of `build.assets`/`exclude`/
  `persistent`/`export`, so the wizard can reject a bad pattern before ever
  writing it, instead of writing first and finding out from a later
  `project.Load` failure.

The wizard uses these primitives behind an explicit on-screen preview and a
yes/no confirmation before every write. Two passes cover the practical
Builder-specific surface of `fluxa.toml`:

1. **Required `[project]` fields** (`name`, `id`, `version`, `entry`) —
   `project.Load`'s returned `*project.Error.Field` names exactly which one
   to ask for, so this pass only asks for what is actually missing or
   invalid, one field at a time, retrying `Load` after each write.
2. **Optional settings**, offered once after the project loads
   (`optionalProjectSettings`), each independently skippable and each
   skipped outright (with a one-line notice, no prompt at all) when
   `HasField` reports it is already configured: `build.assets`,
   `build.exclude`, and `build.persistent` as comma-separated pattern lists;
   `build.export` (only offered when `build.persistent` ends up non-empty in
   this session, and validated as a subset of it before being offered for
   write — the same constraint `validate` enforces); `package.include_source`
   (explained, since it is what today's fail-closed compilation actually
   requires); and the host platform's `targets.<os>.icon` plus, on macOS
   only, `targets.macos.bundle_id`. `toolchain.path` is deliberately *not*
   part of this pass — it is offered separately, inline in `setupOrBuild`,
   and only right after the user had to type an explicit toolchain path this
   session, which is the one case where a future run would otherwise need
   `--fluxa` again.

When setup is incomplete, the wizard prints a manual guide: the
`git clone .../fluxa-lang && make build-packaged` steps, and a best-effort
`runtime.json` template (host OS/arch, `cfg.Build.Terminal`, `packaged: true`,
`program_formats: ["fluxa-source"]`, plus `toolchain_sha256`/`fluxa_version`
when a toolchain was already found, and `binary_name`/`binary_sha256` hashed
live if the user already has a built runtime binary to point at — the same
hash `runtime add` itself will verify). Fields that cannot be known yet are
left empty; the template is not expected to pass `ReadMetadata`'s strict
validation until the user fills in the rest.

## Consequences

`fluxa-builder init` gives a first-time user, and `build --output` gives any
user, real value today without any new trust boundary: no code is downloaded
or executed automatically, every file write requires the user to see the
exact change and confirm it, and the pipeline delegation means the wizard
inherits every safety property `build` already has (transactional workspace,
fail-closed compilation, smoke test before publish). Automatic toolchain
acquisition remains explicitly future work, tracked here rather than
half-implemented.
