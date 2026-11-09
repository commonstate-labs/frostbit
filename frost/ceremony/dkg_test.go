package ceremony

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/json"
	"math/big"
	"testing"

	"dex/frost/core/dkg"
	"dex/frost/security"
	"dex/pb"

	"github.com/btcsuite/btcd/btcec/v2"
)

type ceremonyTestKeys struct {
	Identity   map[string][]byte
	Encryption map[string][]byte
}

func testPlan(t *testing.T) (*Plan, *ceremonyTestKeys) {
	t.Helper()
	plan := &Plan{
		Version: Version, ID: "test-ceremony-1", NetworkChainID: 771,
		PeerManifestDigest: bytes.Repeat([]byte{0x11}, 32),
		FrostConfigDigest:  bytes.Repeat([]byte{0x22}, 32),
		Epoch:              1, Threshold: 3,
	}
	keys := &ceremonyTestKeys{Identity: make(map[string][]byte), Encryption: make(map[string][]byte)}
	for index, id := range []string{"alice", "bob", "carol", "dave"} {
		identity, err := btcec.NewPrivateKey()
		if err != nil {
			t.Fatal(err)
		}
		encryption, err := btcec.NewPrivateKey()
		if err != nil {
			t.Fatal(err)
		}
		keys.Identity[id] = identity.Serialize()
		keys.Encryption[id] = encryption.Serialize()
		plan.Participants = append(plan.Participants, Participant{
			Index: uint64(index), ID: id, PublicKey: identity.PubKey().SerializeCompressed(),
			EncryptionPublicKey: encryption.PubKey().SerializeCompressed(),
		})
	}
	for _, name := range []string{"btc", "eth", "bnb", "base", "arb", "trx"} {
		algorithm := pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128
		if name == "btc" {
			algorithm = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
		}
		plan.Vaults = append(plan.Vaults, Vault{Chain: name, SignAlgo: algorithm, Committee: []string{"carol", "alice", "dave", "bob"}})
	}
	signTestPlanRegistrations(t, plan, keys)
	return plan, keys
}

func signTestPlanRegistrations(t *testing.T, plan *Plan, keys *ceremonyTestKeys) {
	t.Helper()
	contextDigest, err := RegistrationContextDigest(plan)
	if err != nil {
		t.Fatal(err)
	}
	plan.Registrations = nil
	for _, participant := range plan.Participants {
		registration := Registration{
			Version: Version, ContextDigest: bytes.Clone(contextDigest), ParticipantID: participant.ID,
			EncryptionPublicKey: bytes.Clone(participant.EncryptionPublicKey),
		}
		digest, err := RegistrationDigest(&registration)
		if err != nil {
			t.Fatal(err)
		}
		privateKey, _ := btcec.PrivKeyFromBytes(keys.Identity[participant.ID])
		r, s, err := ecdsa.Sign(rand.Reader, privateKey.ToECDSA(), digest)
		privateKey.Zero()
		if err != nil {
			t.Fatal(err)
		}
		if s.Cmp(new(big.Int).Rsh(new(big.Int).Set(btcec.S256().Params().N), 1)) > 0 {
			s.Sub(btcec.S256().Params().N, s)
		}
		registration.Signature = make([]byte, 64)
		r.FillBytes(registration.Signature[:32])
		s.FillBytes(registration.Signature[32:])
		plan.Registrations = append(plan.Registrations, registration)
	}
}

