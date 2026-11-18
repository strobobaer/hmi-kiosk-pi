"""PDH-Terminal Pultgehäuse G1 – parametrisch für Fusion 360.

In Fusion: Dienstprogramme → Add-Ins → Skripte und Add-Ins → Reiter „Skripte“ → „+“ → diesen Ordner wählen
→ Ausführen. Das Skript legt ein neues Design an: Benutzerparameter (Ändern → Parameter ändern), Skizzen
mit Bemaßungen, die auf diese Parameter verweisen, und eine Zeitleiste. Ergebnis: Körper „Wanne“ und
„Deckel“ in Einbaulage.

Die Geometrie steht genau einmal, als Bauplan (build). Zwei Ausführungen lesen ihn: Fusion (unten) und
CadQuery (../../pdh-terminal-gehaeuse-step.py, erzeugt die STEP-Dateien und prüft den Plan gegen die STL).
"""
import math

# ---------------------------------------------------------------------------------------------------
# Benutzerparameter: Name, Ausdruck, Einheit, Beschreibung. Reihenfolge = Abhängigkeiten.
# ---------------------------------------------------------------------------------------------------
PARAMS = [
    ("breite", "104", "mm", "Außenbreite links-rechts"),
    ("tiefe", "112", "mm", "Außentiefe vorn-hinten"),
    ("hoehe_vorn", "32", "mm", "Höhe an der Vorderkante"),
    ("neigung", "15", "deg", "Neigung der Oberseite"),
    ("wand", "2.2", "mm", "Wandstärke"),
    ("boden_t", "2", "mm", "Bodenstärke"),
    ("deckel_t", "2.4", "mm", "Stärke der Oberseite"),
    ("duenn_t", "1.2", "mm", "Oberseite über der PN532-Antenne"),
    ("trennhoehe", "16", "mm", "Trennebene Wanne/Deckel"),
    ("hoehe_hinten", "hoehe_vorn + tiefe * tan(neigung)", "mm", "Höhe hinten (abgeleitet)"),
    # Raspberry Pi 5
    ("pi_l", "85", "mm", "Pi-Platine Länge"),
    ("pi_w", "56", "mm", "Pi-Platine Breite"),
    ("pi_t", "1.4", "mm", "Pi-Platine Dicke"),
    ("so_h", "4", "mm", "Abstandshalter Höhe"),
    ("so_d", "6", "mm", "Abstandshalter Durchmesser"),
    ("so_loch", "2.5", "mm", "Kernloch M2,5"),
    ("pi_x0", "wand + 2", "mm", "Pi-Kante zur linken Wand"),
    ("pi_y0", "tiefe - wand - 2.5 - pi_l", "mm", "Pi-Lage, USB/LAN an der Rückwand"),
    ("pi_z", "boden_t + so_h", "mm", "Unterseite Pi-Platine"),
    ("port_h", "17", "mm", "Höhe USB/LAN-Ausschnitt"),
    ("sd_y", "28", "mm", "microSD quer zur Platine"),
    # Kartenzone, PN532
    ("karte_b", "85.6", "mm", "Karte Breite"),
    ("karte_t", "54", "mm", "Karte Tiefe"),
    ("karte_r", "3.2", "mm", "Karte Eckradius"),
    ("karte_s0", "8", "mm", "Kartenumriss ab Vorderkante (auf der Schräge)"),
    ("pn_b", "42.7", "mm", "PN532 Breite"),
    ("pn_t", "40.4", "mm", "PN532 Tiefe"),
    ("pn_spiel", "1.6", "mm", "Spiel PN532 im Rahmen"),
    ("rahmen_h", "4", "mm", "PN532-Rahmen Höhe"),
    ("rahmen_t", "1.6", "mm", "Rahmen Wandstärke"),
    ("pn_luecke", "24", "mm", "Kabelöffnung PN532-Rahmen"),
    ("pn_s", "karte_s0 + karte_t / 2", "mm", "PN532-Mitte auf der Schräge"),
    ("pn_ib", "pn_b + pn_spiel", "mm", "PN532-Rahmen innen breit"),
    ("pn_it", "pn_t + pn_spiel", "mm", "PN532-Rahmen innen tief"),
    # OLED
    ("ol_b", "35.4", "mm", "OLED-Platine Breite"),
    ("ol_t", "33.5", "mm", "OLED-Platine Tiefe"),
    ("ol_spiel", "0.6", "mm", "Spiel OLED im Rahmen"),
    ("ol_rahmen_h", "3", "mm", "OLED-Rahmen Höhe"),
    ("ol_s", "87", "mm", "OLED-Mitte auf der Schräge"),
    ("fenster_b", "31", "mm", "Sichtfenster Breite"),
    ("fenster_t", "17", "mm", "Sichtfenster Tiefe"),
    ("fenster_vers", "0", "mm", "Sichtfenster zum Glas verschieben"),
    ("ol_luecke", "14", "mm", "Kabelöffnung OLED-Rahmen"),
    ("ol_ib", "ol_b + ol_spiel", "mm", "OLED-Rahmen innen breit"),
    ("ol_it", "ol_t + ol_spiel", "mm", "OLED-Rahmen innen tief"),
    # Verschraubung, Passrand, Füße
    ("saeule_d", "7", "mm", "Schraubsäule Durchmesser"),
    ("saeule_abst", "6.5", "mm", "Säulenmitte von der Außenkante"),
    ("insert_d", "4.2", "mm", "Loch Einschmelzmutter (ohne Mutter: 2.5)"),
    ("insert_h", "6", "mm", "Tiefe Einschmelzmutter"),
    ("schraube_d", "3.4", "mm", "Durchgang M3"),
    ("kopf_d", "6.5", "mm", "Senkung Schraubenkopf"),
    ("schraube_l", "16", "mm", "Schraubenlänge"),
    ("rand_h", "2", "mm", "Passrand Höhe"),
    ("rand_t", "1.2", "mm", "Passrand Stärke"),
    ("rand_spiel", "0.25", "mm", "Spiel am Passrand"),
    ("fuss_d", "10.5", "mm", "Mulde Gummifuß"),
    ("fuss_tiefe", "1", "mm", "Tiefe Fußmulde"),
    ("fuss_x", "14", "mm", "Fußmitte von links/rechts"),
    ("fuss_y", "12", "mm", "Fußmitte von vorn"),
]

