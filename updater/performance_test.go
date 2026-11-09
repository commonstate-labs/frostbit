package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestEmbeddedStreamingDownloadHasConstantAllocation(t *testing.T) {
	f := newEmbeddedFixture(t)
	const n = int64(64 << 20)
	h := sha256.New()
	io.CopyN(h, zeroReader{}, n)
	asset := releaseAsset{ID: 9, Name: "node", Size: n, Digest: "sha256:" + hex.EncodeToString(h.Sum(nil))}
	f.agent.dep.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(io.LimitReader(zeroReader{}, n)), Request: r}, nil
	})}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if e := f.agent.download(context.Background(), asset, filepath.Join(t.TempDir(), "node"), n); e != nil {
		t.Fatal(e)
	}
	runtime.ReadMemStats(&after)
	if bytes := after.TotalAlloc - before.TotalAlloc; bytes > 4<<20 {
		t.Fatalf("64MiB download allocated %d bytes", bytes)
	}
	t.Logf("64MiB asset allocation=%d bytes", after.TotalAlloc-before.TotalAlloc)
}
func TestEmbeddedNoNewReleaseLocal20WorkBound(t *testing.T) {
	f := newEmbeddedFixture(t)
	f.release.Tag = f.cfg.Info.Version
	for i := 0; i < 20; i++ {
		if p, e := f.agent.PrepareOnce(context.Background()); e != nil || p != nil {
			t.Fatalf("unexpected update %v %v", p, e)
		}
	}
	if f.latestCalls != 20 || f.assetCalls != 0 || f.execCalls != 0 || len(f.runner.events) != 0 {
		t.Fatalf("checks=%d downloads=%d execs=%d helpers=%v", f.latestCalls, f.assetCalls, f.execCalls, f.runner.events)
	}
	t.Log("20 background checks: 20 metadata requests, 0 asset downloads, 0 child helpers, 0 node execs")
}
func TestEmbeddedDownloadBoundaries(t *testing.T) {
	for _, n := range []int64{1023, 1024, 1025} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			f := newEmbeddedFixture(t)
			body := strings.Repeat("a", int(n))
			calls := 0
			f.agent.dep.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			e := f.agent.download(context.Background(), releaseAsset{ID: 9, Name: "node", Size: n, Digest: "sha256:" + HashBytes([]byte(body))}, filepath.Join(t.TempDir(), "node"), 1024)
			if n > 1024 {
				if e == nil || calls != 0 {
					t.Fatal("oversize asset requested")
				}
			} else if e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestEmbeddedETagAndRateLimit(t *testing.T) {
	f := newEmbeddedFixture(t)
	calls := 0
	f.agent.dep.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		h := make(http.Header)
		h.Set("ETag", "same")
		if calls == 1 {
			b, _ := json.Marshal(f.release)
			return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(string(b))), Request: r}, nil
		}
		if r.Header.Get("If-None-Match") != "same" {
			t.Error("ETag missing")
		}
		return &http.Response{StatusCode: 304, Header: h, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	if _, e := f.agent.latest(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e := f.agent.latest(context.Background()); e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	f.agent.dep.Now = func() time.Time { return now }
	f.agent.dep.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		h := make(http.Header)
		h.Set("Retry-After", "1200")
		return &http.Response{StatusCode: 429, Header: h, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	if _, e := f.agent.latest(context.Background()); e == nil {
		t.Fatal("rate limit ignored")
	}
	if f.agent.nextCheck.Before(now.Add(20 * time.Minute)) {
		t.Fatal("retry-after ignored")
	}
}
func TestEmbeddedRedirectTokenIsolation(t *testing.T) {
	c, e := restrictedClient(&http.Client{}, "https://api.github.com")
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse("https://release-assets.githubusercontent.com/asset")
	r := &http.Request{URL: u, Header: make(http.Header)}
	r.Header.Set("Authorization", "secret")
	if e = c.CheckRedirect(r, nil); e != nil || r.Header.Get("Authorization") != "" {
		t.Fatal("token redirect", e)
	}
	u, _ = url.Parse("https://evil.invalid/asset")
	if e = c.CheckRedirect(&http.Request{URL: u, Header: make(http.Header)}, nil); e == nil {
		t.Fatal("untrusted redirect allowed")
	}
}
func TestBootstrapChangedChainOrTamperedCacheCannotExec(t *testing.T) {
	f := newEmbeddedFixture(t)
	p := f.prepared(t)
	if e := f.agent.saveState(diskState{Stable: &p}); e != nil {
		t.Fatal(e)
	}
	f.cfg.Info.ChainID = 1
	if e := Bootstrap(context.Background(), f.cfg, f.dep); !errorsIsUnsafe(e) {
		t.Fatalf("changed chain not fatal: %v", e)
	}
	if f.execCalls != 0 {
		t.Fatal("wrong-chain cache executed")
	}
	f.cfg.Info.ChainID = 51025
	os.WriteFile(p.Path, []byte("tampered"), 0700)
	if e := Bootstrap(context.Background(), f.cfg, f.dep); e == nil {
		t.Fatal("tampered cache executed")
	}
	if f.execCalls != 0 {
		t.Fatal("tampered cache execed")
	}
}
func errorsIsUnsafe(e error) bool {
	return e != nil && strings.Contains(e.Error(), ErrUnsafeBootstrap.Error())
}
