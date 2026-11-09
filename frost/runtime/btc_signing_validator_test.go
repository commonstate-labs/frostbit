package runtime

import (
	"bytes"
	"testing"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/frost/runtime/planning"
	"dex/frost/runtime/roast"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"

	"google.golang.org/protobuf/proto"
)

type signingValidatorCountingReader struct {
	data       map[string][]byte
	getCount   int
	batchCount int
	scanCount  int
}

func (r *signingValidatorCountingReader) GetMany(keys []string) (map[string][]byte, error) {
	r.batchCount++
	values := make(map[string][]byte, len(keys))
	for _, key := range keys {
		if value, exists := r.data[key]; exists {
			values[key] = append([]byte(nil), value...)
		}
	}
	return values, nil
}

func (r *signingValidatorCountingReader) Get(key string) ([]byte, bool, error) {
	r.getCount++
	value, exists := r.data[key]
	return append([]byte(nil), value...), exists, nil
}

func (r *signingValidatorCountingReader) Scan(string, func(string, []byte) bool) error {
	r.scanCount++
	return nil
}

type btcSigningValidatorFixture struct {
	reader   *signingValidatorCountingReader
	env      *roast.Envelope
	messages [][]byte
	lockKey  string
}

type evmSigningValidatorFixture struct {
	reader       *signingValidatorCountingReader
	env          *roast.Envelope
	messages     [][]byte
	templateData []byte
}

func newEVMSigningValidatorFixture(t *testing.T) *evmSigningValidatorFixture {
	t.Helper()
	const (
		chainName  = chain.ChainETH
		asset      = "USDT"
		withdrawID = "evm-withdraw-1"
		vaultID    = uint32(2)
		keyEpoch   = uint64(7)
	)
	reader := &signingValidatorCountingReader{data: make(map[string][]byte)}
	put := func(key string, msg proto.Message) {
		data, err := proto.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal %s: %v", key, err)
		}
		reader.data[key] = data
	}
	put(keys.KeyFrostWithdraw(withdrawID), &pb.FrostWithdrawState{
		WithdrawId: withdrawID, Seq: 11, Chain: chainName, Asset: asset,
		To: "0x1111111111111111111111111111111111111111", Amount: "25", GrossAmount: "25", FeeAmount: "1", NetAmount: "24", FeeVersion: 1,
		Status: "SIGNING", VaultId: vaultID, LiveSeq: 11, KeyEpoch: keyEpoch,
	})
	put(keys.KeyToken(asset), &pb.Token{
		Address: asset, Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: chainName,
		NativeContract: "0x2222222222222222222222222222222222222222",
	})
	put(keys.KeyFrostVaultConfig(chainName, 0), &pb.FrostVaultConfig{AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: runtimeAccountGuardianKey(),
		Chain: chainName, EvmChainId: 1, VaultRefs: []string{
			"0x0000000000000000000000000000000000000000",
			"0x0000000000000000000000000000000000000000",
			"0x3333333333333333333333333333333333333333",
		},
	})
	put(keys.KeyFrostVaultState(chainName, vaultID), &pb.FrostVaultState{
		Chain: chainName, VaultId: vaultID, KeyEpoch: keyEpoch, Status: "ACTIVE",
		SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128, GroupPubkey: bytes.Repeat([]byte{0x44}, 64),
		CommitteeMembers: []string{"coordinator", "participant"},
	})

	params, firstSeq, err := planning.BuildAccountTemplateParams(reader, chainName, asset, vaultID, keyEpoch, []string{withdrawID}, nil)
	if err != nil {
		t.Fatalf("build EVM params: %v", err)
	}
	canonical, err := evm.NewEVMAdapter(chainName, 1).BuildWithdrawTemplate(params)
	if err != nil {
		t.Fatalf("build EVM template: %v", err)
	}
	jobID := chain.ComputeWithdrawJobID(chainName, "", vaultID, firstSeq, canonical.TemplateHash, keyEpoch)
	var withdraw pb.FrostWithdrawState
	_ = proto.Unmarshal(reader.data[keys.KeyFrostWithdraw(withdrawID)], &withdraw)
	withdraw.JobId = jobID
	put(keys.KeyFrostWithdraw(withdrawID), &withdraw)
	put(keys.KeyFrostWithdrawJob(jobID), &pb.FrostWithdrawJobState{
		JobId: jobID, Chain: chainName, Asset: "", VaultId: vaultID, KeyEpoch: keyEpoch,
		WithdrawIds: []string{withdrawID}, FirstSeq: firstSeq, LastSeq: firstSeq,
		TemplateHash: canonical.TemplateHash, TemplateData: canonical.TemplateData, Status: "CLAIMED",
		BatchSeq: "1",
	})
	reader.data[keys.KeyFrostWithdrawActiveVaultJob(chainName, vaultID)] = []byte(jobID)
	reader.data[keys.KeyFrostVaultReservedBalance(chainName, asset, vaultID)] = []byte("25")
	reader.getCount, reader.batchCount, reader.scanCount = 0, 0, 0
	return &evmSigningValidatorFixture{
		reader: reader,
		env: &roast.Envelope{
			SessionID: jobID, Chain: chainName, VaultID: vaultID, Epoch: keyEpoch,
			SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		},
		messages: canonical.SigHashes, templateData: canonical.TemplateData,
	}
}

