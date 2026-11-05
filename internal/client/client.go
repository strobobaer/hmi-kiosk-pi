// Package client bereitet VNC-Client-Profile vor und steuert deren Kindprozesse.
package client

import (
	"fmt"
	"strings"
	"time"
)

type Spec struct {
	Host       string
	Port       int
	Password   string
	ViewOnly   bool
	Quality    int
	ColorDepth int
	Scale      bool
	X, Y, W, H int
	SessionN   int
}

// Command.Env enthält nur Ergänzungen; der Runner hängt sie an die Umgebung des Dienstes an.
type Command struct {
	Path string
	Args []string
	Env  []string
}

type Profile interface {
	Name() string
	Prepare(dir string, s Spec) (Command, error)
}

type Process interface {
	Done() <-chan error
	Stop(grace time.Duration) error
}

type Runner interface {
	Start(Command) (Process, error)
}

func ByName(name string) (Profile, error) {
	switch name {
	case "remmina":
		return &remmina{}, nil
	case "tigervnc":
		return tigervnc{}, nil
	}
	return nil, fmt.Errorf("unbekanntes Client-Profil %q", name)
}

func noControl(what, v string) error {
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s enthält ein Steuerzeichen", what)
		}
	}
	return nil
}

func validateSpec(s Spec) error {
	if s.Host == "" {
		return fmt.Errorf("Host fehlt")
	}
	if strings.HasPrefix(s.Host, "-") {
		return fmt.Errorf("Host darf nicht mit „-“ beginnen")
	}
	if err := noControl("Host", s.Host); err != nil {
		return err
	}
	if err := noControl("Passwort", s.Password); err != nil {
		return err
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("Port %d ungültig", s.Port)
	}
	return nil
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
