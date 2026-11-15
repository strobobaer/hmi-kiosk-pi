// PDH-Terminal Pultgehäuse G1
// Raspberry Pi 5 + offizieller Active Cooler, PN532 (Elechouse V3), OLED SH1106 1,3" (I²C)
// Zweiteilig: Wanne (unten) und Pultdeckel (oben, 15° geneigt). Alle Maße in mm.
//
// Ausgabe wählen:  openscad -D 'part="base"' ...   ("base" | "lid" | "assembly")
// "lid" ist bereits zum Drucken gedreht: Sichtseite liegt auf dem Druckbett.

part = "assembly";
$fn = 48;

// ---------- Außenmaße ----------
W       = 104;   // Breite (links-rechts)
D       = 112;   // Tiefe (vorn-hinten)
Hf      = 32;    // Höhe vorn
tilt    = 15;    // Neigung der Oberseite
wall    = 2.2;   // Wandstärke
floor_t = 2.0;   // Bodenstärke
top_t   = 2.4;   // Deckelstärke
thin_t  = 1.2;   // Deckelstärke über der PN532-Antenne
z_split = 16;    // Trennebene Wanne/Deckel
Hb      = Hf + D * tan(tilt);   // Höhe hinten (≈ 62)

// ---------- Raspberry Pi 5 ----------
pi_l   = 85;  pi_w = 56;  pi_t = 1.4;
so_h   = 4;   so_d = 6;   so_hole = 2.5;      // Abstandshalter, M2,5 selbstschneidend
pi_x0  = wall + 2.0;                          // Kante mit USB-C/HDMI zur linken Wand
pi_y0  = D - wall - 2.5 - pi_l;               // USB/LAN zur Rückwand
pi_z   = floor_t + so_h;                      // Unterseite Platine
pi_holes = [[3.5, 3.5], [61.5, 3.5], [3.5, 52.5], [61.5, 52.5]];
// Anschlüsse an der langen Kante: [Lage entlang der Platine, Breite, Höhe] der Öffnung
side_ports = [[11.2, 12, 8], [25.8, 11, 8], [39.2, 11, 8]];   // USB-C, HDMI0, HDMI1
port_row_h = 17;                               // USB/LAN-Stapel hinten
sd_y   = 28;                                   // microSD-Mitte quer zur Platine

// ---------- Kartenzone und PN532 ----------
card_w = 85.6; card_d = 54; card_r = 3.2;
card_s0 = 8;                                   // Abstand Kartenumriss von der Vorderkante (auf der Schräge)
pn_w = 42.7; pn_d = 40.4; pn_clear = 1.6;
frame_h = 4;  frame_t = 1.6;
pn_gap  = 24;                                  // Kabelöffnung im Rahmen (hinten)

// ---------- OLED SH1106 1,3" ----------
ol_w = 35.4; ol_d = 33.5; ol_clear = 0.6; ol_frame_h = 3;
ol_s  = 87;                                    // Mitte des Moduls auf der Schräge
win_w = 31; win_d = 17; win_off = 0;           // Sichtfenster, win_off verschiebt zum Glas
ol_gap = 14;

// ---------- Verschraubung ----------
use_inserts = true;     // true: M3-Einschmelzmuttern, false: Blechschrauben 3,0 mm
post_d = 7;
insert_d = 4.2; insert_h = 6; pilot_d = 2.5;
hole_d = 3.4; head_d = 6.5; screw_len = 16;    // M3×16 von unten
posts = [[6.5, 6.5], [W - 6.5, 6.5], [W - 6.5, D - 6.5]];

lip_h = 2; lip_t = 1.2; lip_gap = 0.25;
foot_d = 10.5; foot_depth = 1;
feet = [[14, 12], [W - 14, 12], [W - 14, D - 14], [14, D - 14]];

// ---------- abgeleitet ----------
cx = W / 2;
pn_s = card_s0 + card_d / 2;
function ztop_in(y) = Hf + y * tan(tilt) - top_t / cos(tilt);
function pi_pos(h) = [pi_x0 + h[1], pi_y0 + h[0], 0];

// Koordinaten auf der Oberseite: x = quer, y = entlang der Schräge, z = Normale (innen < 0)
module on_top() translate([0, 0, Hf]) rotate([tilt, 0, 0]) children();

module rrect(w, d, r) offset(r = r) offset(delta = -r) square([w, d]);

module outer()
    rotate([90, 0, 90]) linear_extrude(W)
        polygon([[0, 0], [D, 0], [D, Hb], [0, Hf]]);

module inner()
    translate([wall, 0, 0]) rotate([90, 0, 90]) linear_extrude(W - 2 * wall)
        polygon([[wall, floor_t], [D - wall, floor_t],
                 [D - wall, ztop_in(D - wall)], [wall, ztop_in(wall)]]);

// Rahmen mit Kabelöffnung an der hinteren Seite
module frame(iw, id, t, h, gap)
    difference() {
        translate([-t, -t, 0]) cube([iw + 2 * t, id + 2 * t, h]);
        translate([0, 0, -1]) cube([iw, id, h + 2]);
        translate([(iw - gap) / 2, id - 1, -1]) cube([gap, t + 2, h + 2]);
    }

