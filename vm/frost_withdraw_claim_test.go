package vm

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/keys"
	"dex/pb"
	statedb "dex/stateDB"

	"google.golang.org/protobuf/proto"
)

type claimFlowFixture struct {
	jobID        string
	coordinator  string
	templateHash []byte
	templateData []byte
	batchSeq     string
}

type claimGetManyCountingStateView struct {
	StateView
	calls int
	keys  int
}

func (s *claimGetManyCountingStateView) GetMany(stateKeys []string) (map[string][]byte, error) {
	s.calls++
	s.keys += len(stateKeys)
	return s.StateView.(interface {
		GetMany([]string) (map[string][]byte, error)
	}).GetMany(stateKeys)
}

func seedClaimFlowState(t *testing.T, base map[string][]byte, withdrawID string) *claimFlowFixture {
	t.Helper()
	put := func(key string, msg proto.Message) { base[key] = mustMarshalProto(t, msg) }
	put(keys.KeyFrostWithdraw(withdrawID), &pb.FrostWithdrawState{
		WithdrawId: withdrawID, Seq: 1, LiveSeq: 1, Chain: "eth", Asset: "USDT", VaultId: 0,
		To: "0x1111111111111111111111111111111111111111", Amount: "10",
		GrossAmount: "10", FeeAmount: "1", NetAmount: "9", FeeVersion: 1,
		RequestHeight: 100, Status: "QUEUED",
	})
	put(keys.KeyToken("USDT"), &pb.Token{Address: "USDT", Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: "eth"})
	base[keys.KeyFrostWithdrawLiveHead("eth", 0)] = []byte("1")
	base[keys.KeyFrostWithdrawLiveTail("eth", 0)] = []byte("1")
	base[keys.KeyFrostWithdrawLivePending("eth", 0)] = []byte("1")
	base[keys.KeyFrostWithdrawLiveIndex("eth", 0, 1)] = []byte(withdrawID)
	base[keys.KeyFrostVaultAvailableBalance("eth", "USDT", 0)] = []byte("0")
	base[keys.KeyFrostVaultReservedBalance("eth", "USDT", 0)] = []byte("10")
	put(keys.KeyFrostVaultConfig("eth", 0), &pb.FrostVaultConfig{AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: accountGuardianTestKey(t),
		Chain: "eth", VaultCount: 1, VaultRefs: []string{"0x00000000000000000000000000000000000000aa"},
	})
	put(keys.KeyFrostVaultState("eth", 0), &pb.FrostVaultState{
		Chain: "eth", VaultId: 0, KeyEpoch: 1, Status: "ACTIVE",
		SignAlgo:         pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		GroupPubkey:      curve.NewBN256Group().SerializePoint(curve.NewBN256Group().ScalarBaseMult(accountTestSecret(true))),
		CommitteeMembers: []string{"coordinator"},
	})

	template, firstSeq, err := buildCanonicalAccountTemplate(newMemoryStateView(base), "eth", "", 0, 1, []string{withdrawID})
	if err != nil {
		t.Fatalf("build template: %v", err)
	}
	jobID := chain.ComputeWithdrawJobID("eth", "", 0, firstSeq, template.TemplateHash, 1)
	coordinator, err := chain.WithdrawCoordinator(jobID, 1, []string{"coordinator"})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := evm.FromJSON(template.TemplateData)
	if err != nil {
		t.Fatal(err)
	}
	return &claimFlowFixture{jobID: jobID, coordinator: coordinator, templateHash: template.TemplateHash, templateData: template.TemplateData, batchSeq: decoded.BatchSeq}
}

func mixedClaimTx(fixture *claimFlowFixture, withdrawID, txID string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: &pb.FrostWithdrawClaimTx{
		Base:  &pb.BaseMessage{TxId: txID, FromAddress: fixture.coordinator, ExecutedHeight: 200},
		JobId: fixture.jobID, WithdrawIds: []string{withdrawID}, VaultId: 0, KeyEpoch: 1,
		TemplateHash: fixture.templateHash, TemplateData: fixture.templateData,
		Chain: "eth", Asset: "", BatchSeq: fixture.batchSeq,
	}}}
}

