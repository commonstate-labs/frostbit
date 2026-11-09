package vm

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"dex/formal/itftrace"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/rent"
	"dex/txverify"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

const formalRequesterBondUnit int64 = 100_000_000

                                                                        
                                                                      
                                                                       
func TestFormalRequesterBondGenesisChainAdmission(t *testing.T) {
	const startingFB int64 = 2_000_000_000
	cases := []struct {
		name    string
		chainID uint64
		height  uint64
		fee     string
		accept  bool
		bond    bool
	}{
		{"launch-empty-fee", 51025, 1, "", false, false},
		{"launch-zero-fee", 51025, 1, "0", false, false},
		{"launch-below-minimum", 51025, 1, "999999", false, false},
		{"launch-exact-minimum", 51025, 1, "1000000", true, true},
		{"launch-before-former-v2-below-minimum", 51025, 42599, "999999", false, false},
		{"launch-before-former-v2-exact-minimum", 51025, 42599, "1000000", true, true},
		{"launch-at-former-v2-below-minimum", 51025, 42600, "999999", false, false},
		{"launch-at-former-v2-exact-minimum", 51025, 42600, "1000000", true, true},
		{"launch-future-below-minimum", 51025, 1000000, "999999", false, false},
		{"launch-future-exact-minimum", 51025, 1000000, "1000000", true, true},
		{"launch-future-above-minimum", 51025, 1000000, "1000001", true, true},
		{"chain-one-genesis", 1, 1, "0", true, false},
		{"local-dev-genesis", 1001, 1, "0", true, false},
		{"local-test-genesis", 1002, 1, "0", true, false},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user := utils.GenerateTestIdentity(85200 + index)
			sv := newStateDBNotFoundStateView(map[string][]byte{})
			vault := &pb.FrostVaultConfig{
				Chain: "eth", VaultCount: 1, DepositProtocol: protocol.DepositProtocolVault,
				DepositDexChainId: tc.chainID,
				VaultRefs:         []string{"0x2222222222222222222222222222222222222222"},
			}
			formalStoreProto(t, sv, keys.KeyFrostVaultConfig("eth", 0), vault)
			putTestVaultStateWithPubkeyHex(t, sv, "eth", 0, VaultStatusActive, btcRechargeTestPubKeyHex)
			sv.Set(keys.KeyRechargeLegacyMigration(), []byte("1"))
			sv.Set(keys.KeyRechargePendingMigration(), []byte("1"))
			SetBalance(sv, user.Address, FeeToken, &pb.TokenBalance{Balance: strconv.FormatInt(startingFB, 10)})
			formalStoreProto(t, sv, keys.KeyToken(FeeToken), &pb.Token{Address: FeeToken, TotalSupply: strconv.FormatInt(startingFB, 10)})
			request := &pb.WitnessRequestTx{
				Base: &pb.BaseMessage{FromAddress: user.Address, ChainId: tc.chainID,
					Nonce: 1, Fee: "1000", ExecutedHeight: tc.height},
				NativeChain: "eth", NativeTxHash: "0x" + strings.Repeat("ab", 32),
				Amount: "100", ReceiverAddress: user.Address,
				VaultId: 0, DepositProtocol: protocol.DepositProtocolVault,
				RechargeFee: tc.fee,
			}
			tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: request}}
			if _, err := txverify.SignWithPrivateKey(tx, user.PrivateKeyHex); err != nil {
				t.Fatal(err)
			}
			if err := txverify.Verify(tx, tc.chainID); err != nil {
				t.Fatalf("signed transaction not valid for its own chain: %v", err)
			}
			otherChain := uint64(1)
			if tc.chainID == 1 {
				otherChain = 51025
			}
			if err := txverify.Verify(tx, otherChain); err == nil {
				t.Fatalf("chain %d signature accepted on chain %d", tc.chainID, otherChain)
			}
			diffBefore := len(sv.Diff())
			_, receipt, err := (&WitnessRequestTxHandler{}).DryRun(tx, sv)
			if !tc.accept {
				if err == nil || receipt == nil || receipt.Status != "FAILED" {
					t.Fatalf("subminimum fee admitted: receipt=%+v err=%v", receipt, err)
				}
				if len(sv.Diff()) != diffBefore {
					t.Fatal("rejected request changed StateView")
				}
				if got := GetBalance(sv, user.Address, FeeToken).Balance; got != strconv.FormatInt(startingFB, 10) {
					t.Fatalf("rejected request debited owner=%s", got)
				}
				return
			}
			if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
				t.Fatalf("expected admission: receipt=%+v err=%v", receipt, err)
			}
			stored := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(tx.GetTxId()))
			if stored == nil {
				t.Fatal("admitted request missing")
			}
			feeRaw, err := strconv.ParseInt(tc.fee, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			bondRaw := int64(0)
			wantBondStatus := pb.RequesterBondStatus_REQUESTER_BOND_NONE
			if tc.bond {
				bondRaw = protocol.RequesterBondRaw
				wantBondStatus = pb.RequesterBondStatus_REQUESTER_BOND_HELD
			}
			if stored.RequesterBondStatus != wantBondStatus ||
				(tc.bond && stored.RequesterBondAmount != strconv.FormatInt(bondRaw, 10)) ||
				(!tc.bond && stored.RequesterBondAmount != "") {
				t.Fatalf("bond attribution mismatch: %+v", stored)
			}
			if stored.FeeEscrow != tc.fee {
				t.Fatalf("fee escrow=%q want=%q", stored.FeeEscrow, tc.fee)
			}
			for _, item := range []struct {
				name, address string
				want          int64
			}{
				{"owner", user.Address, startingFB - feeRaw - bondRaw},
				{"fee pool", WitnessFeePoolAccount, feeRaw},
				{"bond pool", WitnessRequesterBondPoolAccount, bondRaw},
			} {
				if got := GetBalance(sv, item.address, FeeToken).Balance; got != strconv.FormatInt(item.want, 10) {
					t.Fatalf("%s balance=%s want=%d", item.name, got, item.want)
				}
			}
			claim, exists, err := sv.Get(keys.KeyAssetClaimTotal(user.Address, FeeToken))
			if err != nil {
				t.Fatal(err)
			}
			if tc.bond && (!exists || string(claim) != strconv.FormatInt(bondRaw, 10)) ||
				!tc.bond && exists {
				t.Fatalf("bond claim=%q exists=%v", claim, exists)
			}
			var token pb.Token
			data, exists, err := sv.Get(keys.KeyToken(FeeToken))
			if err != nil || !exists || proto.Unmarshal(data, &token) != nil || token.TotalSupply != strconv.FormatInt(startingFB, 10) {
				t.Fatalf("admission changed supply: exists=%v err=%v token=%+v", exists, err, &token)
			}
		})
	}
}

                                                                            
                                                                          
                                                                            
func TestFormalRequesterBondFreshGenesisOmitsFormerV2Migration(t *testing.T) {
	if schedule := protocol.UpgradeSchedule(51025); len(schedule) != 0 {
		t.Fatalf("fresh launch chain retained old upgrade schedule: %+v", schedule)
	}
	sv := newStateDBNotFoundStateView(map[string][]byte{})
	btc := &pb.FrostVaultConfig{Chain: "btc", VaultCount: 1, BtcFeeSatsPerVbyte: 25}
	formalStoreProto(t, sv, keys.KeyFrostVaultConfig("btc", 0), btc)
	initialWrites := len(sv.Diff())
	x := &Executor{networkChainID: 51025}
	for _, height := range []uint64{1, 42599, 42600, 1000000} {
		if err := x.applyProtocolMigrations(sv, height); err != nil {
			t.Fatalf("height %d applied old migration: %v", height, err)
		}
		if len(sv.Diff()) != initialWrites {
			t.Fatalf("height %d wrote a migration diff", height)
		}
		for _, key := range []string{keys.KeyProtocolVersion(), keys.KeyProtocolUpgradeReceipt(2)} {
			if raw, exists, err := sv.Get(key); err != nil || exists {
				t.Fatalf("height %d retained %s: raw=%q exists=%v err=%v", height, key, raw, exists, err)
			}
		}
		raw, exists, err := sv.Get(keys.KeyFrostVaultConfig("btc", 0))
		if err != nil || !exists {
			t.Fatalf("height %d lost BTC genesis config: exists=%v err=%v", height, exists, err)
		}
		var stored pb.FrostVaultConfig
		if err := proto.Unmarshal(raw, &stored); err != nil || stored.BtcFeeSatsPerVbyte != 25 {
			t.Fatalf("height %d changed BTC genesis fee: stored=%+v err=%v", height, &stored, err)
		}
	}
}

