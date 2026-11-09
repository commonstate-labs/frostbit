package evm

import (
	"bytes"
	"dex/frost/core/curve"
	"dex/pb"
	"math/big"
	"testing"
)

func adapterGuardianPubkeyForTest() []byte {
	g := curve.NewBN256Group()
	pub, _ := CanonicalBN128PublicKey(g.SerializePoint(g.ScalarBaseMult(big.NewInt(29))))
	return pub
}
func TestDualCustodyPackageRejectsMissingGuardian(t *testing.T) {
	a := NewETHAdapter()
	r, e := a.BuildWithdrawTemplate(baseParams())
	if e != nil {
		t.Fatal(e)
	}
	sig := validBN128SigWithYForAdapterTest(t)
	if _, e = a.PackageSigned(r.TemplateData, [][]byte{sig}, nil); e == nil {
		t.Fatal("missing guardian accepted")
	}
	p, e := a.PackageSigned(r.TemplateData, [][]byte{sig}, [][]byte{sig})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.GuardianSignatures) != 1 {
		t.Fatal("lost guardian")
	}
}

func TestDualCustodyGuardianConfigurationFailsClosed(t *testing.T) {
	a := NewETHAdapter()
	for _, size := range []int{0, 63, 64, 65} {
		t.Run(new(big.Int).SetInt64(int64(size)).String(), func(t *testing.T) {
			p := baseParams()
			p.GuardianPubkey = append([]byte(nil), adapterGuardianPubkeyForTest()...)
			if size < 64 {
				p.GuardianPubkey = p.GuardianPubkey[:size]
			} else if size > 64 {
				p.GuardianPubkey = append(p.GuardianPubkey, 0)
			}
			_, err := a.BuildWithdrawTemplate(p)
			if (err == nil) != (size == 64) {
				t.Fatalf("guardian bytes=%d err=%v", size, err)
			}
		})
	}
	for _, protocol := range []uint32{0, 1, 2, 4} {
		p := baseParams()
		p.AuthorityProtocol = protocol
		if _, err := a.BuildWithdrawTemplate(p); err == nil {
			t.Fatalf("accepted protocol%d", protocol)
		}
	}
	p := baseParams()
	p.GuardianPubkey = make([]byte, 64)
	if _, err := a.BuildWithdrawTemplate(p); err == nil {
		t.Fatal("accepted infinity guardian")
	}
	built, err := a.BuildWithdrawTemplate(baseParams())
	if err != nil {
		t.Fatal(err)
	}
	tpl, _ := FromJSON(built.TemplateData)
	tpl.AuthorityProtocol = 2
	raw, _ := tpl.ToJSON()
	sig := validBN128SigWithYForAdapterTest(t)
	if _, err = a.PackageSigned(raw, [][]byte{sig}, [][]byte{sig}); err == nil {
		t.Fatal("packaged old protocol")
	}
	if _, err = a.PackageSigned(built.TemplateData, nil, [][]byte{sig}); err == nil {
		t.Fatal("guardian-only package accepted")
	}
	if _, err = a.PackageSigned(built.TemplateData, [][]byte{sig}, [][]byte{sig, sig}); err == nil {
		t.Fatal("extra guardian accepted")
	}
}
func TestDualCustodyHandoverRequiresDistinctAuthorities(t *testing.T) {
	guard := adapterGuardianPubkeyForTest()
	p := MigrationV2Params{Chain: "eth", ChainID: 1, DexChainID: 51025, ContractAddr: "0x1111111111111111111111111111111111111111", OldEpoch: 1, NewEpoch: 2, NewGroupPubkey: bytes.Clone(guard), GuardianPubkey: guard, FrozenNextBatchSeq: "1"}
	if _, _, err := BuildMigrationTemplateV2(p); err == nil {
		t.Fatal("new operator is guardian")
	}
	p.NewGroupPubkey = formalMultigenerationPub(31)
	built, _, err := BuildMigrationTemplateV2(p)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &pb.FrostVaultConfig{AuthorityProtocol: 3, EvmChainId: 1, DepositDexChainId: 51025, GuardianPubkey: guard, VaultRefs: []string{p.ContractAddr}}
	marker := &pb.FrostVaultMigrationState{AuthorityProtocol: 3, Chain: "eth", OldEpoch: 1, NewEpoch: 2, OldGroupPubkey: guard, NewGroupPubkey: p.NewGroupPubkey, FrozenNextBatchSeq: "1"}
	if _, _, err := CanonicalMigrationTemplate(cfg, marker); err == nil {
		t.Fatal("old operator is guardian")
	}
	tpl, _ := FromJSON(built.TemplateData)
	copy(tpl.Payload[:64], guard)
	raw, _ := tpl.ToJSON()
	if _, err := SigningHashFromTemplateData(raw); err == nil {
		t.Fatal("serialized proof bypassed distinct-authority check")
	}
}
