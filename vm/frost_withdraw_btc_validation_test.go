package vm

import (
	"bytes"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

const btcTestFeeRateSatsPerVByte = uint64(25)

func exactSingleBTCWithdrawOutputsForTest(t *testing.T, totalInput, grossAmount uint64, inputCount int, recipient, changeAddress string) []btc.TxOutput {
	t.Helper()
	recipientScript, err := btc.AddressScript(recipient)
	if err != nil {
		t.Fatalf("build BTC recipient script: %v", err)
	}
	var changeScript []byte
	if totalInput > grossAmount {
		changeScript, err = btc.AddressScript(changeAddress)
		if err != nil {
			t.Fatalf("build BTC change script: %v", err)
		}
	}
	netAmounts, _, change, err := btc.ExpectedUserPaidWithdrawAmounts(
		totalInput,
		[]uint64{grossAmount},
		inputCount,
		[][]byte{recipientScript},
		changeScript,
		btcTestFeeRateSatsPerVByte,
	)
	if err != nil {
		t.Fatalf("compute exact BTC outputs: %v", err)
	}
	outputs := []btc.TxOutput{{Address: recipient, Amount: netAmounts[0]}}
	if change > 0 {
		outputs = append(outputs, btc.TxOutput{Address: changeAddress, Amount: change})
	}
	return outputs
}

type btcWithdrawValidationFixture struct {
	sv           StateView
	template     *btc.BTCTemplate
	committee    []string
	scriptPubKey []byte
	txID         string
	withdrawID   string
	asset        string
	vaultID      uint32
	keyEpoch     uint64
}

func newBTCWithdrawValidationFixture(t *testing.T) *btcWithdrawValidationFixture {
	t.Helper()
	const (
		withdrawID     = "wd_btc_validation"
		to             = testBTCTaprootAddress
		asset          = "BTC"
		inputAmount    = uint64(12200)
		withdrawAmount = uint64(10000)
	)
	vaultID, keyEpoch := uint32(3), uint64(7)
	txID := strings.Repeat("11", 32)
	scriptPubKey := btcDualTestScript(t)
	changeAddress := btcChangeAddressForTest(t, scriptPubKey)
	_, err := btc.AddressScript(changeAddress)
	if err != nil {
		t.Fatalf("build BTC change script: %v", err)
	}
	committee := []string{"miner-a", "miner-b", "miner-c"}
	recipientScript, err := btc.AddressScript(to)
	if err != nil {
		t.Fatalf("build BTC recipient script: %v", err)
	}
	netAmounts, fee, change, err := btc.ExpectedUserPaidWithdrawAmounts(
		inputAmount,
		[]uint64{withdrawAmount},
		1,
		[][]byte{recipientScript},
		scriptPubKey,
		btcTestFeeRateSatsPerVByte,
	)
	if err != nil {
		t.Fatalf("compute BTC user-paid fee: %v", err)
	}

	template, err := btc.NewBTCTemplate(chain.WithdrawTemplateParams{
		Chain:          chain.ChainBTC,
		Asset:          asset,
		VaultID:        vaultID,
		KeyEpoch:       keyEpoch,
		WithdrawIDs:    []string{withdrawID},
		Outputs:        []chain.WithdrawOutput{{WithdrawID: withdrawID, To: to, Amount: strconv.FormatUint(netAmounts[0], 10)}},
		Inputs:         []chain.UTXO{{TxID: txID, Vout: 0, Amount: inputAmount, ScriptPubKey: scriptPubKey}},
		GuardianPubkey: btcDualTestGuardianPubkey(), BTCInputPubkeys: [][]byte{btcDualTestOperatorPubkey()},
		Fee:           fee,
		ChangeAmount:  change,
		ChangeAddress: changeAddress,
	})
	if err != nil {
		t.Fatalf("build BTC template: %v", err)
	}

	utxo := &pb.FrostUtxo{Txid: txID, Vout: 0, Amount: "12200", VaultId: vaultID, ScriptPubkey: scriptPubKey, FinalizeHeight: 10}
	utxoData := mustBTCValidationMarshal(t, utxo)
	base := map[string][]byte{
		keys.KeyFrostVaultConfig(chain.ChainBTC, 0): mustBTCValidationMarshal(t, &pb.FrostVaultConfig{
			GuardianPubkey: btcDualTestGuardianPubkey(), Chain: chain.ChainBTC, VaultCount: 1, BtcNetwork: "mainnet", BtcFeeSatsPerVbyte: btcTestFeeRateSatsPerVByte,
		}),
		keys.KeyFrostWithdraw(withdrawID): mustBTCValidationMarshal(t, &pb.FrostWithdrawState{
			WithdrawId: withdrawID, Seq: 1, Chain: chain.ChainBTC, Asset: asset, To: to,
			Amount: "10000", Status: string(WithdrawStatusQueued), VaultId: vaultID,
		}),
		keys.KeyFrostWithdrawFIFOHead(chain.ChainBTC, asset):     []byte("1"),
		keys.KeyFrostWithdrawFIFOSeq(chain.ChainBTC, asset):      []byte("1"),
		keys.KeyFrostWithdrawFIFOIndex(chain.ChainBTC, asset, 1): []byte(withdrawID),
		keys.KeyFrostVaultState(chain.ChainBTC, vaultID): mustBTCValidationMarshal(t, &pb.FrostVaultState{
			Chain: chain.ChainBTC, VaultId: vaultID, KeyEpoch: keyEpoch, Status: "ACTIVE",
			GroupPubkey: btcDualTestOperatorPubkey(), SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
			CommitteeMembers: committee,
		}),
		keys.KeyFrostVaultAvailableBalance(chain.ChainBTC, asset, vaultID): []byte("12200"),
		keys.KeyFrostBtcUtxo(vaultID, txID, 0):                             utxoData,
		keys.KeyFrostBtcUtxoFIFOHead(vaultID):                              []byte("1"),
		keys.KeyFrostBtcUtxoFIFOSeq(vaultID):                               []byte("1"),
		keys.KeyFrostBtcUtxoFIFOIndex(vaultID, 1):                          utxoData,
		keys.KeyFrostBtcSpendableUtxoIndex(vaultID, 1):                     utxoData,
		keys.KeyFrostBtcUtxoSeqRef(vaultID, txID, 0):                       []byte("1"),
	}
	return &btcWithdrawValidationFixture{
		sv: newMemoryStateView(base), template: template, committee: committee, scriptPubKey: scriptPubKey,
		txID: txID, withdrawID: withdrawID, asset: asset, vaultID: vaultID, keyEpoch: keyEpoch,
	}
}

func mustBTCValidationMarshal(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return data
}

func cloneBTCValidationTemplate(t *testing.T, template *btc.BTCTemplate) *btc.BTCTemplate {
	t.Helper()
	data, err := template.ToJSON()
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	cloned, err := btc.FromJSON(data)
	if err != nil {
		t.Fatalf("clone template: %v", err)
	}
	return cloned
}

func (f *btcWithdrawValidationFixture) claimTx(t *testing.T, template *btc.BTCTemplate) *pb.AnyTx {
	t.Helper()
	templateData, err := template.ToJSON()
	if err != nil {
		t.Fatalf("marshal claim template: %v", err)
	}
	templateHash := template.TemplateHash()
	jobID := chain.ComputeWithdrawJobID(chain.ChainBTC, f.asset, f.vaultID, 1, templateHash, f.keyEpoch)
	coordinator, err := chain.WithdrawCoordinator(jobID, f.keyEpoch, f.committee)
	if err != nil {
		t.Fatalf("compute coordinator: %v", err)
	}
	return &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: &pb.FrostWithdrawClaimTx{
		Base:  &pb.BaseMessage{TxId: "claim_" + jobID, FromAddress: coordinator, ExecutedHeight: 100},
		JobId: jobID, WithdrawIds: []string{f.withdrawID}, VaultId: f.vaultID, KeyEpoch: f.keyEpoch,
		TemplateHash: templateHash, Chain: chain.ChainBTC, Asset: f.asset, TemplateData: templateData,
	}}}
}

