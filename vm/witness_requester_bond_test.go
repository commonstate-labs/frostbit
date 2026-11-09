package vm

import (
	"fmt"
	"math/big"
	"strings"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/rent"
	statedb "dex/stateDB"
	"dex/txverify"
	"dex/utils"
	"dex/witness"
	"google.golang.org/protobuf/proto"
)

func requesterBondFixture(t *testing.T, id string) (StateView, ReadThroughFn, *pb.RechargeRequest) {
	t.Helper()
	owner := utils.GenerateTestIdentity(7104).Address
	base := make(map[string][]byte)
	put := func(key string, msg proto.Message) {
		raw, err := proto.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		base[key] = raw
	}
	put(keys.KeyToken(FeeToken), &pb.Token{Address: FeeToken, TotalSupply: "2000000000"})
	put(keys.KeyBalance(owner, FeeToken), &pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: "2000000000"}})
	read := func(key string) ([]byte, error) {
		value, exists := base[key]
		if !exists {
			return nil, statedb.ErrNotFound
		}
		return value, nil
	}
	scan := func(prefix string) (map[string][]byte, error) {
		out := make(map[string][]byte)
		for key, value := range base {
			if strings.HasPrefix(key, prefix) {
				out[key] = value
			}
		}
		return out, nil
	}
	sv := NewStateView(read, scan)
	sv.(*overlayStateView).BindNetworkChainID(51025)
	req := seedAccountRechargeFinalizationTest(t, sv, id, pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION)
	req.EconomicsPolicy = protocol.RechargeEconomicsLaunchV1
	if err := persistRequesterBondState(sv, req); err != nil {
		t.Fatal(err)
	}
	if err := collectRequesterBond(sv, req); err != nil {
		t.Fatal(err)
	}
	if req.RequesterBondAmount != "1000000000" || req.RequesterBondStatus != pb.RequesterBondStatus_REQUESTER_BOND_HELD {
		t.Fatalf("bond not held: %+v", req)
	}
	if got := GetBalance(sv, owner, FeeToken).Balance; got != "1000000000" {
		t.Fatalf("requester balance after lock = %s", got)
	}
	if got := GetBalance(sv, WitnessRequesterBondPoolAccount, FeeToken).Balance; got != "1000000000" {
		t.Fatalf("bond pool after lock = %s", got)
	}
	if raw, exists, _ := sv.Get(keys.KeyAssetClaimTotal(owner, FeeToken)); !exists || string(raw) != "1000000000" {
		t.Fatalf("nominal claim = %q, exists=%v", raw, exists)
	}
	return sv, read, req
}

func requesterBondStored(t *testing.T, sv StateView, id string) *pb.RechargeRequest {
	t.Helper()
	req := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(id))
	if req == nil {
		t.Fatalf("missing request %s", id)
	}
	return req
}

