package runtime

import (
	"bytes"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"testing"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/frost/runtime/roast"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"google.golang.org/protobuf/proto"
)

type migrationValidatorReader struct{ signingValidatorCountingReader }

func (r *migrationValidatorReader) Scan(prefix string, fn func(string, []byte) bool) error {
	r.scanCount++
	ks := []string{}
	for k := range r.data {
		if strings.HasPrefix(k, prefix) {
			ks = append(ks, k)
		}
	}
	sort.Strings(ks)
	for _, k := range ks {
		if !fn(k, r.data[k]) {
			break
		}
	}
	return nil
}

type migrationValidatorFixture struct {
	reader   *migrationValidatorReader
	env      *roast.Envelope
	messages [][]byte
	intent   *pb.FrostVaultMigrationState
	tr       *pb.VaultTransitionState
	vault    *pb.FrostVaultState
	cfg      *pb.FrostVaultConfig
}

func (f *migrationValidatorFixture) put(t *testing.T, key string, msg proto.Message) {
	t.Helper()
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	f.reader.data[key] = data
}
func (f *migrationValidatorFixture) save(t *testing.T) {
	f.put(t, keys.KeyFrostVaultMigration(f.intent.Chain, 0, 2), f.intent)
	f.put(t, keys.KeyFrostVaultTransition(f.intent.Chain, 0, 2), f.tr)
	f.put(t, keys.KeyFrostVaultState(f.intent.Chain, 0), f.vault)
	f.put(t, keys.KeyFrostVaultConfig(f.intent.Chain, 0), f.cfg)
}
func newMigrationValidatorFixture(t *testing.T, name string, funded bool) *migrationValidatorFixture {
	t.Helper()
	var grp curve.Group = curve.NewBN256Group()
	algo := chain.SignAlgoETH
	if name == chain.ChainBTC {
		grp = curve.NewSecp256k1Group()
		algo = chain.SignAlgoBTC
	}
	oldPub, newPub := grp.SerializePoint(grp.ScalarBaseMult(big.NewInt(5))), grp.SerializePoint(grp.ScalarBaseMult(big.NewInt(7)))
	if name == chain.ChainBTC {
		oldPub, _ = utils.NormalizeSecp256k1XOnlyPubKey(oldPub)
		newPub, _ = utils.NormalizeSecp256k1XOnlyPubKey(newPub)
	}
	f := &migrationValidatorFixture{reader: &migrationValidatorReader{signingValidatorCountingReader{data: map[string][]byte{}}}}
	f.cfg = &pb.FrostVaultConfig{Chain: name, SignAlgo: algo, VaultCount: 1, BtcNetwork: "mainnet", BtcFeeSatsPerVbyte: 25, EvmChainId: evm.DefaultChainID(name), VaultRefs: []string{"0x1111111111111111111111111111111111111111"}}
	if name == chain.ChainTRX {
		f.cfg.VaultRefs = []string{"TSqQQDnd9KraaGdBDB1kkrPZbLCtyFiPG8"}
	}
	f.intent = &pb.FrostVaultMigrationState{MigrationId: chain.MigrationID(name, 0, 2), Chain: name, VaultId: 0, OldEpoch: 1, NewEpoch: 2, OldGroupPubkey: oldPub, NewGroupPubkey: newPub, Status: "INTENT_FINALIZED", IntentHeight: 100}
	f.env = &roast.Envelope{SessionID: f.intent.MigrationId + "_r3", Chain: name, VaultID: 0, Epoch: 1, SignAlgo: algo}
	if name == chain.ChainBTC {
		f.cfg.GuardianPubkey, _ = utils.NormalizeSecp256k1XOnlyPubKey(grp.SerializePoint(grp.ScalarBaseMult(big.NewInt(31))))
		if funded {
			script, _, _, err := utils.BuildDualCustodyTaproot(oldPub, f.cfg.GuardianPubkey)
			if err != nil {
				t.Fatal(err)
			}
			u := &pb.FrostUtxo{Txid: strings.Repeat("ab", 32), VaultId: 0, Vout: 0, Amount: "100000", ScriptPubkey: script, FinalizeHeight: 10}
			f.put(t, keys.KeyFrostBtcUtxo(0, u.Txid, 0), u)
			f.put(t, keys.KeyFrostBtcSpendableUtxoIndex(0, 1), u)
			canonical, tpl, err := btc.BuildMigrationTemplate(name, 0, 1, 2, []chain.UTXO{{TxID: u.Txid, Vout: 0, Amount: 100000, ScriptPubKey: script, ConfirmHeight: 10}}, newPub, "mainnet", f.cfg.BtcFeeSatsPerVbyte, f.cfg.GuardianPubkey, [][]byte{oldPub})
			if err != nil {
				t.Fatal(err)
			}
			f.intent.TemplateData, f.intent.TemplateHash = canonical.TemplateData, canonical.TemplateHash
			f.messages, err = tpl.ComputeTaprootSighash([][]byte{script}, btc.SighashDefault)
			if err != nil {
				t.Fatal(err)
			}
			f.env.Tweaks = [][]byte{nil}
		} else {
			f.intent.NoFunds = true
			f.intent.TemplateHash = chain.EmptyMigrationTemplateHash()
		}
		f.intent.IntentHash = chain.MigrationIntentHash(name, 0, 1, 2, oldPub, newPub, f.intent.TemplateHash, f.intent.NoFunds)
		f.messages = append(f.messages, f.intent.IntentHash)
		f.env.Tweaks = append(f.env.Tweaks, nil)
	} else {
		f.cfg.AuthorityProtocol = 3
		f.cfg.DepositDexChainId = 51025
		f.cfg.GuardianPubkey = runtimeAccountGuardianKey()
		f.intent.AuthorityProtocol = 3
		f.intent.FrozenNextBatchSeq = "1"
		canonical, _, err := evm.CanonicalMigrationTemplate(f.cfg, f.intent)
		if err != nil {
			t.Fatal(err)
		}
		f.intent.TemplateHash, f.intent.TemplateData, f.intent.IntentHash = canonical.TemplateHash, canonical.TemplateData, canonical.SigHashes[0]
		f.messages = canonical.SigHashes
	}
	f.tr = &pb.VaultTransitionState{Chain: name, VaultId: 0, EpochId: 2, SignAlgo: algo, DkgStatus: "KEY_READY", Lifecycle: "DRAINING", MigrationStatus: "INTENT_FINALIZED", MigrationJobId: f.intent.MigrationId, MigrationTemplateHash: f.intent.TemplateHash, MigrationIntentHash: f.intent.IntentHash, OldGroupPubkey: oldPub, NewGroupPubkey: newPub, OldCommitteeMembers: []string{"alice", "bob", "carol"}, NewCommitteeMembers: []string{"dave", "erin", "frank"}}
	f.vault = &pb.FrostVaultState{Chain: name, VaultId: 0, KeyEpoch: 1, SignAlgo: algo, Status: "ACTIVE", GroupPubkey: oldPub, CommitteeMembers: f.tr.OldCommitteeMembers}
	coordinator, err := chain.WithdrawCoordinator(f.env.SessionID, 1, f.tr.OldCommitteeMembers)
	if err != nil {
		t.Fatal(err)
	}
	f.env.From = roast.NodeID(coordinator)
	f.save(t)
	return f
}

