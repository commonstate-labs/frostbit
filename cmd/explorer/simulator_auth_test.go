package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dex/utils"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

func TestSimulatorOperatorAuthLoginReplayAndCSRF(t *testing.T) {
	privateKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	address, err := utils.DeriveDexAddress(privateKey)
	if err != nil {
		t.Fatalf("derive address: %v", err)
	}
	fixedNow := time.Date(2026, time.August, 8, 10, 0, 0, 0, time.UTC)
	auth, err := newSimulatorOperatorAuth(address, "http://127.0.0.1:8080", 7, false, func(r *http.Request) string {
		return explorerClientIP(r.RemoteAddr)
	})
	if err != nil {
		t.Fatalf("new auth: %v", err)
	}
	auth.now = func() time.Time { return fixedNow }

	challengeBody := []byte(fmt.Sprintf(`{"address":%q}`, address))
	challengeRequest := httptest.NewRequest(http.MethodPost, "/api/simulator/auth/challenge", bytes.NewReader(challengeBody))
	challengeRequest.RemoteAddr = "127.0.0.1:32000"
	challengeRequest.Header.Set("Origin", "http://127.0.0.1:8080")
	challengeRecorder := httptest.NewRecorder()
	auth.handleChallenge(challengeRecorder, challengeRequest)
	if challengeRecorder.Code != http.StatusOK {
		t.Fatalf("challenge status=%d body=%s", challengeRecorder.Code, challengeRecorder.Body.String())
	}
	var challenge struct {
		ID      string `json:"challenge_id"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(challengeRecorder.Body.Bytes(), &challenge); err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	if challenge.ID == "" || !strings.Contains(challenge.Message, "Scope: simulator:read simulator:write") || strings.Contains(challenge.Message, "node:pause") || !strings.Contains(challenge.Message, address) {
		t.Fatalf("unexpected challenge: %+v", challenge)
	}

	digest := sha256.Sum256([]byte(challenge.Message))
	r, s, err := ecdsa.Sign(rand.Reader, privateKey.ToECDSA(), digest[:])
	if err != nil {
		t.Fatalf("sign challenge: %v", err)
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	verifyPayload := map[string]string{
		"challenge_id": challenge.ID,
		"address":      address,
		"public_key":   base64.StdEncoding.EncodeToString(privateKey.PubKey().SerializeCompressed()),
		"signature":    base64.StdEncoding.EncodeToString(signature),
	}
	verifyBody, _ := json.Marshal(verifyPayload)
	verifyRequest := httptest.NewRequest(http.MethodPost, "/api/simulator/auth/verify", bytes.NewReader(verifyBody))
	verifyRequest.RemoteAddr = "127.0.0.1:32001"
	verifyRequest.Header.Set("Origin", "http://127.0.0.1:8080")
	verifyRecorder := httptest.NewRecorder()
	auth.handleVerify(verifyRecorder, verifyRequest)
	if verifyRecorder.Code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", verifyRecorder.Code, verifyRecorder.Body.String())
	}
	var login simulatorAuthResponse
	if err := json.Unmarshal(verifyRecorder.Body.Bytes(), &login); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if !login.Authorized || login.Address != address || login.CSRFToken == "" {
		t.Fatalf("unexpected login response: %+v", login)
	}
	if login.ExpiresAt != fixedNow.Add(simulatorSessionIdleTTL).Format(time.RFC3339) {
		t.Fatalf("login expires_at=%q, want effective idle deadline", login.ExpiresAt)
	}
	cookies := verifyRecorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != simulatorSessionCookieLocalDev || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Secure {
		t.Fatalf("unexpected local session cookie: %+v", cookie)
	}

	                                                                            
	                                                   
	replayRequest := httptest.NewRequest(http.MethodPost, "/api/simulator/auth/verify", bytes.NewReader(verifyBody))
	replayRequest.RemoteAddr = "127.0.0.1:32002"
	replayRequest.Header.Set("Origin", "http://127.0.0.1:8080")
	replayRecorder := httptest.NewRecorder()
	auth.handleVerify(replayRecorder, replayRequest)
	if replayRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("replay status=%d, want 401", replayRecorder.Code)
	}

	called := 0
	protected := auth.requireOperatorSession(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}))
	getRequest := httptest.NewRequest(http.MethodGet, "/api/simulator/config", nil)
	getRequest.AddCookie(cookie)
	getRecorder := httptest.NewRecorder()
	protected.ServeHTTP(getRecorder, getRequest)
	if getRecorder.Code != http.StatusNoContent || called != 1 || getRecorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("authorized GET status=%d called=%d cache=%q", getRecorder.Code, called, getRecorder.Header().Get("Cache-Control"))
	}

	postWithoutCSRF := httptest.NewRequest(http.MethodPost, "/api/simulator/config", strings.NewReader(`{}`))
	postWithoutCSRF.AddCookie(cookie)
	postWithoutCSRF.Header.Set("Origin", "http://127.0.0.1:8080")
	postWithoutCSRFRecorder := httptest.NewRecorder()
	protected.ServeHTTP(postWithoutCSRFRecorder, postWithoutCSRF)
	if postWithoutCSRFRecorder.Code != http.StatusForbidden || called != 1 {
		t.Fatalf("POST without CSRF status=%d called=%d", postWithoutCSRFRecorder.Code, called)
	}
	postWrongOrigin := httptest.NewRequest(http.MethodPost, "/api/simulator/config", strings.NewReader(`{}`))
	postWrongOrigin.AddCookie(cookie)
	postWrongOrigin.Header.Set("Origin", "https://attacker.example")
	postWrongOrigin.Header.Set(simulatorCSRFHeader, login.CSRFToken)
	postWrongOriginRecorder := httptest.NewRecorder()
	protected.ServeHTTP(postWrongOriginRecorder, postWrongOrigin)
	if postWrongOriginRecorder.Code != http.StatusForbidden || called != 1 {
		t.Fatalf("POST with wrong origin status=%d called=%d", postWrongOriginRecorder.Code, called)
	}

	postRequest := httptest.NewRequest(http.MethodPost, "/api/simulator/config", strings.NewReader(`{}`))
	postRequest.AddCookie(cookie)
	postRequest.Header.Set("Origin", "http://127.0.0.1:8080")
	postRequest.Header.Set(simulatorCSRFHeader, login.CSRFToken)
	postRecorder := httptest.NewRecorder()
	protected.ServeHTTP(postRecorder, postRequest)
	if postRecorder.Code != http.StatusNoContent || called != 2 {
		t.Fatalf("authorized POST status=%d called=%d", postRecorder.Code, called)
	}

	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/simulator/auth/logout", nil)
	logoutRequest.AddCookie(cookie)
	logoutRequest.Header.Set("Origin", "http://127.0.0.1:8080")
	logoutRequest.Header.Set(simulatorCSRFHeader, login.CSRFToken)
	logoutRecorder := httptest.NewRecorder()
	auth.handleLogout(logoutRecorder, logoutRequest)
	if logoutRecorder.Code != http.StatusOK || logoutRecorder.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("logout status=%d cookies=%+v", logoutRecorder.Code, logoutRecorder.Result().Cookies())
	}
	afterLogout := httptest.NewRecorder()
	afterLogoutRequest := httptest.NewRequest(http.MethodGet, "/api/simulator/config", nil)
	afterLogoutRequest.AddCookie(cookie)
	protected.ServeHTTP(afterLogout, afterLogoutRequest)
	if afterLogout.Code != http.StatusUnauthorized || called != 2 {
		t.Fatalf("after logout status=%d called=%d", afterLogout.Code, called)
	}
}

func TestSimulatorOperatorAcceptsFrostBitWalletSignatureVector(t *testing.T) {
	const message = "FrostBit Explorer Simulator Authorization\n" +
		"Version: 1\n" +
		"Origin: https://explorer.example\n" +
		"Chain-ID: 7\n" +
		"Address: fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8\n" +
		"Scope: simulator:read simulator:write\n" +
		"Challenge: vector\n" +
		"Issued-At: 2026-08-08T00:00:00Z\n" +
		"Expires-At: 2026-08-08T00:02:00Z"
	const publicKeyBase64 = "Anm+Zn753LusVaBilc6HCwcCm/zbLc4o2VnygVsW+BeY"
	const signatureBase64 = "iFo5803WNBiFsUgAcFDsqDBpvQUzoc25ymEUERtA4dFiNulfHtdVSB7FXibhPxMB84ZairnHbcNApdMR9CnJMg=="

	publicKey, err := decodeSimulatorBase64(publicKeyBase64, 33)
	if err != nil {
		t.Fatalf("decode wallet public key: %v", err)
	}
	signature, err := decodeSimulatorBase64(signatureBase64, 64)
	if err != nil {
		t.Fatalf("decode wallet signature: %v", err)
	}
	address, err := utils.AddressFromSecp256k1PubKey(publicKey)
	if err != nil {
		t.Fatalf("derive wallet address: %v", err)
	}
	if address != "fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8" {
		t.Fatalf("wallet address=%q", address)
	}
	digest := sha256.Sum256([]byte(message))
	if !utils.VerifyECDSASignature(publicKey, digest[:], signature) {
		t.Fatal("Go verifier rejected the FrostBit Wallet compact signature vector")
	}
}

func TestSimulatorOperatorConfigurationIsStrictAndBounded(t *testing.T) {
	privateKey := secp256k1.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
	address, _ := utils.DeriveDexAddress(privateKey)
	if _, err := newSimulatorOperatorAuth(address, "", 1, false, nil); err == nil || !strings.Contains(err.Error(), simulatorPublicOriginEnv) {
		t.Fatalf("missing public origin error=%v", err)
	}
	if _, err := newSimulatorOperatorAuth(address, "http://example.com", 1, false, nil); err == nil {
		t.Fatal("non-loopback HTTP origin was accepted")
	}
	if _, err := newSimulatorOperatorAuth(address, "https://example.com/path", 1, false, nil); err == nil {
		t.Fatal("origin with a path was accepted")
	}
	if _, err := parseSimulatorOperatorAddresses("not-an-address"); err == nil {
		t.Fatal("invalid operator address was accepted")
	}

	addresses := make([]string, 0, simulatorOperatorMaxAddresses+1)
	for i := 1; i <= simulatorOperatorMaxAddresses+1; i++ {
		raw := make([]byte, 32)
		raw[30] = byte(i >> 8)
		raw[31] = byte(i)
		candidate, _ := utils.DeriveDexAddress(secp256k1.PrivKeyFromBytes(raw))
		addresses = append(addresses, candidate)
	}
	if _, err := parseSimulatorOperatorAddresses(strings.Join(addresses, ",")); err == nil {
		t.Fatalf("more than %d addresses were accepted", simulatorOperatorMaxAddresses)
	}
}

func TestSimulatorOperatorAuthRejectsBadOriginAllowlistProofAndJSON(t *testing.T) {
	allowedKey := secp256k1.PrivKeyFromBytes(bytes.Repeat([]byte{6}, 32))
	allowedAddress, _ := utils.DeriveDexAddress(allowedKey)
	otherKey := secp256k1.PrivKeyFromBytes(bytes.Repeat([]byte{7}, 32))
	otherAddress, _ := utils.DeriveDexAddress(otherKey)
	now := time.Date(2026, time.August, 8, 10, 30, 0, 0, time.UTC)
	auth, err := newSimulatorOperatorAuth(allowedAddress, "https://explorer.example", 9, false, nil)
	if err != nil {
		t.Fatalf("new auth: %v", err)
	}
	auth.now = func() time.Time { return now }

	requestChallenge := func(origin, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/simulator/auth/challenge", strings.NewReader(body))
		req.RemoteAddr = "192.0.2.9:4000"
		req.Header.Set("Origin", origin)
		recorder := httptest.NewRecorder()
		auth.handleChallenge(recorder, req)
		return recorder
	}
	if recorder := requestChallenge("https://attacker.example", fmt.Sprintf(`{"address":%q}`, allowedAddress)); recorder.Code != http.StatusForbidden {
		t.Fatalf("wrong-origin challenge status=%d", recorder.Code)
	}
	if recorder := requestChallenge(auth.publicOrigin, fmt.Sprintf(`{"address":%q}`, otherAddress)); recorder.Code != http.StatusForbidden {
		t.Fatalf("non-allowlisted challenge status=%d", recorder.Code)
	}
	if recorder := requestChallenge(auth.publicOrigin, fmt.Sprintf(`{"address":%q,"unknown":true}`, allowedAddress)); recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown JSON field status=%d", recorder.Code)
	}
	if recorder := requestChallenge(auth.publicOrigin, strings.Repeat("x", int(simulatorAuthJSONBodyLimit)+1)); recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized JSON status=%d", recorder.Code)
	}

	challengeRecorder := requestChallenge(auth.publicOrigin, fmt.Sprintf(`{"address":%q}`, allowedAddress))
	if challengeRecorder.Code != http.StatusOK {
		t.Fatalf("valid challenge status=%d body=%s", challengeRecorder.Code, challengeRecorder.Body.String())
	}
	var challenge struct {
		ID      string `json:"challenge_id"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(challengeRecorder.Body.Bytes(), &challenge); err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	digest := sha256.Sum256([]byte(challenge.Message))
	r, s, err := ecdsa.Sign(rand.Reader, otherKey.ToECDSA(), digest[:])
	if err != nil {
		t.Fatalf("sign forged proof: %v", err)
	}
	forgedSignature := make([]byte, 64)
	r.FillBytes(forgedSignature[:32])
	s.FillBytes(forgedSignature[32:])
	forgedBody, _ := json.Marshal(map[string]string{
		"challenge_id": challenge.ID,
		"address":      allowedAddress,
		"public_key":   base64.StdEncoding.EncodeToString(otherKey.PubKey().SerializeCompressed()),
		"signature":    base64.StdEncoding.EncodeToString(forgedSignature),
	})
	verify := func(body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/simulator/auth/verify", bytes.NewReader(body))
		req.RemoteAddr = "192.0.2.9:4001"
		req.Header.Set("Origin", auth.publicOrigin)
		recorder := httptest.NewRecorder()
		auth.handleVerify(recorder, req)
		return recorder
	}
	if recorder := verify(forgedBody); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("forged proof status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	                                                                           
	                        
	r, s, err = ecdsa.Sign(rand.Reader, allowedKey.ToECDSA(), digest[:])
	if err != nil {
		t.Fatalf("sign correct proof: %v", err)
	}
	validSignature := make([]byte, 64)
	r.FillBytes(validSignature[:32])
	s.FillBytes(validSignature[32:])
	validBody, _ := json.Marshal(map[string]string{
		"challenge_id": challenge.ID,
		"address":      allowedAddress,
		"public_key":   base64.StdEncoding.EncodeToString(allowedKey.PubKey().SerializeCompressed()),
		"signature":    base64.StdEncoding.EncodeToString(validSignature),
	})
	if recorder := verify(validBody); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("consumed challenge reuse status=%d", recorder.Code)
	}
}

func TestSimulatorOperatorChallengeAndSessionStoresStayBounded(t *testing.T) {
	privateKey := secp256k1.PrivKeyFromBytes(bytes.Repeat([]byte{2}, 32))
	address, _ := utils.DeriveDexAddress(privateKey)
	now := time.Date(2026, time.August, 8, 11, 0, 0, 0, time.UTC)
	auth, err := newSimulatorOperatorAuth(address, "https://explorer.example", 1, false, nil)
	if err != nil {
		t.Fatalf("new auth: %v", err)
	}
	auth.now = func() time.Time { return now }
	first := &simulatorOperatorChallenge{
		id: "first", address: address, origin: auth.publicOrigin, clientKey: "same-client",
		message: "first", issuedAt: now, expiresAt: now.Add(simulatorChallengeTTL),
	}
	second := &simulatorOperatorChallenge{
		id: "second", address: address, origin: auth.publicOrigin, clientKey: "same-client",
		message: "second", issuedAt: now, expiresAt: now.Add(simulatorChallengeTTL),
	}
	if err := auth.addChallenge(first, now); err != nil {
		t.Fatalf("add first challenge: %v", err)
	}
	if err := auth.addChallenge(second, now); err != nil {
		t.Fatalf("replace challenge: %v", err)
	}
	auth.mu.Lock()
	if _, exists := auth.challenges[first.id]; exists || len(auth.challenges) != 1 {
		auth.mu.Unlock()
		t.Fatal("same (client,address) retained more than its latest challenge")
	}
	auth.removeChallengeLocked(second.id)
	auth.mu.Unlock()

	for i := 0; i < simulatorChallengeMaxEntries; i++ {
		challenge := &simulatorOperatorChallenge{
			id: fmt.Sprintf("challenge-%d", i), address: address, origin: auth.publicOrigin,
			clientKey: fmt.Sprintf("client-%d", i), message: "message", issuedAt: now, expiresAt: now.Add(simulatorChallengeTTL),
		}
		if err := auth.addChallenge(challenge, now); err != nil {
			t.Fatalf("add challenge %d: %v", i, err)
		}
	}
	if err := auth.addChallenge(&simulatorOperatorChallenge{
		id: "overflow", address: address, origin: auth.publicOrigin, clientKey: "overflow",
		message: "message", issuedAt: now, expiresAt: now.Add(simulatorChallengeTTL),
	}, now); !errors.Is(err, errSimulatorChallengeCapacity) {
		t.Fatalf("overflow error=%v, want capacity", err)
	}
	auth.mu.Lock()
	if len(auth.challenges) != simulatorChallengeMaxEntries || auth.challengeLRU.Len() != simulatorChallengeMaxEntries {
		t.Fatalf("challenge map/list=%d/%d", len(auth.challenges), auth.challengeLRU.Len())
	}
	auth.mu.Unlock()

	var firstSessionID string
	for i := 0; i < simulatorSessionMaxEntries+1; i++ {
		session, err := auth.createSession(address, now.Add(time.Duration(i)*time.Millisecond))
		if err != nil {
			t.Fatalf("create session %d: %v", i, err)
		}
		if i == 0 {
			firstSessionID = session.id
		}
	}
	auth.mu.Lock()
	defer auth.mu.Unlock()
	if len(auth.sessions) != simulatorSessionMaxEntries || auth.sessionLRU.Len() != simulatorSessionMaxEntries {
		t.Fatalf("session map/list=%d/%d", len(auth.sessions), auth.sessionLRU.Len())
	}
	if _, exists := auth.sessions[firstSessionID]; exists {
		t.Fatal("oldest session was not evicted")
	}
}

func TestSimulatorOperatorHTTPSCookieAndDisabledStatus(t *testing.T) {
	privateKey := secp256k1.PrivKeyFromBytes(bytes.Repeat([]byte{3}, 32))
	address, _ := utils.DeriveDexAddress(privateKey)
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	auth, err := newSimulatorOperatorAuth(address, "https://explorer.example", 1, false, nil)
	if err != nil {
		t.Fatalf("new auth: %v", err)
	}
	auth.now = func() time.Time { return now }
	session, err := auth.createSession(address, now)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	recorder := httptest.NewRecorder()
	auth.setSessionCookie(recorder, session)
	cookie := recorder.Result().Cookies()[0]
	if cookie.Name != simulatorSessionCookieHTTPS || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("unexpected HTTPS cookie: %+v", cookie)
	}

	disabled, err := newSimulatorOperatorAuth("", "", 1, false, nil)
	if err != nil {
		t.Fatalf("disabled auth: %v", err)
	}
	statusRecorder := httptest.NewRecorder()
	disabled.handleStatus(statusRecorder, httptest.NewRequest(http.MethodGet, "/api/simulator/auth/status", nil))
	if statusRecorder.Code != http.StatusOK || !strings.Contains(statusRecorder.Body.String(), `"enabled":false`) || !strings.Contains(statusRecorder.Body.String(), `"node_control_enabled":false`) {
		t.Fatalf("disabled status=%d body=%s", statusRecorder.Code, statusRecorder.Body.String())
	}
}

