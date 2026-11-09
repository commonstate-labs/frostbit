package witness_test

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"strconv"
	"testing"

	"dex/formal/itftrace"
	"dex/pb"
	"dex/witness"
)

                                                                           
                                                                              
func rewardsITFInteger(raw json.RawMessage) (*big.Int, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || len(object) != 1 {
		return nil, fmt.Errorf("expected one-field ITF integer: %s", raw)
	}
	value, ok := object["#bigint"]
	if !ok {
		return nil, fmt.Errorf("missing #bigint: %s", raw)
	}
	var digits string
	if err := json.Unmarshal(value, &digits); err != nil || digits == "" {
		return nil, fmt.Errorf("invalid #bigint string: %s", raw)
	}
	for i, digit := range digits {
		if digit == '-' && i == 0 && len(digits) > 1 {
			continue
		}
		if digit < '0' || digit > '9' {
			return nil, fmt.Errorf("noninteger #bigint: %q", digits)
		}
	}
	n, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, fmt.Errorf("unparseable #bigint: %q", digits)
	}
	return n, nil
}

func rewardsITFMoney(raw json.RawMessage) (map[int64]*big.Int, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || len(object) != 1 {
		return nil, fmt.Errorf("expected ITF #map: %s", raw)
	}
	encoded, ok := object["#map"]
	if !ok {
		return nil, fmt.Errorf("missing #map: %s", raw)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(encoded, &entries); err != nil || len(entries) != 6 {
		return nil, fmt.Errorf("ITF money map must contain all six addresses")
	}
	result := make(map[int64]*big.Int, 6)
	for _, entry := range entries {
		var pair []json.RawMessage
		if err := json.Unmarshal(entry, &pair); err != nil || len(pair) != 2 {
			return nil, fmt.Errorf("invalid ITF map pair: %s", entry)
		}
		key, err := rewardsITFInteger(pair[0])
		if err != nil || !key.IsInt64() || key.Int64() < 0 || key.Int64() > 5 {
			return nil, fmt.Errorf("invalid ITF address: %s", pair[0])
		}
		id := key.Int64()
		if _, duplicate := result[id]; duplicate {
			return nil, fmt.Errorf("duplicate ITF address %d", id)
		}
		value, err := rewardsITFInteger(pair[1])
		if err != nil {
			return nil, fmt.Errorf("address %d: %w", id, err)
		}
		result[id] = value
	}
	return result, nil
}

func rewardsModelMoney(t *testing.T, s itftrace.State, field string) map[int64]*big.Int {
	t.Helper()
	raw, ok := s.Values[field]
	if !ok {
		t.Fatalf("ITF state is missing %s", field)
	}
	got, err := rewardsITFMoney(raw)
	if err != nil {
		t.Fatalf("%s: %v", field, err)
	}
	return got
}

func rewardsModelInt(t *testing.T, s itftrace.State, field string) *big.Int {
	t.Helper()
	raw, ok := s.Values[field]
	if !ok {
		t.Fatalf("ITF state is missing %s", field)
	}
	got, err := rewardsITFInteger(raw)
	if err != nil {
		t.Fatalf("%s: %v", field, err)
	}
	return got
}

func rewardsModelBool(t *testing.T, s itftrace.State, field string) bool {
	t.Helper()
	raw, ok := s.Values[field]
	if !ok || (string(raw) != "true" && string(raw) != "false") {
		t.Fatalf("invalid/missing boolean %s: %s", field, raw)
	}
	return string(raw) == "true"
}

func rewardsModelCourt(t *testing.T, s itftrace.State) []string {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(s.Values["court"], &object); err != nil || len(object) != 1 {
		t.Fatal("invalid court set")
	}
	encoded, ok := object["#set"]
	if !ok || string(encoded) == "null" {
		t.Fatal("court is not an ITF set")
	}
	var values []json.RawMessage
	if err := json.Unmarshal(encoded, &values); err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 && len(values) != 4 {
		t.Fatalf("court has %d members", len(values))
	}
	seen, result := map[int64]bool{}, []string{}
	for _, raw := range values {
		n, err := rewardsITFInteger(raw)
		if err != nil || !n.IsInt64() || n.Int64() < 0 || n.Int64() > 5 || seen[n.Int64()] {
			t.Fatalf("invalid court member: %s", raw)
		}
		seen[n.Int64()] = true
		result = append(result, "a"+n.String())
	}
	return result
}

func rewardsModelVotes(t *testing.T, s itftrace.State, field string) []*pb.WitnessVote {
	t.Helper()
	positions := rewardsModelMoney(t, s, field)
	result := []*pb.WitnessVote{}
	for id := int64(0); id < 6; id++ {
		n := positions[id]
		if !n.IsInt64() || n.Int64() < -1 || n.Int64() > 1 {
			t.Fatalf("%s has invalid direction %s", field, n)
		}
		if n.Sign() < 0 {
			continue
		}
		direction := pb.WitnessVoteType_VOTE_FAIL                             
		if n.Sign() > 0 {
			direction = pb.WitnessVoteType_VOTE_PASS
		}
		result = append(result, &pb.WitnessVote{WitnessAddress: "a" + strconv.FormatInt(id, 10), VoteType: direction})
	}
	return result
}

func rewardsAssertMap(t *testing.T, actual map[string]*big.Int, model map[int64]*big.Int, label string) {
	t.Helper()
	for address, value := range actual {
		if len(address) != 2 || address[0] != 'a' || address[1] < '0' || address[1] > '5' || value == nil || value.Sign() < 0 {
			t.Fatalf("%s invalid Go recipient/value: %s=%v", label, address, value)
		}
	}
	for id := int64(0); id < 6; id++ {
		address := "a" + strconv.FormatInt(id, 10)
		got, exists := actual[address]
		                                                                        
		                                                                       
		if !exists {
			got = new(big.Int)
		}
		if got.Cmp(model[id]) != 0 {
			t.Fatalf("%s[%s]: Go=%s Quint=%s", label, address, got, model[id])
		}
	}
}

func TestFormalArbitrationRewardsITFDifferential(t *testing.T) {
	path := os.Getenv("DEX_FORMAL_ITF_ARBITRATION_REWARDS")
	if path == "" {
		t.Skip("set by formal/run-arbitration-rewards-differential.ps1")
	}
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.States) != 264 {
		t.Fatalf("got %d ITF states, want all 66 four-state cases", len(trace.States))
	}
	                                                                         
	                                                                      
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		States []map[string]json.RawMessage `json:"states"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	settlements, expiries := 0, 0
	for caseID := 0; caseID < 66; caseID++ {
		profile, amount, verdict := caseID/6, []int64{100, 101, 103}[(caseID/2)%3], int64(caseID%2)
		t.Run(fmt.Sprintf("profile_%02d/amount_%d/verdict_%d", profile, amount, verdict), func(t *testing.T) {
			states := trace.States[caseID*4 : caseID*4+4]
			wantPhases := []string{"Unfunded", "Funded", "Slashed", "Settled"}
			if profile == 6 {
				wantPhases[2], wantPhases[3] = "Expired", "Expired"
			}
			for pc, state := range states {
				metadata := itftrace.State{Values: envelope.States[caseID*4+pc]}
				index, err := metadata.Int("itfCase")
				if err != nil || index != int64(caseID) {
					t.Fatalf("ITF case counter: %d %v", index, err)
				}
				step, err := metadata.Int("itfPC")
				if err != nil || step != int64(pc) {
					t.Fatalf("ITF step counter: %d %v", step, err)
				}
				phase, err := state.Tag("phase")
				if err != nil || phase != wantPhases[pc] {
					t.Fatalf("phase %d: %s %v, want %s", pc, phase, err, wantPhases[pc])
				}
				if rewardsModelInt(t, state, "fee").Cmp(big.NewInt(amount)) != 0 || rewardsModelInt(t, state, "finalVote").Cmp(big.NewInt(verdict)) != 0 {
					t.Fatal("initialization variant changed across states")
				}
			}
			initial, before, final := states[0], states[2], states[3]
			if profile == 6 {
				                                                                  
				                                                                  
				if rewardsModelInt(t, final, "burned").Sign() != 0 || rewardsModelInt(t, final, "actualFine").Sign() != 0 || rewardsModelInt(t, final, "feeEscrow").Cmp(big.NewInt(amount)) != 0 {
					t.Fatal("expiry did not preserve fee escrow")
				}
				if !reflect.DeepEqual(rewardsModelMoney(t, final, "principalReturned"), rewardsModelMoney(t, initial, "contributions")) {
					t.Fatal("expiry model did not refund every contribution")
				}
				expiries++
				return
			}
			input := witness.ArbitrationRewardInput{
				Original: rewardsModelVotes(t, before, "original"), Ordinary: rewardsModelVotes(t, before, "ordinary"),
				CourtVotes: rewardsModelVotes(t, before, "courtVote"), Court: rewardsModelCourt(t, before),
				Fee: rewardsModelInt(t, before, "feeEscrow"), Fine: rewardsModelInt(t, before, "slashEscrow"),
				Contributions: map[string]*big.Int{}, Challenged: true, ChallengeSuccess: rewardsModelBool(t, before, "success"),
				FinalVote: pb.WitnessVoteType_VOTE_FAIL,
			}
			if verdict == 1 {
				input.FinalVote = pb.WitnessVoteType_VOTE_PASS
			}
			if input.Fine.Cmp(rewardsModelInt(t, before, "actualFine")) != 0 {
				t.Fatal("kernel fine was not actually collected by the model")
			}
			for id, value := range rewardsModelMoney(t, before, "contributions") {
				if value.Sign() < 0 {
					t.Fatal("negative contribution")
				}
				if value.Sign() > 0 {
					input.Contributions["a"+strconv.FormatInt(id, 10)] = value
				}
			}
			plan, err := witness.BuildArbitrationRewardPlan(input)
			if err != nil {
				t.Fatal(err)
			}
			for _, group := range []struct {
				field  string
				actual map[string]*big.Int
			}{
				{"feePaid", plan.Fees}, {"principalReturned", plan.Refunds}, {"contributorAward", plan.ContributorRewards},
				{"voterAward", plan.VoterRewards}, {"failedPrincipalAward", plan.FailedAwards},
			} {
				rewardsAssertMap(t, group.actual, rewardsModelMoney(t, final, group.field), group.field)
			}
			if plan.Burn.Cmp(rewardsModelInt(t, final, "burned")) != 0 {
				t.Fatalf("burn: Go=%s Quint=%s", plan.Burn, rewardsModelInt(t, final, "burned"))
			}
			settlements++
		})
	}
	if settlements != 60 || expiries != 6 {
		t.Fatalf("coverage incomplete: settlements=%d expiry-model-only=%d", settlements, expiries)
	}
	t.Log("60 true Quint-to-Go reward kernel comparisons; six model-only underfunded expiry cases; no VM debit or persistence claim")
}

func TestFormalArbitrationRewardsITFDecoderRejectsMalformed(t *testing.T) {
	valid := []byte(`{"#map":[[{"#bigint":"0"},{"#bigint":"0"}],[{"#bigint":"1"},{"#bigint":"1"}],[{"#bigint":"2"},{"#bigint":"2"}],[{"#bigint":"3"},{"#bigint":"3"}],[{"#bigint":"4"},{"#bigint":"4"}],[{"#bigint":"5"},{"#bigint":"5"}]]}`)
	if _, err := rewardsITFMoney(valid); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `{}`, `{"#map":null}`, `{"#map":[]}`, `{"#set":[]}`, `{"#map":[[0,0]]}`} {
		if _, err := rewardsITFMoney([]byte(raw)); err == nil {
			t.Fatalf("malformed map accepted: %s", raw)
		}
	}
	var base map[string][]json.RawMessage
	if err := json.Unmarshal(valid, &base); err != nil {
		t.Fatal(err)
	}
	for _, malformed := range []string{`null`, `[]`, `[{"#bigint":"0"}]`, `[{"#bigint":"6"},{"#bigint":"5"}]`, `[{"#bigint":"0"},{"#bigint":"5"}]`, `[{"#bigint":"5"},{}]`, `[{"#bigint":"5"},{"#bigint":"0.5"}]`, `[{"#bigint":"5"},0]`} {
		entries := append([]json.RawMessage(nil), base["#map"]...)
		entries[5] = json.RawMessage(malformed)
		raw, err := json.Marshal(map[string][]json.RawMessage{"#map": entries})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rewardsITFMoney(raw); err == nil {
			t.Fatalf("malformed map entry accepted: %s", malformed)
		}
	}
	for _, raw := range []string{`null`, `{}`, `0`, `"0"`, `{"#bigint":0}`, `{"#bigint":""}`, `{"#bigint":"+1"}`, `{"#bigint":"1.0"}`, `{"#bigint":"-"}`} {
		if _, err := rewardsITFInteger([]byte(raw)); err == nil {
			t.Fatalf("malformed integer accepted: %s", raw)
		}
	}
}
