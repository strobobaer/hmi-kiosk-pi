#!/bin/bash
# Prüft check-release-pub.sh gegen das Format, das "hmi-kiosk-pack keygen" schreibt:
# 64 kleingeschriebene Hex-Zeichen und genau ein Zeilenumbruch (65 Byte).
set -u
here=$(cd "$(dirname "$0")" && pwd)
chk="$here/check-release-pub.sh"
t=$(mktemp -d)
trap 'rm -rf "$t"' EXIT
fails=0
h=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef

expect() { # expect <ok|fail> <Beschreibung>; Datei $t/p
	if "$chk" "$t/p" >/dev/null 2>"$t/err"; then got=ok; else got=fail; fi
	if [ "$got" != "$1" ]; then
		echo "FEHLER $2 (erwartet $1, war $got)"
		fails=$((fails + 1))
	elif [ "$1" = fail ] && ! grep -q "$t/p" "$t/err"; then
		echo "FEHLER $2: Meldung nennt die Datei nicht: $(cat "$t/err")"
		fails=$((fails + 1))
	else
		echo "OK $2"
	fi
}

printf '%s\n' "$h" >"$t/p"
expect ok "keygen-Format"
printf '%s' "$h" >"$t/p"
expect fail "ohne Zeilenumbruch"
printf '%s\r\n' "$h" >"$t/p"
expect fail "CRLF"
printf '%s\n' "${h^^}" >"$t/p"
expect fail "Großbuchstaben"
printf '%s\n\n' "$h" >"$t/p"
expect fail "Leerzeile dahinter"
printf '%s\n%s\n' "$h" "$h" >"$t/p"
expect fail "zwei Zeilen"
printf ' %s\n' "$h" >"$t/p"
expect fail "führendes Leerzeichen"
printf '%s\n' "${h:0:63}" >"$t/p"
expect fail "63 Zeichen"
printf '%sf\n' "$h" >"$t/p"
expect fail "65 Zeichen"
printf '%s\n' "${h:0:63}g" >"$t/p"
expect fail "kein Hex"
: >"$t/p"
expect fail "leere Datei"
rm "$t/p"
expect fail "fehlende Datei"

[ "$fails" -eq 0 ] && echo "Alle Prüfungen bestanden." || { echo "$fails gescheitert"; exit 1; }