func assertBTCValidationClaimFailedWithoutWrites(t *testing.T, f *btcWithdrawValidationFixture, tx *pb.AnyTx, wantError string) {
	t.Helper()
	if diff := f.sv.Diff(); len(diff) != 0 {
		t.Fatalf("fixture unexpectedly starts dirty: %+v", diff)
	}
	_, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(tx, f.sv)
	if err == nil || receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("claim should fail: err=%v receipt=%+v", err, receipt)
	}
	if wantError != "" && !strings.Contains(receipt.Error, wantError) {
		t.Fatalf("claim error %q does not contain %q", receipt.Error, wantError)
	}
	if diff := f.sv.Diff(); len(diff) != 0 {
		t.Fatalf("failed claim left partial writes: %+v", diff)
	}
	claim := tx.GetFrostWithdrawClaimTx()
	if _, exists, _ := f.sv.Get(keys.KeyFrostWithdrawJob(claim.JobId)); exists {
		t.Fatal("failed claim created job state")
	}
	data, exists, _ := f.sv.Get(keys.KeyFrostWithdraw(f.withdrawID))
	if !exists {
		t.Fatal("failed claim removed withdraw")
	}
	var withdraw pb.FrostWithdrawState
	if err := proto.Unmarshal(data, &withdraw); err != nil || withdraw.Status != string(WithdrawStatusQueued) || withdraw.JobId != "" {
		t.Fatalf("failed claim changed withdraw: err=%v state=%+v", err, &withdraw)
	}
}

