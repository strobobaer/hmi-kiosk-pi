#!/bin/bash
# Läuft im Container (siehe run.sh): /bundle ist das Bundle, es gibt kein Netz.
set -uo pipefail

fails=0
ok() { echo "OK     $*"; }
bad() {
	echo "FEHLER $*"
	fails=$((fails + 1))
}
check() { # check BESCHREIBUNG BEFEHL...
	local d="$1"
	shift
	if "$@" >/dev/null 2>&1; then ok "$d"; else bad "$d"; fi
}

DATA=/var/lib/hmi-kiosk
V=0.0.1

# Keine Netzverbindung: ein Zugriff müsste scheitern.
if (exec 3<>/dev/tcp/deb.debian.org/80) 2>/dev/null; then bad "Netz ist erreichbar, der Test ist nicht aussagekräftig"; else ok "kein Netz im Container"; fi

# Das Bundle ist schreibgeschützt eingehängt; für die Fehlerfälle unten gibt es Kopien.
# 1. Fremdes System: Bookworm wird abgelehnt, nichts wird verändert.
cp /etc/os-release /tmp/os-release.orig
sed 's/^VERSION_CODENAME=.*/VERSION_CODENAME=bookworm/' /tmp/os-release.orig >/tmp/os-release.bookworm
out=$(HMI_OS_RELEASE=/tmp/os-release.bookworm /bundle/install.sh --no-start 2>&1)
rc=$?
echo "$out" | sed 's/^/      | /'
if [ $rc -ne 0 ]; then ok "Bookworm: Abbruch (Exit $rc)"; else bad "Bookworm wurde nicht abgelehnt"; fi
if echo "$out" | grep -q 'Bundle ist für Debian „trixie“, das System ist „bookworm“'; then ok "Bookworm: klare Meldung"; else bad "Bookworm: Meldung fehlt"; fi
check "Bookworm: nichts installiert (kein Benutzer)" bash -c '! id hmikiosk'
check "Bookworm: kein Datenordner" test ! -e $DATA

# 2. Falsche Architektur im Manifest.
rm -rf /tmp/b2 && cp -r /bundle /tmp/b2
sed -i 's/^arch=.*/arch=arm64/' /tmp/b2/manifest
(cd /tmp/b2 && sed -i '/manifest$/d' SHA256SUMS && sha256sum --text ./manifest >>SHA256SUMS)
out=$(/tmp/b2/install.sh --no-start 2>&1)
rc=$?
echo "$out" | sed 's/^/      | /'
if [ $rc -ne 0 ] && echo "$out" | grep -q 'Architektur „arm64“'; then ok "Falsche Architektur: Abbruch mit Meldung"; else bad "Falsche Architektur nicht abgelehnt"; fi

# 3. Nicht root.
out=$(setpriv --reuid=65534 --regid=65534 --clear-groups /bundle/install.sh --no-start 2>&1)
rc=$?
if [ $rc -ne 0 ] && echo "$out" | grep -q 'als root'; then ok "Ohne root: Abbruch"; else bad "Ohne root nicht abgelehnt ($out)"; fi

# 4. Beschädigtes Bundle.
rm -rf /tmp/b3 && cp -r /bundle /tmp/b3
echo x >>/tmp/b3/bin/hmi-kiosk
out=$(/tmp/b3/install.sh --no-start 2>&1)
rc=$?
if [ $rc -ne 0 ] && echo "$out" | grep -q 'beschädigt'; then ok "Beschädigtes Bundle: Abbruch"; else bad "Beschädigtes Bundle nicht erkannt"; fi

# 5. Vorhandene Konfiguration anlegen, dann zwei Läufe.
mkdir -p $DATA
printf '{"marker":"vorhanden-vor-install"}\n' >$DATA/config.json
sum_before=$(sha256sum $DATA/config.json | cut -d' ' -f1)

state() {
	(cd / && sha256sum etc/systemd/system/hmi-kiosk*.service etc/systemd/system/hmi-kiosk*.d/10-install.conf \
		usr/local/bin/hmi-kiosk-helper usr/local/bin/hmi-kiosk-session etc/hmi-kiosk/labwc/rc.xml etc/chromium/policies/managed/hmi-kiosk.json "${DATA#/}/app/$V/hmi-kiosk")
	find $DATA -printf '%p %l %u %m\n' | sort
}

