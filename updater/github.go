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
	"strconv"
	"strings"
	"time"
)

const maxManifestBytes = 128 << 10
const maxReleaseBytes = 2 << 20

type releaseAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}
type release struct {
	ID         int64          `json:"id"`
	Tag        string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Immutable  bool           `json:"immutable"`
	Assets     []releaseAsset `json:"assets"`
}

func (a *Agent) request(ctx context.Context, path, accept string) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, a.apiURL+path, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "dex-updater/1")
	if token := os.Getenv("GH_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if token = os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if strings.HasSuffix(path, "/releases/latest") && a.etag != "" {
		req.Header.Set("If-None-Match", a.etag)
	}
	resp, e := a.dep.HTTPClient.Do(req)
	if e != nil {
		return nil, fmt.Errorf("GitHub request failed: %w", e)
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		a.deferRateLimit(resp)
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub rate limited update checks")
	}
	return resp, nil
}

func (a *Agent) deferRateLimit(resp *http.Response) {
	next := a.dep.Now().Add(a.cfg.PollInterval)
	if secs, e := strconv.ParseInt(resp.Header.Get("Retry-After"), 10, 64); e == nil && secs > 0 && secs <= 86400 {
		next = a.dep.Now().Add(time.Duration(secs) * time.Second)
	}
	if epoch, e := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); e == nil && epoch > 0 {
		t := time.Unix(epoch, 0)
		if t.After(next) && t.Before(a.dep.Now().Add(24*time.Hour)) {
			next = t
		}
	}
	a.nextCheck = next
}

func decodeBounded(r io.Reader, limit int64, out any) error {
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if e != nil {
		return e
	}
	if int64(len(b)) > limit {
		return fmt.Errorf("metadata exceeds size limit")
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e = d.Decode(out); e != nil {
		return e
	}
	if e = d.Decode(new(any)); e != io.EOF {
		return fmt.Errorf("trailing metadata")
	}
	return nil
}

func (a *Agent) latest(ctx context.Context) (release, error) {
	resp, e := a.request(ctx, "/repos/"+Repository+"/releases/latest", "application/vnd.github+json")
	if e != nil {
		return release{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified && a.cachedRelease != nil {
		return *a.cachedRelease, nil
	}
	if resp.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("latest GitHub release HTTP %d", resp.StatusCode)
	}
	                                                                                      
	b, e := io.ReadAll(io.LimitReader(resp.Body, maxReleaseBytes+1))
	if e != nil {
		return release{}, e
	}
	if len(b) > maxReleaseBytes {
		return release{}, fmt.Errorf("release metadata exceeds limit")
	}
	var r release
	if e = json.Unmarshal(b, &r); e != nil {
		return r, e
	}
	if r.ID <= 0 || r.Draft || r.Prerelease || !r.Immutable {
		return r, fmt.Errorf("latest release must be published, stable and immutable")
	}
	if _, e = canonicalVersion(r.Tag); e != nil {
		return r, e
	}
	if len(r.Assets) > 256 {
		return r, fmt.Errorf("too many release assets")
	}
	a.etag = resp.Header.Get("ETag")
	a.cachedRelease = &r
	return r, nil
}

func findAsset(r release, name string) (releaseAsset, error) {
	var found releaseAsset
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, "/\\\x00") || strings.HasPrefix(name, ".") {
		return found, fmt.Errorf("invalid release asset name")
	}
	for _, asset := range r.Assets {
		if asset.Name == name {
			if found.ID != 0 {
				return found, fmt.Errorf("duplicate release asset %q", name)
			}
			found = asset
		}
	}
	if found.ID <= 0 || found.Size <= 0 || !strings.HasPrefix(found.Digest, "sha256:") || !digestValid(strings.TrimPrefix(found.Digest, "sha256:"), 32) {
		return found, fmt.Errorf("missing or invalid release asset %q", name)
	}
	return found, nil
}

func (a *Agent) download(ctx context.Context, asset releaseAsset, path string, limit int64) error {
	if asset.Size > limit {
		return fmt.Errorf("release asset exceeds configured size limit")
	}
	resp, e := a.request(ctx, fmt.Sprintf("/repos/%s/releases/assets/%d", Repository, asset.ID), "application/octet-stream")
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("release asset HTTP %d", resp.StatusCode)
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	good := false
	defer func() {
		f.Close()
		if !good {
			os.Remove(path)
		}
	}()
	h := sha256.New()
	buf := make([]byte, 64<<10)
	n, e := io.CopyBuffer(io.MultiWriter(f, h), io.LimitReader(resp.Body, asset.Size+1), buf)
	if e != nil {
		return e
	}
	if n != asset.Size || hex.EncodeToString(h.Sum(nil)) != strings.TrimPrefix(asset.Digest, "sha256:") {
		return fmt.Errorf("release asset size/digest mismatch")
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	good = true
	return nil
}

func (a *Agent) verify(ctx context.Context, r release, path string) error {
	_, e := a.command(ctx, a.cfg.GHPath, "release", "verify-asset", r.Tag, path, "--repo", Repository)
	if e != nil {
		return fmt.Errorf("GitHub release attestation verification failed: %w", e)
	}
	return nil
}

func restrictedClient(client *http.Client, api string) (*http.Client, error) {
	u, e := url.Parse(api)
	if e != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid API URL")
	}
	c := *client
	prior := client.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many GitHub redirects")
		}
		h := strings.ToLower(req.URL.Hostname())
		allowed := h == "api.github.com" || h == "github.com" || h == "release-assets.githubusercontent.com" || h == "objects.githubusercontent.com"
		if u.Scheme == "http" && req.URL.Scheme == "http" && req.URL.Host == u.Host {
			allowed = true
		}
		if !allowed || req.URL.Scheme != "https" && req.URL.Scheme != u.Scheme {
			return fmt.Errorf("untrusted asset redirect")
		}
		if req.URL.Host != u.Host {
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
		}
		if prior != nil {
			return prior(req, via)
		}
		return nil
	}
	return &c, nil
}
