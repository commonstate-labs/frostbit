package vm

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg"
	"google.golang.org/protobuf/proto"
)

                                                                           
                                                                          
            
                                                          
                                                                
                                                                           

                                                           

func newMemoryStateView(base map[string][]byte) StateView {
	readFn := func(key string) ([]byte, error) {
		v, ok := base[key]
		if !ok {
			return nil, nil
		}
		out := make([]byte, len(v))
		copy(out, v)
		return out, nil
	}
	scanFn := func(prefix string) (map[string][]byte, error) {
		out := make(map[string][]byte)
		for k, v := range base {
			if strings.HasPrefix(k, prefix) {
				c := make([]byte, len(v))
				copy(c, v)
				out[k] = c
			}
		}
		return out, nil
	}
	return NewStateView(readFn, scanFn)
}

                                                      

type withdrawFlowFixture struct {
	sv           StateView
	privKey      *btcec.PrivateKey
	vaultID      uint32
	asset        string
	userAddr     string
	destination  string
	scriptPubKey []byte
}

func buildWithdrawFlowFixture(t *testing.T) *withdrawFlowFixture {
	t.Helper()

	privKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatalf("new private key: %v", err)
	}

	vaultID := uint32(1)
	userAddr := "user_withdraw_test"
	asset := testBridgeAsset(t, "btc", "")

	                              
	scriptPubKey, _, _, err := utils.BuildDualCustodyTaproot(privKey.PubKey().SerializeCompressed(), btcDualTestGuardianPubkey())
	if err != nil {
		t.Fatal(err)
	}
	destination := btcChangeAddressForTest(t, scriptPubKey)

	base := map[string][]byte{}
	sv := newMemoryStateView(base)
	vaultConfigData, _ := proto.Marshal(&pb.FrostVaultConfig{
		Chain:              "btc",
		SignAlgo:           pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		VaultCount:         2,
		BtcNetwork:         "mainnet",
		BtcFeeSatsPerVbyte: btcTestFeeRateSatsPerVByte, GuardianPubkey: btcDualTestGuardianPubkey(),
	})
	sv.Set(keys.KeyFrostVaultConfig("btc", 0), vaultConfigData)

	                      
	vaultState := &pb.FrostVaultState{
		VaultId:          vaultID,
		Chain:            "btc",
		KeyEpoch:         1,
		GroupPubkey:      privKey.PubKey().SerializeCompressed(),
		SignAlgo:         pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Status:           "ACTIVE",
		CommitteeMembers: []string{"coordinator"},
	}
	vaultStateData, _ := proto.Marshal(vaultState)
	sv.Set(keys.KeyFrostVaultState("btc", vaultID), vaultStateData)

	                            
	sv.Set(keys.KeyFrostVaultAvailableBalance("btc", asset.ID, vaultID), []byte("50000"))

	                                                             
	tokenData, _ := proto.Marshal(&pb.Token{Address: asset.ID, TotalSupply: "999999999", Owner: "system", Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: asset.Chain})
	sv.Set(keys.KeyToken(asset.ID), tokenData)
	sv.Set(keys.KeyNativeTokenIndex(asset.Chain, asset.Contract), []byte(asset.ID))
	userBal := &pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: "999999999"}}
	userBalData, _ := proto.Marshal(userBal)
	sv.Set(keys.KeyBalance(userAddr, asset.ID), userBalData)

	                                
	utxo1TxID := strings.Repeat("aa", 32)
	utxo2TxID := strings.Repeat("bb", 32)
	for i, txid := range []string{utxo1TxID, utxo2TxID} {
		utxo := &pb.FrostUtxo{
			Txid:         txid,
			Vout:         0,
			Amount:       "20000",
			ScriptPubkey: append([]byte(nil), scriptPubKey...),
			VaultId:      vaultID,
		}
		utxoData, _ := proto.Marshal(utxo)
		sv.Set(keys.KeyFrostBtcUtxo(vaultID, txid, 0), utxoData)
		              
		seq := uint64(i + 1)
		sv.Set(keys.KeyFrostBtcUtxoFIFOIndex(vaultID, seq), utxoData)
		if err := setBTCSpendableUTXOIndex(sv, vaultID, seq, utxo, utxoData); err != nil {
			t.Fatalf("index BTC UTXO: %v", err)
		}
	}
	sv.Set(keys.KeyFrostBtcUtxoFIFOSeq(vaultID), []byte("2"))
	sv.Set(keys.KeyFrostBtcUtxoFIFOHead(vaultID), []byte("1"))

	return &withdrawFlowFixture{
		sv:           sv,
		privKey:      privKey,
		vaultID:      vaultID,
		asset:        asset.ID,
		userAddr:     userAddr,
		destination:  destination,
		scriptPubKey: scriptPubKey,
	}
}

                                                                           
                                        
                                                                           

