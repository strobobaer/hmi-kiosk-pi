package pdhterminal

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// adminURLTTL: so lange gilt eine ermittelte Geräteadresse, bevor sie neu bestimmt wird.
const adminURLTTL = 5 * time.Minute

// adminURL ermittelt die Adresse der Admin-Seite für die Kopfzeile X-Terminal-Admin-URL. Die lokale
// IP-Adresse ist die, über die der PDH-Server erreicht wird (UDP-"Verbindung", es gehen keine Pakete raus).
type adminURL struct {
	mu      sync.Mutex
	port    string
	cache   map[string]adminURLEntry // Schlüssel: PDH-Host:Port
	now     func() time.Time
	localIP func(target string) string
}

type adminURLEntry struct {
	ip string // leer: nicht ermittelbar
	at time.Time
}

var adminURLState = &adminURL{now: time.Now, localIP: dialLocalIP}

// SetAdminPort merkt sich den tatsächlich gebundenen Port der Admin-Seite (leer oder ungültig: keine Kopfzeile).
func SetAdminPort(port string) { adminURLState.setPort(port) }

// dialLocalIP gibt die lokale Adresse zurück, mit der target erreicht würde; leer, wenn unbekannt.
func dialLocalIP(target string) string {
	c, err := net.Dial("udp", target)
	if err != nil {
		return ""
	}
	defer c.Close()
	a, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok || a.IP == nil || a.IP.IsUnspecified() {
		return ""
	}
	if a.Zone != "" { // link-local mit Zone taugt nicht für eine URL
		return ""
	}
	return a.IP.String()
}

func (a *adminURL) setPort(port string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		port = ""
	}
	a.port = port
}

// invalidate verwirft die gemerkten Adressen (z. B. bei Netzwechsel).
func (a *adminURL) invalidate() {
	a.mu.Lock()
	a.cache = nil
	a.mu.Unlock()
}

// value liefert https://<ip>:<port>/admin/ für Anfragen an target ("host:port"), sonst "".
func (a *adminURL) value(target string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.port == "" || target == "" {
		return ""
	}
	e, ok := a.cache[target]
	if !ok || a.now().Sub(e.at) >= adminURLTTL {
		e = adminURLEntry{ip: a.localIP(target), at: a.now()}
		if a.cache == nil {
			a.cache = map[string]adminURLEntry{}
		}
		a.cache[target] = e
	}
	if e.ip == "" {
		return ""
	}
	return "https://" + net.JoinHostPort(e.ip, a.port) + "/admin/"
}

// setAdminURLHeader trägt X-Terminal-Admin-URL ein, wenn die Adresse bestimmbar ist. Nie mit Zugangsdaten.
func setAdminURLHeader(req *http.Request) {
	if req.URL == nil {
		return
	}
	host, port := req.URL.Hostname(), req.URL.Port()
	if host == "" {
		return
	}
	if port == "" {
		port = "80"
		if req.URL.Scheme == "https" {
			port = "443"
		}
	}
	if v := adminURLState.value(net.JoinHostPort(host, port)); v != "" {
		req.Header.Set("X-Terminal-Admin-URL", v)
	}
}
