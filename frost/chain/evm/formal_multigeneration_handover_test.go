package evm

import (
	"bytes"
	"math/big"
	"strings"
	"testing"

	"dex/frost/core/curve"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

                                                                             
                                                                             
                                    
const formalMultigenerationABI = `[{"type":"function","name":"withdrawBatchWithHandovers","inputs":[{"name":"batch","type":"tuple","components":[{"name":"domain","type":"bytes32"},{"name":"chainId","type":"uint256"},{"name":"vault","type":"address"},{"name":"epoch","type":"uint256"},{"name":"batchSeq","type":"uint256"},{"name":"rewardTokens","type":"address[]"},{"name":"items","type":"tuple[]","components":[{"name":"requestId","type":"bytes32"},{"name":"token","type":"address"},{"name":"to","type":"address"},{"name":"grossAmount","type":"uint256"},{"name":"rewardIndex","type":"uint256"}]}]},{"name":"signature","type":"tuple","components":[{"name":"rx","type":"uint256"},{"name":"ry","type":"uint256"},{"name":"s","type":"uint256"}]},{"name":"guardianSignature","type":"tuple","components":[{"name":"rx","type":"uint256"},{"name":"ry","type":"uint256"},{"name":"s","type":"uint256"}]},{"name":"proofs","type":"tuple[]","components":[{"name":"fromEpoch","type":"uint256"},{"name":"toEpoch","type":"uint256"},{"name":"newPx","type":"uint256"},{"name":"newPy","type":"uint256"},{"name":"frozenNextBatchSeq","type":"uint256"},{"name":"rx","type":"uint256"},{"name":"ry","type":"uint256"},{"name":"s","type":"uint256"},{"name":"guardianRx","type":"uint256"},{"name":"guardianRy","type":"uint256"},{"name":"guardianS","type":"uint256"}]}],"outputs":[]}]`

type formalMultigenerationItem struct {
	RequestId                [32]byte
	Token, To                common.Address
	GrossAmount, RewardIndex *big.Int
}
type formalMultigenerationBatch struct {
	Domain          [32]byte
	ChainId         *big.Int
	Vault           common.Address
	Epoch, BatchSeq *big.Int
	RewardTokens    []common.Address
	Items           []formalMultigenerationItem
}
type formalMultigenerationSignature struct{ Rx, Ry, S *big.Int }
type formalMultigenerationCertificate struct {
	FromEpoch, ToEpoch, NewPx, NewPy, FrozenNextBatchSeq, Rx, Ry, S, GuardianRx, GuardianRy, GuardianS *big.Int
}

func formalMultigenerationPub(n int64) []byte {
	group := curve.NewBN256Group()
	point := group.ScalarBaseMult(big.NewInt(n))
	if point.Y.Cmp(new(big.Int).Rsh(group.Modulus(), 1)) > 0 {
		point.Y.Sub(group.Modulus(), point.Y)
	}
	return group.SerializePoint(point)
}

func TestFormalMultigenerationMigrationDigestIndependentABI(t *testing.T) {
	p := MigrationV2Params{Chain: "eth", ChainID: 1, DexChainID: 51025,
		ContractAddr: "0x1111111111111111111111111111111111111111", VaultID: 7,
		OldEpoch: 2, NewEpoch: 3, NewGroupPubkey: formalMultigenerationPub(23), GuardianPubkey: formalMultigenerationPub(31), FrozenNextBatchSeq: "19"}
	built, _, err := BuildMigrationTemplateV2(p)
	if err != nil {
		t.Fatal(err)
	}
	types := []string{"bytes32", "uint256", "uint256", "address", "uint256", "uint256", "uint256", "uint256", "uint256"}
	args := make(abi.Arguments, 0, len(types))
	for _, name := range types {
		typ, err := abi.NewType(name, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		args = append(args, abi.Argument{Type: typ})
	}
	expected, err := args.Pack(crypto.Keccak256Hash([]byte("DEX_VAULT_AUTHORITY_HANDOVER_V3")),
		big.NewInt(51025), big.NewInt(1), common.HexToAddress(p.ContractAddr), big.NewInt(2), big.NewInt(3),
		new(big.Int).SetBytes(p.NewGroupPubkey[:32]), new(big.Int).SetBytes(p.NewGroupPubkey[32:]), big.NewInt(19))
	if err != nil {
		t.Fatal(err)
	}
	if len(built.SigHashes) != 1 || !bytes.Equal(built.SigHashes[0], crypto.Keccak256(expected)) {
		t.Fatalf("V3 digest differs from independent nine-word ABI: got %x want %x", built.SigHashes, crypto.Keccak256(expected))
	}
	edits := []struct {
		name string
		edit func(*MigrationV2Params)
	}{
		{"DEX chain", func(q *MigrationV2Params) { q.DexChainID++ }},
		{"external chain", func(q *MigrationV2Params) { q.ChainID++ }},
		{"Vault", func(q *MigrationV2Params) { q.ContractAddr = "0x2222222222222222222222222222222222222222" }},
		{"epochs", func(q *MigrationV2Params) { q.OldEpoch++; q.NewEpoch++ }},
		{"new key", func(q *MigrationV2Params) { q.NewGroupPubkey = formalMultigenerationPub(29) }},
		{"cutoff", func(q *MigrationV2Params) { q.FrozenNextBatchSeq = "20" }},
	}
	for _, edit := range edits {
		t.Run(edit.name, func(t *testing.T) {
			q := p
			edit.edit(&q)
			changed, _, err := BuildMigrationTemplateV2(q)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(changed.SigHashes[0], built.SigHashes[0]) {
				t.Fatal("authorization field absent from V3 digest")
			}
		})
	}
	_, _, err = BuildMigrationTemplate("eth", 1, p.ContractAddr, p.VaultID, 2, 3, p.NewGroupPubkey)
	if err == nil {
		t.Fatal("single-authority migration must be rejected in the fresh protocol")
	}
}

func TestFormalMultigenerationHandoverCalldataIndependentABI(t *testing.T) {
	oracle, err := abi.JSON(strings.NewReader(formalMultigenerationABI))
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{1, 2, 7, 8, 9} {
		t.Run(big.NewInt(int64(count)).String(), func(t *testing.T) {
			params := formalBatchParams(uint64(count+1), 2)
			adapter := NewETHAdapter()
			built, err := adapter.BuildWithdrawTemplate(params)
			if err != nil {
				t.Fatal(err)
			}
			proofs := make([]SignedHandover, count)
			oracleProofs := make([]formalMultigenerationCertificate, count)
			for i := range proofs {
				pub := formalMultigenerationPub(int64(17 + i))
				proofs[i] = SignedHandover{FromEpoch: uint64(i + 1), ToEpoch: uint64(i + 2), NewGroupPubkey: pub,
					FrozenNextBatchSeq: "1", Signature: formalValidSignatureBytes(), GuardianSignature: formalValidSignatureBytes()}
				oracleProofs[i] = formalMultigenerationCertificate{big.NewInt(int64(i + 1)), big.NewInt(int64(i + 2)),
					new(big.Int).SetBytes(pub[:32]), new(big.Int).SetBytes(pub[32:]), big.NewInt(1), big.NewInt(1), big.NewInt(2), big.NewInt(1), big.NewInt(1), big.NewInt(2), big.NewInt(1)}
			}
			packaged, err := adapter.PackageSignedWithHandovers(built.TemplateData, formalValidSignatureBytes(), formalValidSignatureBytes(), proofs)
			if count > 8 {
				if err == nil {
					t.Fatal("nine proof chain accepted despite cap eight")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			batch := formalMultigenerationBatch{
				Domain: crypto.Keccak256Hash([]byte("DEX_VAULT_MIXED_WITHDRAW_BATCH_V1")), ChainId: big.NewInt(1),
				Vault: common.HexToAddress(params.ContractAddr), Epoch: big.NewInt(int64(count + 1)), BatchSeq: big.NewInt(1),
				RewardTokens: []common.Address{common.HexToAddress("0x3333333333333333333333333333333333333333")},
			}
			for i, output := range params.Outputs {
				gross, ok := new(big.Int).SetString(output.Amount, 10)
				if !ok {
					t.Fatal("bad fixed amount")
				}
				batch.Items = append(batch.Items, formalMultigenerationItem{RequestId: crypto.Keccak256Hash([]byte(params.WithdrawIDs[i])),
					Token: common.HexToAddress(output.Token), To: common.HexToAddress(output.To), GrossAmount: gross, RewardIndex: big.NewInt(0)})
			}
			expected, err := oracle.Pack("withdrawBatchWithHandovers", batch,
				formalMultigenerationSignature{big.NewInt(1), big.NewInt(2), big.NewInt(1)}, formalMultigenerationSignature{big.NewInt(1), big.NewInt(2), big.NewInt(1)}, oracleProofs)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(packaged.RawTx, expected) {
				t.Fatalf("handover calldata differs from independent ABI\ngot %x\nwant %x", packaged.RawTx, expected)
			}
			broken := append([]SignedHandover(nil), proofs...)
			broken[0].ToEpoch++
			if _, err := adapter.PackageSignedWithHandovers(built.TemplateData, formalValidSignatureBytes(), formalValidSignatureBytes(), broken); err == nil {
				t.Fatal("nonadjacent certificate accepted")
			}
		})
	}
}
