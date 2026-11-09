package handlers

import (
	"bytes"
	"dex/config"
	"dex/db"
	"dex/keys"
	"dex/logs"
	"dex/stats"
	"dex/types"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type snapshotHandlerTestWrite struct {
	key   string
	value []byte
}

func (w snapshotHandlerTestWrite) GetKey() string   { return w.key }
func (w snapshotHandlerTestWrite) GetValue() []byte { return w.value }
func (snapshotHandlerTestWrite) IsDel() bool        { return false }

func newSnapshotHandlerWithoutCheckpoint(t *testing.T, height uint64) (*HandlerManager, *db.Manager, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Database.StateDB.CheckpointEvery = 1000
	mgr, err := db.NewManagerWithConfig(root, logs.NewNodeLogger("snapshot-handler-test", 0), cfg)
	if err != nil {
		t.Fatalf("NewManagerWithConfig: %v", err)
	}
	t.Cleanup(mgr.Close)

	accepted, err := mgr.SyncStateUpdates(height, []interface{}{snapshotHandlerTestWrite{
		key:   keys.KeyAccount("snapshot-handler-test"),
		value: []byte("live-state"),
	}})
	if err != nil {
		t.Fatalf("SyncStateUpdates: %v", err)
	}
	if accepted != 1 {
		t.Fatalf("accepted state updates = %d, want 1", accepted)
	}
	if _, _, err := mgr.ListStateCheckpointFiles(height); err == nil {
		t.Fatalf("test setup unexpectedly has checkpoint at height %d", height)
	}

	cfg.Network.AdvertiseHost = "127.0.0.1"
	return &HandlerManager{dbManager: mgr, Stats: stats.NewStats(), cfg: cfg}, mgr, root
}

func assertCheckpointWasNotCreated(t *testing.T, root string, height uint64) {
	t.Helper()
	base := filepath.Join(root, "state", "checkpoints", fmt.Sprintf("h_%020d", height))
	for _, path := range []string{base, base + ".manifest.json"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("HTTP handler created checkpoint artifact %s (stat err=%v)", path, err)
		}
	}
}

func TestHandleStateSnapshotShardsDoesNotCreateMissingCheckpoint(t *testing.T) {
	const height = uint64(7)
	hm, _, root := newSnapshotHandlerWithoutCheckpoint(t, height)
	body, err := json.Marshal(types.StateSnapshotShardsRequest{TargetHeight: height})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/statedb/snapshot/shards", bytes.NewReader(body))
	request.RemoteAddr = "127.0.0.1:12345"
	hm.HandleStateSnapshotShards(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var response types.StateSnapshotShardsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.SnapshotHeight != height || response.Error == "" {
		t.Fatalf("response = %+v, want height %d with retryable protocol error", response, height)
	}
	assertCheckpointWasNotCreated(t, root, height)
}

func TestHandleStateCheckpointFilesDoesNotCreateMissingCheckpoint(t *testing.T) {
	const height = uint64(8)
	hm, _, root := newSnapshotHandlerWithoutCheckpoint(t, height)
	body, err := json.Marshal(types.StateCheckpointFilesRequest{TargetHeight: height})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/statedb/checkpoint/files", bytes.NewReader(body))
	request.RemoteAddr = "127.0.0.1:12345"
	hm.HandleStateCheckpointFiles(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var response types.StateCheckpointFilesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.SnapshotHeight != height || response.Error == "" {
		t.Fatalf("response = %+v, want height %d with retryable protocol error", response, height)
	}
	assertCheckpointWasNotCreated(t, root, height)
}

func TestMissingStateArchiveDataDoesNotConsumeServiceEntitlement(t *testing.T) {
	const height = uint64(9)
	serviceHandler, store, remote := newArchiveServicePeerHandler(t)
	hm, _, _ := newSnapshotHandlerWithoutCheckpoint(t, height)
	hm.senderManager = serviceHandler.senderManager

	tests := []struct {
		name string
		path string
		body any
	}{
		{
			name: "snapshot shards",
			path: "/statedb/snapshot/shards",
			body: types.StateSnapshotShardsRequest{TargetHeight: height},
		},
		{
			name: "checkpoint files",
			path: "/statedb/checkpoint/files",
			body: types.StateCheckpointFilesRequest{TargetHeight: height},
		},
		{
			name: "checkpoint chunk",
			path: "/statedb/checkpoint/chunk",
			body: types.StateCheckpointChunkRequest{
				TargetHeight: height,
				Name:         fmt.Sprintf("h_%020d.manifest.json", height),
				Limit:        1,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(test.body)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(body))
			request.RemoteAddr = "1.1.1.1:23456"
			recorder := httptest.NewRecorder()
			switch test.path {
			case "/statedb/snapshot/shards":
				hm.HandleStateSnapshotShards(recorder, request)
			case "/statedb/checkpoint/files":
				hm.HandleStateCheckpointFiles(recorder, request)
			case "/statedb/checkpoint/chunk":
				hm.HandleStateCheckpointChunk(recorder, request)
			}
			peer, _ := store.Get(remote.NodeID)
			if !peer.BootstrapStartedAt.IsZero() {
				t.Fatalf("missing %s consumed bootstrap entitlement: status=%d body=%q", test.name, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestBoundedArchiveChunkBufferRejectsOverflow(t *testing.T) {
	buffer := &boundedArchiveChunkBuffer{limit: 4}
	if _, err := buffer.Write([]byte("1234")); err != nil {
		t.Fatalf("bounded buffer rejected exact limit: %v", err)
	}
	if _, err := buffer.Write([]byte("5")); err == nil {
		t.Fatal("bounded buffer accepted data beyond its limit")
	}
}
