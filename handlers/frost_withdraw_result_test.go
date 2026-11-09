package handlers

import (
	"bytes"
	"dex/frost/core/curve"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"dex/frost/artifact"
	"dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/frost/withdrawresult"
	"dex/keys"
	"dex/pb"
)

func TestWithdrawResultRealDatabaseArchiveMissAndLegacyFallback(t *testing.T) {
	hm, dbm := newFrostQueryTestHandler(t)
	defer dbm.Close()
	query := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		hm.HandleFrostWithdrawResult(r, httptest.NewRequest(http.MethodGet, path, nil))
		return r
	}
	if r := query("/frost/withdraw/result?request_tx_id=unknown"); r.Code != 404 {
		t.Fatalf("missing ref=%d %s", r.Code, r.Body)
	}
	if r := query("/frost/withdraw/result?withdraw_id=unknown"); r.Code != 404 {
		t.Fatalf("missing withdrawal=%d %s", r.Code, r.Body)
	}
	wd := &pb.FrostWithdrawState{
		WithdrawId: "w", TxId: "request", Chain: "ETH", Status: "SIGNED", JobId: "job", Asset: "ETH",
		To: "0x2222222222222222222222222222222222222222", Amount: "123", GrossAmount: "123",
		FeeAmount: "1", NetAmount: "122", FeeVersion: 1, LiveSeq: 1, KeyEpoch: 2,
	}
	adapter := evm.NewEVMAdapter("ETH", 1)
	template, err := adapter.BuildWithdrawTemplate(chain.WithdrawTemplateParams{
		AuthorityProtocol: 3, DexChainID: 51025, GuardianPubkey: curve.NewBN256Group().SerializePoint(curve.NewBN256Group().ScalarBaseMult(big.NewInt(29))), Chain: "ETH", Asset: wd.Asset, VaultID: 0, KeyEpoch: 2, BatchSeq: "1",
		ContractAddr: "0x1111111111111111111111111111111111111111", WithdrawIDs: []string{"w", "w-token"},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w", To: wd.To, Amount: wd.GrossAmount},
			{WithdrawID: "w-token", To: "0x4444444444444444444444444444444444444444", Amount: "10001", Token: "0x3333333333333333333333333333333333333333"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	evmTemplate, err := evm.FromJSON(template.TemplateData)
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 96)
	big.NewInt(1).FillBytes(sig[:32])
	big.NewInt(2).FillBytes(sig[32:64])
	big.NewInt(1).FillBytes(sig[64:])
	signed, err := adapter.PackageSigned(template.TemplateData, [][]byte{sig}, [][]byte{sig})
	if err != nil {
		t.Fatal(err)
	}
	record, err := artifact.NewRecord(artifact.Metadata{Kind: artifact.KindWithdraw, Chain: "ETH", Epoch: 2, SourceID: "job"}, signed.RawTx)
	if err != nil {
		t.Fatal(err)
	}
	storeDownloadTestArtifact(t, dbm, record)
	enqueueProto(t, dbm, keys.KeyArchiveFrostWithdraw("w"), wd)
	jobState := &pb.FrostWithdrawJobState{
		JobId: "job", Status: "SIGNED", Chain: "ETH", KeyEpoch: 2, WithdrawIds: []string{"w", "w-token"},
		TemplateHash: template.TemplateHash, TemplateData: template.TemplateData, BatchSeq: "1",
		RewardTokens: append([]string(nil), evmTemplate.RewardTokens...),
	}
	signedPackage := &pb.FrostSignedPackage{JobId: "job", SignedPackageBytes: append(bytes.Clone(sig), sig...), GuardianSigs: [][]byte{sig}}
	enqueueProto(t, dbm, keys.KeyArchiveFrostWithdrawJob("job"), jobState)
	enqueueProto(t, dbm, keys.KeyArchiveFrostSignedPackage("job", 0), signedPackage)
	enqueueProto(t, dbm, keys.KeyFrostVaultConfig("ETH", 0), &pb.FrostVaultConfig{Chain: "ETH", VaultCount: 1, EvmChainId: 1, AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: curve.NewBN256Group().SerializePoint(curve.NewBN256Group().ScalarBaseMult(big.NewInt(29))), VaultRefs: []string{"0x1111111111111111111111111111111111111111"}})
	enqueueProto(t, dbm, keys.KeyToken("ETH"), &pb.Token{Address: "ETH", NativeChain: "ETH"})
	dbm.EnqueueSet(keys.KeyFrostWithdrawTxRef("request"), "w")
	if err := dbm.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	                                                                           
	                                                                        
	r := query("/frost/withdraw/result?request_tx_id=request")
	var result withdrawresult.Result
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || result.ArtifactID != record.Metadata.ArtifactID {
		t.Fatalf("legacy=%d %s", r.Code, r.Body)
	}
	if result.BatchSeq != "1" || result.GrossAmount != "123" || result.BroadcastFee != "1" || result.NetAmount != "122" || result.FeeVersion != 1 {
		t.Fatalf("unexpected mixed withdrawal result: %+v", result)
	}
	if len(result.RewardTokens) != 2 || result.RewardTokens[0] != "0x0000000000000000000000000000000000000000" || result.RewardTokens[1] != "0x3333333333333333333333333333333333333333" {
		t.Fatalf("unexpected reward tokens: %v", result.RewardTokens)
	}
	if len(result.Items) != 2 || result.Items[0].WithdrawID != "w" || result.Items[0].Token != result.RewardTokens[0] ||
		result.Items[0].GrossAmount != "123" || result.Items[0].BroadcastFee != "1" || result.Items[0].NetAmount != "122" || result.Items[0].RewardIndex != 0 {
		t.Fatalf("unexpected mixed withdrawal items: %+v", result.Items)
	}
	if result.Items[1].WithdrawID != "w-token" || result.Items[1].Token != result.RewardTokens[1] ||
		result.Items[1].GrossAmount != "10001" || result.Items[1].BroadcastFee != "2" || result.Items[1].NetAmount != "9999" || result.Items[1].RewardIndex != 1 {
		t.Fatalf("unexpected token withdrawal item: %+v", result.Items[1])
	}
	dbm.EnqueueSet(keys.KeyFrostWithdrawArtifact("w"), record.Metadata.ArtifactID)
	dbm.EnqueueDelete(keys.KeyArchiveFrostArtifact(record.Metadata.ArtifactID))
	if err := dbm.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	r = query("/frost/withdraw/result?withdraw_id=w")
	if r.Code != 410 {
		t.Fatalf("pruned=%d %s", r.Code, r.Body)
	}

	                                                                           
	enqueueProto(t, dbm, keys.KeyFrostPermanentWithdraw("w"), wd)
	enqueueProto(t, dbm, keys.KeyFrostPermanentWithdrawJob("job"), jobState)
	enqueueProto(t, dbm, keys.KeyFrostPermanentSignedPackage("job", 0), signedPackage)
	dbm.EnqueueSet(keys.KeyFrostPermanentWithdrawArtifact("w"), record.Metadata.ArtifactID)
	raw, _ := artifact.MarshalRecord(record)
	dbm.EnqueueSet(keys.KeyFrostPermanentArtifact(record.Metadata.ArtifactID), string(raw))
	dbm.EnqueueDelete(keys.KeyArchiveFrostWithdraw("w"))
	dbm.EnqueueDelete(keys.KeyArchiveFrostWithdrawJob("job"))
	dbm.EnqueueDelete(keys.KeyArchiveFrostSignedPackage("job", 0))
	dbm.EnqueueDelete(keys.KeyFrostWithdrawArtifact("w"))
	enqueueProto(t, dbm, keys.KeyFrostWithdraw("w"), &pb.FrostWithdrawState{WithdrawId: "w", Status: "SIGNED"})
	if err := dbm.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	r = query("/frost/withdraw/result?request_tx_id=request")
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &result) != nil || result.Calldata == "" || result.ArtifactID != record.Metadata.ArtifactID {
		t.Fatalf("permanent=%d %s", r.Code, r.Body)
	}
	statusResponse := httptest.NewRecorder()
	hm.HandleGetWithdrawStatus(statusResponse, httptest.NewRequest(http.MethodGet, "/frost/withdraw/status?withdraw_id=w", nil))
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("permanent status=%d %s", statusResponse.Code, statusResponse.Body)
	}
	dbm.EnqueueSet(keys.KeyArchiveFrostArtifact(record.Metadata.ArtifactID), string(raw))
	dbm.EnqueueSet(keys.KeyFrostPermanentArtifact(record.Metadata.ArtifactID), "invalid")
	if err := dbm.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	r = query("/frost/withdraw/result?withdraw_id=w")
	if r.Code != http.StatusConflict {
		t.Fatalf("corrupt permanent hidden by archive=%d %s", r.Code, r.Body)
	}
}
