package sender

import (
	"bytes"
	"crypto/rand"
	"dex/types"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNextCheckpointChunkLimit(t *testing.T) {
	tests := []struct {
		name string
		cur  int64
		min  int64
		want int64
	}{
		{name: "halve above min", cur: 10 << 20, min: 1 << 20, want: 5 << 20},
		{name: "clamp to min", cur: 3 << 20, min: 2 << 20, want: 2 << 20},
		{name: "stay at min", cur: 1 << 20, min: 1 << 20, want: 1 << 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextCheckpointChunkLimit(tt.cur, tt.min); got != tt.want {
				t.Fatalf("nextCheckpointChunkLimit(%d,%d)=%d want %d", tt.cur, tt.min, got, tt.want)
			}
		})
	}
}

func TestFetchStateCheckpointFilesShrinksChunkAfterFailure(t *testing.T) {
	const (
		height    = uint64(7)
		fileName  = "000001.sst"
		fullLimit = int64(2 << 20)
		halfLimit = int64(1 << 20)
	)
	content := make([]byte, fullLimit)
	if _, err := rand.Read(content); err != nil {
		t.Fatal(err)
	}
	var fullLimitRequests atomic.Int32
	var halfLimitRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/statedb/checkpoint/files":
			_ = json.NewEncoder(w).Encode(types.StateCheckpointFilesResponse{
				SnapshotHeight: height,
				ChunkSize:      fullLimit,
				TotalBytes:     int64(len(content)),
				Files:          []types.StateCheckpointFile{{Name: fileName, Size: int64(len(content)), Chunks: 1}},
			})
		case "/statedb/checkpoint/chunk":
			var req types.StateCheckpointChunkRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if req.Limit == fullLimit {
				fullLimitRequests.Add(1)
				http.Error(w, "chunk too slow", http.StatusGatewayTimeout)
				return
			}
			if req.Limit == halfLimit {
				halfLimitRequests.Add(1)
			}
			w.Write(content[req.Offset : req.Offset+req.Limit])
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	sm := &SenderManager{httpClient: server.Client()}
	dir, cleanup, err := sm.FetchStateCheckpointFiles(strings.TrimPrefix(server.URL, "https://"), height)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	got, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("downloaded content mismatch")
	}
	if fullLimitRequests.Load() != 1 {
		t.Fatalf("full chunk requests=%d want 1", fullLimitRequests.Load())
	}
	if halfLimitRequests.Load() != 2 {
		t.Fatalf("half chunk requests=%d want 2", halfLimitRequests.Load())
	}
}

func TestFetchStateCheckpointFilesDoesNotShrinkAfterShortTail(t *testing.T) {
	const (
		height    = uint64(8)
		chunkSize = int64(8)
	)
	files := map[string][]byte{
		"000001.log": []byte("abc"),
		"000002.sst": []byte("0123456789abcdef"),
	}
	var sstLimits []int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/statedb/checkpoint/files":
			_ = json.NewEncoder(w).Encode(types.StateCheckpointFilesResponse{
				SnapshotHeight: height,
				ChunkSize:      chunkSize,
				TotalBytes:     int64(len(files["000001.log"]) + len(files["000002.sst"])),
				Files: []types.StateCheckpointFile{
					{Name: "000001.log", Size: int64(len(files["000001.log"])), Chunks: 1},
					{Name: "000002.sst", Size: int64(len(files["000002.sst"])), Chunks: 2},
				},
			})
		case "/statedb/checkpoint/chunk":
			var req types.StateCheckpointChunkRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			content := files[req.Name]
			if req.Name == "000002.sst" {
				sstLimits = append(sstLimits, req.Limit)
			}
			w.Write(content[req.Offset : req.Offset+req.Limit])
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	sm := &SenderManager{httpClient: server.Client()}
	dir, cleanup, err := sm.FetchStateCheckpointFiles(strings.TrimPrefix(server.URL, "https://"), height)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s content mismatch", name)
		}
	}
	if len(sstLimits) != 2 || sstLimits[0] != chunkSize || sstLimits[1] != chunkSize {
		t.Fatalf("sst chunk limits=%v want [%d %d]", sstLimits, chunkSize, chunkSize)
	}
}

