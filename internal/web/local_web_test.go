package web

import (
	"encoding/json"
	"testing"

	"hmikiosk/internal/session"
)

func TestTilesShowWebTile(t *testing.T) {
	srv := serve(t, newFakeSession(), []TileView{
		{ID: "a", Name: "Presse", Icon: "press", Color: "blue", Reachable: true},
		{ID: "w", Name: "Grafana", Icon: "monitor", Color: "green", Reachable: true, Web: true},
	}, "auto")
	body, _ := get(t, srv.URL+"/")
	wantContains(t, body, `data-id="w"`, "Grafana", "tile-web", `<span class="tile-sub">Webseite</span>`)
}

func TestBarShowsWebBadge(t *testing.T) {
	s := newFakeSession()
	s.snap = session.Snapshot{State: session.StateConnected, HMI: "w", HMIName: "Grafana", Web: true}
	srv := serve(t, s, nil, "auto")
	body, _ := get(t, srv.URL+"/bar")
	wantContains(t, body, "Grafana", `class="badge web"`, ">Webseite<", "data-osk-toggle", `id="bar-back"`)
	wantNotContains(t, body, ">Bedienen<")
}

func TestWireSnapshotCarriesWeb(t *testing.T) {
	b, _ := json.Marshal(toWire(session.Snapshot{State: session.StateStarting, HMIName: "Grafana", Web: true}))
	var w map[string]any
	if err := json.Unmarshal(b, &w); err != nil || w["web"] != true {
		t.Fatalf("%s", b)
	}
}
