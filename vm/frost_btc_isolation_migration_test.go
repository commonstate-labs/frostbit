package vm

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	bridgeasset "dex/frost/asset"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"dex/protocol"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"google.golang.org/protobuf/proto"
)

func seedBTCIsolationMigration(t *testing.T, count int) StateView {
	t.Helper()
	sv := newMemoryStateView(map[string][]byte{})
	enableBTCIsolationForTest(t, sv)
	_, newPub := migrationTestKey(t, 41)
	transition := &pb.VaultTransitionState{Chain: "btc", VaultId: 0, EpochId: 2, DkgStatus: DKGStatusKeyReady,
		Lifecycle: VaultLifecycleDraining, OldGroupPubkey: btcDualTestOperatorPubkey(), NewGroupPubkey: newPub,
		SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, OldCommitteeMembers: []string{"old-member"},
		NewCommitteeMembers: []string{"new-member"}, DkgThresholdT: 1}
	transitionKey := keys.KeyFrostVaultTransition("btc", 0, 2)
	putMigrationProto(t, sv, transitionKey, transition)
	sv.Set(keys.KeyFrostVaultTransitionActive("btc", 0, 2), []byte(transitionKey))
	putMigrationProto(t, sv, keys.KeyFrostVaultState("btc", 0), &pb.FrostVaultState{Chain: "btc", VaultId: 0, KeyEpoch: 1,
		GroupPubkey: btcDualTestOperatorPubkey(), SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Status: VaultStatusActive, CommitteeMembers: []string{"old-member"}})
	putMigrationProto(t, sv, keys.KeyFrostVaultConfig("btc", 0), &pb.FrostVaultConfig{Chain: "btc", VaultCount: 1,
		BtcNetwork: "mainnet", BtcFeeSatsPerVbyte: 25, GuardianPubkey: btcDualTestGuardianPubkey(), DepositDexChainId: 51025})
	for i := 1; i <= count; i++ {
		txid := fmt.Sprintf("%064x", i)
		u := &pb.FrostUtxo{Txid: txid, Amount: "100000", VaultId: 0, ScriptPubkey: btcDualTestScript(t),
			OriginId: []byte(fmt.Sprintf("%032d", i)), FinalizeHeight: uint64(i)}
		data := mustMarshalProto(t, u)
		sv.Set(keys.KeyFrostBtcUtxo(0, txid, 0), data)
		sv.Set(keys.KeyFrostBtcSpendableUtxoIndex(0, uint64(i)), data)
		sv.Set(keys.KeyFrostBtcUtxoFIFOIndex(0, uint64(i)), data)
		sv.Set(keys.KeyFrostBtcUtxoSeqRef(0, txid, 0), []byte(strconv.Itoa(i)))
	}
	sv.Set(keys.KeyFrostBtcUtxoFIFOSeq(0), []byte(strconv.Itoa(count)))
	asset, err := bridgeasset.Resolve("btc", "")
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyFrostVaultAvailableBalance("btc", asset.ID, 0), []byte(strconv.Itoa(count*100000)))
	if err := migrateBTCIsolationV3(sv); err != nil {
		t.Fatal(err)
	}
	return sv
}

type btcIsolationMigrationIOView struct {
	StateView
	scans                                  []string
	cursors                                []string
	batches, batchKeys, unboundedUTXOScans int
}

