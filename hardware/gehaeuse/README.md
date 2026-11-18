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
| `pdh-terminal-gehaeuse-step.py` | erzeugt die STEP-Dateien (CadQuery), Maße wie in der `.scad` |
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

## STEP neu erzeugen

Nach den STL-Dateien, weil das Skript sein Ergebnis mit ihnen vergleicht. Ein Maß, das in der `.scad`
geändert wird, muss auch in `pdh-terminal-gehaeuse-step.py` geändert werden; sonst bricht das Skript
mit „STEP weicht von den STL-Dateien ab“ ab (Grenze 0,5 % Volumen).

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD":/w -w /w python:3.12-slim sh -c \
  "apt-get update -qq && apt-get install -y -qq libgl1 libglib2.0-0 >/dev/null && \
   pip install -q --root-user-action=ignore cadquery && python pdh-terminal-gehaeuse-step.py"
```
