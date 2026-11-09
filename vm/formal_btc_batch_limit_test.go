package vm

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"dex/formal/itftrace"
	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
)

                                                                             
                                                                              
func formalBTCBatchLimitFixture(t *testing.T, n int) (StateView, *pb.AnyTx) {
	t.Helper()
	const gross = uint64(100000)
	vaultID, epoch := uint32(3), uint64(7)
	committee := []string{"miner-a", "miner-b", "miner-c"}
	script, err := btc.AddressScript(testBTCTaprootAddress)
	if err != nil {
		t.Fatal(err)
	}
	custodyScript := btcDualTestScript(t)
	changeAddress := btcChangeAddressForTest(t, custodyScript)
	ids := make([]string, n)
	amounts := make([]uint64, n)
	scripts := make([][]byte, n)
	base := map[string][]byte{}
	for i := range ids {
		ids[i] = fmt.Sprintf("formal-btc-%03d", i+1)
		amounts[i], scripts[i] = gross, script
		base[keys.KeyFrostWithdraw(ids[i])] = mustBTCValidationMarshal(t, &pb.FrostWithdrawState{
			WithdrawId: ids[i], Seq: uint64(i + 1), Chain: "btc", Asset: "BTC", To: testBTCTaprootAddress,
			Amount: strconv.FormatUint(gross, 10), Status: "QUEUED", VaultId: vaultID, RequestHeight: 1,
		})
		base[keys.KeyFrostWithdrawFIFOIndex("btc", "BTC", uint64(i+1))] = []byte(ids[i])
	}
	inputAmount := uint64(n)*gross + 1000
	net, fee, change, err := btc.ExpectedUserPaidWithdrawAmounts(inputAmount, amounts, 1, scripts, custodyScript, btcTestFeeRateSatsPerVByte)
	if err != nil {
		t.Fatal(err)
	}
	outputs := make([]chain.WithdrawOutput, n)
	for i := range ids {
		outputs[i] = chain.WithdrawOutput{WithdrawID: ids[i], To: testBTCTaprootAddress, Amount: strconv.FormatUint(net[i], 10)}
	}
	inputTxID := strings.Repeat("ab", 32)
	template, err := btc.NewBTCTemplate(chain.WithdrawTemplateParams{
		Chain: "btc", Asset: "BTC", VaultID: vaultID, KeyEpoch: epoch, WithdrawIDs: ids, Outputs: outputs,
		Inputs:         []chain.UTXO{{TxID: inputTxID, Vout: 0, Amount: inputAmount, ScriptPubKey: custodyScript}},
		GuardianPubkey: btcDualTestGuardianPubkey(), BTCInputPubkeys: [][]byte{btcDualTestOperatorPubkey()},
		Fee: fee, ChangeAmount: change, ChangeAddress: changeAddress,
	})
	if err != nil {
		t.Fatal(err)
	}
	templateData, err := template.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	jobID := chain.ComputeWithdrawJobID("btc", "BTC", vaultID, 1, template.TemplateHash(), epoch)
	coordinator, err := chain.WithdrawCoordinator(jobID, epoch, committee)
	if err != nil {
		t.Fatal(err)
	}
	base[keys.KeyFrostVaultConfig("btc", 0)] = mustBTCValidationMarshal(t, &pb.FrostVaultConfig{Chain: "btc", VaultCount: 1, BtcNetwork: "mainnet", BtcFeeSatsPerVbyte: btcTestFeeRateSatsPerVByte, GuardianPubkey: btcDualTestGuardianPubkey(), AuthorityProtocol: 3})
	base[keys.KeyFrostVaultState("btc", vaultID)] = mustBTCValidationMarshal(t, &pb.FrostVaultState{Chain: "btc", VaultId: vaultID, KeyEpoch: epoch, Status: "ACTIVE", GroupPubkey: btcDualTestOperatorPubkey(), SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, CommitteeMembers: committee})
	base[keys.KeyFrostWithdrawFIFOHead("btc", "BTC")] = []byte("1")
	base[keys.KeyFrostWithdrawFIFOSeq("btc", "BTC")] = []byte(strconv.Itoa(n))
	base[keys.KeyFrostVaultAvailableBalance("btc", "BTC", vaultID)] = []byte(strconv.FormatUint(inputAmount, 10))
	utxo := mustBTCValidationMarshal(t, &pb.FrostUtxo{Txid: inputTxID, Amount: strconv.FormatUint(inputAmount, 10), VaultId: vaultID, ScriptPubkey: custodyScript, FinalizeHeight: 10})
	base[keys.KeyFrostBtcUtxo(vaultID, inputTxID, 0)] = utxo
	base[keys.KeyFrostBtcUtxoFIFOHead(vaultID)] = []byte("1")
	base[keys.KeyFrostBtcUtxoFIFOSeq(vaultID)] = []byte("1")
	base[keys.KeyFrostBtcUtxoFIFOIndex(vaultID, 1)] = utxo
	base[keys.KeyFrostBtcSpendableUtxoIndex(vaultID, 1)] = utxo
	base[keys.KeyFrostBtcUtxoSeqRef(vaultID, inputTxID, 0)] = []byte("1")
	return newMemoryStateView(base), &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: &pb.FrostWithdrawClaimTx{
		Base:  &pb.BaseMessage{TxId: "claim-" + jobID, FromAddress: coordinator, ExecutedHeight: 200},
		JobId: jobID, Chain: "btc", Asset: "BTC", VaultId: vaultID, KeyEpoch: epoch, WithdrawIds: ids,
		TemplateData: templateData, TemplateHash: template.TemplateHash(),
	}}}
}

                                                                         
func formalBTCBatchLimitCase(t *testing.T, n, existing int, exact, contiguous, accepted, writes bool) {
	t.Helper()
	sv, tx := formalBTCBatchLimitFixture(t, n)
	claim := tx.GetFrostWithdrawClaimTx()
	if existing != 0 {
		status := frostWithdrawJobStatusClaimed
		if existing == 2 {
			status = frostWithdrawJobStatusSigned
		}
		sv.Set(keys.KeyFrostWithdrawJob(claim.JobId), mustBTCValidationMarshal(t, &pb.FrostWithdrawJobState{
			JobId: claim.JobId, Chain: "btc", Asset: "BTC", VaultId: claim.VaultId, KeyEpoch: claim.KeyEpoch,
			WithdrawIds: append([]string(nil), claim.WithdrawIds...), FirstSeq: 1, LastSeq: uint64(n),
			TemplateHash: bytes.Clone(claim.TemplateHash), TemplateData: bytes.Clone(claim.TemplateData), Status: status,
		}))
		if !exact {
			claim.WithdrawIds[0] = "tampered-request"
		}
	} else if !contiguous {
		claim.WithdrawIds[0], claim.WithdrawIds[1] = claim.WithdrawIds[1], claim.WithdrawIds[0]
	}
	before := append([]WriteOp(nil), sv.Diff()...)
	ops, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(tx, sv)
	gotAccepted := err == nil && receipt != nil && receipt.Status == "SUCCEED"
	if gotAccepted != accepted {
		t.Fatalf("acceptance differs: n=%d existing=%d exact=%v contiguous=%v want=%v err=%v receipt=%+v", n, existing, exact, contiguous, accepted, err, receipt)
	}
	after := sv.Diff()
	gotWrites := len(ops) != 0 || len(before) != len(after) || (len(before) != 0 && !reflect.DeepEqual(before, after))
	if gotWrites != writes {
		t.Fatalf("reservation/write projection differs: got=%v want=%v ops=%d", gotWrites, writes, len(ops))
	}
	if existing == 0 && accepted {
		job, exists, err := readFrostWithdrawJobState(sv, claim.JobId)
		if err != nil || !exists || job.Status != "CLAIMED" || !sameOrderedWithdrawIDs(job.WithdrawIds, claim.WithdrawIds) {
			t.Fatalf("new claim projection invalid: job=%+v err=%v", job, err)
		}
	}
}

