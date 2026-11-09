package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"math/big"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"dex/config"
	"dex/frost/ceremony"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/utils"
)

const tronMainnetUSDTAddress = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"

type deploymentReceipt struct {
	GuardianPX        string          `json:"guardianPx"`
	GuardianPY        string          `json:"guardianPy"`
	AuthorityProtocol uint32          `json:"authorityProtocol,omitempty"`
	SchemaVersion     uint32          `json:"schemaVersion,omitempty"`
	Status            string          `json:"status,omitempty"`
	DepositProtocol   string          `json:"depositProtocol,omitempty"`
	DEXChainID        string          `json:"dexChainId,omitempty"`
	BlockNumber       uint64          `json:"blockNumber,omitempty"`
	MinimumEnergy     string          `json:"minimumEnergy,omitempty"`
	Network           string          `json:"network"`
	ChainID           string          `json:"chainId"`
	VaultID           string          `json:"vaultId"`
	InitialEpoch      string          `json:"initialEpoch"`
	Contract          string          `json:"contract"`
	Address           string          `json:"address"`
	Deployer          string          `json:"deployer"`
	PX                string          `json:"px"`
	PY                string          `json:"py"`
	RawPY             string          `json:"rawPy"`
	Protocol          string          `json:"protocol"`
	CompilerVersion   string          `json:"compilerVersion"`
	CompilerSettings  json.RawMessage `json:"compilerSettings"`
	CreationCodeHash  string          `json:"creationCodeHash"`
	RuntimeCodeHash   string          `json:"runtimeCodeHash"`
	TxHash            string          `json:"txHash"`
	DeployedAt        string          `json:"deployedAt"`
	NonstandardToken  string          `json:"nonstandardToken,omitempty"`
}

type verifiedDeployment struct {
	chain               string
	vaultID             uint32
	address             string
	blockNumber         uint64
	blockHash           string
	finalityPolicy      string
	finalityBlockNumber uint64
	finalityBlockHash   string
	minConfirmations    uint64
	receipt             deploymentReceipt
	dexChainID          uint64                                                                        
}

type preparedGenesisDeployments struct {
	baseConfig  config.FrostConfig
	manifest    *ceremony.PublicManifest
	deployments []verifiedDeployment
	artifacts   map[string]expectedContractArtifact
	finalConfig config.FrostConfig
	statement   *ceremony.GenesisDeploymentStatement
}

func runBuildConfig(args []string) error {
	fs := flag.NewFlagSet("build-config", flag.ContinueOnError)
	var baseConfigPath, peersPath, genesisPath, manifestPath, deploymentsDir, rpcConfigPath, artifactsDir, approvalsDir, out string
	var acceptOffline bool
	fs.StringVar(&baseConfigPath, "base-frost-config", "", "pre-ceremony frost_default.json")
	fs.StringVar(&peersPath, "peers", "", "the same versioned peers.json bound into the ceremony")
	fs.StringVar(&genesisPath, "genesis", "", "public genesis.json (defaults beside peers.json)")
	fs.StringVar(&manifestPath, "manifest", "", "assembled public ceremony manifest")
	fs.StringVar(&deploymentsDir, "deployments", "", "directory containing one strict deployment receipt per account-chain vault")
	fs.StringVar(&rpcConfigPath, "rpc-config", "", "strict JSON {chains:{eth|bnb|base|arb:{jsonRpc,minConfirmations},trx:{jsonRpc,solidityNode,minConfirmations}}}")
	fs.StringVar(&artifactsDir, "solidity-artifacts-dir", "", "trusted Hardhat artifacts root used to match on-chain runtime code")
	fs.StringVar(&approvalsDir, "deployment-approvals-dir", "", "directory containing one independently online-verified deployment approval per participant")
	fs.BoolVar(&acceptOffline, "accept-offline-unverified-deployments", false, "removed unsafe compatibility flag; always rejected and never writes a runnable config")
	fs.StringVar(&out, "out", "", "new final frost_default.json")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if acceptOffline {
		return errors.New("--accept-offline-unverified-deployments is disabled: unverified receipts can never produce a runnable frost_default.json")
	}
	if baseConfigPath == "" || peersPath == "" || manifestPath == "" || deploymentsDir == "" || out == "" {
		return errors.New("build-config requires --base-frost-config, --peers, --manifest, --deployments, and --out")
	}
	if rpcConfigPath == "" || artifactsDir == "" {
		return errors.New("build-config requires online --rpc-config and --solidity-artifacts-dir verification; unverified receipts cannot produce a runnable config")
	}
	if approvalsDir == "" {
		return errors.New("build-config requires --deployment-approvals-dir with one approval from every ceremony participant")
	}
	out, err := checkedNewPath(out)
	if err != nil {
		return err
	}
	prepared, err := prepareGenesisDeployments(baseConfigPath, peersPath, genesisPath, manifestPath, deploymentsDir, rpcConfigPath, artifactsDir)
	if err != nil {
		return err
	}
	approvals, err := loadGenesisDeploymentApprovals(approvalsDir, len(prepared.manifest.Plan.Participants))
	if err != nil {
		return err
	}
	proof, err := ceremony.AssembleGenesisDeploymentManifest(prepared.statement, approvals, prepared.manifest.Plan.Participants, utils.VerifyECDSASignature)
	if err != nil {
		return err
	}
	if err := ceremony.ValidateGenesisDeploymentManifest(
		proof, prepared.finalConfig, prepared.manifest, nil, prepared.manifest.Plan.NetworkChainID,
		prepared.manifest.Plan.PeerManifestDigest, utils.VerifyECDSASignature,
	); err != nil {
		return err
	}
	proofJSON, err := json.Marshal(proof)
	if err != nil {
		return err
	}
	prepared.finalConfig.GenesisDeploymentManifest = proofJSON
	return writeExclusiveJSON(out, &prepared.finalConfig, 0o644)
}

