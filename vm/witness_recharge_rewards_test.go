package vm

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/protocol"
	statedb "dex/stateDB"
	"dex/utils"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func assertLegacyRechargeWireHasNoNewEconomics(t *testing.T, raw []byte) {
	t.Helper()
	for len(raw) > 0 {
		number, kind, tagLen := protowire.ConsumeTag(raw)
		if tagLen < 0 {
			t.Fatalf("invalid recharge protobuf tag: %d", tagLen)
		}
		if number >= 40 && number <= 44 {
			t.Fatalf("legacy recharge encoded new economics field %d", number)
		}
		raw = raw[tagLen:]
		valueLen := protowire.ConsumeFieldValue(number, kind, raw)
		if valueLen < 0 {
			t.Fatalf("invalid recharge protobuf field %d: %d", number, valueLen)
		}
		raw = raw[valueLen:]
	}
}

func TestLegacyRechargeFinalizationKeepsOldWireAndNoRewardIndex(t *testing.T) {
	sv := NewMockStateView()
	req := seedAccountRechargeFinalizationTest(t, sv, "legacy_reward_wire", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	if err := applyRechargeFinalized(sv, req, 50001, 1); err != nil {
		t.Fatal(err)
	}
	raw, exists, err := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	if err != nil || !exists {
		t.Fatalf("legacy canonical request missing: exists=%v err=%v", exists, err)
	}
	assertLegacyRechargeWireHasNoNewEconomics(t, raw)
	for _, address := range []string{req.ReceiverAddress, req.RequesterAddress} {
		if _, exists, _ := sv.Get(keys.KeyRechargeRewardOwner(address, req.RequestId)); exists {
			t.Fatalf("legacy owner %s acquired new reward index", address)
		}
	}
	if got := GetBalance(sv, req.ReceiverAddress, req.TokenAddress).Balance; got != req.Amount {
		t.Fatalf("legacy receiver amount=%s want=%s", got, req.Amount)
	}
}

func TestLaunchRechargeRewardSplitCreditsOnlyGross(t *testing.T) {
	sv, read, req := requesterBondFixture(t, "launch_reward_split")
	req.Amount = "25001"
	req.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD
	witnesses := []string{
		utils.GenerateTestIdentity(7301).Address,
		utils.GenerateTestIdentity(7302).Address,
		utils.GenerateTestIdentity(7303).Address,
	}
	sort.Strings(witnesses)
	for _, address := range witnesses {
		req.OriginalPositions = append(req.OriginalPositions, &pb.WitnessVote{WitnessAddress: address, VoteType: pb.WitnessVoteType_VOTE_PASS})
	}
	if err := persistRequesterBondState(sv, req); err != nil {
		t.Fatal(err)
	}
	if err := applyRechargeFinalized(sv, req, 50001, 1); err != nil {
		t.Fatal(err)
	}
	if got := GetBalance(sv, req.ReceiverAddress, req.TokenAddress).Balance; got != "24997" {
		t.Fatalf("receiver net=%s want=24997", got)
	}
	if got := GetBalance(sv, req.RequesterAddress, req.TokenAddress).Balance; got != "2" {
		t.Fatalf("requester reward=%s want=2", got)
	}
	if got := GetBalance(sv, witnesses[0], req.TokenAddress).Balance; got != "1" {
		t.Fatalf("first witness reward=%s want=1", got)
	}
	if got := GetBalance(sv, witnesses[1], req.TokenAddress).Balance; got != "1" {
		t.Fatalf("second witness reward=%s want=1", got)
	}
	if got := GetBalance(sv, witnesses[2], req.TokenAddress).Balance; got != "0" {
		t.Fatalf("third witness reward=%s want=0", got)
	}
	if raw, exists, _ := sv.Get(keys.KeyToken(req.TokenAddress)); !exists {
		t.Fatal("bridged token missing")
	} else {
		var token pb.Token
		if err := proto.Unmarshal(raw, &token); err != nil || token.TotalSupply != "25001" {
			t.Fatalf("gross supply=%s err=%v", token.TotalSupply, err)
		}
	}
	if raw, _, _ := sv.Get(keys.KeyFrostVaultFundsLedger(req.NativeChain, req.TokenAddress, req.VaultId)); string(raw) != "25001" {
		t.Fatalf("credited gross ledger=%s", raw)
	}
	if raw, _, _ := sv.Get(keys.KeyFrostVaultAvailableBalance(req.NativeChain, req.TokenAddress, req.VaultId)); string(raw) != "25001" {
		t.Fatalf("vault gross available=%s", raw)
	}
	stored := requesterBondStored(t, sv, req.RequestId)
	if stored.GrossAmount != "25001" || stored.ReceiverNetAmount != "24997" || stored.RequesterRewardAmount != "2" || stored.WitnessRewardAmount != "2" {
		t.Fatalf("canonical reward audit fields=%+v", stored)
	}
	for address, expected := range map[string]string{req.ReceiverAddress: "24997", req.RequesterAddress: "2", witnesses[0]: "1", witnesses[1]: "1"} {
		raw, exists, err := sv.Get(keys.KeyRechargeRewardOwner(address, req.RequestId))
		if err != nil || !exists {
			t.Fatalf("missing owner attribution %s: exists=%v err=%v", address, exists, err)
		}
		var entry pb.RechargeRewardAttribution
		if err := proto.Unmarshal(raw, &entry); err != nil {
			t.Fatal(err)
		}
		actual := entry.ReceiverNetAmount
		if address == req.RequesterAddress {
			actual = entry.RequesterRewardAmount
		} else if address == witnesses[0] || address == witnesses[1] {
			actual = entry.WitnessRewardAmount
		}
		if actual != expected || entry.TokenAddress != req.TokenAddress || entry.RequestId != req.RequestId {
			t.Fatalf("owner attribution %s=%+v want=%s", address, &entry, expected)
		}
	}
	if _, exists, _ := sv.Get(keys.KeyRechargeRewardOwner(witnesses[2], req.RequestId)); exists {
		t.Fatal("zero-credit witness received owner attribution")
	}
	AuditGlobalConservationDiff(sv.Diff(), 50001, read)
	before := len(sv.Diff())
	if err := applyRechargeFinalized(sv, req, 50002, 1); err != nil {
		t.Fatal(err)
	}
	if len(sv.Diff()) != before {
		t.Fatal("replayed finalization changed balances")
	}
	stale := proto.Clone(stored).(*pb.RechargeRequest)
	stale.GrossAmount, stale.ReceiverNetAmount, stale.RequesterRewardAmount, stale.WitnessRewardAmount = "", "", "", ""
	if err := setRechargeRequestWithIndexes(sv, stale, 1); err != nil {
		t.Fatal(err)
	}
	if err := persistWitnessFinancialRequest(sv, stale); err != nil {
		t.Fatal(err)
	}
	if current := requesterBondStored(t, sv, req.RequestId); current.GrossAmount != "25001" || current.ReceiverNetAmount != "24997" {
		t.Fatalf("terminal stale event erased reward audit: %+v", current)
	}
}

func TestLaunchRechargeRewardBatchIOScalesWithWitnesses(t *testing.T) {
	for _, count := range []int{63, 64, 65, 5000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			sv, _, req := requesterBondFixture(t, fmt.Sprintf("launch_reward_io_%d", count))
			req.Amount = fmt.Sprint(count * 10000)
			req.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD
			for i := 0; i < count; i++ {
				req.OriginalPositions = append(req.OriginalPositions, &pb.WitnessVote{
					WitnessAddress: fmt.Sprintf("witness-%05d", i), VoteType: pb.WitnessVoteType_VOTE_PASS,
				})
			}
			if err := persistRequesterBondState(sv, req); err != nil {
				t.Fatal(err)
			}
			view := sv.(*overlayStateView)
			                                                                
			                                                                   
			                                                     
			baseTombstones := make(map[string][]byte, count)
			for i := 0; i < count; i++ {
				baseTombstones[keys.KeyRechargeNativePrefix()+fmt.Sprintf("obsolete-%05d", i)] = []byte("old")
			}
			baseRead, baseScan := view.read, view.scan
			view.read = func(key string) ([]byte, error) {
				if value, ok := baseTombstones[key]; ok {
					return value, nil
				}
				return baseRead(key)
			}
			view.scan = func(prefix string) (map[string][]byte, error) {
				found, err := baseScan(prefix)
				if err != nil {
					return nil, err
				}
				for key, value := range baseTombstones {
					if strings.HasPrefix(key, prefix) {
						found[key] = value
					}
				}
				return found, nil
			}
			for i := 0; i < count; i++ {
				view.Del(keys.KeyRechargeNativePrefix() + fmt.Sprintf("obsolete-%05d", i))
			}
			originalRead, originalScan := view.read, view.scan
			batchCalls, batchKeys, directReads, scans := 0, 0, 0, 0
			scanPrefixes := make(map[string]int)
			view.read = func(key string) ([]byte, error) {
				directReads++
				return originalRead(key)
			}
			view.baseBatchRead = func(batch []string) (map[string][]byte, error) {
				batchCalls++
				batchKeys += len(batch)
				found := make(map[string][]byte, len(batch))
				for _, key := range batch {
					value, err := originalRead(key)
					if err == statedb.ErrNotFound {
						continue
					}
					if err != nil {
						return nil, err
					}
					found[key] = value
				}
				return found, nil
			}
			view.scan = func(prefix string) (map[string][]byte, error) {
				scans++
				scanPrefixes[prefix]++
				return originalScan(prefix)
			}
			if err := applyRechargeFinalized(sv, req, 50001, 1); err != nil {
				t.Fatal(err)
			}
			if batchCalls != 1 || batchKeys > 3*(count+2) || directReads > 30+8*count || scans > 3 {
				t.Fatalf("W=%d batchCalls=%d batchKeys=%d directReads=%d scans=%d prefixes=%v", count, batchCalls, batchKeys, directReads, scans, scanPrefixes)
			}
			t.Logf("W=%d batchCalls=%d batchKeys=%d directReads=%d scans=%d prefixes=%v", count, batchCalls, batchKeys, directReads, scans, scanPrefixes)
		})
	}
}