func TestRequesterBondAllFinalFailsForfeitOnce(t *testing.T) {
	for _, route := range []string{"direct", "indexed", "arbitration"} {
		t.Run(route, func(t *testing.T) {
			sv, read, req := requesterBondFixture(t, "bond_fail_"+route)
			failed := proto.Clone(req).(*pb.RechargeRequest)
			failed.Status = pb.RechargeRequestStatus_RECHARGE_REJECTED
			failed.FinalizeHeight = 50_001
			var err error
			switch route {
			case "direct":
				err = applyRechargeRejected(sv, failed, 1)
			case "indexed":
				err = setRechargeRequestWithIndexes(sv, failed, 1)
			case "arbitration":
				err = applyArbitrationOutcomeToState(sv, &witness.ArbitrationVoteOutcome{Request: failed}, 1, 0, failed.FinalizeHeight)
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := requesterBondStored(t, sv, req.RequestId).RequesterBondStatus; got != pb.RequesterBondStatus_REQUESTER_BOND_FORFEITED {
				t.Fatalf("status=%s", got)
			}
			if got := GetBalance(sv, WitnessRequesterBondPoolAccount, FeeToken).Balance; got != "0" {
				t.Fatalf("pool after burn=%s", got)
			}
			if raw, _, _ := sv.Get(keys.KeyToken(FeeToken)); len(raw) > 0 {
				var token pb.Token
				if err := proto.Unmarshal(raw, &token); err != nil || token.TotalSupply != "1000000000" {
					t.Fatalf("burn supply=%s err=%v", token.TotalSupply, err)
				}
			}
			if raw, exists, _ := sv.Get(keys.KeyAssetClaimTotal(req.RequesterAddress, FeeToken)); exists {
				t.Fatalf("claim retained after burn: %q", raw)
			}
			AuditGlobalConservationDiff(sv.Diff(), failed.FinalizeHeight, read)
			if err := applyRechargeRejected(sv, failed, 1); err != nil {
				t.Fatal(err)
			}
			if err := applyRechargeFinalized(sv, req, failed.FinalizeHeight+1, 1); err != nil {
				t.Fatal(err)
			}
			if got := GetBalance(sv, req.RequesterAddress, FeeToken).Balance; got != "1000000000" {
				t.Fatalf("stale event changed requester balance=%s", got)
			}
		})
	}
}

func TestRequesterBondSuccessfulRechargeRefundsOnce(t *testing.T) {
	sv, read, req := requesterBondFixture(t, "bond_success")
	final := proto.Clone(req).(*pb.RechargeRequest)
	final.Status = pb.RechargeRequestStatus_RECHARGE_FINALIZED
	final.FinalizeHeight = 50_001
	if err := applyArbitrationOutcomeToState(sv, &witness.ArbitrationVoteOutcome{Request: final}, 1, 0, final.FinalizeHeight); err != nil {
		t.Fatal(err)
	}
	if got := requesterBondStored(t, sv, req.RequestId).RequesterBondStatus; got != pb.RequesterBondStatus_REQUESTER_BOND_RETURNED {
		t.Fatalf("status=%s", got)
	}
	if got := GetBalance(sv, req.RequesterAddress, FeeToken).Balance; got != "2000000000" {
		t.Fatalf("refunded balance=%s", got)
	}
	if got := GetBalance(sv, WitnessRequesterBondPoolAccount, FeeToken).Balance; got != "0" {
		t.Fatalf("pool after refund=%s", got)
	}
	AuditGlobalConservationDiff(sv.Diff(), final.FinalizeHeight, read)
	if err := applyRechargeFinalized(sv, final, final.FinalizeHeight+1, 1); err != nil {
		t.Fatal(err)
	}
	if err := applyRechargeRejected(sv, final, 1); err != nil {
		t.Fatal(err)
	}
	if got := GetBalance(sv, req.RequesterAddress, FeeToken).Balance; got != "2000000000" {
		t.Fatalf("replayed outcome changed refund=%s", got)
	}
}

func TestRequesterBondRentRecoveryPreventsDoubleSettlement(t *testing.T) {
	sv, read, req := requesterBondFixture(t, "bond_rent")
	key := keys.KeyAssetClaim(req.RequesterAddress, FeeToken, rentClaimRequesterBond, req.RequestId)
	claims := map[string][]byte{key: []byte(req.RequesterBondAmount)}
	if err := (vmRentCleaner{sv: sv}).PrepareAccountSweepClaimPage(req.RequesterAddress, 40, claims); err != nil {
		t.Fatal(err)
	}
	if got := requesterBondStored(t, sv, req.RequestId).RequesterBondStatus; got != pb.RequesterBondStatus_REQUESTER_BOND_RECOVERED {
		t.Fatalf("status=%s", got)
	}
	if got := GetBalance(sv, rent.RecycleFundAccount, FeeToken).Balance; got != "1000000000" {
		t.Fatalf("recycle fund=%s", got)
	}
	if err := applyRechargeRejected(sv, req, 1); err != nil {
		t.Fatal(err)
	}
	if got := GetBalance(sv, rent.RecycleFundAccount, FeeToken).Balance; got != "1000000000" {
		t.Fatalf("final result moved recovered bond=%s", got)
	}
	if raw, _, _ := sv.Get(keys.KeyToken(FeeToken)); len(raw) > 0 {
		var token pb.Token
		if err := proto.Unmarshal(raw, &token); err != nil || token.TotalSupply != "2000000000" {
			t.Fatalf("recovered bond burned: supply=%s err=%v", token.TotalSupply, err)
		}
	}
	AuditGlobalConservationDiff(sv.Diff(), 40, read)
}

func TestRequesterBondRentRecoveryPageRollsBackOnInvalidClaim(t *testing.T) {
	sv, _, req := requesterBondFixture(t, "bond_rent_atomic")
	valid := keys.KeyAssetClaim(req.RequesterAddress, FeeToken, rentClaimRequesterBond, req.RequestId)
	invalid := keys.KeyAssetClaim(req.RequesterAddress, FeeToken, rentClaimRequesterBond, "zz_missing")
	claims := map[string][]byte{valid: []byte(req.RequesterBondAmount), invalid: []byte(req.RequesterBondAmount)}
	if err := (vmRentCleaner{sv: sv}).sweepRequesterBondsFromClaims(req.RequesterAddress, claims); err == nil {
		t.Fatal("invalid claim accepted")
	}
	if got := requesterBondStored(t, sv, req.RequestId).RequesterBondStatus; got != pb.RequesterBondStatus_REQUESTER_BOND_HELD {
		t.Fatalf("partial rent recovery changed bond=%s", got)
	}
	if got := GetBalance(sv, WitnessRequesterBondPoolAccount, FeeToken).Balance; got != "1000000000" {
		t.Fatalf("partial rent recovery changed pool=%s", got)
	}
	if raw, exists, _ := sv.Get(valid); !exists || string(raw) != "1000000000" {
		t.Fatalf("partial rent recovery erased claim=%q", raw)
	}
}

func TestRequesterBondLegacyRequestNeverCharged(t *testing.T) {
	sv := newStateDBNotFoundStateView(map[string][]byte{})
	seedChallengeSettlementFBToken(t, sv, "1000000000")
	req := &pb.RechargeRequest{RequestId: "legacy_bond", RequesterAddress: "legacy", Status: pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION}
	raw, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyRechargeRequest(req.RequestId), raw)
	if err := settleRequesterBond(sv, req, false); err != nil {
		t.Fatal(err)
	}
	if req.RequesterBondStatus != pb.RequesterBondStatus_REQUESTER_BOND_NONE || req.RequesterBondAmount != "" {
		t.Fatal("legacy request acquired a bond")
	}
	if protocol.WitnessRechargeEconomicsActive(1) {
		t.Fatal("chain 1 must retain legacy recharge economics")
	}
	if amount, err := requesterBondAmount(req); err != nil || amount.Cmp(big.NewInt(0)) != 0 {
		t.Fatalf("legacy amount=%v err=%v", amount, err)
	}
}