func clone[T any](t *testing.T, source T) T {
	t.Helper()
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func packagesFor(t *testing.T, plan *Plan) []*Round1Package {
	t.Helper()
	var packages []*Round1Package
	for _, participant := range plan.Participants {
		pkg, err := Round1(plan, participant.ID)
		if err != nil {
			t.Fatal(err)
		}
		packages = append(packages, pkg)
	}
	return packages
}

func TestPlanValidation(t *testing.T) {
	plan, _ := testPlan(t)
	if err := ValidatePlan(plan); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*Plan){
		"version":                     func(p *Plan) { p.Version++ },
		"empty id":                    func(p *Plan) { p.ID = "" },
		"whitespace id":               func(p *Plan) { p.ID = " invalid" },
		"epoch":                       func(p *Plan) { p.Epoch = 0 },
		"network chain":               func(p *Plan) { p.NetworkChainID = 0 },
		"peer manifest digest":        func(p *Plan) { p.PeerManifestDigest = nil },
		"frost config digest":         func(p *Plan) { p.FrostConfigDigest = make([]byte, 32) },
		"zero threshold":              func(p *Plan) { p.Threshold = 0 },
		"threshold exceeds committee": func(p *Plan) { p.Threshold = 5 },
		"no participants":             func(p *Plan) { p.Participants = nil },
		"duplicate participant":       func(p *Plan) { p.Participants[1].ID = p.Participants[0].ID },
		"duplicate key":               func(p *Plan) { p.Participants[1].PublicKey = p.Participants[0].PublicKey },
		"invalid key":                 func(p *Plan) { p.Participants[0].PublicKey = make([]byte, 33) },
		"missing encryption key":      func(p *Plan) { p.Participants[0].EncryptionPublicKey = nil },
		"duplicate encryption key":    func(p *Plan) { p.Participants[1].EncryptionPublicKey = p.Participants[0].EncryptionPublicKey },
		"identity reused for ECIES":   func(p *Plan) { p.Participants[0].EncryptionPublicKey = p.Participants[0].PublicKey },
		"reordered source index":      func(p *Plan) { p.Participants[1].Index = p.Participants[0].Index },
		"missing registration":        func(p *Plan) { p.Registrations = p.Registrations[:2] },
		"reordered registration":      func(p *Plan) { p.Registrations[0], p.Registrations[1] = p.Registrations[1], p.Registrations[0] },
		"tampered registration":       func(p *Plan) { p.Registrations[0].Signature[0] ^= 1 },
		"high-S registration": func(p *Plan) {
			s := new(big.Int).SetBytes(p.Registrations[0].Signature[32:])
			s.Sub(btcec.S256().Params().N, s)
			s.FillBytes(p.Registrations[0].Signature[32:])
		},
		"coordinator key substitution": func(p *Plan) {
			p.Participants[1].EncryptionPublicKey = bytes.Clone(p.Participants[2].EncryptionPublicKey)
			p.Registrations[1].EncryptionPublicKey = bytes.Clone(p.Participants[2].EncryptionPublicKey)
		},
		"no vaults":        func(p *Plan) { p.Vaults = nil },
		"duplicate vault":  func(p *Plan) { p.Vaults = append(p.Vaults, p.Vaults[0]) },
		"unknown chain":    func(p *Plan) { p.Vaults[0].Chain = "ETH" },
		"wrong algorithm":  func(p *Plan) { p.Vaults[0].SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128 },
		"duplicate member": func(p *Plan) { p.Vaults[0].Committee[0] = "alice" },
		"unknown member":   func(p *Plan) { p.Vaults[0].Committee[0] = "eve" },
		"empty committee":  func(p *Plan) { p.Vaults[0].Committee = nil },
		"unassigned participant": func(p *Plan) {
			for i := range p.Vaults {
				p.Vaults[i].Committee = []string{"alice", "bob"}
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			copy := clone(t, plan)
			mutate(copy)
			if err := ValidatePlan(copy); err == nil {
				t.Fatal("invalid plan accepted")
			}
		})
	}
	if err := ValidatePlan(nil); err == nil {
		t.Fatal("nil plan accepted")
	}
}

