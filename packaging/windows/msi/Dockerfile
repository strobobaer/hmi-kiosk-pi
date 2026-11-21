# Baut den MSI des PDH-Terminals mit WiX 3.14 unter Wine (build-msi.sh). Alles festgelegt: Basisabbild über den Digest,
# WiX und wine-mono über SHA-256, die Debian-Pakete über die Version. Nichts davon liegt auf dem Entwicklungsrechner.
# Stand 2026-11-21: debian:bookworm-slim (linux/amd64 Index), WiX 3.14.1 (wix3141rtm), wine-mono 7.4.0 (für Wine 8.0).
FROM debian@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc

ARG WIX_URL=https://github.com/wixtoolset/wix3/releases/download/wix3141rtm/wix314-binaries.zip
ARG WIX_SHA256=6ac824e1642d6f7277d0ed7ea09411a508f6116ba6fae0aa5f2c7daa2ff43d31
ARG MONO_URL=https://dl.winehq.org/wine/wine-mono/7.4.0/wine-mono-7.4.0-x86.msi
ARG MONO_SHA256=6413ff328ebbf7ec7689c648feb3546d8102ded865079d1fbf0331b14b3ab0ec

ENV DEBIAN_FRONTEND=noninteractive \
    WINEPREFIX=/opt/wineprefix \
    WINEARCH=win64 \
    WINEDEBUG=-all \
    WINEDLLOVERRIDES=winemenubuilder.exe=d

RUN dpkg --add-architecture i386 && apt-get update -qq \
 && apt-get install -y -qq --no-install-recommends ca-certificates curl unzip xvfb xauth \
      wine wine64 wine32 libwine libwine:i386 msitools \
 && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL -o /tmp/wix.zip "$WIX_URL" && echo "$WIX_SHA256  /tmp/wix.zip" | sha256sum -c - \
 && mkdir -p /opt/wix && unzip -q /tmp/wix.zip -d /opt/wix && rm /tmp/wix.zip \
 && curl -fsSL -o /tmp/mono.msi "$MONO_URL" && echo "$MONO_SHA256  /tmp/mono.msi" | sha256sum -c - \
 && xvfb-run -a wineboot --init >/dev/null 2>&1; \
    xvfb-run -a wine msiexec /i /tmp/mono.msi /qn >/dev/null 2>&1; \
    wineserver -w; rm -f /tmp/mono.msi; ls /opt/wineprefix/drive_c/windows/mono >/dev/null

WORKDIR /work
COPY build-in-container.sh /usr/local/bin/build-in-container.sh
ENTRYPOINT ["/usr/local/bin/build-in-container.sh"]
