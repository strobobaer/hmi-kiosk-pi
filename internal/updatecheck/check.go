package updatecheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hmikiosk/internal/config"
)

// DefaultAPI ist die Adresse der GitHub-API.
const DefaultAPI = "https://api.github.com"

const (
	maxAPIBytes     = 1 << 20
	checkTimeout    = 15 * time.Second
	downloadTimeout = 10 * time.Minute
	maxRedirects    = 10
)

// Checker fragt die Releases eines Repositorys ab und lädt Paketdateien. Er stellt
// nur GET-Anfragen an BaseURL und an Adressen aus deren Antwort, immer über HTTPS und
// nur an Rechner von GitHub (siehe githubHost) oder den Rechner einer gesetzten BaseURL.
type Checker struct {
	BaseURL   string       // leer: DefaultAPI; nur Tests setzen sie
	Client    *http.Client // nil: Standard-Transport, also mit Proxy aus der Umgebung
	UserAgent string
}

func (c *Checker) client() *http.Client {
	cl := http.Client{Transport: http.DefaultTransport}
	if c.Client != nil {
		cl = *c.Client
	}
	cl.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return errors.New("Weiterleitung auf eine Adresse ohne HTTPS abgelehnt")
		}
		if !c.allowed(req.URL) {
			return fmt.Errorf("Weiterleitung auf %s nicht erlaubt (nur GitHub)", req.URL.Host)
		}
		if len(via) >= maxRedirects {
			return errors.New("zu viele Weiterleitungen")
		}
		return nil
	}
	return &cl
}

// githubHost lässt nur die Rechner von GitHub für API, Release-Seiten und Paketdateien zu,
// ohne oder mit Port 443. Das verhindert, dass eine Release-Liste das Terminal an
// Rechner im Anlagennetz schickt.
func githubHost(hostport string) bool {
	host, port := strings.ToLower(hostport), ""
	if h, p, err := net.SplitHostPort(host); err == nil {
		host, port = h, p
	}
	if port != "" && port != "443" {
		return false
	}
	switch host {
	case "api.github.com", "github.com", "objects.githubusercontent.com":
		return true
	}
	label, ok := strings.CutSuffix(host, ".githubusercontent.com")
	return ok && label != "" && !strings.Contains(label, ".")
}

// allowed: GitHub, dazu der Rechner einer abweichenden Basis-URL (nur im Test gesetzt).
func (c *Checker) allowed(u *url.URL) bool {
	if githubHost(u.Host) {
		return true
	}
	if c.BaseURL == "" {
		return false
	}
	base, err := url.Parse(c.BaseURL)
	return err == nil && base.Host != "" && strings.EqualFold(base.Host, u.Host)
}

func (c *Checker) get(ctx context.Context, raw, accept string) (*http.Response, error) {
	if !isHTTPS(raw) {
		return nil, errors.New("nur HTTPS-Adressen sind erlaubt")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	if !c.allowed(req.URL) {
		return nil, fmt.Errorf("Rechner %s ist nicht erlaubt (nur GitHub)", req.URL.Host)
	}
	req.Header.Set("Accept", accept)
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	return c.client().Do(req)
}

// Releases liefert die Releases von repo (Besitzer/Repository).
func (c *Checker) Releases(ctx context.Context, repo string) ([]Release, error) {
	if repo == "" || !config.ValidUpdateRepo(repo) {
		return nil, fmt.Errorf("Quelle ungültig: %q", repo)
	}
	owner, name, _ := strings.Cut(repo, "/")
	base := c.BaseURL
	if base == "" {
		base = DefaultAPI
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	resp, err := c.get(ctx, strings.TrimSuffix(base, "/")+"/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name)+"/releases?per_page=100", "application/vnd.github+json")
	if err != nil {
		return nil, fmt.Errorf("GitHub nicht erreichbar: %v", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("Repository %s nicht gefunden", repo)
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, errors.New("Abfragegrenze von GitHub erreicht, bitte später noch einmal versuchen")
	default:
		return nil, fmt.Errorf("GitHub antwortet mit Status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIBytes+1))
	if err != nil {
		return nil, fmt.Errorf("Antwort von GitHub nicht lesbar: %v", err)
	}
	if len(body) > maxAPIBytes {
		return nil, errors.New("Antwort von GitHub zu groß")
	}
	var rels []Release
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) || json.Unmarshal(body, &rels) != nil {
		return nil, errors.New("Antwort von GitHub ist keine Release-Liste")
	}
	return rels, nil
}

// Download lädt die Paketdatei eines Angebots in den Speicher.
func (c *Checker) Download(ctx context.Context, o Offer) ([]byte, error) {
	if o.AssetSize <= 0 || o.AssetSize > MaxPackageBytes {
		return nil, fmt.Errorf("Die Paketdatei ist leer oder größer als %d MB.", MaxPackageBytes>>20)
	}
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	resp, err := c.get(ctx, o.AssetURL, "application/octet-stream")
	if err != nil {
		return nil, fmt.Errorf("Download fehlgeschlagen: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Download fehlgeschlagen: Status %d", resp.StatusCode)
	}
	if resp.ContentLength > MaxPackageBytes {
		return nil, fmt.Errorf("Die Paketdatei ist größer als %d MB.", MaxPackageBytes>>20)
	}
	blob, err := io.ReadAll(io.LimitReader(resp.Body, MaxPackageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("Download abgebrochen: %v", err)
	}
	if int64(len(blob)) > MaxPackageBytes {
		return nil, fmt.Errorf("Die Paketdatei ist größer als %d MB.", MaxPackageBytes>>20)
	}
	if int64(len(blob)) != o.AssetSize {
		return nil, fmt.Errorf("Download unvollständig (%d von %d Bytes)", len(blob), o.AssetSize)
	}
	return blob, nil
}
