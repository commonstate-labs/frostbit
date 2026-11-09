package vm

import (
	"bytes"
	"math/big"
	"testing"

	chainpkg "dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestFormalEVMPartialBatchTimeoutMatchesModel(t *testing.T) {
	const modeledPartialBatchTimeoutBlocks = uint64(100)
	if chainpkg.AccountWithdrawBatchTimeoutBlocks != modeledPartialBatchTimeoutBlocks {
		t.Fatalf("production partial-batch timeout=%d, model=%d", chainpkg.AccountWithdrawBatchTimeoutBlocks, modeledPartialBatchTimeoutBlocks)
	}
}

                                                                         
                                                                             
                                                                            
                                                                              
                                  
func TestFormalEVMBatchWithdrawGrossReservationDifferential(t *testing.T) {
	const (
		chainName     = "eth"
		asset         = "formal-batch-asset"
		vaultID       = uint32(0)
		epoch         = uint64(1)
		requestHeight = uint64(90)
		claimHeight   = requestHeight + chainpkg.AccountWithdrawBatchTimeoutBlocks
	)
	ids := []string{"formal-batch-a", "formal-batch-b"}
	amounts := []string{"3", "4"}
	fees := []string{"1", "1"}
	nets := []string{"2", "3"}
	sv := NewMockStateView()
	secret := accountTestSecret(true)
	group := curve.NewBN256Group()
	contract := "0x1111111111111111111111111111111111111111"
	token := "0x3333333333333333333333333333333333333333"

	putMigrationProto(t, sv, keys.KeyFrostVaultConfig(chainName, 0), &pb.FrostVaultConfig{
		Chain: chainName, SignAlgo: chainpkg.SignAlgoETH, VaultCount: 1,
		EvmChainId: 1, VaultRefs: []string{contract}, AuthorityProtocol: 3, DepositDexChainId: 51025,
		GuardianPubkey: group.SerializePoint(group.ScalarBaseMult(big.NewInt(29))),
	})
	putMigrationProto(t, sv, keys.KeyFrostVaultState(chainName, vaultID), &pb.FrostVaultState{
		Chain: chainName, VaultId: vaultID, KeyEpoch: epoch, Status: "ACTIVE",
		SignAlgo:         chainpkg.SignAlgoETH,
		GroupPubkey:      group.SerializePoint(group.ScalarBaseMult(secret)),
		CommitteeMembers: []string{"coordinator"},
	})
	putMigrationProto(t, sv, keys.KeyToken(asset), &pb.Token{
		Address: asset, Type: pb.TokenType_TOKEN_BRIDGED,
		NativeChain: chainName, NativeContract: token,
	})
	for i, id := range ids {
		seq := uint64(i + 1)
		putMigrationProto(t, sv, keys.KeyFrostWithdraw(id), &pb.FrostWithdrawState{
			WithdrawId: id, Seq: seq, Chain: chainName, Asset: asset,
			To: "0x2222222222226220921060314715629080e2fb77", Amount: amounts[i],
			GrossAmount: amounts[i], FeeAmount: fees[i], NetAmount: nets[i], FeeVersion: 1,
			Status: "QUEUED", VaultId: vaultID, LiveSeq: seq, KeyEpoch: epoch, RequestHeight: requestHeight,
		})
		sv.Set(keys.KeyFrostWithdrawLiveIndex(chainName, vaultID, seq), []byte(id))
	}
	sv.Set(keys.KeyFrostWithdrawLiveHead(chainName, vaultID), []byte("1"))
	sv.Set(keys.KeyFrostWithdrawLiveTail(chainName, vaultID), []byte("2"))
	sv.Set(keys.KeyFrostWithdrawLivePending(chainName, vaultID), []byte("2"))
	sv.Set(keys.KeyFrostVaultAvailableBalance(chainName, asset, vaultID), []byte("0"))
	sv.Set(keys.KeyFrostVaultReservedBalance(chainName, asset, vaultID), []byte("7"))

	template, firstSeq, err := buildCanonicalAccountTemplate(sv, chainName, asset, vaultID, epoch, ids)
	if err != nil {
		t.Fatalf("build canonical account batch: %v", err)
	}
	evmTemplate, err := evm.FromJSON(template.TemplateData)
	if err != nil {
		t.Fatalf("parse EVM template: %v", err)
	}
	if len(evmTemplate.Outputs) != 2 || evmTemplate.BatchSeq != "1" || len(evmTemplate.RewardTokens) != 1 ||
		evmTemplate.Outputs[0].GrossAmount != "3" || evmTemplate.Outputs[0].RewardIndex != 0 ||
		evmTemplate.Outputs[1].GrossAmount != "4" || evmTemplate.Outputs[1].RewardIndex != 0 {
		t.Fatalf("canonical batch lost gross/reward binding: %+v", evmTemplate)
	}
	jobID := chainpkg.ComputeWithdrawJobID(chainName, "", vaultID, firstSeq, template.TemplateHash, epoch)
	coordinator, err := chainpkg.WithdrawCoordinator(jobID, epoch, []string{"coordinator"})
	if err != nil {
		t.Fatal(err)
	}
	claim := &pb.FrostWithdrawClaimTx{
		Base:  &pb.BaseMessage{TxId: "formal-batch-claim", FromAddress: coordinator, ExecutedHeight: claimHeight},
		JobId: jobID, Chain: chainName, Asset: "", VaultId: vaultID,
		KeyEpoch: epoch, WithdrawIds: ids, TemplateHash: template.TemplateHash,
		TemplateData: template.TemplateData, BatchSeq: "1",
	}
	if _, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(
		&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: claim}}, sv,
	); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("batch claim failed: receipt=%+v err=%v", receipt, err)
	}
	if raw, _, _ := sv.Get(keys.KeyFrostVaultAvailableBalance(chainName, asset, vaultID)); string(raw) != "0" {
		t.Fatalf("gross available balance=%q want 0", raw)
	}
	if raw, _, _ := sv.Get(keys.KeyFrostVaultReservedBalance(chainName, asset, vaultID)); string(raw) != "7" {
		t.Fatalf("gross reserved balance=%q want 7", raw)
	}
	activeKey := keys.KeyFrostWithdrawActiveVaultJob(chainName, vaultID)
	if raw, found, _ := sv.Get(activeKey); !found || string(raw) != jobID {
		t.Fatalf("missing exact active batch index: found=%v value=%q", found, raw)
	}

	signature := signAccountThresholdTest(t, secret, template.SigHashes[0])
	signed := &pb.FrostWithdrawSignedTx{
		Base:  &pb.BaseMessage{TxId: "formal-batch-signed", FromAddress: coordinator, ExecutedHeight: claimHeight + 1},
		JobId: jobID, Chain: chainName, Asset: "", VaultId: vaultID,
		KeyEpoch: epoch, WithdrawIds: ids, TemplateHash: template.TemplateHash,
		TemplateData: template.TemplateData, SignedPackageBytes: signature, BatchSeq: "1",
		GuardianSigs: [][]byte{signAccountThresholdTest(t, big.NewInt(29), template.SigHashes[0])},
	}
	tampered := proto.Clone(signed).(*pb.FrostWithdrawSignedTx)
	tampered.Base = proto.Clone(signed.Base).(*pb.BaseMessage)
	tampered.Base.TxId = "formal-batch-signed-invalid"
	tampered.SignedPackageBytes = append([]byte(nil), signature...)
	tampered.SignedPackageBytes[len(tampered.SignedPackageBytes)-1] ^= 1
	if ops, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(
		&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: tampered}}, sv,
	); err == nil || receipt.Status != "FAILED" || ops != nil {
		t.Fatalf("invalid batch signature was accepted: receipt=%+v err=%v ops=%+v", receipt, err, ops)
	}
	if raw, found, _ := sv.Get(keys.KeyFrostVaultDEXNextBatchSeq(chainName, vaultID)); found || len(raw) != 0 {
		t.Fatalf("failed Signed advanced batch sequence: found=%v value=%q", found, raw)
	}
	if raw, _, _ := sv.Get(keys.KeyFrostVaultReservedBalance(chainName, asset, vaultID)); string(raw) != "7" {
		t.Fatalf("failed Signed changed reservation=%q", raw)
	}
	if raw, _, _ := sv.Get(keys.KeyFrostWithdrawLivePending(chainName, vaultID)); string(raw) != "2" {
		t.Fatalf("failed Signed changed pending=%q", raw)
	}
	if _, found, _ := sv.Get(keys.KeyFrostPermanentWithdrawArtifact(ids[0])); found {
		t.Fatal("failed Signed published an artifact")
	}
	if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(
		&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed}}, sv,
	); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("batch signed failed: receipt=%+v err=%v", receipt, err)
	}
	if raw, _, _ := sv.Get(keys.KeyFrostVaultReservedBalance(chainName, asset, vaultID)); string(raw) != "0" {
		t.Fatalf("signed batch left gross reservation=%q", raw)
	}
	if _, found, _ := sv.Get(activeKey); found {
		t.Fatal("signed batch left active in-flight index")
	}
	if raw, _, _ := sv.Get(keys.KeyFrostVaultDEXNextBatchSeq(chainName, vaultID)); string(raw) != "2" {
		t.Fatalf("signed batch next sequence=%q want 2", raw)
	}
	if raw, _, _ := sv.Get(keys.KeyFrostWithdrawLivePending(chainName, vaultID)); string(raw) != "0" {
		t.Fatalf("signed batch pending=%q want 0", raw)
	}
	if raw, _, _ := sv.Get(keys.KeyFrostWithdrawLiveHead(chainName, vaultID)); string(raw) != "3" {
		t.Fatalf("signed batch FIFO head=%q want 3", raw)
	}

	var artifactID []byte
	for i, id := range ids {
		data, found, err := sv.Get(keys.KeyFrostPermanentWithdraw(id))
		if err != nil || !found {
			t.Fatalf("missing permanent withdraw %s: %v", id, err)
		}
		var state pb.FrostWithdrawState
		if err := proto.Unmarshal(data, &state); err != nil {
			t.Fatal(err)
		}
		if state.GrossAmount != amounts[i] || state.FeeAmount != fees[i] || state.Status != string(WithdrawStatusSigned) {
			t.Fatalf("permanent gross/fee/status mismatch: %+v", &state)
		}
		ref, found, err := sv.Get(keys.KeyFrostPermanentWithdrawArtifact(id))
		if err != nil || !found {
			t.Fatalf("missing artifact ref for %s: %v", id, err)
		}
		if i == 0 {
			artifactID = append([]byte(nil), ref...)
		} else if !bytes.Equal(artifactID, ref) {
			t.Fatalf("batch members published different artifacts: %q != %q", artifactID, ref)
		}
	}
	record := loadAccountProtocolArtifact(t, sv, string(artifactID))
	if len(record.RawTx) != 4+27*32 || !bytes.Equal(record.RawTx[:4], evmTemplate.MethodID) {
		t.Fatalf("unexpected two-item batch calldata: len=%d selector=%x", len(record.RawTx), record.RawTx[:4])
	}
}
