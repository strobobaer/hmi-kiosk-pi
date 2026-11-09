package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"hmikiosk/internal/config"
)

const (
	sessionCookie    = "hmi_admin"
	preSessionCookie = "hmi_admin_pre"
	cookiePath       = "/admin"

	sessionIdle = 30 * time.Minute

	// Am Terminal (Loopback, HTTP) eigene Cookies ohne Secure und eine kurze Sitzung:
	// Nach 5 Minuten ohne Eingabe geht es zurück zur Kachelseite.
	localSessionCookie    = "hmi_admin_local"
	localPreSessionCookie = "hmi_admin_local_pre"
	localSessionIdle      = 5 * time.Minute
	maxFailures           = 5
	lockoutDuration       = 5 * time.Minute
	minPasswordLen        = 10
)

func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("web: keine Zufallswerte: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func tokensEqual(got, want string) bool {
	return got != "" && want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func adminCookie(name, value string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name: name, Value: value, Path: cookiePath,
		Secure: secure, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	}
}

func clearCookie(w http.ResponseWriter, name string, secure bool) {
	c := adminCookie(name, "", secure)
	c.MaxAge = -1
	http.SetCookie(w, c)
}

// --- Sitzungen ---

type sessionData struct {
	csrf     string
	local    bool // am Terminal angemeldet (Loopback, HTTP)
	lastSeen time.Time
	flash    string
	showOnce string // eben erzeugter Schlüssel der Fernsteuerung, für genau eine Anzeige
}

// sessionRef verweist auf eine Sitzung; der Schlüssel ist der Hash des
// Cookie-Werts, der Wert selbst liegt nicht im Speicher.
type sessionRef struct {
	key   [sha256.Size]byte
	csrf  string
	local bool
}

func (s *sessionData) expired(now time.Time) bool {
	idle := sessionIdle
	if s.local {
		idle = localSessionIdle
	}
	return now.Sub(s.lastSeen) >= idle
}

type sessionStore struct {
	mu   sync.Mutex
	byID map[[sha256.Size]byte]*sessionData
}

func newSessionStore() *sessionStore {
	return &sessionStore{byID: map[[sha256.Size]byte]*sessionData{}}
}

func (st *sessionStore) create(now time.Time, local bool) (string, sessionRef) {
	token := newToken()
	key := sha256.Sum256([]byte(token))
	csrf := newToken()
	st.mu.Lock()
	defer st.mu.Unlock()
	for k, s := range st.byID {
		if s.expired(now) {
			delete(st.byID, k)
		}
	}
	st.byID[key] = &sessionData{csrf: csrf, lastSeen: now, local: local}
	return token, sessionRef{key: key, csrf: csrf, local: local}
}

// touch gilt nur für eine Sitzung derselben Art: Ein Wert vom Terminal nützt über HTTPS
// nichts und umgekehrt.
func (st *sessionStore) touch(token string, now time.Time, local bool) (sessionRef, bool) {
	key := sha256.Sum256([]byte(token))
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.byID[key]
	if !ok || s.local != local {
		return sessionRef{}, false
	}
	if s.expired(now) {
		delete(st.byID, key)
		return sessionRef{}, false
	}
	s.lastSeen = now
	return sessionRef{key: key, csrf: s.csrf, local: local}, true
}

func (st *sessionStore) remove(s sessionRef) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.byID, s.key)
}

func (st *sessionStore) clear() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.byID = map[[sha256.Size]byte]*sessionData{}
}

func (st *sessionStore) setFlash(s sessionRef, msg string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if d, ok := st.byID[s.key]; ok {
		d.flash = msg
	}
}

func (st *sessionStore) takeFlash(s sessionRef) string {
	st.mu.Lock()
	defer st.mu.Unlock()
	d, ok := st.byID[s.key]
	if !ok {
		return ""
	}
	msg := d.flash
	d.flash = ""
	return msg
}

// cookieNames liefert die Namen von Sitzungs- und Vor-Cookie; am Terminal eigene.
func cookieNames(local bool) (session, pre string) {
	if local {
		return localSessionCookie, localPreSessionCookie
	}
	return sessionCookie, preSessionCookie
}