func TestFinalizedMigrationSigningRebuildsAllSupportedChains(t *testing.T) {
	for _, name := range []string{"btc", "eth", "bnb", "base", "arb", "trx"} {
		for _, funded := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_funded_%v", name, funded), func(t *testing.T) {
				f := newMigrationValidatorFixture(t, name, funded)
				if err := newSigningRequestValidator(f.reader)(f.env, f.messages); err != nil {
					t.Fatal(err)
				}
				if name != "btc" && f.reader.scanCount != 0 {
					t.Fatal("account migration must be point-read only")
				}
			})
		}
	}
}

func TestFinalizedMigrationSigningRejectsContextTampering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*migrationValidatorFixture)
	}{
		{"round-format", func(f *migrationValidatorFixture) { f.env.SessionID = f.intent.MigrationId + "_r03" }},
		{"legacy-id", func(f *migrationValidatorFixture) { f.env.SessionID = "migration_" + f.intent.Chain + "_0_2" }},
		{"wrong-coordinator", func(f *migrationValidatorFixture) { f.env.From = "outsider" }},
		{"algorithm", func(f *migrationValidatorFixture) { f.env.SignAlgo = pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1 }},
		{"old-epoch", func(f *migrationValidatorFixture) { f.env.Epoch = 2 }},
		{"unsigned-intent", func(f *migrationValidatorFixture) { f.intent.Status = "PENDING" }},
		{"completed-transition", func(f *migrationValidatorFixture) { f.tr.Lifecycle = "ACTIVE" }},
		{"committee-order", func(f *migrationValidatorFixture) { f.vault.CommitteeMembers = []string{"bob", "alice", "carol"} }},
		{"duplicate-new-member", func(f *migrationValidatorFixture) { f.tr.NewCommitteeMembers = []string{"dave", "dave"} }},
		{"key-changed", func(f *migrationValidatorFixture) {
			f.vault.GroupPubkey = bytes.Repeat([]byte{1}, len(f.vault.GroupPubkey))
		}},
		{"new-key-changed", func(f *migrationValidatorFixture) {
			f.tr.NewGroupPubkey = bytes.Repeat([]byte{2}, len(f.tr.NewGroupPubkey))
		}},
		{"intent-hash", func(f *migrationValidatorFixture) { f.intent.IntentHash = bytes.Repeat([]byte{3}, 32) }},
		{"message", func(f *migrationValidatorFixture) { f.messages = [][]byte{bytes.Repeat([]byte{4}, 32)} }},
		{"tweaks", func(f *migrationValidatorFixture) { f.env.Tweaks = [][]byte{bytes.Repeat([]byte{5}, 32)} }},
	}
	for _, name := range []string{"eth", "trx", "btc"} {
		for _, tc := range tests {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				f := newMigrationValidatorFixture(t, name, true)
				tc.mutate(f)
				f.save(t)
				if err := newSigningRequestValidator(f.reader)(f.env, f.messages); err == nil {
					t.Fatal("tampering accepted")
				}
			})
		}
	}
}

