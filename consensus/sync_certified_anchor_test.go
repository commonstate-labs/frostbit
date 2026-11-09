package consensus

import (
	"dex/logs"
	"dex/pb"
	"dex/types"
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"
)

type certifiedAnchorFetchTestTransport struct {
	blocks   map[types.NodeID]*pb.Block
	errors   map[types.NodeID]error
	requests []types.NodeID
}

func (t *certifiedAnchorFetchTestTransport) Send(types.NodeID, types.Message) error  { return nil }
func (t *certifiedAnchorFetchTestTransport) Receive() <-chan types.Message           { return nil }
func (t *certifiedAnchorFetchTestTransport) Broadcast(types.Message, []types.NodeID) {}
func (t *certifiedAnchorFetchTestTransport) SamplePeers(types.NodeID, int) []types.NodeID {
	return nil
}
func (t *certifiedAnchorFetchTestTransport) GetAllPeers(types.NodeID) []types.NodeID {
	return nil
}

func (t *certifiedAnchorFetchTestTransport) FetchStateSnapshotShards(types.NodeID, uint64) (*types.StateSnapshotShardsResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (t *certifiedAnchorFetchTestTransport) FetchStateCheckpointFiles(types.NodeID, uint64) (string, func(), error) {
	return "", nil, fmt.Errorf("not implemented")
}

func (t *certifiedAnchorFetchTestTransport) FetchBlockStateRoot(types.NodeID, uint64) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}

func (t *certifiedAnchorFetchTestTransport) FetchFinalizedBlock(peer types.NodeID, _ uint64) (*pb.Block, error) {
	t.requests = append(t.requests, peer)
	if err := t.errors[peer]; err != nil {
		return nil, err
	}
	return t.blocks[peer], nil
}

func (t *certifiedAnchorFetchTestTransport) FetchCertChain(types.NodeID, uint64, uint64, uint64) ([][]byte, error) {
	return nil, fmt.Errorf("not implemented")
}

func TestFetchCertifiedAnchorBlockFallsBackToNextPeer(t *testing.T) {
	valid := certifiedAnchorTestBlock(t)
	malformed := proto.Clone(valid).(*pb.Block)
	malformed.Header.VrfOutput = nil
	recomputeCertifiedAnchorTestID(t, malformed)

	transport := &certifiedAnchorFetchTestTransport{
		blocks: map[types.NodeID]*pb.Block{
			"peer-high-bad": malformed,
			"peer-good":     valid,
		},
		errors: make(map[types.NodeID]error),
	}
	sm := NewSyncManager(
		"self",
		transport,
		nil,
		&SyncConfig{StateSyncPeers: 2},
		nil,
		nil,
		logs.NewNodeLogger("certified-anchor-fetch-test", 0),
	)
	sm.PeerHeights["peer-high-bad"] = valid.Header.Height + 10
	sm.PeerHeights["peer-good"] = valid.Header.Height

	got, err := sm.fetchCertifiedAnchorBlock(valid.Header.Height, valid.BlockHash)
	if err != nil {
		t.Fatalf("fetchCertifiedAnchorBlock failed: %v", err)
	}
	if !proto.Equal(got, valid) {
		t.Fatalf("fetched anchor differs from valid peer payload: got=%+v want=%+v", got, valid)
	}
	if got == valid || got.Header == valid.Header {
		t.Fatal("fetchCertifiedAnchorBlock returned peer-owned protobuf instead of a defensive clone")
	}
	wantRequests := []types.NodeID{"peer-high-bad", "peer-good"}
	if len(transport.requests) != len(wantRequests) {
		t.Fatalf("anchor requests=%v, want %v", transport.requests, wantRequests)
	}
	for i := range wantRequests {
		if transport.requests[i] != wantRequests[i] {
			t.Fatalf("anchor request order=%v, want %v", transport.requests, wantRequests)
		}
	}
}

func TestFetchCertifiedAnchorBlockRejectsAllMismatchedPeers(t *testing.T) {
	valid := certifiedAnchorTestBlock(t)
	wrong := proto.Clone(valid).(*pb.Block)
	wrong.Header.Timestamp++
	recomputeCertifiedAnchorTestID(t, wrong)

	transport := &certifiedAnchorFetchTestTransport{
		blocks: map[types.NodeID]*pb.Block{"peer-wrong": wrong},
		errors: make(map[types.NodeID]error),
	}
	sm := NewSyncManager(
		"self",
		transport,
		nil,
		&SyncConfig{StateSyncPeers: 1},
		nil,
		nil,
		logs.NewNodeLogger("certified-anchor-fetch-test", 0),
	)
	sm.PeerHeights["peer-wrong"] = valid.Header.Height

	got, err := sm.fetchCertifiedAnchorBlock(valid.Header.Height, valid.BlockHash)
	if err == nil {
		t.Fatalf("mismatched peer anchor was accepted: %+v", got)
	}
	if got != nil {
		t.Fatalf("failed fetch returned an anchor: %+v", got)
	}
}

var _ stateSnapshotFetcher = (*certifiedAnchorFetchTestTransport)(nil)