func TestLaunchChainFirstRechargeRequiresFeeAndBond(t *testing.T) {
	for _, fee := range []string{"", "0", "999999", "1000000"} {
		t.Run("fee_"+fee, func(t *testing.T) {
			sv, handler := seedAccountDepositAudit(t, "eth")
			cfg, err := loadFrostVaultConfig(sv, "eth")
			if err != nil {
				t.Fatal(err)
			}
			cfg.DepositDexChainId = 51025
			data, err := proto.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			sv.Set(keys.KeyFrostVaultConfig("eth", 0), data)
			identity := utils.GenerateTestIdentity(9001)
			SetBalance(sv, identity.Address, FeeToken, &pb.TokenBalance{Balance: "2000000000"})
			tx := signedAccountDepositAudit(t, 9001, "eth", 0, 1)
			request := tx.GetWitnessRequestTx()
			request.Base.ChainId = 51025
			request.Base.ExecutedHeight = 1
			request.RechargeFee = fee
			if _, err := txverify.SignWithPrivateKey(tx, identity.PrivateKeyHex); err != nil {
				t.Fatal(err)
			}
			if err := txverify.Verify(tx, 51025); err != nil {
				t.Fatal(err)
			}
			_, receipt, err := handler.DryRun(tx, sv)
			if fee != "1000000" {
				if err == nil || receipt == nil || receipt.Status != "FAILED" {
					t.Fatalf("subminimum fee admitted: receipt=%+v err=%v", receipt, err)
				}
				if _, exists, _ := sv.Get(keys.KeyRechargeRequest(tx.GetTxId())); exists {
					t.Fatal("failed request retained canonical state")
				}
				if got := GetBalance(sv, identity.Address, FeeToken).Balance; got != "2000000000" {
					t.Fatalf("failed request charged requester=%s", got)
				}
				return
			}
			if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
				t.Fatalf("launch request failed: receipt=%+v err=%v", receipt, err)
			}
			stored := requesterBondStored(t, sv, tx.GetTxId())
			if stored.RequesterBondAmount != "1000000000" || stored.RequesterBondStatus != pb.RequesterBondStatus_REQUESTER_BOND_HELD {
				t.Fatalf("first-block bond=%+v", stored)
			}
			if got := GetBalance(sv, identity.Address, FeeToken).Balance; got != "999000000" {
				t.Fatalf("fee and bond debit=%s", got)
			}
			if got := GetBalance(sv, WitnessRequesterBondPoolAccount, FeeToken).Balance; got != "1000000000" {
				t.Fatalf("bond pool=%s", got)
			}
			if got := GetBalance(sv, WitnessFeePoolAccount, FeeToken).Balance; got != "1000000" {
				t.Fatalf("fee pool=%s", got)
			}
		})
	}
}