func (s *btcIsolationMigrationIOView) NetworkChainID() uint64 {
	return s.StateView.(interface{ NetworkChainID() uint64 }).NetworkChainID()
}
func (s *btcIsolationMigrationIOView) GetMany(ks []string) (map[string][]byte, error) {
	s.batches++
	s.batchKeys += len(ks)
	return s.StateView.(interface {
		GetMany([]string) (map[string][]byte, error)
	}).GetMany(ks)
}
func (s *btcIsolationMigrationIOView) Scan(prefix string) (map[string][]byte, error) {
	if strings.HasPrefix(prefix, keys.KeyFrostBtcUtxoAllPrefix()) || prefix == keys.KeyFrostBtcSpendableUtxoIndexPrefix(0) {
		s.unboundedUTXOScans++
	}
	return s.StateView.Scan(prefix)
}
func (s *btcIsolationMigrationIOView) ScanWithLimitOrderedAfter(prefix, cursor string, limit int, reverse bool) ([]KVPair, error) {
	if prefix == keys.KeyFrostBtcUtxoPrefix(0) {
		s.scans = append(s.scans, prefix)
		s.cursors = append(s.cursors, cursor)
	}
	return s.StateView.(orderedStateScannerAfter).ScanWithLimitOrderedAfter(prefix, cursor, limit, reverse)
}

func TestBTCIsolationMigrationChildSignedDoesNotRescanVault(t *testing.T) {
	sv := seedBTCIsolationMigration(t, 4)
	if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 50}}, sv); err != nil {
		t.Fatal(err)
	}
	p := loadBTCIsolationMigrationParent(t, sv)
	tx := btcIsolationMigrationSignedForTest(t, sv, p, 1)
	view := &btcIsolationMigrationIOView{StateView: sv}
	if _, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(tx, view); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("child: %v %+v", err, receipt)
	}
	if view.unboundedUTXOScans != 0 || len(view.scans) != 0 || view.batches > 3 || view.batchKeys > 4 {
		t.Fatalf("child work scales with vault: scans=%d unbounded=%d batches=%d keys=%d", len(view.scans), view.unboundedUTXOScans, view.batches, view.batchKeys)
	}
}

func TestBTCIsolationMigrationManifestPageBoundaries(t *testing.T) {
	for _, size := range []int{1023, 1024, 1025} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			view := &btcIsolationMigrationIOView{StateView: seedBTCIsolationMigration(t, size)}
			for height := uint64(50); height <= 51; height++ {
				if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: height}}, view); err != nil {
					t.Fatal(err)
				}
			}
			p := loadBTCIsolationMigrationParent(t, view)
			wantScans := 1
			if size >= 1024 {
				wantScans = 2
			}
			if p.Status != migrationStatusIntentFinalized || p.BtcChildCount != uint64(size) || len(view.scans) != wantScans || view.unboundedUTXOScans != 0 {
				t.Fatalf("bounded manifest size=%d child=%d scans=%d unbounded=%d", size, p.BtcChildCount, len(view.scans), view.unboundedUTXOScans)
			}
			if len(view.cursors) == 2 && view.cursors[1] <= view.cursors[0] {
				t.Fatal("manifest restarted scanning at prefix head")
			}
		})
	}
}

func TestBTCIsolationMigrationRejectsAlteredManifestAndGuardian(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*pb.FrostVaultTransitionSignedTx)
	}{
		{"manifest hash", func(r *pb.FrostVaultTransitionSignedTx) { r.MsgHash[0] ^= 1 }},
		{"guardian", func(r *pb.FrostVaultTransitionSignedTx) { r.GuardianSigs[1][0] ^= 1 }},
		{"input guardian", func(r *pb.FrostVaultTransitionSignedTx) { r.GuardianSigs[0][0] ^= 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sv := seedBTCIsolationMigration(t, 2)
			if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 50}}, sv); err != nil {
				t.Fatal(err)
			}
			p := loadBTCIsolationMigrationParent(t, sv)
			tx := btcIsolationMigrationSignedForTest(t, sv, p, 1)
			tc.mutate(tx.GetFrostVaultTransitionSignedTx())
			before := sv.Diff()
			if _, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(tx, sv); err == nil || receipt.Status != "FAILED" {
				t.Fatal("accepted altered migration authorization")
			}
			if !reflect.DeepEqual(before, sv.Diff()) {
				t.Fatal("rejected migration partially consumed funds or switched key")
			}
		})
	}
}

