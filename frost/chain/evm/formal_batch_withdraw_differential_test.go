package evm

import (
	"bytes"
	"encoding/hex"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"math/big"
	"strings"
	"testing"

	"dex/frost/chain"
)

                                                                              
                                                                               
                                                                                
                                                 
const formalTwoItemMessageHex = "000000000000000000000000000000000000000000000000000000000000002020f6a54ab25b05f8631eaed69087ed1b151fa46f2c2c6c9bd35470759a81c1dc000000000000000000000000000000000000000000000000000000000000000100000000000000000000000011111111111111111111111111111111111111110000000000000000000000000000000000000000000000000000000000000009000000000000000000000000000000000000000000000000000000000000000100000000000000000000000000000000000000000000000000000000000000e000000000000000000000000000000000000000000000000000000000000001200000000000000000000000000000000000000000000000000000000000000001000000000000000000000000333333333333333333333333333333333333333300000000000000000000000000000000000000000000000000000000000000022bb3ddc4f6566e49049d21e9a5abb7730788919740202c945330f19e0484304f0000000000000000000000003333333333333333333333333333333333333333000000000000000000000000000000000000000000000000000000000000200000000000000000000000000000000000000000000000000000000000000000640000000000000000000000000000000000000000000000000000000000000000c4698cd72fc637122f986b24aa98c0f16f9215268ff368cf4620e2e36a099ac40000000000000000000000003333333333333333333333333333333333333333000000000000000000000000000000000000000000000000000000000000200100000000000000000000000000000000000000000000000000000000000000650000000000000000000000000000000000000000000000000000000000000000"

func formalVectorBytes(t *testing.T, raw string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(raw)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func formalBatchParams(epoch uint64, count int) chain.WithdrawTemplateParams {
	params := chain.WithdrawTemplateParams{
		Chain:             chain.ChainETH,
		AuthorityProtocol: 3, GuardianPubkey: formalMultigenerationPub(31), DexChainID: 51025,
		VaultID:      7,
		KeyEpoch:     epoch,
		BatchSeq:     "1",
		ContractAddr: "0x1111111111111111111111111111111111111111",
	}
	for i := 0; i < count; i++ {
		id := "formal-evm-withdraw-" + new(big.Int).SetInt64(int64(i+1)).String()
		to := "0x0000000000000000000000000000000000002000"
		if i == 1 {
			to = "0x0000000000000000000000000000000000002001"
		}
		if i > 1 {
			to = "0x" + hex.EncodeToString(padLeft(big.NewInt(int64(0x2000+i)).Bytes(), 20))
		}
		params.WithdrawIDs = append(params.WithdrawIDs, id)
		params.Outputs = append(params.Outputs, chain.WithdrawOutput{
			WithdrawID: id, To: to,
			Amount: new(big.Int).SetInt64(int64(100 + i)).String(), BroadcastFee: "1",
			Token: "0x3333333333333333333333333333333333333333",
		})
	}
	return params
}

func formalValidSignatureBytes() []byte {
	out := make([]byte, SchnorrSigWithYSize)
	out[31], out[63], out[95] = 1, 2, 1
	return out
}

func TestFormalEVMBatchWithdrawalABIDifferential(t *testing.T) {
	params := formalBatchParams(9, 2)
	result, err := NewEVMAdapter(chain.ChainETH, 1).BuildWithdrawTemplate(params)
	if err != nil {
		t.Fatalf("BuildWithdrawTemplate: %v", err)
	}
	template, err := FromJSON(result.TemplateData)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	gotMessage, err := template.WithdrawMessage()
	if err != nil {
		t.Fatalf("WithdrawMessage: %v", err)
	}
	wantMessage := formalVectorBytes(t, formalTwoItemMessageHex)
	if !bytes.Equal(gotMessage, wantMessage) {
		t.Fatalf("production batch ABI diverges from ethers ABI fixture\n got %x\nwant %x", gotMessage, wantMessage)
	}
	if len(result.SigHashes) != 1 || !bytes.Equal(result.SigHashes[0], keccak256(wantMessage)) {
		t.Fatalf("signing hash is not keccak256(abi.encode(batch)): %x", result.SigHashes)
	}
	packaged, err := NewEVMAdapter(chain.ChainETH, 1).PackageSigned(result.TemplateData, [][]byte{formalValidSignatureBytes()}, [][]byte{formalValidSignatureBytes()})
	if err != nil {
		t.Fatalf("PackageSigned: %v", err)
	}
	oracle := formalPlainOracle(t)
	batch, err := oracleBatch(params)
	if err != nil {
		t.Fatal(err)
	}
	sig := formalMultigenerationSignature{big.NewInt(1), big.NewInt(2), big.NewInt(1)}
	wantCalldata, err := oracle.Pack("withdrawBatch", batch, sig, sig)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(packaged.RawTx, wantCalldata) {
		t.Fatalf("production withdrawBatch calldata diverges from ethers ABI fixture\n got %x\nwant %x", packaged.RawTx, wantCalldata)
	}
}

func TestFormalEVMBatchSignatureBindsEveryEconomicField(t *testing.T) {
	base := formalBatchParams(9, 2)
	baseResult, err := NewETHAdapter().BuildWithdrawTemplate(base)
	if err != nil {
		t.Fatal(err)
	}
	baseHash := baseResult.SigHashes[0]
	mutations := []struct {
		name string
		edit func(*chain.WithdrawTemplateParams)
	}{
		{"batch-seq", func(p *chain.WithdrawTemplateParams) { p.BatchSeq = "2" }},
		{"gross", func(p *chain.WithdrawTemplateParams) { p.Outputs[1].Amount = "102" }},
		{"recipient", func(p *chain.WithdrawTemplateParams) { p.Outputs[1].To = "0x4444444444444444444444444444444444444444" }},
		{"request-id", func(p *chain.WithdrawTemplateParams) {
			p.WithdrawIDs[1] = "changed"
			p.Outputs[1].WithdrawID = "changed"
		}},
		{"order", func(p *chain.WithdrawTemplateParams) {
			p.WithdrawIDs[0], p.WithdrawIDs[1] = p.WithdrawIDs[1], p.WithdrawIDs[0]
			p.Outputs[0], p.Outputs[1] = p.Outputs[1], p.Outputs[0]
		}},
		{"token", func(p *chain.WithdrawTemplateParams) {
			for i := range p.Outputs {
				p.Outputs[i].Token = "0x5555555555555555555555555555555555555555"
			}
		}},
		{"epoch", func(p *chain.WithdrawTemplateParams) { p.KeyEpoch++ }},
		{"vault", func(p *chain.WithdrawTemplateParams) { p.ContractAddr = "0x6666666666666666666666666666666666666666" }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			candidate := base
			candidate.WithdrawIDs = append([]string(nil), base.WithdrawIDs...)
			candidate.Outputs = append([]chain.WithdrawOutput(nil), base.Outputs...)
			mutation.edit(&candidate)
			result, err := NewETHAdapter().BuildWithdrawTemplate(candidate)
			if err != nil {
				t.Fatalf("mutated template unexpectedly invalid: %v", err)
			}
			if bytes.Equal(baseHash, result.SigHashes[0]) {
				t.Fatalf("%s is not bound by signing hash", mutation.name)
			}
		})
	}
}

