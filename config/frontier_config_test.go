package config

import "testing"

func TestDefaultPeerGateConfigLimitsAcceptedFrontier(t *testing.T) {
	cfg := DefaultPeerGateConfig()
	if cfg.TrustedRPS != 500 || cfg.TrustedBurst != 1000 {
		t.Fatalf("trusted consensus limiter=%v/%d want=500/1000", cfg.TrustedRPS, cfg.TrustedBurst)
	}
	if got := cfg.PathRateLimits["/acceptedfrontier"]; got != 2000 {
		t.Fatalf("accepted-frontier path rate=%v want=2000", got)
	}
}
