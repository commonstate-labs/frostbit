package handlers

import (
	"bytes"
	"context"
	btcadapter "dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
)

func frostUTXOTestTaprootScript(tag byte) []byte {
	return append([]byte{txscript.OP_1, txscript.OP_DATA_32}, bytes.Repeat([]byte{tag}, 32)...)
}

func TestFrostVaultUTXOAddressesFollowEachOutputAndCommittedNetwork(t *testing.T) {
	for _, network := range []string{"mainnet", "testnet3", "signet", "regtest", " MAIN ", "testnet", "regression"} {
		t.Run(network, func(t *testing.T) {
			_, dbm := newFrostQueryTestHandler(t)
			defer dbm.Close()
			enqueueProto(t, dbm, keys.KeyFrostVaultConfig("btc", 0), &pb.FrostVaultConfig{Chain: "btc", BtcNetwork: network})
			params, err := btcadapter.NetworkParams(network)
			if err != nil {
				t.Fatal(err)
			}
			for n := 1; n <= 2; n++ {
				txid := fmt.Sprintf("%064x", n)
				enqueueProto(t, dbm, keys.KeyFrostBtcUtxo(4, txid, uint32(n)), &pb.FrostUtxo{
					Txid: txid, Vout: uint32(n), VaultId: 4, Amount: "100000", ScriptPubkey: frostUTXOTestTaprootScript(byte(n)),
				})
			}
			flushFrostUTXOs(t, dbm)
			store := &measuredFrostUTXOStore{store: dbm}
			page, err := queryFrostVaultUTXOs(context.Background(), store, 4, 50, "")
			if err != nil || len(page.Items) != 2 || page.BTCNetwork != params.Name {
				t.Fatalf("page=%+v err=%v", page, err)
			}
			for n, item := range page.Items {
				want, err := btcutil.NewAddressTaproot(bytes.Repeat([]byte{byte(n + 1)}, 32), params)
				if err != nil || item.Address != want.EncodeAddress() {
					t.Fatalf("address=%s want=%v err=%v", item.Address, want, err)
				}
			}
			if page.Items[0].Address == page.Items[1].Address || store.scans != 1 || store.batches != 1 || store.batchSizes[0] != 5 || store.lastKeys[4] != keys.KeyFrostVaultConfig("btc", 0) {
				t.Fatalf("outputs conflated or extra IO: page=%+v scans=%d batches=%v keys=%v", page, store.scans, store.batchSizes, store.lastKeys)
			}
		})
	}
}

func TestFrostUTXOAddressOnlyAcceptsExactAddressScripts(t *testing.T) {
	params := &chaincfg.MainNetParams
	p2pkh, _ := btcutil.NewAddressPubKeyHash(bytes.Repeat([]byte{1}, 20), params)
	p2sh, _ := btcutil.NewAddressScriptHashFromHash(bytes.Repeat([]byte{2}, 20), params)
	p2wpkh, _ := btcutil.NewAddressWitnessPubKeyHash(bytes.Repeat([]byte{3}, 20), params)
	p2wsh, _ := btcutil.NewAddressWitnessScriptHash(bytes.Repeat([]byte{4}, 32), params)
	p2tr, _ := btcutil.NewAddressTaproot(bytes.Repeat([]byte{5}, 32), params)
	for _, address := range []btcutil.Address{p2pkh, p2sh, p2wpkh, p2wsh, p2tr} {
		script, err := txscript.PayToAddrScript(address)
		if err != nil || frostUTXOAddress(script, params) != address.EncodeAddress() {
			t.Fatalf("address=%s err=%v", address, err)
		}
	}
	                                                                        
	                                                                     
	pubkey := append([]byte{2}, bytes.Repeat([]byte{1}, 32)...)
	p2pk := append(append([]byte{txscript.OP_DATA_33}, pubkey...), txscript.OP_CHECKSIG)
	multisig := append(append([]byte{txscript.OP_1, txscript.OP_DATA_33}, pubkey...), txscript.OP_1, txscript.OP_CHECKMULTISIG)
	for _, script := range [][]byte{nil, {}, p2pk, multisig, {txscript.OP_RETURN}, {txscript.OP_DATA_32, 1}, append(frostUTXOTestTaprootScript(1), txscript.OP_NOP)} {
		if got := frostUTXOAddress(script, params); got != "" {
			t.Fatalf("script=%x falsely labelled %s", script, got)
		}
	}
	if frostUTXOAddress(frostUTXOTestTaprootScript(1), nil) != "" {
		t.Fatal("missing network guessed an address")
	}
}

func TestFrostVaultUTXOMissingOrUnknownNetworkKeepsRowsWithoutAddress(t *testing.T) {
	for _, network := range []string{"missing-config", "", "unknown-network"} {
		t.Run(network, func(t *testing.T) {
			_, dbm := newFrostQueryTestHandler(t)
			defer dbm.Close()
			if network != "missing-config" {
				enqueueProto(t, dbm, keys.KeyFrostVaultConfig("btc", 0), &pb.FrostVaultConfig{Chain: "btc", BtcNetwork: network})
			}
			txid := strings.Repeat("a", 64)
			enqueueProto(t, dbm, keys.KeyFrostBtcUtxo(0, txid, 0), &pb.FrostUtxo{Txid: txid, Amount: "100000", ScriptPubkey: frostUTXOTestTaprootScript(1)})
			flushFrostUTXOs(t, dbm)
			page, err := queryFrostVaultUTXOs(context.Background(), dbm, 0, 50, "")
			if err != nil || len(page.Items) != 1 || page.Items[0].Address != "" || page.BTCNetwork != "" {
				t.Fatalf("unknown network guessed an address or lost row: %+v err=%v", page, err)
			}
		})
	}
}

func TestFrostVaultUTXOCorruptNetworkConfigFailsExplicitly(t *testing.T) {
	hm, dbm := newFrostQueryTestHandler(t)
	defer dbm.Close()
	enqueueFrostUTXO(t, dbm, 0, 1)
	dbm.EnqueueSet(keys.KeyFrostVaultConfig("btc", 0), "not-protobuf")
	flushFrostUTXOs(t, dbm)
	response := httptest.NewRecorder()
	hm.HandleFrostVaultUTXOs(response, httptest.NewRequest(http.MethodGet, "/frost/vault/utxos?chain=btc&vault_id=0", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "invalid BTC vault config") {
		t.Fatalf("invalid config hidden: %d %s", response.Code, response.Body)
	}
}