func TestChain51025RechargeEconomicsAtAllHeights(t *testing.T) {
	for _, height := range []uint64{1, 42599, 42600, 1000000} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			sv, handler := seedAccountDepositAudit(t, "eth")
			cfg, err := loadFrostVaultConfig(sv, "eth")
			if err != nil {
				t.Fatal(err)
			}
			cfg.DepositDexChainId = 51025
			data, err := proto.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			sv.Set(keys.KeyFrostVaultConfig("eth", 0), data)
			identity := utils.GenerateTestIdentity(9002)
			SetBalance(sv, identity.Address, FeeToken, &pb.TokenBalance{Balance: "2000000000"})
			tx := signedAccountDepositAudit(t, 9002, "eth", 0, 1)
			request := tx.GetWitnessRequestTx()
			request.Base.ChainId = 51025
			request.Base.ExecutedHeight = height
			request.RechargeFee = "1000000"
			if _, err := txverify.SignWithPrivateKey(tx, identity.PrivateKeyHex); err != nil {
				t.Fatal(err)
			}
			if err := txverify.Verify(tx, 51025); err != nil {
				t.Fatal(err)
			}
			_, receipt, err := handler.DryRun(tx, sv)
			if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
				t.Fatalf("fresh-chain request failed: receipt=%+v err=%v", receipt, err)
			}
			stored := requesterBondStored(t, sv, tx.GetTxId())
			if stored.RequesterBondAmount != "1000000000" || stored.RequesterBondStatus != pb.RequesterBondStatus_REQUESTER_BOND_HELD {
				t.Fatalf("fresh-chain request lacks bond: %+v", stored)
			}
			if got := GetBalance(sv, WitnessRequesterBondPoolAccount, FeeToken).Balance; got != "1000000000" {
				t.Fatalf("fresh-chain bond pool=%s", got)
			}
			if _, exists, _ := sv.Get(keys.KeyProtocolUpgradeReceipt(2)); exists {
				t.Fatal("fresh-chain request emitted obsolete v2 receipt")
			}
		})
	}
}

