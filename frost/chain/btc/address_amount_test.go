package btc

import (
	"bytes"
	"math"
	"math/big"
	"strings"
	"testing"

	"dex/frost/chain"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

func TestNormalizeAddressForNetworkUsesStandardChecksumAndNetwork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		network string
		params  *chaincfg.Params
	}{
		{name: "mainnet", network: "mainnet", params: &chaincfg.MainNetParams},
		{name: "testnet3", network: "testnet3", params: &chaincfg.TestNet3Params},
		{name: "regtest", network: "regtest", params: &chaincfg.RegressionNetParams},
		{name: "signet", network: "signet", params: &chaincfg.SigNetParams},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			addr := mustTaprootAddress(t, tt.params)
			canonical, script, err := NormalizeAddressForNetwork(addr.EncodeAddress(), tt.network)
			if err != nil {
				t.Fatalf("normalize address: %v", err)
			}
			if canonical != addr.EncodeAddress() {
				t.Fatalf("canonical address = %q, want %q", canonical, addr.EncodeAddress())
			}
			wantScript, err := txscript.PayToAddrScript(addr)
			if err != nil {
				t.Fatalf("build expected script: %v", err)
			}
			if !bytes.Equal(script, wantScript) {
				t.Fatalf("script = %x, want %x", script, wantScript)
			}
		})
	}

	mainAddr := mustTaprootAddress(t, &chaincfg.MainNetParams).EncodeAddress()
	if _, _, err := NormalizeAddressForNetwork(mainAddr, "testnet3"); err == nil {
		t.Fatal("mainnet address was accepted for testnet3")
	}
	regtestAddr := mustTaprootAddress(t, &chaincfg.RegressionNetParams).EncodeAddress()
	if _, _, err := NormalizeAddressForNetwork(regtestAddr, "mainnet"); err == nil {
		t.Fatal("regtest address was accepted for mainnet")
	}
	if _, _, err := NormalizeAddressForNetwork(corruptAddressChecksum(mainAddr), "mainnet"); err == nil {
		t.Fatal("address with a corrupt checksum was accepted")
	}
	if _, _, err := NormalizeAddressForNetwork(mainAddr, "unknown"); err == nil {
		t.Fatal("unknown network was accepted")
	}
}