module pn_frame() {
    iw = pn_w + pn_clear; id = pn_d + pn_clear;
    on_top() translate([cx - iw / 2, pn_s - id / 2, -top_t - frame_h])
        frame(iw, id, frame_t, frame_h + 0.01, pn_gap);
}

module ol_frame() {
    iw = ol_w + ol_clear; id = ol_d + ol_clear;
    on_top() translate([cx - iw / 2, ol_s - id / 2, -top_t - ol_frame_h])
        frame(iw, id, frame_t, ol_frame_h + 0.01, ol_gap);
}

module back_port_cut()
    translate([pi_x0 + 0.5, D - wall - 1, pi_z + pi_t - 0.5])
        cube([pi_w - 1, wall + 2, port_row_h + 0.5]);

module cutouts() {
    // Anschlüsse links (USB-C, 2× Micro-HDMI)
    for (p = side_ports)
        translate([-1, pi_y0 + p[0] - p[1] / 2, pi_z + pi_t + 1.6 - p[2] / 2])
            cube([wall + 2, p[1], p[2]]);
    back_port_cut();
    // microSD von unten erreichbar
    translate([pi_x0 + sd_y - 9, pi_y0 - 5, -1]) cube([18, 18, floor_t + 2]);
    // Gewindelöcher Abstandshalter
    for (h = pi_holes) translate(pi_pos(h) + [0, 0, 1]) cylinder(d = so_hole, h = so_h + floor_t);
    // Verschraubung: Wanne Durchgang + Senkung von unten, Deckel Einschmelzmutter
    for (p = posts) {
        translate([p[0], p[1], -1]) cylinder(d = hole_d, h = z_split + 1.01);
        translate([p[0], p[1], -1]) cylinder(d = head_d, h = z_split + insert_h - screw_len + 1);
        translate([p[0], p[1], z_split - 0.01])
            cylinder(d = use_inserts ? insert_d : pilot_d, h = use_inserts ? insert_h : insert_h + 2);
    }
    // Auslass rechts in der Wanne
    for (y = [36 : 5 : 86]) translate([W - wall - 1, y, 5]) cube([wall + 2, 2.5, 8]);
    // Ansaugschlitze neben dem OLED
    for (x0 = [12, W - 30], i = [0 : 4])
        on_top() translate([x0 + 4 * i, 72, -top_t - 1]) cube([2, 28, top_t + 2]);
    // Kartenumriss, 0,4 mm eingeprägt
    on_top() translate([cx - card_w / 2, card_s0, -0.4]) linear_extrude(1)
        difference() {
            rrect(card_w, card_d, card_r);
            translate([1, 1]) rrect(card_w - 2, card_d - 2, card_r - 1);
        }
    // dünne Zone über der Antenne (von innen)
    on_top() translate([cx - (pn_w + pn_clear) / 2 - 0.05, pn_s - (pn_d + pn_clear) / 2 - 0.05, -top_t - 0.01])
        cube([pn_w + pn_clear + 0.1, pn_d + pn_clear + 0.1, top_t - thin_t + 0.01]);
    // OLED-Fenster
    on_top() translate([cx - win_w / 2, ol_s - win_d / 2 + win_off, -top_t - 1])
        cube([win_w, win_d, top_t + 2]);
    // Mulden für Gummifüße
    for (f = feet) translate([f[0], f[1], -0.01]) cylinder(d = foot_d, h = foot_depth);
}

module body()
    difference() {
        union() {
            difference() { outer(); inner(); }
            intersection() {
                outer();
                union() {
                    for (p = posts) translate([p[0], p[1], 0]) cylinder(d = post_d, h = Hb);
                    for (h = pi_holes) translate(pi_pos(h)) cylinder(d = so_d, h = pi_z);
                }
            }
            pn_frame();
            ol_frame();
        }
        cutouts();
    }

// Passrand: steht auf der inneren Wandhälfte der Wanne, der Deckel ist dort ausgespart
module lip_ring(o, i, z0, h)
    difference() {
        translate([o, o, z0]) cube([W - 2 * o, D - 2 * o, h]);
        translate([i, i, z0 - 1]) cube([W - 2 * i, D - 2 * i, h + 2]);
    }

module lip()
    difference() {
        lip_ring(wall / 2, wall + lip_t, z_split - 0.01, lip_h);
        back_port_cut();
    }

module base() {
    intersection() { body(); translate([-1, -1, -1]) cube([W + 2, D + 2, z_split + 1]); }
    lip();
}

module lid()
    difference() {
        intersection() { body(); translate([-1, -1, z_split]) cube([W + 2, D + 2, Hb]); }
        lip_ring(wall / 2 - lip_gap, wall + lip_t + 0.6, z_split - 1, lip_h + 1.3);
    }

// Deckel so drehen, dass die geneigte Sichtseite auf dem Druckbett liegt
module lid_print() rotate([180, 0, 0]) rotate([-tilt, 0, 0]) translate([0, 0, -Hf]) lid();

if (part == "base") base();
else if (part == "lid") lid_print();
else {
    color("SlateGray") base();
    color("Gainsboro", 0.85) lid();
}
