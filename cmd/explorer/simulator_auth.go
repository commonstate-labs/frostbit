package main

import (
	"bytes"
	"container/list"
	"context"
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
	"net/url"
	"strings"
	"sync"
	"time"

	"dex/utils"
)

const (
	simulatorOperatorAddressesEnv = "EXPLORER_SIMULATOR_OPERATOR_ADDRESSES"
	simulatorPublicOriginEnv      = "EXPLORER_PUBLIC_ORIGIN"
	operatorNodeControlEnabledEnv = "EXPLORER_ENABLE_OPERATOR_NODE_CONTROL"

	simulatorOperatorMaxAddresses  = 64
	simulatorChallengeMaxEntries   = 1024
	simulatorSessionMaxEntries     = 512
	simulatorAuthJSONBodyLimit     = int64(4 << 10)
	simulatorChallengeTTL          = 2 * time.Minute
	simulatorSessionIdleTTL        = 30 * time.Minute
	simulatorSessionAbsoluteTTL    = 8 * time.Hour
	simulatorSessionCookieHTTPS    = "__Host-frostbit-simulator-session"
	simulatorSessionCookieLocalDev = "frostbit_simulator_session_dev"
	simulatorCSRFHeader            = "X-Explorer-CSRF-Token"
)

var (
	errSimulatorChallengeCapacity = errors.New("simulator authorization is busy")
	errSimulatorUnauthorized      = errors.New("Explorer operator authorization required")
)

type simulatorOperatorContextKey struct{}

type simulatorOperatorChallenge struct {
	id        string
	address   string
	origin    string
	clientKey string
	message   string
	issuedAt  time.Time
	expiresAt time.Time
	element   *list.Element
}

type simulatorOperatorSession struct {
	id                string
	address           string
	csrf              string
	createdAt         time.Time
	lastSeen          time.Time
	absoluteExpiresAt time.Time
	element           *list.Element
}

type simulatorOperatorAuth struct {
	mu sync.Mutex

	allowed      map[string]struct{}
	publicOrigin string
	secureCookie bool
	chainID      uint64
	nodeControl  bool
	clientIP     func(*http.Request) string
	now          func() time.Time

	challenges       map[string]*simulatorOperatorChallenge
	challengeSubject map[string]string
	challengeLRU     *list.List
	sessions         map[string]*simulatorOperatorSession
	sessionLRU       *list.List
}

type simulatorAuthResponse struct {
	Enabled            bool   `json:"enabled"`
	NodeControlEnabled bool   `json:"node_control_enabled"`
	Authorized         bool   `json:"authorized"`
	Address            string `json:"address,omitempty"`
	CSRFToken          string `json:"csrf_token,omitempty"`
	ExpiresAt          string `json:"expires_at,omitempty"`
	Error              string `json:"error,omitempty"`
}

func newSimulatorOperatorAuth(rawAddresses, rawOrigin string, chainID uint64, nodeControl bool, clientIP func(*http.Request) string) (*simulatorOperatorAuth, error) {
	allowed, err := parseSimulatorOperatorAddresses(rawAddresses)
	if err != nil {
		return nil, err
	}
	auth := &simulatorOperatorAuth{
		allowed:          allowed,
		chainID:          chainID,
		nodeControl:      nodeControl,
		clientIP:         clientIP,
		now:              time.Now,
		challenges:       make(map[string]*simulatorOperatorChallenge),
		challengeSubject: make(map[string]string),
		challengeLRU:     list.New(),
		sessions:         make(map[string]*simulatorOperatorSession),
		sessionLRU:       list.New(),
	}
	if len(allowed) == 0 {
		return auth, nil
	}
	auth.publicOrigin, auth.secureCookie, err = normalizeSimulatorPublicOrigin(rawOrigin)
	if err != nil {
		return nil, err
	}
	return auth, nil
}

func parseSimulatorOperatorAddresses(raw string) (map[string]struct{}, error) {
	allowed := make(map[string]struct{})
	for _, item := range strings.Split(raw, ",") {
		address := strings.TrimSpace(item)
		if address == "" {
			continue
		}
		if !utils.IsDexAddress(address) {
			return nil, fmt.Errorf("invalid simulator operator address %q", address)
		}
		allowed[address] = struct{}{}
		if len(allowed) > simulatorOperatorMaxAddresses {
			return nil, fmt.Errorf("simulator operator address count exceeds %d", simulatorOperatorMaxAddresses)
		}
	}
	return allowed, nil
}

