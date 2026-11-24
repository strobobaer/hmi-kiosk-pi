package localport

import "testing"

func TestDefaultIs8080(t *testing.T) {
	t.Cleanup(func() { Set(0) })
	Set(0)
	if Get() != 8080 || Admin() != 8081 {
		t.Fatalf("Grundwert: %d/%d", Get(), Admin())
	}
	if Addr() != "127.0.0.1:8080" || AdminAddr() != "127.0.0.1:8081" {
		t.Errorf("Adressen: %s %s", Addr(), AdminAddr())
	}
	if StartURL() != "http://127.0.0.1:8080/" || AdminURL() != "http://127.0.0.1:8081/admin/" {
		t.Errorf("Adressen: %s %s", StartURL(), AdminURL())
	}
}

func TestSetChangesEverything(t *testing.T) {
	t.Cleanup(func() { Set(0) })
	Set(18080)
	if Get() != 18080 || Admin() != 18081 {
		t.Fatalf("Port: %d/%d", Get(), Admin())
	}
	if Addr() != "127.0.0.1:18080" || AdminAddr() != "127.0.0.1:18081" {
		t.Errorf("Adressen: %s %s", Addr(), AdminAddr())
	}
	if StartURL() != "http://127.0.0.1:18080/" || AdminURL() != "http://127.0.0.1:18081/admin/" {
		t.Errorf("URLs: %s %s", StartURL(), AdminURL())
	}
	if !StartHost("127.0.0.1:18080") || !StartHost("localhost:18080") || StartHost("127.0.0.1:8080") || StartHost("evil:18080") {
		t.Error("StartHost")
	}
	if !AdminHost("127.0.0.1:18081") || !AdminHost("localhost:18081") || AdminHost("127.0.0.1:8081") || AdminHost("127.0.0.1:18080") {
		t.Error("AdminHost")
	}
	if !StartOrigin("http://127.0.0.1:18080") || !StartOrigin("http://localhost:18080") || StartOrigin("https://127.0.0.1:18080") || StartOrigin("http://127.0.0.1:8080") {
		t.Error("StartOrigin")
	}
	if !AdminOrigin("http://127.0.0.1:18081") || !AdminOrigin("http://localhost:18081") || AdminOrigin("http://127.0.0.1:18080") {
		t.Error("AdminOrigin")
	}
}

func TestSetZeroResets(t *testing.T) {
	Set(19000)
	Set(0)
	if Get() != Default {
		t.Fatalf("Get = %d", Get())
	}
}
