package testharness_test

import (
	"bytes"
	"dex/config"
	"dex/keys"
	"dex/liveness"
	"dex/pb"
	"dex/protocol"
	"dex/testharness"
	"fmt"
	"slices"
	"strconv"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestMinerExitBlockedDuringActiveDKG(t *testing.T) {
	h := testharness.New(t, 4)

	chain := "btc"
	vaultID := uint32(0)
	epoch := uint64(2)
	miners := h.Committee[:4]

	for _, m := range miners {
		h.SeedMinerAccount(m, "10000000000", "3200000000")
	}

	                                                 
	h.SeedVaultTransition(chain, vaultID, epoch, miners, 1, 100, 200, 300, pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340)
	                     
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epoch)
	activeIdxKey := keys.KeyFrostVaultTransitionActive(chain, vaultID, epoch)
	h.RawSet(activeIdxKey, []byte(transitionKey))

	                                                                    
	h.AdvanceHeight(5)

	                                         
	ts := h.GetTransitionState(chain, vaultID, epoch)
	ts.DkgStatus = "COMMITTING"
	tsData, _ := proto.Marshal(ts)
	h.RawSet(keys.KeyFrostVaultTransition(chain, vaultID, epoch), tsData)

	                                                              
	exitTx := testharness.BuildMinerTx(miners[0], pb.OrderOp_REMOVE, "")
	if err := h.QueueTx(exitTx); err != nil {
		t.Fatalf("queue failed: %v", err)
	}
	spec, err := h.MineBlock()
	if err != nil {
		t.Fatalf("mine block failed: %v", err)
	}

	if len(spec.Receipts) == 0 {
		t.Fatalf("expected receipt")
	}
	rc := spec.Receipts[0]
	if rc.Status != "FAILED" {
		t.Fatalf("expected miner exit to fail (active DKG), got status=%s err=%s", rc.Status, rc.Error)
	}

	acc := mustGetAccount(t, h, miners[0])
	if !acc.IsMiner {
		t.Fatalf("miner %s should still be a miner (exit rejected)", miners[0])
	}

	t.Logf("miner exit correctly rejected during active DKG: err=%s", rc.Error)
}

                                                                             
                                                                                 
func TestVaultCommitteeRotationAtFixedTerm(t *testing.T) {
	for _, currentSize := range []int{4, 8} {
		t.Run(fmt.Sprintf("members_%d", currentSize), func(t *testing.T) {
			h := testharness.New(t, currentSize+1)
			h.Exec.LivenessTracker = nil                                                             
			const chainName = "btc"
			const activation = uint64(1)
			review := activation + protocol.VaultCommitteeTermBlocks
			seedFixedTermVaultConfig(t, h, chainName)
			for _, miner := range h.Committee {
				h.SeedMinerAccount(miner, "10000000000", "3200000000")
			}
			serving := append([]string(nil), h.Committee[:currentSize]...)
			candidates := append([]string(nil), h.Committee[1:]...)
			groupKey := append([]byte(nil), h.PublicKeys[serving[0]]...)
			seedFullVaultState(t, h, chainName, 0, "ACTIVE", 1, serving, groupKey, pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340)
			seedActiveMiners(t, h, candidates, review-2)
			h.SeedHeightForTest(review - 2)
			assertHarnessHeight(t, h, review-2)

			if _, err := h.MineBlock(); err != nil {
				t.Fatalf("pre-term block: %v", err)
			}
			assertHarnessHeight(t, h, review-1)
			if transition := h.GetTransitionState(chainName, 0, 2); transition != nil {
				t.Fatalf("committee changed before the fixed term: %+v", transition)
			}
			assertServingVault(t, h, chainName, serving, groupKey, activation, review)

			if _, err := h.MineBlock(); err != nil {
				t.Fatalf("term boundary block: %v", err)
			}
			assertHarnessHeight(t, h, review)
			transition := h.GetTransitionState(chainName, 0, 2)
			threshold, err := config.ComputeVaultCommitteeThreshold(currentSize)
			if err != nil {
				t.Fatal(err)
			}
			if transition == nil || transition.DkgStatus != "NOT_STARTED" || transition.EpochId != 2 ||
				transition.TriggerHeight != review+1 || transition.DkgN != uint32(currentSize) || transition.DkgThresholdT != uint32(threshold) ||
				!slices.Equal(transition.OldCommitteeMembers, serving) || !slices.Equal(transition.NewCommitteeMembers, candidates) {
				t.Fatalf("incorrect term-boundary transition: %+v", transition)
			}
			                                                                           
			assertServingVault(t, h, chainName, serving, groupKey, activation, review)
			t.Logf("fixed term=%d, committee=%d/%d, replacement=1, real boundary blocks=2", protocol.VaultCommitteeTermBlocks, currentSize, threshold)
		})
	}
}

