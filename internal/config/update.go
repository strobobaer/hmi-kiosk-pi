package config

import (
	"encoding/json"
	"regexp"
	"strings"
)

// DefaultUpdateRepo ist die Quelle der Update-Prüfung, wenn die Konfiguration keine nennt.
const DefaultUpdateRepo = "strobobaer/hmi-kiosk-pi"

// UpdateCfg ist die Quelle der Update-Prüfung (GitHub-Releases). Leeres Repo heißt: keine Prüfung.
type UpdateCfg struct {
	Repo       string `json:"repo"`
	Prerelease bool   `json:"prerelease"`
}

// Regeln von GitHub: Besitzer aus Buchstaben, Ziffern und einzelnen Bindestrichen
// innen, höchstens 39 Zeichen; Repository aus Buchstaben, Ziffern, '.', '-', '_'.
var (
	repoOwnerRe = regexp.MustCompile(`^[A-Za-z0-9](?:-?[A-Za-z0-9]){0,38}$`)
	repoNameRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// ValidUpdateRepo prüft owner/repo. Leer ist erlaubt (Prüfung aus). Der Wert steht
// im Pfad der API-Adresse, deshalb genau ein '/' und keine Punkte-Pfade.
func ValidUpdateRepo(s string) bool {
	if s == "" {
		return true
	}
	owner, name, ok := strings.Cut(s, "/")
	if !ok || !repoOwnerRe.MatchString(owner) || !repoNameRe.MatchString(name) {
		return false
	}
	return name != "." && name != ".."
}

// UnmarshalJSON setzt die Standardwerte der Update-Prüfung vorab (Quelle, Vorabversionen an):
// Eine ältere Datei ohne die Felder bekommt sie, ein bewusst geleertes oder abgewähltes Feld bleibt so. Der Wert wird ganz ersetzt.
func (c *Config) UnmarshalJSON(b []byte) error {
	type plain Config
	var p plain
	p.Update = UpdateCfg{Repo: DefaultUpdateRepo, Prerelease: true}
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*c = Config(p)
	return nil
}