func mixedSignedTx(t *testing.T, base map[string][]byte, fixture *claimFlowFixture, withdrawID, txID string) *pb.AnyTx {
	t.Helper()
	return &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
		Base:  &pb.BaseMessage{TxId: txID, ExecutedHeight: 201},
		JobId: fixture.jobID, GuardianSigs: accountGuardianTemplateSignature(t, fixture.templateData), SignedPackageBytes: SignAccountClaimForTest(t, base, "eth", "USDT", 0, []string{withdrawID}),
		WithdrawIds: []string{withdrawID}, VaultId: 0, KeyEpoch: 1,
		TemplateHash: fixture.templateHash, TemplateData: fixture.templateData,
		Chain: "eth", Asset: "", BatchSeq: fixture.batchSeq,
	}}}
}

func TestFrostWithdrawQueuedBeforeRotationWaitsForNewAuthorityWhileVaultDrains(t *testing.T) {
	base := map[string][]byte{}
	withdrawID := "claim_during_drain"
	fixture := seedClaimFlowState(t, base, withdrawID)
	base[keys.KeyFrostVaultTransition("eth", 0, 2)] = mustMarshalProto(t, &pb.VaultTransitionState{
		Chain: "eth", VaultId: 0, EpochId: 2, Lifecycle: VaultLifecycleDraining,
	})
	sv := newMemoryStateView(base)
	if _, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(mixedClaimTx(fixture, withdrawID, "claim_during_drain_tx"), sv); err == nil || receipt.Status != "FAILED" || receipt.Error != "vault is draining; new old-key withdraw claims are paused" {
		t.Fatalf("queued request was claimed with the draining old authority: err=%v receipt=%+v", err, receipt)
	}
	if active, found, _ := sv.Get(keys.KeyFrostWithdrawActiveVaultJob("eth", 0)); found || len(active) != 0 {
		t.Fatalf("rejected draining claim published an active job: %q", active)
	}
}

func TestFrostWithdrawMixedClaimAgeBoundary(t *testing.T) {
	for _, tc := range []struct {
		name           string
		requestHeight  uint64
		executedHeight uint64
		wantSuccess    bool
	}{
		{name: "age 99 rejected", requestHeight: 100, executedHeight: 199},
		{name: "age 100 accepted", requestHeight: 100, executedHeight: 200, wantSuccess: true},
		{name: "future height rejected", requestHeight: 201, executedHeight: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := map[string][]byte{}
			withdrawID := "claim-age-boundary"
			fixture := seedClaimFlowState(t, base, withdrawID)
			var state pb.FrostWithdrawState
			if err := proto.Unmarshal(base[keys.KeyFrostWithdraw(withdrawID)], &state); err != nil {
				t.Fatal(err)
			}
			state.RequestHeight = tc.requestHeight
			base[keys.KeyFrostWithdraw(withdrawID)] = mustMarshalProto(t, &state)
			claim := mixedClaimTx(fixture, withdrawID, "claim-age-boundary-tx")
			claim.GetFrostWithdrawClaimTx().Base.ExecutedHeight = tc.executedHeight
			sv := newMemoryStateView(base)
			ops, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(claim, sv)
			if tc.wantSuccess {
				if err != nil || receipt.Status != "SUCCEED" || len(ops) == 0 {
					t.Fatalf("age boundary claim failed: ops=%d err=%v receipt=%+v", len(ops), err, receipt)
				}
				return
			}
			if err == nil || receipt.Status != "FAILED" || receipt.Error != "mixed withdrawal batch has not reached count or age trigger" || len(ops) != 0 {
				t.Fatalf("immature claim was accepted: ops=%d err=%v receipt=%+v", len(ops), err, receipt)
			}
			if len(sv.Diff()) != 0 {
				t.Fatalf("rejected age-boundary claim changed state: %+v", sv.Diff())
			}
		})
	}
}

