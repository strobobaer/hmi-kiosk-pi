"""STEP-Ausgabe des Pultgehäuses G1 (echte Flächen statt Dreiecke) mit CadQuery.

Nachbau von pdh-terminal-gehaeuse.scad mit denselben Maßen. Wer dort ein Maß ändert, ändert es hier
auch; die Prüfung am Ende vergleicht das Volumen mit den STL-Dateien und bricht bei Abweichung ab.

Aufruf (Docker, im Ordner hardware/gehaeuse):
  docker run --rm -v "$PWD":/w -w /w python:3.12-slim sh -c \
    "apt-get update -qq && apt-get install -y -qq libgl1 libglib2.0-0 >/dev/null && \
     pip install -q cadquery && python pdh-terminal-gehaeuse-step.py"

Ausgabe: pdh-terminal-base.step, pdh-terminal-lid.step (beide in Einbaulage) und
pdh-terminal-baugruppe.step (beide Teile zusammen).
"""
import math
import sys

import cadquery as cq

# ---------- Außenmaße ----------
W, D, Hf, tilt = 104, 112, 32, 15
wall, floor_t, top_t, thin_t = 2.2, 2.0, 2.4, 1.2
z_split = 16
Hb = Hf + D * math.tan(math.radians(tilt))

# ---------- Raspberry Pi 5 ----------
pi_l, pi_w, pi_t = 85, 56, 1.4
so_h, so_d, so_hole = 4, 6, 2.5
pi_x0 = wall + 2.0
pi_y0 = D - wall - 2.5 - pi_l
pi_z = floor_t + so_h
pi_holes = [(3.5, 3.5), (61.5, 3.5), (3.5, 52.5), (61.5, 52.5)]
side_ports = [(11.2, 12, 8), (25.8, 11, 8), (39.2, 11, 8)]
port_row_h = 17
sd_y = 28

# ---------- Kartenzone, PN532, OLED ----------
card_w, card_d, card_r, card_s0 = 85.6, 54, 3.2, 8
pn_w, pn_d, pn_clear = 42.7, 40.4, 1.6
frame_h, frame_t, pn_gap = 4, 1.6, 24
ol_w, ol_d, ol_clear, ol_frame_h = 35.4, 33.5, 0.6, 3
ol_s, win_w, win_d, win_off, ol_gap = 87, 31, 17, 0, 14

# ---------- Verschraubung, Passrand, Füße ----------
use_inserts = True
post_d, insert_d, insert_h, pilot_d = 7, 4.2, 6, 2.5
hole_d, head_d, screw_len = 3.4, 6.5, 16
posts = [(6.5, 6.5), (W - 6.5, 6.5), (W - 6.5, D - 6.5)]
lip_h, lip_t, lip_gap = 2, 1.2, 0.25
foot_d, foot_depth = 10.5, 1
feet = [(14, 12), (W - 14, 12), (W - 14, D - 14), (14, D - 14)]

cx = W / 2
pn_s = card_s0 + card_d / 2


def ztop_in(y):
    return Hf + y * math.tan(math.radians(tilt)) - top_t / math.cos(math.radians(tilt))


def box(x, y, z, sx, sy, sz):
    return cq.Workplane("XY").box(sx, sy, sz, centered=False).translate((x, y, z))


def cyl(x, y, z, d, h):
    return cq.Workplane("XY").circle(d / 2).extrude(h).translate((x, y, z))


def on_top(w):
    """Koordinaten auf der Oberseite: x quer, y entlang der Schräge, z Normale (innen < 0)."""
    return w.rotate((0, 0, 0), (1, 0, 0), tilt).translate((0, 0, Hf))


def union(parts):
    out = parts[0]
    for p in parts[1:]:
        out = out.union(p)
    return out


def side_profile(x0, width, pts):
    # Ebene YZ: lokales x = globales Y, lokales y = globales Z, Extrusion entlang +X
    return cq.Workplane("YZ", origin=(x0, 0, 0)).polyline(pts).close().extrude(width)


def outer():
    return side_profile(0, W, [(0, 0), (D, 0), (D, Hb), (0, Hf)])


def inner():
    return side_profile(wall, W - 2 * wall, [(wall, floor_t), (D - wall, floor_t),
                                             (D - wall, ztop_in(D - wall)), (wall, ztop_in(wall))])


def frame(iw, idp, t, h, gap):
    return (box(-t, -t, 0, iw + 2 * t, idp + 2 * t, h)
            .cut(box(0, 0, -1, iw, idp, h + 2))
            .cut(box((iw - gap) / 2, idp - 1, -1, gap, t + 2, h + 2)))


def pn_frame():
    iw, idp = pn_w + pn_clear, pn_d + pn_clear
    return on_top(frame(iw, idp, frame_t, frame_h + 0.01, pn_gap)
                  .translate((cx - iw / 2, pn_s - idp / 2, -top_t - frame_h)))


def ol_frame():
    iw, idp = ol_w + ol_clear, ol_d + ol_clear
    return on_top(frame(iw, idp, frame_t, ol_frame_h + 0.01, ol_gap)
                  .translate((cx - iw / 2, ol_s - idp / 2, -top_t - ol_frame_h)))


