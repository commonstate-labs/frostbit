package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dex/updater"
)

func TestReleaseVersionNormalizationAndIncrement(t *testing.T) {
	for _, tc := range []struct{ input, canonical, next string }{
		{"1.2.3", "v1.2.3", "v1.2.4"}, {"v1.2", "v1.2.0", "v1.2.1"},
		{"v12.34.999", "v12.34.999", "v12.34.1000"}, {"v1", "v1.0.0", "v1.0.1"},
	} {
		got, err := canonicalReleaseVersion(tc.input)
		if err != nil || got != tc.canonical {
			t.Fatalf("normalize %q = %q, %v", tc.input, got, err)
		}
		next, err := nextPatchVersion(tc.input)
		if err != nil || next != tc.next {
			t.Fatalf("next %q = %q, %v", tc.input, next, err)
		}
	}
	for _, invalid := range []string{"", "garbage", "v1.02.3", "v1.2.3-rc.1", "v1.2.3+build"} {
		if _, err := nextPatchVersion(invalid); err == nil {
			t.Errorf("invalid latest version %q silently produced a version", invalid)
		}
	}
	if _, err := nextPatchVersion("v1.2.18446744073709551615"); err == nil {
		t.Fatal("patch overflow accepted")
	}
}

type publicationFixture struct {
	publisher        *githubPublisher
	assets           []string
	events           []string
	uploads          int
	published        bool
	disabled         bool
	failUpload       int
	wrongDigest      bool
	untrustedUpload  bool
	existingTag      bool
	wrongTagCommit   bool
	mutablePublished bool
}