func TestFrostWithdrawMixedClaimImmatureReadsOnlyCanonicalLiveHead(t *testing.T) {
	base := map[string][]byte{}
	const firstID = "claim-wait-head"
	fixture := seedClaimFlowState(t, base, firstID)
	for seq := uint64(2); seq <= 63; seq++ {
		id := fmt.Sprintf("claim-wait-%02d", seq)
		base[keys.KeyFrostWithdrawLiveIndex("eth", 0, seq)] = []byte(id)
		base[keys.KeyFrostWithdraw(id)] = mustMarshalProto(t, &pb.FrostWithdrawState{
			WithdrawId: id, LiveSeq: seq, Chain: "eth", Asset: "USDT", VaultId: 0,
			To: fmt.Sprintf("0x%040x", seq), Amount: "10", GrossAmount: "10",
			FeeAmount: "1", NetAmount: "9", FeeVersion: 1, RequestHeight: 100, Status: "QUEUED",
		})
	}
	base[keys.KeyFrostWithdrawLiveTail("eth", 0)] = []byte("63")
	base[keys.KeyFrostWithdrawLivePending("eth", 0)] = []byte("63")
	underlying := newMemoryStateView(base)
	counting := &claimGetManyCountingStateView{StateView: underlying}
	claim := mixedClaimTx(fixture, firstID, "claim-wait-too-early")
	claim.GetFrostWithdrawClaimTx().Base.ExecutedHeight = 199

	ops, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(claim, counting)
	if err == nil || receipt.Status != "FAILED" || receipt.Error != "mixed withdrawal batch has not reached count or age trigger" || len(ops) != 0 {
		t.Fatalf("immature Claim result: ops=%d err=%v receipt=%+v", len(ops), err, receipt)
	}
	                                                                 
	if counting.calls != 3 || counting.keys != 3+63+1 {
		t.Fatalf("immature Claim read beyond canonical live head: calls=%d keys=%d", counting.calls, counting.keys)
	}
	if len(underlying.Diff()) != 0 {
		t.Fatalf("immature Claim changed consensus state: %+v", underlying.Diff())
	}
}

func TestReadMixedClaimPrefixLargeOverlayTombstonesIsLinear(t *testing.T) {
	const (
		tombstones = 4096
		live       = 64
	)
	base := make(map[string][]byte, tombstones+live*2)
	for seq := uint64(1); seq <= tombstones; seq++ {
		base[keys.KeyFrostWithdrawLiveIndex("eth", 0, seq)] = []byte(fmt.Sprintf("cancelled-%d", seq))
	}
	for i := uint64(1); i <= live; i++ {
		seq := uint64(tombstones) + i
		id := fmt.Sprintf("claim-live-%02d", i)
		base[keys.KeyFrostWithdrawLiveIndex("eth", 0, seq)] = []byte(id)
		base[keys.KeyFrostWithdraw(id)] = mustMarshalProto(t, &pb.FrostWithdrawState{
			WithdrawId: id, Chain: "eth", VaultId: 0, LiveSeq: seq, Status: "QUEUED",
		})
	}
	read := func(key string) ([]byte, error) {
		return append([]byte(nil), base[key]...), nil
	}
	baseBatchCalls := 0
	baseBatchKeys := 0
	batchRead := func(stateKeys []string) (map[string][]byte, error) {
		baseBatchCalls++
		baseBatchKeys += len(stateKeys)
		out := make(map[string][]byte, len(stateKeys))
		for _, key := range stateKeys {
			if value, exists := base[key]; exists {
				out[key] = append([]byte(nil), value...)
			}
		}
		return out, nil
	}
	sv := NewStateViewWithLimitScanOrderedAndBatch(read, batchRead, nil, nil, nil, nil, nil)
	for seq := uint64(1); seq <= tombstones; seq++ {
		sv.Del(keys.KeyFrostWithdrawLiveIndex("eth", 0, seq))
	}
	counting := &claimGetManyCountingStateView{StateView: sv}

	withdraws, err := readMixedClaimPrefix(counting, "eth", 0, 1, tombstones+live, live)
	if err != nil {
		t.Fatal(err)
	}
	if len(withdraws) != live || withdraws[0].WithdrawID != "claim-live-01" || withdraws[live-1].WithdrawID != "claim-live-64" {
		t.Fatalf("wrong live prefix after overlay tombstones: %+v", withdraws)
	}
	if counting.calls != 34 || counting.keys != tombstones+live+live {
		t.Fatalf("claim scan did not advance in fixed monotonic pages: calls=%d keys=%d", counting.calls, counting.keys)
	}
	if baseBatchCalls != 2 || baseBatchKeys != live+live {
		t.Fatalf("overlay tombstones leaked into base reads: calls=%d keys=%d", baseBatchCalls, baseBatchKeys)
	}
}

