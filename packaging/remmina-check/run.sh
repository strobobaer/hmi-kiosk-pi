#!/bin/sh
# Startet Xvfb und einen kleinen Fenstermanager (Regel wie die spätere labwc-Regel), dann die Prüfung.
set -eu
export DISPLAY=:99
Xvfb :99 -screen 0 1280x800x24 -nolisten tcp >/dev/null 2>&1 &
i=0
until xdpyinfo >/dev/null 2>&1; do
	i=$((i + 1))
	if [ "$i" -gt 120 ]; then
		echo "Prüfumgebung fehlerhaft: Xvfb startet nicht" >&2
		exit 2
	fi
	sleep 1
done
openbox --config-file /etc/hmi-remmina-check/openbox-rc.xml >/dev/null 2>&1 &
sleep 3
apt-cache policy remmina remmina-plugin-vnc
exec dbus-run-session -- hmi-kiosk-remmina-check "$@"