func TestVaultCommitteeSameMembersRenewTermWithoutRekey(t *testing.T) {
	h := testharness.New(t, 4)
	h.Exec.LivenessTracker = nil
	const chainName = "btc"
	const activation = uint64(1)
	review := activation + protocol.VaultCommitteeTermBlocks
	seedFixedTermVaultConfig(t, h, chainName)
	for _, miner := range h.Committee {
		h.SeedMinerAccount(miner, "10000000000", "3200000000")
	}
	serving := append([]string(nil), h.Committee...)
	slices.Reverse(serving)                                                   
	groupKey := append([]byte(nil), h.PublicKeys[serving[0]]...)
	seedFullVaultState(t, h, chainName, 0, "ACTIVE", 1, serving, groupKey, pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340)
	seedActiveMiners(t, h, h.Committee, review-1)
	h.SeedHeightForTest(review - 1)
	if _, err := h.MineBlock(); err != nil {
		t.Fatalf("renewal boundary block: %v", err)
	}
	assertHarnessHeight(t, h, review)
	if transition := h.GetTransitionState(chainName, 0, 2); transition != nil {
		t.Fatalf("same members unexpectedly started DKG: %+v", transition)
	}
	assertServingVault(t, h, chainName, serving, groupKey, activation, review+protocol.VaultCommitteeTermBlocks)
}