for run in 1 2; do
	echo "--- Lauf $run"
	out=$(/bundle/install.sh --no-start 2>&1)
	rc=$?
	echo "$out" | tail -n 8 | sed 's/^/      | /'
	if [ $rc -eq 0 ]; then ok "Lauf $run: Exit 0"; else bad "Lauf $run: Exit $rc"; fi
	if echo "$out" | grep -q 'systemd läuft hier nicht'; then ok "Lauf $run: Hinweis ohne systemd"; else bad "Lauf $run: Hinweis ohne systemd fehlt"; fi
	[ "$run" = 1 ] && state1=$(state)
done
if [ "$state1" = "$(state)" ]; then ok "zweiter Lauf ändert nichts (Dateien, Verweise, Besitzer)"; else bad "zweiter Lauf hat etwas verändert"; fi

# 6. Ergebnis.
check "Konfiguration unverändert" test "$(sha256sum $DATA/config.json | cut -d' ' -f1)" = "$sum_before"
check "Konfiguration gehört hmikiosk" test "$(stat -c %U $DATA/config.json)" = hmikiosk

for p in labwc xwayland remmina remmina-plugin-vnc tigervnc-viewer openbox xserver-xorg-core xinit xauth x11-utils rfkill libpam-systemd; do
	check "Paket $p installiert" bash -c "dpkg-query -W -f='\${db:Status-Abbrev}\n' $p | grep -q '^ii'"
done
if dpkg-query -W -f='${db:Status-Abbrev}\n' chromium 2>/dev/null | grep -q '^ii'; then ok "Paket chromium installiert"; else bad "Chromium fehlt"; fi
for p in raspi-config overlayroot; do
	check "Abbild-Paket $p nicht installiert" bash -c "! dpkg-query -W -f='\${db:Status-Abbrev}\n' $p 2>/dev/null | grep -q '^ii'"
done

for u in hmi-kiosk hmi-kiosk-helper hmi-kiosk-session hmi-kiosk-update-guard; do
	check "Unit $u.service" test -f /etc/systemd/system/$u.service
	check "Drop-in $u" test -f /etc/systemd/system/$u.service.d/10-install.conf
done
check "keine Erststart-Unit" test ! -e /etc/systemd/system/hmi-kiosk-firstboot.service
check "Helfer root:root 755" test "$(stat -c '%U:%G %a' /usr/local/bin/hmi-kiosk-helper)" = "root:root 755"
check "Dienst in app/$V" test -x $DATA/app/$V/hmi-kiosk
check "current -> $V" test "$(readlink $DATA/app/current)" = "$V"
check "VERSION-Datei" test "$(cat $DATA/app/$V/VERSION)" = "$V"
check "system/ root-eigen 755" test "$(stat -c '%U %a' $DATA/system)" = "root 755"
check "Datenordner hmikiosk 750" test "$(stat -c '%U %a' $DATA)" = "hmikiosk 750"
check "Overlay nicht eingeschaltet (kein overlayroot in cmdline)" bash -c '! grep -qs overlayroot /boot/firmware/cmdline.txt /boot/cmdline.txt'
check "keine Abbild-Kennung" test ! -e /etc/hmi-kiosk/image-release

uid=$(id -u hmikiosk)
check "Benutzer hmikiosk gesperrt (keine Anmeldung)" bash -c "passwd -S hmikiosk | awk '{print \$2}' | grep -q '^L'"
for g in input video render; do
	check "hmikiosk in Gruppe $g" bash -c "id -nG hmikiosk | tr ' ' '\n' | grep -qx $g"
