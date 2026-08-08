# Pinned MinGW cross-compilation environment for fluxa-lang's Windows
# static builds (make build-windows-essential-static,
# make build-windows-packaged), used by internal/toolchainbuild so
# Windows binaries can be built and structurally verified from this Linux
# host — see docs/adr/0027-automatic-toolchain-acquisition.md.
#
# Fedora is used specifically for its MinGW packaging project, which
# ships pre-built, cross-compiled static archives for sqlite/curl/zlib
# (confirmed to exist as separate mingw64-*-static packages — plain
# mingw64-sqlite/mingw64-curl/mingw64-zlib only provide DLL import
# libraries, libX.dll.a, not libX.a; a real build attempt here failed to
# link against those for exactly this reason). Raylib has no package on
# any distro and is instead built by fluxa-lang's own pinned
# platform/windows/build-raylib-static.sh, run against the checkout
# bind-mounted at container-run time (not baked into this image), since
# that script's own git/python3 logic already handles fetching and
# patching the exact pinned raylib commit.
FROM fedora:40

RUN dnf install -y \
      gcc make python3 git curl automake libtool \
      mingw64-gcc mingw64-sqlite-static mingw64-curl-static mingw64-zlib-static \
      mingw64-openssl-static mingw64-libssh2-static mingw64-libidn2-static \
      mingw64-winpthreads-static \
    && dnf clean all

# Fedora's MinGW sysroot layout (documented by the Fedora MinGW SIG):
# headers/libs for each mingw64-* package land under this prefix rather
# than the plain /usr/x86_64-w64-mingw32 a Debian-style layout would use.
ENV FEDORA_MINGW_SYSROOT=/usr/x86_64-w64-mingw32/sys-root/mingw

# libsodium has no Fedora mingw64 static package at all (only a
# DLL-import-library build) — cross-compiled from its own pinned upstream
# release instead, the same "no packaged option, build it ourselves"
# reasoning as raylib above. libsodium's build is a standard, well-known
# autotools cross-compile; no source patching is needed, unlike raylib.
ARG LIBSODIUM_VERSION=1.0.20
RUN curl -fsSL -o /tmp/libsodium.tar.gz \
      "https://github.com/jedisct1/libsodium/releases/download/${LIBSODIUM_VERSION}-RELEASE/libsodium-${LIBSODIUM_VERSION}.tar.gz" \
    && tar -xzf /tmp/libsodium.tar.gz -C /tmp \
    && cd /tmp/libsodium-${LIBSODIUM_VERSION} \
    && ./configure --host=x86_64-w64-mingw32 --prefix=/usr/x86_64-w64-mingw32/sys-root/mingw \
         --disable-shared --enable-static \
    && make -j"$(nproc)" && make install \
    && rm -rf /tmp/libsodium.tar.gz /tmp/libsodium-${LIBSODIUM_VERSION}

# fluxa-lang's httpc/https lib.mk resolves curl's link flags with plain
# `pkg-config --libs libcurl` (not `--static --libs`), which — the same
# raylib-class problem as above — only returns libcurl.pc's *public*
# Libs: line. A real build attempt here confirmed the fallout twice over:
# first with undefined idn2/ssh2/ssl/crypto references (libcurl.pc's own
# static transitive deps, listed only in its Libs.private), then with a
# single missing pathcch symbol from OpenSSL's own libcrypto.pc, whose
# Libs.private lists it but whose Libs: does not. Both are merged below
# the same way raylib.pc was — Libs.private folded into Libs: so the
# unmodified, plain pkg-config call already used upstream resolves the
# full static dependency chain with no fluxa-lang source changes.
RUN cd /usr/x86_64-w64-mingw32/sys-root/mingw/lib/pkgconfig \
    && sed -i \
         's|^Libs:.*|Libs: -L${libdir} -lcurl -lidn2 -lssh2 -lws2_32 -lbcrypt -ladvapi32 -lcrypt32 -lssl -lcrypto -lgdi32 -lwldap32 -lz -lpathcch|' \
         libcurl.pc \
    && sed -i \
         's|^Libs:.*|Libs: -L${libdir} -lcrypto -lz -lws2_32 -lgdi32 -lcrypt32 -lpathcch|' \
         libcrypto.pc
