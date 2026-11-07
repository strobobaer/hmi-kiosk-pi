#!/bin/bash -e
# Kiosk-Stufe: Benutzer, Programme, Dienste und Systemeinstellungen.
# files/ füllt packaging/build-image.sh (rootfs/, bin/, firstboot.sh, build.env).

# shellcheck source=/dev/null
. files/build.env
: "${HMI_VERSION:?HMI_VERSION fehlt in build.env}"
: "${HMI_ENABLE_SSH:?HMI_ENABLE_SSH fehlt in build.env}"

R="${ROOTFS_DIR}"
FS=files/rootfs
KIOSK_UID=1100

# Dienstbenutzer ohne Passwort und ohne Shell; der erste Benutzer aus pi-gen wird gesperrt.
on_chroot <<EOF
if ! getent group hmikiosk >/dev/null; then groupadd -g ${KIOSK_UID} hmikiosk; fi
if ! id -u hmikiosk >/dev/null 2>&1; then
	useradd -u ${KIOSK_UID} -g hmikiosk -G input,video,render -m -d /home/hmikiosk -s /usr/sbin/nologin -c "HMI-Kiosk" hmikiosk
fi
usermod -a -G input,video,render hmikiosk
usermod -L hmikiosk
usermod -L "${FIRST_USER_NAME}"
EOF

# Pakete, die nicht ins Abbild dürfen (Schlüsselbund, automatische Updates, cloud-init mit growpart).
on_chroot <<'EOF'
for p in remmina-plugin-secret gnome-keyring unattended-upgrades cloud-init rpi-cloud-init-mods; do
	if dpkg-query -W -f='${db:Status-Abbrev}' "$p" 2>/dev/null | grep -q '^i'; then
		apt-get purge -y "$p"
	fi
done
EOF

# Systemdateien aus packaging/rootfs, jede mit festem Modus und root als Besitzer.
for u in hmi-kiosk.service hmi-kiosk-helper.service hmi-kiosk-session.service hmi-kiosk-update-guard.service \
	hmi-kiosk-firstboot.service hmi-kiosk-firstboot-data.service; do
	install -m 644 -o root -g root "${FS}/etc/systemd/system/${u}" "${R}/etc/systemd/system/${u}"
done
install -d -m 755 "${R}/etc/systemd/system.conf.d"
install -m 644 -o root -g root "${FS}/etc/systemd/system.conf.d/watchdog.conf" "${R}/etc/systemd/system.conf.d/watchdog.conf"
install -d -m 755 "${R}/etc/hmi-kiosk/labwc" "${R}/etc/xdg/openbox"
install -m 644 -o root -g root "${FS}/etc/hmi-kiosk/labwc/rc.xml" "${R}/etc/hmi-kiosk/labwc/rc.xml"
install -m 644 -o root -g root "${FS}/etc/hmi-kiosk/labwc/environment" "${R}/etc/hmi-kiosk/labwc/environment"
install -m 644 -o root -g root "${FS}/etc/hmi-kiosk/labwc/autostart" "${R}/etc/hmi-kiosk/labwc/autostart"
install -m 644 -o root -g root "${FS}/etc/xdg/openbox/rc.xml" "${R}/etc/xdg/openbox/rc.xml"
# Navigationssperre (Spezifikation 11): Chromium lädt nur die Kachelseite auf 127.0.0.1:8080.
install -d -m 755 "${R}/etc/chromium/policies/managed"
install -m 644 -o root -g root "${FS}/etc/chromium/policies/managed/hmi-kiosk.json" "${R}/etc/chromium/policies/managed/hmi-kiosk.json"
install -m 644 -o root -g root "${FS}/etc/pam.d/hmi-kiosk-session" "${R}/etc/pam.d/hmi-kiosk-session"
install -m 755 -o root -g root "${FS}/usr/local/bin/hmi-kiosk-session" "${R}/usr/local/bin/hmi-kiosk-session"
install -m 755 -o root -g root files/firstboot.sh "${R}/usr/local/sbin/hmi-kiosk-firstboot"

# Programme: der Helfer fest im System, der Dienst als erste Version für /data/app.
install -m 755 -o root -g root files/bin/hmi-kiosk-helper "${R}/usr/local/bin/hmi-kiosk-helper"
install -d -m 755 -o root -g root "${R}/opt/hmi-kiosk/initial/${HMI_VERSION}"
install -m 755 -o root -g root files/bin/hmi-kiosk "${R}/opt/hmi-kiosk/initial/${HMI_VERSION}/hmi-kiosk"
printf '%s\n' "${HMI_VERSION}" >"${R}/opt/hmi-kiosk/initial/${HMI_VERSION}/VERSION"
chmod 644 "${R}/opt/hmi-kiosk/initial/${HMI_VERSION}/VERSION"