func runAttestDeployments(args []string) error {
	fs := flag.NewFlagSet("attest-deployments", flag.ContinueOnError)
	var baseConfigPath, peersPath, genesisPath, manifestPath, deploymentsDir, rpcConfigPath, artifactsDir, nodeID, out string
	var identityFD int
	fs.StringVar(&baseConfigPath, "base-frost-config", "", "pre-ceremony frost_default.json")
	fs.StringVar(&peersPath, "peers", "", "the same versioned peers.json bound into the ceremony")
	fs.StringVar(&genesisPath, "genesis", "", "public genesis.json (defaults beside peers.json)")
	fs.StringVar(&manifestPath, "manifest", "", "assembled public ceremony manifest")
	fs.StringVar(&deploymentsDir, "deployments", "", "directory containing deployment receipts")
	fs.StringVar(&rpcConfigPath, "rpc-config", "", "strict online RPC/finality configuration")
	fs.StringVar(&artifactsDir, "solidity-artifacts-dir", "", "trusted Hardhat artifacts root")
	fs.StringVar(&nodeID, "node-id", "", "this participant's DEX address")
	fs.IntVar(&identityFD, "identity-key-fd", -1, "inherited descriptor containing this participant's identity private key")
	fs.StringVar(&out, "out", "", "new signed deployment approval JSON")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if baseConfigPath == "" || peersPath == "" || manifestPath == "" || deploymentsDir == "" || rpcConfigPath == "" || artifactsDir == "" || nodeID == "" || identityFD < 3 || out == "" {
		return errors.New("attest-deployments requires all public inputs, --node-id, --identity-key-fd>=3, and --out")
	}
	out, err := checkedNewPath(out)
	if err != nil {
		return err
	}
	prepared, err := prepareGenesisDeployments(baseConfigPath, peersPath, genesisPath, manifestPath, deploymentsDir, rpcConfigPath, artifactsDir)
	if err != nil {
		return err
	}
	identity, rawIdentity, err := readIdentityKey(identityFD)
	if err != nil {
		return err
	}
	defer identity.Zero()
	defer wipe(rawIdentity)
	if err := verifyLocalIdentity(&prepared.manifest.Plan, nodeID, identity); err != nil {
		return err
	}
	statementDigest, err := ceremony.GenesisDeploymentStatementDigest(prepared.statement)
	if err != nil {
		return err
	}
	approval := &ceremony.GenesisDeploymentApproval{
		Version: ceremony.GenesisDeploymentVersion, StatementDigest: statementDigest, ParticipantID: nodeID,
	}
	approvalDigest, err := ceremony.GenesisDeploymentApprovalDigest(approval)
	if err != nil {
		return err
	}
	approval.Signature, err = signIdentityDigest(identity, approvalDigest)
	if err != nil {
		return err
	}
	return writeExclusiveJSON(out, approval, 0o644)
}