type formalRequesterBondModel struct {
	Available0 int64
	Available1 int64
	Claim0     int64
	Pool       int64
	Recycle    int64
	Supply     int64
	Burned     int64
	Phase      string
	Bond       string
	Legacy     bool
	Settled    int64
}

func formalBondITFInt(raw json.RawMessage) (int64, error) {
	var encoded struct {
		Number string `json:"#bigint"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return 0, err
	}
	if encoded.Number == "" {
		return 0, fmt.Errorf("not an ITF integer: %s", raw)
	}
	return strconv.ParseInt(encoded.Number, 10, 64)
}

func formalBondITFMapValue(raw json.RawMessage, index int64) (json.RawMessage, error) {
	var encoded struct {
		Entries [][2]json.RawMessage `json:"#map"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	for _, entry := range encoded.Entries {
		key, err := formalBondITFInt(entry[0])
		if err != nil {
			return nil, err
		}
		if key == index {
			return entry[1], nil
		}
	}
	return nil, fmt.Errorf("ITF map has no key %d", index)
}

func formalBondITFTag(raw json.RawMessage) (string, error) {
	var encoded struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return "", err
	}
	if encoded.Tag == "" {
		return "", fmt.Errorf("not an ITF tag: %s", raw)
	}
	return encoded.Tag, nil
}