func TestFrostWithdrawClaimBTCValidTemplate(t *testing.T) {
	f := newBTCWithdrawValidationFixture(t)
	tx := f.claimTx(t, f.template)
	claim := tx.GetFrostWithdrawClaimTx()
	ops, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(tx, f.sv)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" || len(ops) == 0 {
		t.Fatalf("valid BTC claim failed: err=%v receipt=%+v ops=%d", err, receipt, len(ops))
	}
	jobData, exists, _ := f.sv.Get(keys.KeyFrostWithdrawJob(claim.JobId))
	if !exists {
		t.Fatal("valid BTC claim did not create job")
	}
	var job pb.FrostWithdrawJobState
	if err := proto.Unmarshal(jobData, &job); err != nil || job.Status != frostWithdrawJobStatusClaimed {
		t.Fatalf("unexpected job state: err=%v job=%+v", err, &job)
	}
	withdrawData, _, _ := f.sv.Get(keys.KeyFrostWithdraw(f.withdrawID))
	var withdraw pb.FrostWithdrawState
	if err := proto.Unmarshal(withdrawData, &withdraw); err != nil || withdraw.Status != string(WithdrawStatusSigning) || withdraw.JobId != claim.JobId {
		t.Fatalf("withdraw was not claimed: err=%v state=%+v", err, &withdraw)
	}
	lock, locked, _ := f.sv.Get(keys.KeyFrostBtcLockedUtxo(f.vaultID, f.txID, 0))
	if !locked || string(lock) != claim.JobId {
		t.Fatalf("UTXO was not reserved by job: locked=%v owner=%q", locked, string(lock))
	}
	if _, exists, _ := f.sv.Get(keys.KeyFrostBtcSpendableUtxoIndex(f.vaultID, 1)); exists {
		t.Fatal("reserved UTXO remains spendable")
	}
	if balance, _, _ := f.sv.Get(keys.KeyFrostVaultAvailableBalance(chain.ChainBTC, f.asset, f.vaultID)); string(balance) != "0" {
		t.Fatalf("available balance was not reserved: %q", string(balance))
	}
}

