#!/bin/sh
# Läuft im Container (siehe Dockerfile): candle + light unter Wine. Aufruf: build-in-container.sh VERSION PRODUCTCODE
# Erwartet das Repository unter /work. Ergebnis: dist/PDH-Terminal-<VERSION>.msi
set -eu
ver="$1"
pc="$2"
cd /work
mkdir -p /tmp/obj
rm -f "dist/PDH-Terminal-$ver.msi" "dist/PDH-Terminal-$ver.wixpdb"
run() { xvfb-run -a wine "$@"; }
run /opt/wix/candle.exe -nologo -arch x64 -ext WixUtilExtension -ext WixFirewallExtension \
	"-dVersion=$ver" "-dProductCode=$pc" "-dBinDir=dist\win" "-dSrcDir=packaging\windows" \
	-out 'Z:\tmp\obj\' packaging/windows/msi/pdh-terminal.wxs
# -sval: Die ICE-Prüfung braucht den Windows Installer-Dienst, den Wine nicht vollständig bietet.
run /opt/wix/light.exe -nologo -sval -ext WixUtilExtension -ext WixFirewallExtension \
	-out "dist/PDH-Terminal-$ver.msi" Z:/tmp/obj/pdh-terminal.wixobj
rm -f "dist/PDH-Terminal-$ver.wixpdb"
test -s "dist/PDH-Terminal-$ver.msi"