func TestFrostWithdrawMixedClaimThenSigned(t *testing.T) {
	base := map[string][]byte{}
	withdrawID := "claim_wd_1"
	fixture := seedClaimFlowState(t, base, withdrawID)
	sv := newMemoryStateView(base)
	handler := &FrostWithdrawClaimTxHandler{}

	tampered := mixedClaimTx(fixture, withdrawID, "claim_tampered")
	tampered.GetFrostWithdrawClaimTx().TemplateData = []byte("caller-controlled")
	if ops, receipt, err := handler.DryRun(tampered, sv); err == nil || receipt.Status != "FAILED" || len(ops) != 0 {
		t.Fatalf("caller template mutation accepted: ops=%d err=%v receipt=%+v", len(ops), err, receipt)
	}
	if _, receipt, err := handler.DryRun(mixedClaimTx(fixture, withdrawID, "claim_tx_1"), sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("claim failed: err=%v receipt=%+v", err, receipt)
	}
	jobData, found, _ := sv.Get(keys.KeyFrostWithdrawJob(fixture.jobID))
	var claimed pb.FrostWithdrawJobState
	if !found || proto.Unmarshal(jobData, &claimed) != nil || claimed.Asset != "" || claimed.BatchSeq != "1" || !bytes.Equal(claimed.TemplateData, fixture.templateData) {
		t.Fatalf("invalid finalized mixed Claim: %+v", &claimed)
	}
	if available, _, _ := sv.Get(keys.KeyFrostVaultAvailableBalance("eth", "USDT", 0)); string(available) != "0" {
		t.Fatalf("Claim changed request-time available reservation: %q", available)
	}
	if reserved, _, _ := sv.Get(keys.KeyFrostVaultReservedBalance("eth", "USDT", 0)); string(reserved) != "10" {
		t.Fatalf("Claim changed request-time reserved balance: %q", reserved)
	}

	if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(mixedSignedTx(t, base, fixture, withdrawID, "signed_tx_1"), sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("signed failed: err=%v receipt=%+v", err, receipt)
	}
	if _, exists, _ := sv.Get(keys.KeyFrostWithdrawJob(fixture.jobID)); exists {
		t.Fatal("hot job marker was not pruned")
	}
	assertPermanentWithdrawalDelivery(t, sv, withdrawID, fixture.jobID)
	if reserved, _, _ := sv.Get(keys.KeyFrostVaultReservedBalance("eth", "USDT", 0)); string(reserved) != "0" {
		t.Fatalf("Signed did not consume the reservation: %q", reserved)
	}
	if next, _, _ := sv.Get(keys.KeyFrostVaultDEXNextBatchSeq("eth", 0)); string(next) != "2" {
		t.Fatalf("Signed did not atomically advance batch seq: %q", next)
	}
}

func TestFrostWithdrawMixedClaimRestartThenSigned(t *testing.T) {
	base := map[string][]byte{}
	withdrawID := "claim_restart_wd"
	fixture := seedClaimFlowState(t, base, withdrawID)
	claimView := newMemoryStateView(base)
	if _, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(mixedClaimTx(fixture, withdrawID, "claim_restart_tx"), claimView); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("claim before restart failed: err=%v receipt=%+v", err, receipt)
	}
	restartedBase := make(map[string][]byte, len(base)+len(claimView.Diff()))
	for key, value := range base {
		restartedBase[key] = append([]byte(nil), value...)
	}
	for _, op := range claimView.Diff() {
		if op.Del {
			delete(restartedBase, op.Key)
		} else {
			restartedBase[op.Key] = append([]byte(nil), op.Value...)
		}
	}
	restarted := newMemoryStateView(restartedBase)
	if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(mixedSignedTx(t, restartedBase, fixture, withdrawID, "signed_after_restart"), restarted); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("Signed after restart failed: err=%v receipt=%+v", err, receipt)
	}
}

