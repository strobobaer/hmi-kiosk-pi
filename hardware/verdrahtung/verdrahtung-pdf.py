"""Erzeugt pdh-terminal-verdrahtung.pdf: Verdrahtung Raspberry Pi 5 – OLED SH1106 – PN532.

Aufruf (Docker, im Ordner hardware/verdrahtung):
  docker run --rm -v "$PWD":/w -w /w python:3.12-slim sh -c \
    "pip install -q --root-user-action=ignore matplotlib reportlab && python verdrahtung-pdf.py"
"""
import os

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.patches import Circle, FancyBboxPatch, Rectangle
from reportlab.lib import colors
from reportlab.lib.enums import TA_LEFT
from reportlab.lib.pagesizes import A4
from reportlab.lib.styles import ParagraphStyle
from reportlab.lib.units import mm
from reportlab.lib.utils import ImageReader
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont
from reportlab.platypus import (Image, KeepTogether, PageBreak, Paragraph, SimpleDocTemplate, Spacer, Table,
                                TableStyle)

OUT = "pdh-terminal-verdrahtung.pdf"
IMG = "bilder"

# ---------------------------------------------------------------------------------------------------
# Raspberry Pi 5, 40-polige Stiftleiste (physische Nummer → Funktion)
# ---------------------------------------------------------------------------------------------------
PINS = {
    1: "3,3 V", 2: "5 V", 3: "GPIO2 SDA", 4: "5 V", 5: "GPIO3 SCL", 6: "GND", 7: "GPIO4", 8: "GPIO14 TXD",
    9: "GND", 10: "GPIO15 RXD", 11: "GPIO17", 12: "GPIO18", 13: "GPIO27", 14: "GND", 15: "GPIO22",
    16: "GPIO23", 17: "3,3 V", 18: "GPIO24", 19: "GPIO10 MOSI", 20: "GND", 21: "GPIO9 MISO", 22: "GPIO25",
    23: "GPIO11 SCLK", 24: "GPIO8 CE0", 25: "GND", 26: "GPIO7 CE1", 27: "GPIO0 ID_SD", 28: "GPIO1 ID_SC",
    29: "GPIO5", 30: "GND", 31: "GPIO6", 32: "GPIO12", 33: "GPIO13", 34: "GND", 35: "GPIO19", 36: "GPIO16",
    37: "GPIO26", 38: "GPIO20", 39: "GND", 40: "GPIO21",
}


def pin_kind(n):
    f = PINS[n]
    if f.startswith("3,3"):
        return "#f4a3a3"
    if f.startswith("5 V"):
        return "#d62728"
    if f == "GND":
        return "#444444"
    return "#cfd8e3"


# Kabelfarben (Vorschlag) je Signal
COL = {
    "VCC": ("#d62728", "rot"), "GND": ("#222222", "schwarz"), "SDA": ("#1f77b4", "blau"),
    "SCL": ("#e6b800", "gelb"), "SCK": ("#ff7f0e", "orange"), "MISO": ("#9467bd", "violett"),
    "MOSI": ("#2ca02c", "grün"), "SS": ("#8c8c8c", "grau"), "TXD": ("#2ca02c", "grün"),
    "RXD": ("#17becf", "türkis"),
}

OLED = ("OLED SH1106 1,3\"", [("GND", "GND", 9), ("VCC", "VCC", 1), ("SCL", "SCL", 5), ("SDA", "SDA", 3)])

