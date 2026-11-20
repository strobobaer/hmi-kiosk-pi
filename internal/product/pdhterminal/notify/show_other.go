//go:build !windows

package notify

type noop struct{}

func (noop) Show([]string, string, string) {}

// New: Außerhalb von Windows gibt es keine Benachrichtigung (Platzhalter ohne Wirkung).
func New(func() error) Shower { return noop{} }