func prepareGenesisDeployments(baseConfigPath, peersPath, genesisPath, manifestPath, deploymentsDir, rpcConfigPath, artifactsDir string) (*preparedGenesisDeployments, error) {
	baseConfig, err := loadFrostConfigStrict(baseConfigPath)
	if err != nil {
		return nil, err
	}
	if len(baseConfig.GenesisDeploymentManifest) != 0 {
		return nil, errors.New("base FROST config already contains a genesis deployment manifest")
	}
	if err := requireSixChains(baseConfig.Chains); err != nil {
		return nil, err
	}
	peerManifest, err := loadPeerManifestStrict(peersPath)
	if err != nil {
		return nil, err
	}
	if err := validateFreshGenesisInput(genesisPath, peersPath, baseConfig, peerManifest); err != nil {
		return nil, err
	}
	if _, err := normalizeValidators(peerManifest.GenesisValidators); err != nil {
		return nil, err
	}
	for _, chainName := range ceremonyChains {
		chainConfig := baseConfig.Chains[chainName]
		if len(chainConfig.VaultRefs) != 0 || len(chainConfig.InitialGroupPubkeys) != 0 || chainConfig.DepositDexChainID != 0 || chainConfig.DepositProtocol != 0 {
			return nil, fmt.Errorf("base FROST config chain %s already contains ceremony output fields", chainName)
		}
	}
	manifestData, err := readRegularFile(manifestPath, maxLargeJSONBytes)
	if err != nil {
		return nil, err
	}
	manifest, err := ceremony.StrictDecodeManifest(manifestData)
	if err != nil {
		return nil, err
	}
	if err := validatePlanIdentities(&manifest.Plan); err != nil {
		return nil, err
	}
	if err := ceremony.ValidateManifest(manifest, utils.VerifyECDSASignature); err != nil {
		return nil, fmt.Errorf("validate ceremony manifest: %w", err)
	}
	if manifest.Plan.Epoch != 1 {
		return nil, fmt.Errorf("configured genesis requires ceremony epoch 1, got %d", manifest.Plan.Epoch)
	}
	peerDigest, err := ceremony.ComputePeerManifestDigest(peerManifest)
	if err != nil {
		return nil, err
	}
	if manifest.Plan.NetworkChainID != peerManifest.ChainID || !bytes.Equal(manifest.Plan.PeerManifestDigest, peerDigest) {
		return nil, errors.New("peers manifest or DEX network chain ID does not match the ceremony plan")
	}
	frostDigest, err := ceremony.ComputeFrostConfigDigest(baseConfig)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(frostDigest, manifest.Plan.FrostConfigDigest) {
		return nil, errors.New("base FROST config does not match the config bound into the ceremony plan")
	}
	if err := validatePlanAgainstFrostConfig(&manifest.Plan, baseConfig); err != nil {
		return nil, err
	}
	expectedContext, err := buildCeremonyContext(manifest.Plan.ID, manifest.Plan.Epoch, peerManifest, baseConfig)
	if err != nil {
		return nil, err
	}
	if err := validatePlanAgainstContext(&manifest.Plan, expectedContext); err != nil {
		return nil, err
	}

	deployments, err := loadDeploymentReceipts(deploymentsDir, manifest, baseConfig)
	if err != nil {
		return nil, err
	}
	if err := requireUnifiedVaultReceipts(deployments, manifest.Plan.NetworkChainID); err != nil {
		return nil, err
	}
	rpcEndpoints, err := loadRPCConfig(rpcConfigPath)
	if err != nil {
		return nil, err
	}
	artifacts, err := loadExpectedContractArtifacts(artifactsDir)
	if err != nil {
		return nil, err
	}
	if err := verifyDeploymentsOnline(rpcEndpoints, deployments, baseConfig, manifest.Plan.Epoch, artifacts); err != nil {
		return nil, err
	}
	finalConfig, err := applyManifestAndDeployments(baseConfig, manifest, deployments)
	if err != nil {
		return nil, err
	}
	applyUnifiedVaultDeposits(&finalConfig, deployments)
	statement, err := buildGenesisDeploymentStatement(manifest, finalConfig, deployments, artifacts)
	if err != nil {
		return nil, err
	}
	return &preparedGenesisDeployments{
		baseConfig: baseConfig, manifest: manifest, deployments: deployments, artifacts: artifacts,
		finalConfig: finalConfig, statement: statement,
	}, nil
}

func loadGenesisDeploymentApprovals(directory string, expected int) ([]*ceremony.GenesisDeploymentApproval, error) {
	files, err := listStrictJSONFiles(directory)
	if err != nil {
		return nil, err
	}
	if len(files) != expected {
		return nil, fmt.Errorf("deployment approvals directory contains %d files; ceremony requires %d", len(files), expected)
	}
	approvals := make([]*ceremony.GenesisDeploymentApproval, 0, len(files))
	for _, path := range files {
		data, err := readRegularFile(path, maxSmallJSONBytes)
		if err != nil {
			return nil, err
		}
		approval, err := ceremony.StrictDecodeGenesisDeploymentApproval(data)
		if err != nil {
			return nil, fmt.Errorf("decode deployment approval %s: %w", filepath.Base(path), err)
		}
		approvals = append(approvals, approval)
	}
	return approvals, nil
}