func TestAddressScriptSupportsEveryConfiguredNetwork(t *testing.T) {
	t.Parallel()

	for _, params := range []*chaincfg.Params{
		&chaincfg.MainNetParams,
		&chaincfg.TestNet3Params,
		&chaincfg.RegressionNetParams,
		&chaincfg.SigNetParams,
	} {
		addr := mustTaprootAddress(t, params)
		got, err := AddressScript(addr.EncodeAddress())
		if err != nil {
			t.Fatalf("AddressScript(%s): %v", params.Name, err)
		}
		want, err := txscript.PayToAddrScript(addr)
		if err != nil {
			t.Fatalf("PayToAddrScript(%s): %v", params.Name, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("AddressScript(%s) = %x, want %x", params.Name, got, want)
		}
	}

	if _, err := AddressScript("bc1p-this-is-not-an-address"); err == nil {
		t.Fatal("invalid address was converted to a script")
	}
}

func TestBTCAdapterPackageSignedRejectsInvalidOrWrongNetworkAddress(t *testing.T) {
	t.Parallel()

	template := &BTCTemplate{
		Version:  2,
		Inputs:   []TxInput{{TxID: strings.Repeat("11", 32), Amount: 1_000, Sequence: DefaultSequence}},
		Outputs:  []TxOutput{{Address: "not-a-bitcoin-address", Amount: 1_000}},
		VaultID:  1,
		KeyEpoch: 1,
	}
	decorateTestTemplate(template)
	templateData, err := template.ToJSON()
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	adapter := NewBTCAdapter("mainnet")
	if _, err := adapter.PackageSigned(templateData, [][]byte{make([]byte, SchnorrSigSize)}, [][]byte{make([]byte, SchnorrSigSize)}); err == nil {
		t.Fatal("invalid address used the legacy hash fallback")
	}

	template.Outputs[0].Address = mustTaprootAddress(t, &chaincfg.TestNet3Params).EncodeAddress()
	templateData, err = template.ToJSON()
	if err != nil {
		t.Fatalf("marshal wrong-network template: %v", err)
	}
	if _, err := adapter.PackageSigned(templateData, [][]byte{make([]byte, SchnorrSigSize)}, [][]byte{make([]byte, SchnorrSigSize)}); err == nil {
		t.Fatal("testnet address was accepted by the mainnet adapter")
	}

	mainAddr := mustTaprootAddress(t, &chaincfg.MainNetParams)
	template.Outputs[0].Address = mainAddr.EncodeAddress()
	templateData, err = template.ToJSON()
	if err != nil {
		t.Fatalf("marshal valid template: %v", err)
	}
	pkg, err := adapter.PackageSigned(templateData, [][]byte{make([]byte, SchnorrSigSize)}, [][]byte{make([]byte, SchnorrSigSize)})
	if err != nil {
		t.Fatalf("package valid template: %v", err)
	}
	var tx wire.MsgTx
	if err := tx.Deserialize(bytes.NewReader(pkg.RawTx)); err != nil {
		t.Fatalf("decode raw transaction: %v", err)
	}
	wantScript, err := txscript.PayToAddrScript(mainAddr)
	if err != nil {
		t.Fatalf("build expected script: %v", err)
	}
	if len(tx.TxOut) != 1 || !bytes.Equal(tx.TxOut[0].PkScript, wantScript) {
		t.Fatalf("raw transaction output script is not the standard address script")
	}
}

func TestNewBTCTemplateStringAmountBounds(t *testing.T) {
	t.Parallel()

	maxSatoshi := new(big.Int).SetInt64(btcutil.MaxSatoshi)
	tests := []struct {
		name       string
		amount     string
		input      uint64
		wantAmount uint64
		wantErr    bool
	}{
		{name: "minimum positive", amount: "1", input: 1, wantAmount: 1},
		{name: "leading zeroes canonicalized", amount: "00000546", input: 546, wantAmount: 546},
		{name: "max satoshi", amount: maxSatoshi.String(), input: uint64(btcutil.MaxSatoshi), wantAmount: uint64(btcutil.MaxSatoshi)},
		{name: "above max satoshi", amount: new(big.Int).Add(maxSatoshi, big.NewInt(1)).String(), input: 1, wantErr: true},
		{name: "max uint64", amount: new(big.Int).SetUint64(math.MaxUint64).String(), input: 1, wantErr: true},
		{name: "uint256 overflow", amount: new(big.Int).Lsh(big.NewInt(1), 256).String(), input: 1, wantErr: true},
		{name: "zero", amount: "0", input: 1, wantErr: true},
		{name: "negative", amount: "-1", input: 1, wantErr: true},
		{name: "decimal", amount: "1.5", input: 1, wantErr: true},
		{name: "empty", amount: "", input: 1, wantErr: true},
	}

	for _, tt := range tests {
		tc := tt
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			template, err := newTestBTCTemplate(chain.WithdrawTemplateParams{
				WithdrawIDs: []string{"withdraw-1"},
				Outputs: []chain.WithdrawOutput{{
					WithdrawID: "withdraw-1",
					To:         mustTaprootAddress(t, &chaincfg.MainNetParams).EncodeAddress(),
					Amount:     tc.amount,
				}},
				Inputs: []chain.UTXO{{
					TxID:   strings.Repeat("22", 32),
					Amount: tc.input,
				}},
			})
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected amount validation error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewBTCTemplate: %v", err)
			}
			if len(template.Outputs) != 1 || template.Outputs[0].Amount != tc.wantAmount {
				t.Fatalf("template amount = %v, want %d", template.Outputs, tc.wantAmount)
			}
		})
	}
}

func mustTaprootAddress(t *testing.T, params *chaincfg.Params) btcutil.Address {
	t.Helper()
	addr, err := btcutil.NewAddressTaproot(bytes.Repeat([]byte{0x42}, 32), params)
	if err != nil {
		t.Fatalf("create %s taproot address: %v", params.Name, err)
	}
	return addr
}

func corruptAddressChecksum(address string) string {
	if address == "" {
		return "x"
	}
	replacement := byte('q')
	if address[len(address)-1] == replacement {
		replacement = 'p'
	}
	return address[:len(address)-1] + string(replacement)
}
