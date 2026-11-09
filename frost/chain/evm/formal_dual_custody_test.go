package evm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"testing"

	"dex/frost/chain"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

                                                                           
                                                                            
func oracleBatch(params chain.WithdrawTemplateParams) (formalMultigenerationBatch, error) {
	tokens := map[string]bool{}
	for _, out := range params.Outputs {
		tokens[common.HexToAddress(out.Token).Hex()] = true
	}
	ordered := make([]string, 0, len(tokens))
	for token := range tokens {
		ordered = append(ordered, token)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return bytes.Compare(common.HexToAddress(ordered[i]).Bytes(), common.HexToAddress(ordered[j]).Bytes()) < 0
	})
	indices := map[string]int{}
	batch := formalMultigenerationBatch{Domain: crypto.Keccak256Hash([]byte("DEX_VAULT_MIXED_WITHDRAW_BATCH_V1")),
		ChainId: big.NewInt(1), Vault: common.HexToAddress(params.ContractAddr), Epoch: new(big.Int).SetUint64(params.KeyEpoch), BatchSeq: big.NewInt(1)}
	for i, token := range ordered {
		indices[token] = i
		batch.RewardTokens = append(batch.RewardTokens, common.HexToAddress(token))
	}
	for i, out := range params.Outputs {
		gross, ok := new(big.Int).SetString(out.Amount, 10)
		if !ok {
			return batch, fmt.Errorf("invalid oracle amount")
		}
		token := common.HexToAddress(out.Token)
		batch.Items = append(batch.Items, formalMultigenerationItem{RequestId: crypto.Keccak256Hash([]byte(params.WithdrawIDs[i])), Token: token,
			To: common.HexToAddress(out.To), GrossAmount: gross, RewardIndex: big.NewInt(int64(indices[token.Hex()]))})
	}
	return batch, nil
}

func TestFormalDualCustodyEVMNoSingleSignaturePackage(t *testing.T) {
	adapter := NewETHAdapter()
	result, err := adapter.BuildWithdrawTemplate(formalBatchParams(1, 2))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		op, guard [][]byte
		accepted  bool
	}{
		{"both", [][]byte{formalValidSignatureBytes()}, [][]byte{formalValidSignatureBytes()}, true},
		{"operator-only", [][]byte{formalValidSignatureBytes()}, nil, false},
		{"guardian-only", nil, [][]byte{formalValidSignatureBytes()}, false},
		{"neither", nil, nil, false},
		{"extra-guardian", [][]byte{formalValidSignatureBytes()}, [][]byte{formalValidSignatureBytes(), formalValidSignatureBytes()}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pack, err := adapter.PackageSigned(result.TemplateData, tc.op, tc.guard)
			if (err == nil) != tc.accepted {
				t.Fatalf("package accepted=%v want=%v: %v", err == nil, tc.accepted, err)
			}
			if err == nil && (len(pack.GuardianSignatures) != 1 || !bytes.Equal(pack.GuardianSignatures[0], tc.guard[0])) {
				t.Fatal("delivered package lost guardian signature")
			}
		})
	}
}

func TestFormalDualCustodyEVMRejectsPolicyDowngrade(t *testing.T) {
	adapter := NewETHAdapter()
	for _, version := range []uint32{0, 1, 2} {
		params := formalBatchParams(1, 1)
		params.AuthorityProtocol = version
		if _, err := adapter.BuildWithdrawTemplate(params); err == nil {
			t.Fatalf("protocol %d admitted", version)
		}
	}
	params := formalBatchParams(1, 1)
	params.GuardianPubkey = nil
	if _, err := adapter.BuildWithdrawTemplate(params); err == nil {
		t.Fatal("missing guardian key admitted")
	}
	valid, err := adapter.BuildWithdrawTemplate(formalBatchParams(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []uint32{0, 1, 2} {
		var template EVMTemplate
		if err := json.Unmarshal(valid.TemplateData, &template); err != nil {
			t.Fatal(err)
		}
		template.AuthorityProtocol = version
		raw, err := json.Marshal(&template)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := adapter.PackageSigned(raw, [][]byte{formalValidSignatureBytes()}, [][]byte{formalValidSignatureBytes()}); err == nil {
			t.Fatalf("serialized protocol %d downgrade admitted", version)
		}
	}
}

func TestFormalDualCustodyEVMCertificateRequiresGuardian(t *testing.T) {
	adapter := NewETHAdapter()
	result, err := adapter.BuildWithdrawTemplate(formalBatchParams(2, 1))
	if err != nil {
		t.Fatal(err)
	}
	proof := SignedHandover{FromEpoch: 1, ToEpoch: 2, NewGroupPubkey: formalMultigenerationPub(23), FrozenNextBatchSeq: "1", Signature: formalValidSignatureBytes()}
	if _, err := adapter.PackageSignedWithHandovers(result.TemplateData, formalValidSignatureBytes(), formalValidSignatureBytes(), []SignedHandover{proof}); err == nil {
		t.Fatal("operator-only authority proof packageable")
	}
	proof.GuardianSignature = formalValidSignatureBytes()
	pack, err := adapter.PackageSignedWithHandovers(result.TemplateData, formalValidSignatureBytes(), formalValidSignatureBytes(), []SignedHandover{proof})
	if err != nil {
		t.Fatal(err)
	}
	decoded, op, guard, err := DecodeSignedHandovers(pack.RawTx)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || !bytes.Equal(op, formalValidSignatureBytes()) || !bytes.Equal(guard, formalValidSignatureBytes()) || !bytes.Equal(decoded[0].GuardianSignature, proof.GuardianSignature) {
		t.Fatal("round trip omitted or changed safety authorization")
	}
}
