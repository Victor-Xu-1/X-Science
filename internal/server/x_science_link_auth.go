package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultXScienceLinkSessionTTL = 48 * time.Hour
	maxXScienceLinkLoginBytes     = 4 * 1024
	maxXScienceLinkSessionBytes   = 2048
	xscienceSessionVersion        = 1
	xscienceSessionScopeWeb       = "web"
	xscienceSessionScopeLink      = "x-science-link"
)

var (
	errXScienceLinkAuthDisabled   = errors.New("X-Science Link login is not configured")
	errXScienceLinkInvalidLogin   = errors.New("invalid username or password")
	errXScienceLinkInvalidSession = errors.New("invalid or expired X-Science Link session")
)

type XScienceLinkAuthOptions struct {
	Username string
	Password string
	UserID   string
	TTL      time.Duration
}

type xscienceLinkAuthUser struct {
	ID          string `json:"id"`
	UserID      string `json:"userId"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName,omitempty"`
	Email       string `json:"email,omitempty"`
	Provider    string `json:"provider"`
}

type xscienceLinkSessionPayload struct {
	Version     int    `json:"v"`
	UserID      string `json:"uid"`
	Username    string `json:"usr"`
	DisplayName string `json:"displayName,omitempty"`
	Email       string `json:"email,omitempty"`
	Scope       string `json:"scp"`
	Expires     int64  `json:"exp"`
	Nonce       string `json:"nonce"`
}

type xscienceLinkAuthenticator struct {
	enabled      bool
	usernameHash [sha256.Size]byte
	passwordHash [sha256.Size]byte
	signingKey   [sha256.Size]byte
	username     string
	userID       string
	ttl          time.Duration
	now          func() time.Time
	random       io.Reader
}

func newXScienceLinkAuthenticator(options XScienceLinkAuthOptions) *xscienceLinkAuthenticator {
	username := strings.TrimSpace(options.Username)
	password := options.Password
	userID := strings.TrimSpace(options.UserID)
	if username == "" {
		username = "local"
	}
	if userID == "" {
		userID = "local"
	}
	ttl := options.TTL
	if ttl <= 0 {
		ttl = defaultXScienceLinkSessionTTL
	}
	auth := &xscienceLinkAuthenticator{
		enabled:  password != "",
		username: username,
		userID:   userID,
		ttl:      ttl,
		now:      time.Now,
		random:   rand.Reader,
	}
	auth.usernameHash = sha256.Sum256([]byte(username))
	auth.passwordHash = sha256.Sum256([]byte(password))
	auth.signingKey = sha256.Sum256([]byte("x-science-link-session\x00" + username + "\x00" + userID + "\x00" + password))
	return auth
}

func (a *xscienceLinkAuthenticator) Login(username, password string) (string, xscienceLinkAuthUser, error) {
	return a.LoginForScope(username, password, xscienceSessionScopeLink)
}

func (a *xscienceLinkAuthenticator) LoginForScope(username, password, scope string) (string, xscienceLinkAuthUser, error) {
	user, err := a.validateCredentials(username, password)
	if err != nil {
		return "", xscienceLinkAuthUser{}, err
	}
	return a.createSessionForUser(user, scope)
}

func (a *xscienceLinkAuthenticator) validateCredentials(username, password string) (xscienceLinkAuthUser, error) {
	if a == nil || !a.enabled {
		return xscienceLinkAuthUser{}, errXScienceLinkAuthDisabled
	}
	usernameHash := sha256.Sum256([]byte(strings.TrimSpace(username)))
	passwordHash := sha256.Sum256([]byte(password))
	if subtle.ConstantTimeCompare(usernameHash[:], a.usernameHash[:]) != 1 || subtle.ConstantTimeCompare(passwordHash[:], a.passwordHash[:]) != 1 {
		return xscienceLinkAuthUser{}, errXScienceLinkInvalidLogin
	}
	return a.user(), nil
}

