#!/bin/sh
# SPI-/UART-Prüfung für das PDH-Terminal (Kartenleser PN532), eingebunden von verify-image.sh und
# von packaging/test-products.sh. Erwartet die Funktionen ok und bad des Aufrufers.
#
#   check_busses <produkt> <wurzel> <boot>
#     wurzel  eingehängtes Systemdateisystem, boot  Boot-Partition (config.txt, cmdline.txt)
# pdh-terminal: dtparam=spi=on und dtparam=uart0=on im letzten [all]-Abschnitt, hmikiosk in spi und dialout,
# keine Seriell-Konsole auf ttyAMA0. hmi-kiosk darf keine der beiden Zeilen haben.

check_busses() {
	_bus_final=$(awk '/^\[/ { sec = $0; blk = ""; next } { blk = blk $0 "\n" } END { if (sec == "" || sec == "[all]") printf "%s", blk }' "$3/config.txt")
	_bus_msg=""
	case "$1" in
	pdh-terminal)
		printf '%s\n' "$_bus_final" | grep -Eq '^dtparam=spi=on[[:space:]]*$' ||
			_bus_msg="$_bus_msg; dtparam=spi=on fehlt im letzten [all]-Abschnitt von config.txt"
		printf '%s\n' "$_bus_final" | grep -Eq '^dtparam=uart0=on[[:space:]]*$' ||
			_bus_msg="$_bus_msg; dtparam=uart0=on fehlt im letzten [all]-Abschnitt von config.txt"
		for _bus_g in spi dialout; do
			grep -Eq "^$_bus_g:[^:]*:[0-9]+:([^,]*,)*hmikiosk(,|\$)" "$2/etc/group" || _bus_msg="$_bus_msg; hmikiosk nicht in Gruppe $_bus_g"
		done
		if grep -Eq '(^|[[:space:]])console=ttyAMA0' "$3/cmdline.txt"; then
			_bus_msg="$_bus_msg; cmdline.txt belegt ttyAMA0 mit console= (UART des Kartenlesers)"
		fi
		if [ -z "$_bus_msg" ]; then
			ok kartenleser-spi-uart "spi=on und uart0=on im letzten [all], hmikiosk in spi und dialout, ttyAMA0 ohne Konsole"
		else
			bad kartenleser-spi-uart "${_bus_msg#; }"
		fi
		;;
	*)
		if grep -Eq '^dtparam=(spi|uart0)=on[[:space:]]*$' "$3/config.txt"; then
			bad kartenleser-spi-uart "config.txt enthält dtparam=spi=on oder dtparam=uart0=on, gehört nur ins PDH-Terminal"
		else
			ok kartenleser-spi-uart "keine SPI-/UART-Zeilen in config.txt (nur PDH-Terminal)"
		fi
		;;
	esac
}