func formalRequesterBondFromITF(model itftrace.State) (formalRequesterBondModel, error) {
	fields := model.Values
	readInt := func(name string) (int64, error) { return formalBondITFInt(fields[name]) }
	readMapInt := func(name string, index int64) (int64, error) {
		raw, err := formalBondITFMapValue(fields[name], index)
		if err != nil {
			return 0, err
		}
		return formalBondITFInt(raw)
	}
	var out formalRequesterBondModel
	var err error
	if out.Available0, err = readMapInt("available", 0); err != nil {
		return out, err
	}
	if out.Available1, err = readMapInt("available", 1); err != nil {
		return out, err
	}
	if out.Claim0, err = readMapInt("claim", 0); err != nil {
		return out, err
	}
	for name, target := range map[string]*int64{
		"pool": &out.Pool, "recycleFund": &out.Recycle,
		"totalSupply": &out.Supply, "burned": &out.Burned,
	} {
		if *target, err = readInt(name); err != nil {
			return out, err
		}
	}
	rawReq, err := formalBondITFMapValue(fields["requests"], 0)
	if err != nil {
		return out, err
	}
	var req struct {
		Phase       json.RawMessage `json:"phase"`
		Bond        json.RawMessage `json:"bond"`
		Legacy      bool            `json:"legacy"`
		Settlements json.RawMessage `json:"settlements"`
	}
	if err = json.Unmarshal(rawReq, &req); err != nil {
		return out, err
	}
	if out.Phase, err = formalBondITFTag(req.Phase); err != nil {
		return out, err
	}
	if out.Bond, err = formalBondITFTag(req.Bond); err != nil {
		return out, err
	}
	if out.Settled, err = formalBondITFInt(req.Settlements); err != nil {
		return out, err
	}
	out.Legacy = req.Legacy
	return out, nil
}

func formalBondRaw(amount int64) string {
	return strconv.FormatInt(amount*formalRequesterBondUnit, 10)
}

                                                                              
                                                                             
                                                                             
                                                                            
func TestFormalRequesterBondITFDifferential(t *testing.T) {
	cases := []struct {
		name string
		env  string
		kind string
	}{
		{"success", "DEX_FORMAL_ITF_REQUESTER_BOND_SUCCESS", "success"},
		{"direct-reject", "DEX_FORMAL_ITF_REQUESTER_BOND_REJECT", "direct-reject"},
		{"indexed-reject", "DEX_FORMAL_ITF_REQUESTER_BOND_REJECT", "indexed-reject"},
		{"arbitration-financial-pass", "DEX_FORMAL_ITF_REQUESTER_BOND_SUCCESS", "arbitration-pass"},
		{"arbitration-financial-fail", "DEX_FORMAL_ITF_REQUESTER_BOND_REJECT", "arbitration-fail"},
		{"legacy-reject", "DEX_FORMAL_ITF_REQUESTER_BOND_LEGACY_REJECT", "legacy-reject"},
		{"rent-then-success", "DEX_FORMAL_ITF_REQUESTER_BOND_RENT_SUCCESS", "rent-success"},
		{"rent-then-reject", "DEX_FORMAL_ITF_REQUESTER_BOND_RENT_REJECT", "rent-reject"},
	}
	if os.Getenv(cases[0].env) == "" {
		t.Skip("requester bond ITF paths are set by formal/run-go-differential.ps1")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runFormalRequesterBondTrace(t, os.Getenv(tc.env), tc.kind)
		})
	}
}