func TestLaunchRechargeRewardRequiresCorrectWitness(t *testing.T) {
	sv, _, req := requesterBondFixture(t, "launch_reward_no_winner")
	req.Amount = "10000"
	req.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD
	req.OriginalPositions = []*pb.WitnessVote{{WitnessAddress: "wrong", VoteType: pb.WitnessVoteType_VOTE_FAIL}}
	if err := persistRequesterBondState(sv, req); err != nil {
		t.Fatal(err)
	}
	if err := applyRechargeFinalized(sv, req, 50001, 1); err == nil {
		t.Fatal("positive witness reward without eligible recipient was credited")
	}
	if _, exists, _ := sv.Get(keys.KeyToken(req.TokenAddress)); exists {
		t.Fatal("failed credit minted bridged token")
	}
	if _, exists, _ := sv.Get(keys.KeyFrostVaultAvailableBalance(req.NativeChain, req.TokenAddress, req.VaultId)); exists {
		t.Fatal("failed credit increased vault available")
	}
	if _, exists, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); exists {
		t.Fatal("failed credit retained marker")
	}
	if _, exists, _ := sv.Get(keys.KeyRechargeRewardOwner(req.RequesterAddress, req.RequestId)); exists {
		t.Fatal("failed credit retained requester attribution")
	}
	if stored := requesterBondStored(t, sv, req.RequestId); stored.RequesterBondStatus != pb.RequesterBondStatus_REQUESTER_BOND_HELD {
		t.Fatalf("failed credit settled bond: %s", stored.RequesterBondStatus)
	}
	                                                                          
	                                                                         
	req.Amount = "9999"
	if err := persistRequesterBondState(sv, req); err != nil {
		t.Fatal(err)
	}
	if err := applyRechargeFinalized(sv, req, 50002, 1); err != nil {
		t.Fatal(err)
	}
	if got := GetBalance(sv, req.ReceiverAddress, req.TokenAddress).Balance; got != "9999" {
		t.Fatalf("zero-bonus receiver=%s", got)
	}
}

