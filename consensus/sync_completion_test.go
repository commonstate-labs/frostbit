package consensus

import (
	"dex/types"
	"testing"
	"time"
)

func TestAckSyncRequestDoesNotConsumeMismatchedPeerOrRange(t *testing.T) {
	rangeKey := syncRangeKey(101, 105)
	sm := &SyncManager{
		SyncRequests: map[uint32]time.Time{7: time.Now()},
		SyncRequestMeta: map[uint32]syncRequestMeta{7: {
			Peer: "supporter", FromHeight: 101, ToHeight: 105, RangeKey: rangeKey, FrontierSessionID: 9,
		}},
		InFlightSyncRanges: map[string]time.Time{rangeKey: time.Now()},
	}

	if _, ok := sm.ackSyncRequest(types.Message{SyncID: 7, From: "attacker", FromHeight: 101, ToHeight: 105}); ok {
		t.Fatal("response from a non-supporter must not consume the request")
	}
	if len(sm.SyncRequests) != 1 || len(sm.SyncRequestMeta) != 1 || len(sm.InFlightSyncRanges) != 1 {
		t.Fatal("peer-mismatched response consumed in-flight metadata")
	}
	if _, ok := sm.ackSyncRequest(types.Message{SyncID: 7, From: "supporter", FromHeight: 101, ToHeight: 104}); ok {
		t.Fatal("range-mismatched response must not consume the request")
	}
	if len(sm.SyncRequests) != 1 || len(sm.SyncRequestMeta) != 1 || len(sm.InFlightSyncRanges) != 1 {
		t.Fatal("range-mismatched response consumed in-flight metadata")
	}
	meta, ok := sm.ackSyncRequest(types.Message{SyncID: 7, From: "supporter", FromHeight: 101, ToHeight: 105})
	if !ok || meta.FrontierSessionID != 9 {
		t.Fatalf("exact supporter response was not acknowledged: ok=%t meta=%+v", ok, meta)
	}
	if len(sm.SyncRequests) != 0 || len(sm.SyncRequestMeta) != 0 || len(sm.InFlightSyncRanges) != 0 {
		t.Fatal("acknowledged request metadata was not cleared")
	}
}

func TestAckSyncRequestRejectsLegacyUnboundMetadata(t *testing.T) {
	rangeKey := syncRangeKey(1, 1)
	sm := &SyncManager{
		SyncRequests:       map[uint32]time.Time{3: time.Now()},
		SyncRequestMeta:    map[uint32]syncRequestMeta{3: {Peer: "peer", FromHeight: 1, ToHeight: 1, RangeKey: rangeKey}},
		InFlightSyncRanges: map[string]time.Time{rangeKey: time.Now()},
	}
	if _, ok := sm.ackSyncRequest(types.Message{SyncID: 3, From: "peer", FromHeight: 1, ToHeight: 1}); ok {
		t.Fatal("response without frontier session binding must be rejected")
	}
	if len(sm.SyncRequests) != 1 {
		t.Fatal("rejected unbound response must not consume the tracked request")
	}
}