done
check "Dienst-Drop-in nennt Datenordner und Kennung" bash -c "grep -q 'ExecStart=$DATA/app/current/hmi-kiosk -data $DATA ' /etc/systemd/system/hmi-kiosk.service.d/10-install.conf && grep -q 'XDG_RUNTIME_DIR=/run/user/$uid' /etc/systemd/system/hmi-kiosk.service.d/10-install.conf"
check "Helfer-Drop-in nennt -uid $uid und -root $DATA" bash -c "grep -q -- '-uid $uid .* -root $DATA ' /etc/systemd/system/hmi-kiosk-helper.service.d/10-install.conf"
check "Dienstprogramm startet (Flaggen)" bash -c "$DATA/app/current/hmi-kiosk -h 2>&1 | grep -q -- '-data'"
check "Gruppe hmikiosk genau einmal" bash -c '[ "$(getent group hmikiosk | wc -l)" = 1 ]'
check "systemd-analyze verify der vier Dienste samt Drop-ins" systemd-analyze verify hmi-kiosk.service hmi-kiosk-helper.service hmi-kiosk-session.service hmi-kiosk-update-guard.service

# 7. Neuere Bundle-Version: current wechselt, die alte bleibt als previous, Konfiguration bleibt.
rm -rf /tmp/b4 && cp -r /bundle /tmp/b4
sed -i 's/^version=.*/version=0.0.2/' /tmp/b4/manifest
(cd /tmp/b4 && sed -i '/manifest$/d' SHA256SUMS && sha256sum --text ./manifest >>SHA256SUMS)
out=$(/tmp/b4/install.sh --no-start 2>&1)
rc=$?
if [ $rc -eq 0 ]; then ok "Neuere Version: Exit 0"; else bad "Neuere Version: Exit $rc"; echo "$out" | tail -n 5; fi
check "current -> 0.0.2" test "$(readlink $DATA/app/current)" = "0.0.2"
check "previous -> 0.0.1" test "$(readlink $DATA/app/previous)" = "0.0.1"
check "Konfiguration nach Aktualisierung unverändert" test "$(sha256sum $DATA/config.json | cut -d' ' -f1)" = "$sum_before"

# 7b. Rückschritt: ohne Zustimmung abgelehnt, mit --force-downgrade erlaubt.
out=$(/bundle/install.sh --no-start 2>&1)
rc=$?
if [ $rc -ne 0 ] && echo "$out" | grep -q 'ältere Version 0.0.1'; then ok "Rückschritt: Abbruch mit Meldung"; else bad "Rückschritt nicht abgelehnt (Exit $rc)"; fi
check "Rückschritt: current bleibt 0.0.2" test "$(readlink $DATA/app/current)" = "0.0.2"
out=$(/bundle/install.sh --no-start --force-downgrade 2>&1)
rc=$?
if [ $rc -eq 0 ]; then ok "--force-downgrade: Exit 0"; else bad "--force-downgrade: Exit $rc"; echo "$out" | tail -n 5; fi
check "--force-downgrade: current -> 0.0.1, previous -> 0.0.2" bash -c "[ \"\$(readlink $DATA/app/current)\" = 0.0.1 ] && [ \"\$(readlink $DATA/app/previous)\" = 0.0.2 ]"

# 8. Anderer Datenordner per Option.
out=$(/bundle/install.sh --no-start --data-dir /srv/hk 2>&1)
rc=$?
if [ $rc -eq 0 ]; then ok "--data-dir: Exit 0"; else bad "--data-dir: Exit $rc"; echo "$out" | tail -n 5; fi
check "--data-dir: Dienst unter /srv/hk" test -x /srv/hk/app/current/hmi-kiosk
check "--data-dir: Drop-in nennt /srv/hk" grep -q -- '-data /srv/hk ' /etc/systemd/system/hmi-kiosk.service.d/10-install.conf

# 9. Fremder Datenordner wird nicht angefasst, ein leerer ist erlaubt.
mkdir -p /srv/fremd && echo x >/srv/fremd/datei
out=$(/bundle/install.sh --no-start --data-dir /srv/fremd 2>&1)
rc=$?
if [ $rc -ne 0 ] && echo "$out" | grep -q 'enthält keine Kiosk-Dateien'; then ok "Fremder Ordner: Abbruch mit Meldung"; else bad "Fremder Ordner nicht abgelehnt (Exit $rc)"; fi
check "Fremder Ordner: unverändert (root, kein app/, kein system/)" bash -c '[ "$(stat -c %U /srv/fremd/datei)" = root ] && [ ! -e /srv/fremd/app ] && [ ! -e /srv/fremd/system ] && [ "$(stat -c %U /srv/fremd)" = root ]'
mkdir -p /srv/leer
out=$(/bundle/install.sh --no-start --data-dir /srv/leer 2>&1)
rc=$?
if [ $rc -eq 0 ]; then ok "Leerer Ordner: Exit 0"; else bad "Leerer Ordner: Exit $rc"; echo "$out" | tail -n 5; fi

