// hmi-kiosk-pack erzeugt Freigabeschlüssel und signierte Programmpakete.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"hmikiosk/internal/update"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Aufruf: hmi-kiosk-pack keygen -out <Ordner> | pack -key <Datei> -version <x.y.z> -in <Ordner> -out <Datei>")
		return 2
	}
	var err error
	switch args[0] {
	case "keygen":
		err = keygen(args[1:], stdout)
	case "pack":
		err = pack(args[1:], stdout)
	default:
		fmt.Fprintf(stderr, "Unbekannter Befehl %q\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "Fehler:", err)
		return 1
	}
	return 0
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func keygen(args []string, stdout io.Writer) error {
	fs := newFlags("keygen")
	out := fs.String("out", "", "Ordner für release.key und release.pub")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("-out fehlt")
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	keyPath, pubPath := filepath.Join(*out, "release.key"), filepath.Join(*out, "release.pub")
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	// O_EXCL: ein vorhandener Schlüssel wird nie überschrieben.
	kf, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("%s nicht anlegbar (existiert er schon?): %v", keyPath, err)
	}
	_, werr := kf.WriteString(hex.EncodeToString(priv) + "\n")
	if cerr := kf.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(keyPath)
		return werr
	}
	if err := os.WriteFile(pubPath, []byte(hex.EncodeToString(pub)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Geschrieben: %s (geheim halten, nie ins Repository) und %s\n", keyPath, pubPath)
	return nil
}

func pack(args []string, stdout io.Writer) error {
	fs := newFlags("pack")
	keyFile := fs.String("key", "", "Datei release.key")
	version := fs.String("version", "", "Version x.y.z")
	in := fs.String("in", "", "Ordner mit hmi-kiosk")
	out := fs.String("out", "", "Ausgabedatei")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyFile == "" || *version == "" || *in == "" || *out == "" {
		return fmt.Errorf("-key, -version, -in und -out sind erforderlich")
	}
	raw, err := os.ReadFile(*keyFile)
	if err != nil {
		return fmt.Errorf("Schlüssel nicht lesbar: %v", err)
	}
	priv, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("Schlüsseldatei ungültig")
	}
	blob, err := update.Pack(*in, *version, ed25519.PrivateKey(priv))
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, blob, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Paket %s (%d Bytes) geschrieben\n", *out, len(blob))
	return nil
}
