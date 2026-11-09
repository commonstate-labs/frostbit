package ceremony

import (
	"bytes"
	"testing"

	"dex/config"
)

func TestFrostConfigDigestIgnoresOnlyCeremonyResultFields(t *testing.T) {
	cfg := config.DefaultFrostConfig()
	first, err := ComputeFrostConfigDigest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	eth := cfg.Chains["eth"]
	eth.VaultRefs = []string{"0x1111111111111111111111111111111111111111"}
	eth.InitialGroupPubkeys = []string{"abcd"}
	eth.DepositDexChainID = 771
	eth.DepositProtocol = 2
	eth.AuthorityProtocol = 2
	cfg.Chains["eth"] = eth
	withResults, err := ComputeFrostConfigDigest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, withResults) {
		t.Fatal("deployment result fields changed the ceremony config digest")
	}
	eth.ChainID++
	cfg.Chains["eth"] = eth
	changed, err := ComputeFrostConfigDigest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, changed) {
		t.Fatal("external chain ID was not bound by the ceremony config digest")
	}
}

func TestConfiguredFrostDigestIncludesDepositOutputs(t *testing.T) {
	cfg := config.DefaultFrostConfig()
	original, err := ComputeConfiguredFrostConfigDigest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	eth := cfg.Chains["eth"]
	eth.DepositDexChainID = 771
	eth.DepositProtocol = 2
	cfg.Chains["eth"] = eth
	withDeposit, err := ComputeConfiguredFrostConfigDigest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(original, withDeposit) {
		t.Fatal("configured digest ignored deposit outputs")
	}
	eth.DepositDexChainID++
	cfg.Chains["eth"] = eth
	changedDomain, err := ComputeConfiguredFrostConfigDigest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(withDeposit, changedDomain) {
		t.Fatal("configured digest ignored deposit DEX chain ID")
	}
}

func TestPeerManifestDigestBindsNetwork(t *testing.T) {
	manifest := &config.PeerManifest{SchemaVersion: 1, Kind: "public-peer-manifest", ChainID: 771}
	first, err := ComputePeerManifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ChainID++
	second, err := ComputePeerManifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("DEX network chain ID was not bound by peer manifest digest")
	}
}

func TestConfiguredFrostDigestBindsAuthorityCapability(t *testing.T) {
	cfg := config.DefaultFrostConfig()
	first, err := ComputeConfiguredFrostConfigDigest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Chains["eth"]
	c.AuthorityProtocol = 2
	cfg.Chains["eth"] = c
	second, err := ComputeConfiguredFrostConfigDigest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("authority version can be substituted after all-member genesis signing")
	}
}