func TestFrostWithdrawMixedClaimIdempotencyAndCoordinator(t *testing.T) {
	base := map[string][]byte{}
	withdrawID := "claim_replay_wd"
	fixture := seedClaimFlowState(t, base, withdrawID)
	sv := newMemoryStateView(base)
	handler := &FrostWithdrawClaimTxHandler{}
	claim := mixedClaimTx(fixture, withdrawID, "claim_original")
	if _, receipt, err := handler.DryRun(claim, sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatal(err)
	}
	if ops, receipt, err := handler.DryRun(proto.Clone(claim).(*pb.AnyTx), sv); err != nil || receipt.Status != "SUCCEED" || len(ops) != 0 {
		t.Fatalf("coordinator retry was not idempotent: ops=%d err=%v receipt=%+v", len(ops), err, receipt)
	}
	before := sv.Diff()
	attacker := proto.Clone(claim).(*pb.AnyTx)
	attacker.GetFrostWithdrawClaimTx().Base.FromAddress = "attacker"
	if ops, receipt, err := handler.DryRun(attacker, sv); err == nil || receipt.Status != "FAILED" || len(ops) != 0 || !strings.Contains(receipt.Error, "not coordinator") {
		t.Fatalf("non-coordinator replay did not fail closed: ops=%d err=%v receipt=%+v", len(ops), err, receipt)
	}
	if !reflect.DeepEqual(before, sv.Diff()) {
		t.Fatal("non-coordinator replay changed state")
	}
}

func TestFrostWithdrawMixedClaimTreatsStateDBNotFoundAsMissing(t *testing.T) {
	base := map[string][]byte{}
	withdrawID := "claim_missing_job"
	fixture := seedClaimFlowState(t, base, withdrawID)
	sv := newStateDBNotFoundJobStateView(base, keys.KeyFrostWithdrawJob(fixture.jobID))
	if _, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(mixedClaimTx(fixture, withdrawID, "claim_missing_job_tx"), sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("claim should create missing job state: err=%v receipt=%+v", err, receipt)
	}
}

func TestFrostWithdrawMixedSignedRejectsDifferentJobAndMissingReservation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(StateView, *pb.FrostWithdrawSignedTx)
		want   string
	}{
		{name: "different job", tamper: func(_ StateView, signed *pb.FrostWithdrawSignedTx) { signed.JobId = "other-job" }, want: "Claim is missing"},
		{name: "missing token reservation", tamper: func(sv StateView, _ *pb.FrostWithdrawSignedTx) {
			sv.Del(keys.KeyFrostVaultReservedBalance("eth", "USDT", 0))
		}, want: "reservation is insufficient"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := map[string][]byte{}
			withdrawID := "guard-" + strings.ReplaceAll(tc.name, " ", "-")
			fixture := seedClaimFlowState(t, base, withdrawID)
			sv := newMemoryStateView(base)
			if _, _, err := (&FrostWithdrawClaimTxHandler{}).DryRun(mixedClaimTx(fixture, withdrawID, "claim-guard"), sv); err != nil {
				t.Fatal(err)
			}
			signed := mixedSignedTx(t, base, fixture, withdrawID, "signed-guard").GetFrostWithdrawSignedTx()
			tc.tamper(sv, signed)
			if ops, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed}}, sv); err == nil || receipt.Status != "FAILED" || len(ops) != 0 || !strings.Contains(receipt.Error, tc.want) {
				t.Fatalf("invalid Signed did not fail closed: ops=%d err=%v receipt=%+v", len(ops), err, receipt)
			}
		})
	}
}

func newStateDBNotFoundJobStateView(base map[string][]byte, missingJobKey string) StateView {
	readFn := func(key string) ([]byte, error) {
		if key == missingJobKey {
			return nil, statedb.ErrNotFound
		}
		v, ok := base[key]
		if !ok {
			return nil, nil
		}
		return append([]byte(nil), v...), nil
	}
	scanFn := func(prefix string) (map[string][]byte, error) {
		out := make(map[string][]byte)
		for key, value := range base {
			if strings.HasPrefix(key, prefix) {
				out[key] = append([]byte(nil), value...)
			}
		}
		return out, nil
	}
	return NewStateView(readFn, scanFn)
}
