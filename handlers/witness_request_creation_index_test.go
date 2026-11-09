package handlers

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/utils"
	"dex/vm"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"google.golang.org/protobuf/proto"
)

                                                                                                            
func TestWitnessRequestCreationIsDiscoverableBeforeFinalization(t *testing.T) {
	for _, tc := range []struct {
		name    string
		balance string
		accept  bool
	}{{"prepaid", "1000", true}, {"insufficient_fee", "102", false}} {
		t.Run(tc.name, func(t *testing.T) {
			hm, database := newFrostQueryTestHandler(t)
			defer database.Close()
			sv := vm.NewStateView(database.Get, database.Scan)
			put := func(key string, value proto.Message) {
				raw, err := proto.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				sv.Set(key, raw)
			}
			pubkey, err := hex.DecodeString("03d194cd76d1ba9f139cca4af1277993c90e20ac5c817dcfe93a92810c5f9b03cb")
			if err != nil {
				t.Fatal(err)
			}
			const payer, receiver, id = "index-fee-payer", "index-receiver", "new-prepaid-request"
			guardianKey, _ := btcec.PrivKeyFromBytes([]byte{31})
			guardian := schnorr.SerializePubKey(guardianKey.PubKey())
			script, err := utils.BuildTweakedDualCustodyTaprootScriptPubKey(pubkey, receiver, guardian)
			if err != nil {
				t.Fatal(err)
			}
			put(keys.KeyFrostVaultConfig("btc", 0), &pb.FrostVaultConfig{Chain: "btc", VaultCount: 1, GuardianPubkey: guardian})
			put(keys.KeyFrostVaultState("btc", 0), &pb.FrostVaultState{Chain: "btc", Status: "ACTIVE", GroupPubkey: pubkey})
			vm.SetBalance(sv, payer, "FB", &pb.TokenBalance{Balance: tc.balance})
			tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{
				Base:        &pb.BaseMessage{TxId: id, FromAddress: payer, ExecutedHeight: 10},
				NativeChain: "BTC", NativeTxHash: strings.Repeat("b", 64), NativeScript: script,
				Amount: "12300", ReceiverAddress: receiver, RechargeFee: "103",
			}}}
			ops, receipt, runErr := (&vm.WitnessRequestTxHandler{}).DryRun(tx, sv)
			if receipt == nil || (receipt.Status == "SUCCEED") != tc.accept || (runErr == nil) != tc.accept {
				t.Fatalf("accept=%t receipt=%+v err=%v", tc.accept, receipt, runErr)
			}
			if len(ops) != 0 {
				t.Fatalf("handler returned unexpected extra writes: %d", len(ops))
			}
			payerWant, poolWant := tc.balance, "0"
			if tc.accept {
				payerWant, poolWant = "897", "103"
				_, duplicate, err := (&vm.WitnessRequestTxHandler{}).DryRun(tx, sv)
				if err != nil || duplicate == nil || duplicate.Status != "FAILED" {
					t.Fatalf("duplicate request accepted: receipt=%+v err=%v", duplicate, err)
				}
			}
			if got := vm.GetBalance(sv, payer, "FB").Balance; got != payerWant {
				t.Fatalf("payer balance=%s want=%s", got, payerWant)
			}
			pool := vm.GetBalance(sv, vm.WitnessFeePoolAccount, "FB")
			poolGot := "0"
			if pool != nil && pool.Balance != "" {
				poolGot = pool.Balance
			}
			if poolGot != poolWant {
				t.Fatalf("fee pool balance=%s want=%s", poolGot, poolWant)
			}
			for _, op := range sv.Diff() {
				if op.Del {
					database.EnqueueDel(op.Key)
				} else {
					database.EnqueueSet(op.Key, string(op.Value))
				}
			}
			if err := database.ForceFlush(); err != nil {
				t.Fatal(err)
			}
			for _, route := range []string{
				"/witness/requests", "/witness/requests?active=1", "/witness/requests?chain=btc",
				"/witness/requests?vault_id=0", "/witness/requests?chain=btc&vault_id=0",
				"/witness/requests?request_id=" + id,
			} {
				rec := httptest.NewRecorder()
				hm.HandleWitnessRequests(rec, httptest.NewRequest(http.MethodGet, route, nil))
				if rec.Code != http.StatusOK {
					t.Fatalf("%s: status=%d body=%s", route, rec.Code, rec.Body.String())
				}
				var response pb.RechargeRequestList
				if err := proto.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				want := 0
				if tc.accept {
					want = 1
				}
				if len(response.Requests) != want {
					t.Fatalf("%s: requests=%d want=%d", route, len(response.Requests), want)
				}
				if want == 1 {
					req := response.Requests[0]
					if req.RequestId != id || req.Status != pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION || req.FeeEscrow != "103" || req.ArbitrationProtocolVersion != 1 {
						t.Fatalf("%s: inconsistent request: %+v", route, req)
					}
				}
			}
		})
	}
}