func newBTCSigningValidatorFixture(t *testing.T) *btcSigningValidatorFixture {
	t.Helper()

	const (
		jobID    = "btc-signing-job"
		vaultID  = uint32(7)
		keyEpoch = uint64(3)
	)
	txid := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tweak := bytes.Repeat([]byte{0x31}, 32)
	operatorPrivate, _ := btcec.PrivKeyFromBytes([]byte{23})
	guardianPrivate, _ := btcec.PrivKeyFromBytes([]byte{31})
	operator := schnorr.SerializePubKey(operatorPrivate.PubKey())
	guardian := schnorr.SerializePubKey(guardianPrivate.PubKey())
	tweaked, err := utils.ComputeTweakedPubkey(operator, tweak)
	if err != nil {
		t.Fatal(err)
	}
	scriptPubKey, leaf, control, err := utils.BuildDualCustodyTaproot(tweaked, guardian)
	if err != nil {
		t.Fatal(err)
	}
	template := &btc.BTCTemplate{
		Version:  2,
		LockTime: 0,
		Inputs: []btc.TxInput{{
			TxID: txid, Vout: 1, Amount: 125_000, Sequence: btc.DefaultSequence, SpendScript: leaf, ControlBlock: control,
		}},
		Outputs:     []btc.TxOutput{{Address: "tb1pdpd76eytdcdwnm62uuxve403srq0dtcls0uj488wjjy4z4lfm25sdwatf0", Amount: 124_000}},
		VaultID:     vaultID,
		KeyEpoch:    keyEpoch,
		WithdrawIDs: []string{"withdraw-1"},
	}
	templateData, err := template.ToJSON()
	if err != nil {
		t.Fatalf("marshal BTC template: %v", err)
	}
	messages, err := template.ComputeTaprootSighash([][]byte{scriptPubKey}, btc.SighashDefault)
	if err != nil {
		t.Fatalf("compute BTC sighash: %v", err)
	}

	jobData, err := proto.Marshal(&pb.FrostWithdrawJobState{
		JobId: jobID, Chain: "btc", Asset: "BTC", VaultId: vaultID, KeyEpoch: keyEpoch,
		WithdrawIds: []string{"withdraw-1"}, TemplateHash: template.TemplateHash(),
		TemplateData: templateData, Status: "CLAIMED",
	})
	if err != nil {
		t.Fatalf("marshal signing job: %v", err)
	}
	utxoData, err := proto.Marshal(&pb.FrostUtxo{
		Txid: txid, Vout: 1, Amount: "125000", VaultId: vaultID,
		ScriptPubkey: scriptPubKey, Tweak: tweak,
	})
	if err != nil {
		t.Fatalf("marshal BTC UTXO: %v", err)
	}
	lockKey := keys.KeyFrostBtcLockedUtxo(vaultID, txid, 1)
	reader := &signingValidatorCountingReader{data: map[string][]byte{
		keys.KeyFrostWithdrawJob(jobID):        jobData,
		keys.KeyFrostBtcUtxo(vaultID, txid, 1): utxoData,
		lockKey:                                []byte(jobID),
	}}
	cfg, _ := proto.Marshal(&pb.FrostVaultConfig{Chain: "btc", GuardianPubkey: guardian})
	vault, _ := proto.Marshal(&pb.FrostVaultState{Chain: "btc", VaultId: vaultID, KeyEpoch: keyEpoch, GroupPubkey: operator})
	reader.data[keys.KeyFrostVaultConfig("btc", 0)] = cfg
	reader.data[keys.KeyFrostVaultState("btc", vaultID)] = vault
	return &btcSigningValidatorFixture{
		reader: reader,
		env: &roast.Envelope{
			SessionID: jobID, Chain: "btc", VaultID: vaultID, Epoch: keyEpoch,
			SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
			Tweaks:   [][]byte{append([]byte(nil), tweak...)},
		},
		messages: messages,
		lockKey:  lockKey,
	}
}

