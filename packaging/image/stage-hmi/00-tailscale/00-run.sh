#!/bin/bash -e
# Tailscale, Teil 1 (auf dem Bauhost): Schlüsselbund und Paketquelle ins Abbild legen.
# pi-gen führt 00-run-chroot.sh nur über stdin im Chroot aus; files/ ist dort nicht sichtbar,
# deshalb kopiert dieses Skript, 00-run-chroot.sh installiert danach das Paket.
#
# Schlüsselbund: einmalig von https://pkgs.tailscale.com/stable/debian/trixie.noarmor.gpg geladen.
# Fingerabdruck: 2596 A99E AAB3 3821 893C  0A79 458C A832 957F 5868
# (Tailscale Inc., Package repository signing key, RSA 4096, 2020-02-25)
install -d -m 755 "${ROOTFS_DIR}/usr/share/keyrings" "${ROOTFS_DIR}/etc/apt/sources.list.d"
install -m 644 -o root -g root files/tailscale-archive-keyring.gpg "${ROOTFS_DIR}/usr/share/keyrings/tailscale-archive-keyring.gpg"
printf '%s\n' 'deb [signed-by=/usr/share/keyrings/tailscale-archive-keyring.gpg] https://pkgs.tailscale.com/stable/debian trixie main' \
	>"${ROOTFS_DIR}/etc/apt/sources.list.d/tailscale.list"
chmod 644 "${ROOTFS_DIR}/etc/apt/sources.list.d/tailscale.list"

# Zustand von tailscaled auf /data (sonst ist die Anmeldung nach jedem Neustart weg, / liegt im RAM-Overlay):
# Drop-in mit RequiresMountsFor=/data und ExecStart=--state=/data/system/tailscale/…, dazu das Skript, das den
# Ordner vor dem Start anlegt und prüft.
install -d -m 755 "${ROOTFS_DIR}/usr/local/sbin" "${ROOTFS_DIR}/etc/systemd/system/tailscaled.service.d"
install -m 755 -o root -g root files/hmi-kiosk-tailscale-state "${ROOTFS_DIR}/usr/local/sbin/hmi-kiosk-tailscale-state"
install -m 644 -o root -g root files/tailscaled-hmi-kiosk.conf "${ROOTFS_DIR}/etc/systemd/system/tailscaled.service.d/hmi-kiosk.conf"