# Kontrollwerte (mm³) aus der OpenSCAD-Fassung; Abweichung > 0,5 % heißt: Plan und .scad passen nicht.
REF_VOLUME = {"Wanne": 35924.0, "Deckel": 52495.0}

# ---------------------------------------------------------------------------------------------------
# Formen. Koordinaten sind Ausdrücke in mm. Ohne lift liegen sie in Welt-Koordinaten (x quer, y vorn→
# hinten, z hoch). Mit lift gelten Koordinaten der Oberseite (z = Normale, innen < 0): der Körper wird
# um die X-Achse um „neigung“ gedreht und dann um lift angehoben.
# ---------------------------------------------------------------------------------------------------


class Rect:
    def __init__(self, x, y, w, h, r=None):
        self.x, self.y, self.w, self.h, self.r = x, y, w, h, r


class Circle:
    def __init__(self, x, y, d):
        self.x, self.y, self.d = x, y, d


class Ring:
    """Fläche zwischen zwei Rechtecken."""

    def __init__(self, outer, inner):
        self.outer, self.inner = outer, inner


class Prism:
    def __init__(self, shapes, z, h, lift=None):
        self.shapes, self.z, self.h, self.lift = shapes, z, h, lift


def box(x, y, w, h, z, height, lift=None):
    return Prism([Rect(x, y, w, h)], z, height, lift)


def slope(lift):
    # Werkzeug oberhalb der Schräge: alles darüber wird abgeschnitten.
    return box("-1", "-tiefe", "breite + 2", "3 * tiefe", "0", "2 * hoehe_hinten", lift)


def frame(name, b, ib, it, s, h, gap):
    """Rahmen auf der Innenseite der Oberseite, Kabelöffnung hinten."""
    outer = Rect(f"breite / 2 - {ib} / 2 - rahmen_t", f"{s} - {it} / 2 - rahmen_t",
                 f"{ib} + 2 * rahmen_t", f"{it} + 2 * rahmen_t")
    inner = Rect(f"breite / 2 - {ib} / 2", f"{s} - {it} / 2", ib, it)
    b.new(name, Prism([Ring(outer, inner)], f"-deckel_t - {h}", f"{h} + 0.01"))
    b.new(name + "_luecke", box(f"breite / 2 - {gap} / 2", f"{s} + {it} / 2 - 1", gap, "rahmen_t + 2",
                                f"-deckel_t - {h} - 1", f"{h} + 2"))
    b.cut(name, [name + "_luecke"])
    b.move(name, "hoehe_vorn")


