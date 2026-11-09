package runtime

import (
	"bytes"
	"math/big"
	"testing"

	"dex/frost/chain/btc"
	"dex/frost/core/curve"
	"dex/frost/guardian"
	"dex/keys"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

type formalGuardianNodeFixture struct {
	reader   ChainStateReader
	data     map[string][]byte
	request  guardian.Request
	messages [][]byte
	guardian []byte
}

func formalGuardianNode(t *testing.T, kind string) formalGuardianNodeFixture {
	t.Helper()
	if kind == "btc-withdraw" || kind == "account-withdraw" {
		var reader *signingValidatorCountingReader
		var jobID, chainName string
		var vaultID uint32
		var epoch uint64
		var messages [][]byte
		if kind == "btc-withdraw" {
			f := newBTCSigningValidatorFixture(t)
			reader, jobID, chainName, vaultID, epoch, messages = f.reader, f.env.SessionID, f.env.Chain, f.env.VaultID, f.env.Epoch, f.messages
			                                                                   
			                                                                          
			var vault pb.FrostVaultState
			if err := proto.Unmarshal(reader.data[keys.KeyFrostVaultState(chainName, vaultID)], &vault); err != nil {
				t.Fatal(err)
			}
			vault.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
			formalGuardianPut(t, reader.data, keys.KeyFrostVaultState(chainName, vaultID), &vault)
		} else {
			f := newEVMSigningValidatorFixture(t)
			reader, jobID, chainName, vaultID, epoch, messages = f.reader, f.env.SessionID, f.env.Chain, f.env.VaultID, f.env.Epoch, f.messages
		}
		var job pb.FrostWithdrawJobState
		var cfg pb.FrostVaultConfig
		if err := proto.Unmarshal(reader.data[keys.KeyFrostWithdrawJob(jobID)], &job); err != nil {
			t.Fatal(err)
		}
		if err := proto.Unmarshal(reader.data[keys.KeyFrostVaultConfig(chainName, 0)], &cfg); err != nil {
			t.Fatal(err)
		}
		var vault pb.FrostVaultState
		if err := proto.Unmarshal(reader.data[keys.KeyFrostVaultState(chainName, vaultID)], &vault); err != nil {
			t.Fatal(err)
		}
		vault.Status = "ACTIVE"
		if kind == "account-withdraw" {
			group := curve.NewBN256Group()
			vault.GroupPubkey = group.SerializePoint(group.ScalarBaseMult(big.NewInt(17)))
		}
		cfg.AuthorityProtocol, cfg.VaultCount, cfg.SignAlgo = 3, vaultID+1, vault.SignAlgo
		formalGuardianPut(t, reader.data, keys.KeyFrostVaultConfig(chainName, 0), &cfg)
		formalGuardianPut(t, reader.data, keys.KeyFrostVaultState(chainName, vaultID), &vault)
		return formalGuardianNodeFixture{reader: reader, data: reader.data, request: guardian.Request{Chain: chainName, VaultID: vaultID, Epoch: epoch, JobID: jobID, TemplateHash: append([]byte(nil), job.TemplateHash...)}, messages: messages, guardian: cfg.GuardianPubkey}
	}
	name, funded := "btc", false
	switch kind {
	case "btc-funded-migration":
		funded = true
	case "eth-migration":
		name = "eth"
	case "bnb-migration":
		name = "bnb"
	case "base-migration":
		name = "base"
	case "arb-migration":
		name = "arb"
	case "trx-migration":
		name = "trx"
	}
	f := newMigrationValidatorFixture(t, name, funded)
	f.cfg.AuthorityProtocol = 3
	f.save(t)
	return formalGuardianNodeFixture{reader: f.reader, data: f.reader.data, request: guardian.Request{Chain: name, VaultID: f.env.VaultID, Epoch: f.env.Epoch, JobID: f.env.SessionID, TemplateHash: append([]byte(nil), f.intent.TemplateHash...)}, messages: f.messages, guardian: f.cfg.GuardianPubkey}
}

func formalGuardianPut(t *testing.T, data map[string][]byte, key string, message proto.Message) {
	t.Helper()
	raw, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	data[key] = raw
}

var formalGuardianNodeKinds = []string{"btc-withdraw", "account-withdraw", "btc-no-funds-migration", "btc-funded-migration", "eth-migration", "bnb-migration", "base-migration", "arb-migration", "trx-migration"}

func TestFormalDualCustodyGuardianNodeRebuildsFinalizedMessages(t *testing.T) {
	for _, kind := range formalGuardianNodeKinds {
		t.Run(kind, func(t *testing.T) {
			f := formalGuardianNode(t, kind)
			intent, err := ResolveGuardianIntent(f.reader, f.request)
			if err != nil {
				t.Fatal(err)
			}
			if !sameSigningMatrix(intent.Messages, f.messages) || !bytes.Equal(intent.PublicKey, f.guardian) || intent.SignAlgo == 0 {
				t.Fatal("guardian independently rebuilt different messages, key or algorithm")
			}
			                                                                            
			intent.PublicKey[0] ^= 1
			intent.Messages[0][0] ^= 1
			again, err := ResolveGuardianIntent(f.reader, f.request)
			if err != nil || !sameSigningMatrix(again.Messages, f.messages) || !bytes.Equal(again.PublicKey, f.guardian) {
				t.Fatal("caller mutation changed trusted intent state")
			}
		})
	}
}

func TestFormalDualCustodyGuardianNodeRejectsIdentityMutation(t *testing.T) {
	for _, kind := range formalGuardianNodeKinds {
		for _, field := range []string{"template", "chain", "case", "vault", "epoch", "job"} {
			t.Run(kind+"/"+field, func(t *testing.T) {
				f := formalGuardianNode(t, kind)
				request := f.request
				switch field {
				case "template":
					request.TemplateHash = bytes.Repeat([]byte{8}, 32)
				case "chain":
					request.Chain = "unknown-chain"
				case "case":
					request.Chain = "BTC"
				case "vault":
					request.VaultID++
				case "epoch":
					request.Epoch++
				case "job":
					request.JobID = "not-finalized-work"
				}
				if intent, err := ResolveGuardianIntent(f.reader, request); err == nil || intent != nil {
					t.Fatalf("%s supplied an independently signed wrong identity", field)
				}
			})
		}
	}
}

func TestFormalDualCustodyGuardianNodeRejectsBusinessMutation(t *testing.T) {
	for _, kind := range []string{"btc-withdraw", "account-withdraw", "btc-no-funds-migration", "btc-funded-migration", "eth-migration"} {
		for _, field := range []string{"guardian-missing", "template", "business"} {
			t.Run(kind+"/"+field, func(t *testing.T) {
				f := formalGuardianNode(t, kind)
				if field == "guardian-missing" {
					var cfg pb.FrostVaultConfig
					key := keys.KeyFrostVaultConfig(f.request.Chain, 0)
					if err := proto.Unmarshal(f.data[key], &cfg); err != nil {
						t.Fatal(err)
					}
					cfg.GuardianPubkey = nil
					formalGuardianPut(t, f.data, key, &cfg)
				} else if kind == "btc-withdraw" || kind == "account-withdraw" {
					key := keys.KeyFrostWithdrawJob(f.request.JobID)
					var job pb.FrostWithdrawJobState
					if err := proto.Unmarshal(f.data[key], &job); err != nil {
						t.Fatal(err)
					}
					if field == "template" {
						job.TemplateData = []byte(`{}`)
						formalGuardianPut(t, f.data, key, &job)
					} else if kind == "account-withdraw" {
						key = keys.KeyFrostWithdraw(job.WithdrawIds[0])
						var withdrawal pb.FrostWithdrawState
						if err := proto.Unmarshal(f.data[key], &withdrawal); err != nil {
							t.Fatal(err)
						}
						withdrawal.GrossAmount = "999"
						formalGuardianPut(t, f.data, key, &withdrawal)
					} else {
						                                                                
						template, err := btc.FromJSON(job.TemplateData)
						if err != nil || len(template.Inputs) == 0 {
							t.Fatal("fixture lacks canonical input")
						}
						input := template.Inputs[0]
						f.data[keys.KeyFrostBtcLockedUtxo(f.request.VaultID, input.TxID, input.Vout)] = []byte("another-job")
					}
				} else {
					key := keys.KeyFrostVaultMigration(f.request.Chain, f.request.VaultID, f.request.Epoch+1)
					var migration pb.FrostVaultMigrationState
					if err := proto.Unmarshal(f.data[key], &migration); err != nil {
						t.Fatal(err)
					}
					if field == "template" {
						migration.TemplateHash = bytes.Repeat([]byte{9}, 32)
					} else {
						migration.IntentHash = bytes.Repeat([]byte{7}, 32)
					}
					formalGuardianPut(t, f.data, key, &migration)
				}
				if intent, err := ResolveGuardianIntent(f.reader, f.request); err == nil || intent != nil {
					t.Fatalf("%s/%s accepted poisoned finalized business state", kind, field)
				}
			})
		}
	}
}