def back_port_cut():
    return box(pi_x0 + 0.5, D - wall - 1, pi_z + pi_t - 0.5, pi_w - 1, wall + 2, port_row_h + 0.5)


def rrect(w, d, r):
    return cq.Workplane("XY").center(w / 2, d / 2).rect(w, d).extrude(1).edges("|Z").fillet(r)


def cutouts():
    c = []
    for p in side_ports:
        c.append(box(-1, pi_y0 + p[0] - p[1] / 2, pi_z + pi_t + 1.6 - p[2] / 2, wall + 2, p[1], p[2]))
    c.append(back_port_cut())
    c.append(box(pi_x0 + sd_y - 9, pi_y0 - 5, -1, 18, 18, floor_t + 2))
    for h in pi_holes:
        c.append(cyl(pi_x0 + h[1], pi_y0 + h[0], 1, so_hole, so_h + floor_t))
    for x, y in posts:
        c.append(cyl(x, y, -1, hole_d, z_split + 1.01))
        c.append(cyl(x, y, -1, head_d, z_split + insert_h - screw_len + 1))
        c.append(cyl(x, y, z_split - 0.01, insert_d if use_inserts else pilot_d,
                     insert_h if use_inserts else insert_h + 2))
    for y in range(36, 87, 5):
        c.append(box(W - wall - 1, y, 5, wall + 2, 2.5, 8))
    for x0 in (12, W - 30):
        for i in range(5):
            c.append(on_top(box(x0 + 4 * i, 72, -top_t - 1, 2, 28, top_t + 2)))
    ring = rrect(card_w, card_d, card_r).cut(rrect(card_w - 2, card_d - 2, card_r - 1).translate((1, 1, 0)))
    c.append(on_top(ring.translate((cx - card_w / 2, card_s0, -0.4))))
    c.append(on_top(box(cx - (pn_w + pn_clear) / 2 - 0.05, pn_s - (pn_d + pn_clear) / 2 - 0.05, -top_t - 0.01,
                        pn_w + pn_clear + 0.1, pn_d + pn_clear + 0.1, top_t - thin_t + 0.01)))
    c.append(on_top(box(cx - win_w / 2, ol_s - win_d / 2 + win_off, -top_t - 1, win_w, win_d, top_t + 2)))
    for x, y in feet:
        c.append(cyl(x, y, -0.01, foot_d, foot_depth))
    return union(c)


def body():
    o = outer()
    shell = o.cut(inner())
    bosses = union([cyl(x, y, 0, post_d, Hb) for x, y in posts] +
                   [cyl(pi_x0 + h[1], pi_y0 + h[0], 0, so_d, pi_z) for h in pi_holes])
    return union([shell, o.intersect(bosses), pn_frame(), ol_frame()]).cut(cutouts())


def lip_ring(o, i, z0, h):
    return box(o, o, z0, W - 2 * o, D - 2 * o, h).cut(box(i, i, z0 - 1, W - 2 * i, D - 2 * i, h + 2))


def stl_volume(path):
    tris, cur = [], []
    with open(path) as f:
        for line in f:
            t = line.split()
            if t and t[0] == "vertex":
                cur.append(tuple(float(v) for v in t[1:4]))
                if len(cur) == 3:
                    tris.append(cur)
                    cur = []
    v = 0.0
    for a, b, c in tris:
        v += (a[0] * (b[1] * c[2] - b[2] * c[1]) - a[1] * (b[0] * c[2] - b[2] * c[0])
              + a[2] * (b[0] * c[1] - b[1] * c[0])) / 6
    return abs(v)


b = body()
base = b.intersect(box(-1, -1, -1, W + 2, D + 2, z_split + 1)).union(
    lip_ring(wall / 2, wall + lip_t, z_split - 0.01, lip_h).cut(back_port_cut()))
lid = b.intersect(box(-1, -1, z_split, W + 2, D + 2, Hb)).cut(
    lip_ring(wall / 2 - lip_gap, wall + lip_t + 0.6, z_split - 1, lip_h + 1.3))

ok = True
for name, part in (("base", base), ("lid", lid)):
    solids = part.solids().size()
    vol = part.val().Volume()
    ref = stl_volume(f"pdh-terminal-{name}.stl")
    dev = abs(vol - ref) / ref * 100
    print(f"{name}: {solids} Körper, {vol:.0f} mm³, STL {ref:.0f} mm³, Abweichung {dev:.2f} %")
    # Die STL nähert Zylinder mit 48 Ecken an; mehr als 0,5 % heißt: Maße weichen von der .scad ab.
    if solids != 1 or dev > 0.5:
        ok = False
if not ok:
    sys.exit("STEP weicht von den STL-Dateien ab, nichts geschrieben")

cq.exporters.export(base, "pdh-terminal-base.step")
cq.exporters.export(lid, "pdh-terminal-lid.step")
asm = (cq.Assembly(name="pdh-terminal-gehaeuse")
       .add(base, name="wanne", color=cq.Color(0.45, 0.52, 0.6))
       .add(lid, name="deckel", color=cq.Color(0.88, 0.88, 0.86)))
asm.export("pdh-terminal-baugruppe.step")
print("geschrieben: pdh-terminal-base.step, pdh-terminal-lid.step, pdh-terminal-baugruppe.step")