def back_port():
    return box("pi_x0 + 0.5", "tiefe - wand - 1", "pi_w - 1", "wand + 2", "pi_z + pi_t - 0.5", "port_h + 0.5")


def lip_ring(o, i, z, h):
    outer = Rect(o, o, f"breite - 2 * ({o})", f"tiefe - 2 * ({o})")
    inner = Rect(i, i, f"breite - 2 * ({i})", f"tiefe - 2 * ({i})")
    return Prism([Ring(outer, inner)], z, h)


POSTS = [("saeule_abst", "saeule_abst"), ("breite - saeule_abst", "saeule_abst"),
         ("breite - saeule_abst", "tiefe - saeule_abst")]
PI_HOLES = [("3.5", "3.5"), ("61.5", "3.5"), ("3.5", "52.5"), ("61.5", "52.5")]  # entlang, quer
SIDE_PORTS = [("11.2", "12"), ("25.8", "11"), ("39.2", "11")]  # USB-C, HDMI0, HDMI1: Lage, Breite
FEET = [("fuss_x", "fuss_y"), ("breite - fuss_x", "fuss_y"), ("breite - fuss_x", "tiefe - 14"),
        ("fuss_x", "tiefe - 14")]


def build(b):
    """Der Bauplan. b ist die Ausführung (Fusion oder CadQuery)."""
    b.params(PARAMS)
    # Grundkörper und Innenraum, beide oben schräg abgeschnitten
    b.new("koerper", box("0", "0", "breite", "tiefe", "0", "hoehe_hinten"))
    b.new("innen", box("wand", "wand", "breite - 2 * wand", "tiefe - 2 * wand", "boden_t", "hoehe_hinten"))
    b.new("schraege_innen", slope("hoehe_vorn - deckel_t / cos(neigung)"))
    b.cut("innen", ["schraege_innen"])
    b.cut("koerper", ["innen"])
    # Schraubsäulen und Abstandshalter
    b.new("saeulen", Prism([Circle(x, y, "saeule_d") for x, y in POSTS], "0", "hoehe_hinten"))
    b.new("abstandshalter", Prism([Circle(f"pi_x0 + {q}", f"pi_y0 + {a}", "so_d") for a, q in PI_HOLES],
                                  "0", "pi_z"))
    b.join("koerper", ["saeulen", "abstandshalter"])
    b.new("schraege", slope("hoehe_vorn"))
    b.cut("koerper", ["schraege"])
    # Rahmen für PN532 und OLED
    frame("pn_rahmen", b, "pn_ib", "pn_it", "pn_s", "rahmen_h", "pn_luecke")
    frame("ol_rahmen", b, "ol_ib", "ol_it", "ol_s", "ol_rahmen_h", "ol_luecke")
    b.join("koerper", ["pn_rahmen", "ol_rahmen"])
    # Ausschnitte
    cuts = []

    def cut(name, prism):
        b.new(name, prism)
        cuts.append(name)

    cut("seitenanschluesse", Prism([Rect("-1", f"pi_y0 + {p} - {w} / 2", "wand + 2", w) for p, w in SIDE_PORTS],
                                   "pi_z + pi_t + 1.6 - 4", "8"))
    cut("usb_lan", back_port())
    cut("microsd", box("pi_x0 + sd_y - 9", "pi_y0 - 5", "18", "18", "-1", "boden_t + 2"))
    cut("kernloecher", Prism([Circle(f"pi_x0 + {q}", f"pi_y0 + {a}", "so_loch") for a, q in PI_HOLES],
                             "1", "so_h + boden_t"))
    cut("schraubloecher", Prism([Circle(x, y, "schraube_d") for x, y in POSTS], "-1", "trennhoehe + 1.01"))
    cut("senkungen", Prism([Circle(x, y, "kopf_d") for x, y in POSTS], "-1",
                           "trennhoehe + insert_h - schraube_l + 1"))
    cut("einschmelzmuttern", Prism([Circle(x, y, "insert_d") for x, y in POSTS], "trennhoehe - 0.01", "insert_h"))
    cut("auslass", Prism([Rect("breite - wand - 1", str(y), "wand + 2", "2.5") for y in range(36, 87, 5)],
                         "5", "8"))
    cut("ansaugung", Prism([Rect(f"{x0} + {4 * i}", "72", "2", "28") for x0 in ("12", "breite - 30")
                            for i in range(5)], "-deckel_t - 1", "deckel_t + 2", "hoehe_vorn"))
    card_o = Rect("breite / 2 - karte_b / 2", "karte_s0", "karte_b", "karte_t", "karte_r")
    card_i = Rect("breite / 2 - karte_b / 2 + 1", "karte_s0 + 1", "karte_b - 2", "karte_t - 2", "karte_r - 1")
    cut("kartenumriss", Prism([Ring(card_o, card_i)], "-0.4", "1", "hoehe_vorn"))
    cut("antennenzone", box("breite / 2 - pn_ib / 2 - 0.05", "pn_s - pn_it / 2 - 0.05", "pn_ib + 0.1",
                            "pn_it + 0.1", "-deckel_t - 0.01", "deckel_t - duenn_t + 0.01", "hoehe_vorn"))
    cut("oled_fenster", box("breite / 2 - fenster_b / 2", "ol_s - fenster_t / 2 + fenster_vers", "fenster_b",
                            "fenster_t", "-deckel_t - 1", "deckel_t + 2", "hoehe_vorn"))
    cut("fussmulden", Prism([Circle(x, y, "fuss_d") for x, y in FEET], "-0.01", "fuss_tiefe"))
    b.cut("koerper", cuts)
    # Teilen, Passrand an der Wanne, Aussparung im Deckel
    b.split("koerper", "trennhoehe", "Wanne", "Deckel")
    b.new("rand_aussparung", lip_ring("wand / 2 - rand_spiel", "wand + rand_t + 0.6", "trennhoehe - 1",
                                      "rand_h + 1.3"))
    b.cut("Deckel", ["rand_aussparung"])
    b.new("passrand", lip_ring("wand / 2", "wand + rand_t", "trennhoehe - 0.01", "rand_h"))
    b.new("usb_lan_rand", back_port())
    b.cut("passrand", ["usb_lan_rand"])
    b.join("Wanne", ["passrand"])