# Kennzeichen eines Abbild-Systems für die Selbstprüfung.
install -d -m 755 -o root -g root "${R}/etc/hmi-kiosk"
printf 'version=%s\n' "${HMI_VERSION}" >"${R}/etc/hmi-kiosk/image-release"
chown root:root "${R}/etc/hmi-kiosk/image-release"
chmod 644 "${R}/etc/hmi-kiosk/image-release"

# Einhängepunkt für /data. Partition und fstab-Eintrag (ohne nofail) entstehen beim ersten Start
# (hmi-kiosk-firstboot prepare-data), damit kein Eintrag auf eine noch fehlende Partition wartet.
install -d -m 755 -o root -g root "${R}/data"

# Keine Vergrößerung der Systempartition: Token "resize" aus cmdline.txt, rpi-resize maskiert.
# consoleblank=0: keine Bildschirmabschaltung der Konsole.
CMDLINE="${R}/boot/firmware/cmdline.txt"
sed -i -E 's/(^| )resize( |$)/\1\2/; s/  +/ /g; s/ +$//' "${CMDLINE}"
grep -q 'consoleblank=0' "${CMDLINE}" || sed -i 's/$/ consoleblank=0/' "${CMDLINE}"
# fsck.repair=yes (in pi-gen schon gesetzt): systemd-fsck repariert /data ohne Rückfrage beim Start.
grep -Eq '(^| )fsck.repair=yes( |$)' "${CMDLINE}" || sed -i 's/$/ fsck.repair=yes/' "${CMDLINE}"

# Unsichtbares Zeigerthema für labwc: ein durchsichtiges Bild 1x1 im Xcursor-Format.
CURSORS="${R}/usr/share/icons/hmi-blank/cursors"
install -d -m 755 "${CURSORS}"
printf '[Icon Theme]\nName=hmi-blank\nComment=Unsichtbarer Zeiger für den Kiosk\n' >"${R}/usr/share/icons/hmi-blank/index.theme"
printf 'Xcur\x10\x00\x00\x00\x00\x00\x01\x00\x01\x00\x00\x00\x02\x00\xfd\xff\x18\x00\x00\x00\x1c\x00\x00\x00\x24\x00\x00\x00\x02\x00\xfd\xff\x18\x00\x00\x00\x01\x00\x00\x00\x01\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00' >"${CURSORS}/default"
for c in left_ptr arrow top_left_arrow pointer hand1 hand2 text xterm crosshair cross wait watch progress \
	left_ptr_watch grab grabbing fleur move all-scroll not-allowed col-resize row-resize ns-resize ew-resize \
	nwse-resize nesw-resize n-resize s-resize e-resize w-resize ne-resize nw-resize se-resize sw-resize help \
	question_arrow context-menu copy alias cell vertical-text zoom-in zoom-out top_side bottom_side left_side \
	right_side top_left_corner top_right_corner bottom_left_corner bottom_right_corner sb_h_double_arrow \
	sb_v_double_arrow; do
	ln -sf default "${CURSORS}/${c}"
done
chmod 644 "${CURSORS}/default" "${R}/usr/share/icons/hmi-blank/index.theme"

# Laufzeitordner /run/user/1100 auch ohne Sitzung (der Dienst setzt XDG_RUNTIME_DIR fest).
install -d -m 755 "${R}/var/lib/systemd/linger"
: >"${R}/var/lib/systemd/linger/hmikiosk"

on_chroot <<EOF
set -e
systemctl enable hmi-kiosk.service hmi-kiosk-helper.service hmi-kiosk-session.service \
	hmi-kiosk-update-guard.service hmi-kiosk-firstboot.service hmi-kiosk-firstboot-data.service
# tty1 gehört der Kiosk-Sitzung.
systemctl disable getty@tty1.service || true
systemctl mask getty@tty1.service
# Keine automatischen Updates; Paket- und Systemupdates kommen als neues Abbild.
systemctl disable apt-daily.timer apt-daily-upgrade.timer || true
systemctl mask apt-daily.timer apt-daily-upgrade.timer apt-daily.service apt-daily-upgrade.service
if [ -e /usr/lib/systemd/system/rpi-resize.service ] || [ -e /lib/systemd/system/rpi-resize.service ]; then
	systemctl disable rpi-resize.service || true
fi
systemctl mask rpi-resize.service
if [ "${HMI_ENABLE_SSH}" = 1 ]; then
	systemctl enable ssh
else
	systemctl disable ssh || true
fi
EOF