func (a *admin) sessionFrom(r *http.Request) (sessionRef, bool) {
	local := onTerminal(r)
	name, _ := cookieNames(local)
	c, err := r.Cookie(name)
	if err != nil {
		return sessionRef{}, false
	}
	return a.sessions.touch(c.Value, a.d.Clock.Now(), local)
}

// startSession gibt immer einen neuen Wert aus, damit ein vorher bekannter
// Cookie-Wert nach der Anmeldung nichts nützt.
func (a *admin) startSession(w http.ResponseWriter, r *http.Request) sessionRef {
	local := onTerminal(r)
	name, pre := cookieNames(local)
	token, s := a.sessions.create(a.d.Clock.Now(), local)
	http.SetCookie(w, adminCookie(name, token, !local))
	clearCookie(w, pre, !local)
	return s
}

// --- Sperre ---

// lockout zählt Fehlversuche global: Es gibt nur ein Admin-Konto.
type lockout struct {
	mu       sync.Mutex
	failures int
	until    time.Time
}

func (l *lockout) remaining(now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.until.IsZero() {
		return 0
	}
	if !now.Before(l.until) {
		l.until = time.Time{}
		l.failures = 0
		return 0
	}
	return l.until.Sub(now)
}

func (l *lockout) fail(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures++
	if l.failures >= maxFailures {
		l.until = now.Add(lockoutDuration)
	}
}

func (l *lockout) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures = 0
	l.until = time.Time{}
}

func lockedMessage(d time.Duration) string {
	minutes := int((d + time.Minute - 1) / time.Minute) // angefangene Minuten zählen
	if minutes == 1 {
		return "Zu viele Fehlversuche. Die Anmeldung ist noch 1 Minute gesperrt."
	}
	return fmt.Sprintf("Zu viele Fehlversuche. Die Anmeldung ist noch %d Minuten gesperrt.", minutes)
}