# ---------------------------------------------------------------------------------------------------
# Auswertung der Ausdrücke (für Lagen beim Zeichnen und für CadQuery)
# ---------------------------------------------------------------------------------------------------


def make_env():
    env = {"tan": lambda a: math.tan(math.radians(a)), "cos": lambda a: math.cos(math.radians(a))}
    for name, expr, _unit, _c in PARAMS:
        env[name] = ev(expr, env)
    return env


def ev(expr, env):
    return float(eval(str(expr), {"__builtins__": {}}, env))


# ---------------------------------------------------------------------------------------------------
# Fusion 360
# ---------------------------------------------------------------------------------------------------


class FusionBuilder:
    def __init__(self, design):
        import adsk.core
        import adsk.fusion
        self.core, self.fusion = adsk.core, adsk.fusion
        self.design = design
        self.root = design.rootComponent
        self.env = make_env()
        self.bodies = {}
        self.sign = ""  # Vorzeichen der Drehung, beim ersten Drehen geprüft
        self.sign_checked = False

    def vi(self, expr):
        return self.core.ValueInput.createByString(str(expr))

    def pt(self, x, y):
        return self.core.Point3D.create(x / 10.0, y / 10.0, 0)  # Fusion rechnet intern in cm

    def params(self, params):
        up = self.design.userParameters
        for name, expr, unit, comment in params:
            try:
                float(expr)
                expr = f"{expr} {unit}"
            except ValueError:
                pass
            if up.itemByName(name) is None:
                up.add(name, self.vi(expr), unit, comment)

    # --- Skizze -----------------------------------------------------------------------------------

    def dim(self, sk, p1, p2, horizontal, expr, val):
        F = self.fusion
        if abs(val) < 1e-9:
            # gleiche Lage: ausrichten statt Maß 0
            gc = sk.geometricConstraints
            (gc.addVerticalPoints if horizontal else gc.addHorizontalPoints)(p1, p2)
            return
        orient = (F.DimensionOrientations.HorizontalDimensionOrientation if horizontal
                  else F.DimensionOrientations.VerticalDimensionOrientation)
        g1, g2 = p1.geometry, p2.geometry
        tp = self.core.Point3D.create((g1.x + g2.x) / 2 + 0.2, (g1.y + g2.y) / 2 + 0.2, 0)
        d = sk.sketchDimensions.addDistanceDimension(p1, p2, orient, tp)
        d.parameter.expression = str(expr) if val > 0 else f"-({expr})"

    def corner(self, lines, x, y):
        for i in range(lines.count):
            ln = lines.item(i)
            for p in (ln.startSketchPoint, ln.endSketchPoint):
                g = p.geometry
                if abs(g.x - x / 10.0) < 1e-6 and abs(g.y - y / 10.0) < 1e-6:
                    return p
        raise RuntimeError("Ecke nicht gefunden")

    def rect(self, sk, r):
        e = self.env
        x, y, w, h = (ev(v, e) for v in (r.x, r.y, r.w, r.h))
        lines = sk.sketchCurves.sketchLines.addTwoPointRectangle(self.pt(x, y), self.pt(x + w, y + h))
        bl, br = self.corner(lines, x, y), self.corner(lines, x + w, y)
        tr, tl = self.corner(lines, x + w, y + h), self.corner(lines, x, y + h)
        o = sk.originPoint
        self.dim(sk, o, bl, True, r.x, x)
        self.dim(sk, o, bl, False, r.y, y)
        self.dim(sk, bl, br, True, r.w, w)
        self.dim(sk, br, tr, False, r.h, h)
        if r.r:
            rad = ev(r.r, e) / 10.0
            arcs = sk.sketchCurves.sketchArcs
            for c in (bl, br, tr, tl):
                try:
                    ls = [lines.item(i) for i in range(lines.count)
                          if c in (lines.item(i).startSketchPoint, lines.item(i).endSketchPoint)]
                    if len(ls) == 2:
                        arcs.addFillet(ls[0], c.geometry, ls[1], c.geometry, rad)
                except Exception:
                    pass  # Ecke bleibt scharf; die Bemaßung bleibt gültig

    def circle(self, sk, c):
        e = self.env
        x, y, d = ev(c.x, e), ev(c.y, e), ev(c.d, e)
        circ = sk.sketchCurves.sketchCircles.addByCenterRadius(self.pt(x, y), d / 20.0)
        dd = sk.sketchDimensions.addDiameterDimension(circ, self.pt(x + d, y + d))
        dd.parameter.expression = str(c.d)
        o = sk.originPoint
        self.dim(sk, o, circ.centerSketchPoint, True, c.x, x)
        self.dim(sk, o, circ.centerSketchPoint, False, c.y, y)

    # --- Körper -----------------------------------------------------------------------------------

    def new(self, name, prism):
        F = self.fusion
        sk = self.root.sketches.add(self.root.xYConstructionPlane)
        sk.name = name
        ring = False
        for s in prism.shapes:
            if isinstance(s, Ring):
                self.rect(sk, s.outer)
                self.rect(sk, s.inner)
                ring = True
            elif isinstance(s, Circle):
                self.circle(sk, s)
            else:
                self.rect(sk, s)
        profs = [sk.profiles.item(i) for i in range(sk.profiles.count)]
        if ring:
            profs = [min(profs, key=lambda p: p.areaProperties().area)]
        coll = self.core.ObjectCollection.create()
        for p in profs:
            coll.add(p)
        ext = self.root.features.extrudeFeatures
        inp = ext.createInput(coll, F.FeatureOperations.NewBodyFeatureOperation)
        inp.setDistanceExtent(False, self.vi(prism.h))
        if abs(ev(prism.z, self.env)) > 1e-9:
            inp.startExtent = F.OffsetStartDefinition.create(self.vi(prism.z))
        feat = ext.add(inp)
        feat.name = name
        self.bodies[name] = [feat.bodies.item(i) for i in range(feat.bodies.count)]
        for i, bd in enumerate(self.bodies[name]):
            bd.name = name if i == 0 else f"{name}_{i + 1}"
        if prism.lift:
            self.move(name, prism.lift)

    def _move(self, bodies, define):
        coll = self.core.ObjectCollection.create()
        for bd in bodies:
            coll.add(bd)
        mf = self.root.features.moveFeatures
        mi = mf.createInput2(coll)
        define(mi)
        return mf.add(mi)

    def move(self, name, lift):
        bodies = self.bodies[name]
        rot = self._move(bodies, lambda mi: mi.defineAsRotate(self.root.xConstructionAxis,
                                                               self.vi(self.sign + "neigung")))
        if not self.sign_checked:
            # Erwartung bei richtiger Drehrichtung (hinten steigt an): kein Punkt tiefer als −1,5·tiefe·sin
            low = min(bd.boundingBox.minPoint.z for bd in self.bodies[name]) * 10.0
            t, n = self.env["tiefe"], math.radians(self.env["neigung"])
            if low < -1.5 * t * math.sin(n):
                rot.deleteMe()
                self.sign = "-"
                self._move(bodies, lambda mi: mi.defineAsRotate(self.root.xConstructionAxis,
                                                                 self.vi("-neigung")))
            self.sign_checked = True
        self._move(bodies, lambda mi: mi.defineAsTranslateXYZ(self.vi("0 mm"), self.vi("0 mm"),
                                                               self.vi(lift), True))

    def _combine(self, target, tools, op):
        coll = self.core.ObjectCollection.create()
        for t in tools:
            for bd in self.bodies.pop(t):
                coll.add(bd)
        cf = self.root.features.combineFeatures
        ci = cf.createInput(self.bodies[target][0], coll)
        ci.operation = op
        ci.isKeepToolBodies = False
        cf.add(ci)

    def cut(self, target, tools):
        self._combine(target, tools, self.fusion.FeatureOperations.CutFeatureOperation)

    def join(self, target, tools):
        self._combine(target, tools, self.fusion.FeatureOperations.JoinFeatureOperation)

    def split(self, target, z, lower, upper):
        planes = self.root.constructionPlanes
        pin = planes.createInput()
        pin.setByOffset(self.root.xYConstructionPlane, self.vi(z))
        plane = planes.add(pin)
        plane.name = "Trennebene"
        sf = self.root.features.splitBodyFeatures
        feat = sf.add(sf.createInput(self.bodies.pop(target)[0], plane, True))
        parts = sorted((feat.bodies.item(i) for i in range(feat.bodies.count)),
                       key=lambda bd: bd.boundingBox.minPoint.z)
        parts[0].name, parts[-1].name = lower, upper
        self.bodies[lower], self.bodies[upper] = [parts[0]], [parts[-1]]
        plane.isLightBulbOn = False