func TestSigningRequestValidatorAcceptsFinalizedBTCIntentWithoutScan(t *testing.T) {
	fixture := newBTCSigningValidatorFixture(t)
	if err := newSigningRequestValidator(fixture.reader)(fixture.env, fixture.messages); err != nil {
		t.Fatalf("valid BTC signing intent rejected: %v", err)
	}
	if fixture.reader.scanCount != 0 {
		t.Fatalf("validator must not scan state: got=%d", fixture.reader.scanCount)
	}
	if fixture.reader.getCount != 1 || fixture.reader.batchCount != 1 {
		t.Fatalf("unexpected state reads: gets=%d batches=%d", fixture.reader.getCount, fixture.reader.batchCount)
	}
}

func TestSigningRequestValidatorRejectsBTCTamperingWithoutScan(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(*btcSigningValidatorFixture)
	}{
		{
			name: "message",
			tamper: func(f *btcSigningValidatorFixture) {
				f.messages[0] = append([]byte(nil), f.messages[0]...)
				f.messages[0][0] ^= 0xff
			},
		},
		{
			name: "tweak",
			tamper: func(f *btcSigningValidatorFixture) {
				f.env.Tweaks[0][0] ^= 0xff
			},
		},
		{
			name: "lock",
			tamper: func(f *btcSigningValidatorFixture) {
				f.reader.data[f.lockKey] = []byte("another-job")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newBTCSigningValidatorFixture(t)
			tt.tamper(fixture)
			if err := newSigningRequestValidator(fixture.reader)(fixture.env, fixture.messages); err == nil {
				t.Fatal("tampered BTC signing intent was accepted")
			}
			if fixture.reader.scanCount != 0 {
				t.Fatalf("validator must not scan state: got=%d", fixture.reader.scanCount)
			}
		})
	}
}

func TestSigningRequestValidatorAcceptsFinalizedCanonicalEVMIntentWithoutScan(t *testing.T) {
	fixture := newEVMSigningValidatorFixture(t)
	if err := newSigningRequestValidator(fixture.reader)(fixture.env, fixture.messages); err != nil {
		t.Fatalf("valid EVM signing intent rejected: %v", err)
	}
	if fixture.reader.scanCount != 0 || fixture.reader.batchCount > 4 || fixture.reader.getCount > 8 {
		t.Fatalf("unexpected EVM state reads: gets=%d batches=%d scans=%d", fixture.reader.getCount, fixture.reader.batchCount, fixture.reader.scanCount)
	}
}

func TestSigningRequestValidatorRejectsEVMWithoutFinalizedReservation(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(*evmSigningValidatorFixture)
	}{
		{
			name: "missing canonical batch sequence",
			tamper: func(f *evmSigningValidatorFixture) {
				mutateEVMFixtureJob(t, f, func(job *pb.FrostWithdrawJobState) { job.BatchSeq = "" })
			},
		},
		{
			name: "mismatched canonical batch sequence",
			tamper: func(f *evmSigningValidatorFixture) {
				mutateEVMFixtureJob(t, f, func(job *pb.FrostWithdrawJobState) { job.BatchSeq = "2" })
			},
		},
		{
			name: "missing vault reservation",
			tamper: func(f *evmSigningValidatorFixture) {
				delete(f.reader.data, keys.KeyFrostVaultReservedBalance(f.env.Chain, "USDT", f.env.VaultID))
			},
		},
		{
			name: "insufficient vault reservation",
			tamper: func(f *evmSigningValidatorFixture) {
				f.reader.data[keys.KeyFrostVaultReservedBalance(f.env.Chain, "USDT", f.env.VaultID)] = []byte("24")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newEVMSigningValidatorFixture(t)
			tt.tamper(fixture)
			if err := newSigningRequestValidator(fixture.reader)(fixture.env, fixture.messages); err == nil {
				t.Fatal("EVM signing request without a matching finalized reservation was accepted")
			}
			if fixture.reader.scanCount != 0 {
				t.Fatalf("validator must not scan state: got=%d", fixture.reader.scanCount)
			}
		})
	}
}

func mutateEVMFixtureJob(t *testing.T, fixture *evmSigningValidatorFixture, mutate func(*pb.FrostWithdrawJobState)) {
	t.Helper()
	key := keys.KeyFrostWithdrawJob(fixture.env.SessionID)
	var job pb.FrostWithdrawJobState
	if err := proto.Unmarshal(fixture.reader.data[key], &job); err != nil {
		t.Fatalf("unmarshal EVM fixture job: %v", err)
	}
	mutate(&job)
	data, err := proto.Marshal(&job)
	if err != nil {
		t.Fatalf("marshal EVM fixture job: %v", err)
	}
	fixture.reader.data[key] = data
}