func runFormalRequesterBondTrace(t *testing.T, path, kind string) {
	t.Helper()
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.States) < 4 || len(trace.States) > 5 {
		t.Fatalf("requester bond trace length=%d", len(trace.States))
	}
	const owner = "formal-requester-bond-owner"
	const second = "formal-requester-bond-second"
	requestID := "formal-bond-" + kind
	sv := newMemStateView()
	req := seedAccountRechargeFinalizationTest(t, sv, requestID, pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION)
	req.RequesterAddress = owner
	req.CreateHeight = 10
	staleFinancial := proto.Clone(req).(*pb.RechargeRequest)
	if kind == "legacy-reject" {
		sv.(*overlayStateView).BindNetworkChainID(1)
	} else {
		sv.(*overlayStateView).BindNetworkChainID(51025)
		req.EconomicsPolicy = protocol.RechargeEconomicsLaunchV1
	}
	formalStoreProto(t, sv, keys.KeyRechargeRequest(requestID), req)
	SetBalance(sv, owner, FeeToken, &pb.TokenBalance{Balance: formalBondRaw(40), WitnessLockedBalance: formalBondRaw(20)})
	SetBalance(sv, second, FeeToken, &pb.TokenBalance{Balance: formalBondRaw(40)})
	SetBalance(sv, WitnessRequesterBondPoolAccount, FeeToken, &pb.TokenBalance{Balance: "0"})
	SetBalance(sv, rent.RecycleFundAccount, FeeToken, &pb.TokenBalance{Balance: "0"})
	formalStoreProto(t, sv, keys.KeyToken(FeeToken), &pb.Token{Address: FeeToken, TotalSupply: formalBondRaw(100)})

	check := func(index int) formalRequesterBondModel {
		t.Helper()
		model, err := formalRequesterBondFromITF(trace.States[index])
		if err != nil {
			t.Fatalf("decode model step %d: %v", index, err)
		}
		checkAmount := func(name, actual string, expected int64) {
			t.Helper()
			if actual != formalBondRaw(expected) {
				t.Fatalf("step=%d %s actual=%s expected=%s model=%+v", index, name, actual, formalBondRaw(expected), model)
			}
		}
		checkAmount("owner available", GetBalance(sv, owner, FeeToken).Balance, model.Available0)
		checkAmount("second available", GetBalance(sv, second, FeeToken).Balance, model.Available1)
		checkAmount("witness locked", GetBalance(sv, owner, FeeToken).WitnessLockedBalance, 20)
		checkAmount("bond pool", GetBalance(sv, WitnessRequesterBondPoolAccount, FeeToken).Balance, model.Pool)
		checkAmount("recycle fund", GetBalance(sv, rent.RecycleFundAccount, FeeToken).Balance, model.Recycle)
		claimRaw, exists, err := sv.Get(keys.KeyAssetClaimTotal(owner, FeeToken))
		if err != nil {
			t.Fatal(err)
		}
		claim := "0"
		if exists {
			claim = string(claimRaw)
		}
		checkAmount("rent nominal claim", claim, model.Claim0)
		tokenRaw, exists, err := sv.Get(keys.KeyToken(FeeToken))
		if err != nil || !exists {
			t.Fatalf("FB supply read: exists=%v err=%v", exists, err)
		}
		var token pb.Token
		if err := proto.Unmarshal(tokenRaw, &token); err != nil {
			t.Fatal(err)
		}
		checkAmount("FB supply", token.TotalSupply, model.Supply)
		if 100-model.Supply != model.Burned {
			t.Fatalf("model burn/supply mismatch: %+v", model)
		}
		canonical := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(requestID))
		if canonical == nil {
			t.Fatal("canonical request disappeared")
		}
		wantStatus := map[string]pb.RequesterBondStatus{
			"NoBond":    pb.RequesterBondStatus_REQUESTER_BOND_NONE,
			"Escrowed":  pb.RequesterBondStatus_REQUESTER_BOND_HELD,
			"Returned":  pb.RequesterBondStatus_REQUESTER_BOND_RETURNED,
			"Forfeited": pb.RequesterBondStatus_REQUESTER_BOND_FORFEITED,
			"Recovered": pb.RequesterBondStatus_REQUESTER_BOND_RECOVERED,
		}[model.Bond]
		if canonical.RequesterBondStatus != wantStatus {
			t.Fatalf("step=%d bond status=%s want=%s", index, canonical.RequesterBondStatus, wantStatus)
		}
		wantPolicy := uint32(protocol.RechargeEconomicsLaunchV1)
		if kind == "legacy-reject" {
			wantPolicy = protocol.RechargeEconomicsLegacy
		}
		if canonical.EconomicsPolicy != wantPolicy {
			t.Fatalf("step=%d economics policy=%d want=%d", index, canonical.EconomicsPolicy, wantPolicy)
		}
		if model.Phase == "Finalized" && canonical.Status != pb.RechargeRequestStatus_RECHARGE_FINALIZED ||
			model.Phase == "Rejected" && canonical.Status != pb.RechargeRequestStatus_RECHARGE_REJECTED {
			t.Fatalf("step=%d terminal status=%s model=%s", index, canonical.Status, model.Phase)
		}
		return model
	}

	check(0)
	for step := 1; step < len(trace.States); step++ {
		before, err := formalRequesterBondFromITF(trace.States[step-1])
		if err != nil {
			t.Fatal(err)
		}
		after, err := formalRequesterBondFromITF(trace.States[step])
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case before.Phase == "Absent" && after.Phase == "Pending":
			if !after.Legacy {
				if err := collectRequesterBond(sv, req); err != nil {
					t.Fatalf("collect requester bond: %v", err)
				}
				if kind == "arbitration-pass" || kind == "arbitration-fail" {
					                                                           
					                                                       
					                                                
					if err := persistWitnessFinancialRequest(sv, staleFinancial); err != nil {
						t.Fatalf("stale arbitration financial request: %v", err)
					}
				}
			}
		case before.Bond == "Escrowed" && after.Bond == "Recovered":
			claimKey := keys.KeyAssetClaim(owner, FeeToken, rentClaimRequesterBond, requestID)
			if err := (vmRentCleaner{sv: sv}).sweepRequesterBondsFromClaims(owner, map[string][]byte{claimKey: []byte(formalBondRaw(10))}); err != nil {
				t.Fatalf("recover requester bond: %v", err)
			}
		case before.Phase == "Pending" && after.Phase == "Finalized":
			canonical := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(requestID))
			canonical.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD
			formalStoreProto(t, sv, keys.KeyRechargeRequest(requestID), canonical)
			if err := applyRechargeFinalized(sv, canonical, 50_000, 1); err != nil {
				t.Fatalf("finalize recharge: %v", err)
			}
		case before.Phase == "Pending" && after.Phase == "Rejected":
			stale := proto.Clone(req).(*pb.RechargeRequest)
			stale.Status = pb.RechargeRequestStatus_RECHARGE_REJECTED
			stale.FinalizeHeight = 50_000
			if kind == "indexed-reject" || kind == "arbitration-fail" {
				if err := setRechargeRequestWithIndexes(sv, stale, 1); err != nil {
					t.Fatalf("indexed rejected recharge: %v", err)
				}
			} else if err := applyRechargeRejected(sv, stale, 1); err != nil {
				t.Fatalf("reject recharge: %v", err)
			}
		case before.Phase == after.Phase && before.Settled == after.Settled:
			canonical := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(requestID))
			if canonical.Status == pb.RechargeRequestStatus_RECHARGE_FINALIZED {
				if err := applyRechargeFinalized(sv, canonical, 50_001, 1); err != nil {
					t.Fatalf("replayed finalization: %v", err)
				}
			} else if err := applyRechargeRejected(sv, req, 1); err != nil {
				t.Fatalf("replayed rejection: %v", err)
			}
		default:
			t.Fatalf("unsupported model step before=%+v after=%+v", before, after)
		}
		check(step)
	}
}
