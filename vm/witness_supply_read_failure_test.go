package vm

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"dex/keys"
	"dex/pb"
)

type rechargeSupplyReadFailureView struct {
	StateView
	tokenKey string
	reads    int
}

func (s *rechargeSupplyReadFailureView) Get(key string) ([]byte, bool, error) {
	if key == s.tokenKey {
		s.reads++
		if s.reads == 2 {
			return nil, false, fmt.Errorf("injected supply read failure")
		}
	}
	return s.StateView.Get(key)
}

func TestBTCRechargeSupplyReadFailureRollsBackAndCanRetry(t *testing.T) {
	sv := newStateDBNotFoundStateView(map[string][]byte{})
	req := seedBTCRechargeFinalizationTest(t, sv, "btc-supply-read-failure", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	before, err := sv.Scan("")
	if err != nil {
		t.Fatal(err)
	}
	fault := &rechargeSupplyReadFailureView{StateView: sv, tokenKey: keys.KeyToken(req.TokenAddress)}
	if err := applyRechargeFinalized(fault, req, 70, 1); err == nil || !strings.Contains(err.Error(), "injected supply read failure") || fault.reads != 2 {
		t.Fatalf("late read failure not exercised: reads=%d err=%v", fault.reads, err)
	}
	after, err := sv.Scan("")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed supply read left partial balance, UTXO, available, ledger or fee writes")
	}
	if err := applyRechargeFinalized(sv, req, 71, 1); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := GetBalance(sv, req.ReceiverAddress, req.TokenAddress).Balance; got != "7000" {
		t.Fatalf("retry amount=%s", got)
	}
}
