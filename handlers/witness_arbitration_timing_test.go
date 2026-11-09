package handlers

import (
	"dex/config"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWitnessConfigReportsExplicitArbitrationTiming(t *testing.T) {
	hm, dbMgr := newFrostQueryTestHandler(t)
	defer dbMgr.Close()
	for _, tc := range []struct {
		profile string
		period  uint64
	}{{"", 10000}, {"local20", 20}} {
		hm.cfg = config.DefaultConfig()
		hm.cfg.Witness.ArbitrationTimingProfile = tc.profile
		hm.cfg.Witness.ArbitrationPeriodBlocks = tc.period
		if err := hm.cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		hm.HandleWitnessConfig(rec, httptest.NewRequest(http.MethodGet, "/witness/config", nil))
		var result struct {
			Profile string `json:"arbitration_timing_profile"`
			Period  uint64 `json:"arbitration_period_blocks"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || rec.Code != http.StatusOK || result.Profile != tc.profile || result.Period != tc.period {
			t.Fatalf("effective timing differs from API: profile=%q period=%d response=%s err=%v", tc.profile, tc.period, rec.Body, err)
		}
	}
}
