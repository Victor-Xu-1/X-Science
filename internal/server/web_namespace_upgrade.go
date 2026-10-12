package server

import (
	"net/http"
	"strings"
	"time"
)

var previousWebCookieNames = [...]string{"synon_session", "synon_csrf", "synon_device"}

// Retire both historical session and device cookies at every authenticated
// session transition. Otherwise a second account's old cookie could become
// authoritative again after the current account explicitly logs out.
func (s *Server) retirePreviousWebCookies(w http.ResponseWriter, r *http.Request) {
	for _, name := range previousWebCookieNames {
		http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1,
			Expires: time.Unix(1, 0), HttpOnly: name != "synon_csrf",
			Secure: s.webCookieSecure(r), SameSite: http.SameSiteLaxMode})
	}
}

// Historical cookie names are explicit upgrade inputs only. The canonical
// session ledger authenticates the opaque token before any transfer; changing
// a cookie name can never create an identity or revive a revoked session.
func (s *Server) upgradeWebCookieNamespace(w http.ResponseWriter, r *http.Request) *http.Request {
	if s.webSessions == nil || webSessionCookieToken(r) != "" {
		return r
	}
	previous, err := r.Cookie("synon_session")
	if err != nil || strings.TrimSpace(previous.Value) == "" {
		return r
	}
	session, valid, err := s.webSessions.Authenticate(previous.Value, false)
	if err != nil || !valid {
		return r
	}
	csrf, err := r.Cookie("synon_csrf")
	if err != nil {
		return r
	}
	valid, err = s.webSessions.ValidateCSRF(previous.Value, csrf.Value)
	if err != nil || !valid {
		return r
	}
	var device string
	if priorDevice, err := r.Cookie("synon_device"); err == nil {
		device = priorDevice.Value
	}
	s.writeWebSessionCookies(w, r, previous.Value, csrf.Value, device, session.Remembered, session.ExpiresAt)
	previousNames := map[string]bool{"synon_session": true, "synon_csrf": true, "synon_device": true}
	upgraded := r.Clone(r.Context())
	upgraded.Header = r.Header.Clone()
	upgraded.Header.Del("Cookie")
	for _, cookie := range r.Cookies() {
		if !previousNames[cookie.Name] && cookie.Name != webCSRFCookieName && cookie.Name != webDeviceCookieName {
			upgraded.AddCookie(cookie)
		}
	}
	upgraded.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: previous.Value})
	upgraded.AddCookie(&http.Cookie{Name: webCSRFCookieName, Value: csrf.Value})
	if device != "" {
		upgraded.AddCookie(&http.Cookie{Name: webDeviceCookieName, Value: device})
	}
	return upgraded
}