func seedFixedTermVaultConfig(t *testing.T, h *testharness.Harness, chainName string) {
	t.Helper()
	h.SeedVaultConfig(chainName, 0, 1)
	raw, ok := h.RawGet(keys.KeyFrostVaultConfig(chainName, 0))
	var cfg pb.FrostVaultConfig
	if !ok || proto.Unmarshal(raw, &cfg) != nil {
		t.Fatal("missing fixed-term vault config fixture")
	}
	cfg.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
	if err := config.ValidateOnChainVaultCommitteePolicy(&cfg); err != nil {
		t.Fatalf("invalid term policy fixture: %v", err)
	}
	data, err := proto.Marshal(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.RawSet(keys.KeyFrostVaultConfig(chainName, 0), data)
}

func assertHarnessHeight(t *testing.T, h *testharness.Harness, expected uint64) {
	t.Helper()
	raw, ok := h.RawGet(keys.KeyLatestHeight())
	if !ok || string(raw) != strconv.FormatUint(expected, 10) || h.Height() != expected {
		t.Fatalf("checkpoint height mismatch: Harness=%d latest=%q want=%d", h.Height(), raw, expected)
	}
}

func assertServingVault(t *testing.T, h *testharness.Harness, chainName string, members []string, key []byte, activation, review uint64) {
	t.Helper()
	raw, ok := h.RawGet(keys.KeyFrostVaultState(chainName, 0))
	var state pb.FrostVaultState
	if !ok || proto.Unmarshal(raw, &state) != nil {
		t.Fatal("missing serving vault")
	}
	threshold, err := config.ComputeVaultCommitteeThreshold(len(members))
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "ACTIVE" || state.KeyEpoch != 1 || state.ActiveSinceHeight != activation || state.NextCommitteeReviewHeight != review ||
		state.CommitteeThreshold != uint32(threshold) || !bytes.Equal(state.GroupPubkey, key) || !slices.Equal(state.CommitteeMembers, members) {
		t.Fatalf("serving vault changed unexpectedly: %+v", &state)
	}
}

                                                                                                                                                                 
                                                                             
func TestMinerExitUnlockBlockedByPendingHandover(t *testing.T) {
	h := testharness.New(t, 3)

	chain := "btc"
	vaultID := uint32(0)
	epoch := uint64(2)
	miner := h.Committee[0]

	                                                                       
	h.SeedMinerAccount(miner, "3000", "3200000000")

	                                                                                                    
	exitRec, err := proto.Marshal(&pb.MinerExitRecord{MinerAddress: miner, ExitHeight: 1, RewardAddress: miner})
	if err != nil {
		t.Fatal(err)
	}
	h.RawSet(keys.KeyMinerExitRecord(miner), exitRec)

	                                                                                                                  
	ts := &pb.VaultTransitionState{
		Chain:               chain,
		VaultId:             vaultID,
		EpochId:             epoch,
		OldCommitteeMembers: []string{miner, "other1", "other2"},
		NewCommitteeMembers: []string{"new1", "new2", "new3"},
		DkgStatus:           "KEY_READY",
		Lifecycle:           "DRAINING",
	}
	tsData, _ := proto.Marshal(ts)
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epoch)
	h.RawSet(transitionKey, tsData)
	h.RawSet(keys.KeyFrostVaultTransitionActive(chain, vaultID, epoch), []byte(transitionKey))

	                                                
	h.AdvanceHeight(liveness.DefaultFreezeBlocks)

	                                                             
	bal := mustGetTokenBalance(t, h, miner, "FB")
	if bal.MinerLockedBalance != "3200000000" {
		t.Fatalf("expected stake still locked due to pending handover, got locked=%s", bal.MinerLockedBalance)
	}
	if _, ok := h.RawGet(keys.KeyMinerExitRecord(miner)); !ok {
		t.Fatalf("expected MinerExitRecord still present")
	}

	                                         
	ts.Lifecycle = "ACTIVE"
	tsData, _ = proto.Marshal(ts)
	h.RawSet(transitionKey, tsData)

	                                                            
	h.AdvanceHeight(1)

	bal = mustGetTokenBalance(t, h, miner, "FB")
	if bal.MinerLockedBalance != "0" {
		t.Fatalf("expected stake released after handover complete, got locked=%s", bal.MinerLockedBalance)
	}
	if _, ok := h.RawGet(keys.KeyMinerExitRecord(miner)); ok {
		t.Fatalf("expected MinerExitRecord cleared after unlock")
	}
	if h.Liveness.IsRegistered(miner) {
		t.Fatalf("expected liveness record fully unregistered after unlock")
	}
}

                                                                                                      
                                                                                                                         