func normalizeSimulatorPublicOrigin(raw string) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, fmt.Errorf("%s is required when simulator operators are enabled", simulatorPublicOriginEnv)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", false, fmt.Errorf("%s must be an exact origin", simulatorPublicOriginEnv)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = ""
	if parsed.Scheme == "https" {
		return parsed.Scheme + "://" + parsed.Host, true, nil
	}
	if parsed.Scheme != "http" || !simulatorLoopbackHost(parsed.Hostname()) {
		return "", false, fmt.Errorf("%s must use HTTPS; HTTP is allowed only for loopback", simulatorPublicOriginEnv)
	}
	return parsed.Scheme + "://" + parsed.Host, false, nil
}

func simulatorLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func (a *simulatorOperatorAuth) enabled() bool {
	return a != nil && len(a.allowed) > 0
}

func (a *simulatorOperatorAuth) nodeControlEnabled() bool {
	return a != nil && a.nodeControl && a.enabled()
}

func registerSimulatorOperatorRoutes(mux *http.ServeMux, srv *server, auth *simulatorOperatorAuth) {
	if mux == nil || auth == nil {
		return
	}
	mux.HandleFunc("/api/simulator/auth/status", auth.handleStatus)
	if !auth.enabled() {
		return
	}
	mux.HandleFunc("/api/simulator/auth/challenge", auth.handleChallenge)
	mux.HandleFunc("/api/simulator/auth/verify", auth.handleVerify)
	mux.HandleFunc("/api/simulator/auth/logout", auth.handleLogout)
	if srv != nil {
		mux.Handle("/api/simulator/config", auth.requireOperatorSession(http.HandlerFunc(srv.handleSimulatorConfig)))
		if auth.nodeControlEnabled() {
			mux.Handle("/api/operator/node/diagnostics", auth.requireOperatorSession(http.HandlerFunc(srv.handleNodeDiagnostics)))
			mux.Handle("/api/node/pause", auth.requireOperatorMutation(http.HandlerFunc(srv.handleOperatorNodePause)))
			mux.Handle("/api/debug/toggle_miner", auth.requireOperatorMutation(http.HandlerFunc(srv.handleMinerToggleProxy)))
		}
	}
}

func (a *simulatorOperatorAuth) handleStatus(w http.ResponseWriter, r *http.Request) {
	setSimulatorNoStore(w)
	if r.Method != http.MethodGet {
		writeSimulatorJSON(w, http.StatusMethodNotAllowed, simulatorAuthResponse{
			Enabled: a.enabled(), NodeControlEnabled: a.nodeControlEnabled(), Error: "method not allowed",
		})
		return
	}
	if !a.enabled() {
		writeSimulatorJSON(w, http.StatusOK, simulatorAuthResponse{Enabled: false, NodeControlEnabled: false})
		return
	}
	session, err := a.sessionForRequest(r, true)
	if err != nil {
		writeSimulatorJSON(w, http.StatusOK, simulatorAuthResponse{
			Enabled: true, NodeControlEnabled: a.nodeControlEnabled(),
		})
		return
	}
	writeSimulatorJSON(w, http.StatusOK, simulatorAuthResponse{
		Enabled: true, NodeControlEnabled: a.nodeControlEnabled(), Authorized: true, Address: session.address, CSRFToken: session.csrf,
		ExpiresAt: simulatorSessionEffectiveExpiry(session).UTC().Format(time.RFC3339),
	})
}