func TestOtherChainsKeepLegacyRechargeEconomics(t *testing.T) {
	for _, chainID := range []uint64{1, 1001, 1002} {
		t.Run(fmt.Sprint(chainID), func(t *testing.T) {
			sv, handler := seedAccountDepositAudit(t, "eth")
			cfg, err := loadFrostVaultConfig(sv, "eth")
			if err != nil {
				t.Fatal(err)
			}
			cfg.DepositDexChainId = chainID
			data, err := proto.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			sv.Set(keys.KeyFrostVaultConfig("eth", 0), data)
			identity := utils.GenerateTestIdentity(9003)
			tx := signedAccountDepositAudit(t, 9003, "eth", 0, 1)
			request := tx.GetWitnessRequestTx()
			request.Base.ChainId = chainID
			request.Base.ExecutedHeight = 42600
			request.RechargeFee = ""
			if _, err := txverify.SignWithPrivateKey(tx, identity.PrivateKeyHex); err != nil {
				t.Fatal(err)
			}
			if err := txverify.Verify(tx, chainID); err != nil {
				t.Fatal(err)
			}
			_, receipt, err := handler.DryRun(tx, sv)
			if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
				t.Fatalf("legacy request failed: receipt=%+v err=%v", receipt, err)
			}
			stored := requesterBondStored(t, sv, tx.GetTxId())
			if stored.RequesterBondAmount != "" || stored.RequesterBondStatus != pb.RequesterBondStatus_REQUESTER_BOND_NONE {
				t.Fatalf("legacy request acquired bond: %+v", stored)
			}
			if raw, exists, _ := sv.Get(keys.KeyRechargeRequest(tx.GetTxId())); !exists {
				t.Fatal("legacy canonical request missing")
			} else {
				assertLegacyRechargeWireHasNoNewEconomics(t, raw)
			}
		})
	}
}

func TestRequesterBondFailedAdmissionRevertsFeeAndRequest(t *testing.T) {
	sv := newStateDBNotFoundStateView(map[string][]byte{})
	SetBalance(sv, "payer", FeeToken, &pb.TokenBalance{Balance: "500000000"})
	snap := sv.Snapshot()
	req := &pb.RechargeRequest{RequestId: "bond_underfunded", RequesterAddress: "payer", RechargeFee: "1000000", Status: pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION, EconomicsPolicy: protocol.RechargeEconomicsLaunchV1}
	if err := persistRequesterBondState(sv, req); err != nil {
		t.Fatal(err)
	}
	if err := collectWitnessServiceFee(sv, req, true); err != nil {
		t.Fatal(err)
	}
	if err := collectRequesterBond(sv, req); err == nil {
		t.Fatal("underfunded requester bond accepted")
	}
	if err := sv.Revert(snap); err != nil {
		t.Fatal(err)
	}
	if _, exists, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId)); exists {
		t.Fatal("failed admission retained request")
	}
	if _, exists, _ := sv.Get(keys.KeyAssetClaim("payer", FeeToken, rentClaimRequesterBond, req.RequestId)); exists {
		t.Fatal("failed admission retained bond claim")
	}
	if got := GetBalance(sv, "payer", FeeToken).Balance; got != "500000000" {
		t.Fatalf("failed admission charged requester=%s", got)
	}
	if got := GetBalance(sv, WitnessFeePoolAccount, FeeToken).Balance; got != "0" {
		t.Fatalf("failed admission retained witness fee=%s", got)
	}
}

func TestRequesterBondSurvivesArbitrationFinancialPersistence(t *testing.T) {
	for _, finalPass := range []bool{false, true} {
		name := "fail"
		if finalPass {
			name = "pass"
		}
		t.Run(name, func(t *testing.T) {
			sv, _, outcome := settlementFixture(t, !finalPass)
			stored, err := readWitnessSettlementRequest(sv, outcome.Request.RequestId)
			if err != nil {
				t.Fatal(err)
			}
			bond := big.NewInt(protocol.RequesterBondRaw)
			if err := witnessSettlementMove(sv, "payer", bond, false, "fixture_fund_requester", 1, "case"); err != nil {
				t.Fatal(err)
			}
			if _, err := adjustTokenTotalSupplyForAudit(sv, FeeToken, bond); err != nil {
				t.Fatal(err)
			}
			if err := witnessSettlementMove(sv, "payer", bond, true, "requester_bond_lock", 1, "case"); err != nil {
				t.Fatal(err)
			}
			if err := witnessSettlementMove(sv, WitnessRequesterBondPoolAccount, bond, false, "requester_bond_pool", 1, "case"); err != nil {
				t.Fatal(err)
			}
			if err := rent.SetClaim(sv, "payer", FeeToken, rentClaimRequesterBond, "case", bond); err != nil {
				t.Fatal(err)
			}
			stored.RequesterBondAmount = bond.String()
			stored.RequesterBondStatus = pb.RequesterBondStatus_REQUESTER_BOND_HELD
			if err := persistRequesterBondState(sv, stored); err != nil {
				t.Fatal(err)
			}
			                                                                   
			                                                     
			if outcome.Request.RequesterBondAmount != "" {
				t.Fatal("fixture event unexpectedly carried bond")
			}
			if err := settleWitnessArbitration(sv, outcome.Request, outcome.Challenge); err != nil {
				t.Fatal(err)
			}
			if got := requesterBondStored(t, sv, "case").RequesterBondStatus; got != pb.RequesterBondStatus_REQUESTER_BOND_HELD {
				t.Fatalf("arbitration discarded held bond: %s", got)
			}
			if err := settleRequesterBond(sv, outcome.Request, finalPass); err != nil {
				t.Fatal(err)
			}
			want := pb.RequesterBondStatus_REQUESTER_BOND_FORFEITED
			if finalPass {
				want = pb.RequesterBondStatus_REQUESTER_BOND_RETURNED
			}
			if outcome.Request.RequesterBondStatus != want {
				t.Fatalf("arbitration bond status=%s want=%s", outcome.Request.RequesterBondStatus, want)
			}
		})
	}
}

                                                                         
                                                                          
