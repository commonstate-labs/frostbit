package keys

import (
	"fmt"
	"strings"
	"testing"
)

func TestFrostVaultFundsLedgerPrefixes(t *testing.T) {
	key := KeyFrostVaultFundsLedger("btc", "BTC", 7)
	if want := "v1_frost_funds_ledger_btc_BTC_7"; key != want {
		t.Fatalf("ledger key=%q, want %q", key, want)
	}
	for name, prefix := range map[string]string{
		"all":   KeyFrostVaultFundsLedgerAllPrefix(),
		"chain": KeyFrostVaultFundsLedgerPrefix("btc"),
		"asset": KeyFrostVaultFundsLedgerAssetPrefix("btc", "BTC"),
	} {
		if !strings.HasPrefix(key, prefix) {
			t.Fatalf("%s prefix %q does not contain key %q", name, prefix, key)
		}
	}
	if !IsStatefulKey(key) {
		t.Fatal("credited funds ledger must remain consensus state")
	}
}

func TestFrostWithdrawQueryKeysAreNewestFirstAndSlim(t *testing.T) {
	const withdrawID = "withdraw-1"
	global := KeyFrostWithdrawQueryGlobal(100, 7, withdrawID)
	wantGlobal := fmt.Sprintf("v1_frost_withdraw_query_global|height:%020d|seq:%020d|withdraw_id:%s", ^uint64(100), ^uint64(7), withdrawID)
	if global != wantGlobal {
		t.Fatalf("global key=%q, want %q", global, wantGlobal)
	}
	chain := KeyFrostWithdrawQueryChain(" BTC ", 100, 7, withdrawID)
	if !strings.HasPrefix(chain, KeyFrostWithdrawQueryChainPrefix("btc")) {
		t.Fatalf("chain key %q does not match normalized prefix", chain)
	}
	vault := KeyFrostWithdrawQueryChainVault(" BTC ", 9, 100, 7, withdrawID)
	if !strings.HasPrefix(vault, KeyFrostWithdrawQueryChainVaultPrefix("btc", 9)) {
		t.Fatalf("vault key %q does not match normalized prefix", vault)
	}

	if newer, older := KeyFrostWithdrawQueryGlobal(101, 1, "newer"), KeyFrostWithdrawQueryGlobal(100, 999, "older"); newer >= older {
		t.Fatalf("newer request must sort first: newer=%q older=%q", newer, older)
	}
	if highSeq, lowSeq := KeyFrostWithdrawQueryGlobal(100, 8, "high"), KeyFrostWithdrawQueryGlobal(100, 7, "low"); highSeq >= lowSeq {
		t.Fatalf("higher seq must sort first at equal height: high=%q low=%q", highSeq, lowSeq)
	}
	for _, key := range []string{global, chain, vault} {
		if IsStatefulKey(key) {
			t.Fatalf("query index %q must not enter StateDB", key)
		}
		if got := StorageClassForKey(key); got != StorageQuery {
			t.Fatalf("query index %q storage=%s, want query", key, got)
		}
		if !IsPrunableQueryHistoryKey(key) {
			t.Fatalf("query index %q must be recorded by query prune manifests", key)
		}
	}
}

func TestFrostWithdrawLifecycleQueryKeysKeepSortAndProtectActive(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		for _, key := range []string{
			KeyFrostWithdrawQueryGlobalState(terminal, 100, 7, "withdraw-1"),
			KeyFrostWithdrawQueryChainState("BTC", terminal, 100, 7, "withdraw-1"),
			KeyFrostWithdrawQueryChainVaultState("BTC", 9, terminal, 100, 7, "withdraw-1"),
		} {
			if IsStatefulKey(key) || StorageClassForKey(key) != StorageQuery || IsPrunableQueryHistoryKey(key) != terminal {
				t.Fatalf("wrong lifecycle storage/prune classification for %q", key)
			}
		}
	}
	if active, legacy := KeyFrostWithdrawQueryGlobalState(false, 100, 7, "withdraw-1"), KeyFrostWithdrawQueryGlobal(100, 7, "withdraw-1"); active == legacy {
		t.Fatal("old manifest can target new active pointer")
	}
	if newer, older := KeyFrostWithdrawQueryGlobalState(true, 101, 1, "newer"), KeyFrostWithdrawQueryGlobalState(false, 100, 999, "older"); newer >= older {
		t.Fatal("lifecycle suffix changed request-height ordering")
	}
}
