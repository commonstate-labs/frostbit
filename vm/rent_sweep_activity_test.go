package vm

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/rent"
)

func TestCompletedRentSweepEmitsExplorerActivity(t *testing.T) {
	base, owners, _ := overlayRentSchedulerFixtureCount(t, 1)
	owner := owners[0]
	probe := &overlayRentScanProbe{base: base}
	sv := newOverlayRentSchedulerStateView(probe)

	                                                                          
	                                                 
	recordUserActivity(sv, owner, &UserActivityRecord{
		Kind: ActKindRentConversion, Height: overlayRentDueHeight, Amount: "1", Token: rent.FeeToken,
	})
	runtime := rent.NewRuntime(
		rentStateViewStore{sv: sv},
		rent.WithPolicy(rent.Policy{
			Token:          rent.FeeToken,
			ExemptMin:      big.NewInt(10),
			ExemptAssets:   []rent.ExemptAsset{},
			RentPerBlock:   big.NewInt(1),
			RecycleAccount: rent.RecycleFundAccount,
		}),
		rent.WithCleaner(vmRentCleaner{sv: sv}),
	)
	if err := runtime.ProcessBlock(overlayRentDueHeight, nil); err != nil {
		t.Fatalf("process rent sweep: %v", err)
	}
	if got := runtime.LastStats().SweptAccounts; got != 1 {
		t.Fatalf("swept accounts=%d want=1", got)
	}
	if _, exists, err := sv.Get(keys.KeyAccount(owner)); err != nil || exists {
		t.Fatalf("account exists=%t err=%v after completed sweep", exists, err)
	}

	activityKey := keys.KeyUserActivity(owner, overlayRentDueHeight, 1)
	data, exists, err := sv.Get(activityKey)
	if err != nil || !exists {
		t.Fatalf("rent sweep activity exists=%t err=%v", exists, err)
	}
	var record UserActivityRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode rent sweep activity: %v", err)
	}
	if record.Kind != ActKindRentSweep || record.Height != overlayRentDueHeight || record.Ref != rent.RecycleFundAccount {
		t.Fatalf("unexpected rent sweep activity: %+v", record)
	}
	if record.Amount != "" || record.Token != "" || record.TxID != "" {
		t.Fatalf("multi-asset sweep must not claim one amount/token/tx: %+v", record)
	}

	model, err := buildBlockReadModel(&pb.Block{
		BlockHash: "block-rent-sweep",
		Header:    &pb.BlockHeader{Height: overlayRentDueHeight, PrevBlockHash: "block-before-rent-sweep"},
	}, sv.Diff())
	if err != nil {
		t.Fatalf("build rent sweep read model: %v", err)
	}
	mutations := readModelMutationMap(model)
	kindKey := keys.KeyUserActivityKindIndex(owner, ActKindRentSweep, overlayRentDueHeight, "", 1)
	if mutation := mutations[kindKey]; mutation == nil || string(mutation.Value) != activityKey {
		t.Fatalf("missing rent sweep kind projection %q: %+v", kindKey, mutation)
	}
	for _, op := range finalizedNodeDiff(sv.Diff()) {
		if strings.HasPrefix(op.Key, keys.KeyUserActivityFamilyPrefix()) {
			t.Fatalf("rent sweep activity leaked into node StateDB diff: %q", op.Key)
		}
	}
}

func TestRentSweepActivityWorkRemainsLinearAtLocal20Scale(t *testing.T) {
	const ownerCount = overlayRentDueOwnerCount
	base, _, _ := overlayRentSchedulerFixtureCount(t, ownerCount)
	probe := &overlayRentScanProbe{base: base}
	sv := newOverlayRentSchedulerStateView(probe)
	runtime := rent.NewRuntime(
		rentStateViewStore{sv: sv},
		rent.WithPolicy(rent.Policy{
			Token:          rent.FeeToken,
			ExemptMin:      big.NewInt(10),
			ExemptAssets:   []rent.ExemptAsset{},
			RentPerBlock:   big.NewInt(1),
			RecycleAccount: rent.RecycleFundAccount,
		}),
		rent.WithCleaner(vmRentCleaner{sv: sv}),
	)
	if err := runtime.ProcessBlock(overlayRentDueHeight, nil); err != nil {
		t.Fatalf("process %d rent sweeps: %v", ownerCount, err)
	}
	if got := runtime.LastStats().SweptAccounts; got != ownerCount {
		t.Fatalf("swept accounts=%d want=%d", got, ownerCount)
	}
	if got := probe.activitySeqScans; got != ownerCount {
		t.Fatalf("activity seq prefix scans=%d want exactly one per swept owner (%d)", got, ownerCount)
	}

	sweepActivities := 0
	for _, op := range sv.Diff() {
		if op.Del || !strings.HasPrefix(op.Key, keys.KeyUserActivityFamilyPrefix()) {
			continue
		}
		var record UserActivityRecord
		if json.Unmarshal(op.Value, &record) == nil && record.Kind == ActKindRentSweep {
			sweepActivities++
		}
	}
	if sweepActivities != ownerCount {
		t.Fatalf("rent sweep activities=%d want=%d", sweepActivities, ownerCount)
	}
}