VARIANTS = {
    "i2c": dict(
        title="PN532 über I²C",
        header="I²C/HSU-Leiste",
        pins=[("GND", "GND", 6), ("VCC", "VCC", 17), ("SDA", "SDA", 3), ("SCL", "SCL", 5)],
        sel=(True, False),
        admin="PN532 (I²C), Bus /dev/i2c-1, Adresse 0x24",
        config="dtparam=i2c_arm=on (im Abbild ab 0.2 gesetzt)",
        check="i2cdetect -y 1 zeigt 24 (PN532) und 3c (OLED)",
    ),
    "spi": dict(
        title="PN532 über SPI",
        header="SPI-Leiste",
        pins=[("SCK", "SCK", 23), ("MISO", "MISO", 21), ("MOSI", "MOSI", 19), ("SS", "SS", 24),
              ("VCC", "VCC", 17), ("GND", "GND", 20)],
        sel=(False, True),
        admin="PN532 (SPI), Gerät /dev/spidev0.0",
        config="dtparam=spi=on (im Abbild ab der Version mit L1)",
        check="ls /dev/spidev0.0 vorhanden; i2cdetect -y 1 zeigt nur 3c (OLED)",
    ),
    "uart": dict(
        title="PN532 über UART (HSU)",
        header="I²C/HSU-Leiste",
        pins=[("GND", "GND", 14), ("VCC", "VCC", 17), ("TXD", "TXD", 10), ("RXD", "RXD", 8)],
        sel=(False, False),
        admin="PN532 (UART), Gerät /dev/ttyAMA0",
        config="dtparam=uart0=on (im Abbild ab der Version mit L1)",
        check="ls /dev/ttyAMA0 vorhanden; i2cdetect -y 1 zeigt nur 3c (OLED)",
    ),
}

# ---------------------------------------------------------------------------------------------------
# Bilder
# ---------------------------------------------------------------------------------------------------


def pin_xy(n):
    col = (n - 1) // 2
    row = 1 if n % 2 == 0 else 0  # gerade Pins: äußere Reihe (Platinenrand), ungerade: innere Reihe
    return col * 1.0, row * 1.0


def draw_header(ax, used, labels="used"):
    ax.add_patch(FancyBboxPatch((-0.7, -0.7), 20.4, 2.4, boxstyle="round,pad=0.05,rounding_size=0.2",
                                fc="#1d6b3a", ec="#0f3d20", lw=1.5, zorder=1))
    for n in range(1, 41):
        x, y = pin_xy(n)
        fc = used.get(n, pin_kind(n)) if n in used else pin_kind(n)
        ec = "black" if n in used else "#555555"
        if n == 1:
            ax.add_patch(Rectangle((x - 0.32, y - 0.32), 0.64, 0.64, fc=fc, ec="white", lw=1.6, zorder=3))
        else:
            ax.add_patch(Circle((x, y), 0.32, fc=fc, ec=ec, lw=1.6 if n in used else 0.8, zorder=3))
        txt = "white" if fc in ("#444444", "#222222", "#d62728") else "black"
        ax.text(x, y, str(n), ha="center", va="center", fontsize=6.5, color=txt, zorder=4, weight="bold")
        if labels == "all" or (labels == "used" and n in used):
            if y > 0.5:
                ax.text(x, y + 0.55, PINS[n], ha="left", va="bottom", rotation=60, fontsize=7, zorder=4)
            else:
                ax.text(x, y - 0.55, PINS[n], ha="right", va="top", rotation=60, fontsize=7, zorder=4)
    ax.text(-0.9, 0, "Pin 1 ▸", ha="right", va="center", fontsize=8, weight="bold")