func TestLaunchRechargeRewardSameOwnerKeepsAllRoleAttribution(t *testing.T) {
	sv, read, req := requesterBondFixture(t, "launch_reward_same_owner")
	req.Amount = "30000"
	req.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD
	req.ReceiverAddress = req.RequesterAddress
	req.OriginalPositions = []*pb.WitnessVote{{WitnessAddress: req.RequesterAddress, VoteType: pb.WitnessVoteType_VOTE_PASS}}
	if err := persistRequesterBondState(sv, req); err != nil {
		t.Fatal(err)
	}
	if err := applyRechargeFinalized(sv, req, 50001, 1); err != nil {
		t.Fatal(err)
	}
	if got := GetBalance(sv, req.RequesterAddress, req.TokenAddress).Balance; got != "30000" {
		t.Fatalf("same owner total=%s want=30000", got)
	}
	raw, exists, err := sv.Get(keys.KeyRechargeRewardOwner(req.RequesterAddress, req.RequestId))
	if err != nil || !exists {
		t.Fatalf("same owner attribution missing: exists=%v err=%v", exists, err)
	}
	var attribution pb.RechargeRewardAttribution
	if err := proto.Unmarshal(raw, &attribution); err != nil {
		t.Fatal(err)
	}
	if attribution.ReceiverNetAmount != "29994" || attribution.RequesterRewardAmount != "3" || attribution.WitnessRewardAmount != "3" {
		t.Fatalf("same owner role shares=%+v", &attribution)
	}
	AuditGlobalConservationDiff(sv.Diff(), 50001, read)
}

