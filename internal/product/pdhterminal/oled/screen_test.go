package oled

import (
	"flag"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "testdata/screen.txt neu schreiben")

func rows(c *Canvas) []string { return strings.Split(strings.TrimRight(c.ASCII(), "\n"), "\n") }

func TestRenderGolden(t *testing.T) {
	got := Render(Metrics{CPU: 17, RAM: 42, TempC: 52, TempOK: true, IP: "192.168.1.20"}).ASCII()
	const path = "testdata/screen.txt"
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("Bild weicht von %s ab:\n%s", path, got)
	}
}

func TestRenderLayout(t *testing.T) {
	r := rows(Render(Metrics{CPU: 17, RAM: 42, TempC: 52, TempOK: true, IP: "192.168.1.20"}))
	if len(r) != Height {
		t.Fatalf("Zeilen = %d, want %d", len(r), Height)
	}
	if r[52] != strings.Repeat("#", Width) {
		t.Errorf("Zeile 52 nicht voll: %s", r[52])
	}
	for _, y := range []int{41, 48} {
		if r[y][57:128] != strings.Repeat("#", 71) {
			t.Errorf("Rahmen in Zeile %d: %s", y, r[y])
		}
	}
	// Füllung: filled = round(68*42/100) = 29 -> x 59..87
	if r[44][59:88] != strings.Repeat("#", 29) || r[44][88] != '.' || r[44][58] != '.' {
		t.Errorf("Füllung falsch: %s", r[44])
	}
}

func TestRenderNoTempAndOffline(t *testing.T) {
	r := rows(Render(Metrics{CPU: 5, RAM: 10, IP: "Offline"}))
	// „--“: Querstrich in Zeile 3 der Zelle (y=3), je 5 Pixel ab x=85 und x=91
	if r[3][85:90] != "#####" || r[3][91:96] != "#####" {
		t.Errorf("-- nicht ab x=85 gesetzt: %s", r[3])
	}
	found := false
	for y := 53; y < Height; y++ {
		if strings.Contains(r[y], "#") {
			found = true
		}
	}
	if !found {
		t.Error("Offline in Zeile 53ff. fehlt")
	}
	// Vergleich mit direkt gezeichnetem Text
	c := NewCanvas()
	c.Text(0, 53, "Offline", 1)
	want := rows(c)
	for y := 53; y < Height; y++ {
		if r[y][:42] != want[y][:42] {
			t.Errorf("Zeile %d: %s want %s", y, r[y][:42], want[y][:42])
		}
	}
}

func TestRenderRAMZeroNoFill(t *testing.T) {
	r := rows(Render(Metrics{CPU: 1, RAM: 0, IP: "x"}))
	for y := 43; y <= 46; y++ {
		if strings.Contains(r[y][59:127], "#") {
			t.Errorf("Pixel in Zeile %d: %s", y, r[y])
		}
	}
}

func TestPageBitOrder(t *testing.T) {
	c := NewCanvas()
	c.Set(3, 9)
	p := c.Page(1)
	if len(p) != 128 || p[3] != 0x02 {
		t.Errorf("Page(1)[3] = %#x", p[3])
	}
	c.Set(-1, 0)
	c.Set(0, 64)
	c.Set(128, 0)
}

func TestRectInclusive(t *testing.T) {
	c := NewCanvas()
	c.Rect(2, 2, 4, 4)
	r := rows(c)
	if r[2][2:5] != "###" || r[3][2:5] != "#.#" || r[4][2:5] != "###" {
		t.Errorf("Rect: %v", r[2:5])
	}
}
