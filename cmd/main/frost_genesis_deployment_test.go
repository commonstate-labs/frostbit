package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"testing"

	"dex/config"
	"dex/frost/ceremony"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/pb"
	"dex/utils"

	"github.com/btcsuite/btcd/btcec/v2"
)

func buildValidSixChainDeploymentProof(t *testing.T, validatorCount int) ([]PeerInfo, config.FrostConfig, uint64, []byte, map[string]*btcec.PrivateKey) {
	t.Helper()
	peers := make([]PeerInfo, 0, validatorCount)
	identities := make(map[string]*btcec.PrivateKey, validatorCount)
	for i := 0; i < validatorCount; i++ {
		privateKey, err := btcec.NewPrivateKey()
		if err != nil {
			t.Fatal(err)
		}
		publicKey := privateKey.PubKey().SerializeCompressed()
		address, err := utils.AddressFromSecp256k1PubKey(publicKey)
		if err != nil {
			t.Fatal(err)
		}
		identities[address] = privateKey
		peers = append(peers, PeerInfo{Address: address, SecpPublicKey: publicKey, IsMiner: true})
	}
	t.Cleanup(func() {
		for _, key := range identities {
			key.Zero()
		}
	})

	secpGroup := curve.NewSecp256k1Group()
	secpKey := secpGroup.SerializePoint(secpGroup.ScalarBaseMult(big.NewInt(1)))
	bnGroup := curve.NewBN256Group()
	bnKey := bnGroup.SerializePoint(bnGroup.ScalarBaseMult(big.NewInt(1)))
	cfg := testDualCustodyFrostConfig()
	cfg.Enabled = true
	cfg.Committee.TopN = validatorCount
	cfg.Vault.DefaultK, cfg.Vault.MinK, cfg.Vault.MaxK = 4, 4, 4
	cfg.Vault.Count = 1
	cfg.Chains = make(map[string]config.ChainFrostConfig, 6)
	chainIDs := map[string]uint64{"arb": 42161, "base": 8453, "bnb": 56, "eth": 1, "trx": 728126428}
	for _, chainName := range ceremony.GenesisCeremonyChainOrder() {
		chainCfg := config.ChainFrostConfig{VaultsPerChain: 1, AuthorityProtocol: 3, DepositProtocol: 2, DepositDexChainID: 771}
		if chainName == "btc" {
			chainCfg.AuthorityProtocol, chainCfg.DepositProtocol, chainCfg.DepositDexChainID = 0, 0, 0
			chainCfg.SignAlgo = "SCHNORR_SECP256K1_BIP340"
			chainCfg.BTCNetwork = "mainnet"
			chainCfg.InitialGroupPubkeys = []string{hex.EncodeToString(secpKey)}
		} else {
			chainCfg.SignAlgo = "SCHNORR_ALT_BN128"
			chainCfg.ChainID = chainIDs[chainName]
			chainCfg.InitialGroupPubkeys = []string{hex.EncodeToString(bnKey)}
			chainCfg.VaultRefs = []string{"0x1111111111111111111111111111111111111111"}
		}
		cfg.Chains[chainName] = chainCfg
	}
	participants, committeeDigests, err := expectedGenesisDeploymentTrust(peers, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(participants) >= len(peers) {
		t.Fatal("test requires at least one unused TopN candidate")
	}
	networkChainID := uint64(771)
	peerDigest := bytes.Repeat([]byte{0x39}, 32)
	configuredDigest, err := ceremony.ComputeConfiguredFrostConfigDigest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	statement := &ceremony.GenesisDeploymentStatement{
		Version: ceremony.GenesisDeploymentVersion, CeremonyManifestDigest: bytes.Repeat([]byte{0x40}, 32),
		PlanDigest: bytes.Repeat([]byte{0x41}, 32), NetworkChainID: networkChainID,
		PeerManifestDigest: peerDigest, Epoch: 1, ConfiguredFrostDigest: configuredDigest,
	}
	for _, participant := range participants {
		statement.Participants = append(statement.Participants, participant.ID)
	}
	for i, chainName := range ceremony.GenesisCeremonyChainOrder() {
		groupKey := bnKey
		algorithm := pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128
		record := ceremony.GenesisDeploymentVault{
			Chain: chainName, VaultID: 0, Epoch: 1, SignAlgo: algorithm,
			CommitteeDigest: committeeDigests[i], RawGroupPubkey: groupKey,
			Kind: ceremony.GenesisDeploymentKindContract,
		}
		if chainName == "btc" {
			record.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
			record.RawGroupPubkey = secpKey
			record.Kind = ceremony.GenesisDeploymentKindDerived
		} else {
			anchor := bytes.Repeat([]byte{byte(0x50 + i)}, 32)
			canonicalAuthority, err := evm.CanonicalBN128PublicKey(groupKey)
			if err != nil {
				t.Fatal(err)
			}
			record.ExternalChainID = cfg.Chains[chainName].ChainID
			record.VaultRef = cfg.Chains[chainName].VaultRefs[0]
			record.Contract = "SchnorrExampleV3"
			record.AuthorityProtocol = 3
			record.DepositProtocol = "DEX_VAULT_DEPOSIT_V1"
			record.DEXChainID = 771
			record.Protocol = "DEX_VAULT_MIXED_WITHDRAW_BATCH_V1"
			record.Deployer = record.VaultRef
			record.CanonicalAuthority = canonicalAuthority
			record.TransactionHash, record.DeploymentBlockHash = bytes.Clone(anchor), bytes.Clone(anchor)
			record.CreationCodeHash, record.RuntimeCodeHash = bytes.Clone(anchor), bytes.Clone(anchor)
			record.ArtifactDigest, record.CompilerProvenanceDigest = bytes.Clone(anchor), bytes.Clone(anchor)
			record.DeploymentBlockNumber, record.FinalityBlockNumber = 100, 100
			record.FinalityBlockHash = bytes.Clone(anchor)
			record.FinalityPolicy, record.MinimumConfirmations = ceremony.GenesisFinalityRPCFinalized, 1
			if chainName == "trx" {
				record.Contract = "SchnorrExampleTronV3"
				record.NonstandardToken = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
				record.FinalityPolicy = ceremony.GenesisFinalityTronSolidity
			}
		}
		record.GuardianPubkey, err = cfg.GuardianPublicKey(record.Chain)
		if err != nil {
			t.Fatal(err)
		}
		statement.Vaults = append(statement.Vaults, record)
	}
	statementDigest, err := ceremony.GenesisDeploymentStatementDigest(statement)
	if err != nil {
		t.Fatal(err)
	}
	approvals := make([]*ceremony.GenesisDeploymentApproval, 0, len(participants))
	for _, participant := range participants {
		approval := &ceremony.GenesisDeploymentApproval{Version: ceremony.GenesisDeploymentVersion, StatementDigest: statementDigest, ParticipantID: participant.ID}
		approvalDigest, err := ceremony.GenesisDeploymentApprovalDigest(approval)
		if err != nil {
			t.Fatal(err)
		}
		approval.Signature = signGenesisImportDigest(t, identities[participant.ID], approvalDigest)
		approvals = append(approvals, approval)
	}
	proof, err := ceremony.AssembleGenesisDeploymentManifest(statement, approvals, participants, utils.VerifyECDSASignature)
	if err != nil {
		t.Fatal(err)
	}
	proofJSON, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	cfg.GenesisDeploymentManifest = proofJSON
	return peers, cfg, networkChainID, peerDigest, identities
}

func TestValidateFrostGenesisDeploymentProofSixChainsAndUnusedCandidate(t *testing.T) {
	peers, cfg, chainID, peerDigest, identities := buildValidSixChainDeploymentProof(t, 25)
	proof, err := validateFrostGenesisDeploymentProof(peers, cfg, chainID, peerDigest)
	if err != nil {
		t.Fatal(err)
	}
	if len(proof.Statement.Participants) >= len(peers) {
		t.Fatal("proof incorrectly included unused TopN candidates")
	}

	shrunk := *proof
	shrunk.Statement = cloneDeploymentStatementForTest(proof.Statement)
	shrunk.Statement.Participants = shrunk.Statement.Participants[:1]
	shrunk.Approvals = shrunk.Approvals[:1]
	shrunk.StatementDigest, err = ceremony.GenesisDeploymentStatementDigest(&shrunk.Statement)
	if err != nil {
		t.Fatal(err)
	}
	shrunk.Approvals[0].StatementDigest = bytes.Clone(shrunk.StatementDigest)
	approvalDigest, err := ceremony.GenesisDeploymentApprovalDigest(&shrunk.Approvals[0])
	if err != nil {
		t.Fatal(err)
	}
	shrunk.Approvals[0].Signature = signGenesisImportDigest(t, identities[shrunk.Approvals[0].ParticipantID], approvalDigest)
	shrunkJSON, _ := json.Marshal(&shrunk)
	tamperedCfg := cfg
	tamperedCfg.GenesisDeploymentManifest = shrunkJSON
	if _, err := validateFrostGenesisDeploymentProof(peers, tamperedCfg, chainID, peerDigest); err == nil {
		t.Fatal("proof-selected signer subset was accepted")
	}
}

func cloneDeploymentStatementForTest(statement ceremony.GenesisDeploymentStatement) ceremony.GenesisDeploymentStatement {
	encoded, _ := json.Marshal(statement)
	var result ceremony.GenesisDeploymentStatement
	_ = json.Unmarshal(encoded, &result)
	return result
}