func (a *simulatorOperatorAuth) handleChallenge(w http.ResponseWriter, r *http.Request) {
	setSimulatorNoStore(w)
	if r.Method != http.MethodPost {
		writeSimulatorJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !a.validMutationOrigin(r) {
		writeSimulatorJSON(w, http.StatusForbidden, map[string]string{"error": "invalid origin"})
		return
	}
	var request struct {
		Address string `json:"address"`
	}
	if err := decodeSimulatorAuthJSON(r, &request); err != nil {
		writeSimulatorJSON(w, simulatorJSONErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	address := strings.TrimSpace(request.Address)
	if _, ok := a.allowed[address]; !ok || !utils.IsDexAddress(address) {
		writeSimulatorJSON(w, http.StatusForbidden, map[string]string{"error": "Explorer operator permission required"})
		return
	}
	now := a.now().UTC()
	id, err := simulatorRandomToken()
	if err != nil {
		writeSimulatorJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create challenge"})
		return
	}
	expiresAt := now.Add(simulatorChallengeTTL)
	message := simulatorChallengeMessage(a.publicOrigin, a.chainID, address, id, now, expiresAt, a.nodeControlEnabled())
	challenge := &simulatorOperatorChallenge{
		id: id, address: address, origin: a.publicOrigin, clientKey: a.requestClientKey(r), message: message,
		issuedAt: now, expiresAt: expiresAt,
	}
	if err := a.addChallenge(challenge, now); err != nil {
		writeSimulatorJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	writeSimulatorJSON(w, http.StatusOK, map[string]string{
		"challenge_id": id,
		"message":      message,
		"expires_at":   expiresAt.Format(time.RFC3339),
	})
}

func (a *simulatorOperatorAuth) handleVerify(w http.ResponseWriter, r *http.Request) {
	setSimulatorNoStore(w)
	if r.Method != http.MethodPost {
		writeSimulatorJSON(w, http.StatusMethodNotAllowed, simulatorAuthResponse{Enabled: true, NodeControlEnabled: a.nodeControlEnabled(), Error: "method not allowed"})
		return
	}
	if !a.validMutationOrigin(r) {
		writeSimulatorJSON(w, http.StatusForbidden, simulatorAuthResponse{Enabled: true, NodeControlEnabled: a.nodeControlEnabled(), Error: "invalid origin"})
		return
	}
	var request struct {
		ChallengeID string `json:"challenge_id"`
		Address     string `json:"address"`
		PublicKey   string `json:"public_key"`
		Signature   string `json:"signature"`
	}
	if err := decodeSimulatorAuthJSON(r, &request); err != nil {
		writeSimulatorJSON(w, simulatorJSONErrorStatus(err), simulatorAuthResponse{Enabled: true, NodeControlEnabled: a.nodeControlEnabled(), Error: err.Error()})
		return
	}
	request.ChallengeID = strings.TrimSpace(request.ChallengeID)
	request.Address = strings.TrimSpace(request.Address)
	challenge, err := a.takeChallenge(request.ChallengeID, request.Address, a.requestClientKey(r), a.now().UTC())
	if err != nil {
		writeSimulatorJSON(w, http.StatusUnauthorized, simulatorAuthResponse{Enabled: true, NodeControlEnabled: a.nodeControlEnabled(), Error: "invalid or expired challenge"})
		return
	}
	publicKey, err := decodeSimulatorBase64(request.PublicKey, 33)
	if err != nil {
		writeSimulatorJSON(w, http.StatusUnauthorized, simulatorAuthResponse{Enabled: true, NodeControlEnabled: a.nodeControlEnabled(), Error: "invalid authorization proof"})
		return
	}
	signature, err := decodeSimulatorBase64(request.Signature, 64)
	if err != nil {
		writeSimulatorJSON(w, http.StatusUnauthorized, simulatorAuthResponse{Enabled: true, NodeControlEnabled: a.nodeControlEnabled(), Error: "invalid authorization proof"})
		return
	}
	derivedAddress, err := utils.AddressFromSecp256k1PubKey(publicKey)
	if err != nil || derivedAddress != challenge.address {
		writeSimulatorJSON(w, http.StatusUnauthorized, simulatorAuthResponse{Enabled: true, NodeControlEnabled: a.nodeControlEnabled(), Error: "invalid authorization proof"})
		return
	}
	if _, ok := a.allowed[derivedAddress]; !ok {
		writeSimulatorJSON(w, http.StatusForbidden, simulatorAuthResponse{Enabled: true, NodeControlEnabled: a.nodeControlEnabled(), Error: "Explorer operator permission required"})
		return
	}
	digest := sha256.Sum256([]byte(challenge.message))
	if !utils.VerifyECDSASignature(publicKey, digest[:], signature) {
		writeSimulatorJSON(w, http.StatusUnauthorized, simulatorAuthResponse{Enabled: true, NodeControlEnabled: a.nodeControlEnabled(), Error: "invalid authorization proof"})
		return
	}
	session, err := a.createSession(derivedAddress, a.now().UTC())
	if err != nil {
		writeSimulatorJSON(w, http.StatusInternalServerError, simulatorAuthResponse{Enabled: true, NodeControlEnabled: a.nodeControlEnabled(), Error: "failed to create session"})
		return
	}
	a.setSessionCookie(w, session)
	writeSimulatorJSON(w, http.StatusOK, simulatorAuthResponse{
		Enabled: true, NodeControlEnabled: a.nodeControlEnabled(), Authorized: true, Address: session.address, CSRFToken: session.csrf,
		ExpiresAt: simulatorSessionEffectiveExpiry(session).UTC().Format(time.RFC3339),
	})
}

func (a *simulatorOperatorAuth) handleLogout(w http.ResponseWriter, r *http.Request) {
	setSimulatorNoStore(w)
	if r.Method != http.MethodPost {
		writeSimulatorJSON(w, http.StatusMethodNotAllowed, simulatorAuthResponse{Enabled: a.enabled(), NodeControlEnabled: a.nodeControlEnabled(), Error: "method not allowed"})
		return
	}
	if !a.validMutationOrigin(r) {
		writeSimulatorJSON(w, http.StatusForbidden, simulatorAuthResponse{Enabled: a.enabled(), NodeControlEnabled: a.nodeControlEnabled(), Error: "invalid origin"})
		return
	}
	session, err := a.sessionForRequest(r, false)
	if err != nil || !constantTimeStringEqual(r.Header.Get(simulatorCSRFHeader), session.csrf) {
		writeSimulatorJSON(w, http.StatusForbidden, simulatorAuthResponse{Enabled: a.enabled(), NodeControlEnabled: a.nodeControlEnabled(), Error: "invalid CSRF token"})
		return
	}
	a.deleteSession(session.id)
	a.clearSessionCookie(w)
	writeSimulatorJSON(w, http.StatusOK, simulatorAuthResponse{Enabled: a.enabled(), NodeControlEnabled: a.nodeControlEnabled()})
}

func (a *simulatorOperatorAuth) requireOperatorSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSimulatorNoStore(w)
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			writeSimulatorJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		session, err := a.sessionForRequest(r, true)
		if err != nil {
			writeSimulatorJSON(w, http.StatusUnauthorized, map[string]string{"error": errSimulatorUnauthorized.Error()})
			return
		}
		if r.Method == http.MethodPost {
			if !a.validMutationOrigin(r) {
				writeSimulatorJSON(w, http.StatusForbidden, map[string]string{"error": "invalid origin"})
				return
			}
			if !constantTimeStringEqual(r.Header.Get(simulatorCSRFHeader), session.csrf) {
				writeSimulatorJSON(w, http.StatusForbidden, map[string]string{"error": "invalid CSRF token"})
				return
			}
		}
		ctx := context.WithValue(r.Context(), simulatorOperatorContextKey{}, session.address)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *simulatorOperatorAuth) requireOperatorMutation(next http.Handler) http.Handler {
	protected := a.requireOperatorSession(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			setSimulatorNoStore(w)
			w.Header().Set("Allow", http.MethodPost)
			writeSimulatorJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		protected.ServeHTTP(w, r)
	})
}

func simulatorOperatorAddress(r *http.Request) string {
	if r == nil {
		return ""
	}
	address, _ := r.Context().Value(simulatorOperatorContextKey{}).(string)
	return address
}

func simulatorChallengeMessage(origin string, chainID uint64, address, challengeID string, issuedAt, expiresAt time.Time, nodeControl bool) string {
	title := "FrostBit Explorer Simulator Authorization"
	version := 1
	scope := "simulator:read simulator:write"
	if nodeControl {
		title = "FrostBit Explorer Operator Authorization"
		version = 3
		scope = "node:diagnostics:read node:pause node:miner simulator:read simulator:write"
	}
	return fmt.Sprintf("%s\nVersion: %d\nOrigin: %s\nChain-ID: %d\nAddress: %s\nScope: %s\nChallenge: %s\nIssued-At: %s\nExpires-At: %s",
		title, version, origin, chainID, address, scope, challengeID, issuedAt.UTC().Format(time.RFC3339), expiresAt.UTC().Format(time.RFC3339))
}

func (a *simulatorOperatorAuth) addChallenge(challenge *simulatorOperatorChallenge, now time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.purgeExpiredChallengesLocked(now)
	subject := simulatorChallengeSubject(challenge.clientKey, challenge.address)
	if oldID := a.challengeSubject[subject]; oldID != "" {
		a.removeChallengeLocked(oldID)
	}
	if len(a.challenges) >= simulatorChallengeMaxEntries {
		return errSimulatorChallengeCapacity
	}
	challenge.element = a.challengeLRU.PushBack(challenge.id)
	a.challenges[challenge.id] = challenge
	a.challengeSubject[subject] = challenge.id
	return nil
}

func (a *simulatorOperatorAuth) takeChallenge(id, address, clientKey string, now time.Time) (*simulatorOperatorChallenge, error) {
	a.mu.Lock()
	challenge := a.challenges[id]
	if challenge != nil {
		a.removeChallengeLocked(id)
	}
	a.mu.Unlock()
	if challenge == nil || !now.Before(challenge.expiresAt) || challenge.address != address || challenge.clientKey != clientKey || challenge.origin != a.publicOrigin {
		return nil, errSimulatorUnauthorized
	}
	return challenge, nil
}

func (a *simulatorOperatorAuth) purgeExpiredChallengesLocked(now time.Time) {
	for {
		front := a.challengeLRU.Front()
		if front == nil {
			return
		}
		id, _ := front.Value.(string)
		challenge := a.challenges[id]
		if challenge != nil && now.Before(challenge.expiresAt) {
			return
		}
		a.removeChallengeLocked(id)
	}
}

func (a *simulatorOperatorAuth) removeChallengeLocked(id string) {
	challenge := a.challenges[id]
	if challenge == nil {
		return
	}
	delete(a.challenges, id)
	if challenge.element != nil {
		a.challengeLRU.Remove(challenge.element)
	}
	subject := simulatorChallengeSubject(challenge.clientKey, challenge.address)
	if a.challengeSubject[subject] == id {
		delete(a.challengeSubject, subject)
	}
}

func simulatorChallengeSubject(clientKey, address string) string {
	return clientKey + "\x00" + address
}

func (a *simulatorOperatorAuth) createSession(address string, now time.Time) (*simulatorOperatorSession, error) {
	id, err := simulatorRandomToken()
	if err != nil {
		return nil, err
	}
	csrf, err := simulatorRandomToken()
	if err != nil {
		return nil, err
	}
	session := &simulatorOperatorSession{
		id: id, address: address, csrf: csrf, createdAt: now, lastSeen: now,
		absoluteExpiresAt: now.Add(simulatorSessionAbsoluteTTL),
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.purgeExpiredSessionsLocked(now)
	for len(a.sessions) >= simulatorSessionMaxEntries {
		front := a.sessionLRU.Front()
		if front == nil {
			break
		}
		oldID, _ := front.Value.(string)
		a.removeSessionLocked(oldID)
	}
	session.element = a.sessionLRU.PushBack(session.id)
	a.sessions[session.id] = session
	return cloneSimulatorSession(session), nil
}

func (a *simulatorOperatorAuth) sessionForRequest(r *http.Request, touch bool) (*simulatorOperatorSession, error) {
	cookie, err := r.Cookie(a.cookieName())
	if err != nil || cookie.Value == "" {
		return nil, errSimulatorUnauthorized
	}
	now := a.now().UTC()
	a.mu.Lock()
	defer a.mu.Unlock()
	session := a.sessions[cookie.Value]
	if session == nil || !now.Before(session.absoluteExpiresAt) || now.Sub(session.lastSeen) >= simulatorSessionIdleTTL {
		if session != nil {
			a.removeSessionLocked(session.id)
		}
		return nil, errSimulatorUnauthorized
	}
	if _, ok := a.allowed[session.address]; !ok {
		a.removeSessionLocked(session.id)
		return nil, errSimulatorUnauthorized
	}
	if touch {
		session.lastSeen = now
		a.sessionLRU.MoveToBack(session.element)
	}
	return cloneSimulatorSession(session), nil
}

func (a *simulatorOperatorAuth) deleteSession(id string) {
	a.mu.Lock()
	a.removeSessionLocked(id)
	a.mu.Unlock()
}

func (a *simulatorOperatorAuth) purgeExpiredSessionsLocked(now time.Time) {
	for {
		front := a.sessionLRU.Front()
		if front == nil {
			return
		}
		id, _ := front.Value.(string)
		session := a.sessions[id]
		if session != nil && now.Before(session.absoluteExpiresAt) && now.Sub(session.lastSeen) < simulatorSessionIdleTTL {
			return
		}
		a.removeSessionLocked(id)
	}
}

func (a *simulatorOperatorAuth) removeSessionLocked(id string) {
	session := a.sessions[id]
	if session == nil {
		return
	}
	delete(a.sessions, id)
	if session.element != nil {
		a.sessionLRU.Remove(session.element)
	}
}

func cloneSimulatorSession(session *simulatorOperatorSession) *simulatorOperatorSession {
	if session == nil {
		return nil
	}
	clone := *session
	clone.element = nil
	return &clone
}

func simulatorSessionEffectiveExpiry(session *simulatorOperatorSession) time.Time {
	if session == nil {
		return time.Time{}
	}
	idleExpiry := session.lastSeen.Add(simulatorSessionIdleTTL)
	if idleExpiry.Before(session.absoluteExpiresAt) {
		return idleExpiry
	}
	return session.absoluteExpiresAt
}

func (a *simulatorOperatorAuth) setSessionCookie(w http.ResponseWriter, session *simulatorOperatorSession) {
	maxAge := int(time.Until(session.absoluteExpiresAt).Seconds())
	if a.now != nil {
		maxAge = int(session.absoluteExpiresAt.Sub(a.now().UTC()).Seconds())
	}
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name: a.cookieName(), Value: session.id, Path: "/", HttpOnly: true, Secure: a.secureCookie,
		SameSite: http.SameSiteStrictMode, Expires: session.absoluteExpiresAt, MaxAge: maxAge,
	})
}

func (a *simulatorOperatorAuth) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: a.cookieName(), Value: "", Path: "/", HttpOnly: true, Secure: a.secureCookie,
		SameSite: http.SameSiteStrictMode, Expires: time.Unix(1, 0), MaxAge: -1,
	})
}