// acquireAuth belegt den einzigen Platz für Argon2-Arbeit. Gibt der Browser auf,
// bevor er an der Reihe ist, wird nichts mehr gerechnet.
func (a *admin) acquireAuth(ctx context.Context) bool {
	select {
	case a.authSlot <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (a *admin) releaseAuth() { <-a.authSlot }

var errWrongPassword = errors.New("Das Passwort ist falsch.")

type lockedError struct{ left time.Duration }

func (e lockedError) Error() string { return lockedMessage(e.left) }

// verifyHeld prüft pw gegen das gespeicherte Passwort. Der Aufrufer hält authSlot.
// Die Sperre wird erst hier verbindlich geprüft, nachdem der Platz belegt ist.
func (a *admin) verifyHeld(pw string) error {
	now := a.d.Clock.Now()
	if left := a.lock.remaining(now); left > 0 {
		return lockedError{left}
	}
	cfg, _, err := a.d.Store.Load()
	if err != nil {
		return errConfigUnreadable
	}
	if checkPassword(cfg.Admin.PasswordHash, pw) {
		a.lock.reset()
		return nil
	}
	a.lock.fail(now)
	if left := a.lock.remaining(now); left > 0 {
		return lockedError{left}
	}
	return errWrongPassword
}

func authStatus(err error) int {
	var locked lockedError
	if errors.As(err, &locked) {
		return http.StatusTooManyRequests
	}
	if errors.Is(err, errConfigUnreadable) {
		return http.StatusInternalServerError
	}
	return http.StatusUnauthorized
}

// checkNewPassword gilt für die Ersteinrichtung und den Passwortwechsel.
func checkNewPassword(pw, repeat string) error {
	if utf8.RuneCountInString(pw) < minPasswordLen {
		return fmt.Errorf("Das Passwort muss mindestens %d Zeichen lang sein.", minPasswordLen)
	}
	if pw != repeat {
		return errors.New("Die beiden Eingaben stimmen nicht überein.")
	}
	return nil
}

// --- Vorsitzung: CSRF für Anmeldung und Ersteinrichtung ---

// preToken liefert den Wert des Vor-Cookies und setzt ihn bei Bedarf.
func preToken(w http.ResponseWriter, r *http.Request) string {
	local := onTerminal(r)
	_, pre := cookieNames(local)
	if c, err := r.Cookie(pre); err == nil && c.Value != "" {
		return c.Value
	}
	t := newToken()
	http.SetCookie(w, adminCookie(pre, t, !local))
	return t
}

func checkPreToken(r *http.Request) bool {
	_, pre := cookieNames(onTerminal(r))
	c, err := r.Cookie(pre)
	return err == nil && tokensEqual(r.PostForm.Get("csrf"), c.Value)
}

// --- Ersteinrichtung ---

func (a *admin) renderSetup(w http.ResponseWriter, r *http.Request, status int, msg string) {
	a.writePage(w, status, "setup.html", adminView{Title: "Ersteinrichtung", CSRF: preToken(w, r), Error: msg, Terminal: onTerminal(r)})
}

func (a *admin) handleSetupForm(w http.ResponseWriter, r *http.Request, _ sessionRef) {
	a.renderSetup(w, r, http.StatusOK, "")
}

var errAlreadySet = errors.New("Passwort ist bereits gesetzt")

func (a *admin) handleSetup(w http.ResponseWriter, r *http.Request, _ sessionRef) {
	pw := r.PostForm.Get("password")
	if err := checkNewPassword(pw, r.PostForm.Get("password2")); err != nil {
		a.renderSetup(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if !a.acquireAuth(r.Context()) {
		return
	}
	defer a.releaseAuth()
	hash, err := hashPassword(pw)
	if err != nil {
		a.renderSetup(w, r, http.StatusInternalServerError, "Das Passwort konnte nicht verarbeitet werden.")
		return
	}
	// Erneut unter der Sperre prüfen: Zwei gleichzeitige Einrichtungen dürfen
	// einander nicht überschreiben.
	err = a.update(func(c *config.Config) error {
		if c.Admin.PasswordHash != "" {
			return errAlreadySet
		}
		c.Admin.PasswordHash = hash
		return nil
	})
	switch {
	case errors.Is(err, errAlreadySet):
		notFound(w)
	case err != nil:
		a.renderSetup(w, r, formStatus(err), publicMessage(err))
	default:
		s := a.startSession(w, r)
		a.done(w, r, s, "/admin/hmis", "Passwort gesetzt.")
	}
}

// --- Anmeldung ---

func (a *admin) renderLogin(w http.ResponseWriter, r *http.Request, status int, msg string) {
	a.writePage(w, status, "login.html", adminView{Title: "Anmeldung", CSRF: preToken(w, r), Error: msg, Terminal: onTerminal(r)})
}

func (a *admin) handleLoginForm(w http.ResponseWriter, r *http.Request, _ sessionRef) {
	if _, ok := a.sessionFrom(r); ok {
		redirect(w, r, "/admin/hmis")
		return
	}
	a.renderLogin(w, r, http.StatusOK, "")
}

func (a *admin) handleLogin(w http.ResponseWriter, r *http.Request, _ sessionRef) {
	// Vorab ohne Warteschlange prüfen: Während der Sperre keine Argon2-Arbeit.
	if left := a.lock.remaining(a.d.Clock.Now()); left > 0 {
		a.renderLogin(w, r, http.StatusTooManyRequests, lockedMessage(left))
		return
	}
	if !a.acquireAuth(r.Context()) {
		return
	}
	err := a.verifyHeld(r.PostForm.Get("password"))
	a.releaseAuth()
	if err != nil {
		a.renderLogin(w, r, authStatus(err), loginMessage(err))
		return
	}
	a.startSession(w, r)
	redirect(w, r, "/admin/hmis")
}

func loginMessage(err error) string {
	if errors.Is(err, errWrongPassword) {
		return "Anmeldung fehlgeschlagen."
	}
	return err.Error()
}

func (a *admin) handleLogout(w http.ResponseWriter, r *http.Request, s sessionRef) {
	a.sessions.remove(s)
	name, _ := cookieNames(s.local)
	clearCookie(w, name, !s.local)
	redirect(w, r, "/admin/login")
}

// --- Passwortwechsel ---

func (a *admin) handleChangePassword(w http.ResponseWriter, r *http.Request, s sessionRef) {
	f := r.PostForm
	if err := checkNewPassword(f.Get("password"), f.Get("password2")); err != nil {
		a.renderSystem(w, s, http.StatusUnprocessableEntity, err.Error(), systemExtras{})
		return
	}
	if left := a.lock.remaining(a.d.Clock.Now()); left > 0 {
		a.renderSystem(w, s, http.StatusTooManyRequests, lockedMessage(left), systemExtras{})
		return
	}
	if !a.acquireAuth(r.Context()) {
		return
	}
	defer a.releaseAuth()
	if err := a.verifyHeld(f.Get("current")); err != nil {
		msg := err.Error()
		if errors.Is(err, errWrongPassword) {
			msg = "Das aktuelle Passwort ist falsch."
		}
		a.renderSystem(w, s, authStatus(err), msg, systemExtras{})
		return
	}
	hash, err := hashPassword(f.Get("password"))
	if err != nil {
		a.renderSystem(w, s, http.StatusInternalServerError, "Das Passwort konnte nicht verarbeitet werden.", systemExtras{})
		return
	}
	err = a.update(func(c *config.Config) error {
		c.Admin.PasswordHash = hash
		return nil
	})
	if err != nil {
		a.renderSystem(w, s, formStatus(err), err.Error(), systemExtras{})
		return
	}
	// Alle Sitzungen enden, auch ein womöglich mitgelesener Wert der eigenen.
	a.sessions.clear()
	fresh := a.startSession(w, r)
	a.done(w, r, fresh, "/admin/system", "Passwort geändert. Andere Sitzungen wurden beendet.")
}

// --- CSRF bei Uploads ---

// serveUpload prüft den CSRF-Wert, bevor die Datei gelesen wird. Er muss deshalb
// das erste Feld des Formulars sein; die Seiten senden ihn als erstes.
func (a *admin) serveUpload(w http.ResponseWriter, r *http.Request, rt route, s sessionRef) {
	r.Body = http.MaxBytesReader(w, r.Body, rt.limit)
	mr, err := r.MultipartReader()
	if err != nil {
		rejectCSRF(w)
		return
	}
	part, err := mr.NextPart()
	if err != nil {
		if isTooLarge(err) {
			http.Error(w, "Anfrage zu groß", http.StatusRequestEntityTooLarge)
			return
		}
		rejectCSRF(w)
		return
	}
	if part.FormName() != "csrf" {
		rejectCSRF(w)
		return
	}
	tok, err := io.ReadAll(io.LimitReader(part, 256))
	if err != nil || !tokensEqual(string(tok), s.csrf) {
		rejectCSRF(w)
		return
	}
	rt.upload(w, r, s, mr)
}

// readUpload liest die Felder und die Datei "file" nach dem CSRF-Wert.
func readUpload(mr *multipart.Reader) (fields map[string]string, file []byte, err error) {
	fields = map[string]string{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return fields, file, nil
		}
		if err != nil {
			return nil, nil, err
		}
		if part.FormName() == "file" {
			if file, err = io.ReadAll(part); err != nil {
				return nil, nil, err
			}
			continue
		}
		v, err := io.ReadAll(io.LimitReader(part, formBodyLimit))
		if err != nil {
			return nil, nil, err
		}
		fields[part.FormName()] = string(v)
	}
}

// setShowOnce hält einen Wert für genau eine Anzeige in dieser Sitzung (POST-Redirect-GET).
func (st *sessionStore) setShowOnce(s sessionRef, v string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if d, ok := st.byID[s.key]; ok {
		d.showOnce = v
	}
}

func (st *sessionStore) takeShowOnce(s sessionRef) string {
	st.mu.Lock()
	defer st.mu.Unlock()
	d, ok := st.byID[s.key]
	if !ok {
		return ""
	}
	v := d.showOnce
	d.showOnce = ""
	return v
}
