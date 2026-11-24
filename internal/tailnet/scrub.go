package tailnet

import (
	"context"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxOutputMsg ist die größte Länge der Programmausgabe, die in eine Fehlermeldung übernommen wird.
const MaxOutputMsg = 200

type secretsKey struct{}

// WithSecret merkt sich einen Wert (den Auth-Key), den Scrub aus jeder Programmausgabe entfernt, die in diesem
// Aufruf in eine Fehlermeldung gelangt.
func WithSecret(ctx context.Context, secret string) context.Context {
	if secret == "" {
		return ctx
	}
	prev, _ := ctx.Value(secretsKey{}).([]string)
	return context.WithValue(ctx, secretsKey{}, append(append([]string(nil), prev...), secret))
}

var keyTokenRe = regexp.MustCompile(`tskey-[A-Za-z0-9_-]+`)

// Scrub macht die Ausgabe eines Programms fehlertauglich, und zwar in dieser Reihenfolge: (a) jeden mit
// WithSecret gemerkten Wert ersetzen, (b) jedes Wort tskey-… durch "tskey-***" ersetzen (auch Teile eines
// Keys), (c) erst dann auf MaxOutputMsg Bytes an einer UTF-8-Grenze kürzen. Würde zuerst gekürzt, bliebe ein
// angeschnittener Key unerkannt.
func Scrub(ctx context.Context, msg string) string {
	msg = strings.TrimSpace(msg)
	secrets, _ := ctx.Value(secretsKey{}).([]string)
	for _, s := range secrets {
		msg = strings.ReplaceAll(msg, s, "***")
	}
	msg = keyTokenRe.ReplaceAllString(msg, "tskey-***")
	if len(msg) > MaxOutputMsg {
		n := MaxOutputMsg
		for n > 0 && !utf8.RuneStart(msg[n]) {
			n--
		}
		msg = msg[:n]
	}
	return msg
}
