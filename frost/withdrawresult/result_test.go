package withdrawresult

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"testing"

	"dex/frost/artifact"
	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/keys"
	"dex/pb"
	"dex/txverify"
	"dex/utils"

	"github.com/btcsuite/btcd/wire"
	"golang.org/x/crypto/sha3"
	"google.golang.org/protobuf/proto"
)

type records map[string][]byte

func (r records) get(key string) ([]byte, bool, error) { v, ok := r[key]; return v, ok, nil }
func (r records) put(key string, value proto.Message)  { r[key], _ = proto.Marshal(value) }
func (r records) artifact(t *testing.T, meta artifact.Metadata, raw []byte) *artifact.Record {
	t.Helper()
	record, err := artifact.NewRecord(meta, raw)
	if err != nil {
		t.Fatal(err)
	}
	r[keys.KeyFrostArtifactMeta(record.Metadata.ArtifactID)], _ = artifact.MarshalMetadata(record.Metadata)
	r[keys.KeyArchiveFrostArtifact(record.Metadata.ArtifactID)], _ = artifact.MarshalRecord(record)
	return record
}

func accountFixture(t *testing.T, name string, native bool) (records, *artifact.Record) {
	t.Helper()
	rows := records{}
	token := ""
	if !native {
		token = "0x3333333333333333333333333333333333333333"
	}
	wd := &pb.FrostWithdrawState{WithdrawId: "withdraw-1", TxId: "request-1", Chain: name, Asset: "asset-1", Amount: "100000", GrossAmount: "100000", FeeAmount: "10", NetAmount: "99990", FeeVersion: 1, To: "0x2222222222222222222222222222222222222222", Status: "SIGNED", JobId: "job-1", SignedTxId: "signed-1", VaultId: 0, LiveSeq: 1, KeyEpoch: 7}
	cfg := &pb.FrostVaultConfig{Chain: name, VaultCount: 1, EvmChainId: 1, AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: guardianResultKey(t), VaultRefs: []string{"0x1111111111111111111111111111111111111111"}}
	if name == "TRX" {
		cfg.EvmChainId = 728126428
	}
	rows.put(keys.KeyArchiveFrostWithdraw(wd.WithdrawId), wd)
	rows.put(keys.KeyFrostVaultConfig(name, 0), cfg)
	rows.put(keys.KeyToken(wd.Asset), &pb.Token{Address: wd.Asset, NativeChain: name, NativeContract: token})
	rows[keys.KeyFrostWithdrawTxRef(wd.TxId)] = []byte(wd.WithdrawId)
	adapter := evm.NewEVMAdapter(name, cfg.EvmChainId)
	template, err := adapter.BuildWithdrawTemplate(chain.WithdrawTemplateParams{
		AuthorityProtocol: 3, DexChainID: 51025, GuardianPubkey: cfg.GuardianPubkey, Chain: name, Asset: wd.Asset, VaultID: 0, KeyEpoch: 7, BatchSeq: "1",
		ContractAddr: cfg.VaultRefs[0], WithdrawIDs: []string{wd.WithdrawId},
		Outputs: []chain.WithdrawOutput{{WithdrawID: wd.WithdrawId, To: wd.To, Amount: wd.Amount, BroadcastFee: wd.BroadcastFee, Token: token}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, evm.SchnorrSigWithYSize)
	big.NewInt(1).FillBytes(sig[:32])                           
	big.NewInt(2).FillBytes(sig[32:64])
	big.NewInt(1).FillBytes(sig[64:])
	packaged, err := adapter.PackageSigned(template.TemplateData, [][]byte{sig}, [][]byte{sig})
	if err != nil {
		t.Fatal(err)
	}
	rows.put(keys.KeyArchiveFrostWithdrawJob(wd.JobId), &pb.FrostWithdrawJobState{
		JobId: wd.JobId, Status: "SIGNED", Chain: name, Asset: "", VaultId: 0, KeyEpoch: 7,
		WithdrawIds: []string{wd.WithdrawId}, TemplateHash: template.TemplateHash, TemplateData: template.TemplateData, BatchSeq: "1",
	})
	rows.put(keys.KeyArchiveFrostSignedPackage(wd.JobId, 0), &pb.FrostSignedPackage{JobId: wd.JobId, SignedPackageBytes: append(bytes.Clone(sig), sig...), GuardianSigs: [][]byte{sig}})
	record := rows.artifact(t, artifact.Metadata{Kind: artifact.KindWithdraw, Chain: name, VaultID: 0, Epoch: 7, SourceID: "job-1"}, packaged.RawTx)
	rows[keys.KeyFrostWithdrawArtifact(wd.WithdrawId)] = []byte(record.Metadata.ArtifactID)
	                                                                            
	                                                             
	rows.put(keys.KeyFrostVaultState(name, 0), &pb.FrostVaultState{Chain: name, KeyEpoch: 8})
	return rows, record
}

func errorStatus(t *testing.T, err error, want int) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Status != want {
		t.Fatalf("error=%v want HTTP %d", err, want)
	}
}

                                                                              
func permanentRows(source records) records {
	rows := records{}
	for key, raw := range source {
		switch {
		case strings.HasPrefix(key, keys.KeyArchiveFrostArtifact("")):
			key = keys.KeyFrostPermanentArtifact(strings.TrimPrefix(key, keys.KeyArchiveFrostArtifact("")))
		case strings.HasPrefix(key, keys.KeyArchiveFrostWithdrawJob("")):
			key = keys.KeyFrostPermanentWithdrawJob(strings.TrimPrefix(key, keys.KeyArchiveFrostWithdrawJob("")))
		case strings.HasPrefix(key, keys.KeyArchiveFrostWithdraw("")):
			key = keys.KeyFrostPermanentWithdraw(strings.TrimPrefix(key, keys.KeyArchiveFrostWithdraw("")))
		case strings.HasPrefix(key, keys.KeyArchiveFrostMigration("")):
			key = keys.KeyFrostPermanentMigration(strings.TrimPrefix(key, keys.KeyArchiveFrostMigration("")))
		case strings.HasPrefix(key, keys.KeyFrostWithdrawArtifact("")):
			key = keys.KeyFrostPermanentWithdrawArtifact(strings.TrimPrefix(key, keys.KeyFrostWithdrawArtifact("")))
		}
		rows[key] = append([]byte(nil), raw...)
	}
	return rows
}

func stateOnlyGet(t *testing.T, rows records) Get {
	t.Helper()
	return func(key string) ([]byte, bool, error) {
		if strings.HasPrefix(key, "archive_") {
			t.Fatalf("StateDB-only result unexpectedly reads archive: %s", key)
		}
		return rows.get(key)
	}
}

func TestAccountResultFromPermanentStateOnly(t *testing.T) {
	for _, name := range []string{"ETH", "BNB", "BASE", "ARB", "TRX"} {
		for _, native := range []bool{true, false} {
			t.Run(name+map[bool]string{true: "/native", false: "/token"}[native], func(t *testing.T) {
				legacy, record := accountFixture(t, name, native)
				rows := permanentRows(legacy)
				                                                               
				rows.put(keys.KeyFrostWithdraw("withdraw-1"), &pb.FrostWithdrawState{WithdrawId: "withdraw-1", Status: "SIGNED"})
				result, err := Resolve(context.Background(), stateOnlyGet(t, rows), "", "request-1")
				if err != nil || result.Calldata != "0x"+hex.EncodeToString(record.RawTx) || result.IsNative != native {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			})
		}
	}
}

func TestPermanentWithdrawalAndReferenceCorruptionDoNotUseArchive(t *testing.T) {
	for _, field := range []string{"withdrawal", "reference", "artifact"} {
		t.Run(field, func(t *testing.T) {
			rows, record := accountFixture(t, "ETH", true)
			permanent := permanentRows(rows)
			for key, raw := range permanent {
				rows[key] = raw
			}
			switch field {
			case "withdrawal":
				rows[keys.KeyFrostPermanentWithdraw("withdraw-1")] = nil
			case "reference":
				rows[keys.KeyFrostPermanentWithdrawArtifact("withdraw-1")] = []byte("invalid")
			case "artifact":
				rows[keys.KeyFrostPermanentArtifact(record.Metadata.ArtifactID)] = nil
			}
			_, err := Resolve(context.Background(), rows.get, "withdraw-1", "")
			errorStatus(t, err, http.StatusConflict)
		})
	}
}

func TestAccountResultBindsAllChainsAndSurvivesDEXRotation(t *testing.T) {
	for _, name := range []string{"ETH", "BNB", "BASE", "ARB", "TRX"} {
		for _, native := range []bool{true, false} {
			t.Run(name+map[bool]string{true: "/native", false: "/token"}[native], func(t *testing.T) {
				rows, record := accountFixture(t, name, native)
				result, err := Resolve(context.Background(), rows.get, "", "request-1")
				if err != nil {
					t.Fatal(err)
				}
				if result.Status != "SIGNED" || result.IsNative != native || result.Epoch != "7" || result.Calldata != "0x"+hex.EncodeToString(record.RawTx) || len(result.Dependencies) != 0 {
					t.Fatalf("unexpected result: %+v", result)
				}
				_, err = Resolve(context.Background(), rows.get, "different", "request-1")
				errorStatus(t, err, http.StatusConflict)
			})
		}
	}
}

func TestAccountResultAcceptsAndBindsAtomicHandoverCalldata(t *testing.T) {
	rows, _ := accountFixture(t, "TRX", false)
	var wd pb.FrostWithdrawState
	var cfg pb.FrostVaultConfig
	_ = proto.Unmarshal(rows[keys.KeyArchiveFrostWithdraw("withdraw-1")], &wd)
	_ = proto.Unmarshal(rows[keys.KeyFrostVaultConfig("TRX", 0)], &cfg)
	params := chain.WithdrawTemplateParams{
		AuthorityProtocol: 3, DexChainID: 51025, GuardianPubkey: cfg.GuardianPubkey, Chain: "TRX", Asset: wd.Asset, VaultID: 0, KeyEpoch: 8, BatchSeq: "1",
		WithdrawIDs: []string{wd.WithdrawId}, ContractAddr: cfg.VaultRefs[0],
		Outputs: []chain.WithdrawOutput{{WithdrawID: wd.WithdrawId, To: wd.To, Amount: wd.Amount, BroadcastFee: wd.BroadcastFee, Token: "0x3333333333333333333333333333333333333333"}},
	}
	adapter := evm.NewEVMAdapter("TRX", cfg.EvmChainId)
	withdraw, err := adapter.BuildWithdrawTemplate(params)
	if err != nil {
		t.Fatal(err)
	}
	rows.put(keys.KeyArchiveFrostWithdrawJob(wd.JobId), &pb.FrostWithdrawJobState{
		JobId: wd.JobId, Status: "SIGNED", Chain: "TRX", Asset: "", VaultId: 0, KeyEpoch: 8,
		WithdrawIds: []string{wd.WithdrawId}, TemplateHash: withdraw.TemplateHash, TemplateData: withdraw.TemplateData, BatchSeq: "1", IncludesHandover: true,
	})
	pubkey, withdrawalSignature := signBN128ResultFixture(t, withdraw.SigHashes[0])
	migration, _, err := evm.BuildMigrationTemplateV2(evm.MigrationV2Params{Chain: "TRX", ChainID: cfg.EvmChainId, DexChainID: 51025, ContractAddr: cfg.VaultRefs[0], VaultID: 0, OldEpoch: 7, NewEpoch: 8, NewGroupPubkey: pubkey, GuardianPubkey: cfg.GuardianPubkey, FrozenNextBatchSeq: "1"})
	if err != nil {
		t.Fatal(err)
	}
	_, handoverSignature := signBN128ResultFixture(t, migration.SigHashes[0])
	_, guardianWithdrawal := signBN128ResultSecret(t, withdraw.SigHashes[0], big.NewInt(29))
	_, guardianHandover := signBN128ResultSecret(t, migration.SigHashes[0], big.NewInt(29))
	packaged, err := adapter.PackageSignedWithHandover(
		withdraw.TemplateData, withdrawalSignature, guardianWithdrawal, migration.TemplateData, handoverSignature, guardianHandover,
	)
	if err != nil {
		t.Fatal(err)
	}
	state := &pb.FrostVaultMigrationState{
		MigrationId: chain.MigrationID("TRX", 0, 8), Chain: "TRX", VaultId: 0, OldEpoch: 7, NewEpoch: 8,
		OldGroupPubkey: pubkey, NewGroupPubkey: pubkey, TemplateHash: migration.TemplateHash,
		TemplateData: migration.TemplateData, IntentHash: migration.SigHashes[0], AuthorityProtocol: 3, FrozenNextBatchSeq: "1", Status: "SIGNED",
	}
	rows[keys.KeyFrostVaultHandoverProof("trx", 0, 8)] = append(bytes.Clone(handoverSignature), guardianHandover...)
	rows.put(keys.KeyArchiveFrostSignedPackage(wd.JobId, 0), &pb.FrostSignedPackage{JobId: wd.JobId, SignedPackageBytes: append(bytes.Clone(withdrawalSignature), guardianWithdrawal...), GuardianSigs: [][]byte{guardianWithdrawal}})
	marker := proto.Clone(state).(*pb.FrostVaultMigrationState)
	marker.TemplateData = nil
	rows.put(keys.KeyFrostVaultMigration("trx", 0, 8), marker)
	rows.put(keys.KeyArchiveFrostMigration(state.MigrationId), &pb.FrostVaultMigrationArchive{State: state, Signature: handoverSignature})
	record := rows.artifact(t, artifact.Metadata{Kind: artifact.KindWithdraw, Chain: "TRX", VaultID: 0, Epoch: 8, SourceID: "job-1"}, packaged.RawTx)
	rows[keys.KeyFrostWithdrawArtifact(wd.WithdrawId)] = []byte(record.Metadata.ArtifactID)

	result, err := Resolve(context.Background(), rows.get, wd.WithdrawId, "")
	if err != nil || result.Calldata != "0x"+hex.EncodeToString(packaged.RawTx) || result.Epoch != "8" || !result.IncludesHandover {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	t.Run("permanent-proof-after-archive-removal", func(t *testing.T) {
		permanent := permanentRows(rows)
		result, err := Resolve(context.Background(), stateOnlyGet(t, permanent), wd.WithdrawId, "")
		if err != nil || !result.IncludesHandover || result.Calldata != "0x"+hex.EncodeToString(packaged.RawTx) {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		                                                                   
		                                                                         
		delete(permanent, keys.KeyFrostPermanentMigration(state.MigrationId))
		result, err = Resolve(context.Background(), stateOnlyGet(t, permanent), wd.WithdrawId, "")
		if err != nil || !result.IncludesHandover || result.Calldata != "0x"+hex.EncodeToString(packaged.RawTx) {
			t.Fatalf("old migration marker result=%+v err=%v", result, err)
		}
		permanent[keys.KeyFrostPermanentMigration(state.MigrationId)] = []byte("invalid")
		permanent[keys.KeyArchiveFrostMigration(state.MigrationId)] = rows[keys.KeyArchiveFrostMigration(state.MigrationId)]
		result, err = Resolve(context.Background(), permanent.get, wd.WithdrawId, "")
		if err != nil || !result.IncludesHandover {
			t.Fatalf("consensus handover marker was shadowed by query archive: result=%+v err=%v", result, err)
		}
	})

	tampered := append([]byte(nil), packaged.RawTx...)
	tampered[228] ^= 1
	tamperedRecord := rows.artifact(t, artifact.Metadata{Kind: artifact.KindWithdraw, Chain: "TRX", Epoch: 8, SourceID: "job-1"}, tampered)
	rows[keys.KeyFrostWithdrawArtifact(wd.WithdrawId)] = []byte(tamperedRecord.Metadata.ArtifactID)
	_, err = Resolve(context.Background(), rows.get, wd.WithdrawId, "")
	errorStatus(t, err, http.StatusConflict)
}

func guardianResultKey(t *testing.T) []byte {
	key, _ := signBN128ResultSecret(t, make([]byte, 32), big.NewInt(29))
	return key
}

func signBN128ResultFixture(t *testing.T, message []byte) ([]byte, []byte) {
	return signBN128ResultSecret(t, message, big.NewInt(1))
}

func signBN128ResultSecret(t *testing.T, message []byte, secret *big.Int) ([]byte, []byte) {
	t.Helper()
	grp := curve.NewBN256Group()
	nonce := big.NewInt(987654321)
	public := grp.ScalarBaseMult(secret)
	commitment := grp.ScalarBaseMult(nonce)
	h := sha3.NewLegacyKeccak256()
	for _, word := range []*big.Int{commitment.X, commitment.Y, public.X, public.Y} {
		padded := make([]byte, 32)
		word.FillBytes(padded)
		_, _ = h.Write(padded)
	}
	_, _ = h.Write(message)
	challenge := new(big.Int).SetBytes(h.Sum(nil))
	challenge.Mod(challenge, grp.Order())
	scalar := new(big.Int).Mul(challenge, secret)
	scalar.Add(scalar, nonce)
	scalar.Mod(scalar, grp.Order())
	signature := make([]byte, evm.SchnorrSigWithYSize)
	commitment.X.FillBytes(signature[:32])
	commitment.Y.FillBytes(signature[32:64])
	scalar.FillBytes(signature[64:])
	return grp.SerializePoint(public), signature
}

func TestAccountResultRejectsTamperedRequestArchiveAndCalldata(t *testing.T) {
	for _, field := range []string{"receiver", "amount", "request", "asset", "metadata", "selector", "offset", "message", "trailing", "missing_raw", "missing_meta", "wrong_artifact"} {
		t.Run(field, func(t *testing.T) {
			rows, record := accountFixture(t, "ETH", true)
			var wd pb.FrostWithdrawState
			_ = proto.Unmarshal(rows[keys.KeyArchiveFrostWithdraw("withdraw-1")], &wd)
			want := http.StatusConflict
			switch field {
			case "receiver":
				wd.To = "0x4444444444444444444444444444444444444444"
			case "amount":
				wd.Amount = "99999"
				wd.GrossAmount = "99999"
			case "request":
				wd.WithdrawId = "other"
			case "asset":
				rows.put(keys.KeyToken(wd.Asset), &pb.Token{NativeChain: "TRX"})
			case "metadata":
				record.Metadata.Epoch++
				rows[keys.KeyArchiveFrostArtifact(record.Metadata.ArtifactID)], _ = artifact.MarshalRecord(record)
			case "missing_raw":
				delete(rows, keys.KeyArchiveFrostArtifact(record.Metadata.ArtifactID))
				want = http.StatusGone
			case "missing_meta":
				delete(rows, keys.KeyFrostArtifactMeta(record.Metadata.ArtifactID))
			case "selector", "offset", "message", "trailing", "wrong_artifact":
				raw := append([]byte(nil), record.RawTx...)
				meta := record.Metadata
				meta.ArtifactID = ""
				switch field {
				case "selector":
					raw[0] ^= 1
				case "offset":
					raw[35] = 64
				case "message":
					raw[300] ^= 1
				case "trailing":
					raw = append(raw, 0)
				case "wrong_artifact":
					meta.SourceID = "other-job"
				}
				newRecord := rows.artifact(t, meta, raw)
				rows[keys.KeyFrostWithdrawArtifact(wd.WithdrawId)] = []byte(newRecord.Metadata.ArtifactID)
			}
			rows.put(keys.KeyArchiveFrostWithdraw("withdraw-1"), &wd)
			_, err := Resolve(context.Background(), rows.get, "withdraw-1", "")
			errorStatus(t, err, want)
		})
	}
}

func TestPendingResultNeedsNoSigningOrExternalRecords(t *testing.T) {
	for _, status := range []string{"QUEUED", "SIGNING"} {
		rows := records{}
		rows.put(keys.KeyFrostWithdraw("w"), &pb.FrostWithdrawState{WithdrawId: "w", TxId: "r", Chain: "ETH", Amount: "10", Status: status})
		result, err := Resolve(context.Background(), rows.get, "w", "")
		if err != nil || result.Status != status || result.Calldata != "" || result.RawTxHex != "" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
	for _, id := range []string{"", "../private", "a/b", "a\\b", " a", strings.Repeat("x", 257)} {
		_, err := Resolve(context.Background(), records{}.get, id, "")
		errorStatus(t, err, http.StatusBadRequest)
	}
}

func TestCancelledResultIsTerminalAndReturnsRefundAuditFields(t *testing.T) {
	rows := records{}
	rows.put(keys.KeyArchiveFrostWithdraw("w"), &pb.FrostWithdrawState{
		WithdrawId: "w", TxId: "r", Status: "CANCELLED", Requester: "fbc-requester",
		CancelTxId: "cancel-1", CancelHeight: 42, Chain: "BTC", Asset: "btc-token", Amount: "10000", To: "bc1qreceiver",
	})
	result, err := Resolve(context.Background(), rows.get, "w", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "CANCELLED" || result.Requester != "fbc-requester" || result.CancelTxID != "cancel-1" || result.CancelHeight != 42 {
		t.Fatalf("unexpected cancelled result: %+v", result)
	}
	if result.Calldata != "" || result.RawTxHex != "" {
		t.Fatalf("cancelled result exposed signing artifact: %+v", result)
	}
}

func legacyRequestProof(t *testing.T) (*pb.AnyTx, []byte) {
	t.Helper()
	identity := utils.GenerateTestIdentity(77)
	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawRequestTx{FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{
		Base:  &pb.BaseMessage{FromAddress: identity.Address, Nonce: 3, Fee: "100", ChainId: 1},
		Chain: "ETH", Asset: "asset-legacy", To: "0x2222222222222222222222222222222222222222", Amount: "100",
	}}}
	if _, err := txverify.SignWithPrivateKey(tx, identity.PrivateKeyHex); err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	return tx, raw
}

func TestLegacyQueuedResultReturnsVerifiedOriginalRequestProof(t *testing.T) {
	original, raw := legacyRequestProof(t)
	rows := records{}
	rows.put(keys.KeyFrostWithdraw("w"), &pb.FrostWithdrawState{
		WithdrawId: "w", TxId: original.GetTxId(), Status: "QUEUED", Chain: "ETH", Asset: "asset-legacy",
		Amount: "100", To: "0x2222222222222222222222222222222222222222",
	})
	rows[keys.KeyTxRaw(original.GetTxId())] = raw

	result, err := Resolve(context.Background(), rows.get, "w", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Requester != original.GetBase().FromAddress || result.OriginalRequestTx != "0x"+hex.EncodeToString(raw) {
		t.Fatalf("legacy proof not returned: %+v", result)
	}

	                                                                           
	                                                                             
	                                                                            
	var tampered pb.AnyTx
	if err := proto.Unmarshal(raw, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered.GetFrostWithdrawRequestTx().Amount = "101"
	rows[keys.KeyTxRaw(original.GetTxId())], _ = proto.Marshal(&tampered)
	result, err = Resolve(context.Background(), rows.get, "w", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Requester != "" || result.OriginalRequestTx != "" {
		t.Fatalf("tampered legacy proof accepted: %+v", result)
	}
}

func TestLegacySummaryDoesNotExpandTxRaw(t *testing.T) {
	original, _ := legacyRequestProof(t)
	rows := records{}
	rows.put(keys.KeyFrostWithdraw("w"), &pb.FrostWithdrawState{
		WithdrawId: "w", TxId: original.GetTxId(), Status: "QUEUED", Chain: "ETH", Asset: "asset-legacy",
		Amount: "100", To: "0x2222222222222222222222222222222222222222",
	})
	reads := 0
	get := func(key string) ([]byte, bool, error) {
		reads++
		if key == keys.KeyTxRaw(original.GetTxId()) {
			t.Fatal("summary expanded legacy txraw")
		}
		return rows.get(key)
	}
	result, err := ResolveSummary(context.Background(), get, "w", "")
	if err != nil || result.Requester != "" || result.OriginalRequestTx != "" || reads != 2 {
		t.Fatalf("summary=%+v err=%v reads=%d", result, err, reads)
	}
}

func TestSignedHistorySummaryDoesNotReadOrExpandArtifact(t *testing.T) {
	rows, _ := accountFixture(t, "ETH", true)
	reads := 0
	get := func(key string) ([]byte, bool, error) {
		reads++
		if key != keys.KeyFrostPermanentWithdraw("withdraw-1") && key != keys.KeyFrostWithdraw("withdraw-1") && key != keys.KeyArchiveFrostWithdraw("withdraw-1") {
			t.Fatalf("history expanded unrelated data %s", key)
		}
		return rows.get(key)
	}
	result, err := ResolveSummary(context.Background(), get, "withdraw-1", "")
	if err != nil || result.Status != "SIGNED" || result.ArtifactID != "" || result.Calldata != "" || reads != 3 {
		t.Fatalf("summary=%+v err=%v reads=%d", result, err, reads)
	}
}

func TestLegacyResultRequiresOriginalPermanentAndArchiveData(t *testing.T) {
	rows, record := accountFixture(t, "ETH", true)
	delete(rows, keys.KeyFrostWithdrawArtifact("withdraw-1"))
	result, err := Resolve(context.Background(), rows.get, "withdraw-1", "")
	if err != nil || result.ArtifactID != record.Metadata.ArtifactID {
		t.Fatalf("legacy result=%+v err=%v", result, err)
	}
	delete(rows, keys.KeyArchiveFrostArtifact(record.Metadata.ArtifactID))
	_, err = Resolve(context.Background(), rows.get, "withdraw-1", "")
	errorStatus(t, err, http.StatusGone)
}

func TestBTCResultDeliversSignedParentBeforeChildAndFailsClosed(t *testing.T) {
	rows := records{}
	                                                                             
	address := "bcrt1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqdku202"
	script, err := btc.AddressScriptForNetwork(address, "regtest")
	if err != nil {
		t.Fatal(err)
	}
	makeRecord := func(parent *artifact.Record, sequence uint32, source string) *artifact.Record {
		tx := wire.NewMsgTx(2)
		in := wire.NewTxIn(&wire.OutPoint{}, nil, wire.TxWitness{bytes.Repeat([]byte{1}, 64)})
		in.Sequence = sequence
		var parents []string
		if parent != nil {
			var ptx wire.MsgTx
			_ = ptx.Deserialize(bytes.NewReader(parent.RawTx))
			in.PreviousOutPoint.Hash = ptx.TxHash()
			parents = []string{parent.Metadata.ArtifactID}
		}
		tx.AddTxIn(in)
		tx.AddTxOut(wire.NewTxOut(1000, script))
		var raw bytes.Buffer
		if err := tx.Serialize(&raw); err != nil {
			t.Fatal(err)
		}
		return rows.artifact(t, artifact.Metadata{ArtifactID: tx.TxHash().String(), Kind: artifact.KindWithdraw, Chain: "BTC", Epoch: 2, SourceID: source, ParentIDs: parents}, raw.Bytes())
	}
	parent := makeRecord(nil, 1, "parent-job")
	middle := makeRecord(parent, 2, "middle-job")
	root := makeRecord(middle, 3, "job")
	template := &btc.BTCTemplate{
		Version:     2,
		Inputs:      []btc.TxInput{{TxID: strings.Repeat("00", 32), Amount: 3080, Sequence: 3}},
		Outputs:     []btc.TxOutput{{Address: address, Amount: 1000}},
		KeyEpoch:    2,
		WithdrawIDs: []string{"w"},
	}
	templateData, err := template.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	rows.put(keys.KeyArchiveFrostWithdraw("w"), &pb.FrostWithdrawState{WithdrawId: "w", TxId: "r", Chain: "BTC", Status: "SIGNED", JobId: "job", Amount: "3080", To: address})
	rows.put(keys.KeyArchiveFrostWithdrawJob("job"), &pb.FrostWithdrawJobState{JobId: "job", Chain: "BTC", Status: "SIGNED", KeyEpoch: 2, WithdrawIds: []string{"w"}, TemplateHash: template.TemplateHash(), TemplateData: templateData})
	rows.put(keys.KeyFrostVaultConfig("BTC", 0), &pb.FrostVaultConfig{Chain: "BTC", VaultCount: 1, BtcNetwork: "regtest"})
	rows[keys.KeyFrostWithdrawArtifact("w")] = []byte(root.Metadata.ArtifactID)
	result, err := Resolve(context.Background(), rows.get, "w", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.TxID != root.Metadata.ArtifactID || result.RawTxHex != hex.EncodeToString(root.RawTx) || len(result.Dependencies) != 2 || result.Dependencies[0].TxID != parent.Metadata.ArtifactID || result.Dependencies[1].TxID != middle.Metadata.ArtifactID {
		t.Fatalf("wrong BTC package: %+v", result)
	}
	if result.RequestedAmount != "3080" || result.NetworkFee != "2080" || result.ReceivedAmount != "1000" {
		t.Fatalf("wrong BTC user-paid amounts: %+v", result)
	}
	t.Run("permanent-parent-package-after-archive-removal", func(t *testing.T) {
		permanent := permanentRows(rows)
		result, err := Resolve(context.Background(), stateOnlyGet(t, permanent), "w", "")
		if err != nil || result.RawTxHex != hex.EncodeToString(root.RawTx) || len(result.Dependencies) != 2 || result.Dependencies[0].TxID != parent.Metadata.ArtifactID || result.Dependencies[1].TxID != middle.Metadata.ArtifactID {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		permanent[keys.KeyFrostPermanentWithdrawJob("job")] = []byte("invalid")
		permanent[keys.KeyArchiveFrostWithdrawJob("job")] = rows[keys.KeyArchiveFrostWithdrawJob("job")]
		_, err = Resolve(context.Background(), permanent.get, "w", "")
		errorStatus(t, err, http.StatusConflict)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Resolve(ctx, rows.get, "w", "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	delete(rows, keys.KeyArchiveFrostArtifact(parent.Metadata.ArtifactID))
	_, err = Resolve(context.Background(), rows.get, "w", "")
	errorStatus(t, err, http.StatusGone)
}
