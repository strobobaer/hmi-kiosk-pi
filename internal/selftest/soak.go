package selftest

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

const (
	// leakMinSamples und leakBytesPerHour: ab 20 Proben und mehr als 1 MB Zuwachs je Stunde.
	leakMinSamples   = 20
	leakBytesPerHour = 1 << 20
)

// SoakSample ist die Probe nach einem Durchgang (Client starten, verbinden, warten, stoppen).
// RSSBytes ist die Summe aus Dienst und Client. Failures zählt alle Fehler seit dem Anfang.
type SoakSample struct {
	Cycle             int       `json:"cycle"`
	Time              time.Time `json:"time"`
	RSSBytes          int64     `json:"rssBytes"`
	ServiceRSS        int64     `json:"serviceRss"`
	ClientRSS         int64     `json:"clientRss"`
	Failures          int       `json:"failures"`
	Error             string    `json:"error,omitempty"`
	Leak              bool      `json:"leak,omitempty"`
	SlopeBytesPerHour float64   `json:"slopeBytesPerHour,omitempty"`
}

type point struct {
	at  time.Time
	rss float64
}

// leakSlope legt eine Gerade durch die Proben (kleinste Quadrate) und liefert den
// Zuwachs je Stunde. Gemeldet wird erst ab leakMinSamples Proben.
func leakSlope(ps []point) (perHour float64, leak bool) {
	n := float64(len(ps))
	if len(ps) < leakMinSamples {
		return 0, false
	}
	var sx, sy, sxx, sxy float64
	for _, p := range ps {
		x := p.at.Sub(ps[0].at).Hours()
		sx += x
		sy += p.rss
		sxx += x * x
		sxy += x * p.rss
	}
	den := n*sxx - sx*sx
	if den <= 1e-12 {
		return 0, false
	}
	perHour = (n*sxy - sx*sy) / den
	return perHour, perHour > leakBytesPerHour
}

// Soak startet und beendet gegen den Test-VNC-Server wiederholt einen Client, bis dur
// um ist (mindestens ein Durchgang), und meldet nach jedem Durchgang eine Probe.
// Ein Leck wird gemeldet, bricht aber nichts ab. Das Ende per ctx liefert dessen Fehler.
func Soak(ctx context.Context, d Deps, dur time.Duration, report func(SoakSample)) error {
	if msg := Refuse(d); msg != "" {
		return errors.New(msg)
	}
	d.Timing = d.Timing.withDefaults()
	r, err := newRig(d)
	if err != nil {
		return err
	}
	defer r.close()
	groupRSS := d.GroupRSS
	if groupRSS == nil {
		groupRSS = func(pid int) (int64, error) { return GroupRSS("/proc", pid) }
	}

	var svc, cli []point
	failures := 0
	leakLogged := false
	begin := d.Timing.Now()
	for cycle := 1; ; cycle++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if cycle > 1 && d.Timing.Now().Sub(begin) >= dur {
			return nil
		}
		s := SoakSample{Cycle: cycle}
		clientRSS, cerr := r.soakCycle(ctx, groupRSS)
		if err := ctx.Err(); err != nil {
			return err
		}
		if cerr != nil {
			failures++
			s.Error = cerr.Error()
		}
		s.Failures = failures
		s.Time = d.Timing.Now()
		s.ClientRSS = clientRSS
		if d.ServicePID != nil {
			if pid := d.ServicePID(); pid > 0 {
				rss, err := groupRSS(pid)
				if err != nil {
					failures++
					s.Failures = failures
					s.Error = joinErr(s.Error, fmt.Sprintf("Speicher des Dienstes nicht lesbar: %v", err))
				}
				s.ServiceRSS = rss
			}
		}
		s.RSSBytes = s.ServiceRSS + s.ClientRSS
		if s.ServiceRSS > 0 {
			svc = append(svc, point{s.Time, float64(s.ServiceRSS)})
		}
		if s.ClientRSS > 0 {
			cli = append(cli, point{s.Time, float64(s.ClientRSS)})
		}
		for _, series := range [][]point{svc, cli} {
			if slope, leak := leakSlope(series); leak && slope > s.SlopeBytesPerHour {
				s.Leak, s.SlopeBytesPerHour = true, slope
			}
		}
		if s.Leak && !leakLogged {
			leakLogged = true
			log.Printf("Dauerprüfung: Speicher wächst um %.1f MB je Stunde (Durchgang %d)", s.SlopeBytesPerHour/(1<<20), cycle)
		}
		report(s)
	}
}

func joinErr(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

// soakCycle ist ein Durchgang. Der Speicher des Clients wird gemessen, solange er läuft.
func (r *rig) soakCycle(ctx context.Context, groupRSS func(int) (int64, error)) (clientRSS int64, err error) {
	tm := r.d.Timing
	p, err := r.start(r.password, false)
	if err != nil {
		return 0, err
	}
	defer func() {
		if !p.gone() {
			_ = p.Stop(tm.StopGrace)
		}
		if _, gone := r.waitExit(context.Background(), p, tm.StopWithin); !gone && err == nil {
			err = fmt.Errorf("Client nach dem Stopp nicht innerhalb von %s beendet", secs(tm.StopWithin))
		}
		if p.gone() {
			r.release(p)
		}
	}()
	if _, err := r.waitConnected(ctx, p); err != nil {
		return 0, err
	}
	if !sleepCtx(ctx, tm.SoakHold) {
		return 0, ctx.Err()
	}
	if p.gone() {
		return 0, fmt.Errorf("Client endete während der Wartezeit (%v)", p.err)
	}
	if pp, ok := p.Process.(pider); ok {
		if rss, rerr := groupRSS(pp.Pid()); rerr == nil {
			clientRSS = rss
		} else {
			err = fmt.Errorf("Speicher des Clients nicht lesbar: %v", rerr)
		}
	} else {
		err = errors.New("Client hat keine Prozesskennung, Speicher nicht messbar")
	}
	return clientRSS, err
}

// SoakResult fasst die Proben einer Dauerprüfung zusammen. Ein einmal erkanntes
// Leck bleibt erhalten, ebenso die größte gemessene Steigung.
type SoakResult struct {
	Cycles, Failures  int
	Leak              bool
	SlopeBytesPerHour float64
}

func (r *SoakResult) Add(s SoakSample) {
	r.Cycles, r.Failures = s.Cycle, s.Failures
	r.Leak = r.Leak || s.Leak
	r.SlopeBytesPerHour = max(r.SlopeBytesPerHour, s.SlopeBytesPerHour)
}