func TestSimulatorOperatorRoutesOnlyExposeScopedConfigWhenEnabled(t *testing.T) {
	privateKey := secp256k1.PrivKeyFromBytes(bytes.Repeat([]byte{4}, 32))
	address, _ := utils.DeriveDexAddress(privateKey)
	auth, err := newSimulatorOperatorAuth(address, "https://explorer.example", 1, false, nil)
	if err != nil {
		t.Fatalf("new auth: %v", err)
	}
	mux := http.NewServeMux()
	registerSimulatorOperatorRoutes(mux, &server{}, auth)
	for _, path := range []string{
		"/api/simulator/auth/status",
		"/api/simulator/auth/challenge",
		"/api/simulator/auth/verify",
		"/api/simulator/auth/logout",
		"/api/simulator/config",
	} {
		if _, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, path, nil)); pattern != path {
			t.Fatalf("route %s registered as %q", path, pattern)
		}
	}
	unauthorized := httptest.NewRecorder()
	mux.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/simulator/config", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("public config without session status=%d, want 401", unauthorized.Code)
	}
	if _, pattern := mux.Handler(httptest.NewRequest(http.MethodPost, "/api/node/pause", nil)); pattern != "" {
		t.Fatalf("node control without opt-in unexpectedly registered as %q", pattern)
	}
	if _, pattern := mux.Handler(httptest.NewRequest(http.MethodPost, "/api/debug/toggle_miner", nil)); pattern != "" {
		t.Fatalf("miner control without opt-in unexpectedly registered as %q", pattern)
	}
	if _, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, "/api/operator/node/diagnostics", nil)); pattern != "" {
		t.Fatalf("diagnostics without node-control opt-in unexpectedly registered as %q", pattern)
	}

	nodeControlAuth, err := newSimulatorOperatorAuth(address, "https://explorer.example", 1, true, nil)
	if err != nil {
		t.Fatalf("new node-control auth: %v", err)
	}
	nodeControlMux := http.NewServeMux()
	registerSimulatorOperatorRoutes(nodeControlMux, &server{}, nodeControlAuth)
	if _, pattern := nodeControlMux.Handler(httptest.NewRequest(http.MethodPost, "/api/node/pause", nil)); pattern != "/api/node/pause" {
		t.Fatalf("opted-in public pause registered as %q", pattern)
	}
	if _, pattern := nodeControlMux.Handler(httptest.NewRequest(http.MethodPost, "/api/debug/toggle_miner", nil)); pattern != "/api/debug/toggle_miner" {
		t.Fatalf("opted-in public miner control registered as %q", pattern)
	}
	if _, pattern := nodeControlMux.Handler(httptest.NewRequest(http.MethodGet, "/api/operator/node/diagnostics", nil)); pattern != "/api/operator/node/diagnostics" {
		t.Fatalf("opted-in diagnostics registered as %q", pattern)
	}
	statusRecorder := httptest.NewRecorder()
	nodeControlMux.ServeHTTP(statusRecorder, httptest.NewRequest(http.MethodGet, "/api/simulator/auth/status", nil))
	var status simulatorAuthResponse
	if err := json.Unmarshal(statusRecorder.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode node-control status: %v", err)
	}
	if !status.Enabled || !status.NodeControlEnabled {
		t.Fatalf("node-control status=%+v", status)
	}

	disabled, err := newSimulatorOperatorAuth("", "", 1, true, nil)
	if err != nil {
		t.Fatalf("disabled auth: %v", err)
	}
	disabledMux := http.NewServeMux()
	registerSimulatorOperatorRoutes(disabledMux, &server{}, disabled)
	if _, pattern := disabledMux.Handler(httptest.NewRequest(http.MethodGet, "/api/simulator/auth/status", nil)); pattern == "" {
		t.Fatal("disabled status route is missing")
	}
	if _, pattern := disabledMux.Handler(httptest.NewRequest(http.MethodGet, "/api/simulator/config", nil)); pattern != "" {
		t.Fatalf("disabled public config unexpectedly registered as %q", pattern)
	}
	if _, pattern := disabledMux.Handler(httptest.NewRequest(http.MethodPost, "/api/node/pause", nil)); pattern != "" {
		t.Fatalf("disabled public pause unexpectedly registered as %q", pattern)
	}
	if _, pattern := disabledMux.Handler(httptest.NewRequest(http.MethodPost, "/api/debug/toggle_miner", nil)); pattern != "" {
		t.Fatalf("disabled public miner control unexpectedly registered as %q", pattern)
	}
	if _, pattern := disabledMux.Handler(httptest.NewRequest(http.MethodGet, "/api/operator/node/diagnostics", nil)); pattern != "" {
		t.Fatalf("disabled diagnostics unexpectedly registered as %q", pattern)
	}
}

