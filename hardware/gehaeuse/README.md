# Pultgehäuse G1 für das PDH-Terminal

Zweiteiliges Tischgehäuse (Oberseite 15° geneigt) für Raspberry Pi 5 mit offiziellem Active Cooler,
PN532-Kartenleser (Elechouse V3) und OLED SH1106 1,3" (I²C). Der Bildschirm ist extern.

| Datei | Inhalt |
|---|---|
| `pdh-terminal-gehaeuse.scad` | parametrische Quelle (OpenSCAD 2021.01), alle Maße oben in der Datei |
| `pdh-terminal-base.stl` | Wanne, 104 × 112 mm, Boden aufs Druckbett |
| `pdh-terminal-lid.stl` | Pultdeckel, bereits gedreht: Sichtseite liegt auf dem Druckbett |
| `pdh-terminal-base.step`, `pdh-terminal-lid.step` | dieselben Teile als STEP mit echten Flächen, beide in Einbaulage (für Fusion, FreeCAD, Onshape …) |
| `pdh-terminal-baugruppe.step` | Wanne und Deckel zusammen als Baugruppe |
| `fusion360/PDH-Terminal-Gehaeuse/` | Fusion-360-Skript: baut das Gehäuse parametrisch mit Zeitleiste auf |
| `pdh-terminal-gehaeuse-step.py` | erzeugt die STEP-Dateien (CadQuery) aus demselben Bauplan wie das Fusion-Skript |
| `vorschau-*.png` | Ansichten aus den STL-Dateien |

## Maße zuerst prüfen

Die Maße stammen aus Datenblättern, nicht aus einer Messung. Vor dem Druck nachmessen:

- PN532-Platine (`pn_w`, `pn_d`) und OLED-Platine (`ol_w`, `ol_d`);
- Lage des OLED-Glases auf der Platine; weicht sie von der Mitte ab, `win_off` setzen;
- Anschlüsse des Pi 5 (`side_ports`, `port_row_h`).

Am besten zuerst nur den Deckel drucken und PN532 und OLED einlegen.

## Druck

PETG (PLA geht, wird über dem Kühler aber weich), 0,2 mm Schichthöhe, 3 Wände, 20 % Füllung, keine Stützen.

## Stückliste

- 3× Schraube M3×16 und 3× M3-Einschmelzmutter (Ø 4,2 × 6); ohne Einschmelzmuttern `use_inserts = false` setzen, dann gehen Blechschrauben 3,0 mm
- 4× Blechschraube M2,5×6 für den Pi
- 4 Gummifüße Ø 10
- etwas Heißkleber oder doppelseitiges Klebeband für PN532 und OLED

## Montage

1. Einschmelzmuttern mit dem Lötkolben in die drei Säulen des Deckels setzen.
2. Pi mit Kühler auf die Abstandshalter der Wanne schrauben, USB/LAN zur Rückwand.
3. PN532 (Schalter für die gewünschte Schnittstelle vorher setzen) und OLED in ihre Rahmen im Deckel legen, mit einem Tropfen Heißkleber sichern; die Kabel laufen durch die Öffnungen hinten im Rahmen zur Stiftleiste.
4. Deckel aufsetzen, von unten mit den drei M3-Schrauben verschrauben, Gummifüße in die Mulden kleben.

Die hintere linke Ecke hat keine Schraubsäule (dort liegt der Pi); sie hält über den umlaufenden Passrand.

## STL neu erzeugen

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD":/w -w /w openscad/openscad:2021.01 \
  openscad -o pdh-terminal-base.stl -D 'part="base"' pdh-terminal-gehaeuse.scad
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD":/w -w /w openscad/openscad:2021.01 \
  openscad -o pdh-terminal-lid.stl -D 'part="lid"' pdh-terminal-gehaeuse.scad
```

Die Ausgabe muss `Volumes: 2` zeigen (ein geschlossener Körper) und darf keine Warnung enthalten.

## In Fusion 360 bearbeiten

1. In Fusion: **Dienstprogramme → Add-Ins → Skripte und Add-Ins** (Umschalt+S), Reiter **Skripte**.
2. Auf **+** neben „Meine Skripte“ klicken und den Ordner `fusion360/PDH-Terminal-Gehaeuse` wählen.
3. Das Skript auswählen und **Ausführen**. Es legt ein neues Design an; am Ende zeigt eine Meldung die
   Volumen von Wanne und Deckel im Vergleich zur OpenSCAD-Fassung.
4. Design speichern. Ab dann ist es ein normales Fusion-Design: Maße unter **Ändern → Parameter ändern**
   (z. B. `pn_b`, `ol_b`, `fenster_vers`, `neigung`), alles Weitere über die Zeitleiste.

Die Körper heißen „Wanne“ und „Deckel“ und liegen in Einbaulage. Zum Drucken den Deckel als STL
exportieren und im Slicer mit der Sichtseite nach unten legen. Ohne Einschmelzmuttern `insert_d` auf 2,5
setzen. Die Kartenecken versucht das Skript als Skizzenrundung zu setzen; gelingt das in einer
Fusion-Version nicht, bleiben sie eckig, die Maße gelten trotzdem.

Bauplan, Fusion-Skript und STEP-Skript sind eine Quelle: Die Geometrie steht nur in
`PDH-Terminal-Gehaeuse.py` (Funktion `build`). Wer dort etwas ändert, prüft es mit dem STEP-Skript.

## STEP neu erzeugen

Nach den STL-Dateien, weil das Skript sein Ergebnis mit ihnen vergleicht. Ein Maß, das in der `.scad`
geändert wird, muss auch in den Parametern des Fusion-Skripts geändert werden; sonst bricht das Skript
mit „STEP weicht von den STL-Dateien ab“ ab (Grenze 0,5 % Volumen).

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD":/w -w /w python:3.12-slim sh -c \
  "apt-get update -qq && apt-get install -y -qq libgl1 libglib2.0-0 >/dev/null && \
   pip install -q --root-user-action=ignore cadquery && python pdh-terminal-gehaeuse-step.py"
```
