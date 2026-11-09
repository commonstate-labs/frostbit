package vm

import (
	"bytes"
	"dex/future"
	"dex/keys"
	"dex/pb"
	"math/big"
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestConservationAuditTransitionPlanReadBoundary(t *testing.T) {
	quotes := map[string]string{"BTC_USDT": "USDT"}
	tests := []struct {
		name     string
		key      string
		kind     conservationAuditOpKind
		needsOld bool
	}{
		{name: "token", key: keys.KeyToken("USDT"), kind: conservationAuditToken, needsOld: true},
		{name: "empty token", key: keys.KeyToken(""), kind: conservationAuditToken, needsOld: false},
		{name: "token registry", key: keys.KeyTokenRegistry(), kind: conservationAuditOther, needsOld: false},
		{name: "balance", key: keys.KeyBalance("alice", "USDT"), kind: conservationAuditBalance, needsOld: true},
		{name: "position", key: keys.KeyFuturePosition("position-1"), kind: conservationAuditPosition, needsOld: true},
		{name: "insurance", key: keys.KeyFutureInsuranceFund("BTC_USDT"), kind: conservationAuditInsurance, needsOld: true},
		{name: "insurance shortfall", key: keys.KeyFutureInsuranceShortfall("BTC_USDT"), kind: conservationAuditInsurance, needsOld: false},
		{name: "unknown insurance quote", key: keys.KeyFutureInsuranceFund("BTC.USD"), kind: conservationAuditInsurance, needsOld: false},
		{name: "challenge", key: keys.KeyChallengeRecord("challenge-1"), kind: conservationAuditChallenge, needsOld: true},
		{name: "other", key: "v1_unrelated_state", kind: conservationAuditOther, needsOld: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := planConservationAuditOp(tc.key, quotes)
			if plan.kind != tc.kind || plan.needsOldValue != tc.needsOld {
				t.Fatalf("plan=(kind=%d needsOld=%t), want (kind=%d needsOld=%t)",
					plan.kind, plan.needsOldValue, tc.kind, tc.needsOld)
			}
		})
	}
}

func TestConservationAuditTransitionContract(t *testing.T) {
	const token = "USDT"
	const pair = "BTC_USDT"
	quotes := map[string]string{pair: token}
	tests := []struct {
		name        string
		key         string
		oldValue    []byte
		newValue    []byte
		wantToken   string
		wantDelta   int64
		wantInvalid bool
	}{
		{
			name:      "supply increase is a negative accounting adjustment",
			key:       keys.KeyToken(token),
			oldValue:  mustConservationMarshal(t, &pb.Token{Address: token, TotalSupply: "10"}),
			newValue:  mustConservationMarshal(t, &pb.Token{Address: token, TotalSupply: "12"}),
			wantToken: token,
			wantDelta: -2,
		},
		{
			name: "all balance buckets are included",
			key:  keys.KeyBalance("alice", token),
			oldValue: mustConservationMarshal(t, &pb.TokenBalanceRecord{
				Address: "alice", Token: token,
				Balance: &pb.TokenBalance{Balance: "2", OrderFrozenBalance: "1"},
			}),
			newValue: mustConservationMarshal(t, &pb.TokenBalanceRecord{
				Address: "alice", Token: token,
				Balance: &pb.TokenBalance{Balance: "4", OrderFrozenBalance: "2"},
			}),
			wantToken: token,
			wantDelta: 3,
		},
		{
			name:      "open position margin",
			key:       keys.KeyFuturePosition("position-1"),
			oldValue:  mustConservationMarshal(t, &pb.FuturePositionState{Pair: pair, Margin: "3", Status: int32(future.StatusOpen)}),
			newValue:  mustConservationMarshal(t, &pb.FuturePositionState{Pair: pair, Margin: "5", Status: int32(future.StatusOpen)}),
			wantToken: token,
			wantDelta: 2,
		},
		{
			name:      "insurance fund",
			key:       keys.KeyFutureInsuranceFund(pair),
			oldValue:  big.NewInt(4).Bytes(),
			newValue:  big.NewInt(7).Bytes(),
			wantToken: token,
			wantDelta: 3,
		},
		{
			name: "finalized challenge releases escrow",
			key:  keys.KeyChallengeRecord("challenge-1"),
			oldValue: mustConservationMarshal(t, &pb.ChallengeRecord{
				StakeAmount: "3", Status: pb.ChallengeStatus_CHALLENGE_ACTIVE,
			}),
			newValue: mustConservationMarshal(t, &pb.ChallengeRecord{
				StakeAmount: "3", Status: pb.ChallengeStatus_CHALLENGE_FINALIZED, Finalized: true,
			}),
			wantToken: FeeToken,
			wantDelta: -3,
		},
		{
			name: "mismatched balance token is rejected",
			key:  keys.KeyBalance("alice", token),
			oldValue: mustConservationMarshal(t, &pb.TokenBalanceRecord{
				Address: "alice", Token: token, Balance: &pb.TokenBalance{Balance: "1"},
			}),
			newValue: mustConservationMarshal(t, &pb.TokenBalanceRecord{
				Address: "alice", Token: "BTC", Balance: &pb.TokenBalance{Balance: "1"},
			}),
			wantInvalid: true,
		},
		{
			name: "empty tombstone is neutral",
			key:  keys.KeyBalance("alice", token),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oldBefore := append([]byte(nil), tc.oldValue...)
			newBefore := append([]byte(nil), tc.newValue...)
			quotesBefore := map[string]string{pair: token}
			plan := planConservationAuditOp(tc.key, quotes)
			result := evaluateConservationAuditTransition(conservationAuditTransitionInput{
				Key:          tc.key,
				OldValue:     tc.oldValue,
				NewValue:     tc.newValue,
				FutureQuotes: quotes,
				plan:         plan,
			})

			if !bytes.Equal(tc.oldValue, oldBefore) || !bytes.Equal(tc.newValue, newBefore) || !reflect.DeepEqual(quotes, quotesBefore) {
				t.Fatal("pure transition mutated its input")
			}
			if tc.wantInvalid {
				if result.invalidBalanceRecord == "" || len(result.contributions) != 0 {
					t.Fatalf("invalid result=%+v", result)
				}
				return
			}
			if result.invalidBalanceRecord != "" {
				t.Fatalf("unexpected invalid balance: %s", result.invalidBalanceRecord)
			}
			got := conservationTransitionTotal(result, tc.wantToken)
			if got.Cmp(big.NewInt(tc.wantDelta)) != 0 {
				t.Fatalf("delta=%s want=%d result=%+v", got, tc.wantDelta, result)
			}
			for _, contribution := range result.contributions {
				if contribution.delta == nil {
					t.Fatal("transition returned a nil delta")
				}
			}
		})
	}
}

func conservationTransitionTotal(result conservationAuditTransitionResult, token string) *big.Int {
	total := new(big.Int)
	for _, contribution := range result.contributions {
		if contribution.token == token {
			total.Add(total, contribution.delta)
		}
	}
	return total
}

func mustConservationMarshal(t *testing.T, message proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