func buildGenesisDeploymentStatement(manifest *ceremony.PublicManifest, finalConfig config.FrostConfig, deployments []verifiedDeployment, artifacts map[string]expectedContractArtifact) (*ceremony.GenesisDeploymentStatement, error) {
	if manifest == nil {
		return nil, errors.New("ceremony manifest is nil")
	}
	manifestDigest, err := ceremony.ManifestDigest(manifest, utils.VerifyECDSASignature)
	if err != nil {
		return nil, err
	}
	planDigest, err := ceremony.PlanDigest(&manifest.Plan)
	if err != nil {
		return nil, err
	}
	configuredDigest, err := ceremony.ComputeConfiguredFrostConfigDigest(finalConfig)
	if err != nil {
		return nil, err
	}
	statement := &ceremony.GenesisDeploymentStatement{
		Version: ceremony.GenesisDeploymentVersion, CeremonyManifestDigest: manifestDigest, PlanDigest: planDigest,
		NetworkChainID: manifest.Plan.NetworkChainID, PeerManifestDigest: bytes.Clone(manifest.Plan.PeerManifestDigest),
		Epoch: manifest.Plan.Epoch, ConfiguredFrostDigest: configuredDigest,
		Participants: make([]string, len(manifest.Plan.Participants)), Vaults: make([]ceremony.GenesisDeploymentVault, 0, len(manifest.Vaults)),
	}
	for i, participant := range manifest.Plan.Participants {
		statement.Participants[i] = participant.ID
	}
	deploymentByVault := make(map[string]verifiedDeployment, len(deployments))
	for _, deployment := range deployments {
		deploymentByVault[fmt.Sprintf("%s/%d", deployment.chain, deployment.vaultID)] = deployment
	}
	for _, vault := range manifest.Vaults {
		guardian, err := finalConfig.GuardianPublicKey(vault.Chain)
		if err != nil {
			return nil, err
		}
		record := ceremony.GenesisDeploymentVault{
			Chain: vault.Chain, VaultID: vault.VaultID, Epoch: vault.Epoch, SignAlgo: vault.SignAlgo,
			CommitteeDigest: bytes.Clone(vault.CommitteeDigest), RawGroupPubkey: bytes.Clone(vault.RawGroupPubkey), GuardianPubkey: guardian,
		}
		if vault.Chain == "btc" {
			record.Kind = ceremony.GenesisDeploymentKindDerived
			statement.Vaults = append(statement.Vaults, record)
			continue
		}
		deployment, ok := deploymentByVault[fmt.Sprintf("%s/%d", vault.Chain, vault.VaultID)]
		if !ok {
			return nil, fmt.Errorf("missing verified deployment for %s/%d", vault.Chain, vault.VaultID)
		}
		artifact, ok := artifacts[deployment.receipt.Contract]
		if !ok {
			return nil, fmt.Errorf("missing trusted artifact for %s", deployment.receipt.Contract)
		}
		canonicalAuthority, err := evm.CanonicalBN128PublicKey(vault.RawGroupPubkey)
		if err != nil {
			return nil, err
		}
		deployer, err := evm.NormalizeAccountAddress(vault.Chain, deployment.receipt.Deployer, false)
		if err != nil {
			return nil, err
		}
		txHash, err := decodeFixedHex(deployment.receipt.TxHash, 32)
		if err != nil {
			return nil, err
		}
		blockHash, err := decodeFixedHex(deployment.blockHash, 32)
		if err != nil {
			return nil, err
		}
		creationHash, err := decodeFixedHex(deployment.receipt.CreationCodeHash, 32)
		if err != nil {
			return nil, err
		}
		runtimeHash, err := decodeFixedHex(deployment.receipt.RuntimeCodeHash, 32)
		if err != nil {
			return nil, err
		}
		finalityHash, err := decodeFixedHex(deployment.finalityBlockHash, 32)
		if err != nil {
			return nil, err
		}
		artifactDigest, err := trustedArtifactDigest(artifact)
		if err != nil {
			return nil, err
		}
		compilerDigest, err := deploymentDomainDigest("DEX_SOLIDITY_COMPILER_PROVENANCE_V1", struct {
			Version  string          `json:"version"`
			Settings json.RawMessage `json:"settings"`
		}{artifact.solcVersion, json.RawMessage(artifact.compilerSettings)})
		if err != nil {
			return nil, err
		}
		record.Kind = ceremony.GenesisDeploymentKindContract
		record.CanonicalAuthority = canonicalAuthority
		record.ExternalChainID = finalConfig.Chains[vault.Chain].ChainID
		record.VaultRef = deployment.address
		record.Contract = deployment.receipt.Contract
		record.Protocol = deployment.receipt.Protocol
		record.AuthorityProtocol = deployment.receipt.AuthorityProtocol
		record.DepositProtocol = deployment.receipt.DepositProtocol
		record.DEXChainID = deployment.dexChainID
		record.Deployer = deployer
		record.TransactionHash = txHash
		record.DeploymentBlockNumber = deployment.blockNumber
		record.DeploymentBlockHash = blockHash
		record.CreationCodeHash = creationHash
		record.RuntimeCodeHash = runtimeHash
		record.ArtifactDigest = artifactDigest
		record.CompilerProvenanceDigest = compilerDigest
		record.NonstandardToken = deployment.receipt.NonstandardToken
		record.FinalityPolicy = deployment.finalityPolicy
		record.FinalityBlockNumber = deployment.finalityBlockNumber
		record.FinalityBlockHash = finalityHash
		record.MinimumConfirmations = deployment.minConfirmations
		statement.Vaults = append(statement.Vaults, record)
	}
	if len(deploymentByVault)+countBTCManifestVaults(manifest) != len(statement.Vaults) {
		return nil, errors.New("verified deployment set contains an extra or duplicate vault")
	}
	if _, err := ceremony.GenesisDeploymentStatementDigest(statement); err != nil {
		return nil, err
	}
	return statement, nil
}

