package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	bridgeasset "dex/frost/asset"
	"dex/pb"
)

const (
	tokenIconSourceHost     = "assets-cdn.trustwallet.com"
	tokenIconMaxSourceBytes = int64(256 << 10)
	tokenIconMaxSide        = 512
	tokenIconDiskMaxBytes   = int64(64 << 20)
	tokenIconDiskMaxEntries = 4096
	tokenIconPositiveTTL    = 30 * 24 * time.Hour
	tokenIconNegativeTTL    = 24 * time.Hour
)

type tokenIconIdentity struct {
	chain    string
	contract string
}

type tokenIconResult struct {
	data   []byte
	status int
	digest string
}

                                                                            
                                                                              
func tokenWithIconURL(node string, token tradingPairToken) tradingPairToken {
	                                                                 
	token.IconURL = ""
	if token.Type != "bridged" || token.NativeContract == "" {
		return token
	}
	identity, err := bridgeasset.Resolve(token.NativeChain, token.NativeContract)
	if err != nil || identity.ID != token.Address || trustWalletChain(identity.Chain) == "" {
		return token
	}
	query := url.Values{"node": {node}, "addr": {token.Address}}
	token.IconURL = "/api/token-icon?" + query.Encode()
	return token
}

func trustWalletChain(chain string) string {
	switch chain {
	case "eth":
		return "ethereum"
	case "bnb":
		return "smartchain"
	case "base":
		return "base"
	case "arb":
		return "arbitrum"
	case "trx":
		return "tron"
	default:
		return ""
	}
}

func validBridgeAssetID(id string) bool {
	if len(id) != len(bridgeasset.IDPrefix)+64 || !strings.HasPrefix(id, bridgeasset.IDPrefix) {
		return false
	}
	for _, c := range id[len(bridgeasset.IDPrefix):] {
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}

func (s *server) ensureTokenIconCaches() {
	s.tokenIconClientOnce.Do(func() {
		if s.tokenIconIdentityCache == nil {
			s.tokenIconIdentityCache = newBoundedTTLCache[tokenIconIdentity](4096, 1<<20, 64)
		}
		if s.tokenIconCache == nil {
			s.tokenIconCache = newBoundedTTLCache[tokenIconResult](2048, tokenIconDiskMaxBytes, 256)
		}
		if s.tokenIconFetchSlots == nil {
			s.tokenIconFetchSlots = make(chan struct{}, 4)
		}
		if s.tokenIconClient == nil {
			s.tokenIconClient = &http.Client{
				Timeout:       5 * time.Second,
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
				Transport: &http.Transport{
					Proxy:               nil,
					DialContext:         dialPublicTokenIconHost,
					MaxIdleConnsPerHost: 4,
				},
			}
		}
	})
}

                                                                            
                                                                             
func dialPublicTokenIconHost(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host != tokenIconSourceHost || port != "443" {
		return nil, errors.New("token icon source host is not allowed")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	for _, candidate := range addresses {
		ip, ok := netip.AddrFromSlice(candidate.IP)
		ip = ip.Unmap()
		if !ok || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		err = dialErr
	}
	if err == nil {
		err = errors.New("token icon source has no public address")
	}
	return nil, err
}

func (s *server) handleTokenIcon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	node := s.pickNode(r.URL.Query().Get("node"))
	assetID := r.URL.Query().Get("addr")
	if node == "" || !validBridgeAssetID(assetID) {
		http.Error(w, "invalid token icon identity", http.StatusBadRequest)
		return
	}
	s.ensureTokenIconCaches()
	identity, err := s.tokenIconIdentityCache.GetOrLoad(r.Context(), node+"\x00"+assetID, 10*time.Minute, func(ctx context.Context) (tokenIconIdentity, int64, error) {
		token, fetchErr := s.fetchToken(ctx, node, assetID)
		if fetchErr != nil {
			return tokenIconIdentity{}, 1, fetchErr
		}
		if token.Type != pb.TokenType_TOKEN_BRIDGED || token.NativeContract == "" {
			return tokenIconIdentity{}, 1, errors.New("asset is not a bridged contract")
		}
		resolved, resolveErr := bridgeasset.Resolve(token.NativeChain, token.NativeContract)
		if resolveErr != nil || resolved.ID != token.Address || trustWalletChain(resolved.Chain) == "" {
			return tokenIconIdentity{}, 1, errors.New("asset identity does not match registry")
		}
		return tokenIconIdentity{chain: resolved.Chain, contract: resolved.Contract}, 128, nil
	})
	if err != nil {
		                                                                    
		                                                              
		http.Error(w, "token icon identity unavailable", http.StatusNotFound)
		return
	}
	key := identity.chain + "\x00" + identity.contract
	result, err := s.tokenIconCache.GetOrLoad(r.Context(), key, time.Minute, func(loadCtx context.Context) (tokenIconResult, int64, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(loadCtx), 6*time.Second)
		defer cancel()
		if cached, ok := s.loadTokenIconDisk(key); ok {
			return cached, int64(len(cached.data)) + 128, nil
		}
		select {
		case s.tokenIconFetchSlots <- struct{}{}:
			defer func() { <-s.tokenIconFetchSlots }()
		case <-ctx.Done():
			return tokenIconResult{status: http.StatusServiceUnavailable}, 128, nil
		}
		loaded := s.fetchTokenIconSource(ctx, identity)
		if loaded.status == http.StatusOK || loaded.status == http.StatusNotFound {
			s.writeTokenIconDisk(key, loaded)
		}
		return loaded, int64(len(loaded.data)) + 128, nil
	})
	if err != nil {
		http.Error(w, "token icon service busy", http.StatusServiceUnavailable)
		return
	}
	if result.status != http.StatusOK {
		if result.status == http.StatusNotFound {
			w.Header().Set("Cache-Control", "public, max-age=300")
			http.NotFound(w, r)
		} else {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "token icon source unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", `"`+result.digest+`"`)
	if r.Header.Get("If-None-Match") == `"`+result.digest+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(result.data)
}

func (s *server) fetchTokenIconSource(ctx context.Context, identity tokenIconIdentity) tokenIconResult {
	chain := trustWalletChain(identity.chain)
	if chain == "" || strings.ContainsAny(identity.contract, "/?#\\") {
		return tokenIconResult{status: http.StatusNotFound}
	}
	endpoint := "https://" + tokenIconSourceHost + "/blockchains/" + chain + "/assets/" + identity.contract + "/logo.png"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return tokenIconResult{status: http.StatusServiceUnavailable}
	}
	req.Header.Set("Accept", "image/png")
	resp, err := s.tokenIconClient.Do(req)
	if err != nil {
		return tokenIconResult{status: http.StatusServiceUnavailable}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return tokenIconResult{status: http.StatusNotFound}
	}
	if resp.StatusCode != http.StatusOK || resp.ContentLength > tokenIconMaxSourceBytes {
		return tokenIconResult{status: http.StatusServiceUnavailable}
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(strings.ToLower(ct), "image/png") {
		return tokenIconResult{status: http.StatusServiceUnavailable}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, tokenIconMaxSourceBytes+1))
	if err != nil || int64(len(raw)) > tokenIconMaxSourceBytes {
		return tokenIconResult{status: http.StatusServiceUnavailable}
	}
	config, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > tokenIconMaxSide || config.Height > tokenIconMaxSide {
		return tokenIconResult{status: http.StatusServiceUnavailable}
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return tokenIconResult{status: http.StatusServiceUnavailable}
	}
	var clean bytes.Buffer
	if err := png.Encode(&clean, img); err != nil || clean.Len() > int(tokenIconMaxSourceBytes) {
		return tokenIconResult{status: http.StatusServiceUnavailable}
	}
	data := clean.Bytes()
	digest := sha256.Sum256(data)
	return tokenIconResult{status: http.StatusOK, data: data, digest: hex.EncodeToString(digest[:])}
}

func tokenIconDiskName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func (s *server) loadTokenIconDisk(key string) (tokenIconResult, bool) {
	if s.tokenIconDir == "" {
		return tokenIconResult{}, false
	}
	name := tokenIconDiskName(key)
	for _, candidate := range []struct {
		suffix string
		ttl    time.Duration
	}{{".png", tokenIconPositiveTTL}, {".miss", tokenIconNegativeTTL}} {
		path := filepath.Join(s.tokenIconDir, name+candidate.suffix)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) >= candidate.ttl {
			_ = os.Remove(path)
			continue
		}
		if candidate.suffix == ".miss" {
			return tokenIconResult{status: http.StatusNotFound}, true
		}
		if info.Size() < 1 || info.Size() > tokenIconMaxSourceBytes {
			_ = os.Remove(path)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		digest := sha256.Sum256(data)
		return tokenIconResult{status: http.StatusOK, data: data, digest: hex.EncodeToString(digest[:])}, true
	}
	return tokenIconResult{}, false
}

func (s *server) writeTokenIconDisk(key string, result tokenIconResult) {
	if s.tokenIconDir == "" {
		return
	}
	s.tokenIconDiskMu.Lock()
	defer s.tokenIconDiskMu.Unlock()
	if err := os.MkdirAll(s.tokenIconDir, 0755); err != nil {
		return
	}
	suffix := ".miss"
	if result.status == http.StatusOK {
		suffix = ".png"
	}
	name := tokenIconDiskName(key)
	path := filepath.Join(s.tokenIconDir, name+suffix)
	tmp, err := os.CreateTemp(s.tokenIconDir, name+"-*.tmp")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(result.data); err != nil {
		_ = tmp.Close()
		return
	}
	if err = tmp.Close(); err != nil {
		return
	}
	_ = os.Remove(path)
	if err := os.Rename(tmp.Name(), path); err != nil {
		return
	}
	opposite := ".png"
	if suffix == ".png" {
		opposite = ".miss"
	}
	_ = os.Remove(filepath.Join(s.tokenIconDir, name+opposite))
	s.pruneTokenIconDisk()
}

                                                                         
                                                                               
func (s *server) pruneTokenIconDisk() {
	entries, err := os.ReadDir(s.tokenIconDir)
	if err != nil {
		return
	}
	type file struct {
		path     string
		size     int64
		modified time.Time
	}
	files := make([]file, 0, len(entries))
	var total int64
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || (len(name) != 68 && len(name) != 69) || (!strings.HasSuffix(name, ".png") && !strings.HasSuffix(name, ".miss")) {
			continue
		}
		if _, err := hex.DecodeString(name[:64]); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, file{path: filepath.Join(s.tokenIconDir, name), size: info.Size(), modified: info.ModTime()})
		total += info.Size()
	}
	if len(files) <= tokenIconDiskMaxEntries && total <= tokenIconDiskMaxBytes {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modified.Before(files[j].modified) })
	remaining := len(files)
	for _, entry := range files {
		if remaining <= tokenIconDiskMaxEntries && total <= tokenIconDiskMaxBytes {
			break
		}
		if err := os.Remove(entry.path); err == nil {
			total -= entry.size
			remaining--
		}
	}
}