func TestRechargeRewardFrozenEligibilityAndRoleOverlap(t *testing.T) {
	owner := "same-address"
	req := &pb.RechargeRequest{RequestId: "role_overlap", ReceiverAddress: owner, RequesterAddress: owner,
		Status: pb.RechargeRequestStatus_RECHARGE_ARBITRATION, EconomicsPolicy: protocol.RechargeEconomicsLaunchV1,
		Votes: []*pb.WitnessVote{{WitnessAddress: "stale-event-winner", VoteType: pb.WitnessVoteType_VOTE_PASS}}}
	if _, err := rechargeCreditPayouts(req, big.NewInt(30000)); err == nil {
		t.Fatal("challenged PASS guessed eligibility from stale votes")
	}
	plan, err := rechargeCreditPayouts(req, big.NewInt(30000), []string{owner, owner, "other"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ReceiverNet.String() != "29994" || plan.Payouts[owner].String() != "29998" || plan.Payouts["other"].String() != "2" || len(plan.Payouts) != 2 || plan.WitnessCredits[owner].String() != "1" {
		t.Fatalf("deduplicated overlap: net=%s payouts=%v witness=%v", plan.ReceiverNet, plan.Payouts, plan.WitnessCredits)
	}
	if _, found := plan.Payouts["stale-event-winner"]; found {
		t.Fatal("stale event vote received reward")
	}
}

func TestRechargeEconomicsPolicyFailsClosedAtTerminal(t *testing.T) {
	launch := newStateDBNotFoundStateView(map[string][]byte{})
	launch.(*overlayStateView).BindNetworkChainID(51025)
	req := &pb.RechargeRequest{RequestId: "policy", RequesterBondAmount: "1000000000", RequesterBondStatus: pb.RequesterBondStatus_REQUESTER_BOND_HELD}
	if err := validateRechargeEconomicsPolicy(launch, req); err == nil {
		t.Fatal("51025 accepted missing policy")
	}
	req.EconomicsPolicy = protocol.RechargeEconomicsLaunchV1
	if err := validateRechargeEconomicsPolicy(newStateDBNotFoundStateView(map[string][]byte{}), req); err == nil {
		t.Fatal("launch policy accepted absent trusted context")
	}
	req.RequesterBondStatus = pb.RequesterBondStatus_REQUESTER_BOND_FORFEITED
	if err := validateRechargeEconomicsPolicy(launch, req); err == nil {
		t.Fatal("pending PASS accepted already-forfeited bond")
	}
	req.RequesterBondStatus = pb.RequesterBondStatus_REQUESTER_BOND_HELD
	if err := validateRechargeEconomicsPolicy(launch, req); err != nil {
		t.Fatal(err)
	}
	legacy := newStateDBNotFoundStateView(map[string][]byte{})
	legacy.(*overlayStateView).BindNetworkChainID(1)
	if err := validateRechargeEconomicsPolicy(legacy, req); err == nil {
		t.Fatal("legacy network accepted launch policy")
	}
}
