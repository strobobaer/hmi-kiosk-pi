// Package config holds the kiosk configuration model, its validation and
// crash-safe persistence.
package config

import (
	"crypto/rand"
	"encoding/hex"
)

type Role string

const (
	RolePrimary Role = "primary"
	RoleNormal  Role = "normal"
)

type Mode string

const (
	ModeTakeover Mode = "takeover"
	ModeLock     Mode = "lock"
)

type TakeoverStyle string

const (
	StyleConfirm   TakeoverStyle = "confirm"
	StyleImmediate TakeoverStyle = "immediate"
)

type HMI struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Host        string        `json:"host"`
	Port        int           `json:"port"`
	PasswordEnc string        `json:"passwordEnc"`
	Icon        string        `json:"icon"`
	Color       string        `json:"color"`
	Order       int           `json:"order"`
	ViewOnly    bool          `json:"viewOnly"`
	Mode        *Mode         `json:"mode,omitempty"`
	Takeover    TakeoverStyle `json:"takeover"`
	AllowWatch  bool          `json:"allowWatch"`
	// PasswordLost: Das Passwort ging bei einer Schlüsselreparatur verloren und muss neu eingegeben werden.
	PasswordLost bool `json:"passwordLost,omitempty"`
	// AuthFailed: Der VNC-Server hat das Passwort abgelehnt. Bis zur Änderung des Passworts oder einer erfolgreichen Prüfung gibt es keine weiteren Anmeldeversuche.
	AuthFailed bool `json:"authFailed,omitempty"`
	// Reach wählt die Erreichbarkeitsprüfung für die Kachel: ReachPing (Standard, auch leer), ReachPort oder ReachOff.
	Reach string `json:"reach,omitempty"`
	// Kind ist KindVNC (Standard, auch leer) oder KindWeb. Eine Web-Kachel zeigt URL in
	// einem eigenen Chromium-Fenster; Host, Port, Passwort und Abstimmung gelten dann nicht.
	Kind string `json:"kind,omitempty"`
	URL  string `json:"url,omitempty"`
}

type Peer struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type ClientCfg struct {
	Profile    string `json:"profile"`
	Quality    int    `json:"quality"`
	ColorDepth int    `json:"colorDepth"`
	Scale      bool   `json:"scale"`
}

type KioskCfg struct {
	InactivityMinutes int    `json:"inactivityMinutes"`
	Layout            string `json:"layout"`
	PermanentHMI      string `json:"permanentHmi"`
	HideBar           bool   `json:"hideBar"`
	// InputMode: InputTouch blendet den Mauszeiger aus, InputMouse zeigt ihn (leer = Touch).
	InputMode string `json:"inputMode"`
	// NetworkTiles gibt die Kachelseite für Tests im Netz frei (Port 8080 auf allen
	// Adressen). Sie gehört zum Gerät: Der Import übernimmt sie nicht.
	NetworkTiles bool `json:"networkTiles,omitempty"`
}

const (
	InputTouch = "touch"
	InputMouse = "mouse"
)

type NetworkCfg struct {
	NTPServer    string `json:"ntpServer"`
	WifiOff      bool   `json:"wifiOff"`
	BluetoothOff bool   `json:"bluetoothOff"`
	// Ethernet wirkt über den Helfer (Rückfallfrist) und wird erst nach der Bestätigung gespeichert.
	Ethernet EthernetCfg `json:"ethernet"`
}

type DisplayCfg struct {
	OverrideW       int    `json:"overrideW"`
	OverrideH       int    `json:"overrideH"`
	GraphicsProfile string `json:"graphicsProfile"`
}

type AdminCfg struct {
	PasswordHash string `json:"passwordHash"`
	// RemoteKeyHash ist SHA-256 (hex klein) des Schlüssels für die Fernbefehle von PDH; leer: aus.
	// Er gehört zu diesem Terminal: Export und Import übernehmen ihn nicht (wie das Admin-Passwort).
	RemoteKeyHash string `json:"remoteKeyHash,omitempty"`
}

type Config struct {
	SchemaVersion int `json:"schemaVersion"`
	Terminal      struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Role Role   `json:"role"`
		// HardwareID ist die Kennung des Geräts, auf dem die Konfiguration eingerichtet
		// wurde. Sie bleibt lokal: Der Import übernimmt sie nicht.
		HardwareID string `json:"hardwareId"`
		// MaintenanceManual hält den Wartungsmodus an, den der Admin von Hand gestartet hat
		// (Sicherung ohne Kennung, Übertragung auf ein anderes Terminal). Auch lokal.
		MaintenanceManual bool `json:"maintenanceManual,omitempty"`
	} `json:"terminal"`
	Peers        []Peer     `json:"peers"`
	SharedKeyEnc string     `json:"sharedKeyEnc"`
	DefaultMode  Mode       `json:"defaultMode"`
	HMIs         []HMI      `json:"hmis"`
	Client       ClientCfg  `json:"client"`
	Kiosk        KioskCfg   `json:"kiosk"`
	Network      NetworkCfg `json:"network"`
	Display      DisplayCfg `json:"display"`
	Admin        AdminCfg   `json:"admin"`
	Update       UpdateCfg  `json:"update"`
	// PDH fehlt in der Datei, solange nichts eingetragen ist (omitzero, Go 1.24): alte
	// Konfigurationen bleiben Byte für Byte gleich.
	PDH PDHCfg `json:"pdh,omitzero"`
}

const SchemaVersion = 1

func Default() Config {
	var c Config
	c.SchemaVersion = SchemaVersion
	c.Terminal.ID = newID()
	c.Terminal.Name = "Terminal"
	c.Terminal.Role = RoleNormal
	c.DefaultMode = ModeTakeover
	c.Client = ClientCfg{Profile: "remmina", Quality: 2, ColorDepth: 32, Scale: true}
	c.Kiosk.InactivityMinutes = 15
	c.Kiosk.InputMode = InputTouch
	c.Display.GraphicsProfile = "labwc"
	c.Update = UpdateCfg{Repo: DefaultUpdateRepo, Prerelease: true}
	return c
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("config: no randomness: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// HMIIcons und HMIColors sind die geschlossenen Mengen für Symbol und Farbe einer
// Kachel; leer heißt Standard. Die Kachelseite hat zu jedem Namen ein festes Bild.
var (
	HMIIcons  = []string{"monitor", "press", "tank", "conveyor", "gear", "bolt"}
	HMIColors = []string{"blue", "green", "amber", "red", "gray", "violet"}
)

const (
	ReachPing = "ping"
	ReachPort = "port"
	ReachOff  = "off"
)

// ReachMode liefert die Prüfart; leer oder unbekannt (alte Konfiguration) heißt Ping.
func (h HMI) ReachMode() string {
	switch h.Reach {
	case ReachPort, ReachOff:
		return h.Reach
	}
	return ReachPing
}
