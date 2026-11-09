package vm

import (
	"bytes"
	"context"
	chainpkg "dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/frost/withdrawresult"
	"dex/keys"
	"dex/pb"
	"google.golang.org/protobuf/proto"
	"math/big"
	"testing"
)

                                                                            
                                                                                  
func TestMultigenerationEmptyEpochsThenRealWithdrawal(t *testing.T) {
	sv, _ := formalMultigenerationVault(t, "eth")
	for epoch := uint64(2); epoch <= 4; epoch++ {
		formalMultigenerationPrepare(t, sv, "eth", epoch)
		formalMultigenerationFinalize(t, sv, "eth", epoch)
	}
	const asset = "v2-real-token"
	ids := []string{"v2-withdraw-a", "v2-withdraw-b"}
	putMigrationProto(t, sv, keys.KeyToken(asset), &pb.Token{Address: asset, Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: "eth", NativeContract: "0x3333333333333333333333333333333333333333"})
	for i, id := range ids {
		seq := uint64(i + 1)
		putMigrationProto(t, sv, keys.KeyFrostWithdraw(id), &pb.FrostWithdrawState{WithdrawId: id, Seq: seq, Chain: "eth", Asset: asset, To: "0x2222222222222222222222222222222222222222", Amount: "10000", GrossAmount: "10000", FeeAmount: "1", NetAmount: "9999", FeeVersion: 1, Status: "QUEUED", VaultId: 0, LiveSeq: seq, KeyEpoch: 4, RequestHeight: 90})
		sv.Set(keys.KeyFrostWithdrawLiveIndex("eth", 0, seq), []byte(id))
	}
	sv.Set(keys.KeyFrostWithdrawLiveHead("eth", 0), []byte("1"))
	sv.Set(keys.KeyFrostWithdrawLiveTail("eth", 0), []byte("2"))
	sv.Set(keys.KeyFrostWithdrawLivePending("eth", 0), []byte("2"))
	sv.Set(keys.KeyFrostVaultAvailableBalance("eth", asset, 0), []byte("0"))
	sv.Set(keys.KeyFrostVaultReservedBalance("eth", asset, 0), []byte("20000"))
	built, first, err := buildCanonicalAccountTemplate(sv, "eth", "", 0, 4, ids)
	if err != nil {
		t.Fatal(err)
	}
	template, err := evm.FromJSON(built.TemplateData)
	if err != nil || template.AuthorityProtocol != 3 || template.DexChainID != 51025 {
		t.Fatalf("V2 template scope: %+v %v", template, err)
	}
	jobID := chainpkg.ComputeWithdrawJobID("eth", "", 0, first, built.TemplateHash, 4)
	claim := &pb.FrostWithdrawClaimTx{Base: &pb.BaseMessage{TxId: "v2-real-claim", FromAddress: "member", ExecutedHeight: 190}, JobId: jobID, Chain: "eth", VaultId: 0, KeyEpoch: 4, WithdrawIds: ids, TemplateHash: built.TemplateHash, TemplateData: built.TemplateData, BatchSeq: "1", IncludesHandover: true}
	if _, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: claim}}, sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("real Claim: %+v %v", receipt, err)
	}
	sig := signAccountThresholdTest(t, big.NewInt(20), built.SigHashes[0])
	guardian := signAccountThresholdTest(t, big.NewInt(29), built.SigHashes[0])
	signed := &pb.FrostWithdrawSignedTx{Base: &pb.BaseMessage{TxId: "v2-real-signed", FromAddress: "member", ExecutedHeight: 191}, JobId: jobID, Chain: "eth", VaultId: 0, KeyEpoch: 4, WithdrawIds: ids, TemplateHash: built.TemplateHash, TemplateData: built.TemplateData, BatchSeq: "1", IncludesHandover: true, SignedPackageBytes: sig, GuardianSigs: [][]byte{guardian}}
	if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed}}, sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("real Signed: %+v %v", receipt, err)
	}
	if raw, _, _ := sv.Get(keys.KeyFrostVaultReservedBalance("eth", asset, 0)); string(raw) != "0" {
		t.Fatalf("reservation not consumed once: %q", raw)
	}
	if raw, _, _ := sv.Get(keys.KeyFrostVaultDEXNextBatchSeq("eth", 0)); string(raw) != "2" {
		t.Fatalf("next batch: %q", raw)
	}
	                                                                          
	restarted := NewMockStateView()
	for k, v := range sv.data {
		if keys.IsStatefulKey(k) {
			restarted.Set(k, bytes.Clone(v))
		}
	}
	result, err := withdrawresult.ResolveBatch(context.Background(), restarted.Get, "eth", 0, "1")
	if err != nil {
		t.Fatal(err)
	}
	if result.AuthorityProtocol != 3 || result.DexChainID != "51025" || result.Epoch != "4" {
		t.Fatalf("permanent result scope: %+v", result)
	}
	page, err := withdrawresult.ResolveHandoverPage(context.Background(), restarted.Get, "eth", 0, 1, 4)
	if err != nil || len(page.Certificates) != 3 || page.HasMore {
		t.Fatalf("checkpoint proof chain: %+v %v", page, err)
	}
	var proofChain []evm.SignedHandover
	for epoch := uint64(2); epoch <= 4; epoch++ {
		var m pb.FrostVaultMigrationState
		raw, _, _ := restarted.Get(keys.KeyFrostVaultMigration("eth", 0, epoch))
		if err := proto.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		pub, err := evm.CanonicalBN128PublicKey(m.NewGroupPubkey)
		if err != nil {
			t.Fatal(err)
		}
		proof, _, _ := restarted.Get(keys.KeyFrostVaultHandoverProof("eth", 0, epoch))
		proofChain = append(proofChain, evm.SignedHandover{FromEpoch: epoch - 1, ToEpoch: epoch, NewGroupPubkey: pub, FrozenNextBatchSeq: m.FrozenNextBatchSeq, Signature: proof[:96], GuardianSignature: proof[96:]})
	}
	pkg, err := evm.NewETHAdapter().PackageSignedWithHandovers(built.TemplateData, sig, guardian, proofChain)
	if err != nil {
		t.Fatal(err)
	}
	decoded, withdrawSig, guardianSig, err := evm.DecodeSignedHandovers(pkg.RawTx)
	if err != nil || len(decoded) != 3 || !bytes.Equal(withdrawSig, sig) || !bytes.Equal(guardianSig, guardian) {
		t.Fatalf("public recovery envelope: %v", err)
	}
	                                                                           
	formalMultigenerationPrepare(t, restarted, "eth", 5)
	if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 200}}, restarted); err != nil {
		t.Fatal(err)
	}
	cut := mustLoadFormalMigration(t, restarted, keys.KeyFrostVaultMigration("eth", 0, 5))
	if cut.FrozenNextBatchSeq != "2" {
		t.Fatalf("old SIGNED obligation lost: %+v", cut)
	}
}
