package withdrawresult

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"testing"

	"dex/frost/artifact"
	"dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

func handoverRows(t *testing.T, n int) (records, *pb.FrostVaultConfig) {
	t.Helper()
	rows := records{}
	cfg := &pb.FrostVaultConfig{Chain: "eth", VaultCount: 1, VaultRefs: []string{"0x1111111111111111111111111111111111111111"}, EvmChainId: 1, DepositDexChainId: 51025, AuthorityProtocol: 3, GuardianPubkey: guardianResultKey(t)}
	rows.put(keys.KeyFrostVaultConfig("eth", 0), cfg)
	rows.put(keys.KeyFrostVaultState("eth", 0), &pb.FrostVaultState{Chain: "eth", KeyEpoch: uint64(n + 1)})
	key, _ := signBN128ResultFixture(t, make([]byte, 32))
	for epoch := uint64(2); epoch <= uint64(n+1); epoch++ {
		m := &pb.FrostVaultMigrationState{Chain: "eth", MigrationId: chain.MigrationID("eth", 0, epoch), OldEpoch: epoch - 1, NewEpoch: epoch, OldGroupPubkey: key, NewGroupPubkey: key, AuthorityProtocol: 3, FrozenNextBatchSeq: "1", Status: "SIGNED"}
		canonical, _, err := evm.CanonicalMigrationTemplate(cfg, m)
		if err != nil {
			t.Fatal(err)
		}
		m.TemplateHash = canonical.TemplateHash
		m.IntentHash = canonical.SigHashes[0]
		_, sig := signBN128ResultFixture(t, m.IntentHash)
		_, guardian := signBN128ResultSecret(t, m.IntentHash, big.NewInt(29))
		rows.put(keys.KeyFrostVaultMigration("eth", 0, epoch), m)
		rows[keys.KeyFrostVaultHandoverProof("eth", 0, epoch)] = append(bytes.Clone(sig), guardian...)
	}
	return rows, cfg
}

func TestHandoverPageSevenEightNineAndLargeHistoryBound(t *testing.T) {
	for _, n := range []int{7, 8, 9} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			rows, _ := handoverRows(t, n)
			                                                                      
			for i := 0; i < 20000; i++ {
				rows[fmt.Sprintf("unrelated_history_%d", i)] = []byte("history")
			}
			reads := 0
			get := func(key string) ([]byte, bool, error) { reads++; return stateOnlyGet(t, rows)(key) }
			page, err := ResolveHandoverPage(context.Background(), get, "ETH", 0, 1, uint64(n+1))
			want := n
			if want > 8 {
				want = 8
			}
			if err != nil || len(page.Certificates) != want || page.HasMore != (n > 8) || page.NextEpoch != strconv.Itoa(want+1) {
				t.Fatalf("page=%+v err=%v", page, err)
			}
			if reads != 2+want*2 {
				t.Fatalf("point reads=%d want=%d", reads, 2+want*2)
			}
			if n == 9 {
				rest, err := ResolveHandoverPage(context.Background(), get, "eth", 0, 9, 10)
				if err != nil || rest.HasMore || rest.NextEpoch != "10" || len(rest.Certificates) != 1 {
					t.Fatalf("rest=%+v err=%v", rest, err)
				}
			}
		})
	}
}

func TestHandoverPageRejectsInvalidPermanentProof(t *testing.T) {
	for _, mutation := range []string{"chain", "vault", "cutoff", "epoch", "version", "template", "proof", "missing_proof", "dex_chain", "contract", "future", "range"} {
		t.Run(mutation, func(t *testing.T) {
			rows, cfg := handoverRows(t, 2)
			var marker pb.FrostVaultMigrationState
			key := keys.KeyFrostVaultMigration("eth", 0, 2)
			_ = proto.Unmarshal(rows[key], &marker)
			from, target := uint64(1), uint64(3)
			switch mutation {
			case "chain":
				marker.Chain = "base"
			case "vault":
				marker.VaultId = 1
			case "cutoff":
				marker.FrozenNextBatchSeq = "2"
			case "epoch":
				marker.OldEpoch = 2
			case "version":
				marker.AuthorityProtocol = 1
			case "template":
				marker.TemplateData = []byte("corrupt")
			case "proof":
				rows[keys.KeyFrostVaultHandoverProof("eth", 0, 2)][95] ^= 1
			case "missing_proof":
				delete(rows, keys.KeyFrostVaultHandoverProof("eth", 0, 2))
			case "dex_chain":
				cfg.DepositDexChainId++
			case "contract":
				cfg.VaultRefs[0] = "0x2222222222222222222222222222222222222222"
			case "future":
				target = 4
			case "range":
				from = 4
			}
			rows.put(key, &marker)
			rows.put(keys.KeyFrostVaultConfig("eth", 0), cfg)
			if _, err := ResolveHandoverPage(context.Background(), stateOnlyGet(t, rows), "eth", 0, from, target); err == nil {
				t.Fatal("accepted invalid handover")
			}
		})
	}
}

