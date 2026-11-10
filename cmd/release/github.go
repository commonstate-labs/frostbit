package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dex/updater"

	"golang.org/x/mod/semver"
)

const githubAPIVersion = "2026-03-10"

type githubPublisher struct {
	client *http.Client
	apiURL string
	token  string
}

func newGitHubPublisher(token string) *githubPublisher {
	return &githubPublisher{client: &http.Client{Transport: http.DefaultClient.Transport, Timeout: 10 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, apiURL: "https://api.github.com", token: token}
}

func canonicalReleaseVersion(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(value, "v") {
		value = "v" + value
	}
	if !semver.IsValid(value) || semver.Prerelease(value) != "" || semver.Build(value) != "" {
		return "", fmt.Errorf("release version must be a stable semantic version: %q", raw)
	}
	return semver.Canonical(value), nil
}

func nextPatchVersion(tag string) (string, error) {
	canonical, err := canonicalReleaseVersion(tag)
	if err != nil {
		return "", err
	}
	parts := strings.Split(strings.TrimPrefix(canonical, "v"), ".")
	patch, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil || patch == math.MaxUint64 {
		return "", errors.New("release patch version cannot be incremented")
	}
	return fmt.Sprintf("v%s.%s.%d", parts[0], parts[1], patch+1), nil
}

func (p *githubPublisher) endpoint(suffix string) string {
	return strings.TrimRight(p.apiURL, "/") + "/repos/" + updater.Repository + suffix
}

func (p *githubPublisher) request(method, endpoint string, body io.Reader, length int64, contentType string, expected int, output any) error {
	u, err := url.Parse(endpoint)
	base, baseErr := url.Parse(p.apiURL)
	if err != nil || baseErr != nil || u.User != nil || u.Fragment != "" ||
		(u.Host != base.Host && !(base.Host == "api.github.com" && u.Host == "uploads.github.com")) ||
		(u.Scheme != base.Scheme) || (base.Host == "api.github.com" && u.Scheme != "https") {
		return errors.New("GitHub returned an untrusted API or upload URL")
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return err
	}
	req.ContentLength = length
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	response, err := p.client.Do(req)
	if err != nil {
		                                                                 
		return errors.New("GitHub request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != expected {
		return githubStatusError{response.StatusCode}
	}
	if output == nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return errors.New("GitHub response is unreadable or exceeds 2 MiB")
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return errors.New("GitHub returned malformed JSON")
	}
	return nil
}

type githubStatusError struct{ status int }

func (e githubStatusError) Error() string { return fmt.Sprintf("GitHub returned HTTP %d", e.status) }

func (p *githubPublisher) jsonRequest(method, endpoint string, input, output any, expected int) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return p.request(method, endpoint, strings.NewReader(string(raw)), int64(len(raw)), "application/json", expected, output)
}

func (p *githubPublisher) NextVersion() (string, error) {
	var release struct {
		TagName string `json:"tag_name"`
	}
	err := p.request(http.MethodGet, p.endpoint("/releases/latest"), nil, 0, "", http.StatusOK, &release)
	var status githubStatusError
	if errors.As(err, &status) && status.status == http.StatusNotFound {
		                                                                       
		                                                      
		var repo struct {
			FullName string `json:"full_name"`
		}
		if err := p.request(http.MethodGet, p.endpoint(""), nil, 0, "", http.StatusOK, &repo); err != nil {
			return "", err
		}
		if repo.FullName != updater.Repository {
			return "", errors.New("GitHub repository identity mismatch")
		}
		return "v0.1.0", nil
	}
	if err != nil {
		return "", err
	}
	return nextPatchVersion(release.TagName)
}

func (p *githubPublisher) Preflight(tag string) error {
	canonical, err := canonicalReleaseVersion(tag)
	if err != nil || canonical != tag {
		return errors.New("published tag must be a canonical stable version such as v1.2.3")
	}
	var settings struct {
		Enabled bool `json:"enabled"`
	}
	if err := p.request(http.MethodGet, p.endpoint("/immutable-releases"), nil, 0, "", http.StatusOK, &settings); err != nil {
		return fmt.Errorf("cannot verify immutable releases: enable the repository setting and grant the publisher Administration:read with repository administrator access: %w", err)
	}
	if !settings.Enabled {
		return errors.New("immutable releases are not enabled in the official repository")
	}
	var ref any
	err = p.request(http.MethodGet, p.endpoint("/git/ref/tags/"+url.PathEscape(tag)), nil, 0, "", http.StatusOK, &ref)
	var status githubStatusError
	if errors.As(err, &status) && status.status == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("release tag already exists; publish a new version instead of replacing it")
}

type githubGitRef struct {
	Ref    string                     `json:"ref"`
	Object struct{ Type, SHA string } `json:"object"`
}