func TestBTCIsolationMigrationEmptyManifestStillRequiresDualAuthorization(t *testing.T) {
	sv := seedBTCIsolationMigration(t, 0)
	if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 50}}, sv); err != nil {
		t.Fatal(err)
	}
	p := loadBTCIsolationMigrationParent(t, sv)
	if !p.NoFunds || p.BtcChildCount != 0 || p.Status != migrationStatusIntentFinalized {
		t.Fatalf("empty manifest: %+v", p)
	}
	op, guardian := btcDualTestKeys()
	auth, err := schnorr.Sign(op, p.IntentHash)
	if err != nil {
		t.Fatal(err)
	}
	guardAuth, err := schnorr.Sign(guardian, p.IntentHash)
	if err != nil {
		t.Fatal(err)
	}
	req := &pb.FrostVaultTransitionSignedTx{Base: &pb.BaseMessage{TxId: "v3-empty-handover", FromAddress: "old-member", ExecutedHeight: 60},
		Chain: "btc", OldVaultId: 0, NewVaultId: 0, EpochId: 2, MigrationId: p.MigrationId, MsgHash: p.IntentHash,
		TemplateHash: p.TemplateHash, Signature: auth.Serialize()}
	before := sv.Diff()
	if _, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: req}}, sv); err == nil || receipt.Status != "FAILED" {
		t.Fatal("empty handover omitted guardian")
	}
	if !reflect.DeepEqual(before, sv.Diff()) {
		t.Fatal("rejected empty handover changed vault")
	}
	req.GuardianSigs = [][]byte{guardAuth.Serialize()}
	if _, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: req}}, sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("empty dual handover: %v %+v", err, receipt)
	}
	if p = loadBTCIsolationMigrationParent(t, sv); p.Status != migrationStatusSigned {
		t.Fatal("empty signed marker missing")
	}
}

func loadBTCIsolationMigrationParent(t *testing.T, sv StateView) *pb.FrostVaultMigrationState {
	t.Helper()
	raw, found, err := sv.Get(keys.KeyFrostVaultMigration("btc", 0, 2))
	var p pb.FrostVaultMigrationState
	if err != nil || !found || proto.Unmarshal(raw, &p) != nil {
		t.Fatalf("parent missing: %v", err)
	}
	return &p
}

func btcIsolationMigrationSignedForTest(t *testing.T, sv StateView, p *pb.FrostVaultMigrationState, seq uint64) *pb.AnyTx {
	t.Helper()
	ref, found, _ := sv.Get(keys.KeyFrostBtcMigrationChild(p.MigrationId, seq))
	if !found {
		t.Fatal("child index missing")
	}
	raw, found, _ := sv.Get(keys.KeyFrostBtcMigrationChildState(string(ref)))
	var child pb.FrostVaultMigrationState
	if !found || proto.Unmarshal(raw, &child) != nil {
		t.Fatal("child missing")
	}
	template, err := btc.FromJSON(child.TemplateData)
	if err != nil {
		t.Fatal(err)
	}
	scripts := [][]byte{btcDualTestScript(t)}
	hashes, err := template.ComputeTaprootSighash(scripts, btc.SighashDefault)
	if err != nil {
		t.Fatal(err)
	}
	op, guardian := btcDualTestKeys()
	inputSig, err := schnorr.Sign(op, hashes[0])
	if err != nil {
		t.Fatal(err)
	}
	guardianInputSig, err := schnorr.Sign(guardian, hashes[0])
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := schnorr.Sign(op, p.IntentHash)
	if err != nil {
		t.Fatal(err)
	}
	guardianAuthorization, err := schnorr.Sign(guardian, p.IntentHash)
	if err != nil {
		t.Fatal(err)
	}
	inputSigs := [][]byte{inputSig.Serialize()}
	guardianSigs := [][]byte{guardianInputSig.Serialize(), guardianAuthorization.Serialize()}
	transaction, err := template.BuildSignedRawTx(inputSigs, guardianSigs[:1])
	if err != nil {
		t.Fatal(err)
	}
	return &pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: &pb.FrostVaultTransitionSignedTx{
		Base:  &pb.BaseMessage{TxId: fmt.Sprintf("migration-child-%d", seq), FromAddress: "old-member", ExecutedHeight: 70 + seq},
		Chain: "btc", OldVaultId: 0, NewVaultId: 0, EpochId: 2, MigrationId: child.MigrationId, MsgHash: p.IntentHash,
		Signature: authorization.Serialize(), TemplateHash: child.TemplateHash, TemplateData: child.TemplateData,
		InputSigs: inputSigs, GuardianSigs: guardianSigs, ScriptPubkeys: scripts, RawTx: transaction,
	}}}
}

