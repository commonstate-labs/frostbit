package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"dex/db"
	"dex/keys"
	"dex/pb"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func witnessQueryFixture(t *testing.T, mgr *db.Manager, req *pb.RechargeRequest) {
	t.Helper()
	canonical := keys.KeyRechargeRequest(req.RequestId)
	enqueueProto(t, mgr, canonical, req)
	terminal := req.Status == pb.RechargeRequestStatus_RECHARGE_FINALIZED || req.Status == pb.RechargeRequestStatus_RECHARGE_REJECTED
	height := req.CreateHeight
	if terminal && req.FinalizeHeight != 0 {
		height = req.FinalizeHeight
	}
	for _, key := range []string{
		keys.KeyRechargeQueryRecentGlobal(terminal, height, req.RequestId),
		keys.KeyRechargeQueryRecentChain(req.NativeChain, terminal, height, req.RequestId),
		keys.KeyRechargeQueryRecentVault(req.VaultId, terminal, height, req.RequestId),
		keys.KeyRechargeQueryRecentChainVault(req.NativeChain, req.VaultId, terminal, height, req.RequestId),
	} {
		mgr.EnqueueSet(key, canonical)
	}
}

func witnessQueryPage(t *testing.T, hm *HandlerManager, query string) []*pb.RechargeRequest {
	t.Helper()
	rec := httptest.NewRecorder()
	hm.HandleWitnessRequests(rec, httptest.NewRequest(http.MethodGet, "/witness/requests"+query, nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var list pb.RechargeRequestList
	require.NoError(t, proto.Unmarshal(rec.Body.Bytes(), &list))
	return list.Requests
}

func witnessQueryIDs(requests []*pb.RechargeRequest) []string {
	ids := make([]string, 0, len(requests))
	for _, req := range requests {
		ids = append(ids, req.RequestId)
	}
	return ids
}

func TestWitnessQueryDefenseDeduplicatesStaleActiveBeforeOffsetAndKeepsRejectedArchive(t *testing.T) {
	hm, mgr := newFrostQueryTestHandler(t)
	defer mgr.Close()
	active := &pb.RechargeRequest{RequestId: "active", NativeChain: "btc", VaultId: 2, CreateHeight: 20, FinalizeHeight: 999, Status: pb.RechargeRequestStatus_RECHARGE_ARBITRATION}
	finalized := &pb.RechargeRequest{RequestId: "finalized", NativeChain: "btc", VaultId: 2, CreateHeight: 10, FinalizeHeight: 30, Status: pb.RechargeRequestStatus_RECHARGE_FINALIZED}
	rejected := &pb.RechargeRequest{RequestId: "rejected", NativeChain: "btc", VaultId: 2, CreateHeight: 15, Status: pb.RechargeRequestStatus_RECHARGE_REJECTED}
	for _, req := range []*pb.RechargeRequest{active, finalized, rejected} {
		witnessQueryFixture(t, mgr, req)
	}
	                                                                            
	for _, req := range []*pb.RechargeRequest{finalized, rejected} {
		mgr.EnqueueSet(keys.KeyRechargeQueryRecentGlobal(false, req.CreateHeight, req.RequestId), keys.KeyRechargeRequest(req.RequestId))
	}
	                                                                           
	                                                                 
	mgr.EnqueueSet(keys.KeyRechargeQueryRecentGlobal(false, active.FinalizeHeight, active.RequestId), keys.KeyRechargeRequest(active.RequestId))
	mgr.EnqueueSet(keys.KeyRechargeQueryRecentGlobal(true, finalized.CreateHeight, finalized.RequestId), keys.KeyRechargeRequest(finalized.RequestId))
	archived := proto.Clone(rejected).(*pb.RechargeRequest)
	archived.SelectedWitnesses = []string{"retained-history"}
	enqueueProto(t, mgr, keys.KeyArchiveRechargeRequest(rejected.RequestId), archived)
	require.NoError(t, mgr.ForceFlush())
	require.Equal(t, []string{"finalized", "active", "rejected"}, witnessQueryIDs(witnessQueryPage(t, hm, "?limit=10")))
	page := witnessQueryPage(t, hm, "?offset=1&limit=2")
	require.Equal(t, []string{"active", "rejected"}, witnessQueryIDs(page))
	require.Equal(t, []string{"retained-history"}, page[1].SelectedWitnesses)
	require.Equal(t, []string{"rejected", "active", "finalized"}, witnessQueryIDs(witnessQueryPage(t, hm, "?order=asc&limit=10")))
	require.Equal(t, []string{"active"}, witnessQueryIDs(witnessQueryPage(t, hm, "?active=1&limit=10")))
}

func TestWitnessQueryDefenseRejectsCanonicalIdentityAndWrongHeight(t *testing.T) {
	hm, mgr := newFrostQueryTestHandler(t)
	defer mgr.Close()
	good := &pb.RechargeRequest{RequestId: "good", NativeChain: "btc", CreateHeight: 10, Status: pb.RechargeRequestStatus_RECHARGE_ARBITRATION}
	witnessQueryFixture(t, mgr, good)
	                                                                            
	mgr.EnqueueSet(keys.KeyRechargeQueryRecentGlobal(false, 30, "missing"), keys.KeyRechargeRequest("missing"))
	mgr.EnqueueSet(keys.KeyRechargeQueryRecentGlobal(false, 29, "foreign"), keys.KeyAccount("foreign"))
	enqueueProto(t, mgr, keys.KeyRechargeRequest("alias"), &pb.RechargeRequest{RequestId: "other", CreateHeight: 28, Status: pb.RechargeRequestStatus_RECHARGE_ARBITRATION})
	mgr.EnqueueSet(keys.KeyRechargeQueryRecentGlobal(false, 28, "alias"), keys.KeyRechargeRequest("alias"))
	mgr.EnqueueSet(keys.KeyRechargeQueryRecentGlobal(false, 27, "wrong-key-id"), keys.KeyRechargeRequest("good"))
	mgr.EnqueueSet(keys.KeyRechargeQueryRecentGlobal(false, 26, "good"), keys.KeyRechargeRequest("good"))
	mgr.EnqueueSet(keys.KeyRechargeQueryRecentGlobal(true, 25, "good"), keys.KeyRechargeRequest("good"))
	require.NoError(t, mgr.ForceFlush())
	for _, query := range []string{"?limit=1", "?active=1&limit=1"} {
		require.Equal(t, []string{"good"}, witnessQueryIDs(witnessQueryPage(t, hm, query)))
	}
}

func TestWitnessQueryDefenseRejectsMisplacedChainAndVaultRows(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		badKey      func(string) string
		chain       string
		vault       uint32
	}{
		{"chain", "?chain=btc&limit=1", func(id string) string { return keys.KeyRechargeQueryRecentChain("btc", false, 30, id) }, "eth", 2},
		{"vault", "?vault_id=2&limit=1", func(id string) string { return keys.KeyRechargeQueryRecentVault(2, false, 30, id) }, "btc", 3},
		{"chain_and_vault", "?chain=btc&vault_id=2&limit=1", func(id string) string { return keys.KeyRechargeQueryRecentChainVault("btc", 2, false, 30, id) }, "eth", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hm, mgr := newFrostQueryTestHandler(t)
			defer mgr.Close()
			good := &pb.RechargeRequest{RequestId: "good", NativeChain: "BTC", VaultId: 2, CreateHeight: 10, Status: pb.RechargeRequestStatus_RECHARGE_ARBITRATION}
			witnessQueryFixture(t, mgr, good)
			enqueueProto(t, mgr, keys.KeyRechargeRequest("misplaced"), &pb.RechargeRequest{RequestId: "misplaced", NativeChain: tc.chain, VaultId: tc.vault, CreateHeight: 30, Status: pb.RechargeRequestStatus_RECHARGE_ARBITRATION})
			mgr.EnqueueSet(tc.badKey("misplaced"), keys.KeyRechargeRequest("misplaced"))
			require.NoError(t, mgr.ForceFlush())
			require.Equal(t, []string{"good"}, witnessQueryIDs(witnessQueryPage(t, hm, tc.query)))
		})
	}
}

