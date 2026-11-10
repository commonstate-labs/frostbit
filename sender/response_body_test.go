package sender

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"dex/pb"
	"dex/types"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	"google.golang.org/protobuf/proto"
)

func gzipResponseBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var body bytes.Buffer
	w := gzip.NewWriter(&body)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

type countedResponseBody struct {
	io.Reader
	read   int
	closed bool
}

func (r *countedResponseBody) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += n
	return n, err
}
func (r *countedResponseBody) Close() error { r.closed = true; return nil }

func TestResponseBodyEnforcesDecodedAndWireBudgets(t *testing.T) {
	const limit = int64(128)
	payload := bytes.Repeat([]byte("a"), int(limit))
	emptyGzip := gzipResponseBytes(t, nil)
	for _, tc := range []struct {
		name          string
		body          []byte
		encoding      string
		contentLength int64
		limit         int64
		wantErr       string
	}{
		{name: "plain exact", body: payload, limit: limit},
		{name: "gzip exact", body: gzipResponseBytes(t, payload), encoding: "gzip", limit: limit},
		{name: "gzip one byte tail", body: gzipResponseBytes(t, []byte("a")), encoding: "gzip", limit: 1},
		{name: "plain oversized", body: append(bytes.Clone(payload), 'a'), limit: limit, wantErr: "response body exceeds"},
		{name: "gzip decoded bomb", body: gzipResponseBytes(t, bytes.Repeat(payload, 8192)), encoding: "gzip", limit: limit, wantErr: "response body exceeds"},
		{name: "gzip empty members wire bomb", body: bytes.Repeat(emptyGzip, 10000), encoding: "gzip", limit: limit, wantErr: "wire limit"},
		{name: "gzip truncated", body: gzipResponseBytes(t, payload)[:15], encoding: "gzip", limit: limit, wantErr: "unexpected EOF"},
		{name: "unsupported encoding", body: payload, encoding: "br", limit: limit, wantErr: "unsupported"},
		{name: "declared huge response", body: payload, contentLength: 1 << 40, limit: limit, wantErr: "wire limit"},
		{name: "zero is not unlimited", body: payload, wantErr: "invalid response limit"},
		{name: "negative is not unlimited", body: payload, limit: -1, wantErr: "invalid response limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &countedResponseBody{Reader: bytes.NewReader(tc.body)}
			resp := &http.Response{Body: body, ContentLength: tc.contentLength, Header: make(http.Header)}
			resp.Header.Set("Content-Encoding", tc.encoding)
			got, err := readProtobufResponseBodyLimited(resp, "test", tc.limit)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got %v, want %q", err, tc.wantErr)
				}
				if got != nil {
					t.Fatal("partial oversized data escaped to caller")
				}
			} else if err != nil || len(got) != int(tc.limit) {
				t.Fatalf("len=%d err=%v", len(got), err)
			}
			if body.read > int(tc.limit+tc.limit/1000+(64<<10)+1) {
				t.Fatalf("wire read escaped budget: %d", body.read)
			}
		})
	}
	resp := &http.Response{Body: io.NopCloser(bytes.NewReader(make([]byte, pb.MaxTotalSize+1))), Header: make(http.Header)}
	if _, err := readProtobufResponseBody(resp, "default"); err == nil {
		t.Fatal("default protobuf response is unbounded")
	}
}

func TestHeightResponseRejectsBombAndPreservesValidGzip(t *testing.T) {
	valid, err := proto.Marshal(&pb.HeightResponse{LastAcceptedHeight: 42})
	if err != nil {
		t.Fatal(err)
	}
	for _, bomb := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "bomb"}[bomb], func(t *testing.T) {
			data := valid
			if bomb {
				data = bytes.Repeat([]byte("x"), int(maxControlResponseBytes+1))
			}
			compressed := gzipResponseBytes(t, data)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept-Encoding") != "gzip" {
					t.Error("gzip must be explicit to disable transparent decompression")
				}
				w.Header().Set("Content-Encoding", "gzip")
				_, _ = w.Write(compressed)
			}))
			defer server.Close()
			called := false
			err := doSendHeightQuery(&SendTask{Target: strings.TrimPrefix(server.URL, "https://"), Message: &heightQueryMessage{onSuccess: func(resp *pb.HeightResponse) {
				called = true
				if resp.LastAcceptedHeight != 42 {
					t.Errorf("height %d", resp.LastAcceptedHeight)
				}
			}}}, server.Client())
			if bomb {
				if err == nil || called {
					t.Fatalf("bomb reached callback: called=%v err=%v", called, err)
				}
			} else if err != nil || !called {
				t.Fatalf("valid response rejected: called=%v err=%v", called, err)
			}
		})
	}
}

