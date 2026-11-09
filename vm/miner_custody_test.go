package vm

import (
	"dex/liveness"
	"encoding/json"
	"sort"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/utils"

	"go.dedis.ch/kyber/v3/pairing/bn256"
	"google.golang.org/protobuf/proto"
)

func newMinerTestKey(t *testing.T) *utils.KeyManager {
	t.Helper()
	km := utils.NewKeyManager()
	if err := km.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	return km
}

func minerTestBLSKey(t *testing.T, km *utils.KeyManager) []byte {
	t.Helper()
	raw, err := km.BLSPublicKeyBytes()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func buildMinerRegistration(t *testing.T, hotKey, blsKey *utils.KeyManager, reward, amount string) *pb.MinerTx {
	t.Helper()
	tx := &pb.MinerTx{
		Base: &pb.BaseMessage{
			TxId:        "miner-register",
			FromAddress: hotKey.GetAddress(),
			PublicKeys: &pb.PublicKeys{Keys: map[int32][]byte{
				int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX): hotKey.PublicKeyBytes(),
			}},
		},
		Op:            pb.OrderOp_ADD,
		Amount:        amount,
		RewardAddress: reward,
		BlsPublicKey:  minerTestBLSKey(t, blsKey),
		Endpoint:      "node.example.org:3030",
	}
	digest, err := MinerRegistrationPoPDigest(tx)
	if err != nil {
		t.Fatal(err)
	}
	tx.BlsProof, err = blsKey.SignBLS(digest)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func seedMinerTestAccount(t *testing.T, sv StateView, account *pb.Account, available, locked string) {
	t.Helper()
	raw, err := proto.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyAccount(account.Address), raw)
	SetBalance(sv, account.Address, "FB", &pb.TokenBalance{Balance: available, MinerLockedBalance: locked})
}

func applyMinerTestOps(sv StateView, ops []WriteOp) {
	for _, op := range ops {
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, op.Value)
		}
	}
}

func TestRewardAddressOfDefaultsToHotAndUsesFixedAddress(t *testing.T) {
	sv := NewMockStateView()
	hotKey := newMinerTestKey(t)
	rewardKey := newMinerTestKey(t)
	hot := hotKey.GetAddress()
	seedMinerTestAccount(t, sv, &pb.Account{Address: hot}, "0", "0")
	if got := RewardAddressOf(sv, hot); got != hot {
		t.Fatalf("fallback reward address=%s want %s", got, hot)
	}

	seedMinerTestAccount(t, sv, &pb.Account{Address: hot, RewardAddress: rewardKey.GetAddress()}, "0", "0")
	if got := RewardAddressOf(sv, hot); got != rewardKey.GetAddress() {
		t.Fatalf("fixed reward address=%s want %s", got, rewardKey.GetAddress())
	}
}

func TestResolveMinerHot(t *testing.T) {
	tx := &pb.MinerTx{Base: &pb.BaseMessage{FromAddress: "signer"}, MinerAddress: "miner"}
	if got := ResolveMinerHot(tx); got != "miner" {
		t.Fatalf("ResolveMinerHot=%s want miner", got)
	}
	tx.MinerAddress = ""
	if got := ResolveMinerHot(tx); got != "signer" {
		t.Fatalf("ResolveMinerHot fallback=%s want signer", got)
	}
}

func TestMinerRegistrationRequiresCompleteIdentityAndBLSProof(t *testing.T) {
	sv := NewMockStateView()
	hotKey := newMinerTestKey(t)
	rewardKey := newMinerTestKey(t)
	hot := hotKey.GetAddress()
	seedMinerTestAccount(t, sv, &pb.Account{Address: hot}, "6400000000", "0")

	tx := buildMinerRegistration(t, hotKey, hotKey, rewardKey.GetAddress(), "3200000000")
	ops, receipt, err := (&MinerTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_MinerTx{MinerTx: tx}}, sv)
	if err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("complete registration failed: receipt=%+v err=%v", receipt, err)
	}
	applyMinerTestOps(sv, ops)
	raw, _, _ := sv.Get(keys.KeyAccount(hot))
	var account pb.Account
	if err := proto.Unmarshal(raw, &account); err != nil {
		t.Fatal(err)
	}
	if !account.IsMiner || account.Index != 0 || account.Ip != tx.Endpoint || account.RewardAddress != rewardKey.GetAddress() {
		t.Fatalf("unexpected registered account: %+v", &account)
	}
	if got := account.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)]; string(got) != string(hotKey.PublicKeyBytes()) {
		t.Fatal("registered secp256k1 key mismatch")
	}
	if got := account.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_BLS_BN256_G2)]; string(got) != string(tx.BlsPublicKey) {
		t.Fatal("registered BLS key mismatch")
	}
	if got := GetBalance(sv, hot, "FB"); got.Balance != "3200000000" || got.MinerLockedBalance != "3200000000" {
		t.Fatalf("registration balance=%+v", got)
	}
}

