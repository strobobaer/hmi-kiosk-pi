package coord

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

const (
	pathNonce     = "/peer/nonce"
	pathClaim     = "/peer/claim"
	pathHeartbeat = "/peer/heartbeat"
	pathTakeover  = "/peer/takeover"

	maxBodyBytes = 64 << 10
)

type nonceResp struct {
	Nonce string `json:"nonce"`
}

type heartbeatReq struct {
	HMI    string `json:"hmi"`
	Holder Holder `json:"holder"`
}

type takeoverReq struct {
	HMI  string        `json:"hmi"`
	By   Holder        `json:"by"`
	Warn time.Duration `json:"warn"`
}

var errBadRequest = errors.New("ungültige Anfrage")

type peerAPI struct {
	key    func() []byte
	nonces *nonceStore
}

// NewHandler stellt die Terminal-API bereit. Außer /peer/nonce ist jede Route
// signiert und verlangt eine frische Zufallszahl.
func NewHandler(c *Coordinator, key []byte) http.Handler {
	return NewHandlerFunc(c, func() []byte { return key })
}

// NewHandlerFunc liest den Schlüssel je Anfrage, damit ein neuer sofort gilt.
func NewHandlerFunc(c *Coordinator, key func() []byte) http.Handler {
	a := &peerAPI{key: key, nonces: newNonceStore(c.clock)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+pathNonce, a.serveNonce)
	mux.HandleFunc("POST "+pathClaim, signedJSON(a, pathClaim, func(r ClaimReq) (any, error) {
		if r.HMI == "" || r.Claim.Terminal == "" {
			return nil, errBadRequest
		}
		return c.handleClaim(r), nil
	}))
	mux.HandleFunc("POST "+pathHeartbeat, signedJSON(a, pathHeartbeat, func(r heartbeatReq) (any, error) {
		if r.HMI == "" || r.Holder.Terminal == "" {
			return nil, errBadRequest
		}
		c.handleHeartbeat(r.HMI, r.Holder)
		return struct{}{}, nil
	}))
	mux.HandleFunc("POST "+pathTakeover, signedJSON(a, pathTakeover, func(r takeoverReq) (any, error) {
		if r.HMI == "" || r.By.Terminal == "" || r.Warn < 0 {
			return nil, errBadRequest
		}
		c.handleTakeover(r.HMI, r.By, r.Warn)
		return struct{}{}, nil
	}))
	return mux
}

func (a *peerAPI) serveNonce(w http.ResponseWriter, _ *http.Request) {
	nonce, err := a.nonces.issue()
	if err != nil {
		http.Error(w, "keine Zufallszahl", http.StatusInternalServerError)
		return
	}
	writeJSON(w, nonceResp{Nonce: nonce})
}

func signedJSON[Req any](a *peerAPI, path string, handle func(Req) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, "Anfrage zu groß", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Anfrage nicht lesbar", http.StatusBadRequest)
			return
		}
		// Erst die Signatur prüfen: Ohne Schlüssel darf niemand Zufallszahlen entwerten.
		nonce := r.Header.Get(headerNonce)
		if !verify(a.key(), path, nonce, body, r.Header.Get(headerSignature)) || !a.nonces.consume(nonce) {
			http.Error(w, "Signatur abgelehnt", http.StatusUnauthorized)
			return
		}
		var req Req
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, errBadRequest.Error(), http.StatusBadRequest)
			return
		}
		resp, err := handle(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, resp)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
