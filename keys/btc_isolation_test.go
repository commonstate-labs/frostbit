package keys

import (
	"sort"
	"strings"
	"testing"
)

func TestBTCAmountIndexCanonicalOrderingAndCursorBoundaries(t *testing.T) {
	txid := strings.Repeat("ab", 32)
	amounts := []uint64{18446744073709551615, 100001, 99999, 100000, 1}
	var ordered []string
	for _, amount := range amounts {
		ordered = append(ordered, KeyFrostBtcUtxoAmount(6, amount, txid, 4294967295))
	}
	sort.Strings(ordered)
	var previous uint64
	for _, key := range ordered {
		vault, amount, id, vout, err := ParseFrostBtcUtxoAmountKey(key)
		if err != nil || vault != 6 || id != txid || vout != 4294967295 || amount < previous {
			t.Fatalf("bad index key %q: %v", key, err)
		}
		previous = amount
	}
	key := KeyFrostBtcUtxoAmount(6, 100000, txid, 3)
	for _, bad := range []string{strings.Replace(key, "0000000006", "6", 1), strings.Replace(key, txid, strings.ToUpper(txid), 1), key + "_junk", strings.Replace(key, "00000000000000100000", "100000", 1)} {
		if _, _, _, _, err := ParseFrostBtcUtxoAmountKey(bad); err == nil {
			t.Fatalf("accepted noncanonical key %q", bad)
		}
	}
	if !(KeyFrostBtcWithdrawPart("parent", 9) < KeyFrostBtcWithdrawPart("parent", 10)) {
		t.Fatal("part cursor lexical order differs from sequence")
	}
	if strings.HasPrefix(KeyFrostBtcMigrationActiveChild("parent", 1), KeyFrostBtcMigrationChildPrefix("parent")) {
		t.Fatal("active and all-child scans overlap")
	}
}