func TestLegacyMinerADDForIncompleteIdentityFailsClosed(t *testing.T) {
	sv := NewMockStateView()
	hotKey := newMinerTestKey(t)
	hot := hotKey.GetAddress()
	seedMinerTestAccount(t, sv, &pb.Account{Address: hot}, "6400000000", "0")
	tx := &pb.MinerTx{
		Base: &pb.BaseMessage{TxId: "legacy-add", FromAddress: hot, PublicKeys: &pb.PublicKeys{Keys: map[int32][]byte{
			int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX): hotKey.PublicKeyBytes(),
		}}},
		Op: pb.OrderOp_ADD, Amount: "3200000000",
	}
	if _, receipt, err := (&MinerTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_MinerTx{MinerTx: tx}}, sv); err == nil || receipt.Status == "SUCCEED" {
		t.Fatalf("incomplete legacy ADD was accepted: receipt=%+v err=%v", receipt, err)
	}
	if got := GetBalance(sv, hot, "FB"); got.Balance != "6400000000" || got.MinerLockedBalance != "0" {
		t.Fatalf("failed registration changed balance: %+v", got)
	}
}

func TestMinerRegistrationRejectsStakeAboveConsensusUint64Limit(t *testing.T) {
	sv := NewMockStateView()
	hotKey := newMinerTestKey(t)
	hot := hotKey.GetAddress()
	const tooLarge = "18446744073709551616"
	seedMinerTestAccount(t, sv, &pb.Account{Address: hot}, tooLarge, "0")
	tx := buildMinerRegistration(t, hotKey, hotKey, hot, tooLarge)
	if _, receipt, err := (&MinerTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_MinerTx{MinerTx: tx}}, sv); err == nil || receipt.Status == "SUCCEED" {
		t.Fatalf("stake above uint64 was accepted: receipt=%+v err=%v", receipt, err)
	}
	if got := GetBalance(sv, hot, "FB"); got.Balance != tooLarge || got.MinerLockedBalance != "0" {
		t.Fatalf("rejected oversized stake changed balance: %+v", got)
	}
}

func TestMinerRegistrationRejectsInvalidProofAndReusedBLSKey(t *testing.T) {
	firstKey := newMinerTestKey(t)
	secondKey := newMinerTestKey(t)

	t.Run("invalid proof", func(t *testing.T) {
		sv := NewMockStateView()
		seedMinerTestAccount(t, sv, &pb.Account{Address: firstKey.GetAddress()}, "6400000000", "0")
		tx := buildMinerRegistration(t, firstKey, firstKey, firstKey.GetAddress(), "3200000000")
		tx.BlsProof[0] ^= 0xff
		if _, receipt, err := (&MinerTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_MinerTx{MinerTx: tx}}, sv); err == nil || receipt.Status == "SUCCEED" {
			t.Fatalf("invalid BLS proof was accepted: receipt=%+v err=%v", receipt, err)
		}
	})

	t.Run("reused BLS key", func(t *testing.T) {
		sv := NewMockStateView()
		sv.Set(keys.KeyMinerBLSPublicKeyOwner(minerTestBLSKey(t, firstKey)), []byte(firstKey.GetAddress()))
		seedMinerTestAccount(t, sv, &pb.Account{Address: secondKey.GetAddress()}, "6400000000", "0")
		tx := buildMinerRegistration(t, secondKey, firstKey, secondKey.GetAddress(), "3200000000")
		if _, receipt, err := (&MinerTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_MinerTx{MinerTx: tx}}, sv); err == nil || receipt.Status == "SUCCEED" {
			t.Fatalf("reused BLS key was accepted: receipt=%+v err=%v", receipt, err)
		}
	})
}

func TestMinerRegistrationRejectsIdentityBLSKeyAndProof(t *testing.T) {
	sv := NewMockStateView()
	hotKey := newMinerTestKey(t)
	seedMinerTestAccount(t, sv, &pb.Account{Address: hotKey.GetAddress()}, "6400000000", "0")
	tx := buildMinerRegistration(t, hotKey, hotKey, hotKey.GetAddress(), "3200000000")

	suite := bn256.NewSuite()
	var err error
	tx.BlsPublicKey, err = suite.G2().Point().Null().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	tx.BlsProof, err = suite.G1().Point().Null().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	if _, receipt, err := (&MinerTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_MinerTx{MinerTx: tx}}, sv); err == nil || receipt.Status == "SUCCEED" {
		t.Fatalf("identity BLS key/proof was accepted: receipt=%+v err=%v", receipt, err)
	}
	if got := GetBalance(sv, hotKey.GetAddress(), "FB"); got.Balance != "6400000000" || got.MinerLockedBalance != "0" {
		t.Fatalf("rejected identity registration changed balance: %+v", got)
	}
}

