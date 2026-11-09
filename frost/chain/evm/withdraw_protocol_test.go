package evm

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"dex/frost/chain"
	"dex/frost/core/curve"
	"github.com/btcsuite/btcd/btcutil/base58"
)

func TestTRONAddressFormsUseIdenticalABI(t *testing.T) {
	decoded, version, err := base58.CheckDecode("TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t")
	if err != nil || version != 0x41 {
		t.Fatal("bad public test vector")
	}
	forms := []string{"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", "41" + hex.EncodeToString(decoded), hex.EncodeToString(decoded), "0x" + hex.EncodeToString(decoded)}
	var want string
	for _, form := range forms {
		got, err := NormalizeAccountAddress("trx", form, false)
		if err != nil {
			t.Fatalf("%s: %v", form, err)
		}
		if want == "" {
			want = got
		}
		if got != want {
			t.Fatalf("TRON ABI address differs: %s != %s", got, want)
		}
	}
	if _, err := NormalizeAccountAddress("trx", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6u", false); err == nil {
		t.Fatal("bad Base58 checksum accepted")
	}
	if _, err := NormalizeAccountAddress("eth", forms[1], false); err == nil {
		t.Fatal("TRON address accepted as Ethereum")
	}
}

func TestWithdrawSigningDomainAndRequestIDAreBound(t *testing.T) {
	original, err := NewETHAdapter().BuildWithdrawTemplate(baseParams())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		mutate  func(*chain.WithdrawTemplateParams)
		network uint64
	}{
		{"request", func(p *chain.WithdrawTemplateParams) {
			p.WithdrawIDs[0] = "another-request"
			p.Outputs[0].WithdrawID = "another-request"
		}, 1},
		{"vault-address", func(p *chain.WithdrawTemplateParams) { p.ContractAddr = "0x1111111111111111111111111111111111111111" }, 1},
		{"epoch", func(p *chain.WithdrawTemplateParams) { p.KeyEpoch++ }, 1},
		{"chain", func(p *chain.WithdrawTemplateParams) {}, 31337},
		{"amount", func(p *chain.WithdrawTemplateParams) { p.Outputs[0].Amount = "1001" }, 1},
		{"token", func(p *chain.WithdrawTemplateParams) {
			p.Outputs[0].Token = "0x2222222222222222222222222222222222222222"
		}, 1},
		{"recipient", func(p *chain.WithdrawTemplateParams) { p.Outputs[0].To = "0x3333333333333333333333333333333333333333" }, 1},
	}
	pub, sig := signBN128WithYForAdapterTest(t, original.SigHashes[0])
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := baseParams()
			tc.mutate(&p)
			changed, err := NewEVMAdapter("eth", tc.network).BuildWithdrawTemplate(p)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(changed.SigHashes[0], original.SigHashes[0]) {
				t.Fatal("digest unchanged")
			}
			if valid, err := NewETHAdapter().VerifySignature(pub, changed.SigHashes[0], sig); err != nil || valid {
				t.Fatalf("old signature valid for changed domain: valid=%v err=%v", valid, err)
			}
		})
	}
}

func TestWithdrawRejectsAmbiguousIdentityAndSelfPayment(t *testing.T) {
	for _, ids := range [][]string{nil, {""}, {" "}, {"w_0001", "w_0001"}, {" w_0001"}, {"w_0001 "}} {
		p := baseParams()
		p.WithdrawIDs = ids
		if _, err := NewETHAdapter().BuildWithdrawTemplate(p); err == nil {
			t.Fatalf("invalid IDs accepted: %q", ids)
		}
	}
	p := baseParams()
	p.Outputs[0].To = p.ContractAddr
	if _, err := NewETHAdapter().BuildWithdrawTemplate(p); err == nil {
		t.Fatal("self payment accepted")
	}
}

func TestCanonicalBN128RejectsMalformedAndKeepsRawKey(t *testing.T) {
	grp := curve.NewBN256Group()
	_, raw := nonCanonicalBN128KeyForTest(t, grp)
	before := append([]byte(nil), raw...)
	canonical, err := CanonicalBN128PublicKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, before) || bytes.Equal(raw, canonical) {
		t.Fatal("canonicalization mutated DKG record or retained high Y")
	}
	if new(big.Int).SetBytes(canonical[32:]).Cmp(new(big.Int).Rsh(grp.Modulus(), 1)) > 0 {
		t.Fatal("high Y output")
	}
	badKeys := [][]byte{make([]byte, 64), bytes.Repeat([]byte{1}, 64), raw[:63], append(raw, 0)}
	for _, coordinate := range []int{0, 32} {
		bad := append([]byte(nil), raw...)
		grp.Modulus().FillBytes(bad[coordinate : coordinate+32])
		badKeys = append(badKeys, bad)
	}
	for _, bad := range badKeys {
		if _, err := CanonicalBN128PublicKey(bad); err == nil {
			t.Fatal("invalid key accepted")
		}
		if _, _, err := BuildMigrationTemplate("eth", 1, "0x"+strings.Repeat("1", 40), 0, 1, 2, bad); err == nil {
			t.Fatal("invalid migration key accepted")
		}
	}
}