func trustedArtifactDigest(artifact expectedContractArtifact) ([]byte, error) {
	type rangeRecord struct {
		Start  int `json:"start"`
		Length int `json:"length"`
	}
	ranges := make([]rangeRecord, len(artifact.immutableRanges))
	for i, item := range artifact.immutableRanges {
		ranges[i] = rangeRecord{Start: item.start, Length: item.length}
	}
	creationHash := sha256.Sum256(artifact.creationCode)
	runtimeHash := sha256.Sum256(artifact.runtimeCode)
	type namedRangeRecord struct {
		Name   string        `json:"name"`
		Ranges []rangeRecord `json:"ranges"`
	}
	var named []namedRangeRecord
	if artifact.unifiedVault {
		names := make([]string, 0, len(artifact.vaultImmutables))
		for name := range artifact.vaultImmutables {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			item := namedRangeRecord{Name: name}
			for _, slot := range artifact.vaultImmutables[name] {
				item.Ranges = append(item.Ranges, rangeRecord{Start: slot.start, Length: slot.length})
			}
			named = append(named, item)
		}
	}
	return deploymentDomainDigest("DEX_TRUSTED_SOLIDITY_ARTIFACT_V1", struct {
		Contract         string             `json:"contract"`
		CreationSHA256   []byte             `json:"creation_sha256"`
		RuntimeSHA256    []byte             `json:"runtime_sha256"`
		ImmutableRanges  []rangeRecord      `json:"immutable_ranges"`
		CompilerVersion  string             `json:"compiler_version"`
		CompilerSettings json.RawMessage    `json:"compiler_settings"`
		VaultImmutables  []namedRangeRecord `json:"vault_immutables,omitempty"`
	}{artifact.contractName, creationHash[:], runtimeHash[:], ranges, artifact.solcVersion, json.RawMessage(artifact.compilerSettings), named})
}

func deploymentDomainDigest(domain string, value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain + "\x00"))
	_, _ = hash.Write(encoded)
	return hash.Sum(nil), nil
}

func countBTCManifestVaults(manifest *ceremony.PublicManifest) int {
	count := 0
	for _, vault := range manifest.Vaults {
		if vault.Chain == "btc" {
			count++
		}
	}
	return count
}

