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
}

type NetworkCfg struct {
	NTPServer    string `json:"ntpServer"`
	WifiOff      bool   `json:"wifiOff"`
	BluetoothOff bool   `json:"bluetoothOff"`
}

type DisplayCfg struct {
	OverrideW       int    `json:"overrideW"`
	OverrideH       int    `json:"overrideH"`
	GraphicsProfile string `json:"graphicsProfile"`
}

type AdminCfg struct {
	PasswordHash string `json:"passwordHash"`
}

type Config struct {
	SchemaVersion int `json:"schemaVersion"`
	Terminal      struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Role Role   `json:"role"`
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
	c.Display.GraphicsProfile = "labwc"
	return c
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("config: no randomness: " + err.Error())
	}
	return hex.EncodeToString(b)
}
