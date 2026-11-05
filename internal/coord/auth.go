package coord

import (
	"container/list"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

const (
	headerNonce     = "X-Kiosk-Nonce"
	headerSignature = "X-Kiosk-Signature"

	nonceBytes = 32
	nonceTTL   = 30 * time.Second
	maxNonces  = 1024
)

func signatureMAC(key []byte, path, nonce string, body []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(path + "\n" + nonce + "\n"))
	mac.Write(body)
	return mac.Sum(nil)
}

func sign(key []byte, path, nonce string, body []byte) string {
	return hex.EncodeToString(signatureMAC(key, path, nonce, body))
}

// verify lehnt bei leerem Schlüssel alles ab: Über einen leeren Schlüssel
// könnte sonst jeder eine gültige Signatur bilden.
func verify(key []byte, path, nonce string, body []byte, signature string) bool {
	if len(key) == 0 {
		return false
	}
	got, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	return hmac.Equal(got, signatureMAC(key, path, nonce, body))
}

type nonceEntry struct {
	value  string
	issued time.Time
}

// nonceStore hält ausgegebene Zufallszahlen, die älteste zuerst.
type nonceStore struct {
	mu     sync.Mutex
	clock  Clock
	order  *list.List
	byName map[string]*list.Element
}

func newNonceStore(c Clock) *nonceStore {
	return &nonceStore{clock: c, order: list.New(), byName: map[string]*list.Element{}}
}

func (s *nonceStore) issue() (string, error) {
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	v := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropExpired()
	if s.order.Len() >= maxNonces {
		s.remove(s.order.Front())
	}
	s.byName[v] = s.order.PushBack(nonceEntry{value: v, issued: s.clock.Now()})
	return v, nil
}

// consume meldet, ob die Zufallszahl gültig war, und entwertet sie.
func (s *nonceStore) consume(v string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropExpired()
	el, ok := s.byName[v]
	if !ok {
		return false
	}
	s.remove(el)
	return true
}

func (s *nonceStore) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.order.Len()
}

func (s *nonceStore) dropExpired() {
	now := s.clock.Now()
	for el := s.order.Front(); el != nil; el = s.order.Front() {
		if now.Sub(el.Value.(nonceEntry).issued) < nonceTTL {
			return
		}
		s.remove(el)
	}
}

func (s *nonceStore) remove(el *list.Element) {
	delete(s.byName, el.Value.(nonceEntry).value)
	s.order.Remove(el)
}