func TestKickedMinerCanRemoveToUnlockStake(t *testing.T) {
	h := testharness.New(t, 1)
	miner := h.Committee[0]

	const (
		initialFB = "3200005000"
		stakeFB   = "3200000000"
	)
	                                                                                          
	h.SeedAccount(miner, "FB", initialFB)
	h.SubmitTxExpectSuccess(h.BuildMinerTx(miner, pb.OrderOp_ADD, stakeFB))

	                                                                                           
	acc := mustGetAccount(t, h, miner)
	if !acc.IsMiner {
		t.Fatalf("prep: expected IsMiner=true after ADD")
	}
	acc.IsMiner = false
	accData, _ := proto.Marshal(acc)
	h.RawSet(keys.KeyAccount(miner), accData)

	                                                                    
	bal := mustGetTokenBalance(t, h, miner, "FB")
	if bal.MinerLockedBalance != stakeFB {
		t.Fatalf("prep: expected locked=%s, got %s", stakeFB, bal.MinerLockedBalance)
	}
	if _, ok := h.RawGet(keys.KeyMinerExitRecord(miner)); ok {
		t.Fatalf("prep: expected no exit record before REMOVE")
	}

	                                                                        
	h.SubmitTxExpectSuccess(testharness.BuildMinerTx(miner, pb.OrderOp_REMOVE, ""))
	if _, ok := h.RawGet(keys.KeyMinerExitRecord(miner)); !ok {
		t.Fatalf("expected exit record written after kicked miner REMOVE")
	}

	                                                      
	h.AdvanceHeight(liveness.DefaultFreezeBlocks)
	bal = mustGetTokenBalance(t, h, miner, "FB")
	if bal.MinerLockedBalance != "0" {
		t.Fatalf("expected locked=0 after freeze period, got %s", bal.MinerLockedBalance)
	}
	if bal.Balance != "3200003000" {                                   
		t.Fatalf("expected liquid=3200003000, got %s", bal.Balance)
	}
}

                                                                                   
                                                                                                              
func TestKickedGenesisMinerCanRemove(t *testing.T) {
	h := testharness.New(t, 1)
	miner := h.Committee[0]

	const (
		initialFB = "5000"
		stakeFB   = "3200000000"
	)
	                                                                                                 
	h.SeedMinerAccount(miner, initialFB, stakeFB)

	                                                                     
	acc := mustGetAccount(t, h, miner)
	acc.IsMiner = false
	accData, _ := proto.Marshal(acc)
	h.RawSet(keys.KeyAccount(miner), accData)

	                                                                     
	h.SubmitTxExpectSuccess(testharness.BuildMinerTx(miner, pb.OrderOp_REMOVE, ""))
	if _, ok := h.RawGet(keys.KeyMinerExitRecord(miner)); !ok {
		t.Fatalf("expected exit record written after genesis kicked miner REMOVE")
	}

	                                 
	h.AdvanceHeight(liveness.DefaultFreezeBlocks)
	bal := mustGetTokenBalance(t, h, miner, "FB")
	if bal.MinerLockedBalance != "0" {
		t.Fatalf("expected locked=0 after freeze, got %s", bal.MinerLockedBalance)
	}
	if bal.Balance != "3200004000" {                                       
		t.Fatalf("expected liquid=3200004000, got %s", bal.Balance)
	}
}

                                                                                                                                
func TestNonMinerRemoveRejected(t *testing.T) {
	h := testharness.New(t, 1)
	addr := h.Committee[0]

	                                                      
	h.SeedAccount(addr, "FB", "5000")

	removeTx := testharness.BuildMinerTx(addr, pb.OrderOp_REMOVE, "")
	if err := h.QueueTx(removeTx); err != nil {
		t.Fatalf("queue: %v", err)
	}
	spec, err := h.MineBlock()
	if err != nil {
		t.Fatalf("mine: %v", err)
	}
	if len(spec.Receipts) == 0 || spec.Receipts[0].Status != "FAILED" {
		t.Fatalf("expected non-miner REMOVE rejected, got %+v", spec.Receipts)
	}
	if spec.Receipts[0].Error != "no locked balance to release" {
		t.Fatalf("expected reason=no locked balance to release, got %q", spec.Receipts[0].Error)
	}
}

                                                                                                       
                                                         
