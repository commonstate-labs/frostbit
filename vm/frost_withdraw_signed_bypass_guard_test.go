package vm

import (
	"strings"
	"testing"

	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                                                  
func TestFrostWithdrawSigned_ETH_EmptyTemplateHashRejected(t *testing.T) {
	chainName := "eth"
	asset := "USDT"
	vaultID := uint32(1)
	withdrawID := "wid_eth_empty_hash"

	base := map[string][]byte{}
	base[keys.KeyFrostWithdrawFIFOHead(chainName, asset)] = []byte("1")
	withdraw := &pb.FrostWithdrawState{
		WithdrawId:   withdrawID,
		Seq:          1,
		Chain:        chainName,
		Asset:        asset,
		To:           "0x00000000000000000000000000000000000000bb",
		Amount:       "100",
		BroadcastFee: "1",
		Status:       string(WithdrawStatusQueued),
		VaultId:      vaultID,
	}
	base[keys.KeyFrostWithdraw(withdrawID)] = mustMarshalProto(t, withdraw)
	_, jobID := seedAccountChainClaimStub(t, base, chainName, asset, vaultID, []string{withdrawID})

	sv := newReadOnlyBaseStateView(base)
	handler := &FrostWithdrawSignedTxHandler{}
	signed := &pb.FrostWithdrawSignedTx{
		Base: &pb.BaseMessage{
			TxId:           "tx_eth_empty_hash",
			FromAddress:    "attacker",
			ExecutedHeight: 100,
		},
		JobId:              jobID,
		SignedPackageBytes: make([]byte, evm.SchnorrSigWithYSize), GuardianSigs: [][]byte{make([]byte, evm.SchnorrSigWithYSize)},
		WithdrawIds: []string{withdrawID},
		VaultId:     vaultID,
		KeyEpoch:    1,
		Chain:       chainName,
	}
	populateMixedSignedTxFromClaim(t, base, signed)
	signed.TemplateHash = nil
	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed}}

	_, receipt, err := handler.DryRun(tx, sv)
	if err == nil {
		t.Fatal("empty template_hash on ETH path must be rejected")
	}
	if receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("expected FAILED receipt, got %+v", receipt)
	}
	if !strings.Contains(receipt.Error, "invalid mixed account withdrawal") {
		t.Fatalf("expected invalid mixed withdrawal error, got: %s", receipt.Error)
	}

	                               
	data, _, _ := sv.Get(keys.KeyFrostWithdraw(withdrawID))
	var got pb.FrostWithdrawState
	if err := proto.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Status != string(WithdrawStatusSigning) || got.JobId != jobID {
		t.Fatalf("withdraw claim state changed after rejection: %+v", &got)
	}
}

                                                                       
func TestFrostWithdrawSigned_TRX_WithoutClaimRejected(t *testing.T) {
	chainName := "trx"
	asset := "USDT"
	vaultID := uint32(1)
	withdrawID := "wid_trx_any"

	base := map[string][]byte{}
	base[keys.KeyFrostWithdrawFIFOHead(chainName, asset)] = []byte("1")
	withdraw := &pb.FrostWithdrawState{
		WithdrawId:   withdrawID,
		Seq:          1,
		Chain:        chainName,
		Asset:        asset,
		To:           "Taddress",
		Amount:       "100",
		BroadcastFee: "1",
		Status:       string(WithdrawStatusQueued),
		VaultId:      vaultID,
	}
	base[keys.KeyFrostWithdraw(withdrawID)] = mustMarshalProto(t, withdraw)

	sv := newReadOnlyBaseStateView(base)
	handler := &FrostWithdrawSignedTxHandler{}
	tx := &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawSignedTx{
			FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
				Base: &pb.BaseMessage{
					TxId:           "tx_trx_any",
					FromAddress:    "attacker",
					ExecutedHeight: 100,
				},
				JobId:              "trx_bypass_job",
				SignedPackageBytes: make([]byte, evm.SchnorrSigWithYSize), GuardianSigs: [][]byte{make([]byte, evm.SchnorrSigWithYSize)},
				WithdrawIds:  []string{withdrawID},
				VaultId:      vaultID,
				Chain:        chainName,
				Asset:        "",
				TemplateHash: []byte{0xaa, 0xbb},
				TemplateData: []byte("missing-claim"),
				BatchSeq:     "1",
			},
		},
	}

	_, receipt, err := handler.DryRun(tx, sv)
	if err == nil {
		t.Fatal("TRX without a finalized claim must be rejected")
	}
	if receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("expected FAILED receipt, got %+v", receipt)
	}
	if !strings.Contains(receipt.Error, "Claim is missing") {
		t.Fatalf("expected missing claim error, got: %s", receipt.Error)
	}
}

                                                     
func TestFrostWithdrawSigned_BTC_MissingVaultStateRejected(t *testing.T) {
	chainName := "btc"
	asset := "BTC"
	vaultID := uint32(99999)                         
	withdrawID := "wid_btc_no_vault"

	base := map[string][]byte{}
	base[keys.KeyFrostWithdrawFIFOHead(chainName, asset)] = []byte("1")
	withdraw := &pb.FrostWithdrawState{
		WithdrawId: withdrawID,
		Seq:        1,
		Chain:      chainName,
		Asset:      asset,
		To:         "bc1qtest",
		Amount:     "100",
		Status:     string(WithdrawStatusSigning),
		JobId:      "btc_bypass_job",
		VaultId:    vaultID,
	}
	base[keys.KeyFrostWithdraw(withdrawID)] = mustMarshalProto(t, withdraw)
	                                                       

	                                                                                             
	templateJSON := `{"version":2,"lock_time":0,"vault_id":99999,"key_epoch":1,` +
		`"inputs":[{"txid":"` + strings.Repeat("11", 32) + `","vout":0,"amount":0,"sequence":4294967295}],` +
		`"outputs":[{"address":"bc1qtest","amount":10}],"withdraw_ids":["wid_btc_no_vault"]}`
	template, err := btc.FromJSON([]byte(templateJSON))
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}
	jobState := &pb.FrostWithdrawJobState{
		JobId: "btc_bypass_job", Chain: chainName, Asset: asset, VaultId: vaultID, KeyEpoch: 1,
		WithdrawIds: []string{withdrawID}, TemplateHash: template.TemplateHash(), TemplateData: []byte(templateJSON), Status: frostWithdrawJobStatusClaimed,
	}
	base[keys.KeyFrostWithdrawJob(jobState.JobId)] = mustMarshalProto(t, jobState)

	sv := newReadOnlyBaseStateView(base)
	handler := &FrostWithdrawSignedTxHandler{}
	tx := &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawSignedTx{
			FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
				Base: &pb.BaseMessage{
					TxId:           "tx_btc_no_vault",
					FromAddress:    "attacker",
					ExecutedHeight: 100,
				},
				JobId:              "btc_bypass_job",
				SignedPackageBytes: []byte{0x01},
				WithdrawIds:        []string{withdrawID},
				VaultId:            vaultID,
				KeyEpoch:           1,
				Chain:              chainName,
				Asset:              asset,
				TemplateData:       []byte(templateJSON),
				TemplateHash:       template.TemplateHash(),
				InputSigs:          [][]byte{make([]byte, 64)},
				ScriptPubkeys:      [][]byte{make([]byte, 34)},
			},
		},
	}

	_, receipt, err := handler.DryRun(tx, sv)
	if err == nil {
		t.Fatal("missing VaultState must be rejected")
	}
	if receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("expected FAILED receipt, got %+v", receipt)
	}
	if !strings.Contains(receipt.Error, "vault state not found") {
		t.Fatalf("expected vault state not found error, got: %s", receipt.Error)
	}
}