func TestWitnessQueryDefenseActivePaginationCrossesStaleBatchesAndTerminalCursor(t *testing.T) {
	hm, mgr := newFrostQueryTestHandler(t)
	defer mgr.Close()
	terminal := &pb.RechargeRequest{RequestId: "stale-terminal", CreateHeight: 1000, FinalizeHeight: 1001, Status: pb.RechargeRequestStatus_RECHARGE_FINALIZED}
	witnessQueryFixture(t, mgr, terminal)
	for i := 0; i < 200; i++ {
		mgr.EnqueueSet(keys.KeyRechargeQueryRecentGlobal(false, uint64(1000+i), fmt.Sprintf("old-%03d", i)), keys.KeyRechargeRequest(terminal.RequestId))
	}
	for i, id := range []string{"a", "b", "c"} {
		witnessQueryFixture(t, mgr, &pb.RechargeRequest{RequestId: id, CreateHeight: uint64(100 - i*10), Status: pb.RechargeRequestStatus_RECHARGE_ARBITRATION})
	}
	                                                                        
	mgr.EnqueueSet(keys.KeyRechargeQueryRecentGlobal(false, 85, "gone"), keys.KeyRechargeRequest("gone"))
	require.NoError(t, mgr.ForceFlush())
	require.Equal(t, []string{"a", "b"}, witnessQueryIDs(witnessQueryPage(t, hm, "?active=1&limit=2")))
	enqueueProto(t, mgr, keys.KeyRechargeRequest("b"), &pb.RechargeRequest{RequestId: "b", CreateHeight: 90, FinalizeHeight: 2000, Status: pb.RechargeRequestStatus_RECHARGE_REJECTED})
	require.NoError(t, mgr.ForceFlush())
	require.Equal(t, []string{"c"}, witnessQueryIDs(witnessQueryPage(t, hm, "?active=1&after=b&limit=2")))
	require.Empty(t, witnessQueryPage(t, hm, "?active=1&after=c&limit=2"))
}

func TestWitnessQueryDefenseBoundedCorruptionReturnsErrorInsteadOfFalseExhaustion(t *testing.T) {
	hm, mgr := newFrostQueryTestHandler(t)
	defer mgr.Close()
	for i := 0; i < rechargeQueryRepairReadAllowance+2; i++ {
		id := fmt.Sprintf("missing-%04d", i)
		mgr.EnqueueSet(keys.KeyRechargeQueryRecentGlobal(false, uint64(1000+i), id), keys.KeyRechargeRequest(id))
	}
	witnessQueryFixture(t, mgr, &pb.RechargeRequest{RequestId: "behind-corruption", CreateHeight: 1, Status: pb.RechargeRequestStatus_RECHARGE_ARBITRATION})
	require.NoError(t, mgr.ForceFlush())
	for _, query := range []string{"?active=1&limit=1", "?limit=1"} {
		rec := httptest.NewRecorder()
		hm.HandleWitnessRequests(rec, httptest.NewRequest(http.MethodGet, "/witness/requests"+query, nil))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.Contains(t, rec.Body.String(), "bounded scan exhausted")
	}
}