func (a *xscienceLinkAuthenticator) createSessionForUser(user xscienceLinkAuthUser, scope string) (string, xscienceLinkAuthUser, error) {
	if a == nil || !a.enabled {
		return "", xscienceLinkAuthUser{}, errXScienceLinkAuthDisabled
	}
	if !validXScienceSessionScope(scope) {
		return "", xscienceLinkAuthUser{}, errXScienceLinkInvalidSession
	}
	user.ID = strings.TrimSpace(user.ID)
	user.UserID = strings.TrimSpace(user.UserID)
	user.Username = strings.TrimSpace(user.Username)
	if user.ID == "" {
		user.ID = user.UserID
	}
	if user.UserID == "" {
		user.UserID = user.ID
	}
	if user.DisplayName == "" {
		user.DisplayName = user.Username
	}
	if user.Provider == "" {
		user.Provider = "local"
	}
	if user.ID == "" || user.UserID == "" || user.Username == "" ||
		len(user.ID) > 512 || len(user.Username) > 512 || len(user.DisplayName) > 512 || len(user.Email) > 512 {
		return "", xscienceLinkAuthUser{}, errXScienceLinkInvalidSession
	}
	nonceBytes := make([]byte, 18)
	if _, err := io.ReadFull(a.random, nonceBytes); err != nil {
		return "", xscienceLinkAuthUser{}, fmt.Errorf("generate X-Science Link session nonce: %w", err)
	}
	payload := xscienceLinkSessionPayload{
		Version: xscienceSessionVersion, UserID: user.UserID, Username: user.Username,
		DisplayName: user.DisplayName, Email: user.Email, Scope: scope,
		Expires: a.now().UTC().Add(a.ttl).Unix(), Nonce: base64.RawURLEncoding.EncodeToString(nonceBytes),
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return "", xscienceLinkAuthUser{}, err
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(encodedPayload)
	signaturePart := base64.RawURLEncoding.EncodeToString(a.sign([]byte(payloadPart)))
	return payloadPart + "." + signaturePart, user, nil
}

func (a *xscienceLinkAuthenticator) Authenticate(token string) (xscienceLinkAuthUser, bool) {
	payload, ok := a.sessionPayload(token)
	if !ok {
		return xscienceLinkAuthUser{}, false
	}
	if !validXScienceSessionScope(payload.Scope) || !a.validSessionPayload(payload) {
		return xscienceLinkAuthUser{}, false
	}
	return userFromXScienceSession(payload), true
}

func (a *xscienceLinkAuthenticator) AuthenticateScope(token, scope string) (xscienceLinkAuthUser, bool) {
	payload, ok := a.sessionPayload(token)
	if !ok || payload.Scope != scope || !a.validSessionPayload(payload) {
		return xscienceLinkAuthUser{}, false
	}
	return userFromXScienceSession(payload), true
}

func (a *xscienceLinkAuthenticator) validSessionPayload(payload xscienceLinkSessionPayload) bool {
	return a != nil && payload.Version == xscienceSessionVersion &&
		strings.TrimSpace(payload.UserID) != "" && strings.TrimSpace(payload.Username) != "" &&
		payload.Nonce != "" && payload.Expires > a.now().UTC().Unix()
}

func userFromXScienceSession(payload xscienceLinkSessionPayload) xscienceLinkAuthUser {
	displayName := payload.DisplayName
	if displayName == "" {
		displayName = payload.Username
	}
	return xscienceLinkAuthUser{
		ID: payload.UserID, UserID: payload.UserID, Username: payload.Username,
		DisplayName: displayName, Email: payload.Email, Provider: "local",
	}
}

func validXScienceSessionScope(scope string) bool {
	return scope == xscienceSessionScopeWeb || scope == xscienceSessionScopeLink
}

func (a *xscienceLinkAuthenticator) sessionPayload(token string) (xscienceLinkSessionPayload, bool) {
	if a == nil || !a.enabled || len(token) == 0 || len(token) > maxXScienceLinkSessionBytes {
		return xscienceLinkSessionPayload{}, false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return xscienceLinkSessionPayload{}, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, a.sign([]byte(parts[0]))) {
		return xscienceLinkSessionPayload{}, false
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(payloadJSON) > maxXScienceLinkSessionBytes {
		return xscienceLinkSessionPayload{}, false
	}
	var payload xscienceLinkSessionPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return xscienceLinkSessionPayload{}, false
	}
	return payload, true
}

func (a *xscienceLinkAuthenticator) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, a.signingKey[:])
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func (a *xscienceLinkAuthenticator) user() xscienceLinkAuthUser {
	return xscienceLinkAuthUser{
		ID: a.userID, UserID: a.userID, Username: a.username, DisplayName: a.username, Provider: "local",
	}
}

func (s *Server) handleXScienceLinkLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST is required")
		return
	}
	if !isLoopbackRequest(r) {
		writeError(w, http.StatusForbidden, "LOCAL_ONLY", "X-Science Link login is available only from the local host")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxXScienceLinkLoginBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid login request")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "login request must contain one JSON value")
		return
	}
	if retryAfter := s.loginLimiter.RetryAfter(r, input.Username); retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(max(1, int64(retryAfter.Round(time.Second)/time.Second)), 10))
		writeError(w, http.StatusTooManyRequests, "LOGIN_RATE_LIMITED", "too many login attempts; try again later")
		return
	}
	token, user, err := s.xscienceLinkAuth.LoginForScope(input.Username, input.Password, xscienceSessionScopeLink)
	if err != nil {
		if errors.Is(err, errXScienceLinkInvalidLogin) {
			s.loginLimiter.RecordFailure(r, input.Username)
		}
		if errors.Is(err, errXScienceLinkAuthDisabled) {
			writeError(w, http.StatusServiceUnavailable, "AUTH_NOT_CONFIGURED", err.Error())
			return
		}
		if errors.Is(err, errXScienceLinkInvalidLogin) {
			writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "AUTH_ERROR", "unable to create X-Science Link session")
		return
	}
	s.loginLimiter.RecordSuccess(r, input.Username)
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": user, "expiresIn": int64(s.xscienceLinkAuth.ttl.Seconds())})
}

func (s *Server) handleXScienceLinkCompatibilityWebSocket(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("appSession"))
	if revoked, err := s.xscienceLinkSessionRevoked(token); err != nil || revoked {
		http.Error(w, errXScienceLinkInvalidSession.Error(), http.StatusUnauthorized)
		return
	}
	user, ok := s.xscienceLinkAuth.AuthenticateScope(token, xscienceSessionScopeLink)
	if !ok {
		http.Error(w, errXScienceLinkInvalidSession.Error(), http.StatusUnauthorized)
		return
	}
	request := r.Clone(r.Context())
	requestURL := *r.URL
	query := requestURL.Query()
	query.Set("userId", user.ID)
	requestURL.RawQuery = query.Encode()
	request.URL = &requestURL
	s.handleXScienceLinkWebSocket(w, request)
}

func isLoopbackRequest(r *http.Request) bool {
	host := strings.TrimSpace(r.RemoteAddr)
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	host = strings.Trim(host, "[]")
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
