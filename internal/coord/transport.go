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

const defaultPeerPort = "8443"

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
	key    []byte
	client *http.Client
}

// NewHTTPTransport signiert jede Anfrage mit key. Ohne client wird einer erzeugt,
// der selbst erzeugte Zertifikate annimmt.
func NewHTTPTransport(key []byte, client *http.Client) Transport {
	if client == nil {
		client = &http.Client{Transport: &http.Transport{
			// Die Terminals haben selbst erzeugte Zertifikate. Die Echtheit kommt
			// aus der HMAC über jede Anfrage, nicht aus TLS.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
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
		headerSignature: sign(t.key, path, nonce.Nonce, body),
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
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrBadKey
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("Terminal antwortet mit Status %d", resp.StatusCode)
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