func TestSlashMinerStakeUsesIndependentHotBucket(t *testing.T) {
	sv := NewMockStateView()
	hotKey := newMinerTestKey(t)
	hot := hotKey.GetAddress()
	seedMinerTestAccount(t, sv, &pb.Account{Address: hot, IsMiner: true}, "100", "1000")

	ops, err := slashMinerStake(sv, hot, 1, "unit-test")
	if err != nil {
		t.Fatal(err)
	}
	applyMinerTestOps(sv, ops)
	if got := GetBalance(sv, hot, "FB"); got.Balance != "100" || got.MinerLockedBalance != "0" {
		t.Fatalf("hot stake was not slashed correctly: %+v", got)
	}
	if got := GetBalance(sv, DkgSlashPoolAccount, "FB").Balance; got != "1000" {
		t.Fatalf("slash pool=%s want 1000", got)
	}
	raw, _, _ := sv.Get(keys.KeyAccount(hot))
	var account pb.Account
	if err := proto.Unmarshal(raw, &account); err != nil || account.IsMiner {
		t.Fatalf("miner flag was not revoked: is_miner=%v err=%v", account.IsMiner, err)
	}
}

func TestUnsignedBlockHeaderLivenessReportsCannotMoveFundsOrKick(t *testing.T) {
	sv := NewMockStateView()
	victimKey := newMinerTestKey(t)
	attackerKey := newMinerTestKey(t)
	victim := victimKey.GetAddress()
	attacker := attackerKey.GetAddress()
	const stake = uint64(10_000)

	seedMinerTestAccount(t, sv, &pb.Account{
		Address:       victim,
		IsMiner:       true,
		RewardAddress: victim,
	}, "77", "10000")
	seedMinerTestAccount(t, sv, &pb.Account{
		Address:       attacker,
		IsMiner:       true,
		RewardAddress: attacker,
	}, "123", "10000")

	cfg := liveness.DefaultConfig()
	                                                                        
	                                                                      
	                                     
	cfg.MinStake = 100
	cfg.SlashPerReport = stake
	cfg.MinSamples = 1
	cfg.Threshold = 0
	cfg.MinActiveMiners = 0
	cfg.PauseRatio = 2
	cfg.WindowFloor = 100
	cfg.WindowCeil = 100
	tracker := liveness.NewTracker(cfg)
	victimIdx, err := tracker.RegisterMinerAtHeight(sv, victim, stake, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.RegisterMinerAtHeight(sv, attacker, stake, 0); err != nil {
		t.Fatal(err)
	}

	exec := &Executor{LivenessTracker: tracker}
	for height := uint64(1); height <= 5; height++ {
		block := &pb.Block{Header: &pb.BlockHeader{
			Height:         height,
			Miner:          attacker,
			SampledReports: []uint32{victimIdx, victimIdx, 999999},
			OfflineReports: []uint32{victimIdx, victimIdx, 999999},
		}}
		if _, changed, err := exec.processLivenessHooks(block, sv); err != nil {
			t.Fatalf("height %d: process liveness hooks: %v", height, err)
		} else if changed {
			t.Fatalf("height %d: unsigned reports changed miner membership", height)
		}
	}

	if got := GetBalance(sv, victim, "FB"); got.Balance != "77" || got.MinerLockedBalance != "10000" {
		t.Fatalf("victim funds changed by unsigned reports: %+v", got)
	}
	if got := GetBalance(sv, attacker, "FB"); got.Balance != "123" || got.MinerLockedBalance != "10000" {
		t.Fatalf("attacker received funds from unsigned reports: %+v", got)
	}
	info := tracker.GetMinerByAddress(victim)
	if info == nil || info.State != liveness.StateActive || info.Stake != stake || info.TotalSlashed != 0 || info.ExitHeight != 0 {
		t.Fatalf("victim tracker state changed by unsigned reports: %+v", info)
	}
	accountRaw, exists, err := sv.Get(keys.KeyAccount(victim))
	if err != nil || !exists {
		t.Fatalf("victim account missing: exists=%v err=%v", exists, err)
	}
	var account pb.Account
	if err := proto.Unmarshal(accountRaw, &account); err != nil {
		t.Fatal(err)
	}
	if !account.IsMiner {
		t.Fatal("unsigned reports revoked victim Account.IsMiner")
	}
	if _, exists, err := sv.Get(keys.KeyMinerExitRecord(victim)); err != nil || exists {
		t.Fatalf("unsigned reports created exit record: exists=%v err=%v", exists, err)
	}
}

func TestMinerRemoveRequiresHotSignatureAndSnapshotsRewardOnly(t *testing.T) {
	sv := NewMockStateView()
	hotKey := newMinerTestKey(t)
	rewardKey := newMinerTestKey(t)
	hot, reward := hotKey.GetAddress(), rewardKey.GetAddress()
	seedMinerTestAccount(t, sv, &pb.Account{Address: hot, IsMiner: true, RewardAddress: reward}, "0", "5000")
	handler := &MinerTxHandler{}

	bad := &pb.MinerTx{Base: &pb.BaseMessage{TxId: "bad-exit", FromAddress: reward, ExecutedHeight: 1}, Op: pb.OrderOp_REMOVE, MinerAddress: hot}
	if _, receipt, err := handler.DryRun(&pb.AnyTx{Content: &pb.AnyTx_MinerTx{MinerTx: bad}}, sv); err == nil || receipt.Status == "SUCCEED" {
		t.Fatalf("non-hot exit was accepted: receipt=%+v err=%v", receipt, err)
	}

	good := &pb.MinerTx{Base: &pb.BaseMessage{TxId: "good-exit", FromAddress: hot, ExecutedHeight: 1}, Op: pb.OrderOp_REMOVE}
	ops, receipt, err := handler.DryRun(&pb.AnyTx{Content: &pb.AnyTx_MinerTx{MinerTx: good}}, sv)
	if err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("hot exit failed: receipt=%+v err=%v", receipt, err)
	}
	var exit pb.MinerExitRecord
	found := false
	for _, op := range ops {
		if op.Key == keys.KeyMinerExitRecord(hot) {
			found = true
			if err := proto.Unmarshal(op.Value, &exit); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found || exit.MinerAddress != hot || exit.RewardAddress != reward || exit.ExitHeight != 1 {
		t.Fatalf("unexpected exit record: found=%v record=%+v", found, &exit)
	}
}

func TestMinerExitUnlockPaysActualPostSlashPrincipalToReward(t *testing.T) {
	sv := NewMockStateView()
	hotKey := newMinerTestKey(t)
	rewardKey := newMinerTestKey(t)
	hot, reward := hotKey.GetAddress(), rewardKey.GetAddress()
	seedMinerTestAccount(t, sv, &pb.Account{Address: hot, RewardAddress: reward}, "7", "125")
	rec, err := proto.Marshal(&pb.MinerExitRecord{MinerAddress: hot, ExitHeight: 1, RewardAddress: reward})
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyMinerExitRecord(hot), rec)

	(&Executor{}).processMinerExitUnlocks(&pb.Block{Header: &pb.BlockHeader{Height: 1 + MinerExitFreezeBlocks}}, sv)
	if got := GetBalance(sv, hot, "FB"); got.Balance != "7" || got.MinerLockedBalance != "0" {
		t.Fatalf("hot balance after unlock=%+v", got)
	}
	if got := GetBalance(sv, reward, "FB").Balance; got != "125" {
		t.Fatalf("reward principal=%s want actual remainder 125", got)
	}
	if _, exists, _ := sv.Get(keys.KeyMinerExitRecord(hot)); exists {
		t.Fatal("mature exit record was not deleted")
	}
}

func TestMinerExitUnlockOrdersSharedRewardActivityByExitKey(t *testing.T) {
	sv := NewMockStateView()
	hotA := newMinerTestKey(t).GetAddress()
	hotB := newMinerTestKey(t).GetAddress()
	reward := newMinerTestKey(t).GetAddress()
	seedMinerTestAccount(t, sv, &pb.Account{Address: hotA, RewardAddress: reward}, "0", "100")
	seedMinerTestAccount(t, sv, &pb.Account{Address: hotB, RewardAddress: reward}, "0", "200")
	for _, hot := range []string{hotA, hotB} {
		raw, err := proto.Marshal(&pb.MinerExitRecord{MinerAddress: hot, ExitHeight: 1, RewardAddress: reward})
		if err != nil {
			t.Fatal(err)
		}
		sv.Set(keys.KeyMinerExitRecord(hot), raw)
	}

	(&Executor{}).processMinerExitUnlocks(&pb.Block{Header: &pb.BlockHeader{Height: 1 + MinerExitFreezeBlocks}}, sv)

	exitKeys := []string{keys.KeyMinerExitRecord(hotA), keys.KeyMinerExitRecord(hotB)}
	sort.Strings(exitKeys)
	expectedHotBySeq := []string{hotA, hotB}
	if exitKeys[0] == keys.KeyMinerExitRecord(hotB) {
		expectedHotBySeq[0], expectedHotBySeq[1] = hotB, hotA
	}
	for seq, expectedHot := range expectedHotBySeq {
		raw, exists, err := sv.Get(keys.KeyUserActivity(reward, 1+MinerExitFreezeBlocks, uint32(seq)))
		if err != nil || !exists {
			t.Fatalf("activity seq %d unavailable: exists=%v err=%v", seq, exists, err)
		}
		var activity UserActivityRecord
		if err := json.Unmarshal(raw, &activity); err != nil {
			t.Fatal(err)
		}
		wantRef := "miner=" + expectedHot + " exit_height=1"
		if activity.Kind != ActKindMinerExitUnlock || activity.Ref != wantRef {
			t.Fatalf("activity seq %d = kind=%s ref=%q, want kind=%s ref=%q", seq, activity.Kind, activity.Ref, ActKindMinerExitUnlock, wantRef)
		}
	}
	if got := GetBalance(sv, reward, "FB").Balance; got != "300" {
		t.Fatalf("shared reward balance=%s want 300", got)
	}
}

func TestFrozenMinerCleanupRemovesLivenessAndPublicSlot(t *testing.T) {
	sv := NewMockStateView()
	hotKey := newMinerTestKey(t)
	hot := hotKey.GetAddress()
	seedMinerTestAccount(t, sv, &pb.Account{Address: hot, RewardAddress: hot, Index: 0}, "0", "0")
	sv.Set(keys.KeyIndexToAccount(0), []byte(keys.KeyAccount(hot)))
	cfg := liveness.DefaultConfig()
	cfg.FreezeBlocks = 3
	tracker := liveness.NewTracker(cfg)
	if _, err := tracker.RegisterMinerAtHeight(sv, hot, MinMinerStake.Uint64(), 0); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RemoveMiner(sv, hot, 10); err != nil {
		t.Fatal(err)
	}
	if err := ensureMinerExitRecord(sv, hot, 10); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{LivenessTracker: tracker}
	executor.processMinerExitUnlocks(&pb.Block{Header: &pb.BlockHeader{Height: 12}}, sv)
	if !tracker.IsRegistered(hot) {
		t.Fatal("miner was cleaned before the configured freeze elapsed")
	}
	executor.processMinerExitUnlocks(&pb.Block{Header: &pb.BlockHeader{Height: 13}}, sv)
	if tracker.IsRegistered(hot) {
		t.Fatal("mature frozen miner remained registered")
	}
	if _, exists, _ := sv.Get(keys.KeyIndexToAccount(0)); exists {
		t.Fatal("mature frozen miner public slot remained mapped")
	}
	if _, exists, _ := sv.Get(keys.KeyMinerExitRecord(hot)); exists {
		t.Fatal("mature frozen miner exit record remained")
	}
}

func TestMinerRemoveBlocksNotStartedAndKeyReadyCommitteeMembers(t *testing.T) {
	for _, status := range []string{DKGStatusNotStarted, DKGStatusKeyReady} {
		t.Run(status, func(t *testing.T) {
			sv := NewMockStateView()
			miner := newMinerTestKey(t).GetAddress()
			seedMinerTestAccount(t, sv, &pb.Account{Address: miner, IsMiner: true}, "0", "5000")
			transition := &pb.VaultTransitionState{
				Chain: "btc", VaultId: 0, EpochId: 2,
				DkgStatus: status, Lifecycle: VaultLifecycleDraining,
				OldCommitteeMembers: []string{"old-miner"}, NewCommitteeMembers: []string{miner},
			}
			transitionKey := keys.KeyFrostVaultTransition("btc", 0, 2)
			if err := setTransitionStateWithIndex(sv, transitionKey, transition); err != nil {
				t.Fatal(err)
			}
			tx := &pb.MinerTx{Base: &pb.BaseMessage{TxId: "blocked-exit", FromAddress: miner, ExecutedHeight: 10}, Op: pb.OrderOp_REMOVE}
			_, receipt, err := (&MinerTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_MinerTx{MinerTx: tx}}, sv)
			if err == nil || receipt.Status == "SUCCEED" {
				t.Fatalf("committee member exited during %s: receipt=%+v err=%v", status, receipt, err)
			}
		})
	}
}