func TestSimulatorOperatorNodeControlOptInChangesChallengeScope(t *testing.T) {
	issuedAt := time.Date(2026, time.August, 11, 15, 0, 0, 0, time.UTC)
	expiresAt := issuedAt.Add(simulatorChallengeTTL)
	withoutNodeControl := simulatorChallengeMessage("https://explorer.example", 7, "fbc1operator", "challenge-1", issuedAt, expiresAt, false)
	if !strings.Contains(withoutNodeControl, "FrostBit Explorer Simulator Authorization\nVersion: 1") ||
		!strings.Contains(withoutNodeControl, "Scope: simulator:read simulator:write") || strings.Contains(withoutNodeControl, "node:pause") {
		t.Fatalf("unexpected simulator-only challenge: %q", withoutNodeControl)
	}
	withNodeControl := simulatorChallengeMessage("https://explorer.example", 7, "fbc1operator", "challenge-2", issuedAt, expiresAt, true)
	if !strings.Contains(withNodeControl, "FrostBit Explorer Operator Authorization\nVersion: 3") ||
		!strings.Contains(withNodeControl, "Scope: node:diagnostics:read node:pause node:miner simulator:read simulator:write") {
		t.Fatalf("unexpected node-control challenge: %q", withNodeControl)
	}
}

