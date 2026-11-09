package vm

import (
	"bytes"
	"dex/formal/itftrace"
	"dex/formal/refinement"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"math"
	"os"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"
)

                                                                                                          
func TestFormalFixedVaultTermITFDifferential(t *testing.T) {
	path := os.Getenv("DEX_FORMAL_ITF_VAULT_TERM")
	if path == "" {
		t.Skip("由 formal/formal_liveness_vault_term.ps1 提供独立 Quint ITF")
	}
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.States) != 5 || protocol.VaultCommitteeTermBlocks%3 != 0 {
		t.Fatalf("轨迹截断或参数不可按比例映射：states=%d term=%d", len(trace.States), protocol.VaultCommitteeTermBlocks)
	}
	current, target := frostAutoPolicyMembers("old", 4), frostAutoPolicyMembers("new", 4)
	x, sv, cfg, _, snapshot := formalTermFixture(t, current, target, 1)
	scale := protocol.VaultCommitteeTermBlocks / 3
	for i, expected := range trace.States {
		h, err := expected.Int("height")
		if err != nil {
			t.Fatal(err)
		}
		running, err := expected.Bool("running")
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 && expected.Action == "review" {
			x.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: uint64(h) * scale}}, snapshot, sv)
		}
		vault := frostAutoPolicyLoadVault(t, sv, cfg.Chain, 0)
		review, err := expected.Int("nextReview")
		if err != nil {
			t.Fatal(err)
		}
		if vault.KeyEpoch != 1 || vault.NextCommitteeReviewHeight != uint64(review)*scale || !bytes.Equal(vault.GroupPubkey, []byte{1}) {
			t.Fatalf("模型/生产epoch-key-review关系不符：step=%d vault=%+v", i, vault)
		}
		_, exists, err := sv.Get(keys.KeyFrostVaultTransition(cfg.Chain, 0, 2))
		if err != nil || exists != running {
			t.Fatalf("模型/生产创建关系不符：step=%d running=%v exists=%v err=%v", i, running, exists, err)
		}
		                                                                                           
		copyVault := new(pb.FrostVaultState)
		raw, _, err := sv.Get(keys.KeyFrostVaultState(cfg.Chain, 0))
		if err != nil || proto.Unmarshal(raw, copyVault) != nil || !proto.Equal(vault, copyVault) {
			t.Fatalf("持久任期恢复失败：step=%d err=%v", i, err)
		}
	}
}

func formalTermFixture(t *testing.T, current, top []string, multiplier uint32) (*Executor, *MockStateView, *pb.FrostVaultConfig, *pb.FrostVaultState, *pb.FrostActiveMiners) {
	t.Helper()
	x, sv, cfg, vault, snapshot := seedFrostAutoTransitionPolicy(t, current, top, uint32(len(top)), multiplier, 100)
	cfg.CommitteeTermBlocks = protocol.VaultCommitteeTermBlocks
	vault.ActiveSinceHeight = 0
	vault.NextCommitteeReviewHeight = protocol.VaultCommitteeTermBlocks
	frostAutoPolicyStoreProto(t, sv, keys.KeyFrostVaultConfig(cfg.Chain, 0), cfg)
	frostAutoPolicyStoreProto(t, sv, keys.KeyFrostVaultState(cfg.Chain, 0), vault)
	return x, sv, cfg, vault, snapshot
}

func TestFormalFixedVaultTermBoundariesAndExpansion(t *testing.T) {
	for _, expanding := range []bool{false, true} {
		current := frostAutoPolicyMembers("old", 4)
		top, multiplier := frostAutoPolicyMembers("new", 4), uint32(1)
		if expanding {
			top, multiplier = frostAutoPolicyMembers("expanded", 16), 2
		}
		x, sv, cfg, _, snapshot := formalTermFixture(t, current, top, multiplier)
		key := keys.KeyFrostVaultTransition(cfg.Chain, 0, 2)
		x.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: protocol.VaultCommitteeTermBlocks - 1}}, snapshot, sv)
		frostAutoPolicyRequireNoTransition(t, sv, key)
		x.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: protocol.VaultCommitteeTermBlocks}}, snapshot, sv)
		transition := mustLoadTransition(t, sv, key)
		if transition.TriggerHeight != protocol.VaultCommitteeTermBlocks+1 {
			t.Fatalf("期满不能被旧滚动确认延迟：expansion=%v transition=%+v", expanding, transition)
		}
		wantK := uint32(4)
		if expanding {
			wantK = 8
		}
		if transition.DkgN != wantK || transition.DkgThresholdT != wantK*3/4 {
			t.Fatalf("固定任期不应改变门槛：%+v", transition)
		}
	}
}