def run(context):
    import traceback

    import adsk.core
    import adsk.fusion
    app = adsk.core.Application.get()
    ui = app.userInterface
    try:
        app.documents.add(adsk.core.DocumentTypes.FusionDesignDocumentType)
        design = adsk.fusion.Design.cast(app.activeProduct)
        design.designType = adsk.fusion.DesignTypes.ParametricDesignType
        design.fusionUnitsManager.distanceDisplayUnits = adsk.fusion.DistanceUnits.MillimeterDistanceUnits
        b = FusionBuilder(design)
        build(b)
        lines = []
        for name, ref in REF_VOLUME.items():
            vol = b.bodies[name][0].volume * 1000.0  # cm³ → mm³
            dev = abs(vol - ref) / ref * 100
            lines.append(f"{name}: {vol:.0f} mm³ (Soll {ref:.0f}, Abweichung {dev:.2f} %)"
                         + ("" if dev <= 0.5 else "  ← PRÜFEN"))
        ui.messageBox("Gehäuse G1 aufgebaut.\n\n" + "\n".join(lines)
                      + "\n\nMaße ändern: Ändern → Parameter ändern.", "PDH-Terminal Gehäuse")
    except Exception:
        ui.messageBox("Fehler beim Aufbau:\n" + traceback.format_exc(), "PDH-Terminal Gehäuse")
