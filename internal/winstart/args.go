package winstart

import "strings"

// Paths sind die Ordner des Starters.
type Paths struct {
	Base string // Grundversion (Program Files), enthält pdh-terminal.exe
	Data string // Datenordner (%ProgramData%\PDH-Terminal), Updates unter app\<version>
	Run  string // Laufzeitordner mit der Gesundmarke
}

// ParseArgs liest -base, -data und -run aus den Argumenten des Starters. Das Programm bekommt dieselben
// Argumente außer -base (das kennt nur der Starter); -data und -run bleiben also für beide gleich. Fehlende Werte
// ersetzen die Vorgaben def.
func ParseArgs(args []string, def Paths) (Paths, []string) {
	p := def
	var pass []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		isFlag := strings.HasPrefix(a, "-") && (name == "base" || name == "data" || name == "run")
		if !isFlag {
			pass = append(pass, a)
			continue
		}
		if !hasVal && i+1 < len(args) {
			i++
			val, hasVal = args[i], true
			if name != "base" {
				pass = append(pass, a, val)
			}
		} else if name != "base" {
			pass = append(pass, a)
		}
		if !hasVal {
			continue
		}
		switch name {
		case "base":
			p.Base = val
		case "data":
			p.Data = val
		case "run":
			p.Run = val
		}
	}
	return p, pass
}