func TestSyncRequestCarriesNodeAddressHeader(t *testing.T) {
	const nodeAddress = "miner-address"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Node-Address"); got != nodeAddress {
			t.Errorf("X-Node-Address=%q want %q", got, nodeAddress)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type=%q want application/json", got)
		}
		_ = json.NewEncoder(w).Encode(types.SyncBlocksResponse{Blocks: []types.SyncBlockBundle{{
			Height: 1, BlockID: "block-1", BlockBytes: []byte{1},
		}}})
	}))
	defer server.Close()

	err := doSendSyncRequest(&SendTask{
		Target:      strings.TrimPrefix(server.URL, "https://"),
		NodeAddress: nodeAddress,
		Message:     &syncRequestMessage{fromHeight: 1, toHeight: 1},
	}, server.Client())
	if err != nil {
		t.Fatalf("doSendSyncRequest: %v", err)
	}
}

func TestResponseErrorAndAcknowledgementBodiesAreBounded(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			compressed := gzipResponseBytes(t, bytes.Repeat([]byte("x"), int(maxErrorResponseBytes+1)))
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Encoding", "gzip")
				w.WriteHeader(status)
				_, _ = w.Write(compressed)
			}))
			defer server.Close()
			err := doSendTx(&SendTask{Target: strings.TrimPrefix(server.URL, "https://"), Message: []byte{1}}, server.Client())
			if err == nil || !strings.Contains(err.Error(), "response body exceeds") {
				t.Fatalf("unbounded response: %v", err)
			}
			if len(err.Error()) > int(maxErrorResponseBytes)+256 {
				t.Fatal("error log contains unbounded body")
			}
			if status != http.StatusOK {
				var statusErr *httpStatusError
				if !errors.As(err, &statusErr) || statusErr.statusCode != status {
					t.Fatalf("lost retry/backpressure status: %v", err)
				}
			}
		})
	}
}

func TestResponseBodyReadsHonorTaskDeadline(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message interface{}
		send    func(*SendTask, *http.Client) error
	}{
		{"height", &heightQueryMessage{}, doSendHeightQuery},
		{"sync", &syncRequestMessage{fromHeight: 1, toHeight: 1}, doSendSyncRequest},
		{"transaction", []byte{1}, doSendTx},
		{"getdata", &pullTxMessage{}, doSendGetDataWithManager},
		{"getblockbyid", &pullBlockByIDMessage{}, doSendGetBlockByID},
		{"pushquery", &pushQueryMsg{}, doSendPushQuery},
		{"service sync", &serviceSyncRequestMessage{from: 1, to: 1}, doSendServiceSyncRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Keep the body open independently of transport cancellation.
			releaseBody := make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-releaseBody
			}))
			defer server.Close()
			defer close(releaseBody)
			client := server.Client()                                                          
			start := time.Now()
			err := tc.send(&SendTask{Target: strings.TrimPrefix(server.URL, "https://"), Message: tc.message, Timeout: 100 * time.Millisecond}, client)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("read did not preserve deadline: %v", err)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("slow peer outlived request deadline")
			}
		})
	}
}

func TestHTTP3ResponseBodyReadHonorsTaskDeadline(t *testing.T) {
	certServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	tlsConfig := certServer.TLS.Clone()
	certServer.Close()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	releaseBody := make(chan struct{})
	server := &http3.Server{TLSConfig: tlsConfig, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-releaseBody
	})}
	serveDone := make(chan struct{})
	go func() { _ = server.Serve(conn); close(serveDone) }()
	defer func() { _ = server.Close(); <-serveDone }()
	transport := &http3.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableCompression: true}
	defer transport.Close()
	defer close(releaseBody)
	start := time.Now()
	err = doSendHeightQuery(&SendTask{Target: conn.LocalAddr().String(), Message: &heightQueryMessage{}, Timeout: 100 * time.Millisecond}, &http.Client{Transport: transport})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("HTTP/3 read did not preserve deadline: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("HTTP/3 slow peer outlived request deadline")
	}
}