func TestFetchCertChainUsesBulkEndpoint(t *testing.T) {
	var bulkRequests atomic.Int32
	var getRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cert/chain":
			bulkRequests.Add(1)
			_ = json.NewEncoder(w).Encode(certChainResponse{
				Certs: []certChainEntry{
					{Height: 10, Data: []byte("cert-10")},
					{Height: 7, Data: []byte("skip-7")},
					{Height: 5, Data: []byte("cert-5")},
				},
				Count: 3,
			})
		case "/cert/get":
			getRequests.Add(1)
			http.Error(w, "unexpected per-height fetch", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	sm := &SenderManager{httpClient: server.Client()}
	certs, err := sm.FetchCertChain(strings.TrimPrefix(server.URL, "https://"), 0, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(certs) != 2 || !bytes.Equal(certs[0], []byte("cert-5")) || !bytes.Equal(certs[1], []byte("cert-10")) {
		t.Fatalf("certs=%q want cert-5,cert-10", certs)
	}
	if bulkRequests.Load() != 1 || getRequests.Load() != 0 {
		t.Fatalf("bulk=%d get=%d want 1,0", bulkRequests.Load(), getRequests.Load())
	}
}

func TestFetchCertChainFollowsAllBulkPages(t *testing.T) {
	const certificateCount = 105
	allEntries := make([]certChainEntry, 0, certificateCount)
	for i := 1; i <= certificateCount; i++ {
		height := uint64(i * 100)
		allEntries = append(allEntries, certChainEntry{Height: height, Data: []byte(strconv.FormatUint(height, 10))})
	}

	var bulkRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bulkRequests.Add(1)
		if got := r.URL.Query().Get("to"); got != "10500" {
			t.Errorf("to=%s want 10500", got)
		}
		page := certChainResponse{Certs: allEntries[:100], Count: 100, HasMore: true, NextFrom: 10001}
		if r.URL.Query().Get("from") == "10001" {
			page = certChainResponse{Certs: allEntries[100:], Count: 5}
		} else if r.URL.Query().Get("from") != "0" {
			http.Error(w, "unexpected cursor", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()

	sm := &SenderManager{httpClient: server.Client()}
	certs, err := sm.FetchCertChain(strings.TrimPrefix(server.URL, "https://"), 0, 10500, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(certs) != certificateCount {
		t.Fatalf("certificates=%d want %d", len(certs), certificateCount)
	}
	if !bytes.Equal(certs[0], []byte("100")) || !bytes.Equal(certs[len(certs)-1], []byte("10500")) {
		t.Fatalf("unexpected first/last certificate: %q/%q", certs[0], certs[len(certs)-1])
	}
	if bulkRequests.Load() != 2 {
		t.Fatalf("bulk requests=%d want 2", bulkRequests.Load())
	}
}

func TestFetchCertChainBulkRejectsNonAdvancingCursor(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(certChainResponse{
			Certs: []certChainEntry{{Height: 5, Data: []byte("cert-5")}}, Count: 1,
			HasMore: true,
		})
	}))
	defer server.Close()

	sm := &SenderManager{httpClient: server.Client()}
	certs, err := sm.fetchCertChainBulk(strings.TrimPrefix(server.URL, "https://"), 0, 10, 5)
	if err == nil || !strings.Contains(err.Error(), "invalid certificate chain cursor") {
		t.Fatalf("error=%v want invalid cursor", err)
	}
	if len(certs) != 0 {
		t.Fatalf("certificates=%d want 0 after invalid cursor", len(certs))
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d want 1", requests.Load())
	}
}

func TestFetchCertChainFallsBackToPerHeight(t *testing.T) {
	var bulkRequests atomic.Int32
	var getRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cert/chain":
			bulkRequests.Add(1)
			http.Error(w, "bulk unavailable", http.StatusInternalServerError)
		case "/cert/get":
			getRequests.Add(1)
			switch r.URL.Query().Get("height") {
			case "5":
				w.Write([]byte("cert-5"))
			case "10":
				w.Write([]byte("cert-10"))
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	sm := &SenderManager{httpClient: server.Client()}
	certs, err := sm.FetchCertChain(strings.TrimPrefix(server.URL, "https://"), 0, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(certs) != 2 || !bytes.Equal(certs[0], []byte("cert-5")) || !bytes.Equal(certs[1], []byte("cert-10")) {
		t.Fatalf("certs=%q want cert-5,cert-10", certs)
	}
	if bulkRequests.Load() != 1 || getRequests.Load() != 2 {
		t.Fatalf("bulk=%d get=%d want 1,2", bulkRequests.Load(), getRequests.Load())
	}
}
