package handlers

import (
	"dex/config"
	"dex/keys"
	"dex/pb"
	"dex/witness"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWitnessChallengeQuoteSeparatesRemainderFromContribution(t *testing.T) {
	for _, tc := range []struct{ name, required, current, remaining, suggested string }{
		{"new case", "1000000000", "0", "1000000000", "1000000000"},
		{"small remainder", "1000000000", "800000000", "200000000", "1000000000"},
		{"already funded", "1000000000", "20000000000", "0", "1000000000"},
		{"large dynamic total", "30000000000", "1000000000", "29000000000", "29000000000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hm, dbMgr := newFrostQueryTestHandler(t)
			defer dbMgr.Close()
			hm.cfg = &config.Config{Witness: config.DefaultWitnessConfig()}
			hm.cfg.Witness.ChallengeStakeAmount = tc.required
			enqueueProto(t, dbMgr, keys.KeyRechargeRequest("request"), &pb.RechargeRequest{
				RequestId: "request", ChallengeId: "case", Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD,
			})
			enqueueProto(t, dbMgr, keys.KeyChallengeRecord("case"), &pb.ChallengeRecord{
				ChallengeId: "case", RequestId: "request", StakeAmount: tc.current, RequiredStake: tc.required, Status: pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION,
			})
			if err := dbMgr.ForceFlush(); err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			hm.HandleWitnessChallengeQuote(rec, httptest.NewRequest(http.MethodGet, "/witness/challenge_quote?request_id=request", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
			var response struct {
				Remaining string                      `json:"remaining_challenge_stake"`
				Minimum   string                      `json:"min_challenge_contribution"`
				Suggested string                      `json:"suggested_challenge_stake"`
				Quote     witness.ChallengeStakeQuote `json:"quote"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Remaining != tc.remaining || response.Suggested != tc.suggested || response.Minimum != "1000000000" || response.Quote.RequiredChallengeStake != tc.required {
				t.Fatalf("unexpected quote: %+v", response)
			}
		})
	}
}
