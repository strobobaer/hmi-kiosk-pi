# Build-Kontext ist die Repo-Wurzel:
#   docker build --platform linux/arm64 -f packaging/remmina-check/Dockerfile -t hmi-remmina-check .
FROM --platform=$BUILDPLATFORM golang:1.24-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/hmi-kiosk-remmina-check ./cmd/hmi-kiosk-remmina-check

FROM debian:trixie AS base
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      remmina remmina-plugin-vnc xvfb xauth dbus \
      x11-utils xdotool openbox imagemagick \
 && remmina --version

FROM base
RUN useradd -m kiosk
COPY --from=build /out/hmi-kiosk-remmina-check /usr/local/bin/
COPY packaging/remmina-check/run.sh /usr/local/bin/run.sh
COPY packaging/remmina-check/openbox-rc.xml /etc/hmi-remmina-check/openbox-rc.xml
USER kiosk
ENTRYPOINT ["/usr/local/bin/run.sh"]
