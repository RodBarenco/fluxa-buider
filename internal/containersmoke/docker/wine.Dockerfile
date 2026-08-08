# Runs a real Windows self-test executable via Wine, network-isolated, for
# internal/containersmoke.RunWindows. See
# docs/adr/0028-container-verified-cross-platform-builds.md.
FROM debian:bookworm-slim

# Debian's own bundled `wine` package is stale/inconsistently available
# across suites; WineHQ's own apt repo is the documented, supported way to
# get a current Wine (https://wiki.winehq.org/Debian). WineHQ's packaging
# depends on i386 multiarch even for a 64-bit-only target (this project's
# Windows build is x64-only, but wine-stable itself is not split cleanly
# from its 32-bit dependencies on Debian).
#
# Pinned to an exact version, not just "winehq-stable" (which silently
# tracks whatever WineHQ currently publishes) — the same determinism
# rule this project already applies to its Debian base image (pinned by
# digest), mesa-dist-win, and raylib (both pinned by exact
# version/commit). A future Wine release that regresses something this
# project depends on cannot silently break Fluxa Builder: bumping this
# version is a deliberate, reviewable change, not something that just
# happens on the next image rebuild. See docs/adr/0028's Consequences
# section for what happens if the pinned version does break: the build
# still publishes, with a warning, rather than being blocked outright.
#
# All four related sub-packages must be pinned together, not just
# winehq-stable: apt does not cascade a version pin through its own
# dependencies, and installing them at mismatched versions fails
# outright.
ARG WINEHQ_VERSION=11.0.0.0~bookworm-1
RUN dpkg --add-architecture i386 \
    && apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates gnupg2 wget xz-utils xvfb xauth \
    && mkdir -pm755 /etc/apt/keyrings \
    && wget -O /etc/apt/keyrings/winehq-archive.key https://dl.winehq.org/wine-builds/winehq.key \
    && wget -NP /etc/apt/sources.list.d/ https://dl.winehq.org/wine-builds/debian/dists/bookworm/winehq-bookworm.sources \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        winehq-stable=${WINEHQ_VERSION} wine-stable=${WINEHQ_VERSION} \
        wine-stable-amd64=${WINEHQ_VERSION} wine-stable-i386=${WINEHQ_VERSION} \
    && rm -rf /var/lib/apt/lists/*

# Every self-test invocation runs as the host's own uid:gid (see
# dockerRunIsolated), an arbitrary, unknown-to-this-image uid with no
# /etc/passwd entry and no $HOME — Wine refuses to run as root by default,
# and an unknown uid otherwise gets confused looking for a home directory
# that does not exist. dockerRunIsolated sets -e HOME=/tmp for exactly this
# reason.
#
# WINEPREFIX is deliberately not created or initialized here at all —
# only pointed at by this ENV var. wine.go's ensureWineprefixInitialized
# bind-mounts a host-owned directory there instead (both to prime it via
# a real `wineboot --init` on first use and on every real run
# afterward): real testing found Wine's own ownership check on
# WINEPREFIX ("wine: '/wineprefix' is not owned by you") rejects
# anything not created by the exact uid using it, and every path that
# pre-creates the directory as part of the image itself — a plain RUN
# step, `docker commit` after priming as a different uid, even a
# pre-created empty directory with permissive chmod bits — leaves it
# owned by whichever uid happened to touch it at build/commit time, not
# the arbitrary host uid dockerRunIsolated actually runs as. A bind
# mount's ownership is simply whatever the host-side directory's owner
# already is — the same host uid on every invocation — sidestepping the
# problem entirely. See docs/adr/0028.
ENV WINEPREFIX=/wineprefix WINEARCH=win64 WINEDEBUG=-all

# X11 clients (Xvfb included) expect /tmp/.X11-unix to already exist with
# the standard world-writable-plus-sticky-bit mode a real desktop's own
# init/X infrastructure normally creates ahead of time — a minimal
# container image has no such thing. Real testing found this was silently
# failing Xvfb itself under the arbitrary non-root uid dockerRunIsolated
# always runs as (`_XSERVTransmkdir: ERROR: euid != 0, directory
# /tmp/.X11-unix will not be created` — visible only via `xvfb-run
# --error-file`, since xvfb-run's own default behavior discards Xvfb's
# stderr entirely) and was the cause of one class of "wine: could not
# load kernel32.dll" failure hit while building this image: with no
# display to attach to, wineboot --init failed deep inside its own module
# loader with a message that never mentioned X11 or Xvfb at all. Creating
# this directory here, as root at build time, fixed that class for good —
# wineboot --init now succeeds reliably. A second, narrower instance of
# the exact same error message remains open: see wine.go's own doc
# comments and docs/adr/0028 for what that is and what was ruled out.
RUN mkdir -p /tmp/.X11-unix && chmod 1777 /tmp/.X11-unix

WORKDIR /work