func TestResolveBatchUsesPermanentIndexAndRejectsScope(t *testing.T) {
	legacy, _ := accountFixture(t, "ETH", true)
	rows := permanentRows(legacy)
	rows[keys.KeyFrostWithdrawBatch("eth", 0, "1")] = []byte("withdraw-1")
	result, err := ResolveBatch(context.Background(), stateOnlyGet(t, rows), "eth", 0, "1")
	if err != nil || result.WithdrawID != "withdraw-1" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	rows[keys.KeyFrostWithdrawBatch("eth", 0, "2")] = []byte("withdraw-1")
	_, err = ResolveBatch(context.Background(), rows.get, "eth", 0, "2")
	errorStatus(t, err, http.StatusConflict)
	_, err = ResolveBatch(context.Background(), rows.get, "eth", 0, "3")
	errorStatus(t, err, http.StatusNotFound)
	_, err = ResolveBatch(context.Background(), rows.get, "eth", 0, "01")
	errorStatus(t, err, http.StatusBadRequest)
}

func TestV2ResultVerifiesDirectCertificateFromCheckpoint(t *testing.T) {
	legacy, _ := accountFixture(t, "ETH", true)
	rows := permanentRows(legacy)
	certRows, cfg := handoverRows(t, 6)
	for key, value := range certRows {
		rows[key] = value
	}
	rows.put(keys.KeyFrostVaultConfig("ETH", 0), cfg)
	var job pb.FrostWithdrawJobState
	_ = proto.Unmarshal(rows[keys.KeyFrostPermanentWithdrawJob("job-1")], &job)
	template, err := evm.FromJSON(job.TemplateData)
	if err != nil {
		t.Fatal(err)
	}
	template.AuthorityProtocol = 3
	template.DexChainID = 51025
	job.TemplateData, _ = template.ToJSON()
	job.TemplateHash = template.TemplateHash()
	rows.put(keys.KeyFrostPermanentWithdrawJob("job-1"), &job)
	_, cert, err := signedHandover(rows.get, cfg, "eth", 0, 7)
	if err != nil {
		t.Fatal(err)
	}
	message, _ := template.WithdrawMessage()
	                                                                         
	                                                                     
	_, sig := signBN128ResultFixture(t, keccak(message))
	_, guardian := signBN128ResultSecret(t, keccak(message), big.NewInt(29))
	rows.put(keys.KeyFrostPermanentSignedPackage("job-1", 0), &pb.FrostSignedPackage{JobId: "job-1", SignedPackageBytes: append(bytes.Clone(sig), guardian...), GuardianSigs: [][]byte{guardian}})
	packaged, err := evm.NewEVMAdapter("eth", 1).PackageSignedWithHandovers(job.TemplateData, sig, guardian, []evm.SignedHandover{cert})
	if err != nil {
		t.Fatal(err)
	}
	rec := rows.artifact(t, artifact.Metadata{Kind: artifact.KindWithdraw, Chain: "ETH", VaultID: 0, Epoch: 7, SourceID: "job-1"}, packaged.RawTx)
	raw, _ := artifact.MarshalRecord(rec)
	rows[keys.KeyFrostPermanentArtifact(rec.Metadata.ArtifactID)] = raw
	rows[keys.KeyFrostPermanentWithdrawArtifact("withdraw-1")] = []byte(rec.Metadata.ArtifactID)
	result, err := Resolve(context.Background(), stateOnlyGet(t, rows), "withdraw-1", "")
	if err != nil || result.AuthorityProtocol != 3 || result.DexChainID != "51025" || !result.IncludesHandover || result.Calldata != "0x"+hex.EncodeToString(packaged.RawTx) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	                                                                        
	                                                                
	rows[keys.KeyFrostVaultHandoverProof("eth", 0, 7)] = bytes.Repeat([]byte{1}, 96)
	_, err = Resolve(context.Background(), stateOnlyGet(t, rows), "withdraw-1", "")
	errorStatus(t, err, http.StatusConflict)
}
