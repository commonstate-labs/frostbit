package vm

import (
	"bytes"
	"strings"
	"testing"

	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                                                                                         
                                                                                              
                                                                                                     
                                                                            

func seedAccountWithdrawCross(t *testing.T, base map[string][]byte, chainName, asset, withdrawID, to string, vaultID uint32) {
	t.Helper()
	base[keys.KeyFrostWithdrawFIFOHead(chainName, asset)] = []byte("1")
	withdraw := &pb.FrostWithdrawState{
		WithdrawId:   withdrawID,
		Seq:          1,
		Chain:        chainName,
		Asset:        asset,
		To:           to,
		Amount:       "100",
		BroadcastFee: "1",
		Status:       string(WithdrawStatusQueued),
		VaultId:      vaultID,
	}
	base[keys.KeyFrostWithdraw(withdrawID)] = mustMarshalProto(t, withdraw)
}

                                                                                                          
                                                                                             
                                                                                               
func TestBuildCanonicalAccountTemplate_ChainIDBindsHash(t *testing.T) {
	vaultID := uint32(1)
	asset := "USDT"
	withdrawID := "wid_replay_probe"

	baseETH := map[string][]byte{}
	seedAccountWithdrawCross(t, baseETH, "eth", asset, withdrawID, "0xabc0000000000000000000000000000000000001", vaultID)
	                                                                                    
	ethHash := seedAccountChainSignatureStub(t, baseETH, "eth", asset, vaultID, []string{withdrawID})

	baseBNB := map[string][]byte{}
	                                                          
	seedAccountWithdrawCross(t, baseBNB, "bnb", asset, withdrawID, "0xabc0000000000000000000000000000000000001", vaultID)
	bnbHash := seedAccountChainSignatureStub(t, baseBNB, "bnb", asset, vaultID, []string{withdrawID})

	if bytes.Equal(ethHash, bnbHash) {
		t.Fatalf("canonical template hash must differ across chains, got equal: eth=%x bnb=%x", ethHash, bnbHash)
	}
}

                                                                                 
                                                                                                                                
func TestFrostWithdrawSigned_CrossChainReplay_ETHHashOnBNB_Rejected(t *testing.T) {
	vaultID := uint32(1)
	asset := "USDT"
	withdrawID := "wid_replay_eth_to_bnb"
	to := "0xabc0000000000000000000000000000000000002"

	                                                              
	baseETH := map[string][]byte{}
	seedAccountWithdrawCross(t, baseETH, "eth", asset, withdrawID, to, vaultID)
	ethTemplateHash := seedAccountChainSignatureStub(t, baseETH, "eth", asset, vaultID, []string{withdrawID})

	                                                                                  
	baseBNB := map[string][]byte{}
	seedAccountWithdrawCross(t, baseBNB, "bnb", asset, withdrawID, to, vaultID)
	_, bnbJobID := seedAccountChainClaimStub(t, baseBNB, "bnb", asset, vaultID, []string{withdrawID})
	sv := newReadOnlyBaseStateView(baseBNB)

	                                                                     
	handler := &FrostWithdrawSignedTxHandler{}
	signed := &pb.FrostWithdrawSignedTx{
		Base: &pb.BaseMessage{
			TxId:           "tx_replay_eth_to_bnb",
			FromAddress:    "attacker",
			ExecutedHeight: 100,
		},
		JobId:              bnbJobID,
		SignedPackageBytes: make([]byte, evm.SchnorrSigWithYSize), GuardianSigs: [][]byte{make([]byte, evm.SchnorrSigWithYSize)},
		WithdrawIds: []string{withdrawID},
		VaultId:     vaultID,
		KeyEpoch:    1,
		Chain:       "bnb",
	}
	populateMixedSignedTxFromClaim(t, baseBNB, signed)
	signed.TemplateHash = ethTemplateHash                                   
	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed}}

	_, receipt, err := handler.DryRun(tx, sv)
	if err == nil {
		t.Fatal("cross-chain replay with ETH template hash on BNB must be rejected")
	}
	if receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("expected FAILED receipt, got %+v", receipt)
	}
	if !strings.Contains(receipt.Error, "does not match finalized Claim") {
		t.Fatalf("expected finalized Claim mismatch error, got: %s", receipt.Error)
	}

	                                                                               
	data, _, _ := sv.Get(keys.KeyFrostWithdraw(withdrawID))
	var got pb.FrostWithdrawState
	if err := proto.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Status != string(WithdrawStatusSigning) || got.JobId != bnbJobID {
		t.Fatalf("withdraw claim state changed after rejection: %+v", &got)
	}
}

                                                                                                     
                                                                             
func TestBuildCanonicalAccountTemplate_DestinationBindsHash(t *testing.T) {
	vaultID := uint32(1)
	asset := "USDT"
	withdrawID := "wid_dest_probe"

	baseA := map[string][]byte{}
	seedAccountWithdrawCross(t, baseA, "eth", asset, withdrawID, "0xaaaa000000000000000000000000000000000001", vaultID)
	hashA := seedAccountChainSignatureStub(t, baseA, "eth", asset, vaultID, []string{withdrawID})

	baseB := map[string][]byte{}
	seedAccountWithdrawCross(t, baseB, "eth", asset, withdrawID, "0xbbbb000000000000000000000000000000000002", vaultID)
	hashB := seedAccountChainSignatureStub(t, baseB, "eth", asset, vaultID, []string{withdrawID})

	if bytes.Equal(hashA, hashB) {
		t.Fatalf("canonical template hash must differ when destination changes; got equal")
	}
}

                                                                                                                  
func TestBuildCanonicalAccountTemplate_NonceBindsHash(t *testing.T) {
	vaultID := uint32(1)
	asset := "USDT"
	withdrawID := "wid_nonce_probe"

	withSeq := func(seq uint64) []byte {
		base := map[string][]byte{}
		base[keys.KeyFrostWithdrawFIFOHead("eth", asset)] = []byte("1")
		withdraw := &pb.FrostWithdrawState{
			WithdrawId: withdrawID, Seq: seq, Chain: "eth", Asset: asset,
			To:     "0xdead000000000000000000000000000000000001",
			Amount: "100", BroadcastFee: "1", Status: string(WithdrawStatusQueued), VaultId: vaultID,
		}
		base[keys.KeyFrostWithdraw(withdrawID)] = mustMarshalProto(t, withdraw)
		return seedAccountChainSignatureStub(t, base, "eth", asset, vaultID, []string{withdrawID})
	}

	if bytes.Equal(withSeq(1), withSeq(2)) {
		t.Fatal("canonical template hash must differ when Seq/Nonce changes")
	}
}