def module_box(ax, x0, y0, w, h, name, sub, pins, sel=None):
    ax.add_patch(FancyBboxPatch((x0, y0), w, h, boxstyle="round,pad=0.05,rounding_size=0.3",
                                fc="#2b4f8c" if "PN532" in name else "#1a1a1a", ec="black", lw=1.2, zorder=2))
    ax.text(x0 + w / 2, y0 + h * 0.45, name, ha="center", va="center", color="white", fontsize=10,
            weight="bold", zorder=3)
    ax.text(x0 + w / 2 - (1.1 if sel is not None else 0), y0 + h * 0.2, sub, ha="center", va="center", color="#dddddd", fontsize=7.5, zorder=3)
    xs = []
    step = w / (len(pins) + 1)
    for i, (sig, lbl, _pin) in enumerate(pins):
        px = x0 + step * (i + 1)
        ax.add_patch(Rectangle((px - 0.22, y0 + h - 0.05), 0.44, 0.44, fc="#d4af37", ec="black", lw=0.8, zorder=3))
        ax.text(px, y0 + h - 0.35, lbl, ha="center", va="top", color="white", fontsize=7.5, zorder=3)
        xs.append(px)
    if sel is not None:
        # DIP-Schalter: Hebel oben = ON
        sx, sy = x0 + w - 2.2, y0 + 0.25
        ax.add_patch(Rectangle((sx, sy), 1.9, 1.0, fc="#c0392b", ec="black", lw=0.8, zorder=3))
        for i, on in enumerate(sel):
            cx = sx + 0.45 + i * 1.0
            ax.add_patch(Rectangle((cx - 0.18, sy + 0.12), 0.36, 0.76, fc="#eeeeee", ec="black", lw=0.5, zorder=4))
            ax.add_patch(Rectangle((cx - 0.15, sy + (0.5 if on else 0.15)), 0.3, 0.35, fc="#222222", zorder=5))
            ax.text(cx, sy - 0.08, f"SEL{i}", ha="center", va="top", fontsize=6.5, color="white", zorder=4)
        ax.text(sx + 0.95, sy + 1.08, "ON ▲", ha="center", va="bottom", fontsize=6.5, color="white", zorder=4)
        ax.text(sx - 0.15, sy + 0.5, "Schalter", ha="right", va="center", fontsize=7, color="#ffe28a", zorder=3)
    return xs


def sub_left(sel):
    return f"SEL0 {'ON' if sel[0] else 'OFF'} · SEL1 {'ON' if sel[1] else 'OFF'}"


def route(ax, x_mod, y_mod, n, lane, color):
    hx, hy = pin_xy(n)
    # Ziele in der äußeren Reihe seitlich versetzt anfahren, damit die Leitung nicht über den inneren Pin läuft
    off = 0.5 if hy > 0.5 else 0.0
    pts = [(x_mod, y_mod), (x_mod, lane), (hx + off, lane), (hx + off, hy - 0.5 if off else hy - 0.32)]
    if off:
        pts += [(hx + off, hy), (hx + 0.32, hy)]
    xs, ys = zip(*pts)
    ax.plot(xs, ys, color="white", lw=4.2, solid_capstyle="round", zorder=5)
    ax.plot(xs, ys, color=color, lw=2.6, solid_capstyle="round", zorder=6)


def draw_variant(key, path):
    v = VARIANTS[key]
    used = {}
    for _s, _l, p in OLED[1] + v["pins"]:
        used[p] = COL[_s][0]
    fig, ax = plt.subplots(figsize=(11, 7.2))
    draw_header(ax, used, labels="none")
    oy, mh = -8.6, 3.0
    oxs = module_box(ax, -0.5, oy, 6.5, mh, OLED[0], "I²C-Adresse 0x3C", OLED[1])
    pw = 9.5 if key == "spi" else 7.5
    pxs = module_box(ax, 19.5 - pw, oy, pw, mh, "PN532 V3", v["header"], v["pins"], v["sel"])
    lane = -1.4
    wires = [(x, sig, p) for x, (sig, _l, p) in zip(oxs, OLED[1])] + \
            [(x, sig, p) for x, (sig, _l, p) in zip(pxs, v["pins"])]
    # Leitungen nach Zielspalte sortiert auf Bahnen verteilen
    for i, (x, sig, p) in enumerate(sorted(wires, key=lambda w: abs(pin_xy(w[2])[0] - w[0]), reverse=True)):
        route(ax, x, oy + mh + 0.4, p, lane - i * 0.42, COL[sig][0])
    ax.text(9.5, 3.9, f"Raspberry Pi 5 – 40-polige Stiftleiste (Draufsicht, USB/LAN rechts)", ha="center",
            fontsize=10, weight="bold")
    ax.set_xlim(-2.6, 21)
    ax.set_ylim(oy - 0.6, 4.4)
    ax.set_aspect("equal")
    ax.axis("off")
    fig.savefig(path, dpi=200, bbox_inches="tight", facecolor="white")
    plt.close(fig)


