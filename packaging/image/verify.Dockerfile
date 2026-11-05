# Werkzeuge für verify-image.sh und das Komprimieren des Abbilds.
#   docker build -t hmi-kiosk-verify -f packaging/image/verify.Dockerfile packaging/image
FROM debian:trixie
RUN apt-get update \
 && apt-get install -y --no-install-recommends fdisk mount initramfs-tools-core cpio zstd xz-utils lz4 \
 && rm -rf /var/lib/apt/lists/*
