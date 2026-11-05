package coord

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"hmikiosk/internal/config"
)

const (
	defaultPeerPort = "8443"
	idleConnTimeout = 30 * time.Second
)

// ErrBadKey: Der Empfänger lehnt die Signatur ab.
var ErrBadKey = errors.New("Schlüssel vom Terminal abgelehnt")

type ClaimReq struct {
	HMI   string
	Claim Claim
}

type ClaimResp struct {
	Holder      *Holder
	ContestedBy *Claim
}

// Transport spricht mit einem anderen Terminal.
type Transport interface {
	Claim(ctx context.Context, peer config.Peer, req ClaimReq) (ClaimResp, error)
	Heartbeat(ctx context.Context, peer config.Peer, hmi string, h Holder) error
	Takeover(ctx context.Context, peer config.Peer, hmi string, by Holder, warn time.Duration) error
}

type httpTransport struct {
	key    func() []byte
	client *http.Client
}

// NewHTTPTransport signiert jede Anfrage mit key. Ohne client wird einer erzeugt,
// der selbst erzeugte Zertifikate annimmt.
func NewHTTPTransport(key []byte, client *http.Client) Transport {
	return NewHTTPTransportFunc(func() []byte { return key }, client)
}

// NewHTTPTransportFunc liest den Schlüssel je Anfrage, damit ein neuer sofort gilt.
func NewHTTPTransportFunc(key func() []byte, client *http.Client) Transport {
	if client == nil {
		client = &http.Client{Transport: &http.Transport{
			// Die Terminals haben selbst erzeugte Zertifikate. Die Echtheit kommt
			// aus der HMAC über jede Anfrage, nicht aus TLS.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			// Ein neu gestartetes Terminal soll keine tote Verbindung hinterlassen.
			IdleConnTimeout: idleConnTimeout,
		}}
	}
	return &httpTransport{key: key, client: client}
}

func (t *httpTransport) Claim(ctx context.Context, peer config.Peer, req ClaimReq) (ClaimResp, error) {
	var resp ClaimResp
	err := t.call(ctx, peer, pathClaim, req, &resp)
	return resp, err
}

func (t *httpTransport) Heartbeat(ctx context.Context, peer config.Peer, hmi string, h Holder) error {
	return t.call(ctx, peer, pathHeartbeat, heartbeatReq{HMI: hmi, Holder: h}, nil)
}

func (t *httpTransport) Takeover(ctx context.Context, peer config.Peer, hmi string, by Holder, warn time.Duration) error {
	return t.call(ctx, peer, pathTakeover, takeoverReq{HMI: hmi, By: by, Warn: warn}, nil)
}

func (t *httpTransport) call(ctx context.Context, peer config.Peer, path string, in, out any) error {
	base := peerBaseURL(peer.Address)
	var nonce nonceResp
	if err := t.post(ctx, base+pathNonce, nil, nil, &nonce); err != nil {
		return err
	}
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	headers := map[string]string{
		headerNonce:     nonce.Nonce,
		headerSignature: sign(t.key(), path, nonce.Nonce, body),
	}
	return t.post(ctx, base+path, headers, body, out)
}

func (t *httpTransport) post(ctx context.Context, url string, headers map[string]string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		// Nur ein ganz gelesener Körper gibt die Verbindung zur Wiederverwendung frei.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
		resp.Body.Close()
	}()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrBadKey
	case resp.StatusCode != http.StatusOK:
		return &StatusError{Code: resp.StatusCode}
	case out == nil:
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(out)
}

func peerBaseURL(address string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return "https://" + address
	}
	host := strings.TrimSuffix(strings.TrimPrefix(address, "["), "]")
	return "https://" + net.JoinHostPort(host, defaultPeerPort)
}

// PeerState ist das Ergebnis von CheckPeer. KeyOK gilt nur bei Reachable.
type PeerState struct {
	Reachable, KeyOK bool
	Err              error
}

// StatusError ist eine Antwort mit anderem Status als 200.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("Terminal antwortet mit Status %d", e.Code) }

// CheckPeer prüft ein anderes Terminal ohne Nebenwirkung: erst die Zufallszahl
// (erreichbar), dann ein signierter Herzschlag mit leerer HMI-Kennung. Der
// Empfänger prüft die Signatur vor dem Inhalt und lehnt den leeren Inhalt mit 400
// ab, ohne etwas zu ändern. 401 heißt falscher Schlüssel, 400 richtiger.
func CheckPeer(ctx context.Context, key func() []byte, client *http.Client, peer config.Peer) PeerState {
	t := NewHTTPTransportFunc(key, client).(*httpTransport)
	var nonce nonceResp
	if err := t.post(ctx, peerBaseURL(peer.Address)+pathNonce, nil, nil, &nonce); err != nil {
		return PeerState{Err: err}
	}
	err := t.Heartbeat(ctx, peer, "", Holder{})
	var se *StatusError
	switch {
	case errors.Is(err, ErrBadKey):
		return PeerState{Reachable: true}
	case errors.As(err, &se) && se.Code == http.StatusBadRequest:
		return PeerState{Reachable: true, KeyOK: true}
	case err == nil:
		return PeerState{Reachable: true, KeyOK: true}
	}
	return PeerState{Reachable: true, Err: err}
}
