#!/bin/bash
# Installiert den HMI-Kiosk auf einem Debian- oder Raspberry-Pi-OS-System (Trixie, arm64 oder amd64).
# Gedacht für Systeme ohne das fertige SD-Abbild: ohne Schreibschutz, ohne Datenpartition. Die SD-Sicherung
# in der Admin-Seite baut dort ein Abbild dateiweise (rsync, dosfstools, fdisk, e2fsprogs, util-linux).
#
#   sudo ./install.sh [--no-start] [--online] [--disable-desktop] [--data-dir ORDNER] [--force-downgrade]
#
# Das Skript liegt in einem Bundle (erzeugt von packaging/bundle.sh) und installiert daraus ohne Netz:
# Pakete aus bundle/repo, Programme aus bundle/bin, Dienstdateien aus bundle/rootfs.
# Es ist wiederholbar; vorhandene Konfiguration im Datenordner bleibt unberührt.
#
#   --no-start        Dienste einrichten, aber weder aktivieren noch starten (für Tests)
#   --online          Pakete aus den Paketquellen des Systems holen statt aus dem Bundle
#   --disable-desktop Einen vorhandenen Desktop (Display-Manager, z. B. lightdm auf Raspberry Pi OS
#                     Desktop) abschalten und in die Textkonsole starten; wirkt nach dem Neustart
#   --data-dir ORDNER Datenordner (Standard /var/lib/hmi-kiosk; im SD-Abbild ist es /data)
#   --force-downgrade Erlaubt, eine ältere Version als die laufende zu installieren
#
# Umgebung (nur für Tests): HMI_OS_RELEASE (Standard /etc/os-release).
# shellcheck disable=SC1111  # deutsche Anführungszeichen in den Meldungen
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OS_RELEASE="${HMI_OS_RELEASE:-/etc/os-release}"

DATA_DIR=/var/lib/hmi-kiosk
NO_START=0
ONLINE=0
FORCE_DOWNGRADE=0
DISABLE_DESKTOP=0

KIOSK_USER=hmikiosk
KIOSK_UID_WISH=1100
KIOSK_GROUPS=input,video,render
UNITS="hmi-kiosk.service hmi-kiosk-helper.service hmi-kiosk-session.service hmi-kiosk-update-guard.service hmi-kiosk-grow-root.service hmi-kiosk-identity.service"

msg() { echo "install.sh: $*"; }
die() {
	echo "install.sh: FEHLER: $*" >&2
	exit 1
}

usage() {
	sed -n '2,/^# shellcheck/p' "${BASH_SOURCE[0]}" | sed -e '$d' -e 's/^# \{0,1\}//'
}

while [ $# -gt 0 ]; do
	case "$1" in
	--no-start) NO_START=1 ;;
	--online) ONLINE=1 ;;
	--force-downgrade) FORCE_DOWNGRADE=1 ;;
	--disable-desktop) DISABLE_DESKTOP=1 ;;
	--data-dir)
		[ $# -ge 2 ] || die "--data-dir braucht einen Ordner."
		DATA_DIR="$2"
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*) die "Unbekannte Option „$1“. Hilfe: ./install.sh --help" ;;
	esac
	shift
done

