package coord

import (
	"testing"
	"time"

	"hmikiosk/internal/config"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time          { return f.t }
func (f *fakeClock) Advance(d time.Duration) { f.t = f.t.Add(d) }

const (
	pri = config.RolePrimary
	nor = config.RoleNormal
)

func TestDecideTable(t *testing.T) {
	to, lock := config.ModeTakeover, config.ModeLock
	conf, imm := config.StyleConfirm, config.StyleImmediate
	w := 5 * time.Second
	cases := []struct {
		name       string
		req, hold  config.Role
		perm, wch  bool
		mode       config.Mode
		style      config.TakeoverStyle
		allowWatch bool
		want       Outcome
		warn       time.Duration
	}{
		{"n/n takeover confirm", nor, nor, false, false, to, conf, false, AskConfirm, w},
		{"n/n takeover immediate", nor, nor, false, false, to, imm, false, TakeOver, 0},
		{"n/n lock", nor, nor, false, false, lock, conf, false, Reject, 0},
		{"p/n takeover", pri, nor, false, false, to, conf, false, TakeOver, w},
		{"p/n lock", pri, nor, false, false, lock, conf, false, TakeOver, w},
		{"p/n immediate", pri, nor, false, false, to, imm, false, TakeOver, 0},
		{"n/p takeover", nor, pri, false, false, to, conf, false, Reject, 0},
		{"n/p lock", nor, pri, false, false, lock, conf, false, Reject, 0},
		{"n/p immediate", nor, pri, false, false, to, imm, false, Reject, 0},
		{"n/p allowWatch not permanent", nor, pri, false, false, to, conf, true, Reject, 0},
		{"p/p takeover", pri, pri, false, false, to, conf, false, TakeOver, w},
		{"p/p lock", pri, pri, false, false, lock, conf, false, Reject, 0},
		{"n/permanent no watch", nor, nor, true, false, to, conf, false, Reject, 0},
		{"n/permanent watch", nor, nor, true, false, to, conf, true, AllowWatch, 0},
		{"n/primary permanent watch", nor, pri, true, false, lock, conf, true, AllowWatch, 0},
		{"n/primary permanent no watch", nor, pri, true, false, to, conf, false, Reject, 0},
		{"p/normal permanent", pri, nor, true, false, to, conf, false, TakeOver, w},
		{"p/primary permanent takeover", pri, pri, true, false, to, conf, false, TakeOver, w},
		{"p/primary permanent lock", pri, pri, true, false, lock, conf, false, Reject, 0},
		{"holder watching", nor, pri, true, true, lock, conf, false, Allow, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &Holder{Terminal: "h", Role: c.hold, Permanent: c.perm, Watch: c.wch}
			d := Decide(c.req, h, Rules{Mode: c.mode, Style: c.style, AllowWatch: c.allowWatch})
			if d.Outcome != c.want || d.Warn != c.warn {
				t.Fatalf("got %v/%v, want %v/%v", d.Outcome, d.Warn, c.want, c.warn)
			}
			if d.Holder != h {
				t.Fatalf("Holder not set")
			}
		})
	}
	d := Decide(nor, nil, Rules{Mode: lock})
	if d.Outcome != Allow || d.Holder != nil || d.Warn != 0 {
		t.Fatalf("free HMI: %+v", d)
	}
}

func TestResolveHigherRoleWinsThenSmallerID(t *testing.T) {
	cases := []struct{ a, b, want Claim }{
		{Claim{Terminal: "b", Role: nor, Nonce: "1"}, Claim{Terminal: "z", Role: pri, Nonce: "2"}, Claim{Terminal: "z", Role: pri, Nonce: "2"}},
		{Claim{Terminal: "b", Role: pri, Nonce: "1"}, Claim{Terminal: "a", Role: pri, Nonce: "2"}, Claim{Terminal: "a", Role: pri, Nonce: "2"}},
		{Claim{Terminal: "b", Role: nor, Nonce: "1"}, Claim{Terminal: "a", Role: nor, Nonce: "2"}, Claim{Terminal: "a", Role: nor, Nonce: "2"}},
	}
	for _, c := range cases {
		if got := ResolveSimultaneous(c.a, c.b); got != c.want {
			t.Errorf("(%v,%v) = %v, want %v", c.a, c.b, got, c.want)
		}
		if got := ResolveSimultaneous(c.b, c.a); got != c.want {
			t.Errorf("(%v,%v) = %v, want %v", c.b, c.a, got, c.want)
		}
	}
}