func TestDigestsBindOrderAndExcludeSignature(t *testing.T) {
	plan, _ := testPlan(t)
	_, err := PlanDigest(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []func(*Plan){
		func(p *Plan) { p.ID += "-new" },
		func(p *Plan) { p.Participants[0], p.Participants[1] = p.Participants[1], p.Participants[0] },
		func(p *Plan) { p.Vaults[0], p.Vaults[1] = p.Vaults[1], p.Vaults[0] },
		func(p *Plan) {
			p.Vaults[0].Committee[0], p.Vaults[0].Committee[1] = p.Vaults[0].Committee[1], p.Vaults[0].Committee[0]
		},
	} {
		copy := clone(t, plan)
		mutation(copy)
		if _, err := PlanDigest(copy); err == nil {
			t.Fatal("identity-signed context mutation was accepted")
		}
	}
	pkg, err := Round1(plan, "alice")
	if err != nil {
		t.Fatal(err)
	}
	a, err := Round1PackageDigest(plan, pkg)
	if err != nil {
		t.Fatal(err)
	}
	pkg.Signature = []byte("outer signature")
	b, err := Round1PackageDigest(plan, clone(t, pkg))
	if err != nil || !bytes.Equal(a, b) {
		t.Fatal("signature or JSON roundtrip altered payload digest", err)
	}
	pkg.Vaults[0].Shares[0].Ciphertext[40] ^= 1
	b, err = Round1PackageDigest(plan, pkg)
	if err != nil || bytes.Equal(a, b) {
		t.Fatal("ciphertext mutation not bound", err)
	}
}

func TestAllChainsFinalizeAndThresholdPublicRelation(t *testing.T) {
	plan, keys := testPlan(t)
	packages := packagesFor(t, plan)
	results := make(map[string]*Result)
	for _, participant := range plan.Participants {
		result, err := Finalize(plan, participant.ID, keys.Encryption[participant.ID], clone(t, packages))
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Vaults) != len(plan.Vaults) {
			t.Fatal("missing vault result")
		}
		results[participant.ID] = result
	}
	for vi, vault := range plan.Vaults {
		group, _ := vaultGroup(vault)
		expected := results["alice"].Vaults[vi].RawGroupPubkey
		for _, participant := range plan.Participants {
			result := results[participant.ID]
			if !bytes.Equal(result.Vaults[vi].RawGroupPubkey, expected) || !bytes.Equal(result.TranscriptDigest, results["alice"].TranscriptDigest) {
				t.Fatal("parties disagree on public output")
			}
			if int(result.Vaults[vi].SignerIndex) != memberIndex(vault.Committee, participant.ID) {
				t.Fatal("signer index does not preserve committee order")
			}
		}
		                                                                       
		                                                
		for _, ids := range [][]string{
			{"alice", "bob", "carol"}, {"alice", "bob", "dave"},
			{"alice", "carol", "dave"}, {"bob", "carol", "dave"},
		} {
			indices := []*big.Int{
				big.NewInt(int64(memberIndex(vault.Committee, ids[0]))),
				big.NewInt(int64(memberIndex(vault.Committee, ids[1]))),
				big.NewInt(int64(memberIndex(vault.Committee, ids[2]))),
			}
			weights := dkg.ComputeLagrangeCoefficients(indices, group.Order())
			a := group.ScalarBaseMultBytes(results[ids[0]].Vaults[vi].LocalShare)
			b := group.ScalarBaseMultBytes(results[ids[1]].Vaults[vi].LocalShare)
			c := group.ScalarBaseMultBytes(results[ids[2]].Vaults[vi].LocalShare)
			public := group.Add(
				group.Add(group.ScalarMult(a, weights[0]), group.ScalarMult(b, weights[1])),
				group.ScalarMult(c, weights[2]),
			)
			if !bytes.Equal(group.SerializePoint(public), expected) {
				t.Fatalf("%s threshold shares do not match raw public key", vault.Chain)
			}
		}
	}
	packages[0], packages[2] = packages[2], packages[0]
	again, err := Finalize(plan, "alice", keys.Encryption["alice"], packages)
	if err != nil || !bytes.Equal(again.TranscriptDigest, results["alice"].TranscriptDigest) {
		t.Fatal("input file order altered transcript", err)
	}
}

func TestFinalizeFailsClosedOnTampering(t *testing.T) {
	plan, keys := testPlan(t)
	packages := packagesFor(t, plan)
	tests := map[string]func([]*Round1Package) []*Round1Package{
		"missing package":   func(p []*Round1Package) []*Round1Package { return p[:2] },
		"extra package":     func(p []*Round1Package) []*Round1Package { return append(p, p[0]) },
		"nil package":       func(p []*Round1Package) []*Round1Package { p[0] = nil; return p },
		"duplicate dealer":  func(p []*Round1Package) []*Round1Package { p[1] = p[0]; return p },
		"wrong plan digest": func(p []*Round1Package) []*Round1Package { p[0].PlanDigest[0] ^= 1; return p },
		"missing vault":     func(p []*Round1Package) []*Round1Package { p[0].Vaults = p[0].Vaults[1:]; return p },
		"reordered vault": func(p []*Round1Package) []*Round1Package {
			p[0].Vaults[0], p[0].Vaults[1] = p[0].Vaults[1], p[0].Vaults[0]
			return p
		},
		"missing commitment": func(p []*Round1Package) []*Round1Package {
			p[0].Vaults[0].Commitments = p[0].Vaults[0].Commitments[:1]
			return p
		},
		"invalid commitment":        func(p []*Round1Package) []*Round1Package { p[0].Vaults[0].Commitments[0] = make([]byte, 33); return p },
		"infinite BN128 commitment": func(p []*Round1Package) []*Round1Package { p[0].Vaults[1].Commitments[0] = make([]byte, 64); return p },
		"missing recipient":         func(p []*Round1Package) []*Round1Package { p[0].Vaults[0].Shares = p[0].Vaults[0].Shares[:2]; return p },
		"wrong recipient label":     func(p []*Round1Package) []*Round1Package { p[0].Vaults[0].Shares[1].RecipientID = "bob"; return p },
		"malformed other recipient": func(p []*Round1Package) []*Round1Package { p[0].Vaults[0].Shares[0].Ciphertext = nil; return p },
		"bad ephemeral key":         func(p []*Round1Package) []*Round1Package { p[0].Vaults[0].Shares[0].Ciphertext[0] = 0; return p },
		"ciphertext MAC":            func(p []*Round1Package) []*Round1Package { p[0].Vaults[0].Shares[1].Ciphertext[40] ^= 1; return p },
		"different recipient ciphertext": func(p []*Round1Package) []*Round1Package {
			p[0].Vaults[0].Shares[1].Ciphertext = p[0].Vaults[0].Shares[0].Ciphertext
			return p
		},
		"cross vault ciphertext": func(p []*Round1Package) []*Round1Package {
			p[0].Vaults[1].Shares[1].Ciphertext = p[0].Vaults[2].Shares[1].Ciphertext
			return p
		},
		"cross dealer ciphertext": func(p []*Round1Package) []*Round1Package {
			p[0].Vaults[1].Shares[1].Ciphertext = p[1].Vaults[1].Shares[1].Ciphertext
			return p
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := Finalize(plan, "alice", keys.Encryption["alice"], mutate(clone(t, packages)))
			if err == nil || result != nil {
				t.Fatal("invalid transcript produced a result")
			}
		})
	}
	for _, private := range [][]byte{nil, make([]byte, 32), keys.Encryption["bob"], keys.Identity["alice"], btcec.S256().Params().N.FillBytes(make([]byte, 32))} {
		if result, err := Finalize(plan, "alice", private, packages); err == nil || result != nil {
			t.Fatal("invalid recipient secret accepted")
		}
	}
	if result, err := Finalize(plan, "eve", keys.Encryption["alice"], packages); err == nil || result != nil {
		t.Fatal("unknown recipient accepted")
	}
}