func TestFormalFixedVaultTermUnchangedAndShortage(t *testing.T) {
	for _, shortage := range []bool{false, true} {
		current := frostAutoPolicyMembers("old", 4)
		top := []string{current[3], current[2], current[1], current[0]}
		if shortage {
			top = top[:3]
		}
		x, sv, cfg, vault, snapshot := formalTermFixture(t, current, top, 1)
		beforeKey := append([]byte(nil), vault.GroupPubkey...)
		for _, height := range []uint64{protocol.VaultCommitteeTermBlocks, protocol.VaultCommitteeTermBlocks + 1} {
			x.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: height}}, snapshot, sv)
		}
		frostAutoPolicyRequireNoTransition(t, sv, keys.KeyFrostVaultTransition(cfg.Chain, 0, 2))
		actual := frostAutoPolicyLoadVault(t, sv, cfg.Chain, 0)
		wantReview := 2 * protocol.VaultCommitteeTermBlocks
		if shortage {
			wantReview = protocol.VaultCommitteeTermBlocks
		}
		if actual.NextCommitteeReviewHeight != wantReview || actual.ActiveSinceHeight != 0 || actual.KeyEpoch != 1 || !bytes.Equal(actual.GroupPubkey, beforeKey) || !slices.Equal(actual.CommitteeMembers, current) || actual.CommitteeThreshold != 3 {
			t.Fatalf("相同集合/候选不足的旧钥保持不正确 shortage=%v vault=%+v", shortage, actual)
		}
	}
}

func TestFormalFixedVaultTermFirstDKGProjectionAndInitGuard(t *testing.T) {
	transition := formalDkgValidationTransition()
	transition.DkgStatus = DKGStatusKeyReady
	transition.NewGroupPubkey = []byte("formal-first-key")
	first, err := applyDkgVaultActivation(dkgVaultActivationTransitionInput{
		chain: "eth", vaultID: 0, epochID: 1, height: 77, transition: transition,
	})
	if err != nil || !first.writeVault || first.vault.ActiveSinceHeight != 77 || first.vault.NextCommitteeReviewHeight != 77+protocol.VaultCommitteeTermBlocks {
		t.Fatalf("首次DKG必须从真实激活高度开始完整任期：result=%+v err=%v", first, err)
	}
	for _, empty := range []bool{false, true} {
		current := frostAutoPolicyMembers("old", 4)
		_, sv, cfg, vault, _ := formalTermFixture(t, current, current, 1)
		if empty {
			vault.GroupPubkey = nil
			vault.KeyEpoch, vault.NextCommitteeReviewHeight = 0, 0
			frostAutoPolicyStoreProto(t, sv, keys.KeyFrostVaultState(cfg.Chain, 0), vault)
		}
		if err := InitVaultTransitions(sv, cfg.Chain, 2, 10, 10, 10, 10); err != nil {
			t.Fatal(err)
		}
		_, exists, err := sv.Get(keys.KeyFrostVaultTransition(cfg.Chain, 0, 2))
		if err != nil || exists != empty {
			t.Fatalf("初代入口不得绕过既有金库任期：empty=%v exists=%v err=%v", empty, exists, err)
		}
	}
}

func TestFormalFixedVaultTermMissingMetadataAndOverflow(t *testing.T) {
	vault := &pb.FrostVaultState{GroupPubkey: []byte{1}, KeyEpoch: 1}
	if vaultCommitteeTermDue(vault, math.MaxUint64) {
		t.Fatal("缺失共识任期字段不能按本机启动推定到期")
	}
	if !vaultCommitteeTermDue(&pb.FrostVaultState{}, 0) {
		t.Fatal("首代空钥应允许初始化DKG")
	}
	before := proto.Clone(vault).(*pb.FrostVaultState)
	if err := activateVaultCommitteeTerm(vault, math.MaxUint64-protocol.VaultCommitteeTermBlocks+1); err == nil {
		t.Fatal("溢出任期应拒绝")
	}
	if !proto.Equal(vault, before) {
		t.Fatal("溢出失败修改了既有任期/激活高度")
	}
}

                                                                                  
func TestFormalFixedVaultTermSignedActivationResetsClock(t *testing.T) {
	fixture := newFormalAuthorityFixture(t, refinement.AuthorityHandoverState{Chain: "Bitcoin", Phase: "Draining", CurrentEpoch: 1, TargetEpoch: 2})
	var cfg pb.FrostVaultConfig
	raw, _, err := fixture.sv.Get(keys.KeyFrostVaultConfig(fixture.chain, 0))
	if err != nil || proto.Unmarshal(raw, &cfg) != nil {
		t.Fatal("无法恢复真实交接配置")
	}
	cfg.CommitteeTermBlocks = protocol.VaultCommitteeTermBlocks
	putMigrationProto(t, fixture.sv, keys.KeyFrostVaultConfig(fixture.chain, 0), &cfg)
	vault := mustLoadFormalVault(t, fixture.sv, fixture.vaultKey)
	vault.NextCommitteeReviewHeight = protocol.VaultCommitteeTermBlocks
	putMigrationProto(t, fixture.sv, fixture.vaultKey, vault)
	if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 50}}, fixture.sv); err != nil {
		t.Fatal(err)
	}
	                                                                                         
	submitFormalAuthorityMigration(t, fixture, formalAuthorityValid)
	actual := mustLoadFormalVault(t, fixture.sv, fixture.vaultKey)
	if actual.KeyEpoch != 2 || actual.ActiveSinceHeight != 60 || actual.NextCommitteeReviewHeight != 60+protocol.VaultCommitteeTermBlocks {
		t.Fatalf("成功本链交接未重置完整任期：%+v", actual)
	}
	migration := mustLoadFormalMigration(t, fixture.sv, fixture.migrationKey)
	artifactID, exists, err := fixture.sv.Get(keys.KeyFrostMigrationArtifact(migration.MigrationId))
	if err != nil || !exists || len(artifactID) == 0 {
		t.Fatalf("本链激活缺少永久交接制品索引：exists=%v err=%v", exists, err)
	}
}