func TestValidateBTCWithdrawTemplateSplitsBatchFeeInFIFOOrder(t *testing.T) {
	const (
		asset    = "BTC"
		vaultID  = uint32(5)
		keyEpoch = uint64(9)
	)
	withdraws := []*FrostWithdrawRequest{
		{WithdrawID: "wd_batch_1", Seq: 1, Chain: chain.ChainBTC, Asset: asset, To: testBTCTaprootAddress, Amount: "10000", Status: WithdrawStatusQueued},
		{WithdrawID: "wd_batch_2", Seq: 2, Chain: chain.ChainBTC, Asset: asset, To: testBTCTaprootAddress, Amount: "20000", Status: WithdrawStatusQueued},
	}
	input := btc.TxInput{TxID: strings.Repeat("55", 32), Vout: 0, Amount: 30000, Sequence: btc.DefaultSequence}
	recipientScript, err := btc.AddressScript(testBTCTaprootAddress)
	if err != nil {
		t.Fatal(err)
	}
	netAmounts, fee, change, err := btc.ExpectedUserPaidWithdrawAmounts(
		input.Amount,
		[]uint64{10000, 20000},
		1,
		[][]byte{recipientScript, recipientScript},
		nil,
		btcTestFeeRateSatsPerVByte,
	)
	if err != nil {
		t.Fatal(err)
	}
	template, err := btc.NewBTCTemplate(chain.WithdrawTemplateParams{
		Chain: chain.ChainBTC, Asset: asset, VaultID: vaultID, KeyEpoch: keyEpoch,
		WithdrawIDs: []string{withdraws[0].WithdrawID, withdraws[1].WithdrawID},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: withdraws[0].WithdrawID, To: withdraws[0].To, Amount: strconv.FormatUint(netAmounts[0], 10)},
			{WithdrawID: withdraws[1].WithdrawID, To: withdraws[1].To, Amount: strconv.FormatUint(netAmounts[1], 10)},
		},
		Inputs:         []chain.UTXO{{TxID: input.TxID, Vout: input.Vout, Amount: input.Amount, ScriptPubKey: btcDualTestScript(t)}},
		GuardianPubkey: btcDualTestGuardianPubkey(), BTCInputPubkeys: [][]byte{btcDualTestOperatorPubkey()},
		Fee: fee, ChangeAmount: change,
	})
	if err != nil {
		t.Fatal(err)
	}
	templateData, err := template.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	utxo := &pb.FrostUtxo{Txid: input.TxID, Vout: input.Vout, Amount: strconv.FormatUint(input.Amount, 10), VaultId: vaultID, ScriptPubkey: btcDualTestScript(t)}
	sv := newMemoryStateView(map[string][]byte{
		keys.KeyFrostVaultConfig(chain.ChainBTC, 0): mustBTCValidationMarshal(t, &pb.FrostVaultConfig{
			GuardianPubkey: btcDualTestGuardianPubkey(), Chain: chain.ChainBTC, VaultCount: 1, BtcNetwork: "mainnet", BtcFeeSatsPerVbyte: btcTestFeeRateSatsPerVByte,
		}),
		keys.KeyFrostVaultState(chain.ChainBTC, vaultID): mustBTCValidationMarshal(t, &pb.FrostVaultState{
			Chain: chain.ChainBTC, VaultId: vaultID, KeyEpoch: keyEpoch, Status: "ACTIVE",
			GroupPubkey: btcDualTestOperatorPubkey(), CommitteeMembers: []string{"miner-a"},
		}),
		keys.KeyFrostBtcUtxo(vaultID, input.TxID, input.Vout): mustBTCValidationMarshal(t, utxo),
	})
	jobID := chain.ComputeWithdrawJobID(chain.ChainBTC, asset, vaultID, withdraws[0].Seq, template.TemplateHash(), keyEpoch)
	validated, err := validateBTCWithdrawTemplate(sv, chain.ChainBTC, asset, jobID, vaultID, keyEpoch, templateData, template.TemplateHash(), withdraws)
	if err != nil {
		t.Fatal(err)
	}
	if validated.template.Outputs[0].Amount != netAmounts[0] || validated.template.Outputs[1].Amount != netAmounts[1] || netAmounts[0]+netAmounts[1]+fee != input.Amount {
		t.Fatalf("batch fee split mismatch: outputs=%+v fee=%d", validated.template.Outputs, fee)
	}
}