func TestFinalizeRejectsAuthenticatedButFalseShare(t *testing.T) {
	plan, keys := testPlan(t)
	packages := packagesFor(t, plan)
	for _, invalid := range []*big.Int{big.NewInt(0), big.NewInt(1), curveOrder(t, plan.Vaults[1])} {
		mutated := clone(t, packages)
		vault := plan.Vaults[1]
		context, err := shareContext(mutated[0].PlanDigest, vault, "alice", "alice")
		if err != nil {
			t.Fatal(err)
		}
		plain := append(context, invalid.FillBytes(make([]byte, 32))...)
		randomness := big.NewInt(42).FillBytes(make([]byte, 32))
		ciphertext, err := security.ECIESEncrypt(plan.Participants[0].EncryptionPublicKey, plain, randomness)
		if err != nil {
			t.Fatal(err)
		}
		mutated[0].Vaults[1].Shares[1].Ciphertext = ciphertext
		if result, err := Finalize(plan, "alice", keys.Encryption["alice"], mutated); err == nil || result != nil {
			t.Fatal("false but MAC-valid scalar accepted")
		}
	}
}

func curveOrder(t *testing.T, vault Vault) *big.Int {
	t.Helper()
	group, err := vaultGroup(vault)
	if err != nil {
		t.Fatal(err)
	}
	return group.Order()
}

func TestDifferentCommitteesAndIndependentPackages(t *testing.T) {
	plan, keys := testPlan(t)
	identity, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	encryption, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	keys.Identity["erin"] = identity.Serialize()
	keys.Encryption["erin"] = encryption.Serialize()
	identityPublic := identity.PubKey().SerializeCompressed()
	encryptionPublic := encryption.PubKey().SerializeCompressed()
	identity.Zero()
	encryption.Zero()
	plan.Participants = append(plan.Participants, Participant{
		Index: 4, ID: "erin", PublicKey: identityPublic,
		EncryptionPublicKey: encryptionPublic,
	})
	plan.Vaults = plan.Vaults[:2]
	plan.Vaults[0].Committee = []string{"alice", "bob", "carol", "dave"}
	plan.Vaults[1].Committee = []string{"bob", "carol", "dave", "erin"}
	signTestPlanRegistrations(t, plan, keys)
	packages := packagesFor(t, plan)
	if len(packages[0].Vaults) != 1 || len(packages[1].Vaults) != 2 || len(packages[2].Vaults) != 2 || len(packages[3].Vaults) != 2 || len(packages[4].Vaults) != 1 {
		t.Fatal("dealer contributed to unrelated vault")
	}
	for _, participant := range plan.Participants {
		result, err := Finalize(plan, participant.ID, keys.Encryption[participant.ID], packages)
		if err != nil {
			t.Fatal(err)
		}
		want := 1
		if participant.ID == "bob" || participant.ID == "carol" || participant.ID == "dave" {
			want = 2
		}
		if len(result.Vaults) != want {
			t.Fatal("unexpected local vault result")
		}
	}
	a, err := Round1(plan, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.Vaults[0].Commitments[0], packages[0].Vaults[0].Commitments[0]) {
		t.Fatal("dealer randomness was reused")
	}
	if _, err := Round1(plan, "unknown"); err == nil {
		t.Fatal("unknown dealer accepted")
	}
}