func TestSimulatorOperatorSessionIdleAndAbsoluteExpiry(t *testing.T) {
	privateKey := secp256k1.PrivKeyFromBytes(bytes.Repeat([]byte{5}, 32))
	address, _ := utils.DeriveDexAddress(privateKey)
	current := time.Date(2026, time.August, 8, 13, 0, 0, 0, time.UTC)
	auth, err := newSimulatorOperatorAuth(address, "https://explorer.example", 1, false, nil)
	if err != nil {
		t.Fatalf("new auth: %v", err)
	}
	auth.now = func() time.Time { return current }

	idleSession, err := auth.createSession(address, current)
	if err != nil {
		t.Fatalf("create idle session: %v", err)
	}
	idleRequest := httptest.NewRequest(http.MethodGet, "/api/simulator/config", nil)
	idleRequest.AddCookie(&http.Cookie{Name: auth.cookieName(), Value: idleSession.id})
	current = current.Add(simulatorSessionIdleTTL)
	if _, err := auth.sessionForRequest(idleRequest, true); !errors.Is(err, errSimulatorUnauthorized) {
		t.Fatalf("idle expiry error=%v, want unauthorized", err)
	}

	current = time.Date(2026, time.August, 8, 14, 0, 0, 0, time.UTC)
	absoluteSession, err := auth.createSession(address, current)
	if err != nil {
		t.Fatalf("create absolute session: %v", err)
	}
	absoluteRequest := httptest.NewRequest(http.MethodGet, "/api/simulator/config", nil)
	absoluteRequest.AddCookie(&http.Cookie{Name: auth.cookieName(), Value: absoluteSession.id})
	current = absoluteSession.absoluteExpiresAt.Add(-time.Minute)
	auth.mu.Lock()
	auth.sessions[absoluteSession.id].lastSeen = current
	auth.mu.Unlock()
	if _, err := auth.sessionForRequest(absoluteRequest, true); err != nil {
		t.Fatalf("touch before absolute expiry: %v", err)
	}
	current = absoluteSession.absoluteExpiresAt
	if _, err := auth.sessionForRequest(absoluteRequest, true); !errors.Is(err, errSimulatorUnauthorized) {
		t.Fatalf("absolute expiry error=%v, want unauthorized", err)
	}
}