def draw_overview(path):
    fig, ax = plt.subplots(figsize=(11, 3.6))
    draw_header(ax, {}, labels="all")
    ax.set_xlim(-2.6, 21)
    ax.set_ylim(-3.6, 4.2)
    ax.set_aspect("equal")
    ax.axis("off")
    fig.savefig(path, dpi=200, bbox_inches="tight", facecolor="white")
    plt.close(fig)


def draw_board(path):
    """Lage der Stiftleiste auf der Platine."""
    fig, ax = plt.subplots(figsize=(7, 4.6))
    ax.add_patch(FancyBboxPatch((0, 0), 85, 56, boxstyle="round,pad=0,rounding_size=3", fc="#1d6b3a", ec="black"))
    ax.add_patch(Rectangle((7, 50.5), 51, 5, fc="#111111"))
    for n in range(1, 41):
        col = (n - 1) // 2
        y = 54.3 if n % 2 == 0 else 51.7
        ax.add_patch(Circle((8.3 + col * 2.54, y), 0.75, fc="#d4af37" if n != 1 else "#ffffff", ec="none"))
    ax.annotate("Pin 1 (eckiges Pad)", (8.3, 51.7), (-2, 61), fontsize=9, arrowprops=dict(arrowstyle="->"))
    ax.add_patch(Rectangle((67, 2), 21, 14, fc="#bbbbbb", ec="black"))
    ax.text(77.5, 9, "LAN", ha="center", va="center", fontsize=9)
    for i, y in enumerate((19, 36)):
        ax.add_patch(Rectangle((69, y), 19, 14, fc="#3a6fd8" if i == 0 else "#bbbbbb", ec="black"))
        ax.text(78.5, y + 7, "USB 3" if i == 0 else "USB 2", ha="center", va="center", fontsize=9,
                color="white" if i == 0 else "black")
    ax.add_patch(Rectangle((22, 14), 30, 30, fc="#888888", ec="black", alpha=0.85))
    ax.text(37, 29, "Active Cooler", ha="center", va="center", fontsize=9, color="white")
    for x, lab in ((11.2, "USB-C"), (25.8, "HDMI0"), (39.2, "HDMI1")):
        ax.add_patch(Rectangle((x - 4, -1.5), 8, 4, fc="#bbbbbb", ec="black"))
        ax.text(x, -4, lab, ha="center", va="top", fontsize=8)
    ax.set_xlim(-3, 92)
    ax.set_ylim(-8, 64)
    ax.set_aspect("equal")
    ax.axis("off")
    fig.savefig(path, dpi=200, bbox_inches="tight", facecolor="white")
    plt.close(fig)


# ---------------------------------------------------------------------------------------------------
# PDF
# ---------------------------------------------------------------------------------------------------
fd = os.path.join(matplotlib.get_data_path(), "fonts", "ttf")
pdfmetrics.registerFont(TTFont("DV", os.path.join(fd, "DejaVuSans.ttf")))
pdfmetrics.registerFont(TTFont("DVB", os.path.join(fd, "DejaVuSans-Bold.ttf")))

S = {
    "h1": ParagraphStyle("h1", fontName="DVB", fontSize=20, leading=24, spaceAfter=8),
    "h2": ParagraphStyle("h2", fontName="DVB", fontSize=14, leading=18, spaceBefore=6, spaceAfter=6),
    "h3": ParagraphStyle("h3", fontName="DVB", fontSize=11, leading=14, spaceBefore=6, spaceAfter=3),
    "p": ParagraphStyle("p", fontName="DV", fontSize=9.5, leading=13, alignment=TA_LEFT, spaceAfter=4),
    "small": ParagraphStyle("s", fontName="DV", fontSize=8, leading=10.5, textColor=colors.HexColor("#444444")),
    "warn": ParagraphStyle("w", fontName="DV", fontSize=9.5, leading=13, backColor=colors.HexColor("#fff4d6"),
                           borderColor=colors.HexColor("#e0a800"), borderWidth=0.8, borderPadding=6,
                           spaceBefore=6, spaceAfter=10),
}