func TestFinalizedMigrationSigningRejectsLocalConfigAndUTXOTampering(t *testing.T) {
	for _, name := range []string{"eth", "trx"} {
		t.Run(name, func(t *testing.T) {
			f := newMigrationValidatorFixture(t, name, false)
			f.cfg.EvmChainId++
			f.save(t)
			if err := newSigningRequestValidator(f.reader)(f.env, f.messages); err == nil {
				t.Fatal("wrong network accepted")
			}
		})
	}
	for _, mutation := range []string{"locked", "missing-primary", "changed-amount", "changed-script", "false-empty"} {
		t.Run(mutation, func(t *testing.T) {
			f := newMigrationValidatorFixture(t, "btc", true)
			key := keys.KeyFrostBtcUtxo(0, strings.Repeat("ab", 32), 0)
			switch mutation {
			case "locked":
				f.reader.data[keys.KeyFrostBtcLockedUtxo(0, strings.Repeat("ab", 32), 0)] = []byte("another-job")
			case "missing-primary":
				delete(f.reader.data, key)
			case "changed-amount", "changed-script":
				var u pb.FrostUtxo
				if err := proto.Unmarshal(f.reader.data[key], &u); err != nil {
					t.Fatal(err)
				}
				if mutation == "changed-amount" {
					u.Amount = "100001"
				} else {
					u.ScriptPubkey = bytes.Repeat([]byte{1}, 34)
				}
				f.put(t, key, &u)
				f.put(t, keys.KeyFrostBtcSpendableUtxoIndex(0, 1), &u)
			case "false-empty":
				f.intent.NoFunds = true
				f.save(t)
			}
			if err := newSigningRequestValidator(f.reader)(f.env, f.messages); err == nil {
				t.Fatal("unsafe BTC migration accepted")
			}
		})
	}
}

func runtimeAccountGuardianKey() []byte {
	g := curve.NewBN256Group()
	key, _ := evm.CanonicalBN128PublicKey(g.SerializePoint(g.ScalarBaseMult(big.NewInt(29))))
	return key
}
