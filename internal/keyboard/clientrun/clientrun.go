// Package clientrun bindet die Bildschirmtastatur an den Prozessstarter des Clients
// (client.Runner). Es ist die einzige Stelle, an der keyboard den Client berührt.
package clientrun

import (
	"hmikiosk/internal/client"
	"hmikiosk/internal/keyboard"
)

type runner struct{ r client.Runner }

func (a runner) Start(c keyboard.Command) (keyboard.Process, error) {
	p, err := a.r.Start(client.Command{Path: c.Path, Args: c.Args})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// New wie keyboard.New, startet wvkbd aber über r.
func New(r client.Runner, find func() (string, error), screenH func() int) *keyboard.Supervisor {
	return keyboard.New(runner{r}, find, screenH)
}

// NewExec startet wvkbd als gewöhnlichen Prozess.
func NewExec(find func() (string, error), screenH func() int) *keyboard.Supervisor {
	return New(client.NewExecRunner(), find, screenH)
}