def P(t, s="p"):
    return Paragraph(t, S[s])


def img(path, width):
    """Bild mit fester Breite, Höhe aus dem Seitenverhältnis der Datei."""
    w, h = ImageReader(path).getSize()
    return Image(path, width=width, height=width * h / w)


def table(rows, widths):
    data = [[P(f"<b>{c}</b>", "p") for c in rows[0]]] + [[P(str(c), "p") for c in r] for r in rows[1:]]
    t = Table(data, colWidths=widths, repeatRows=1)
    t.setStyle(TableStyle([("GRID", (0, 0), (-1, -1), 0.4, colors.HexColor("#999999")),
                           ("BACKGROUND", (0, 0), (-1, 0), colors.HexColor("#e8edf3")),
                           ("VALIGN", (0, 0), (-1, -1), "MIDDLE"),
                           ("TOPPADDING", (0, 0), (-1, -1), 2), ("BOTTOMPADDING", (0, 0), (-1, -1), 2)]))
    return t


def wiring_rows(v):
    rows = [["Modul", "Anschluss am Modul", "Pi-Pin", "Funktion am Pi", "Kabel"]]
    for name, pins in ((OLED[0], OLED[1]), ("PN532", v["pins"])):
        for sig, lbl, p in pins:
            rows.append([name, lbl, str(p), PINS[p], COL[sig][1]])
    return rows


def on_page(c, doc):
    c.setFont("DV", 7.5)
    c.setFillColor(colors.HexColor("#777777"))
    c.drawString(18 * mm, 10 * mm, "PDH-Terminal · Verdrahtung Raspberry Pi 5, OLED SH1106, PN532")
    c.drawRightString(A4[0] - 18 * mm, 10 * mm, f"Seite {doc.page}")


os.makedirs(IMG, exist_ok=True)
draw_overview(f"{IMG}/stiftleiste.png")
draw_board(f"{IMG}/platine.png")
for k in VARIANTS:
    draw_variant(k, f"{IMG}/{k}.png")