func validatePlanAgainstContext(plan *ceremony.Plan, context *ceremonyContext) error {
	if plan == nil || context == nil || len(plan.Participants) != len(context.Participants) || len(plan.Vaults) != len(context.Vaults) {
		return errors.New("ceremony plan does not match the deterministic peers/config context")
	}
	for i, participant := range context.Participants {
		if plan.Participants[i].Index != participant.Index || plan.Participants[i].ID != participant.ID || !bytes.Equal(plan.Participants[i].PublicKey, participant.IdentityPublicKey) {
			return fmt.Errorf("ceremony participant %d does not match deterministic genesis selection", i)
		}
	}
	for i, vault := range context.Vaults {
		got := plan.Vaults[i]
		if got.Chain != vault.Chain || got.VaultID != vault.VaultID || got.SignAlgo != vault.SignAlgo || !equalStrings(got.Committee, vault.Committee) {
			return fmt.Errorf("ceremony vault %d does not match deterministic genesis assignment", i)
		}
	}
	return nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func validatePlanAgainstFrostConfig(plan *ceremony.Plan, frostConfig config.FrostConfig) error {
	if plan == nil {
		return errors.New("ceremony plan is nil")
	}
	k := frostConfig.Vault.DefaultK
	wantThreshold, err := configuredCeremonyThreshold(k, frostConfig.Vault.ThresholdRatio)
	if err != nil {
		return err
	}
	if int(plan.Threshold) != wantThreshold {
		return fmt.Errorf("ceremony threshold %d does not match configured threshold %d", plan.Threshold, wantThreshold)
	}
	expectedVaults := 0
	for _, chainName := range ceremonyChains {
		expectedVaults += frostConfig.Chains[chainName].VaultsPerChain
	}
	if len(plan.Vaults) != expectedVaults {
		return fmt.Errorf("ceremony contains %d vaults; FROST config requires %d", len(plan.Vaults), expectedVaults)
	}
	next := 0
	for _, chainName := range ceremonyChains {
		chainConfig := frostConfig.Chains[chainName]
		algorithm, err := ceremonySignAlgo(chainName, chainConfig.SignAlgo)
		if err != nil {
			return err
		}
		for vaultID := 0; vaultID < chainConfig.VaultsPerChain; vaultID++ {
			vault := plan.Vaults[next]
			next++
			if vault.Chain != chainName || vault.VaultID != uint32(vaultID) || vault.SignAlgo != algorithm || len(vault.Committee) != k {
				return fmt.Errorf("ceremony vault at position %d does not match configured %s/%d", next-1, chainName, vaultID)
			}
		}
	}
	return nil
}

func loadDeploymentReceipts(directory string, manifest *ceremony.PublicManifest, frostConfig config.FrostConfig) ([]verifiedDeployment, error) {
	files, err := listStrictJSONFiles(directory)
	if err != nil {
		return nil, err
	}
	expectedCount := 0
	chainByID := make(map[uint64]string, 5)
	for _, chainName := range ceremonyChains {
		if chainName == "btc" {
			continue
		}
		chainConfig := frostConfig.Chains[chainName]
		if chainConfig.ChainID == 0 {
			return nil, fmt.Errorf("chain %s requires a nonzero external chainId", chainName)
		}
		if prior := chainByID[chainConfig.ChainID]; prior != "" {
			return nil, fmt.Errorf("chains %s and %s share external chainId %d", prior, chainName, chainConfig.ChainID)
		}
		chainByID[chainConfig.ChainID] = chainName
		expectedCount += chainConfig.VaultsPerChain
	}
	if len(files) != expectedCount {
		return nil, fmt.Errorf("deployment directory contains %d receipts; account-chain vaults require %d", len(files), expectedCount)
	}
	publicVaults := make(map[string]ceremony.PublicVaultResult, len(manifest.Vaults))
	for _, vault := range manifest.Vaults {
		publicVaults[fmt.Sprintf("%s/%d", vault.Chain, vault.VaultID)] = vault
	}
	seen := make(map[string]struct{}, len(files))
	seenAddresses := make(map[string]struct{}, len(files))
	seenTransactions := make(map[string]struct{}, len(files))
	verified := make([]verifiedDeployment, 0, len(files))
	for _, path := range files {
		data, err := readRegularFile(path, maxSmallJSONBytes)
		if err != nil {
			return nil, err
		}
		var receipt deploymentReceipt
		if err := strictJSON(data, &receipt); err != nil {
			return nil, fmt.Errorf("decode deployment receipt %s: %w", filepath.Base(path), err)
		}
		chainID, err := canonicalUint64(receipt.ChainID, "chainId")
		if err != nil {
			return nil, fmt.Errorf("deployment receipt %s: %w", filepath.Base(path), err)
		}
		chainName := chainByID[chainID]
		if chainName == "" {
			return nil, fmt.Errorf("deployment receipt %s has an unconfigured chainId", filepath.Base(path))
		}
		vaultID64, err := canonicalUint64(receipt.VaultID, "vaultId")
		if err != nil || vaultID64 > math.MaxUint32 {
			return nil, fmt.Errorf("deployment receipt %s has an invalid vaultId", filepath.Base(path))
		}
		vaultID := uint32(vaultID64)
		if int(vaultID) >= frostConfig.Chains[chainName].VaultsPerChain {
			return nil, fmt.Errorf("deployment receipt %s references an out-of-range vault", filepath.Base(path))
		}
		epoch, err := canonicalUint64(receipt.InitialEpoch, "initialEpoch")
		if err != nil || epoch != manifest.Plan.Epoch {
			return nil, fmt.Errorf("deployment receipt %s has an initial epoch different from the ceremony", filepath.Base(path))
		}
		key := fmt.Sprintf("%s/%d", chainName, vaultID)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("duplicate deployment receipt for %s", key)
		}
		publicVault, ok := publicVaults[key]
		if !ok {
			return nil, fmt.Errorf("deployment receipt %s does not map to a ceremony vault", filepath.Base(path))
		}
		address, err := validateDeploymentReceipt(chainName, &receipt, publicVault.RawGroupPubkey)
		if err != nil {
			return nil, fmt.Errorf("deployment receipt %s: %w", filepath.Base(path), err)
		}
		receiptGuardian, guardianErr := receiptGuardianKey(&receipt)
		pinnedGuardian, pinErr := frostConfig.GuardianPublicKey(chainName)
		if guardianErr != nil || pinErr != nil || !bytes.Equal(receiptGuardian, pinnedGuardian) {
			return nil, fmt.Errorf("deployment receipt %s: guardian differs from FROST config", filepath.Base(path))
		}
		dexChainID, err := validateUnifiedReceiptIdentity(&receipt)
		if err != nil || (dexChainID != 0 && dexChainID != manifest.Plan.NetworkChainID) {
			return nil, fmt.Errorf("deployment receipt %s: invalid unified deposit identity or DEX domain: %v", filepath.Base(path), err)
		}
		addressKey := chainName + "/" + strings.ToLower(address)
		if _, duplicate := seenAddresses[addressKey]; duplicate {
			return nil, fmt.Errorf("duplicate deployment address on chain %s", chainName)
		}
		transactionKey := chainName + "/" + strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(receipt.TxHash, "0x"), "0X"))
		if _, duplicate := seenTransactions[transactionKey]; duplicate {
			return nil, fmt.Errorf("duplicate deployment transaction on chain %s", chainName)
		}
		seen[key] = struct{}{}
		seenAddresses[addressKey] = struct{}{}
		seenTransactions[transactionKey] = struct{}{}
		verified = append(verified, verifiedDeployment{chain: chainName, vaultID: vaultID, address: address, receipt: receipt, dexChainID: dexChainID})
	}
	for _, chainName := range ceremonyChains {
		if chainName == "btc" {
			continue
		}
		for vaultID := 0; vaultID < frostConfig.Chains[chainName].VaultsPerChain; vaultID++ {
			key := fmt.Sprintf("%s/%d", chainName, vaultID)
			if _, ok := seen[key]; !ok {
				return nil, fmt.Errorf("missing deployment receipt for %s", key)
			}
		}
	}
	return verified, nil
}