# 10. System mit Desktop (Display-Manager wie auf Raspberry Pi OS Desktop).
touch /lib/systemd/system/lightdm.service
ln -sfn /lib/systemd/system/lightdm.service /etc/systemd/system/display-manager.service
ln -sfn /lib/systemd/system/graphical.target /etc/systemd/system/default.target
out=$(/bundle/install.sh --no-start 2>&1)
rc=$?
if [ $rc -ne 0 ] && echo "$out" | grep -q 'lightdm.service' && echo "$out" | grep -q -- '--disable-desktop'; then ok "Desktop: Abbruch mit Hinweis auf --disable-desktop"; else bad "Desktop nicht erkannt (Exit $rc)"; echo "$out" | tail -n 5; fi
check "Desktop: Display-Manager bleibt ohne Schalter" test -L /etc/systemd/system/display-manager.service
check "Desktop: Standardziel bleibt ohne Schalter" test "$(readlink /etc/systemd/system/default.target)" = /lib/systemd/system/graphical.target
out=$(/bundle/install.sh --no-start --disable-desktop 2>&1)
rc=$?
if [ $rc -eq 0 ]; then ok "--disable-desktop: Exit 0"; else bad "--disable-desktop: Exit $rc"; echo "$out" | tail -n 5; fi
check "--disable-desktop: Display-Manager abgeschaltet" test ! -e /etc/systemd/system/display-manager.service
check "--disable-desktop: Standardziel multi-user" bash -c 'readlink /etc/systemd/system/default.target | grep -q multi-user.target'
if echo "$out" | grep -q 'sudo reboot'; then ok "--disable-desktop: Hinweis auf Neustart"; else bad "--disable-desktop: Neustart-Hinweis fehlt"; fi
out=$(/bundle/install.sh --no-start --disable-desktop 2>&1)
rc=$?
if [ $rc -eq 0 ]; then ok "--disable-desktop ohne Desktop: Exit 0 (wiederholbar)"; else bad "--disable-desktop wiederholt: Exit $rc"; fi

# 11. Desktop-Ordner von labwc: der Kiosk hat einen eigenen Ordner und lässt den Desktop in Ruhe.
#     Simuliert eine Installation bis 0.1.1, die /etc/xdg/labwc überschrieben hatte.
mkdir -p /etc/xdg/labwc
printf 'wf-panel-pi &\n' >/etc/xdg/labwc/autostart
printf '<desktop-rc/>\n' >/etc/xdg/labwc/rc.xml.vor-hmi-kiosk
cp /bundle/rootfs/etc/hmi-kiosk/labwc/rc.xml /etc/xdg/labwc/rc.xml
cp /bundle/rootfs/etc/hmi-kiosk/labwc/environment /etc/xdg/labwc/environment
out=$(/bundle/install.sh --no-start --disable-desktop 2>&1)
rc=$?
if [ $rc -eq 0 ]; then ok "labwc-Ordner: Exit 0"; else bad "labwc-Ordner: Exit $rc"; echo "$out" | tail -n 5; fi
check "Kiosk-Sitzung nutzt /etc/hmi-kiosk/labwc" grep -q 'labwc -C /etc/hmi-kiosk/labwc' /usr/local/bin/hmi-kiosk-session
check "eigener Autostart ohne Befehle" bash -c '! grep -v "^#" /etc/hmi-kiosk/labwc/autostart | grep -q .'
check "Desktop-Autostart unberührt" test "$(cat /etc/xdg/labwc/autostart)" = "wf-panel-pi &"
check "rc.xml des Desktops wiederhergestellt" test "$(cat /etc/xdg/labwc/rc.xml)" = "<desktop-rc/>"
check "alte Kiosk-environment im Desktop-Ordner entfernt" test ! -e /etc/xdg/labwc/environment


echo
if [ $fails -eq 0 ]; then echo "Alle Prüfungen bestanden."; else echo "$fails Prüfung(en) fehlgeschlagen."; exit 1; fi
