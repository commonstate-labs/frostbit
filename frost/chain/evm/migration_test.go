package evm

import (
	"bytes"
	"testing"
)

func TestBuildMigrationTemplateBindsChainContractNonceAndCannotBePackagedStandalone(t *testing.T) {
	p := MigrationV2Params{Chain: "eth", ChainID: 1, DexChainID: 51025, ContractAddr: "0x1111111111111111111111111111111111111111", VaultID: 0, OldEpoch: 1, NewEpoch: 2, NewGroupPubkey: adapterGuardianPubkeyForTest(), GuardianPubkey: formalMultigenerationPub(31), FrozenNextBatchSeq: "1"}
	r, _, e := BuildMigrationTemplateV2(p)
	if e != nil {
		t.Fatal(e)
	}
	q := p
	q.ChainID = 2
	other, _, e := BuildMigrationTemplateV2(q)
	if e != nil || bytes.Equal(r.SigHashes[0], other.SigHashes[0]) {
		t.Fatal("chain not bound")
	}
	sig := validBN128SigWithYForAdapterTest(t)
	if _, e = NewETHAdapter().PackageSigned(r.TemplateData, [][]byte{sig}, [][]byte{sig}); e == nil {
		t.Fatal("standalone migration accepted")
	}
	if _, _, e = BuildMigrationTemplate("eth", 1, p.ContractAddr, 0, 1, 2, p.NewGroupPubkey); e == nil {
		t.Fatal("old migration accepted")
	}
}
func TestPackageSignedWithHandoverBuildsAtomicCalldata(t *testing.T) {
	a := NewETHAdapter()
	p := baseParams()
	p.KeyEpoch = 2
	w, e := a.BuildWithdrawTemplate(p)
	if e != nil {
		t.Fatal(e)
	}
	m, _, e := BuildMigrationTemplateV2(MigrationV2Params{Chain: "eth", ChainID: 1, DexChainID: 51025, ContractAddr: p.ContractAddr, VaultID: p.VaultID, OldEpoch: 1, NewEpoch: 2, NewGroupPubkey: formalMultigenerationPub(31), GuardianPubkey: p.GuardianPubkey, FrozenNextBatchSeq: "1"})
	if e != nil {
		t.Fatal(e)
	}
	sig := validBN128SigWithYForAdapterTest(t)
	pkg, e := a.PackageSignedWithHandover(w.TemplateData, sig, sig, m.TemplateData, sig, sig)
	if e != nil {
		t.Fatal(e)
	}
	cs, op, g, e := DecodeSignedHandovers(pkg.RawTx)
	if e != nil || len(cs) != 1 || !bytes.Equal(op, sig) || !bytes.Equal(g, sig) || !bytes.Equal(cs[0].GuardianSignature, sig) {
		t.Fatal("dual proof calldata mismatch", e)
	}
}