func TestSigningRequestValidatorRejectsEVMBusinessFieldTamperingWithoutScan(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(*evm.EVMTemplate)
	}{
		{name: "amount", tamper: func(template *evm.EVMTemplate) { template.Outputs[0].GrossAmount = "26" }},
		{name: "token", tamper: func(template *evm.EVMTemplate) {
			template.Outputs[0].Token = "0x4444444444444444444444444444444444444444"
			template.RewardTokens[0] = template.Outputs[0].Token
		}},
		{name: "recipient", tamper: func(template *evm.EVMTemplate) { template.Outputs[0].To = "0x5555555555555555555555555555555555555555" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newEVMSigningValidatorFixture(t)
			template, err := evm.FromJSON(fixture.templateData)
			if err != nil {
				t.Fatalf("decode EVM template: %v", err)
			}
			tt.tamper(template)
			message, err := template.SigningHash()
			if err != nil {
				t.Fatalf("hash tampered EVM template: %v", err)
			}
			if err := newSigningRequestValidator(fixture.reader)(fixture.env, [][]byte{message}); err == nil {
				t.Fatal("tampered EVM signing request was accepted")
			}
			if fixture.reader.scanCount != 0 {
				t.Fatalf("validator must not scan state: got=%d", fixture.reader.scanCount)
			}
		})
	}
}

func TestSigningRequestValidatorRejectsEVMWithoutFinalizedIntent(t *testing.T) {
	reader := &signingValidatorCountingReader{data: map[string][]byte{}}
	env := &roast.Envelope{
		SessionID: "attacker-selected-session", Chain: "eth", VaultID: 9, Epoch: 2,
		SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
	}
	if err := newSigningRequestValidator(reader)(env, [][]byte{{0x01}}); err == nil {
		t.Fatal("EVM request without finalized intent was accepted")
	}
	if reader.getCount != 1 || reader.batchCount != 0 || reader.scanCount != 0 {
		t.Fatalf("unexpected state reads: gets=%d batches=%d scans=%d", reader.getCount, reader.batchCount, reader.scanCount)
	}
}

func TestSigningRequestValidatorRejectsLegacyDKGTransitionIntent(t *testing.T) {
	const (
		chainName       = chain.ChainETH
		vaultID         = uint32(4)
		oldEpoch        = uint64(8)
		transitionEpoch = oldEpoch + 1
	)
	message := bytes.Repeat([]byte{0x66}, 32)
	sessionID := "migration_eth_4_9"
	transitionData, err := proto.Marshal(&pb.VaultTransitionState{
		Chain: chainName, VaultId: vaultID, EpochId: transitionEpoch,
		SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128, DkgStatus: "KEY_READY", Lifecycle: "DRAINING",
		ValidationMsgHash: message,
	})
	if err != nil {
		t.Fatalf("marshal transition: %v", err)
	}
	reader := &signingValidatorCountingReader{data: map[string][]byte{
		keys.KeyFrostVaultTransition(chainName, vaultID, transitionEpoch): transitionData,
	}}
	env := &roast.Envelope{
		SessionID: sessionID, Chain: chainName, VaultID: vaultID, Epoch: oldEpoch,
		SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
	}
	if err := newSigningRequestValidator(reader)(env, [][]byte{message}); err == nil {
		t.Fatal("legacy DKG validation hash must never authorize a migration")
	}
	tampered := append([]byte(nil), message...)
	tampered[0] ^= 0xff
	if err := newSigningRequestValidator(reader)(env, [][]byte{tampered}); err == nil {
		t.Fatal("tampered EVM transition message was accepted")
	}
	if reader.scanCount != 0 {
		t.Fatalf("transition validator must not scan state: got=%d", reader.scanCount)
	}
}

func TestSigningRequestValidatorRejectsBTCWithoutFinalizedIntent(t *testing.T) {
	reader := &signingValidatorCountingReader{data: map[string][]byte{}}
	env := &roast.Envelope{SessionID: "migration_btc_without_intent", Chain: "btc", VaultID: 9, Epoch: 2, SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340}
	if err := newSigningRequestValidator(reader)(env, [][]byte{{0x01}}); err == nil {
		t.Fatal("BTC request without finalized intent was accepted")
	}
	if reader.getCount != 1 || reader.batchCount != 0 || reader.scanCount != 0 {
		t.Fatalf("unexpected state reads: gets=%d batches=%d scans=%d", reader.getCount, reader.batchCount, reader.scanCount)
	}
}
