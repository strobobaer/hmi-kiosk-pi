#!/bin/bash -e
# Tailscale, Teil 2 (im Chroot): Paket aus der signierten Quelle installieren; Quelle und Schlüsselbund
# hat 00-run.sh angelegt (Fingerabdruck des Schlüssels: 2596 A99E AAB3 3821 893C  0A79 458C A832 957F 5868).
# Kein "tailscale up": die Anmeldung am Tailnet macht der Helfer zur Laufzeit mit einem Einmalschlüssel.
apt-get update
apt-get install -y --no-install-recommends tailscale
systemctl enable tailscaled
