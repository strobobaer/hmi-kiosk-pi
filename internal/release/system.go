package release

// SystemLevel ist der Stand der Systemteile (Helfer, Dateien unter /etc, Pakete), den
// dieses Programm braucht. Ein Programm-Update tauscht nur hmi-kiosk; ändert sich etwas
// an den Systemteilen, wird SystemLevel erhöht und packaging/rootfs/etc/hmi-kiosk/system-level
// gleich mitgezogen. Pakete tragen den Wert, ein Terminal mit älterem Stand lehnt sie ab.
//
// 1: Stand 0.1.6 (Admin-Seite am Terminal in der Chromium-Richtlinie, wvkbd, Helfer mit
// set-web-origins und Netzwerkbefehlen).
const SystemLevel = 1