func validateDeploymentReceipt(chainName string, receipt *deploymentReceipt, rawGroupPubkey []byte) (string, error) {
	if receipt == nil || strings.TrimSpace(receipt.Network) == "" {
		return "", errors.New("network is required")
	}
	wantContract := "SchnorrExampleV3"
	if chainName == "trx" {
		wantContract = "SchnorrExampleTronV3"
	}
	if receipt.AuthorityProtocol != config.FrostDualCustodyProtocol || receipt.DepositProtocol != unifiedVaultDepositProtocol {
		return "", errors.New("dual custody V3 with a DEX domain is required")
	}
	guardianKey, err := receiptGuardianKey(receipt)
	if err != nil {
		return "", err
	}
	operatorKey, err := evm.CanonicalBN128PublicKey(rawGroupPubkey)
	if err != nil || bytes.Equal(guardianKey, operatorKey) {
		return "", errors.New("operator and guardian must be distinct valid keys")
	}
	if receipt.Contract != wantContract {
		return "", fmt.Errorf("contract must be %s", wantContract)
	}
	if chainName == "trx" {
		if receipt.NonstandardToken != tronMainnetUSDTAddress {
			return "", errors.New("TRON mainnet nonstandardToken must be the fixed USDT contract")
		}
	} else if receipt.NonstandardToken != "" {
		return "", errors.New("nonstandardToken is only valid for the TRON vault")
	}
	if receipt.Protocol != "DEX_VAULT_MIXED_WITHDRAW_BATCH_V1" {
		return "", errors.New("deployment protocol is not DEX_VAULT_MIXED_WITHDRAW_BATCH_V1")
	}
	address, err := evm.NormalizeAccountAddress(chainName, receipt.Address, false)
	if err != nil {
		return "", fmt.Errorf("invalid contract address: %w", err)
	}
	if _, err := evm.NormalizeAccountAddress(chainName, receipt.Deployer, false); err != nil {
		return "", fmt.Errorf("invalid deployer address: %w", err)
	}
	if strings.TrimSpace(receipt.CompilerVersion) == "" || len(receipt.CompilerSettings) == 0 || bytes.Equal(bytes.TrimSpace(receipt.CompilerSettings), []byte("null")) {
		return "", errors.New("compiler provenance is incomplete")
	}
	if _, err := time.Parse(time.RFC3339, receipt.DeployedAt); err != nil {
		return "", errors.New("deployedAt is not RFC3339")
	}
	for name, value := range map[string]string{
		"creationCodeHash": receipt.CreationCodeHash,
		"runtimeCodeHash":  receipt.RuntimeCodeHash,
		"txHash":           receipt.TxHash,
	} {
		if err := requireNonzeroHash(value); err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
	}
	if len(rawGroupPubkey) != 64 {
		return "", errors.New("ceremony group public key is not a 64-byte BN128 point")
	}
	canonical, err := evm.CanonicalBN128PublicKey(rawGroupPubkey)
	if err != nil {
		return "", fmt.Errorf("invalid ceremony group public key: %w", err)
	}
	x := new(big.Int).SetBytes(rawGroupPubkey[:32])
	rawY := new(big.Int).SetBytes(rawGroupPubkey[32:])
	canonicalY := new(big.Int).SetBytes(canonical[32:])
	defer x.SetInt64(0)
	defer rawY.SetInt64(0)
	defer canonicalY.SetInt64(0)
	receiptX, err := canonicalDecimal(receipt.PX, "px")
	if err != nil {
		return "", err
	}
	defer receiptX.SetInt64(0)
	receiptY, err := canonicalDecimal(receipt.PY, "py")
	if err != nil {
		return "", err
	}
	defer receiptY.SetInt64(0)
	receiptRawY, err := canonicalDecimal(receipt.RawPY, "rawPy")
	if err != nil {
		return "", err
	}
	defer receiptRawY.SetInt64(0)
	field := curve.NewBN256Group().Modulus()
	if receiptX.Cmp(field) >= 0 || receiptY.Cmp(field) >= 0 || receiptRawY.Cmp(field) >= 0 ||
		receiptX.Cmp(x) != 0 || receiptRawY.Cmp(rawY) != 0 || receiptY.Cmp(canonicalY) != 0 {
		return "", errors.New("deployed authority point does not match the ceremony raw key and its canonical low-Y form")
	}
	return address, nil
}