func TestFrostWithdrawClaimBTCUsesConsensusFeeConfig(t *testing.T) {
	f := newBTCWithdrawValidationFixture(t)
	updatedCfg := mustBTCValidationMarshal(t, &pb.FrostVaultConfig{
		GuardianPubkey: btcDualTestGuardianPubkey(), Chain: chain.ChainBTC, VaultCount: 1, BtcNetwork: "mainnet", BtcFeeSatsPerVbyte: btcTestFeeRateSatsPerVByte + 5,
	})
	f.sv.Set(keys.KeyFrostVaultConfig(chain.ChainBTC, 0), updatedCfg)

	_, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(f.claimTx(t, f.template), f.sv)
	if err == nil || receipt == nil || receipt.Status != "FAILED" || !strings.Contains(receipt.Error, "withdraw output mismatch") {
		t.Fatalf("claim did not enforce configured BTC fee rate: err=%v receipt=%+v", err, receipt)
	}
}

func TestFrostWithdrawClaimBTCRejectsTamperingWithoutWrites(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*btc.BTCTemplate)
		wantError string
	}{
		{name: "empty inputs", mutate: func(tpl *btc.BTCTemplate) { tpl.Inputs = nil }, wantError: "must not be empty"},
		{name: "vault", mutate: func(tpl *btc.BTCTemplate) { tpl.VaultID++ }, wantError: "vault/key epoch mismatch"},
		{name: "key epoch", mutate: func(tpl *btc.BTCTemplate) { tpl.KeyEpoch++ }, wantError: "vault/key epoch mismatch"},
		{name: "withdraw id", mutate: func(tpl *btc.BTCTemplate) { tpl.WithdrawIDs[0] = "other" }, wantError: "withdraw order mismatch"},
		{name: "destination", mutate: func(tpl *btc.BTCTemplate) { tpl.Outputs[0].Address = "bc1qattacker" }, wantError: "withdraw output mismatch"},
		{name: "withdraw amount", mutate: func(tpl *btc.BTCTemplate) { tpl.Outputs[0].Amount++ }, wantError: "withdraw output mismatch"},
		{name: "unknown UTXO", mutate: func(tpl *btc.BTCTemplate) {
			tpl.Inputs[0].TxID = strings.Repeat("33", 32)
		}, wantError: "input not found"},
		{name: "UTXO amount", mutate: func(tpl *btc.BTCTemplate) { tpl.Inputs[0].Amount++ }, wantError: "input state mismatch"},
		{name: "duplicate UTXO", mutate: func(tpl *btc.BTCTemplate) {
			tpl.Inputs = append(tpl.Inputs, tpl.Inputs[0])
		}, wantError: "duplicate BTC input"},
		{name: "unexpected change", mutate: func(tpl *btc.BTCTemplate) {
			tpl.Outputs = append(tpl.Outputs, btc.TxOutput{Address: "bc1qattacker", Amount: 1})
		}, wantError: "BTC change output count mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newBTCWithdrawValidationFixture(t)
			template := cloneBTCValidationTemplate(t, f.template)
			tt.mutate(template)
			assertBTCValidationClaimFailedWithoutWrites(t, f, f.claimTx(t, template), tt.wantError)
		})
	}
}

func TestFrostWithdrawClaimBTCRejectsWrongCoordinatorWithoutWrites(t *testing.T) {
	f := newBTCWithdrawValidationFixture(t)
	tx := f.claimTx(t, f.template)
	tx.GetFrostWithdrawClaimTx().Base.FromAddress = "attacker"
	assertBTCValidationClaimFailedWithoutWrites(t, f, tx, "not coordinator")
}

