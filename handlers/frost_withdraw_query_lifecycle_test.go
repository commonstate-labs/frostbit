package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestWithdrawQueryLifecycleKeepsOrderingAndDeduplicatesBeforeOffset(t *testing.T) {
	hm, dbm := newFrostQueryTestHandler(t)
	defer dbm.Close()
	for i, status := range []string{"SIGNED", "QUEUED", "CANCELLED", "SIGNING"} {
		id := fmt.Sprintf("request-%d", i)
		state := &pb.FrostWithdrawState{WithdrawId: id, Chain: "btc", Asset: "BTC", VaultId: 2, RequestHeight: uint64(20 - i), Seq: uint64(i + 1), Status: status}
		terminal := isFrostWithdrawTerminal(status)
		stateKey := keys.KeyFrostWithdraw(id)
		if status == "SIGNED" {
			stateKey = keys.KeyFrostPermanentWithdraw(id)
		} else if terminal {
			stateKey = keys.KeyArchiveFrostWithdraw(id)
		}
		enqueueProto(t, dbm, stateKey, state)
		for _, key := range []string{
			keys.KeyFrostWithdrawQueryGlobalState(terminal, state.RequestHeight, state.Seq, id),
			keys.KeyFrostWithdrawQueryChainState(state.Chain, terminal, state.RequestHeight, state.Seq, id),
			keys.KeyFrostWithdrawQueryChainVaultState(state.Chain, 2, terminal, state.RequestHeight, state.Seq, id),
			keys.KeyFrostWithdrawQueryGlobal(state.RequestHeight, state.Seq, id),
			keys.KeyFrostWithdrawQueryChain(state.Chain, state.RequestHeight, state.Seq, id),
			keys.KeyFrostWithdrawQueryChainVault(state.Chain, 2, state.RequestHeight, state.Seq, id),
		} {
			dbm.EnqueueSet(key, id)
		}
	}
	                                                                       
	dbm.EnqueueSet(keys.KeyFrostWithdrawQueryGlobalState(false, 99, 1, "request-0"), "request-0")
	dbm.EnqueueSet(keys.KeyFrostWithdrawQueryGlobalState(false, 20, 1, "request-0"), "request-0")
	requireFlush := func() {
		t.Helper()
		if err := dbm.ForceFlush(); err != nil {
			t.Fatal(err)
		}
	}
	requireFlush()
	for _, scope := range []string{"", "&chain=btc", "&chain=btc&vault_id=2"} {
		for _, order := range []string{"asc", "desc"} {
			var got []string
			for offset := 0; offset < 4; offset++ {
				r := httptest.NewRecorder()
				hm.HandleFrostWithdrawList(r, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/frost/withdraw/list?limit=1&offset=%d&order=%s%s", offset, order, scope), nil))
				var page pb.FrostWithdrawStateList
				if r.Code != http.StatusOK || proto.Unmarshal(r.Body.Bytes(), &page) != nil || len(page.States) != 1 {
					t.Fatalf("page=%d %s", r.Code, r.Body.String())
				}
				got = append(got, page.States[0].WithdrawId)
			}
			want := []string{"request-0", "request-1", "request-2", "request-3"}
			if order == "asc" {
				want = []string{"request-3", "request-2", "request-1", "request-0"}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s %s got=%v want=%v", scope, order, got, want)
			}
		}
	}
}