func newPublicationFixture(t *testing.T) *publicationFixture {
	t.Helper()
	f := &publicationFixture{}
	for _, name := range []string{
		"complete.zip",
		"dex-node-v1.2.3-linux-amd64",
		"dex-update-linux-amd64.json",
		"frostbit-wallet-v1.2.3.zip",
		"frostbit-v1.2.3-source.zip",
	} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte("contents of "+name), 0o600); err != nil {
			t.Fatal(err)
		}
		f.assets = append(f.assets, path)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.events = append(f.events, r.Method+" "+r.URL.Path)
		if got := r.Header.Get("Authorization"); got != "Bearer fake-test-token" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("X-GitHub-Api-Version"); got != githubAPIVersion {
			t.Errorf("API version = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		prefix := "/repos/" + updater.Repository
		switch {
		case r.Method == "GET" && r.URL.Path == prefix:
			_ = json.NewEncoder(w).Encode(map[string]any{"full_name": updater.Repository, "private": false})
		case r.Method == "GET" && r.URL.Path == prefix+"/git/ref/heads/main":
			_ = json.NewEncoder(w).Encode(map[string]any{"ref": "refs/heads/main", "object": map[string]string{"type": "commit", "sha": strings.Repeat("a", 40)}})
		case r.Method == "GET" && r.URL.Path == prefix+"/immutable-releases":
			if f.disabled {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = io.WriteString(w, `{"enabled":true}`)
		case r.Method == "GET" && r.URL.Path == prefix+"/git/ref/tags/v1.2.3":
			if !f.published && !f.existingTag && !(f.wrongTagCommit && f.uploads == len(f.assets)) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			commit := strings.Repeat("a", 40)
			if f.wrongTagCommit {
				commit = strings.Repeat("b", 40)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ref": "refs/tags/v1.2.3", "object": map[string]string{"type": "commit", "sha": commit}})
		case r.Method == "POST" && r.URL.Path == prefix+"/releases":
			var input map[string]any
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]any{"draft": true, "prerelease": false, "make_latest": "false", "target_commitish": strings.Repeat("a", 40), "tag_name": "v1.2.3"} {
				if input[key] != want {
					t.Errorf("draft %s = %v, want %v", key, input[key], want)
				}
			}
			uploadURL := server.URL + "/uploads{?name,label}"
			if f.untrustedUpload {
				uploadURL = "https://untrusted.invalid/uploads{?name,label}"
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 17, "draft": true, "tag_name": "v1.2.3", "upload_url": uploadURL})
		case r.Method == "POST" && r.URL.Path == "/uploads":
			f.uploads++
			if f.failUpload == f.uploads {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(body)
			digest := "sha256:" + hex.EncodeToString(sum[:])
			if f.wrongDigest {
				digest = "sha256:" + strings.Repeat("0", 64)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"name": r.URL.Query().Get("name"), "size": len(body), "digest": digest})
		case r.Method == "PATCH" && r.URL.Path == prefix+"/releases/17":
			if f.uploads != len(f.assets) {
				t.Errorf("published after %d uploads", f.uploads)
			}
			var input map[string]any
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input["draft"] != false || input["prerelease"] != false || input["make_latest"] != "true" {
				t.Errorf("publish payload: %v", input)
			}
			f.published = true
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.2.3", "draft": false, "prerelease": false, "immutable": !f.mutablePublished})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	f.publisher = &githubPublisher{client: server.Client(), apiURL: server.URL, token: "fake-test-token"}
	return f
}

func TestPublishUploadsCompleteDraftBeforeVisibility(t *testing.T) {
	f := newPublicationFixture(t)
	if err := f.publisher.Publish("v1.2.3", strings.Repeat("a", 40), f.assets); err != nil {
		t.Fatal(err)
	}
	prefix := "/repos/" + updater.Repository
	want := []string{"GET " + prefix, "GET " + prefix + "/git/ref/heads/main",
		"GET " + prefix + "/immutable-releases", "GET " + prefix + "/git/ref/tags/v1.2.3", "POST " + prefix + "/releases",
		"POST /uploads", "POST /uploads", "POST /uploads", "POST /uploads", "POST /uploads",
		"GET " + prefix + "/git/ref/tags/v1.2.3", "PATCH " + prefix + "/releases/17", "GET " + prefix + "/git/ref/tags/v1.2.3"}
	if !reflect.DeepEqual(f.events, want) {
		t.Fatalf("publication sequence\ngot: %v\nwant: %v", f.events, want)
	}
}

func TestPublishFailureKeepsIncompleteDraftPrivate(t *testing.T) {
	for _, name := range []string{"immutable disabled", "existing tag", "second upload fails", "wrong uploaded digest", "untrusted upload URL", "tag moved during upload"} {
		t.Run(name, func(t *testing.T) {
			f := newPublicationFixture(t)
			switch name {
			case "immutable disabled":
				f.disabled = true
			case "existing tag":
				f.existingTag = true
			case "second upload fails":
				f.failUpload = 2
			case "wrong uploaded digest":
				f.wrongDigest = true
			case "untrusted upload URL":
				f.untrustedUpload = true
			case "tag moved during upload":
				f.wrongTagCommit = true
			}
			if err := f.publisher.Publish("v1.2.3", strings.Repeat("a", 40), f.assets); err == nil {
				t.Fatal("invalid publication succeeded")
			}
			if f.published {
				t.Fatal("failed/incomplete draft became public")
			}
		})
	}
}

func TestPublishConfirmsImmutableResult(t *testing.T) {
	f := newPublicationFixture(t)
	f.mutablePublished = true
	if err := f.publisher.Publish("v1.2.3", strings.Repeat("a", 40), f.assets); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("mutable published result accepted: %v", err)
	}
}

func TestVerifyPublicSourceRequiresExactVisibleMain(t *testing.T) {
	commit := strings.Repeat("a", 40)
	publicRepo := `{"full_name":"` + updater.Repository + `","private":false}`
	mainRef := `{"ref":"refs/heads/main","object":{"type":"commit","sha":"` + commit + `"}}`
	for _, tc := range []struct {
		name, repo, ref string
		wantOK          bool
	}{
		{"exact public main", publicRepo, mainRef, true},
		{"different repository", `{"full_name":"private/repo","private":false}`, mainRef, false},
		{"private repository", `{"full_name":"` + updater.Repository + `","private":true}`, mainRef, false},
		{"missing visibility", `{"full_name":"` + updater.Repository + `"}`, mainRef, false},
		{"different head", publicRepo, strings.Replace(mainRef, commit, strings.Repeat("b", 40), 1), false},
		{"different branch", publicRepo, strings.Replace(mainRef, "refs/heads/main", "refs/heads/dev", 1), false},
		{"different object type", publicRepo, strings.Replace(mainRef, `"commit"`, `"tag"`, 1), false},
		{"missing commit", publicRepo, `{"ref":"refs/heads/main","object":{"type":"commit"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/" + updater.Repository:
					_, _ = io.WriteString(w, tc.repo)
				case "/repos/" + updater.Repository + "/git/ref/heads/main":
					_, _ = io.WriteString(w, tc.ref)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			p := githubPublisher{client: server.Client(), apiURL: server.URL}
			if err := p.verifyPublicSource(commit); (err == nil) != tc.wantOK {
				t.Fatalf("verifyPublicSource = %v, want success: %v", err, tc.wantOK)
			}
		})
	}
}

func TestNextVersionDoesNotTreatInaccessibleRepositoryAsFirstRelease(t *testing.T) {
	for _, accessible := range []bool{true, false} {
		t.Run(map[bool]string{true: "accessible empty repository", false: "inaccessible repository"}[accessible], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/repos/"+updater.Repository && accessible {
					_, _ = io.WriteString(w, `{"full_name":"`+updater.Repository+`"}`)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()
			p := githubPublisher{client: server.Client(), apiURL: server.URL}
			got, err := p.NextVersion()
			if accessible && (err != nil || got != "v0.1.0") || !accessible && err == nil {
				t.Fatalf("next = %q, %v", got, err)
			}
		})
	}
}
