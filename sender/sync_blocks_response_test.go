package sender

import (
	"bytes"
	"dex/config"
	"dex/logs"
	"dex/types"
	"encoding/json"
	"strings"
	"testing"
)

func TestSendSyncBlocksRequestReturnsQueueRejection(t *testing.T) {
	sq := &SendQueue{
		controlQueue: newControlTaskQueue(1),
		Logger:       logs.NewNodeLogger("sync-block-admission-test", 0),
		peerHealth:   newPeerHealthTracker(),
	}
	sq.controlQueue.push(&SendTask{})
	sm := &SenderManager{
		address:      "local",
		SendQueue:    sq,
		cfg:          config.DefaultConfig(),
		minerIPCache: make(map[string]string),
	}
	if err := sm.SendSyncBlocksRequest("127.0.0.1:65535", 10, 11, nil); err == nil || !strings.Contains(err.Error(), "not admitted") {
		t.Fatalf("expected queue admission error, got %v", err)
	}
}

func TestDecodeSyncBlocksResponseRequiresCompleteContiguousRange(t *testing.T) {
	encoded, err := json.Marshal(types.SyncBlocksResponse{Blocks: []types.SyncBlockBundle{
		{Height: 10, BlockID: "block-10", BlockBytes: []byte{1}},
		{Height: 12, BlockID: "block-12", BlockBytes: []byte{2}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeSyncBlocksResponse(bytes.NewReader(encoded), 10, 11); err == nil || !strings.Contains(err.Error(), "expected 11") {
		t.Fatalf("expected non-contiguous height error, got %v", err)
	}
}

func TestDecodeSyncBlocksResponseRejectsOversizedBlockPayload(t *testing.T) {
	encoded, err := json.Marshal(types.SyncBlocksResponse{Blocks: []types.SyncBlockBundle{
		{Height: 10, BlockID: "block-10", BlockBytes: make([]byte, maxSyncBlockPayloadBytes+1)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeSyncBlocksResponse(bytes.NewReader(encoded), 10, 10); err == nil || !strings.Contains(err.Error(), "block payload size") {
		t.Fatalf("expected oversized block payload error, got %v", err)
	}
}

func TestDecodeSyncBlocksResponseStopsBeforeExtraBundle(t *testing.T) {
	encoded, err := json.Marshal(types.SyncBlocksResponse{Blocks: []types.SyncBlockBundle{
		{Height: 10, BlockID: "block-10", BlockBytes: []byte{1}},
		{Height: 11, BlockID: "block-11", BlockBytes: []byte{2}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeSyncBlocksResponse(bytes.NewReader(encoded), 10, 10); err == nil || !strings.Contains(err.Error(), "more than 1 bundles") {
		t.Fatalf("expected extra bundle rejection, got %v", err)
	}
}