func TestFrostWithdrawClaimBTCRejectsInvalidJobAndReservationStateWithoutWrites(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*testing.T, *btcWithdrawValidationFixture, *pb.AnyTx)
		wantError string
	}{
		{name: "job id", mutate: func(_ *testing.T, _ *btcWithdrawValidationFixture, tx *pb.AnyTx) {
			tx.GetFrostWithdrawClaimTx().JobId = "wrong-job"
		}, wantError: "job_id mismatch"},
		{name: "locked by another job", mutate: func(_ *testing.T, f *btcWithdrawValidationFixture, _ *pb.AnyTx) {
			f.sv.Set(keys.KeyFrostBtcLockedUtxo(f.vaultID, f.txID, 0), []byte("other-job"))
		}, wantError: "already locked"},
		{name: "spendable index mismatch", mutate: func(t *testing.T, f *btcWithdrawValidationFixture, _ *pb.AnyTx) {
			f.sv.Set(keys.KeyFrostBtcSpendableUtxoIndex(f.vaultID, 1), mustBTCValidationMarshal(t, &pb.FrostUtxo{Txid: strings.Repeat("44", 32), Vout: 0, Amount: "12200", VaultId: f.vaultID, ScriptPubkey: f.scriptPubKey}))
		}, wantError: "spendable index mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newBTCWithdrawValidationFixture(t)
			tx := f.claimTx(t, f.template)
			tt.mutate(t, f, tx)
			before := f.sv.Diff()
			_, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(tx, f.sv)
			if err == nil || receipt == nil || !strings.Contains(receipt.Error, tt.wantError) {
				t.Fatalf("claim should fail with %q: err=%v receipt=%+v", tt.wantError, err, receipt)
			}
			if after := f.sv.Diff(); !reflect.DeepEqual(before, after) {
				t.Fatalf("failed claim changed state:\nbefore=%+v\nafter=%+v", before, after)
			}
		})
	}
}

func TestFrostWithdrawSignedBTCRejectsLocalScriptMismatchWithoutWrites(t *testing.T) {
	f := newBTCWithdrawValidationFixture(t)
	claimTx := f.claimTx(t, f.template)
	claim := claimTx.GetFrostWithdrawClaimTx()
	if _, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(claimTx, f.sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("claim setup failed: err=%v receipt=%+v", err, receipt)
	}
	before := f.sv.Diff()
	signedTx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
		Base:  &pb.BaseMessage{TxId: "signed_script_mismatch", FromAddress: "miner-a", ExecutedHeight: 101},
		JobId: claim.JobId, SignedPackageBytes: make([]byte, 64), WithdrawIds: []string{f.withdrawID},
		VaultId: f.vaultID, KeyEpoch: f.keyEpoch, TemplateHash: append([]byte(nil), claim.TemplateHash...),
		Chain: chain.ChainBTC, Asset: f.asset, TemplateData: append([]byte(nil), claim.TemplateData...),
		InputSigs: [][]byte{make([]byte, 64)}, ScriptPubkeys: [][]byte{append([]byte(nil), bytes.Repeat([]byte{0x44}, len(f.scriptPubKey))...)},
	}}}
	_, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(signedTx, f.sv)
	if err == nil || receipt == nil || receipt.Status != "FAILED" || !strings.Contains(receipt.Error, "script_pubkeys differ from local UTXO state") {
		t.Fatalf("script mismatch should fail: err=%v receipt=%+v", err, receipt)
	}
	if after := f.sv.Diff(); !reflect.DeepEqual(before, after) {
		t.Fatalf("failed Signed changed state:\nbefore=%+v\nafter=%+v", before, after)
	}
	jobData, exists, _ := f.sv.Get(keys.KeyFrostWithdrawJob(claim.JobId))
	var job pb.FrostWithdrawJobState
	if !exists || proto.Unmarshal(jobData, &job) != nil || job.Status != frostWithdrawJobStatusClaimed {
		t.Fatalf("failed Signed changed job: exists=%v state=%+v", exists, &job)
	}
	if _, exists, _ := f.sv.Get(keys.KeyArchiveFrostSignedPackage(claim.JobId, 0)); exists {
		t.Fatal("failed Signed archived a signature package")
	}
}
