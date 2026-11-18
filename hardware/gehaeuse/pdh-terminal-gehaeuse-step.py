"""STEP-Ausgabe des Pultgehäuses G1 (echte Flächen statt Dreiecke) mit CadQuery.

Führt denselben Bauplan aus wie das Fusion-360-Skript (fusion360/PDH-Terminal-Gehaeuse/), damit beide
nicht auseinanderlaufen. Am Ende wird das Volumen mit den STL-Dateien der OpenSCAD-Fassung verglichen;
bei mehr als 0,5 % Abweichung wird nichts geschrieben.

Aufruf (Docker, im Ordner hardware/gehaeuse):
  docker run --rm -v "$PWD":/w -w /w python:3.12-slim sh -c \
    "apt-get update -qq && apt-get install -y -qq libgl1 libglib2.0-0 >/dev/null && \
     pip install -q --root-user-action=ignore cadquery && python pdh-terminal-gehaeuse-step.py"

Ausgabe: pdh-terminal-base.step, pdh-terminal-lid.step (beide in Einbaulage) und
pdh-terminal-baugruppe.step (beide Teile zusammen).
"""
import importlib.util
import os
import sys

import cadquery as cq

_here = os.path.dirname(os.path.abspath(__file__))
_spec = importlib.util.spec_from_file_location(
    "plan", os.path.join(_here, "fusion360", "PDH-Terminal-Gehaeuse", "PDH-Terminal-Gehaeuse.py"))
plan = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(plan)


class CadQueryBuilder:
    def __init__(self):
        self.env = plan.make_env()
        self.bodies = {}

    def params(self, _params):
        pass  # Werte stehen schon in env

    def ev(self, expr):
        return plan.ev(expr, self.env)

    def _rect(self, r, z, h):
        x, y, w, d = (self.ev(v) for v in (r.x, r.y, r.w, r.h))
        s = cq.Workplane("XY").box(w, d, h, centered=False).translate((x, y, z))
        if r.r:
            s = s.edges("|Z").fillet(self.ev(r.r))
        return s

    def new(self, name, prism):
        z, h = self.ev(prism.z), self.ev(prism.h)
        out = None
        for s in prism.shapes:
            if isinstance(s, plan.Ring):
                part = self._rect(s.outer, z, h).cut(self._rect(s.inner, z - 1, h + 2))
            elif isinstance(s, plan.Circle):
                part = (cq.Workplane("XY").circle(self.ev(s.d) / 2).extrude(h)
                        .translate((self.ev(s.x), self.ev(s.y), z)))
            else:
                part = self._rect(s, z, h)
            out = part if out is None else out.union(part)
        self.bodies[name] = out
        if prism.lift:
            self.move(name, prism.lift)

    def move(self, name, lift):
        self.bodies[name] = (self.bodies[name].rotate((0, 0, 0), (1, 0, 0), self.env["neigung"])
                             .translate((0, 0, self.ev(lift))))

    def cut(self, target, tools):
        for t in tools:
            self.bodies[target] = self.bodies[target].cut(self.bodies.pop(t))

    def join(self, target, tools):
        for t in tools:
            self.bodies[target] = self.bodies[target].union(self.bodies.pop(t))

    def split(self, target, z, lower, upper):
        e, body = self.env, self.bodies.pop(target)
        zz = self.ev(z)
        big = e["breite"] + 2, e["tiefe"] + 2
        self.bodies[lower] = body.intersect(
            cq.Workplane("XY").box(big[0], big[1], zz + 1, centered=False).translate((-1, -1, -1)))
        self.bodies[upper] = body.intersect(
            cq.Workplane("XY").box(big[0], big[1], e["hoehe_hinten"] + 1, centered=False).translate((-1, -1, zz)))


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


b = CadQueryBuilder()
plan.build(b)
base, lid = b.bodies["Wanne"], b.bodies["Deckel"]

ok = True
for name, part, stl in (("Wanne", base, "pdh-terminal-base.stl"), ("Deckel", lid, "pdh-terminal-lid.stl")):
    solids = part.solids().size()
    vol = part.val().Volume()
    ref = stl_volume(stl)
    dev = abs(vol - ref) / ref * 100
    print(f"{name}: {solids} Körper, {vol:.0f} mm³, STL {ref:.0f} mm³, Abweichung {dev:.2f} %")
    # Die STL nähert Zylinder mit 48 Ecken an; mehr als 0,5 % heißt: Plan und .scad passen nicht.
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