func TestWithdrawRequestDryRun_Standalone(t *testing.T) {
	base := map[string][]byte{}
	asset := testBridgeAsset(t, "btc", "")

	                               
	tokenData, _ := proto.Marshal(&pb.Token{Address: asset.ID, TotalSupply: "999999999", Owner: "system", Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: asset.Chain})
	base[keys.KeyToken(asset.ID)] = tokenData
	base[keys.KeyNativeTokenIndex(asset.Chain, asset.Contract)] = []byte(asset.ID)
	vaultConfigData, _ := proto.Marshal(&pb.FrostVaultConfig{
		Chain:              "btc",
		SignAlgo:           pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		VaultCount:         1,
		BtcNetwork:         "mainnet",
		BtcFeeSatsPerVbyte: btcTestFeeRateSatsPerVByte, GuardianPubkey: btcDualTestGuardianPubkey(),
	})
	base[keys.KeyFrostVaultConfig("btc", 0)] = vaultConfigData
	bal := &pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: "999999999"}}
	balData, _ := proto.Marshal(bal)
	base[keys.KeyBalance("alice", asset.ID)] = balData

	sv := newMemoryStateView(base)

	handler := &FrostWithdrawRequestTxHandler{}

	tx := &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawRequestTx{
			FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{
				Base: &pb.BaseMessage{
					TxId:           "tx_withdraw_req_001",
					FromAddress:    "alice",
					Status:         pb.Status_PENDING,
					ExecutedHeight: 10,
				},
				Chain:  "btc",
				Asset:  asset.ID,
				To:     btcAdmissionAddressForTest(t, &chaincfg.MainNetParams, 4),
				Amount: fmt.Sprintf("%d", btc.MinimumWithdrawAmountSats),
			},
		},
	}

	ops, receipt, err := handler.DryRun(tx, sv)
	if err != nil {
		t.Fatalf("DryRun failed: %v", err)
	}
	if receipt.Status != "SUCCEED" {
		t.Fatalf("unexpected status: %s, error: %s", receipt.Status, receipt.Error)
	}
	if len(ops) != 8 {                                                                  
		t.Fatalf("expected 8 write ops, got %d", len(ops))
	}

	                         
	seqData, exists, _ := sv.Get(keys.KeyFrostWithdrawFIFOSeq("btc", asset.ID))
	if !exists || string(seqData) != "1" {
		t.Fatalf("expected seq=1, got %s", string(seqData))
	}

	                                   
	withdrawID := generateWithdrawID("btc", asset.ID, 1, 10)
	withdrawData, exists, _ := sv.Get(keys.KeyFrostWithdraw(withdrawID))
	if !exists {
		t.Fatal("withdraw not found after DryRun")
	}
	withdraw, _ := unmarshalWithdrawRequest(withdrawData)
	if withdraw.Status != WithdrawStatusQueued {
		t.Fatalf("expected QUEUED, got %s", withdraw.Status)
	}
	for name, key := range map[string]string{
		"global":      keys.KeyFrostWithdrawQueryGlobalState(false, 10, 1, withdrawID),
		"chain":       keys.KeyFrostWithdrawQueryChainState("btc", false, 10, 1, withdrawID),
		"chain_vault": keys.KeyFrostWithdrawQueryChainVaultState("btc", 0, false, 10, 1, withdrawID),
	} {
		if value, ok, _ := sv.Get(key); !ok || string(value) != withdrawID {
			t.Fatalf("%s query index missing: exists=%v value=%q", name, ok, string(value))
		}
	}

	                                                                        
	ops2, receipt2, err2 := handler.DryRun(tx, sv)
	if err2 != nil {
		t.Fatalf("idempotent DryRun failed: %v", err2)
	}
	if receipt2.Status != "SUCCEED" {
		t.Fatalf("idempotent: unexpected status: %s", receipt2.Status)
	}
	if len(ops2) != 0 {
		t.Fatalf("idempotent: expected 0 ops, got %d", len(ops2))
	}

	t.Logf("✅ WithdrawRequest DryRun 通过：withdraw_id=%s, seq=1, status=QUEUED", withdrawID)
}

                                                                           
                                                    
                                                                           