func TestFormalEVMBatchBoundAndStandaloneHandoverNegativeControls(t *testing.T) {
	adapter := NewETHAdapter()
	if MaxWithdrawalsPerBatch != 64 {
		t.Fatalf("production batch bound=%d want 64", MaxWithdrawalsPerBatch)
	}
	if MaxWithdrawalsPerBatch != chain.MaxWithdrawalsPerBatch {
		t.Fatalf("adapter batch bound=%d differs from shared bound=%d", MaxWithdrawalsPerBatch, chain.MaxWithdrawalsPerBatch)
	}
	if _, err := adapter.BuildWithdrawTemplate(formalBatchParams(1, 64)); err != nil {
		t.Fatalf("exact batch bound rejected: %v", err)
	}
	if _, err := adapter.BuildWithdrawTemplate(formalBatchParams(1, 65)); err != ErrInvalidOutput {
		t.Fatalf("oversized batch error=%v want ErrInvalidOutput", err)
	}
	if _, _, err := BuildMigrationTemplate(chain.ChainETH, 1, formalBatchParams(1, 1).ContractAddr, 7, 1, 2, formalMultigenerationPub(23)); err == nil {
		t.Fatal("single-authority migration constructor accepted")
	}
	migration, _, err := BuildMigrationTemplateV2(MigrationV2Params{Chain: "eth", ChainID: 1, DexChainID: 51025, ContractAddr: formalBatchParams(1, 1).ContractAddr, VaultID: 7, OldEpoch: 1, NewEpoch: 2, NewGroupPubkey: formalMultigenerationPub(23), GuardianPubkey: formalMultigenerationPub(31), FrozenNextBatchSeq: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.PackageSigned(migration.TemplateData, [][]byte{formalValidSignatureBytes()}, [][]byte{formalValidSignatureBytes()}); err != ErrInvalidTemplateData {
		t.Fatalf("standalone authority proof was packageable: %v", err)
	}
}

func TestFormalEVMHandoverCalldataDifferential(t *testing.T) {
	adapter := NewETHAdapter()
	params := formalBatchParams(2, 2)
	withdraw, err := adapter.BuildWithdrawTemplate(params)
	if err != nil {
		t.Fatal(err)
	}
	migration, _, err := BuildMigrationTemplateV2(MigrationV2Params{Chain: "eth", ChainID: 1, DexChainID: 51025, ContractAddr: params.ContractAddr, VaultID: params.VaultID, OldEpoch: 1, NewEpoch: 2, NewGroupPubkey: formalMultigenerationPub(23), GuardianPubkey: params.GuardianPubkey, FrozenNextBatchSeq: "1"})
	if err != nil {
		t.Fatal(err)
	}
	sig := formalValidSignatureBytes()
	combined, err := adapter.PackageSignedWithHandover(withdraw.TemplateData, sig, sig, migration.TemplateData, sig, sig)
	if err != nil {
		t.Fatal(err)
	}
	chainPack, err := adapter.PackageSignedWithHandovers(withdraw.TemplateData, sig, sig, []SignedHandover{{FromEpoch: 1, ToEpoch: 2, NewGroupPubkey: formalMultigenerationPub(23), FrozenNextBatchSeq: "1", Signature: sig, GuardianSignature: sig}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(combined.RawTx, chainPack.RawTx) {
		t.Fatal("single-certificate helper diverges from the independently ABI-checked certificate path")
	}
}

func formalPlainOracle(t *testing.T) abi.ABI {
	t.Helper()
	oracle, err := abi.JSON(strings.NewReader(formalMultigenerationABI))
	if err != nil {
		t.Fatal(err)
	}
	source := oracle.Methods["withdrawBatchWithHandovers"]
	method := abi.NewMethod("withdrawBatch", "withdrawBatch", abi.Function, "nonpayable", false, false, source.Inputs[:3], nil)
	return abi.ABI{Methods: map[string]abi.Method{"withdrawBatch": method}}
}
