# Minimal extraction environment for the Mesa3D Windows fallback archive
# (see docs/adr/0027-automatic-toolchain-acquisition.md). Mesa's own
# distribution (github.com/pal1000/mesa-dist-win) ships only as .7z, and
# p7zip is deliberately not required on the host running fluxa-builder —
# it lives in this container instead, so building a Windows target never
# adds a new host dependency for whoever runs fluxa-builder.
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends p7zip-full \
    && rm -rf /var/lib/apt/lists/*
