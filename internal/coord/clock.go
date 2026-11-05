package coord

import "time"

// Clock liefert die Zeit. Differenzen laufen über time.Time.Sub, damit die
// monotone Uhr von Go greift und Wanduhr-Sprünge keine Belegung verfälschen.
type Clock interface {
	Now() time.Time
}

// SystemClock ist die echte Uhr.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }
