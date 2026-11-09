package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bridgeasset "dex/frost/asset"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func testIconIdentity(t *testing.T) bridgeasset.Identity {
	t.Helper()
	identity, err := bridgeasset.Resolve("eth", "0x1f9840a85d5af5bf1d1762f925bdaddc4201f984")
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func TestTokenIconURLRequiresExactRegisteredIdentity(t *testing.T) {
	identity := testIconIdentity(t)
	token := tradingPairToken{Address: identity.ID, Type: "bridged", NativeChain: "eth", NativeContract: strings.ToLower(identity.Contract), IconURL: "https://attacker.invalid/logo.png"}
	got := tokenWithIconURL("node.example:443", token)
	if got.IconURL == "" || !strings.HasPrefix(got.IconURL, "/api/token-icon?") {
		t.Fatalf("missing same-origin icon URL: %+v", got)
	}
	parsed, err := url.Parse(got.IconURL)
	if err != nil || parsed.Query().Get("addr") != identity.ID {
		t.Fatalf("icon URL=%q err=%v", got.IconURL, err)
	}
	for _, bad := range []tradingPairToken{
		{Address: "br_" + strings.Repeat("a", 64), Type: "bridged", NativeChain: "eth", NativeContract: identity.Contract, IconURL: "https://attacker.invalid/logo.png"},
		{Address: identity.ID, Type: "native", NativeChain: "eth", NativeContract: identity.Contract, IconURL: "https://attacker.invalid/logo.png"},
		{Address: identity.ID, Type: "bridged", NativeChain: "eth", NativeContract: "", IconURL: "https://attacker.invalid/logo.png"},
	} {
		if result := tokenWithIconURL("node.example:443", bad); result.IconURL != "" {
			t.Fatalf("untrusted icon URL leaked: %+v", result)
		}
	}
}

func TestTokenDirectoryAdvertisesIconWithoutFetchingImage(t *testing.T) {
	identity := testIconIdentity(t)
	var listCalls atomic.Int32
	node := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/listtokens" {
			t.Errorf("unexpected node route %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		listCalls.Add(1)
		_ = json.NewEncoder(w).Encode([]tradingPairToken{{Address: identity.ID, Type: "bridged", NativeChain: identity.Chain, NativeContract: identity.Contract}})
	}))
	defer node.Close()
	address := strings.TrimPrefix(node.URL, "https://")
	s := &server{client: node.Client(), timeout: time.Second, defaultNodes: []string{address}}
	response := requestTokens(t, s, "/api/tokens?node="+url.QueryEscape(address)+"&addresses="+identity.ID)
	if response.Status != http.StatusOK || len(response.Tokens) != 1 || response.Tokens[0].IconURL == "" {
		t.Fatalf("token icon advertisement: %+v", response)
	}
	if listCalls.Load() != 1 || s.tokenIconCache != nil {
		t.Fatalf("list triggered icon fetch/cache: calls=%d", listCalls.Load())
	}
}

func TestTokenIconValidatedSourceAndDiskCache(t *testing.T) {
	identity := testIconIdentity(t)
	var imageBuf bytes.Buffer
	if err := png.Encode(&imageBuf, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	var nodeCalls, sourceCalls atomic.Int32
	newServer := func(source frostWithdrawV2RoundTripFunc) *server {
		s := newFrostWithdrawV2TestServer([]string{"node.example:443"}, func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/gettoken" {
				return nil, errors.New("unexpected node request")
			}
			nodeCalls.Add(1)
			return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.GetTokenResponse{Token: &pb.Token{
				Address: identity.ID, Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: identity.Chain, NativeContract: identity.Contract,
			}})
		})
		s.tokenIconClient = &http.Client{Transport: source}
		return s
	}
	iconDir := t.TempDir()
	source := frostWithdrawV2RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		sourceCalls.Add(1)
		if req.URL.Host != tokenIconSourceHost || req.URL.Path != "/blockchains/ethereum/assets/"+identity.Contract+"/logo.png" {
			t.Errorf("wrong fixed source: %s", req.URL)
		}
		data := imageBuf.Bytes()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"image/png"}}, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)), Request: req}, nil
	})
	s := newServer(source)
	s.tokenIconDir = iconDir
	path := "/api/token-icon?node=node-0&addr=" + identity.ID
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		s.handleTokenIcon(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("request %d: status=%d body=%s", i, rec.Code, rec.Body)
		}
		if _, err := png.Decode(bytes.NewReader(rec.Body.Bytes())); err != nil {
			t.Fatal(err)
		}
	}
	if nodeCalls.Load() != 1 || sourceCalls.Load() != 1 {
		t.Fatalf("node/source calls=%d/%d want=1/1", nodeCalls.Load(), sourceCalls.Load())
	}
	restarted := newServer(func(*http.Request) (*http.Response, error) {
		t.Error("source fetched after restart")
		return nil, errors.New("offline")
	})
	restarted.tokenIconDir = iconDir
	rec := httptest.NewRecorder()
	restarted.handleTokenIcon(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK || sourceCalls.Load() != 1 {
		t.Fatalf("disk cache status=%d source calls=%d", rec.Code, sourceCalls.Load())
	}
}

func TestTokenIconRejectsMismatchedRegistryAndMalformedPNG(t *testing.T) {
	identity := testIconIdentity(t)
	var sourceCalls atomic.Int32
	s := newFrostWithdrawV2TestServer([]string{"node.example:443"}, func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		var query pb.GetTokenRequest
		if err := proto.Unmarshal(body, &query); err != nil {
			return nil, err
		}
		return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.GetTokenResponse{Token: &pb.Token{
			Address: query.TokenAddress, Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: "eth", NativeContract: identity.Contract,
		}})
	})
	s.tokenIconDir = t.TempDir()
	s.tokenIconClient = &http.Client{Transport: frostWithdrawV2RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		sourceCalls.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"image/png"}}, Body: io.NopCloser(strings.NewReader("not a PNG")), ContentLength: 9, Request: req}, nil
	})}
	                                                                         
	                                                                       
	badID := "br_" + strings.Repeat("a", 64)
	rec := httptest.NewRecorder()
	s.handleTokenIcon(rec, httptest.NewRequest(http.MethodGet, "/api/token-icon?node=node-0&addr="+badID, nil))
	if rec.Code != http.StatusNotFound || sourceCalls.Load() != 0 {
		t.Fatalf("mismatch status=%d source=%d", rec.Code, sourceCalls.Load())
	}
	rec = httptest.NewRecorder()
	s.handleTokenIcon(rec, httptest.NewRequest(http.MethodGet, "/api/token-icon?node=node-0&addr="+identity.ID, nil))
	if rec.Code != http.StatusServiceUnavailable || sourceCalls.Load() != 1 {
		t.Fatalf("malformed PNG status=%d source=%d", rec.Code, sourceCalls.Load())
	}
}

func TestTokenIconNegativeCacheSurvivesRestart(t *testing.T) {
	identity := testIconIdentity(t)
	key := identity.Chain + "\x00" + identity.Contract
	dir := t.TempDir()
	s := &server{tokenIconDir: dir}
	s.writeTokenIconDisk(key, tokenIconResult{status: http.StatusNotFound})
	restarted := &server{tokenIconDir: dir}
	result, ok := restarted.loadTokenIconDisk(key)
	if !ok || result.status != http.StatusNotFound {
		t.Fatalf("negative disk cache: ok=%v result=%+v", ok, result)
	}
	if files, err := os.ReadDir(dir); err != nil || len(files) != 1 || !strings.HasSuffix(files[0].Name(), ".miss") {
		t.Fatalf("negative cache file: files=%v err=%v", files, err)
	}
}

func TestTokenIconConcurrentRequestsFetchSourceOnce(t *testing.T) {
	identity := testIconIdentity(t)
	var imageBuf bytes.Buffer
	if err := png.Encode(&imageBuf, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	var sourceCalls atomic.Int32
	s := newFrostWithdrawV2TestServer([]string{"node.example:443"}, func(req *http.Request) (*http.Response, error) {
		return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.GetTokenResponse{Token: &pb.Token{
			Address: identity.ID, Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: identity.Chain, NativeContract: identity.Contract,
		}})
	})
	s.tokenIconDir = t.TempDir()
	s.tokenIconClient = &http.Client{Transport: frostWithdrawV2RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		sourceCalls.Add(1)
		time.Sleep(30 * time.Millisecond)
		data := imageBuf.Bytes()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"image/png"}}, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)), Request: req}, nil
	})}
	var wg sync.WaitGroup
	results := make(chan int, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			s.handleTokenIcon(rec, httptest.NewRequest(http.MethodGet, "/api/token-icon?node=node-0&addr="+identity.ID, nil))
			results <- rec.Code
		}()
	}
	wg.Wait()
	close(results)
	for code := range results {
		if code != http.StatusOK {
			t.Fatalf("concurrent icon status=%d", code)
		}
	}
	if sourceCalls.Load() != 1 {
		t.Fatalf("source calls=%d want=1", sourceCalls.Load())
	}
}
