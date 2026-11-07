#!/bin/sh
# Test von identity.sh (hmi-kiosk-identity) mit Ersatzpfaden. Braucht root (Besitzerprüfung), sonst nichts:
#
#   docker run --rm -v "$PWD:/src" -w /src debian:trixie sh packaging/image/test-identity.sh
#
# Prüft: Originalgerät (erster Lauf legt /data/system/identity an, ändert nichts), gleiche Seriennummer
# (setzt Maschinen-ID und Rechnername aus der Datei), Klon (neue Maschinen-ID, Rechnername aus der
# Seriennummer, stabil beim nächsten Lauf), DMI-Rückfall, Fallen (Verweis statt Ordner, Verweis statt
# Datei, falscher Besitzer, für andere schreibbar, unlesbare Seriennummer, kaputte oder feindliche Datei)
# und dass der Dienst nie mit Fehler endet.
set -u

SCRIPT=$(pwd)/packaging/image/identity.sh
fails=0
ok() { echo "OK $1"; }
bad() {
	echo "FEHLER $1"
	fails=$((fails + 1))
}

if [ "$(id -u)" != 0 ]; then
	echo "Der Test braucht root (Besitzerprüfung)"
	exit 2
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"
PATH="$work/bin:$PATH"
export PATH

# Ersatz für hostname: ohne Argument den "Kernel-Namen" ausgeben, sonst setzen und aufzeichnen.
cat >"$work/bin/hostname" <<STUB
#!/bin/sh
if [ \$# -eq 0 ]; then cat "$work/khost"; else echo "\$1" >"$work/khost"; echo "\$1" >>"$work/hostname.calls"; fi
STUB
chmod 755 "$work/bin/hostname"

reset() {
	rm -rf "$work/proc" "$work/sys" "$work/data" "$work/ziel" "$work/fremd" "$work/hostname.calls" "$work/pwned"
	mkdir -p "$work/proc/device-tree" "$work/sys/class/dmi/id" "$work/data"
	chmod 755 "$work/data"
	printf '10000000ABCDEF01\000' >"$work/proc/device-tree/serial-number"
	echo aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa >"$work/machine-id"
	echo raspberrypi >"$work/hostname"
	echo raspberrypi >"$work/khost"
	printf '127.0.0.1\tlocalhost\n127.0.1.1\traspberrypi\n' >"$work/hosts"
}

DIR=$work/data/system
rc=0
run() {
	HMI_IDENTITY_PROC=$work/proc HMI_IDENTITY_SYS=$work/sys HMI_IDENTITY_DIR=$DIR \
		HMI_IDENTITY_MACHINE_ID=$work/machine-id HMI_IDENTITY_HOSTNAME=$work/hostname HMI_IDENTITY_HOSTS=$work/hosts \
		sh "$SCRIPT" >"$work/out" 2>&1
	rc=$?
	return 0
}
mid() { cat "$work/machine-id"; }
hn() { cat "$work/hostname"; }
field() { sed -n "s/^$1=//p" "$DIR/identity" | head -n 1; }
calls() { if [ -f "$work/hostname.calls" ]; then cat "$work/hostname.calls"; fi; }
snapshot() { echo "$(mid)|$(hn)|$(cat "$work/khost")|$(cat "$work/hosts")|$(calls)"; }
exit0() { [ "$rc" = 0 ]; }
VALID_FILE='serial=10000000abcdef01
machine_id=cccccccccccccccccccccccccccccccc
hostname=fremd'

echo "== Originalgerät: erster Lauf"
reset
before=$(snapshot)
run
if exit0 && [ -f "$DIR/identity" ] && [ "$(field serial)" = 10000000abcdef01 ] &&
	[ "$(field machine_id)" = aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa ] && [ "$(field hostname)" = raspberrypi ] &&
	[ "$(snapshot)" = "$before" ]; then
	ok "Datei angelegt (serial, machine_id, hostname), System unverändert"
else
	bad "erster Lauf: rc=$rc $(cat "$DIR/identity" 2>/dev/null) $(cat "$work/out")"
fi
if [ "$(stat -c '%u %a' "$DIR")" = "0 755" ] && [ "$(stat -c '%u %a' "$DIR/identity")" = "0 644" ]; then
	ok "Ordner und Datei gehören root, nicht für andere schreibbar"
else
	bad "Rechte: $(stat -c '%u %a' "$DIR") $(stat -c '%u %a' "$DIR/identity")"
fi

echo "== gleiche Seriennummer, System weicht ab (Overlay beim Start leer)"
echo bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb >"$work/machine-id"
echo anderername >"$work/hostname"
echo anderername >"$work/khost"
run
if exit0 && [ "$(mid)" = aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa ] && [ "$(hn)" = raspberrypi ] &&
	[ "$(cat "$work/khost")" = raspberrypi ] && grep -q '127.0.1.1[[:space:]]raspberrypi' "$work/hosts"; then
	ok "Maschinen-ID und Rechnername aus der Datei gesetzt"
else
	bad "Wiederherstellen: $(snapshot) $(cat "$work/out")"
fi
before=$(snapshot)
idfile=$(cat "$DIR/identity")
run
if exit0 && [ "$(snapshot)" = "$before" ] && [ "$(cat "$DIR/identity")" = "$idfile" ]; then
	ok "nichts zu tun: nichts geändert"
else
	bad "unnötige Änderung: $(snapshot)"
fi

echo "== Klon: andere Seriennummer"
printf '10000000FEDCBA98\000' >"$work/proc/device-tree/serial-number"
run
newid=$(mid)
if exit0 && [ "$newid" != aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa ] && printf '%s' "$newid" | grep -Eq '^[0-9a-f]{32}$' &&
	[ "$(hn)" = hmi-kiosk-dcba98 ] && [ "$(cat "$work/khost")" = hmi-kiosk-dcba98 ] &&
	grep -q '127.0.1.1[[:space:]]hmi-kiosk-dcba98' "$work/hosts"; then
	ok "neue Maschinen-ID, Rechnername hmi-kiosk-dcba98"
else
	bad "Klon: $(snapshot) $(cat "$work/out")"
fi
if [ "$(field serial)" = 10000000fedcba98 ] && [ "$(field machine_id)" = "$newid" ] && [ "$(field hostname)" = hmi-kiosk-dcba98 ]; then
	ok "Datei neu geschrieben"
else
	bad "Datei nach Klon: $(cat "$DIR/identity")"
fi
before=$(snapshot)
run
if exit0 && [ "$(snapshot)" = "$before" ] && [ "$(mid)" = "$newid" ]; then
	ok "nächster Lauf auf dem Klon ändert nichts"
else
	bad "Klon nicht stabil: $(snapshot)"
fi

echo "== DMI-Rückfall (kein Device-Tree)"
reset
rm -f "$work/proc/device-tree/serial-number"
echo 4C4C4544-0042-3510-8047-B8C04F4D3532 >"$work/sys/class/dmi/id/product_uuid"
run
if exit0 && [ "$(field serial)" = 4c4c4544-0042-3510-8047-b8c04f4d3532 ]; then
	ok "product_uuid als Kennung"
else
	bad "DMI: $(cat "$work/out")"
fi
echo 4C4C4544-0042-3510-8047-000000ABCDEF >"$work/sys/class/dmi/id/product_uuid"
run
if exit0 && [ "$(hn)" = hmi-kiosk-abcdef ]; then
	ok "Klon über DMI: Rechnername aus dem Ende der Kennung"
else
	bad "DMI-Klon: $(hn)"
fi

echo "== keine lesbare Kennung"
for content in "" "0000000000000000" "ab cd" "ab/cd"; do
	reset
	printf '%s' "$content" >"$work/proc/device-tree/serial-number"
	before=$(snapshot)
	run
	if exit0 && [ ! -e "$DIR" ] && [ "$(snapshot)" = "$before" ]; then
		ok "Kennung '$content': nichts getan, Exit 0"
	else
		bad "Kennung '$content': rc=$rc $(snapshot)"
	fi
done
reset
rm -f "$work/proc/device-tree/serial-number"
run
if exit0 && [ ! -e "$DIR" ]; then ok "gar keine Quelle: nichts getan"; else bad "keine Quelle: rc=$rc"; fi
reset
rm -f "$work/proc/device-tree/serial-number"
ln -s "$work/machine-id" "$work/proc/device-tree/serial-number"
run
if exit0 && [ ! -e "$DIR" ]; then ok "Seriennummer als Verweis wird nicht gelesen"; else bad "Verweis als Seriennummer gelesen"; fi

echo "== Falle: /data/system ist ein Verweis"
reset
mkdir "$work/ziel"
ln -s "$work/ziel" "$DIR"
before=$(snapshot)
run
if exit0 && [ -z "$(ls -A "$work/ziel")" ] && [ "$(snapshot)" = "$before" ] && grep -q 'Verweis' "$work/out"; then
	ok "Verweis auf Ordner: nichts geschrieben, protokolliert"
else
	bad "Verweis auf Ordner: rc=$rc $(ls -A "$work/ziel") $(cat "$work/out")"
fi
reset
ln -s "$work/gibt-es-nicht" "$DIR"
run
if exit0 && [ ! -e "$work/gibt-es-nicht" ]; then ok "hängender Verweis: nichts angelegt"; else bad "hängender Verweis: rc=$rc"; fi

echo "== Falle: identity ist ein Verweis"
reset
mkdir -p "$DIR"
chmod 755 "$DIR"
printf '%s\n' "$VALID_FILE" >"$work/fremd"
ln -s "$work/fremd" "$DIR/identity"
before=$(snapshot)
fremd=$(cat "$work/fremd")
run
if exit0 && [ "$(snapshot)" = "$before" ] && [ "$(cat "$work/fremd")" = "$fremd" ] && [ -L "$DIR/identity" ]; then
	ok "Verweis statt Datei: nichts übernommen, Ziel unverändert"
else
	bad "Verweis statt Datei: rc=$rc $(snapshot)"
fi

echo "== Falle: falscher Besitzer oder für andere schreibbar"
reset
mkdir -p "$DIR"
chmod 755 "$DIR"
chown 1100:1100 "$DIR"
before=$(snapshot)
run
if exit0 && [ ! -e "$DIR/identity" ] && [ "$(snapshot)" = "$before" ]; then
	ok "Ordner gehört dem Dienstbenutzer: nichts getan"
else
	bad "falscher Besitzer: rc=$rc $(ls -A "$DIR")"
fi
reset
mkdir -p "$DIR"
chmod 777 "$DIR"
run
if exit0 && [ ! -e "$DIR/identity" ]; then ok "Ordner für andere schreibbar: nichts getan"; else bad "schreibbarer Ordner: rc=$rc"; fi
reset
mkdir -p "$DIR"
chmod 755 "$DIR"
printf '%s\n' "$VALID_FILE" >"$DIR/identity"
chown 1100:1100 "$DIR/identity"
before=$(snapshot)
run
if exit0 && [ "$(snapshot)" = "$before" ]; then ok "Datei gehört dem Dienstbenutzer: nichts übernommen"; else bad "Datei mit falschem Besitzer: $(snapshot)"; fi

echo "== kaputte oder feindliche Datei"
check_hostile() {
	reset
	mkdir -p "$DIR"
	chmod 755 "$DIR"
	printf '%s\n' "$1" >"$DIR/identity"
	before=$(snapshot)
	run
	if exit0 && [ "$(snapshot)" = "$before" ] && [ ! -e "$work/pwned" ] && [ "$(cat "$DIR/identity")" = "$1" ]; then
		ok "Datei abgelehnt, nichts geändert ($2)"
	else
		bad "Datei nicht abgelehnt ($2): rc=$rc $(snapshot)"
	fi
}
check_hostile "Müll" "Müll"
check_hostile "" "leer"
check_hostile "serial=10000000abcdef01" "nur serial"
check_hostile "serial=10000000abcdef01
machine_id=XYZ
hostname=ok" "machine_id ungültig"
check_hostile "serial=10000000abcdef01
machine_id=cccccccccccccccccccccccccccccccc
hostname=x; touch $work/pwned" "Befehl im Rechnernamen"
check_hostile "serial=10000000abcdef01
machine_id=cccccccccccccccccccccccccccccccc
hostname=-bad" "Rechnername beginnt mit Minus"
check_hostile "serial=10000000abcdef01
machine_id=cccccccccccccccccccccccccccccccc
hostname=a/b" "Schrägstrich im Rechnernamen"
reset
mkdir -p "$DIR"
chmod 755 "$DIR"
head -c 100000 /dev/zero | tr '\0' 'a' >"$DIR/identity"
before=$(snapshot)
run
if exit0 && [ "$(snapshot)" = "$before" ]; then ok "überlange Datei abgelehnt"; else bad "überlange Datei: $(snapshot)"; fi

echo "== Originalgerät mit uninitialisierter Maschinen-ID"
reset
echo uninitialized >"$work/machine-id"
run
if exit0 && printf '%s' "$(mid)" | grep -Eq '^[0-9a-f]{32}$' && [ "$(field machine_id)" = "$(mid)" ]; then
	ok "ungültige Maschinen-ID ersetzt und festgehalten"
else
	bad "uninitialisiert: $(mid) $(cat "$DIR/identity" 2>/dev/null)"
fi

echo "== Fehler enden nie mit Exit ungleich 0"
reset
mkdir -p "$DIR/identity"
run
if exit0; then ok "identity ist ein Ordner: Exit 0"; else bad "Exit $rc"; fi
reset
rm -rf "$work/data"
run
if exit0; then ok "/data fehlt: Exit 0"; else bad "Exit $rc"; fi
reset
printf '%s\n' "$VALID_FILE" >/dev/null
HMI_IDENTITY_PROC=$work/proc HMI_IDENTITY_SYS=$work/sys HMI_IDENTITY_DIR=$DIR HMI_IDENTITY_MACHINE_ID=$work/gibt/es/nicht \
	HMI_IDENTITY_HOSTNAME=$work/hostname HMI_IDENTITY_HOSTS=$work/hosts sh "$SCRIPT" >"$work/out" 2>&1
rc=$?
if exit0; then ok "Maschinen-ID-Datei nicht lesbar: Exit 0"; else bad "Exit $rc"; fi

if [ "$fails" -gt 0 ]; then
	echo "$fails Prüfung(en) gescheitert"
	exit 1
fi
echo "Alle Prüfungen bestanden."