func TestRequesterBondStateViewIOScalesWithRequests(t *testing.T) {
	for _, n := range []int{63, 64, 65, 5000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			base := make(map[string][]byte, 3*n+1)
			marshal := func(msg proto.Message) []byte {
				data, err := proto.Marshal(msg)
				if err != nil {
					t.Fatal(err)
				}
				return data
			}
			base[keys.KeyToken(FeeToken)] = marshal(&pb.Token{Address: FeeToken, TotalSupply: fmt.Sprint(int64(n) * 2 * protocol.RequesterBondRaw)})
			for i := 0; i < n; i++ {
				owner, id := fmt.Sprintf("fbc1bondperf%05d", i), fmt.Sprintf("request-%05d", i)
				base[keys.KeyBalance(owner, FeeToken)] = marshal(&pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: "2000000000"}})
				base[keys.KeyRechargeRequest(id)] = marshal(&pb.RechargeRequest{RequestId: id, RequesterAddress: owner, Status: pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION, EconomicsPolicy: protocol.RechargeEconomicsLaunchV1})
				base[keys.KeyRechargeRequest("obsolete-"+id)] = []byte("old")
			}
			reads, scans := 0, 0
			scanPrefixes := make(map[string]int)
			read := func(key string) ([]byte, error) {
				reads++
				value, exists := base[key]
				if !exists {
					return nil, statedb.ErrNotFound
				}
				return value, nil
			}
			scan := func(prefix string) (map[string][]byte, error) {
				scans++
				scanPrefixes[prefix]++
				out := make(map[string][]byte)
				for key, value := range base {
					if strings.HasPrefix(key, prefix) {
						out[key] = value
					}
				}
				return out, nil
			}
			sv := NewStateView(read, scan)
			sv.(*overlayStateView).BindNetworkChainID(51025)
			for i := 0; i < n; i++ {
				sv.Del(keys.KeyRechargeRequest(fmt.Sprintf("obsolete-request-%05d", i)))
			}
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("request-%05d", i)
				req := requesterBondStored(t, sv, id)
				if err := collectRequesterBond(sv, req); err != nil {
					t.Fatal(err)
				}
				if err := settleRequesterBond(sv, req, false); err != nil {
					t.Fatal(err)
				}
				if err := persistRequesterBondState(sv, req); err != nil {
					t.Fatal(err)
				}
			}
			if scans != 0 || reads > 12*n+8 {
				t.Fatalf("R=%d base reads=%d scans=%d prefixes=%v: exceeded linear point-read bound", n, reads, scans, scanPrefixes)
			}
			if got := GetBalance(sv, WitnessRequesterBondPoolAccount, FeeToken).Balance; got != "0" {
				t.Fatalf("pool remainder=%s", got)
			}
			t.Logf("R=%d base reads=%d scans=%d", n, reads, scans)
		})
	}
}
