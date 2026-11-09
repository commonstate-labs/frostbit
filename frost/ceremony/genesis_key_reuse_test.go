package ceremony

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"dex/config"
	"dex/utils"
)

                                                                              
                                                                                 
func reuseFixture(t *testing.T) (config.FrostConfig, *PublicManifest, *config.PeerManifest) {
	t.Helper()
	read := func(name string) []byte {
		b, err := os.ReadFile("testdata/key-reuse-v1/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	cfg, err := PrepareGenesisKeyReuse(read("source.json"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := StrictDecodeManifest(read("manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var peers config.PeerManifest
	if err := json.Unmarshal(read("peers.json"), &peers); err != nil {
		t.Fatal(err)
	}
	return cfg, m, &peers
}

func TestLegacyKeyReusePreservesFrozenEvidenceButCannotAuthorizeBatchVaults(t *testing.T) {
	cfg, m, peers := reuseFixture(t)
	peerDigest, err := ComputePeerManifestDigest(peers)
	if err != nil {
		t.Fatal(err)
	}
	                                                                          
	                                                                        
	                                                                           
	                                                                             
	if err := ValidateGenesisKeyReuse(cfg, nil, m, nil, peers.ChainID, peerDigest, utils.VerifyECDSASignature); err == nil {
		t.Fatal("legacy single-withdraw Vault proof authorized the batch runtime")
	}
	if err := ValidateCeremonyConfigBinding(cfg, m); err != nil {
		t.Fatal(err)
	}
	validators := make([]GenesisTrustValidator, 0, len(peers.GenesisValidators))
	for _, v := range peers.GenesisValidators {
		pk, err := hex.DecodeString(v.SecpPublicKey)
		if err != nil {
			t.Fatal(err)
		}
		validators = append(validators, GenesisTrustValidator{Address: v.Address, PublicKey: pk, IsMiner: v.IsMiner})
	}
	participants, digests, err := ExpectedGenesisDeploymentTrust(validators, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(participants) != 5 {
		t.Fatalf("lost original participant: %d", len(participants))
	}
	for i, digest := range digests {
		if !bytes.Equal(digest, m.Vaults[i].CommitteeDigest) {
			t.Fatalf("committee changed for %s", m.Vaults[i].Chain)
		}
	}
	if err := ValidateGenesisKeyReuse(cfg, nil, nil, participants, peers.ChainID, peerDigest, utils.VerifyECDSASignature); err == nil {
		t.Fatal("legacy proof authorized the batch runtime through explicit participants")
	}
}

func TestKeyReuseRejectsTamperingAndOldApprovals(t *testing.T) {
	for _, name := range []string{"key", "vault", "threshold", "fee", "old-signature", "old-missing-approval", "unknown-field", "duplicate-field", "old-as-new"} {
		t.Run(name, func(t *testing.T) {
			cfg, m, p := reuseFixture(t)
			var reuse GenesisKeyReuse
			if err := json.Unmarshal(cfg.GenesisKeyReuse, &reuse); err != nil {
				t.Fatal(err)
			}
			var source config.FrostConfig
			if err := json.Unmarshal(reuse.SourceConfig, &source); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "key":
				c := cfg.Chains["eth"]
				c.InitialGroupPubkeys = []string{"changed"}
				cfg.Chains["eth"] = c
			case "vault":
				c := cfg.Chains["eth"]
				c.VaultRefs = []string{"0x1111111111111111111111111111111111111111"}
				cfg.Chains["eth"] = c
			case "threshold":
				cfg.Vault.ThresholdRatio = 1
			case "fee":
				c := cfg.Chains["btc"]
				c.FeeSatsPerVByte++
				cfg.Chains["btc"] = c
			case "old-signature", "old-missing-approval":
				var frozen legacyFrostConfigV1
				_ = json.Unmarshal(reuse.SourceConfig, &frozen)
				proof, err := StrictDecodeGenesisDeploymentManifest(frozen.GenesisDeploymentManifest)
				if err != nil {
					t.Fatal(err)
				}
				if name == "old-signature" {
					proof.Approvals[0].Signature[0] ^= 1
				} else {
					proof.Approvals = proof.Approvals[1:]
				}
				frozen.GenesisDeploymentManifest, _ = json.Marshal(proof)
				reuse.SourceConfig, _ = json.Marshal(frozen)
				cfg.GenesisKeyReuse, _ = json.Marshal(reuse)
			case "unknown-field":
				reuse.SourceConfig = append([]byte(`{"unexpected":1,`), reuse.SourceConfig[1:]...)
				cfg.GenesisKeyReuse, _ = json.Marshal(reuse)
			case "duplicate-field":
				cfg.GenesisKeyReuse = append([]byte(`{"version":1,"version":1,"source_config":`), append(reuse.SourceConfig, '}')...)
			case "old-as-new":
				proof, err := StrictDecodeGenesisDeploymentManifest(source.GenesisDeploymentManifest)
				if err != nil {
					t.Fatal(err)
				}
				if err := ValidateGenesisDeploymentManifest(proof, cfg, m, nil, p.ChainID, m.Plan.PeerManifestDigest, utils.VerifyECDSASignature); err == nil {
					t.Fatal("old approvals authorized new runtime")
				}
				return
			}
			if err := ValidateGenesisKeyReuse(cfg, nil, m, nil, p.ChainID, m.Plan.PeerManifestDigest, utils.VerifyECDSASignature); err == nil {
				t.Fatal("tampered reuse accepted")
			}
		})
	}
}

func TestRawJSONDoesNotRelaxBinaryArtifactEncoding(t *testing.T) {
	var value struct {
		Raw    json.RawMessage `json:"raw"`
		Binary []byte          `json:"binary"`
	}
	if err := strictJSON([]byte(`{"raw":{"a":1},"binary":[1,2]}`), 1024, &value); err == nil {
		t.Fatal("binary array accepted")
	}
	if err := strictJSON([]byte(`{"raw":{"a":1,"a":2},"binary":"AQI="}`), 1024, &value); err == nil {
		t.Fatal("nested duplicate accepted")
	}
}

func TestLegacyKeyReuseFreshApprovalsStillCannotAuthorizeBatchVaults(t *testing.T) {
	_, _, peers := reuseFixture(t)
	raw, err := os.ReadFile("testdata/key-reuse-v1/reused.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.FrostConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfiguredGenesisDeploymentEnvironment(cfg, peers, peers.ChainID, utils.VerifyECDSASignature); err == nil {
		t.Fatal("fresh runtime approvals retained an incompatible single-withdraw Vault")
	}
}
