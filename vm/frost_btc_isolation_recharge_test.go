package vm

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"dex/frost/chain"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

func TestBTCIsolationRechargeMinimumPerOutpoint(t *testing.T) {
	for _, amount := range []uint64{99999, 100000, 100001} {
		t.Run(strconv.FormatUint(amount, 10), func(t *testing.T) {
			sv := newMemoryStateView(map[string][]byte{})
			enableBTCIsolationForTest(t, sv)
			sv.(interface{ BindNetworkChainID(uint64) }).BindNetworkChainID(999)
			putTestVaultConfig(t, sv, "btc", 1)
			putTestVaultStateWithPubkeyHex(t, sv, "btc", 0, VaultStatusActive, btcRechargeTestPubKeyHex)
			asset := testBridgeAsset(t, "btc", "")
			putTestToken(t, sv, &pb.Token{Address: asset.ID, NativeChain: "btc", Type: pb.TokenType_TOKEN_BRIDGED, TotalSupply: "0"})
			sv.Set(keys.KeyNativeTokenIndex("btc", ""), []byte(asset.ID))
			script, err := utils.BuildTweakedDualCustodyTaprootScriptPubKey(mustDecodeHex(t, btcRechargeTestPubKeyHex), "receiver", btcDualTestGuardianPubkey())
			if err != nil {
				t.Fatal(err)
			}
			request := &pb.WitnessRequestTx{Base: &pb.BaseMessage{TxId: "min-recharge", FromAddress: "owner", ChainId: 999, ExecutedHeight: 20},
				NativeChain: "btc", NativeTxHash: strings.Repeat("a", 64), NativeVout: 0, NativeScript: script,
				Amount: strconv.FormatUint(amount, 10), ReceiverAddress: "receiver"}
			before := sv.Diff()
			_, receipt, err := (&WitnessRequestTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: request}}, sv)
			if amount < 100000 {
				if err == nil || receipt.Status != "FAILED" || !strings.Contains(receipt.Error, "0.001 BTC") {
					t.Fatalf("small recharge: %v %+v", err, receipt)
				}
				if !reflect.DeepEqual(before, sv.Diff()) {
					t.Fatal("rejected small recharge wrote state")
				}
				return
			}
			if err != nil || receipt.Status != "SUCCEED" {
				t.Fatalf("boundary recharge: %v %+v", err, receipt)
			}
			raw, found, _ := sv.Get(keys.KeyRechargeRequest("min-recharge"))
			var stored pb.RechargeRequest
			if !found || proto.Unmarshal(raw, &stored) != nil || stored.BtcProtocolVersion != 3 {
				t.Fatal("admission version missing")
			}
		})
	}
}

func TestBTCIsolationPreviouslyAdmittedSmallRechargeStillFinalizes(t *testing.T) {
	sv := newMemoryStateView(map[string][]byte{})
	request := seedBTCRechargeFinalizationTest(t, sv, "legacy-small", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	enableBTCIsolationForTest(t, sv)
	sv.(interface{ BindNetworkChainID(uint64) }).BindNetworkChainID(999)
	cfgRaw, _, _ := sv.Get(keys.KeyFrostVaultConfig("btc", 0))
	var cfg pb.FrostVaultConfig
	if err := proto.Unmarshal(cfgRaw, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.BtcNetwork = "mainnet"
	sv.Set(keys.KeyFrostVaultConfig("btc", 0), mustMarshalProto(t, &cfg))
	if err := applyRechargeFinalized(sv, request, 70, 1); err != nil {
		t.Fatal(err)
	}
	raw, found, _ := sv.Get(keys.KeyFrostBtcUtxo(0, request.NativeTxHash, request.NativeVout))
	var u pb.FrostUtxo
	if !found || proto.Unmarshal(raw, &u) != nil || u.Amount != "7000" || !u.OriginLegacy ||
		!reflect.DeepEqual(u.OriginId, chain.BTCOriginID(999, "mainnet", u.Txid, u.Vout, true)) {
		t.Fatalf("legacy admission changed: %+v", &u)
	}
}

func TestBTCIsolationFinalizedRechargeHotMarkerRetainsAdmissionVersion(t *testing.T) {
	sv := newMemoryStateView(map[string][]byte{})
	request := seedBTCRechargeFinalizationTest(t, sv, "v3-hot-policy", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	request.Amount, request.BtcProtocolVersion = "100000", 3
	sv.Set(keys.KeyRechargeRequest(request.RequestId), mustMarshalProto(t, request))
	enableBTCIsolationForTest(t, sv)
	sv.(interface{ BindNetworkChainID(uint64) }).BindNetworkChainID(999)
	cfgRaw, _, _ := sv.Get(keys.KeyFrostVaultConfig("btc", 0))
	var cfg pb.FrostVaultConfig
	if err := proto.Unmarshal(cfgRaw, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.BtcNetwork = "mainnet"
	sv.Set(keys.KeyFrostVaultConfig("btc", 0), mustMarshalProto(t, &cfg))
	if err := applyRechargeFinalized(sv, request, 70, 1); err != nil {
		t.Fatal(err)
	}
	raw, found, _ := sv.Get(keys.KeyRechargeRequest(request.RequestId))
	var hot pb.RechargeRequest
	if !found || proto.Unmarshal(raw, &hot) != nil || hot.BtcProtocolVersion != 3 {
		t.Fatalf("terminal hot policy was dropped: %+v", &hot)
	}
	uRaw, found, _ := sv.Get(keys.KeyFrostBtcUtxo(0, request.NativeTxHash, request.NativeVout))
	var utxo pb.FrostUtxo
	if !found || proto.Unmarshal(uRaw, &utxo) != nil || utxo.OriginLegacy || len(utxo.OriginId) != 32 {
		t.Fatalf("new admission was marked legacy: %+v", &utxo)
	}
}

func TestBTCIsolationOldMultiInputChangeStartsLegacyBoundary(t *testing.T) {
	f := newBTCWithdrawValidationFixture(t)
	enableBTCIsolationForTest(t, f.sv)
	                                                                            
	                                                                         
	template := cloneBTCValidationTemplate(t, f.template)
	template.Inputs = append(template.Inputs, template.Inputs[0])
	template.Inputs[1].TxID = strings.Repeat("2", 64)
	changeTxID, _ := template.TxID()
	created, amount, err := ensureBTCChangeUTXO(f.sv, f.vaultID, template, nil, 110)
	if err != nil || !created || amount != 2200 {
		t.Fatalf("change: %v %v %d", err, created, amount)
	}
	raw, found, _ := f.sv.Get(keys.KeyFrostBtcUtxo(f.vaultID, changeTxID, 1))
	var change pb.FrostUtxo
	if !found || proto.Unmarshal(raw, &change) != nil || !change.OriginLegacy ||
		!reflect.DeepEqual(change.OriginId, chain.BTCOriginID(51025, "mainnet", changeTxID, 1, true)) {
		t.Fatalf("wrong merged provenance: %+v", &change)
	}
}
