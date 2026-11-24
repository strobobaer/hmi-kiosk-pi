package pdhterminal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

var errNet = &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}

func TestIsNetworkErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"refused", errNet, true},
		{"dns", &net.DNSError{IsNotFound: true}, true},
		{"deadline", context.DeadlineExceeded, true},
		{"x509", x509.UnknownAuthorityError{}, false},
		{"tls", &tls.CertificateVerificationError{}, false},
		{"other", errors.New("x"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := IsNetworkErr(c.err); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRouteSwitchOnNetworkErr(t *testing.T) {
	r := NewRoute("https://a/", "https://b")
	if !r.Fail("https://a", errNet) {
		t.Fatal("kein Wechsel")
	}
	if r.Base() != "https://b" || r.Name() != "tailnet" {
		t.Fatalf("%s %s", r.Base(), r.Name())
	}
	if st := r.Status(); st.Name != "tailnet" || st.LastErr == "" || st.Since.IsZero() {
		t.Fatalf("Status %+v", st)
	}
}

func TestRouteNoSwitchOnTLS(t *testing.T) {
	r := NewRoute("https://a", "https://b")
	if r.Fail("https://a", x509.UnknownAuthorityError{}) || r.Base() != "https://a" {
		t.Fatal("Wechsel bei TLS")
	}
}

func TestRouteNoFallbackConfigured(t *testing.T) {
	r := NewRoute("https://a", "")
	if r.Fail("https://a", errNet) || r.Base() != "https://a" {
		t.Fatal("Wechsel ohne Ausweichadresse")
	}
}

func TestRouteFailOnFallbackNoFlip(t *testing.T) {
	r := NewRoute("https://a", "https://b")
	r.Fail("https://a", errNet)
	if r.Fail("https://b", errNet) || r.Base() != "https://b" {
		t.Fatal("Schleife")
	}
}

func TestRouteRunReturns(t *testing.T) {
	old := routeProbeEvery
	routeProbeEvery = 10 * time.Millisecond
	defer func() { routeProbeEvery = old }()
	r := NewRoute("https://a", "https://b")
	var calls atomic.Int32
	probe := func(ctx context.Context, base string) error {
		if base != "https://a" {
			t.Errorf("probe base %s", base)
		}
		if calls.Add(1) == 1 {
			return errNet
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx, probe); close(done) }()
	time.Sleep(60 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("probe bei aktiver Hauptadresse")
	}
	r.Fail("https://a", errNet)
	deadline := time.Now().Add(2 * time.Second)
	for r.Base() != "https://a" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r.Base() != "https://a" || calls.Load() < 2 {
		t.Fatalf("nicht zurück: %s %d", r.Base(), calls.Load())
	}
	cancel()
	<-done
}

func TestRouteSetURLs(t *testing.T) {
	r := NewRoute("https://a", "https://b")
	r.Fail("https://a", errNet)
	r.SetURLs("https://a", "")
	if r.Base() != "https://a" {
		t.Fatal(r.Base())
	}
	r.SetURLs("https://a", "https://b")
	r.Fail("https://a", errNet)
	r.SetURLs("https://a2", "https://b")
	if r.Base() != "https://a2" {
		t.Fatal(r.Base())
	}
}
