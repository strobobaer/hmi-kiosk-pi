package oled

import (
	"fmt"
	"math"
)

// Metrics sind die Messwerte für das Statusbild.
type Metrics struct {
	CPU, RAM float64 // Prozent
	TempC    float64
	TempOK   bool
	IP       string
}

// Render zeichnet das Statusbild wie das raspi-Oled-Skript.
func Render(m Metrics) *Canvas {
	m.CPU, m.RAM = clampPercent(m.CPU), clampPercent(m.RAM)
	c := NewCanvas()
	c.Text(0, 0, "CPU", 1)
	temp := "--"
	if m.TempOK {
		temp = fmt.Sprintf("%.0f°C", m.TempC)
	}
	c.Text(85, 0, temp, 1)
	c.Text(0, 10, fmt.Sprintf("%.0f%%", m.CPU), 3)
	c.Text(0, 39, fmt.Sprintf("RAM %.0f%%", m.RAM), 1)
	c.Rect(57, 41, 127, 48)
	if filled := int(math.Round(68 * m.RAM / 100)); filled > 0 {
		c.FillRect(59, 43, 58+filled, 46)
	}
	for x := 0; x < Width; x++ {
		c.Set(x, 52)
	}
	c.Text(0, 53, m.IP, 1)
	return c
}