func TestWitnessRequestDiscoveryRecoversMissingActiveIndex(t *testing.T) {
	hm, database := newFrostQueryTestHandler(t)
	defer database.Close()
	const id = "legacy-prepaid-without-query-index"
	enqueueProto(t, database, keys.KeyRechargeRequest(id), &pb.RechargeRequest{
		RequestId: id, NativeChain: "BTC", VaultId: 2, CreateHeight: 100,
		Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD, FeeEscrow: "103", ArbitrationProtocolVersion: 1,
	})
	if err := database.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	                                                                                         
	root := string(database.GetStateFingerprint())
	if err := database.RebuildActiveRechargeQueryIndex(); err != nil {
		t.Fatal(err)
	}
	if got := string(database.GetStateFingerprint()); got != root {
		t.Fatal("query-only repair changed the consensus state root")
	}
	for _, route := range []string{"/witness/requests", "/witness/requests?active=1", "/witness/requests?chain=btc&vault_id=2"} {
		rec := httptest.NewRecorder()
		hm.HandleWitnessRequests(rec, httptest.NewRequest(http.MethodGet, route, nil))
		var response pb.RechargeRequestList
		if rec.Code != http.StatusOK || proto.Unmarshal(rec.Body.Bytes(), &response) != nil || len(response.Requests) != 1 || response.Requests[0].RequestId != id {
			t.Fatalf("recovered request not discoverable via %s: status=%d response=%v", route, rec.Code, &response)
		}
	}
}

func TestRejectedWitnessRequestHydratesRetainedArchive(t *testing.T) {
	for _, archived := range []bool{true, false} {
		name := "pruned"
		if archived {
			name = "retained"
		}
		t.Run(name, func(t *testing.T) {
			hm, database := newFrostQueryTestHandler(t)
			defer database.Close()
			marker := &pb.RechargeRequest{
				RequestId: "rejected-after-successful-challenge", NativeChain: "BTC",
				Status: pb.RechargeRequestStatus_RECHARGE_REJECTED, CreateHeight: 10, FinalizeHeight: 90,
				ChallengeId: "challenge", FeeSettled: true,
			}
			enqueueProto(t, database, keys.KeyRechargeRequest(marker.RequestId), marker)
			database.EnqueueSet(keys.KeyRechargeQueryRecentGlobal(true, marker.FinalizeHeight, marker.RequestId), keys.KeyRechargeRequest(marker.RequestId))
			if archived {
				full := proto.Clone(marker).(*pb.RechargeRequest)
				full.SelectedWitnesses = []string{"original-witness"}
				full.Votes = []*pb.WitnessVote{{WitnessAddress: "original-witness", VoteType: pb.WitnessVoteType_VOTE_PASS}}
				full.OriginalPositions = append([]*pb.WitnessVote(nil), full.Votes...)
				enqueueProto(t, database, keys.KeyArchiveRechargeRequest(marker.RequestId), full)
			}
			if err := database.ForceFlush(); err != nil {
				t.Fatal(err)
			}
			for _, route := range []string{"/witness/requests", "/witness/requests?request_id=" + marker.RequestId} {
				rec := httptest.NewRecorder()
				hm.HandleWitnessRequests(rec, httptest.NewRequest(http.MethodGet, route, nil))
				var response pb.RechargeRequestList
				if rec.Code != http.StatusOK || proto.Unmarshal(rec.Body.Bytes(), &response) != nil || len(response.Requests) != 1 {
					t.Fatalf("%s: status=%d response=%v", route, rec.Code, &response)
				}
				got := response.Requests[0]
				if got.Status != marker.Status || got.RequestId != marker.RequestId || got.ChallengeId != marker.ChallengeId {
					t.Fatalf("changed terminal identity: %v", got)
				}
				if archived {
					if len(got.SelectedWitnesses) != 1 || got.SelectedWitnesses[0] != "original-witness" || len(got.Votes) != 1 || got.Votes[0].VoteType != pb.WitnessVoteType_VOTE_PASS || len(got.OriginalPositions) != 1 {
						t.Fatalf("rejected archive details missing: %v", got)
					}
				} else if !proto.Equal(got, marker) {
					t.Fatalf("pruned archive must return exact compact marker: %v", got)
				}
			}
		})
	}
}