func TestWithdrawFlowEndToEnd(t *testing.T) {
	f := buildWithdrawFlowFixture(t)

	                                     
	reqHandler := &FrostWithdrawRequestTxHandler{}
	reqTx := &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawRequestTx{
			FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{
				Base: &pb.BaseMessage{
					TxId:           "tx_e2e_withdraw_req",
					FromAddress:    f.userAddr,
					Status:         pb.Status_PENDING,
					ExecutedHeight: 100,
				},
				Chain:  "btc",
				Asset:  f.asset,
				To:     f.destination,
				Amount: "20000",
			},
		},
	}

	_, receipt, err := reqHandler.DryRun(reqTx, f.sv)
	if err != nil {
		t.Fatalf("WithdrawRequest DryRun failed: %v", err)
	}
	if receipt.Status != "SUCCEED" {
		t.Fatalf("WithdrawRequest failed: %s", receipt.Error)
	}

	                     
	withdrawID := generateWithdrawID("btc", f.asset, 1, 100)
	t.Logf("第一步完成: withdraw_id=%s", withdrawID)
	globalQueryKey := keys.KeyFrostWithdrawQueryGlobalState(false, 100, 1, withdrawID)
	chainQueryKey := keys.KeyFrostWithdrawQueryChainState("btc", false, 100, 1, withdrawID)
	pendingVaultQueryKey := keys.KeyFrostWithdrawQueryChainVaultState("btc", 0, false, 100, 1, withdrawID)
	for _, key := range []string{globalQueryKey, chainQueryKey, pendingVaultQueryKey} {
		if value, exists, _ := f.sv.Get(key); !exists || string(value) != withdrawID {
			t.Fatalf("request query index missing: key=%q exists=%v value=%q", key, exists, string(value))
		}
	}

	                                              
	f.sv.Set(keys.KeyFrostWithdrawFIFOHead("btc", f.asset), []byte("1"))

	                                                     
	utxo1TxID := strings.Repeat("aa", 32)
	utxo2TxID := strings.Repeat("bb", 32)
	changeAddress := btcChangeAddressForTest(t, f.scriptPubKey)

	template := &btc.BTCTemplate{
		Version:  2,
		LockTime: 0,
		Inputs: []btc.TxInput{
			{TxID: utxo1TxID, Vout: 0, Amount: 20000, Sequence: btc.DefaultSequence},
			{TxID: utxo2TxID, Vout: 0, Amount: 20000, Sequence: btc.DefaultSequence},
		},
		Outputs:     exactSingleBTCWithdrawOutputsForTest(t, 40000, 20000, 2, f.destination, changeAddress),
		VaultID:     f.vaultID,
		KeyEpoch:    1,
		WithdrawIDs: []string{withdrawID},
	}

	btcDualDecorateTemplateKey(t, template, f.privKey.PubKey().SerializeCompressed())
	templateData, err := template.ToJSON()
	if err != nil {
		t.Fatalf("template ToJSON: %v", err)
	}

	                           
	scriptPubkeys := [][]byte{
		append([]byte(nil), f.scriptPubKey...),
		append([]byte(nil), f.scriptPubKey...),
	}
	sighashes, err := template.ComputeTaprootSighash(scriptPubkeys, btc.SighashDefault)
	if err != nil {
		t.Fatalf("compute sighash: %v", err)
	}

	_, guardianPrivate := btcDualTestKeys()
	guardianSigs := make([][]byte, len(sighashes))
	inputSigs := make([][]byte, len(sighashes))
	for i, msg := range sighashes {
		sig, err := schnorr.Sign(f.privKey, msg)
		if err != nil {
			t.Fatalf("sign input[%d]: %v", i, err)
		}
		inputSigs[i] = sig.Serialize()
		guardianSig, err := schnorr.Sign(guardianPrivate, msg)
		if err != nil {
			t.Fatal(err)
		}
		guardianSigs[i] = guardianSig.Serialize()
	}

	                                                                         
	for i, sig := range inputSigs {
		valid, err := verifySignature(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, f.privKey.PubKey().SerializeCompressed(), sighashes[i], sig)
		if err != nil {
			t.Fatalf("预验签 input[%d] 出错: %v", i, err)
		}
		if !valid {
			t.Fatalf("预验签 input[%d] 失败: 签名无效", i)
		}
	}
	t.Logf("预验签通过: %d 个 input 签名均有效", len(inputSigs))

	jobID := claimBTCWithdrawForTest(t, f.sv, f.asset, template)
	claimedVaultQueryKey := keys.KeyFrostWithdrawQueryChainVaultState("btc", f.vaultID, false, 100, 1, withdrawID)
	if value, exists, _ := f.sv.Get(pendingVaultQueryKey); exists || len(value) != 0 {
		t.Fatalf("claim left stale vault=0 query index: exists=%v value=%q", exists, string(value))
	}
	for _, key := range []string{globalQueryKey, chainQueryKey, claimedVaultQueryKey} {
		if value, exists, _ := f.sv.Get(key); !exists || string(value) != withdrawID {
			t.Fatalf("claimed query index missing: key=%q exists=%v value=%q", key, exists, string(value))
		}
	}

	                               
	signedHandler := &FrostWithdrawSignedTxHandler{}
	signedTx := &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawSignedTx{
			FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
				Base: &pb.BaseMessage{
					TxId:           "tx_e2e_withdraw_signed",
					ExecutedHeight: 101,
					FromAddress:    "signer_node",
				},
				JobId:              jobID,
				SignedPackageBytes: bytes.Join(inputSigs, nil),
				WithdrawIds:        []string{withdrawID},
				VaultId:            f.vaultID,
				KeyEpoch:           1,
				TemplateHash:       template.TemplateHash(),
				Chain:              "btc",
				Asset:              f.asset,
				TemplateData:       templateData,
				InputSigs:          inputSigs,
				GuardianSigs:       guardianSigs,
				ScriptPubkeys:      scriptPubkeys,
			},
		},
	}

	ops, receipt, err := signedHandler.DryRun(signedTx, f.sv)
	if err != nil {
		t.Fatalf("WithdrawSigned DryRun failed: %v\nreceipt: %+v", err, receipt)
	}
	if receipt.Status != "SUCCEED" {
		t.Fatalf("WithdrawSigned failed: %s", receipt.Error)
	}
	t.Logf("第二步完成: %d write ops, job_id=%s", len(ops), jobID)
	for _, key := range []string{globalQueryKey, chainQueryKey, claimedVaultQueryKey} {
		if _, exists, _ := f.sv.Get(key); exists {
			t.Fatalf("signed lifecycle retained active index: %q", key)
		}
	}
	for _, key := range []string{
		keys.KeyFrostWithdrawQueryGlobalState(true, 100, 1, withdrawID),
		keys.KeyFrostWithdrawQueryChainState("btc", true, 100, 1, withdrawID),
		keys.KeyFrostWithdrawQueryChainVaultState("btc", f.vaultID, true, 100, 1, withdrawID),
	} {
		if value, exists, _ := f.sv.Get(key); !exists || string(value) != withdrawID {
			t.Fatalf("signed lifecycle missing terminal index: %q", key)
		}
	}

	                               

	                                  
	if hotWithdraw, exists, _ := f.sv.Get(keys.KeyFrostWithdraw(withdrawID)); exists || len(hotWithdraw) > 0 {
		t.Fatal("hot withdraw should be pruned after signed")
	}
	archivedWithdrawData, archivedWithdraw, _ := f.sv.Get(keys.KeyArchiveFrostWithdraw(withdrawID))
	if !archivedWithdraw {
		t.Fatal("archived withdraw not written")
	}
	archivedWithdrawReq, err := unmarshalWithdrawRequest(archivedWithdrawData)
	if err != nil {
		t.Fatalf("unmarshal archived withdraw failed: %v", err)
	}
	if archivedWithdrawReq.Status != WithdrawStatusSigned {
		t.Fatalf("expected SIGNED, got %s", archivedWithdrawReq.Status)
	}
	if archivedWithdrawReq.JobID != jobID {
		t.Fatalf("expected job_id=%s, got %s", jobID, archivedWithdrawReq.JobID)
	}
	if archivedWithdrawReq.To == "" || archivedWithdrawReq.Amount == "" || archivedWithdrawReq.RequestHeight == 0 {
		t.Fatalf("archived withdraw lost full details: %+v", archivedWithdrawReq)
	}
	doneIndexData, doneIndexExists, _ := f.sv.Get(keys.KeyArchiveFrostWithdrawDone("btc", f.asset, archivedWithdrawReq.Seq, withdrawID))
	if !doneIndexExists || string(doneIndexData) != withdrawID {
		t.Fatalf("archive done index mismatch: exists=%v value=%q", doneIndexExists, string(doneIndexData))
	}

	                                                                               
	for _, txid := range []string{utxo1TxID, utxo2TxID} {
		lockKey := keys.KeyFrostBtcLockedUtxo(f.vaultID, txid, 0)
		if _, locked, _ := f.sv.Get(lockKey); locked {
			t.Fatalf("UTXO %s:0 retained a terminal lock", txid[:8])
		}
		if _, exists, _ := f.sv.Get(keys.KeyFrostBtcUtxo(f.vaultID, txid, 0)); exists {
			t.Fatalf("UTXO %s:0 retained spent primary state", txid[:8])
		}
	}

	                         
	headData, _, _ := f.sv.Get(keys.KeyFrostWithdrawFIFOHead("btc", f.asset))
	if string(headData) != "2" {
		t.Fatalf("expected withdraw FIFO head=2, got %s", string(headData))
	}

	                             
	if hotPkgData, hotPkgExists, _ := f.sv.Get(keys.KeyFrostSignedPackage(jobID, 0)); hotPkgExists || len(hotPkgData) > 0 {
		t.Fatal("hot signed package should be pruned")
	}
	archivedPkgData, archivedPkg, _ := f.sv.Get(keys.KeyArchiveFrostSignedPackage(jobID, 0))
	if !archivedPkg {
		t.Fatal("archived signed package not written")
	}
	var archivedPkgMsg pb.FrostSignedPackage
	if err := proto.Unmarshal(archivedPkgData, &archivedPkgMsg); err != nil {
		t.Fatalf("unmarshal archived signed package failed: %v", err)
	}
	if !bytes.Equal(archivedPkgMsg.SignedPackageBytes, bytes.Join(inputSigs, nil)) {
		t.Fatal("archived signed package bytes mismatch")
	}

	t.Logf("✅ 提现流程 E2E 测试通过：QUEUED → SIGNED, spent UTXO reclaimed, FIFO advanced")
}