func TestFormalBTCBatchLimitProductionDifferential(t *testing.T) {
	                                                                             
	for _, n := range []int{255, 256, 257} {
		for _, contiguous := range []bool{false, true} {
			t.Run(fmt.Sprintf("new-%d-contiguous-%v", n, contiguous), func(t *testing.T) {
				formalBTCBatchLimitCase(t, n, 0, true, contiguous, n <= 256 && contiguous, n <= 256 && contiguous)
			})
		}
	}
	for _, existing := range []int{1, 2} {
		for _, exact := range []bool{false, true} {
			t.Run(fmt.Sprintf("historical-257-status-%d-exact-%v", existing, exact), func(t *testing.T) { formalBTCBatchLimitCase(t, 257, existing, exact, true, exact, false) })
		}
	}
}

func TestFormalBTCBatchLimitITFDifferential(t *testing.T) {
	path := os.Getenv("DEX_FORMAL_ITF_BTC_BATCH_LIMIT")
	if path == "" {
		t.Skip("run formal/formal_btc_batch_limit.ps1 for retained Quint trace")
	}
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.States) != 8 {
		t.Fatalf("expected init and seven boundary cases, got %d", len(trace.States))
	}
	for i, state := range trace.States[1:] {
		n, err := state.Int("n")
		if err != nil {
			t.Fatal(err)
		}
		existing, err := state.Int("existing")
		if err != nil {
			t.Fatal(err)
		}
		exact, err := state.Bool("exact")
		if err != nil {
			t.Fatal(err)
		}
		contiguous, err := state.Bool("contiguous")
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := state.Bool("accepted")
		if err != nil {
			t.Fatal(err)
		}
		writes, err := state.Bool("writes")
		if err != nil {
			t.Fatal(err)
		}
		t.Run(fmt.Sprintf("edge-%d", i+1), func(t *testing.T) {
			formalBTCBatchLimitCase(t, int(n), int(existing), exact, contiguous, accepted, writes)
		})
	}
}