case "$DATA_DIR" in
/*) ;;
*) die "Der Datenordner muss ein absoluter Pfad sein: $DATA_DIR" ;;
esac
case "$DATA_DIR" in
*[[:space:]\"\'\\\$\;\&\|\<\>\(\)\`]* | /) die "Der Datenordner enthält unzulässige Zeichen oder ist /: $DATA_DIR" ;;
esac
DATA_DIR="${DATA_DIR%/}"

[ "$(id -u)" = 0 ] || die "Bitte als root ausführen (sudo ./install.sh)."

# --- Bundle und System prüfen ---

[ -f "$HERE/manifest" ] || die "Kein Bundle gefunden: $HERE/manifest fehlt. install.sh gehört in ein mit packaging/bundle.sh erzeugtes Bundle."

manifest() { sed -n "s/^$1=//p" "$HERE/manifest" | head -n 1; }

B_VERSION="$(manifest version)"
B_CODENAME="$(manifest codename)"
B_ARCH="$(manifest arch)"
B_PACKAGES="$(manifest packages)"
[[ "$B_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "Das Bundle hat keine gültige Version (manifest: „$B_VERSION“)."
[ -n "$B_CODENAME" ] && [ -n "$B_ARCH" ] && [ -n "$B_PACKAGES" ] || die "Das Manifest des Bundles ist unvollständig."

[ -r "$OS_RELEASE" ] || die "$OS_RELEASE nicht lesbar, Betriebssystem nicht erkennbar."
# shellcheck disable=SC1090
OS_CODENAME="$(. "$OS_RELEASE" && echo "${VERSION_CODENAME:-}")"
command -v dpkg >/dev/null || die "dpkg fehlt. Dieses Skript läuft nur auf Debian oder Raspberry Pi OS."
SYS_ARCH="$(dpkg --print-architecture)"

if [ "$OS_CODENAME" != "$B_CODENAME" ]; then
	die "Dieses Bundle ist für Debian „$B_CODENAME“, das System ist „${OS_CODENAME:-unbekannt}“. Bitte ein Bundle für diese Version bauen (packaging/bundle.sh in einem passenden Container) oder das SD-Abbild verwenden. Es wurde nichts verändert."
fi
if [ "$SYS_ARCH" != "$B_ARCH" ]; then
	die "Dieses Bundle ist für die Architektur „$B_ARCH“, das System hat „$SYS_ARCH“. Bitte ein passendes Bundle bauen. Es wurde nichts verändert."
fi

for f in bin/hmi-kiosk bin/hmi-kiosk-helper rootfs/usr/local/bin/hmi-kiosk-session; do
	[ -f "$HERE/$f" ] || die "Im Bundle fehlt $f."
done
if [ "$ONLINE" = 0 ] && [ ! -f "$HERE/repo/Packages" ]; then
	die "Im Bundle fehlt das Paketverzeichnis repo/. Ohne --online lassen sich die Pakete nicht installieren."
fi

if [ -f "$HERE/SHA256SUMS" ]; then
	(cd "$HERE" && sha256sum --quiet -c SHA256SUMS) || die "Das Bundle ist beschädigt oder unvollständig (Prüfsummen stimmen nicht). Bitte neu kopieren."
fi

# --- Datenordner und Version prüfen (vor jeder Änderung) ---

# Erlaubt sind ein neuer oder leerer Ordner und ein Ordner, in dem schon ein Kiosk liegt
# (config.json oder app/). Alles andere ist ein fremder Ordner und wird nicht angefasst.
if [ -e "$DATA_DIR" ] || [ -L "$DATA_DIR" ]; then
	[ -d "$DATA_DIR" ] && [ ! -L "$DATA_DIR" ] || die "Der Datenordner $DATA_DIR ist kein Ordner (oder ein Verweis). Es wurde nichts verändert."
	if [ -n "$(ls -A "$DATA_DIR")" ] && [ ! -e "$DATA_DIR/config.json" ] && [ ! -d "$DATA_DIR/app" ]; then
		die "Der Datenordner $DATA_DIR ist nicht leer und enthält keine Kiosk-Dateien (weder config.json noch app/). Bitte einen neuen oder leeren Ordner mit --data-dir wählen. Es wurde nichts verändert."
	fi
fi

if [ -L "$DATA_DIR/app/current" ]; then
	RUNNING="$(readlink "$DATA_DIR/app/current")"
	if [[ "$RUNNING" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] && [ "$RUNNING" != "$B_VERSION" ] &&
		[ "$(printf '%s\n%s\n' "$RUNNING" "$B_VERSION" | sort -V | head -n 1)" = "$B_VERSION" ] && [ "$FORCE_DOWNGRADE" = 0 ]; then
		die "Auf diesem System läuft Version $RUNNING, das Bundle enthält die ältere Version $B_VERSION. Ein Rückschritt wird nicht ohne Zustimmung ausgeführt. Mit --force-downgrade erzwingen. Es wurde nichts verändert."
	fi
fi

# Ein Desktop (Display-Manager) würde sich mit der Kiosk-Sitzung um Bildschirm und Eingaben streiten.
DM_LINK=/etc/systemd/system/display-manager.service
DM_UNIT=""
if [ -L "$DM_LINK" ] || [ -e "$DM_LINK" ]; then
	DM_UNIT="$(basename "$(readlink "$DM_LINK" || echo display-manager.service)")"
	if [ "$DISABLE_DESKTOP" = 0 ]; then
		die "Auf diesem System startet beim Booten ein Desktop ($DM_UNIT). Er verträgt sich nicht mit der Kiosk-Sitzung. Mit --disable-desktop schaltet install.sh ihn ab und startet künftig in die Textkonsole (rückgängig: sudo systemctl enable $DM_UNIT && sudo systemctl set-default graphical.target). Es wurde nichts verändert."
	fi
fi

# --- Pakete ---

install_packages() {
	local -a pkgs
	read -r -a pkgs <<<"$B_PACKAGES"
	export DEBIAN_FRONTEND=noninteractive
	if [ "$ONLINE" = 1 ]; then
		msg "Installiere Pakete aus den Paketquellen des Systems (--online)."
		apt-get update
		apt-get install -y --no-install-recommends "${pkgs[@]}"
		return
	fi
	msg "Installiere Pakete aus dem Bundle, ohne Netz."
	# Eigene Listen und eigener Zwischenspeicher: die Paketquellen des Systems bleiben unberührt.
	work="$(mktemp -d)"
	chmod 755 "$work" # der Benutzer _apt muss die Quelle lesen können
	trap 'rm -rf "$work"' EXIT
	mkdir -p "$work/lists/partial" "$work/cache/archives/partial"
	echo "deb [trusted=yes] file:$HERE/repo ./" >"$work/sources.list"
	local -a apt=(
		-o "Dir::Etc::sourcelist=$work/sources.list"
		-o "Dir::Etc::sourceparts=/dev/null"
		-o "Dir::State::lists=$work/lists"
		-o "Dir::Cache=$work/cache"
		-o "Dir::Cache::archives=$work/cache/archives"
		-o "Acquire::Languages=none"
		-o "Acquire::Retries=0"
		-o "APT::Get::List-Cleanup=0"
	)
	apt-get "${apt[@]}" update
	apt-get "${apt[@]}" install -y --no-install-recommends "${pkgs[@]}"
}

# --- Benutzer ---

ensure_user() {
	if ! getent group "$KIOSK_USER" >/dev/null; then
		if getent group "$KIOSK_UID_WISH" >/dev/null; then
			groupadd "$KIOSK_USER"
		else
			groupadd -g "$KIOSK_UID_WISH" "$KIOSK_USER"
		fi
	fi
	if ! id -u "$KIOSK_USER" >/dev/null 2>&1; then
		local uidarg=()
		if ! getent passwd "$KIOSK_UID_WISH" >/dev/null; then
			uidarg=(-u "$KIOSK_UID_WISH")
		fi
		useradd "${uidarg[@]}" -g "$KIOSK_USER" -G "$KIOSK_GROUPS" -m -d "/home/$KIOSK_USER" \
			-s /usr/sbin/nologin -c "HMI-Kiosk" "$KIOSK_USER"
		msg "Benutzer $KIOSK_USER angelegt."
	fi
	local g
	for g in ${KIOSK_GROUPS//,/ }; do
		getent group "$g" >/dev/null || groupadd -r "$g"
	done
	usermod -a -G "$KIOSK_GROUPS" "$KIOSK_USER"
	usermod -L "$KIOSK_USER"
	KIOSK_UID="$(id -u "$KIOSK_USER")"
}

# --- Dateien ---

# put MODUS QUELLE ZIEL: legt Zieldatei atomar an; eine abweichende fremde Datei bleibt einmal als .vor-hmi-kiosk erhalten.
put() {
	local mode="$1" src="$2" dst="$3" tmp
	install -d -m 755 "$(dirname "$dst")"
	if [ -e "$dst" ] && ! cmp -s "$src" "$dst" && [ ! -e "$dst.vor-hmi-kiosk" ]; then
		cp -p "$dst" "$dst.vor-hmi-kiosk"
	fi
	tmp="$(dirname "$dst")/.$(basename "$dst").neu.$$"
	install -m "$mode" -o root -g root "$src" "$tmp"
	mv -f "$tmp" "$dst"
}

# Bis Version 0.1.1 lag die Kiosk-Konfiguration in /etc/xdg/labwc, dem Ordner des Desktops.
# Eigene Dateien dort wieder entfernen und gesicherte Desktop-Dateien zurücklegen.
restore_desktop_labwc() {
	local f p marker
	for f in rc.xml environment; do
		p="/etc/xdg/labwc/$f"
		case "$f" in
		rc.xml) marker="labwc im Kiosk" ;;
		environment) marker="Umgebung der labwc-Sitzung im Kiosk" ;;
		esac
		[ -f "$p" ] && grep -q "$marker" "$p" || continue
		if [ -f "$p.vor-hmi-kiosk" ]; then
			mv -f "$p.vor-hmi-kiosk" "$p"
			msg "$p: Fassung des Desktops wiederhergestellt."
		else
			rm -f "$p"
			msg "$p: alte Kiosk-Datei entfernt."
		fi
	done
}

install_system_files() {
	local u
	for u in $UNITS; do
		put 644 "$HERE/rootfs/etc/systemd/system/$u" "/etc/systemd/system/$u"
	done
	put 644 "$HERE/rootfs/etc/systemd/system.conf.d/watchdog.conf" /etc/systemd/system.conf.d/watchdog.conf
	put 644 "$HERE/rootfs/etc/hmi-kiosk/labwc/rc.xml" /etc/hmi-kiosk/labwc/rc.xml
	put 644 "$HERE/rootfs/etc/hmi-kiosk/labwc/environment" /etc/hmi-kiosk/labwc/environment
	put 644 "$HERE/rootfs/etc/hmi-kiosk/labwc/autostart" /etc/hmi-kiosk/labwc/autostart
	restore_desktop_labwc
	put 644 "$HERE/rootfs/etc/xdg/openbox/rc.xml" /etc/xdg/openbox/rc.xml
	put 644 "$HERE/rootfs/etc/chromium/policies/managed/hmi-kiosk.json" /etc/chromium/policies/managed/hmi-kiosk.json
	put 644 "$HERE/rootfs/etc/pam.d/hmi-kiosk-session" /etc/pam.d/hmi-kiosk-session
	put 755 "$HERE/rootfs/usr/local/bin/hmi-kiosk-session" /usr/local/bin/hmi-kiosk-session
	put 755 "$HERE/bin/hmi-kiosk-helper" /usr/local/bin/hmi-kiosk-helper
	# Wachstum der Wurzel auf größerer Karte und Netzwerk-Kennung (wie im SD-Abbild): beide gehören in jede
	# dateiweise SD-Sicherung dieses Systems, damit eine Kopie auf anderer Hardware richtig anläuft.
	put 755 "$HERE/rootfs/usr/local/sbin/hmi-kiosk-grow-root" /usr/local/sbin/hmi-kiosk-grow-root
	put 755 "$HERE/rootfs/usr/local/sbin/hmi-kiosk-identity" /usr/local/sbin/hmi-kiosk-identity
	install_cursor_theme
	# Die Sitzung braucht /run/user/<uid> auch ohne Anmeldung.
	install -d -m 755 /var/lib/systemd/linger
	: >"/var/lib/systemd/linger/$KIOSK_USER"
	install -d -m 755 /etc/hmi-kiosk
	printf 'version=%s\nbundle=%s-%s\n' "$B_VERSION" "$B_CODENAME" "$B_ARCH" >/etc/hmi-kiosk/install-release
	chmod 644 /etc/hmi-kiosk/install-release
}

# Gleiches unsichtbares Zeigerthema wie im Abbild (packaging/image/stage-hmi/01-hmi/00-run.sh).
install_cursor_theme() {
	local dir=/usr/share/icons/hmi-blank c
	install -d -m 755 "$dir/cursors"
	printf '[Icon Theme]\nName=hmi-blank\nComment=Unsichtbarer Zeiger für den Kiosk\n' >"$dir/index.theme"
	printf 'Xcur\x10\x00\x00\x00\x00\x00\x01\x00\x01\x00\x00\x00\x02\x00\xfd\xff\x18\x00\x00\x00\x1c\x00\x00\x00\x24\x00\x00\x00\x02\x00\xfd\xff\x18\x00\x00\x00\x01\x00\x00\x00\x01\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00' >"$dir/cursors/default"
	for c in left_ptr arrow top_left_arrow pointer hand1 hand2 text xterm crosshair cross wait watch progress \
		left_ptr_watch grab grabbing fleur move all-scroll not-allowed col-resize row-resize ns-resize ew-resize \
		nwse-resize nesw-resize n-resize s-resize e-resize w-resize ne-resize nw-resize se-resize sw-resize help \
		question_arrow context-menu copy alias cell vertical-text zoom-in zoom-out top_side bottom_side left_side \
		right_side top_left_corner top_right_corner bottom_left_corner bottom_right_corner sb_h_double_arrow \
		sb_v_double_arrow; do
		ln -sf default "$dir/cursors/$c"
	done
	chmod 644 "$dir/index.theme" "$dir/cursors/default"
}

# Die Dienstdateien im Bundle sind für den Datenordner /data und die Kennung 1100 geschrieben;
# hier werden Datenordner und Kennung per Drop-in angepasst, die Dateien selbst bleiben unverändert.
write_dropin() {
	local body="$2" dir="/etc/systemd/system/$1.d"
	install -d -m 755 "$dir"
	printf '%s\n' "$body" >"$dir/10-install.conf.neu"
	chmod 644 "$dir/10-install.conf.neu"
	mv -f "$dir/10-install.conf.neu" "$dir/10-install.conf"
}

install_dropins() {
	local sock=/run/hmi-kiosk-helper/helper.sock
	write_dropin hmi-kiosk.service "# Von install.sh erzeugt: Datenordner und Kennung dieses Systems.
[Unit]
RequiresMountsFor=
RequiresMountsFor=$DATA_DIR
[Service]
ExecStart=
ExecStart=$DATA_DIR/app/current/hmi-kiosk -data $DATA_DIR -run /run/hmi-kiosk -helper-socket $sock
Environment=
Environment=XDG_RUNTIME_DIR=/run/user/$KIOSK_UID"

	write_dropin hmi-kiosk-helper.service "# Von install.sh erzeugt: Datenordner und Kennung dieses Systems.
[Unit]
RequiresMountsFor=
RequiresMountsFor=$DATA_DIR
[Service]
ExecStart=
ExecStart=/usr/local/bin/hmi-kiosk-helper -uid $KIOSK_UID -gid $(id -g "$KIOSK_USER") -socket $sock -root $DATA_DIR -ntp-state $DATA_DIR/system/ntp-server"

	write_dropin hmi-kiosk-update-guard.service "# Von install.sh erzeugt: Datenordner dieses Systems.
[Unit]
RequiresMountsFor=
RequiresMountsFor=$DATA_DIR
[Service]
ExecStart=
ExecStart=/usr/local/bin/hmi-kiosk-helper update-guard -root $DATA_DIR"

	write_dropin hmi-kiosk-session.service "# Von install.sh erzeugt: Datenordner und Kennung dieses Systems.
[Service]
Environment=
Environment=XDG_RUNTIME_DIR=/run/user/$KIOSK_UID XDG_SESSION_TYPE=wayland XDG_SESSION_CLASS=user HMI_KIOSK_CONFIG=$DATA_DIR/config.json"

	# Die Kennung liegt im root-eigenen Unterordner system des Datenordners. Ohne Overlay schreibt das Skript
	# /etc/machine-id und /etc/hostname dauerhaft.
	write_dropin hmi-kiosk-identity.service "# Von install.sh erzeugt: Datenordner dieses Systems.
[Unit]
RequiresMountsFor=
RequiresMountsFor=$DATA_DIR
[Service]
Environment=HMI_IDENTITY_DIR=$DATA_DIR/system"
}

# --- Datenordner und Programmversion ---

install_data_and_app() {
	local app="$DATA_DIR/app" v="$B_VERSION" tmp old
	install -d -m 750 "$DATA_DIR"
	chown "$KIOSK_USER:$KIOSK_USER" "$DATA_DIR"
	chmod 750 "$DATA_DIR"
	# Vorhandene Dateien (config.json, Schlüssel, Protokoll) bleiben, nur der Besitzer wird angeglichen.
	find "$DATA_DIR" -mindepth 1 -maxdepth 1 ! -name system ! -name app -exec chown -hR "$KIOSK_USER:$KIOSK_USER" {} +
	# Hier schreibt nur der Helfer (NTP-Stand); der Dienst darf es nicht ändern.
	install -d -o root -g root -m 755 "$DATA_DIR/system"
	install -d -o "$KIOSK_USER" -g "$KIOSK_USER" -m 755 "$app"

	if [ -x "$app/$v/hmi-kiosk" ] && cmp -s "$HERE/bin/hmi-kiosk" "$app/$v/hmi-kiosk"; then
		msg "Version $v liegt schon in $app."
	else
		tmp="$app/.tmp-install-$v"
		rm -rf "$tmp"
		install -d -o "$KIOSK_USER" -g "$KIOSK_USER" -m 755 "$tmp"
		install -o "$KIOSK_USER" -g "$KIOSK_USER" -m 755 "$HERE/bin/hmi-kiosk" "$tmp/hmi-kiosk"
		printf '%s\n' "$v" >"$tmp/VERSION"
		chown "$KIOSK_USER:$KIOSK_USER" "$tmp/VERSION"
		chmod 644 "$tmp/VERSION"
		rm -rf "${app:?}/$v"
		mv "$tmp" "$app/$v"
		msg "Version $v nach $app/$v kopiert."
	fi

	old=""
	[ -L "$app/current" ] && old="$(readlink "$app/current")"
	if [ "$old" != "$v" ]; then
		if [ -n "$old" ] && [ -x "$app/$old/hmi-kiosk" ]; then
			ln -sfn "$old" "$app/.previous-install"
			chown -h "$KIOSK_USER:$KIOSK_USER" "$app/.previous-install"
			mv -T "$app/.previous-install" "$app/previous"
		fi
		ln -sfn "$v" "$app/.current-install"
		chown -h "$KIOSK_USER:$KIOSK_USER" "$app/.current-install"
		mv -T "$app/.current-install" "$app/current"
		msg "current zeigt auf $v${old:+ (vorher $old, als previous gemerkt)}."
	fi
}

# --- systemd ---

have_systemd() { [ -d /run/systemd/system ] && command -v systemctl >/dev/null; }

# Schaltet den Desktop ab, ohne ihn jetzt zu beenden: install.sh läuft oft in einem Terminal darauf.
disable_desktop() {
	[ -n "$DM_UNIT" ] || return 0
	if have_systemd; then
		systemctl disable "$DM_UNIT"
		systemctl set-default multi-user.target
	else
		rm -f "$DM_LINK"
		ln -sfn /lib/systemd/system/multi-user.target /etc/systemd/system/default.target
	fi
	msg "Desktop ($DM_UNIT) abgeschaltet, das System startet künftig in die Textkonsole."
}

setup_services() {
	if ! have_systemd; then
		msg "Hinweis: systemd läuft hier nicht (Container oder Chroot). Dienste werden nicht aktiviert oder gestartet."
		return
	fi
	systemctl daemon-reload
	if [ "$NO_START" = 1 ]; then
		msg "Mit --no-start: Dienste sind eingerichtet, aber nicht aktiviert und nicht gestartet."
		return
	fi
	# shellcheck disable=SC2086
	systemctl enable $UNITS
	if [ -n "$DM_UNIT" ]; then
		# Der Desktop läuft noch bis zum Neustart; zwei grafische Sitzungen gleichzeitig vermeiden.
		msg "Dienste aktiviert. Sie starten nach dem Neustart."
		return
	fi
	systemctl restart hmi-kiosk-helper.service
	systemctl restart hmi-kiosk-session.service
	systemctl restart hmi-kiosk.service
	msg "Dienste aktiviert und gestartet."
}

main() {
	msg "Bundle: Version $B_VERSION, Debian $B_CODENAME, $B_ARCH; Datenordner $DATA_DIR."
	install_packages
	ensure_user
	install_system_files
	install_dropins
	install_data_and_app
	disable_desktop
	setup_services
	if [ -n "$DM_UNIT" ]; then
		msg "Bitte jetzt neu starten: sudo reboot"
	fi
	msg "Fertig. Die Admin-Seite ist unter https://<Adresse dieses Geräts>:8443 erreichbar, sobald der Dienst läuft."
	msg "Kein Schreibschutz und keine eigene Datenpartition: das ist ein System ohne SD-Abbild. Der Watchdog wirkt nach dem nächsten Neustart."
	msg "Hinweis: Die Netzwerk-Kennung (hmi-kiosk-identity) gibt einer Kopie dieses Systems auf anderer Hardware beim ersten Start eine eigene Maschinen-ID und einen eigenen Rechnernamen; hmi-kiosk-grow-root vergrößert die Wurzel auf einer größeren Karte. Beides wirkt nach dem Neustart."
}

main
