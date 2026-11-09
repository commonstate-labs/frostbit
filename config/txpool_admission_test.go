package config

import (
	"testing"
	"time"
)

func TestTxPoolAdmissionDefaultsAndValidation(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.TxPool.MaxPendingBytes != 64<<20 || cfg.TxPool.MaxPendingPerAccount != 2048 || cfg.TxPool.MaxPendingAccountBytes != 4<<20 || cfg.TxPool.MaxFutureNonceGap != 2047 || cfg.TxPool.TxExpirationTime != 10*time.Minute {
		t.Fatalf("unexpected pending admission defaults: %+v", cfg.TxPool)
	}
	for _, test := range []struct {
		name string
		set  func(*Config)
	}{
		{"bytes", func(c *Config) { c.TxPool.MaxPendingBytes = 0 }},
		{"account_count", func(c *Config) { c.TxPool.MaxPendingPerAccount = 0 }},
		{"account_bytes", func(c *Config) { c.TxPool.MaxPendingAccountBytes = -1 }},
		{"expiration", func(c *Config) { c.TxPool.TxExpirationTime = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := DefaultConfig()
			test.set(c)
			if err := c.Validate(); err == nil {
				t.Fatal("unsafe pending admission config accepted")
			}
		})
	}
	                                                                             
	cfg.TxPool.MaxFutureNonceGap = 0
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