func TestKickedMinerRemoveBlockedByPendingHandover(t *testing.T) {
	h := testharness.New(t, 1)
	miner := h.Committee[0]

	const (
		initialFB = "3200005000"
		stakeFB   = "3200000000"
	)
	h.SeedAccount(miner, "FB", initialFB)
	h.SubmitTxExpectSuccess(h.BuildMinerTx(miner, pb.OrderOp_ADD, stakeFB))

	              
	acc := mustGetAccount(t, h, miner)
	acc.IsMiner = false
	accData, _ := proto.Marshal(acc)
	h.RawSet(keys.KeyAccount(miner), accData)

	                                                                         
	chain := "btc"
	vaultID := uint32(0)
	epoch := uint64(2)
	ts := &pb.VaultTransitionState{
		Chain:               chain,
		VaultId:             vaultID,
		EpochId:             epoch,
		OldCommitteeMembers: []string{miner, "other1", "other2"},
		NewCommitteeMembers: []string{"new1", "new2", "new3"},
		DkgStatus:           "KEY_READY",
		Lifecycle:           "DRAINING",
	}
	tsData, _ := proto.Marshal(ts)
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epoch)
	h.RawSet(transitionKey, tsData)
	h.RawSet(keys.KeyFrostVaultTransitionActive(chain, vaultID, epoch), []byte(transitionKey))

	                                                                                   
	removeTx := testharness.BuildMinerTx(miner, pb.OrderOp_REMOVE, "")
	if err := h.QueueTx(removeTx); err != nil {
		t.Fatalf("queue failed: %v", err)
	}
	spec, err := h.MineBlock()
	if err != nil {
		t.Fatalf("mine: %v", err)
	}
	if len(spec.Receipts) == 0 || spec.Receipts[0].Status != "FAILED" {
		t.Fatalf("expected REMOVE to fail due to pending handover, got %+v", spec.Receipts)
	}
	if _, ok := h.RawGet(keys.KeyMinerExitRecord(miner)); ok {
		t.Fatalf("failed REMOVE unexpectedly produced an exit record")
	}
}

                    

func seedFullVaultState(t *testing.T, h *testharness.Harness, chain string, vaultID uint32,
	status string, epochID uint64, committee []string, groupPubkey []byte, signAlgo pb.SignAlgo) {
	t.Helper()
	threshold, err := config.ComputeVaultCommitteeThreshold(len(committee))
	if err != nil {
		t.Fatalf("vault state committee: %v", err)
	}
	state := &pb.FrostVaultState{
		Chain:                     chain,
		VaultId:                   vaultID,
		Status:                    status,
		KeyEpoch:                  epochID,
		CommitteeMembers:          committee,
		CommitteeThreshold:        uint32(threshold),
		GroupPubkey:               groupPubkey,
		SignAlgo:                  signAlgo,
		ActiveSinceHeight:         1,
		NextCommitteeReviewHeight: 1 + protocol.VaultCommitteeTermBlocks,
	}
	data, _ := proto.Marshal(state)
	h.RawSet(keys.KeyFrostVaultState(chain, vaultID), data)
}

func seedActiveMiners(t *testing.T, h *testharness.Harness, addrs []string, height uint64) {
	t.Helper()
	miners := &pb.FrostActiveMiners{
		Height:             height,
		UpdateHeight:       height,
		Addresses:          append([]string(nil), addrs...),
		Indices:            make([]uint64, len(addrs)),
		PublicKeys:         make([][]byte, len(addrs)),
		MaturedStakes:      make([]uint64, len(addrs)),
		ActiveSinceHeights: make([]uint64, len(addrs)),
		EligibleCount:      uint32(len(addrs)),
	}
	for i, addr := range addrs {
		index := -1
		for candidateIndex, candidate := range h.Committee {
			if candidate == addr {
				index = candidateIndex
				break
			}
		}
		if index < 0 {
			t.Fatalf("missing committee identity for %s", addr)
		}
		miners.Indices[i] = uint64(index)
		miners.PublicKeys[i] = append([]byte(nil), h.PublicKeys[addr]...)
		miners.MaturedStakes[i] = protocol.MinMinerStakeRaw
		miners.ActiveSinceHeights[i] = 1
	}
	if err := config.ValidateVaultCandidateSnapshot(miners); err != nil {
		t.Fatalf("VaultEligible fixture: %v", err)
	}
	data, _ := proto.Marshal(miners)
	h.RawSet(keys.KeyFrostActiveMiners(), data)
}