func (p *githubPublisher) verifyPublicSource(commit string) error {
	var repo struct {
		FullName string `json:"full_name"`
		Private  *bool  `json:"private"`
	}
	if err := p.request(http.MethodGet, p.endpoint(""), nil, 0, "", http.StatusOK, &repo); err != nil {
		return fmt.Errorf("verify official repository visibility: %w", err)
	}
	if repo.FullName != updater.Repository || repo.Private == nil || *repo.Private {
		return errors.New("GitHub did not confirm the exact official repository is public")
	}
	var ref githubGitRef
	if err := p.request(http.MethodGet, p.endpoint("/git/ref/heads/main"), nil, 0, "", http.StatusOK, &ref); err != nil {
		return fmt.Errorf("verify official main commit: %w", err)
	}
	if ref.Ref != "refs/heads/main" || ref.Object.Type != "commit" || ref.Object.SHA != commit {
		return errors.New("the release build commit must match the exact official public main commit")
	}
	return nil
}

func (p *githubPublisher) verifyTagCommit(tag, commit string, allowAbsent bool) error {
	var ref githubGitRef
	err := p.request(http.MethodGet, p.endpoint("/git/ref/tags/"+url.PathEscape(tag)), nil, 0, "", http.StatusOK, &ref)
	var status githubStatusError
	if allowAbsent && errors.As(err, &status) && status.status == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if ref.Ref != "refs/tags/"+tag || ref.Object.Type != "commit" || ref.Object.SHA != commit {
		return errors.New("release tag does not point to the exact build commit")
	}
	return nil
}

func (p *githubPublisher) Publish(tag, commit string, assets []string) error {
	if len(commit) != 40 {
		return errors.New("publishing requires a full 40-character build commit")
	}
	if _, err := hex.DecodeString(commit); err != nil {
		return errors.New("invalid build commit")
	}
	if len(assets) < 5 {
		return errors.New("publishing requires the complete archive, node binary, update manifest, wallet ZIP and source ZIP")
	}
	seen := make(map[string]bool, len(assets))
	for _, asset := range assets {
		if err := validateRegularSourceFile(asset); err != nil {
			return err
		}
		name := filepath.Base(asset)
		if seen[name] {
			return fmt.Errorf("duplicate release asset %q", name)
		}
		seen[name] = true
	}
	if !seen[walletArchiveName(tag)] || !seen[sourceArchiveName(tag)] {
		return errors.New("publishing requires the versioned wallet and source ZIPs")
	}
	if err := p.verifyPublicSource(commit); err != nil {
		return err
	}
	if err := p.Preflight(tag); err != nil {
		return err
	}
	var draft struct {
		ID        int64  `json:"id"`
		Draft     bool   `json:"draft"`
		TagName   string `json:"tag_name"`
		UploadURL string `json:"upload_url"`
	}
	if err := p.jsonRequest(http.MethodPost, p.endpoint("/releases"), map[string]any{
		"tag_name": tag, "name": tag, "body": "Automated release " + tag,
		"target_commitish": commit, "draft": true, "prerelease": false, "make_latest": "false",
	}, &draft, http.StatusCreated); err != nil {
		return err
	}
	if draft.ID <= 0 || !draft.Draft || draft.TagName != tag || draft.UploadURL == "" {
		return errors.New("GitHub did not create the expected draft release")
	}
	for _, path := range assets {
		if err := p.upload(draft.UploadURL, path); err != nil {
			return fmt.Errorf("draft release %d retained; upload failed: %w", draft.ID, err)
		}
	}
	if err := p.verifyTagCommit(tag, commit, true); err != nil {
		return fmt.Errorf("draft release %d retained: %w", draft.ID, err)
	}
	var published struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Immutable  bool   `json:"immutable"`
	}
	if err := p.jsonRequest(http.MethodPatch, p.endpoint("/releases/"+strconv.FormatInt(draft.ID, 10)),
		map[string]any{"draft": false, "prerelease": false, "make_latest": "true"}, &published, http.StatusOK); err != nil {
		return err
	}
	if published.TagName != tag || published.Draft || published.Prerelease || !published.Immutable {
		return errors.New("published release is not the expected immutable official release; automatic updaters will reject it")
	}
	return p.verifyTagCommit(tag, commit, false)
}

func (p *githubPublisher) upload(uploadTemplate, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("release asset must be a regular file")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	endpoint := strings.Split(uploadTemplate, "{")[0]
	u, err := url.Parse(endpoint)
	if err != nil || u.RawQuery != "" {
		return errors.New("invalid upload URL")
	}
	query := url.Values{"name": []string{filepath.Base(path)}}
	u.RawQuery = query.Encode()
	var uploaded struct {
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	}
	contentType := "application/octet-stream"
	if strings.HasSuffix(path, ".json") {
		contentType = "application/json"
	} else if strings.HasSuffix(path, ".zip") {
		contentType = "application/zip"
	}
	if err := p.request(http.MethodPost, u.String(), f, info.Size(), contentType, http.StatusCreated, &uploaded); err != nil {
		return err
	}
	if uploaded.Name != filepath.Base(path) || uploaded.Size != info.Size() || uploaded.Digest != "sha256:"+hex.EncodeToString(hash.Sum(nil)) {
		return errors.New("uploaded release asset metadata or digest does not match the local file")
	}
	return nil
}