func TestSnapshotAndBlockResponsesPreserveLargerLegalPayloads(t *testing.T) {
	block := &pb.Block{Header: &pb.BlockHeader{Height: 7, StateRoot: bytes.Repeat([]byte{1}, 32)}, ShortTxs: bytes.Repeat([]byte{2}, 512<<10)}
	blockData, err := proto.Marshal(&pb.GetBlockResponse{Block: block})
	if err != nil {
		t.Fatal(err)
	}
	blockGzip := gzipResponseBytes(t, blockData)
	chunk := bytes.Repeat([]byte{3}, int(checkpointChunkSizeLimit))
	chunkGzip := gzipResponseBytes(t, chunk)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		switch r.URL.Path {
		case "/getblock":
			_, _ = w.Write(blockGzip)
		case "/statedb/checkpoint/chunk":
			_, _ = w.Write(chunkGzip)
		case "/statedb/checkpoint/files":
			_, _ = w.Write(gzipResponseBytes(t, []byte(`{"snapshot_height":7}`)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	sm := &SenderManager{httpClient: server.Client()}
	target := strings.TrimPrefix(server.URL, "https://")
	got, err := sm.FetchFinalizedBlock(target, 7)
	if err != nil || !proto.Equal(block, got) {
		t.Fatalf("valid block rejected: %v", err)
	}
	root, err := sm.FetchBlockStateRoot(target, 7)
	if err != nil || !bytes.Equal(root, block.Header.StateRoot) {
		t.Fatalf("valid root rejected: %v", err)
	}
	gotChunk, err := sm.fetchStateCheckpointChunk(target, 7, "00001.sst", 0, checkpointChunkSizeLimit)
	if err != nil || !bytes.Equal(gotChunk, chunk) {
		t.Fatalf("valid chunk rejected: %v", err)
	}
	if _, err := sm.fetchStateCheckpointChunk(target, 7, "00001.sst", 0, 0); err == nil {
		t.Fatal("zero chunk limit accepted")
	}
	if _, err := sm.fetchStateCheckpointChunk(target, 7, "00001.sst", 0, 1); err == nil {
		t.Fatal("oversized chunk accepted")
	}
	var manifest types.StateCheckpointFilesResponse
	if err := sm.postJSON(target, "/statedb/checkpoint/files", types.StateCheckpointFilesRequest{}, &manifest); err != nil || manifest.SnapshotHeight != 7 {
		t.Fatalf("manifest: %+v err=%v", manifest, err)
	}
}

func TestSyncHTTPResponseDecodesGzipAndRejectsTrailingValues(t *testing.T) {
	valid, err := json.Marshal(types.SyncBlocksResponse{Blocks: []types.SyncBlockBundle{{Height: 1, BlockID: "block-1", BlockBytes: []byte{1}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, trailing := range []string{"", " {}"} {
		body := gzipResponseBytes(t, append(bytes.Clone(valid), trailing...))
		resp := &http.Response{Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{"Content-Encoding": []string{"gzip"}}}
		got, err := decodeSyncHTTPResponse(resp, 1, 1)
		if trailing == "" {
			if err != nil || len(got.Blocks) != 1 {
				t.Fatalf("valid sync response: %v", err)
			}
		} else if err == nil {
			t.Fatal("multiple JSON values accepted")
		}
	}
}

func TestCertificatePagesRejectTrailingJSONAndRepeatedHeights(t *testing.T) {
	for _, data := range []string{
		`{"certs":[{"height":5,"data":"AQ=="}],"count":1} {}`,
		`{"certs":[{"height":5,"data":"AQ=="},{"height":5,"data":"AQ=="}],"count":2}`,
		`{"certs":[{"height":11,"data":"AQ=="}],"count":1}`,
	} {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("limit") != "4" {
				t.Error("certificate page lost maximum-size entry budget")
			}
			_, _ = io.WriteString(w, data)
		}))
		sm := &SenderManager{httpClient: server.Client()}
		_, err := sm.fetchCertChainBulk(strings.TrimPrefix(server.URL, "https://"), 0, 10, 5)
		server.Close()
		if err == nil {
			t.Fatalf("malformed certificate page accepted: %s", data)
		}
	}
}