func (a *simulatorOperatorAuth) cookieName() string {
	if a.secureCookie {
		return simulatorSessionCookieHTTPS
	}
	return simulatorSessionCookieLocalDev
}

func (a *simulatorOperatorAuth) validMutationOrigin(r *http.Request) bool {
	return r != nil && r.Header.Get("Origin") == a.publicOrigin
}

func (a *simulatorOperatorAuth) requestClientKey(r *http.Request) string {
	if a.clientIP != nil {
		return a.clientIP(r)
	}
	if r == nil {
		return ""
	}
	return explorerClientIP(r.RemoteAddr)
}

func simulatorRandomToken() (string, error) {
	value := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func decodeSimulatorBase64(raw string, expectedLength int) ([]byte, error) {
	if len(raw) > base64.StdEncoding.EncodedLen(expectedLength)+2 {
		return nil, errors.New("encoded value too long")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(raw)
	if err != nil || len(decoded) != expectedLength {
		return nil, errors.New("invalid encoded value")
	}
	return decoded, nil
}

var errSimulatorJSONTooLarge = errors.New("request body exceeds simulator authorization limit")

func decodeSimulatorAuthJSON(r *http.Request, destination any) error {
	if r == nil || r.Body == nil {
		return errors.New("request body is required")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, simulatorAuthJSONBodyLimit+1))
	if err != nil {
		return errors.New("failed to read request body")
	}
	if int64(len(body)) > simulatorAuthJSONBodyLimit {
		return errSimulatorJSONTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("invalid JSON body")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("invalid JSON body")
	}
	return nil
}

func simulatorJSONErrorStatus(err error) int {
	if errors.Is(err, errSimulatorJSONTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func setSimulatorNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

func simulatorNoStoreHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSimulatorNoStore(w)
		next.ServeHTTP(w, r)
	})
}

func writeSimulatorJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func constantTimeStringEqual(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