func TestBTCIsolationMigrationAllChildrenBeforeKeySwitch(t *testing.T) {
	sv := seedBTCIsolationMigration(t, 2)
	if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 50}}, sv); err != nil {
		t.Fatal(err)
	}
	p := loadBTCIsolationMigrationParent(t, sv)
	if p.Status != migrationStatusIntentFinalized || p.BtcChildCount != 2 || len(p.BtcManifestHash) != 32 {
		t.Fatalf("manifest: %+v", p)
	}
	if _, found, _ := sv.Get(keys.KeyFrostPermanentMigration(p.MigrationId)); !found {
		t.Fatal("sealed manifest is not permanent")
	}
	first := btcIsolationMigrationSignedForTest(t, sv, p, 1)
	second := btcIsolationMigrationSignedForTest(t, sv, p, 2)
	for i, tx := range []*pb.AnyTx{first, second} {
		if ops, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(tx, sv); err != nil || receipt.Status != "SUCCEED" || len(ops) != 0 {
			t.Fatalf("child %d: %v %+v", i, err, receipt)
		}
		raw, _, _ := sv.Get(keys.KeyFrostVaultState("btc", 0))
		var vault pb.FrostVaultState
		if err := proto.Unmarshal(raw, &vault); err != nil {
			t.Fatal(err)
		}
		wantEpoch := uint64(1 + i)
		if vault.KeyEpoch != wantEpoch {
			t.Fatalf("child %d key switched to %d want %d", i, vault.KeyEpoch, wantEpoch)
		}
	}
	p = loadBTCIsolationMigrationParent(t, sv)
	if p.Status != migrationStatusSigned || p.BtcSignedChildCount != 2 {
		t.Fatalf("completion: %+v", p)
	}
	before := sv.Diff()
	if _, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(first, sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("replay after handover: %v %+v", err, receipt)
	}
	if !reflect.DeepEqual(before, sv.Diff()) {
		t.Fatal("migration replay mutated state")
	}
}

func TestBTCIsolationMigrationManifestUsesMonotonicPages(t *testing.T) {
	sv := seedBTCIsolationMigration(t, protocol.BTCIsolationIndexPageSize+1)
	if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 50}}, sv); err != nil {
		t.Fatal(err)
	}
	first := loadBTCIsolationMigrationParent(t, sv)
	if first.Status != migrationStatusBuildingManifest || first.BtcChildCount != 1024 || first.BtcBuildCursor == "" || len(first.IntentHash) != 0 {
		t.Fatalf("premature seal: %+v", first)
	}
	if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 51}}, sv); err != nil {
		t.Fatal(err)
	}
	last := loadBTCIsolationMigrationParent(t, sv)
	if last.Status != migrationStatusIntentFinalized || last.BtcChildCount != 1025 || last.BtcBuildCursor <= first.BtcBuildCursor {
		t.Fatalf("cursor/seal: %+v", last)
	}
	if _, found, _ := sv.Get(keys.KeyFrostBtcMigrationChild(last.MigrationId, 1026)); found {
		t.Fatal("duplicated a previous page")
	}
}
