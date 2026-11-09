package vm

import (
	"bytes"
	"strings"
	"testing"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                                                           
  
                                          
                                                                                                                       
                                                                                          
                                                                                
  
                                                                                                                       
                                                                                             
  
                                                                                                                          

type preimageWithdraw struct {
	ID     string
	Seq    uint64
	To     string
	Amount string
}

type preimageCase struct {
	Chain        string
	Asset        string
	VaultID      uint32
	KeyEpoch     uint64
	ContractAddr string                                         
	Withdraws    []preimageWithdraw
}

func preimageBaseline() preimageCase {
	return preimageCase{
		Chain:        "eth",
		Asset:        "USDT",
		VaultID:      1,
		KeyEpoch:     1,
		ContractAddr: "0x000000000000000000000000000000000000dead",
		Withdraws: []preimageWithdraw{
			{ID: "wid_matrix_base_001", Seq: 1,
				To:     "0xabc0000000000000000000000000000000000001",
				Amount: "100"},
		},
	}
}

                                                                                            
                                                                                                 
                                                        
func computePreimageHash(t *testing.T, c preimageCase) []byte {
	t.Helper()

	base := map[string][]byte{}

	                                                                                                             
	refs := make([]string, int(c.VaultID)+1)
	for i := range refs {
		refs[i] = "0x0000000000000000000000000000000000000000"
	}
	refs[c.VaultID] = c.ContractAddr
	cfg := &pb.FrostVaultConfig{AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: accountGuardianTestKey(t),
		Chain:          c.Chain,
		SignAlgo:       pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		VaultCount:     uint32(len(refs)),
		VaultRefs:      refs,
		ThresholdRatio: 0.67,
	}
	cfgData, err := proto.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal vault config: %v", err)
	}
	base[keys.KeyFrostVaultConfig(c.Chain, 0)] = cfgData

	                                    
	vs := &pb.FrostVaultState{
		VaultId:     c.VaultID,
		Chain:       c.Chain,
		KeyEpoch:    c.KeyEpoch,
		GroupPubkey: make([]byte, 64),
		SignAlgo:    pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		Status:      "ACTIVE",
	}
	vsData, err := proto.Marshal(vs)
	if err != nil {
		t.Fatalf("marshal vault state: %v", err)
	}
	base[keys.KeyFrostVaultState(c.Chain, c.VaultID)] = vsData

	                              
	base[keys.KeyFrostWithdrawFIFOHead(c.Chain, c.Asset)] = []byte("1")
	ids := make([]string, 0, len(c.Withdraws))
	for _, w := range c.Withdraws {
		ws := &pb.FrostWithdrawState{
			WithdrawId: w.ID, Seq: w.Seq, Chain: c.Chain, Asset: c.Asset,
			To: w.To, Amount: w.Amount, BroadcastFee: "1",
			Status: string(WithdrawStatusQueued), VaultId: c.VaultID,
		}
		data, err := proto.Marshal(ws)
		if err != nil {
			t.Fatalf("marshal withdraw state: %v", err)
		}
		base[keys.KeyFrostWithdraw(w.ID)] = data
		ids = append(ids, w.ID)
	}

	normalizeMixedAccountWithdrawFixtures(t, base, c.Chain, c.Asset, c.VaultID, ids)
	sv := newReadOnlyBaseStateView(base)
	tpl, _, err := buildCanonicalAccountTemplate(sv, c.Chain, c.Asset, c.VaultID, c.KeyEpoch, ids)
	if err != nil {
		t.Fatalf("buildCanonicalAccountTemplate: %v", err)
	}
	return tpl.TemplateHash
}

                                                                                                           
func TestPreimageMatrix_Baseline_Deterministic(t *testing.T) {
	c := preimageBaseline()
	h1 := computePreimageHash(t, c)
	h2 := computePreimageHash(t, c)
	if !bytes.Equal(h1, h2) {
		t.Fatalf("相同输入必须得到相同 canonical hash；h1=%x h2=%x", h1, h2)
	}
	if len(h1) == 0 {
		t.Fatal("canonical hash 不应为空")
	}
}

                                                                            
                                                                                        
func TestPreimageMatrix_FieldMutationsChangeHash(t *testing.T) {
	base := preimageBaseline()
	baseHash := computePreimageHash(t, base)

	mutations := []struct {
		name   string
		mutate func(*preimageCase)
	}{
		{
			name: "VaultID 变化",
			mutate: func(c *preimageCase) {
				c.VaultID = 2
				                                                          
				c.ContractAddr = "0x000000000000000000000000000000000000dead"
			},
		},
		{
			name: "KeyEpoch 变化",
			mutate: func(c *preimageCase) {
				c.KeyEpoch = 2
			},
		},
		                                                                                                 
		                                                                                       
		                                             
		                                                                                                   
		                                                                                          
		                                                                           
		                                                                 
		{
			name: "ContractAddr 变化",
			mutate: func(c *preimageCase) {
				c.ContractAddr = "0x000000000000000000000000000000000000beef"
			},
		},
		{
			name: "Amount 变化",
			mutate: func(c *preimageCase) {
				c.Withdraws[0].Amount = "101"
			},
		},
		{
			name: "WithdrawID 字符串本身变化（同 Seq/To/Amount）",
			mutate: func(c *preimageCase) {
				c.Withdraws[0].ID = "wid_matrix_base_001_renamed"
			},
		},
	}

	for _, m := range mutations {
		m := m
		t.Run(m.name, func(t *testing.T) {
			mutated := preimageBaseline()
			m.mutate(&mutated)
			got := computePreimageHash(t, mutated)
			if bytes.Equal(got, baseHash) {
				t.Fatalf("字段"+m.name+"未进入 preimage：base=%x got=%x", baseHash, got)
			}
		})
	}
}

func TestBuildCanonicalAccountTemplateRejectsTokenNativeChainMismatch(t *testing.T) {
	vaultID := uint32(1)
	withdrawID := "wid_asset_mismatch_probe"
	asset := "USDT"

	base := map[string][]byte{}
	seedAccountWithdrawCross(t, base, "eth", asset, withdrawID,
		"0xabc0000000000000000000000000000000000001", vaultID)
	seedAccountChainSignatureStub(t, base, "eth", asset, vaultID, []string{withdrawID})
	base[keys.KeyToken(asset)] = mustMarshalProto(t, &pb.Token{
		Address: asset, Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: "bnb",
	})
	sv := newReadOnlyBaseStateView(base)
	if _, _, err := buildCanonicalAccountTemplate(sv, "eth", "", vaultID, 1, []string{withdrawID}); err == nil || !strings.Contains(err.Error(), "token metadata mismatch") {
		t.Fatalf("cross-chain token metadata must fail closed, got %v", err)
	}
}
