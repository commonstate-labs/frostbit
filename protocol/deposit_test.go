package protocol

import (
	"strings"
	"testing"
)

func TestCanonicalExternalTxHashRejectsAliases(t *testing.T) {
	for _, chain := range []string{"btc", "eth", "bnb", "base", "arb", "trx"} {
		t.Run(chain, func(t *testing.T) {
			raw := strings.Repeat("ab", 32)
			if chain != "btc" && chain != "trx" {
				raw = "0x" + raw
			}
			if got, err := CanonicalExternalTxHash(chain, raw); err != nil || got != raw {
				t.Fatalf("valid hash=%q err=%v", got, err)
			}
			for _, alias := range []string{" " + raw, raw + " ", strings.ToUpper(raw)} {
				if _, err := CanonicalExternalTxHash(chain, alias); err == nil {
					t.Errorf("accepted alias %q", alias)
				}
				if normalized, err := NormalizeExternalTxHash(chain, alias); err != nil || normalized != raw {
					t.Errorf("migration failed hash=%q err=%v", normalized, err)
				}
			}
			for _, bad := range []string{"", raw[:len(raw)-1], raw + "00", strings.Repeat("g", 64)} {
				if _, err := CanonicalExternalTxHash(chain, bad); err == nil {
					t.Errorf("accepted malformed hash %q", bad)
				}
			}
		})
	}
	if _, err := CanonicalExternalTxHash("other", strings.Repeat("a", 64)); err == nil {
		t.Fatal("unsupported chain accepted")
	}
}