func applyManifestAndDeployments(base config.FrostConfig, manifest *ceremony.PublicManifest, deployments []verifiedDeployment) (config.FrostConfig, error) {
	result := base
	result.Chains = make(map[string]config.ChainFrostConfig, len(base.Chains))
	for chainName, chainConfig := range base.Chains {
		chainConfig.VaultRefs = nil
		chainConfig.InitialGroupPubkeys = nil
		result.Chains[chainName] = chainConfig
	}
	for _, vault := range manifest.Vaults {
		chainConfig, ok := result.Chains[vault.Chain]
		if !ok || int(vault.VaultID) != len(chainConfig.InitialGroupPubkeys) {
			return config.FrostConfig{}, fmt.Errorf("manifest vault order is invalid at %s/%d", vault.Chain, vault.VaultID)
		}
		chainConfig.InitialGroupPubkeys = append(chainConfig.InitialGroupPubkeys, hex.EncodeToString(vault.RawGroupPubkey))
		result.Chains[vault.Chain] = chainConfig
	}
	addresses := make(map[string]map[uint32]string)
	for _, deployment := range deployments {
		if addresses[deployment.chain] == nil {
			addresses[deployment.chain] = make(map[uint32]string)
		}
		addresses[deployment.chain][deployment.vaultID] = deployment.address
	}
	for _, chainName := range ceremonyChains {
		chainConfig := result.Chains[chainName]
		if chainName != "btc" {
			chainConfig.VaultRefs = make([]string, chainConfig.VaultsPerChain)
			for vaultID := range chainConfig.VaultRefs {
				chainConfig.VaultRefs[vaultID] = addresses[chainName][uint32(vaultID)]
			}
		}
		if _, err := config.DecodeInitialFrostGroupPubkeys(chainName, chainConfig); err != nil {
			return config.FrostConfig{}, err
		}
		result.Chains[chainName] = chainConfig
	}
	computed, err := ceremony.ComputeFrostConfigDigest(result)
	if err != nil {
		return config.FrostConfig{}, err
	}
	if !bytes.Equal(computed, manifest.Plan.FrostConfigDigest) {
		return config.FrostConfig{}, errors.New("final FROST config changed ceremony-bound parameters")
	}
	return result, nil
}

func canonicalUint64(value, name string) (uint64, error) {
	if value == "" || strings.TrimSpace(value) != value || (len(value) > 1 && value[0] == '0') {
		return 0, fmt.Errorf("%s is not canonical decimal", name)
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || strconv.FormatUint(parsed, 10) != value {
		return 0, fmt.Errorf("%s is not a decimal uint64", name)
	}
	return parsed, nil
}

func canonicalDecimal(value, name string) (*big.Int, error) {
	if value == "" || strings.TrimSpace(value) != value || (len(value) > 1 && value[0] == '0') {
		return nil, fmt.Errorf("%s is not canonical nonnegative decimal", name)
	}
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok || parsed.Sign() < 0 || parsed.String() != value {
		return nil, fmt.Errorf("%s is not canonical nonnegative decimal", name)
	}
	return parsed, nil
}

func requireNonzeroHash(value string) error {
	raw := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(value), "0x"), "0X")
	if len(raw) != 64 {
		return errors.New("must be a 32-byte hex value")
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil || allZero(decoded) {
		return errors.New("must be a nonzero 32-byte hex value")
	}
	return nil
}

                                                                          
func receiptGuardianKey(receipt *deploymentReceipt) ([]byte, error) {
	if receipt == nil {
		return nil, errors.New("guardian receipt required")
	}
	x, err := canonicalDecimal(receipt.GuardianPX, "guardianPx")
	if err != nil || x.BitLen() > 256 {
		return nil, errors.New("invalid guardianPx")
	}
	y, err := canonicalDecimal(receipt.GuardianPY, "guardianPy")
	if err != nil || y.BitLen() > 256 {
		return nil, errors.New("invalid guardianPy")
	}
	raw := appendUint256(nil, x)
	raw = appendUint256(raw, y)
	canonical, err := evm.CanonicalBN128PublicKey(raw)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, errors.New("invalid or noncanonical guardian key")
	}
	return canonical, nil
}