W = A4[0] - 36 * mm
story = [
    P("Verdrahtung PDH-Terminal", "h1"),
    P("Raspberry Pi 5 · OLED-Statusdisplay SH1106 1,3\" · Kartenleser PN532 (Elechouse V3)", "h3"),
    Spacer(1, 4),
    P("Diese Anleitung zeigt die drei Anschlussarten des PN532 (I²C, SPI, UART). Das OLED hängt in allen "
      "drei Fällen gleich am I²C-Bus. Wähle eine Anschlussart, verdrahte nach der passenden Seite, stelle "
      "die Schalter am PN532 ein und wähle dieselbe Art auf der Admin-Seite unter <b>Kartenleser</b>."),
    P("<b>Vor dem Verdrahten:</b> Pi ausschalten und das Netzteil abziehen. Alle Module an <b>3,3 V</b> "
      "betreiben, nie an 5 V: Die GPIO-Pins des Pi vertragen nur 3,3 V, und mit 3,3 V am Modul liegen "
      "auch dessen Signale auf 3,3 V. Die Schalter des PN532 nur im ausgeschalteten Zustand umstellen; "
      "der PN532 liest sie beim Einschalten.", "warn"),
    P("Übersicht", "h2"),
    table([["Anschlussart", "Seite", "Schalter SEL0 / SEL1", "Admin-Seite, Typ", "Verfügbar"],
           ["PN532 über I²C", "3", "ON / OFF", "PN532 (I²C)", "ab 0.2"],
           ["PN532 über SPI", "4", "OFF / ON", "PN532 (SPI)", "ab der Version mit L1"],
           ["PN532 über UART", "5", "OFF / OFF", "PN532 (UART)", "ab der Version mit L1"]],
          [32 * mm, 14 * mm, 38 * mm, 36 * mm, W - 120 * mm]),
    Spacer(1, 8),
    P("Material", "h3"),
    P("• Dupont-Kabel Buchse–Buchse, ca. 20 cm: 4 für das OLED, 4 bis 6 für den PN532<br/>"
      "• bei PN532 über I²C zusätzlich zwei Y-Verbinder oder ein kleines Stück Lochraster für SDA und SCL "
      "(siehe Seite 3)<br/>"
      "• PN532-Modul mit eingelöteter Stiftleiste für die gewählte Anschlussart"),
    P("Lage der Stiftleiste", "h2"),
    img(f"{IMG}/platine.png", 110 * mm),
    P("Pin 1 erkennt man am eckigen Lötpad auf der Unterseite. Ungerade Pins liegen in der inneren Reihe, "
      "gerade Pins am Platinenrand. Der Active Cooler lässt die Stiftleiste frei.", "small"),
    PageBreak(),
    P("Belegung der Stiftleiste", "h2"),
    img(f"{IMG}/stiftleiste.png", W),
    Spacer(1, 6),
    P("Verwendete Pins aller Varianten", "h3"),
    table([["Pi-Pin", "Funktion", "verwendet von"],
           ["1", "3,3 V", "OLED VCC"], ["3", "GPIO2 SDA", "OLED SDA; PN532 SDA (nur I²C)"],
           ["5", "GPIO3 SCL", "OLED SCL; PN532 SCL (nur I²C)"], ["6", "GND", "PN532 GND (I²C)"],
           ["8", "GPIO14 TXD", "PN532 RXD (UART)"], ["9", "GND", "OLED GND"],
           ["10", "GPIO15 RXD", "PN532 TXD (UART)"], ["14", "GND", "PN532 GND (UART)"],
           ["17", "3,3 V", "PN532 VCC (alle)"], ["19", "GPIO10 MOSI", "PN532 MOSI (SPI)"],
           ["20", "GND", "PN532 GND (SPI)"], ["21", "GPIO9 MISO", "PN532 MISO (SPI)"],
           ["23", "GPIO11 SCLK", "PN532 SCK (SPI)"], ["24", "GPIO8 CE0", "PN532 SS (SPI)"]],
          [18 * mm, 32 * mm, W - 50 * mm]),
    Spacer(1, 6),
    P("OLED und PN532 bekommen getrennte Pins für 3,3 V (1 und 17) und Masse. So braucht die Versorgung "
      "keinen Verteiler. Pins 27 und 28 (ID_SD, ID_SC) sind für HAT-Kennungen reserviert und bleiben frei.",
      "small"),
]

