package config

import "testing"

func TestAuthorityCapabilityRejectsUnsupportedOrUnboundConfiguration(t *testing.T) {
	for _, name := range []string{"eth", "bnb", "base", "arb", "trx"} {
		if err := ValidateAuthorityProtocol(name, 3, 2, 51025); err != nil {
			t.Fatal(err)
		}
		for _, bad := range []struct {
			version, deposit uint32
			id               uint64
		}{{0, 2, 51025}, {1, 2, 51025}, {2, 2, 51025}, {3, 0, 51025}, {3, 2, 0}} {
			if err := ValidateAuthorityProtocol(name, bad.version, bad.deposit, bad.id); err == nil {
				t.Fatalf("accepted unbound authority: %s %+v", name, bad)
			}
		}
	}
	if err := ValidateAuthorityProtocol("btc", 2, 2, 51025); err == nil {
		t.Fatal("account capability accepted for BTC")
	}
	if err := ValidateAuthorityProtocol("unknown", 2, 2, 51025); err == nil {
		t.Fatal("unknown account chain accepted")
	}
	for _, version := range []uint32{0, 1} {
		if err := ValidateAuthorityProtocol("eth", version, 0, 0); err == nil {
			t.Fatal("single authority accepted")
		}
	}
}
