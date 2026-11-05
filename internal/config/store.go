package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	fileName     = "config.json"
	goodFileName = "config.json.good"
)

type LoadInfo struct {
	UsedFallback bool
	Reason       string
}

type Store struct{ dir string }

func Open(dir string) *Store { return &Store{dir: dir} }

func (s *Store) path(name string) string { return filepath.Join(s.dir, name) }

func readConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if err := Validate(c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return c, nil
}

// Load returns the active config. A missing file yields Default(); an
// unreadable or invalid one falls back to the last known-good copy.
func (s *Store) Load() (Config, LoadInfo, error) {
	c, err := readConfig(s.path(fileName))
	if err == nil {
		return c, LoadInfo{}, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), LoadInfo{}, nil
	}
	g, gerr := readConfig(s.path(goodFileName))
	if gerr != nil {
		return Config{}, LoadInfo{}, fmt.Errorf("Konfiguration defekt (%v) und kein gueltiger Rueckfall (%v)", err, gerr)
	}
	return g, LoadInfo{UsedFallback: true, Reason: err.Error()}, nil
}

func (s *Store) Save(c Config) error {
	if err := Validate(c); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return s.writeAtomic(fileName, data)
}

// MarkGood records the current config.json as the known-good fallback.
func (s *Store) MarkGood() error {
	data, err := os.ReadFile(s.path(fileName))
	if err != nil {
		return err
	}
	return s.writeAtomic(goodFileName, data)
}

func (s *Store) writeAtomic(name string, data []byte) (err error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, name+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), s.path(name)); err != nil {
		return err
	}
	d, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