for i, (k, v) in enumerate(VARIANTS.items()):
    story += [PageBreak(), P(f"Variante {i + 1}: {v['title']} + OLED", "h2"),
              img(f"{IMG}/{k}.png", 0.8 * W),
              table(wiring_rows(v), [36 * mm, 34 * mm, 16 * mm, 34 * mm, W - 120 * mm])]
    extra = []
    if k == "i2c":
        extra.append(P("<b>SDA und SCL werden geteilt:</b> OLED und PN532 hängen beide an Pin 3 (SDA) und Pin 5 "
                       "(SCL). Auf jeden dieser Pins passt nur eine Dupont-Buchse. Verbinde die beiden Leitungen "
                       "deshalb über einen Y-Verbinder (2 Buchsen auf 1 Buchse) oder ein kleines Stück "
                       "Lochraster mit je einer 3-poligen Stiftleiste für SDA und SCL. Die Adressen "
                       "unterscheiden sich (PN532 0x24, OLED 0x3C), deshalb stören sich die Module nicht.",
                       "warn"))
    if k == "uart":
        extra.append(P("<b>Über Kreuz:</b> TXD des Moduls geht an RXD des Pi (Pin 10), RXD des Moduls an TXD "
                       "des Pi (Pin 8). Beim Elechouse V3 sind das die Stifte <b>SDA</b> (= TXD) und <b>SCL</b> "
                       "(= RXD) der 4-poligen Leiste.", "warn"))
    if k == "spi":
        extra.append(P("IRQ und RSTO der SPI-Leiste bleiben frei. Das OLED hängt hier allein am I²C-Bus und "
                       "braucht keinen Y-Verbinder.", "small"))
    extra += [P("Einstellen und prüfen", "h3"),
              table([["Was", "Einstellung"], ["Schalter am PN532", sub_left(v["sel"])],
                     ["Admin-Seite → Kartenleser", v["admin"]],
                     ["Admin-Seite → Statusdisplay", "SH1106, Bus /dev/i2c-1, Adresse 0x3C, „Testen“"],
                     ["config.txt", v["config"]],
                     ["Kontrolle", v["check"] + "; Admin-Seite → System → Selbstprüfung: "
                                                "„kartenleser“ und „statusdisplay“ grün"]],
                    [50 * mm, W - 50 * mm])]
    story.append(KeepTogether(extra))

story += [
    PageBreak(),
    P("Hinweise", "h2"),
    P("OLED-Module", "h3"),
    P("Die Reihenfolge der vier Stifte unterscheidet sich je nach Hersteller (GND–VCC–SCL–SDA oder "
      "VCC–GND–SCL–SDA). Verdrahte immer nach der Beschriftung auf dem Modul, nicht nach der Lage im Bild. "
      "Ein vertauschtes VCC/GND kann das Modul zerstören."),
    P("Einbau ins Gehäuse G1", "h3"),
    P("PN532 und OLED liegen in ihren Rahmen im Deckel. Die Kabel laufen durch die Öffnung an der hinteren "
      "Seite des jeweiligen Rahmens nach unten zur Stiftleiste. Die Dupont-Stecker passen unter den "
      "PN532-Rahmen; die Kabel so lang lassen, dass der Deckel zum Aufsetzen gekippt werden kann "
      "(ca. 20 cm)."),
    P("Fehlersuche", "h3"),
    table([["Anzeige", "Ursache", "Abhilfe"],
           ["Kartenleser: „keine Antwort“", "Schalterstellung passt nicht zur Anschlussart, oder Schalter "
            "bei laufendem Modul umgestellt", "Schalter prüfen, Pi aus- und wieder einschalten"],
           ["„SPI/UART ist im System nicht eingeschaltet“", "Programm-Update ohne neues Abbild",
            "neues Abbild aufspielen oder die genannte dtparam-Zeile in /boot/firmware/config.txt eintragen, "
            "neu starten"],
           ["UART: keine Antwort", "TXD/RXD nicht über Kreuz", "die beiden Signalleitungen tauschen"],
           ["Statusdisplay antwortet nicht", "SDA/SCL vertauscht oder Adresse 0x3D",
            "Leitungen tauschen; i2cdetect -y 1 zeigt die echte Adresse, auf der Admin-Seite eintragen"],
           ["Display bleibt dunkel, Test grün", "SSD1306 statt SH1106 bestückt",
            "Modul mit SH1106 verwenden (1,3\"-Module haben fast immer SH1106)"]],
          [42 * mm, 58 * mm, W - 100 * mm]),
]

doc = SimpleDocTemplate(OUT, pagesize=A4, leftMargin=18 * mm, rightMargin=18 * mm, topMargin=16 * mm,
                        bottomMargin=16 * mm, title="Verdrahtung PDH-Terminal", author="PDH-Terminal",
                        subject="Raspberry Pi 5, OLED SH1106, PN532")
doc.build(story, onFirstPage=on_page, onLaterPages=on_page)
print("geschrieben:", OUT)
