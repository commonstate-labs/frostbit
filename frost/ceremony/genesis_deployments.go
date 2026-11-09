package ceremony

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"dex/config"
	"dex/frost/chain/evm"
	"dex/pb"
	"dex/utils"
)

const (
	GenesisDeploymentVersion        uint32 = 1
	GenesisDeploymentKindDerived           = "derived"
	GenesisDeploymentKindContract          = "contract"
	GenesisFinalityRPCFinalized            = "rpc-finalized"
	GenesisFinalityBNBConfirmations        = "bnb-confirmations"
	GenesisFinalityTronSolidity            = "tron-solidity"
	genesisTronUSDTAddress                 = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
)

var genesisCeremonyChainOrder = [...]string{"arb", "base", "bnb", "btc", "eth", "trx"}

                                                                             
                                                                           
                                                                 
func GenesisCeremonyChainOrder() []string {
	return append([]string(nil), genesisCeremonyChainOrder[:]...)
}

                                                                              
                                                                              
                                                                                
type GenesisDeploymentVault struct {
	Chain                    string      `json:"chain"`
	VaultID                  uint32      `json:"vault_id"`
	Epoch                    uint64      `json:"epoch"`
	SignAlgo                 pb.SignAlgo `json:"sign_algo"`
	CommitteeDigest          []byte      `json:"committee_digest"`
	RawGroupPubkey           []byte      `json:"raw_group_pubkey"`
	GuardianPubkey           []byte      `json:"guardian_pubkey"`
	CanonicalAuthority       []byte      `json:"canonical_authority,omitempty"`
	Kind                     string      `json:"kind"`
	ExternalChainID          uint64      `json:"external_chain_id"`
	VaultRef                 string      `json:"vault_ref,omitempty"`
	Contract                 string      `json:"contract,omitempty"`
	Protocol                 string      `json:"protocol,omitempty"`
	AuthorityProtocol        uint32      `json:"authority_protocol,omitempty"`
	DepositProtocol          string      `json:"deposit_protocol,omitempty"`
	DEXChainID               uint64      `json:"dex_chain_id,omitempty"`
	Deployer                 string      `json:"deployer,omitempty"`
	TransactionHash          []byte      `json:"transaction_hash,omitempty"`
	DeploymentBlockNumber    uint64      `json:"deployment_block_number,omitempty"`
	DeploymentBlockHash      []byte      `json:"deployment_block_hash,omitempty"`
	CreationCodeHash         []byte      `json:"creation_code_hash,omitempty"`
	RuntimeCodeHash          []byte      `json:"runtime_code_hash,omitempty"`
	ArtifactDigest           []byte      `json:"artifact_digest,omitempty"`
	CompilerProvenanceDigest []byte      `json:"compiler_provenance_digest,omitempty"`
	NonstandardToken         string      `json:"nonstandard_token,omitempty"`
	FinalityPolicy           string      `json:"finality_policy,omitempty"`
	FinalityBlockNumber      uint64      `json:"finality_block_number,omitempty"`
	FinalityBlockHash        []byte      `json:"finality_block_hash,omitempty"`
	MinimumConfirmations     uint64      `json:"minimum_confirmations,omitempty"`
}

                                                                              
                                                                  
type GenesisDeploymentStatement struct {
	Version                uint32                   `json:"version"`
	CeremonyManifestDigest []byte                   `json:"ceremony_manifest_digest"`
	PlanDigest             []byte                   `json:"plan_digest"`
	NetworkChainID         uint64                   `json:"network_chain_id"`
	PeerManifestDigest     []byte                   `json:"peer_manifest_digest"`
	Epoch                  uint64                   `json:"epoch"`
	ConfiguredFrostDigest  []byte                   `json:"configured_frost_digest"`
	Participants           []string                 `json:"participants"`
	Vaults                 []GenesisDeploymentVault `json:"vaults"`
	DeferTRXVault          bool                     `json:"defer_trx_vault,omitempty"`
}

type GenesisDeploymentApproval struct {
	Version         uint32 `json:"version"`
	StatementDigest []byte `json:"statement_digest"`
	ParticipantID   string `json:"participant_id"`
	Signature       []byte `json:"signature,omitempty"`
}

type SignedGenesisDeploymentManifest struct {
	Version         uint32                      `json:"version"`
	Statement       GenesisDeploymentStatement  `json:"statement"`
	StatementDigest []byte                      `json:"statement_digest"`
	Approvals       []GenesisDeploymentApproval `json:"approvals"`
}

func GenesisDeploymentStatementDigest(statement *GenesisDeploymentStatement) ([]byte, error) {
	if err := validateGenesisDeploymentStatementShape(statement); err != nil {
		return nil, err
	}
	return canonicalDigest("DEX_GENESIS_DEPLOYMENT_STATEMENT_V1", statement)
}

func GenesisDeploymentApprovalDigest(approval *GenesisDeploymentApproval) ([]byte, error) {
	if approval == nil || approval.Version != GenesisDeploymentVersion || !validDigest(approval.StatementDigest) || !validID(approval.ParticipantID) {
		return nil, errors.New("invalid genesis deployment approval")
	}
	if len(approval.Signature) != 0 && len(approval.Signature) != 64 {
		return nil, errors.New("invalid genesis deployment approval signature length")
	}
	unsigned := *approval
	unsigned.StatementDigest = bytes.Clone(approval.StatementDigest)
	unsigned.Signature = nil
	return canonicalDigest("DEX_GENESIS_DEPLOYMENT_APPROVAL_V1", &unsigned)
}

func AssembleGenesisDeploymentManifest(statement *GenesisDeploymentStatement, approvals []*GenesisDeploymentApproval, participants []Participant, verify SignatureVerifier) (*SignedGenesisDeploymentManifest, error) {
	digest, err := GenesisDeploymentStatementDigest(statement)
	if err != nil {
		return nil, err
	}
	if err := validateDeploymentParticipants(statement.Participants, participants); err != nil {
		return nil, err
	}
	if verify == nil || len(approvals) != len(participants) {
		return nil, errors.New("deployment manifest requires exactly one signed approval from every participant")
	}
	byID := make(map[string]*GenesisDeploymentApproval, len(approvals))
	for _, approval := range approvals {
		if approval == nil || !bytes.Equal(approval.StatementDigest, digest) || !IsCanonicalIdentitySignature(approval.Signature) {
			return nil, errors.New("invalid genesis deployment approval")
		}
		participant := participantByIDFrom(participants, approval.ParticipantID)
		approvalDigest, digestErr := GenesisDeploymentApprovalDigest(approval)
		if digestErr != nil || participant == nil || !verify(participant.PublicKey, approvalDigest, approval.Signature) {
			return nil, errors.New("invalid genesis deployment approval signature")
		}
		if byID[approval.ParticipantID] != nil {
			return nil, errors.New("duplicate genesis deployment approval")
		}
		byID[approval.ParticipantID] = approval
	}
	result := &SignedGenesisDeploymentManifest{
		Version: GenesisDeploymentVersion, Statement: cloneGenesisDeploymentStatement(*statement), StatementDigest: bytes.Clone(digest),
		Approvals: make([]GenesisDeploymentApproval, len(participants)),
	}
	for i, participant := range participants {
		approval := byID[participant.ID]
		if approval == nil {
			return nil, fmt.Errorf("missing genesis deployment approval from %s", participant.ID)
		}
		result.Approvals[i] = cloneGenesisDeploymentApproval(*approval)
	}
	return result, nil
}

                                                                            
                                                                              
                                                                              
                                                                       
func ValidateGenesisDeploymentManifest(proof *SignedGenesisDeploymentManifest, cfg config.FrostConfig, manifest *PublicManifest, trustedParticipants []Participant, networkChainID uint64, peerManifestDigest []byte, verify SignatureVerifier) error {
	if len(cfg.GenesisKeyReuse) != 0 {
		if err := ValidateGenesisKeyReuse(cfg, proof, manifest, trustedParticipants, networkChainID, peerManifestDigest, verify); err != nil {
			return err
		}
	}
	digest, err := ComputeConfiguredFrostConfigDigest(cfg)
	if err != nil {
		return err
	}
	return validateGenesisDeploymentWithConfigDigest(proof, cfg, manifest, trustedParticipants, networkChainID, peerManifestDigest, verify, digest)
}

                                                                              
                                                                             
func validateGenesisDeploymentWithConfigDigest(proof *SignedGenesisDeploymentManifest, cfg config.FrostConfig, manifest *PublicManifest, trustedParticipants []Participant, networkChainID uint64, peerManifestDigest []byte, verify SignatureVerifier, configuredDigest []byte) error {
	if proof == nil || proof.Version != GenesisDeploymentVersion {
		return errors.New("missing or unsupported signed genesis deployment manifest")
	}
	participants := trustedParticipants
	if manifest != nil {
		if err := ValidateManifest(manifest, verify); err != nil {
			return fmt.Errorf("validate bound ceremony manifest: %w", err)
		}
		participants = manifest.Plan.Participants
		manifestDigest, err := ManifestDigest(manifest, verify)
		if err != nil || !bytes.Equal(proof.Statement.CeremonyManifestDigest, manifestDigest) {
			return errors.New("deployment statement does not bind the supplied ceremony manifest")
		}
		planDigest, err := PlanDigest(&manifest.Plan)
		if err != nil || !bytes.Equal(proof.Statement.PlanDigest, planDigest) {
			return errors.New("deployment statement plan digest mismatch")
		}
		if err := validateDeploymentVaultsAgainstManifest(proof.Statement.Vaults, manifest); err != nil {
			return err
		}
	}
	if len(participants) == 0 || verify == nil {
		return errors.New("trusted genesis deployment participants are required")
	}
	digest, err := GenesisDeploymentStatementDigest(&proof.Statement)
	if err != nil || !bytes.Equal(proof.StatementDigest, digest) {
		return errors.New("genesis deployment statement digest mismatch")
	}
	assembled, err := AssembleGenesisDeploymentManifest(&proof.Statement, approvalPointers(proof.Approvals), participants, verify)
	if err != nil {
		return err
	}
	want, err := json.Marshal(assembled)
	if err != nil {
		return err
	}
	got, err := json.Marshal(proof)
	if err != nil || !bytes.Equal(got, want) {
		return errors.New("signed genesis deployment manifest is not canonical")
	}
	if !bytes.Equal(proof.Statement.ConfiguredFrostDigest, configuredDigest) {
		return errors.New("signed deployment statement does not match the final FROST config")
	}
	if networkChainID == 0 || proof.Statement.NetworkChainID != networkChainID ||
		!validDigest(peerManifestDigest) || !bytes.Equal(proof.Statement.PeerManifestDigest, peerManifestDigest) {
		return errors.New("signed deployment statement does not match the trusted network environment")
	}
	if err := validateDeploymentParticipants(proof.Statement.Participants, participants); err != nil {
		return err
	}
	if err := validateDeploymentVaultsAgainstConfig(proof.Statement.Vaults, cfg, proof.Statement.Epoch); err != nil {
		return err
	}
	return nil
}

func StrictDecodeGenesisDeploymentApproval(data []byte) (*GenesisDeploymentApproval, error) {
	var approval GenesisDeploymentApproval
	if err := strictJSON(data, maxAttestationJSONSize, &approval); err != nil {
		return nil, err
	}
	return &approval, nil
}

func StrictDecodeGenesisDeploymentManifest(data []byte) (*SignedGenesisDeploymentManifest, error) {
	var proof SignedGenesisDeploymentManifest
	if err := strictJSON(data, maxManifestJSONSize, &proof); err != nil {
		return nil, err
	}
	return &proof, nil
}

func validateGenesisDeploymentStatementShape(statement *GenesisDeploymentStatement) error {
	if statement == nil || statement.Version != GenesisDeploymentVersion || statement.NetworkChainID == 0 || statement.Epoch != 1 ||
		!validDigest(statement.CeremonyManifestDigest) || !validDigest(statement.PlanDigest) || !validDigest(statement.PeerManifestDigest) || !validDigest(statement.ConfiguredFrostDigest) ||
		len(statement.Participants) == 0 || len(statement.Participants) > MaxParticipants || len(statement.Vaults) == 0 {
		return errors.New("invalid genesis deployment statement header")
	}
	seenParticipants := make(map[string]struct{}, len(statement.Participants))
	if err := validateUnifiedGenesisVaults(statement); err != nil {
		return err
	}
	for _, participant := range statement.Participants {
		if !validID(participant) {
			return errors.New("invalid deployment participant identity")
		}
		if _, duplicate := seenParticipants[participant]; duplicate {
			return errors.New("duplicate deployment participant identity")
		}
		seenParticipants[participant] = struct{}{}
	}
	seenVaults := make(map[string]struct{}, len(statement.Vaults))
	for i, vault := range statement.Vaults {
		key := vaultKey(vault.Chain, vault.VaultID)
		if _, duplicate := seenVaults[key]; duplicate {
			return errors.New("duplicate genesis deployment vault")
		}
		seenVaults[key] = struct{}{}
		if vault.Epoch != statement.Epoch || !validDigest(vault.CommitteeDigest) || len(vault.RawGroupPubkey) == 0 {
			return fmt.Errorf("invalid genesis deployment vault %d", i)
		}
		if err := ValidateGroupPublicKey(vault.Chain, vault.SignAlgo, vault.RawGroupPubkey); err != nil {
			return fmt.Errorf("invalid genesis deployment group key for %s: %w", key, err)
		}
		guardianConfig := config.FrostConfig{Guardian: config.GuardianConfig{BTCPublicKey: hex.EncodeToString(vault.GuardianPubkey), AccountPublicKey: hex.EncodeToString(vault.GuardianPubkey)}}
		guardianKey, guardianErr := guardianConfig.GuardianPublicKey(vault.Chain)
		if guardianErr != nil {
			return fmt.Errorf("invalid guardian for %s: %w", key, guardianErr)
		}
		if vault.Chain == "btc" {
			if _, _, _, err := utils.BuildDualCustodyTaproot(vault.RawGroupPubkey, guardianKey); err != nil {
				return err
			}
			if vault.Kind != GenesisDeploymentKindDerived || vault.ExternalChainID != 0 || vault.VaultRef != "" || len(vault.CanonicalAuthority) != 0 || !emptyContractDeployment(vault) {
				return errors.New("BTC genesis vault must be a derived, non-contract result")
			}
			continue
		}
		if vault.Kind != GenesisDeploymentKindContract || vault.ExternalChainID == 0 || vault.VaultRef == "" || vault.Contract == "" || len(vault.CanonicalAuthority) != 64 ||
			vault.Protocol != "DEX_VAULT_MIXED_WITHDRAW_BATCH_V1" || len(vault.TransactionHash) != 32 || allZeroBytes(vault.TransactionHash) || vault.DeploymentBlockNumber == 0 ||
			len(vault.DeploymentBlockHash) != 32 || allZeroBytes(vault.DeploymentBlockHash) || len(vault.CreationCodeHash) != 32 || allZeroBytes(vault.CreationCodeHash) ||
			len(vault.RuntimeCodeHash) != 32 || allZeroBytes(vault.RuntimeCodeHash) || len(vault.ArtifactDigest) != 32 || allZeroBytes(vault.ArtifactDigest) ||
			len(vault.CompilerProvenanceDigest) != 32 || allZeroBytes(vault.CompilerProvenanceDigest) || vault.FinalityBlockNumber < vault.DeploymentBlockNumber ||
			len(vault.FinalityBlockHash) != 32 || allZeroBytes(vault.FinalityBlockHash) || vault.MinimumConfirmations == 0 {
			return fmt.Errorf("incomplete contract deployment proof for %s", key)
		}
		if vault.AuthorityProtocol != config.FrostDualCustodyProtocol || vault.DEXChainID == 0 {
			return fmt.Errorf("dual custody V3 and DEX identity required for %s", key)
		}
		expectedContract := "SchnorrExampleV3"
		if vault.Chain == "trx" {
			expectedContract = "SchnorrExampleTronV3"
		}
		if vault.Contract != expectedContract {
			return fmt.Errorf("contract/authority protocol mismatch for %s", key)
		}
		canonical, err := evm.CanonicalBN128PublicKey(vault.RawGroupPubkey)
		if err != nil || !bytes.Equal(canonical, vault.CanonicalAuthority) || bytes.Equal(canonical, guardianKey) {
			return fmt.Errorf("canonical authority mismatch for %s", key)
		}
		address, err := evm.NormalizeAccountAddress(vault.Chain, vault.VaultRef, false)
		if err != nil || address != vault.VaultRef {
			return fmt.Errorf("noncanonical vault reference for %s", key)
		}
		deployer, err := evm.NormalizeAccountAddress(vault.Chain, vault.Deployer, false)
		if err != nil || deployer != vault.Deployer {
			return fmt.Errorf("noncanonical deployer for %s", key)
		}
		switch vault.Chain {
		case "eth", "base", "arb":
			if vault.FinalityPolicy != GenesisFinalityRPCFinalized {
				return fmt.Errorf("chain %s requires finalized-tag deployment proof", vault.Chain)
			}
			if vault.FinalityBlockNumber != vault.DeploymentBlockNumber || !bytes.Equal(vault.FinalityBlockHash, vault.DeploymentBlockHash) {
				return fmt.Errorf("chain %s finalized anchor must be the deployment block", vault.Chain)
			}
		case "bnb":
			if vault.FinalityPolicy == GenesisFinalityRPCFinalized {
				if vault.FinalityBlockNumber != vault.DeploymentBlockNumber || !bytes.Equal(vault.FinalityBlockHash, vault.DeploymentBlockHash) {
					return errors.New("BNB finalized anchor must be the deployment block")
				}
			} else if vault.FinalityPolicy != GenesisFinalityBNBConfirmations || vault.MinimumConfirmations < 64 {
				return errors.New("BNB deployment proof has an unsafe finality policy")
			} else {
				delta := vault.MinimumConfirmations - 1
				if vault.DeploymentBlockNumber > ^uint64(0)-delta || vault.FinalityBlockNumber != vault.DeploymentBlockNumber+delta {
					return errors.New("BNB confirmation anchor is not the canonical fixed target")
				}
			}
		case "trx":
			if vault.FinalityPolicy != GenesisFinalityTronSolidity || vault.NonstandardToken != genesisTronUSDTAddress {
				return errors.New("TRON deployment proof lacks solidity-node finality or fixed USDT")
			}
			if vault.FinalityBlockNumber != vault.DeploymentBlockNumber || !bytes.Equal(vault.FinalityBlockHash, vault.DeploymentBlockHash) {
				return errors.New("TRON solidity anchor must be the deployment block")
			}
		default:
			return fmt.Errorf("unsupported genesis deployment chain %q", vault.Chain)
		}
	}
	return nil
}

func validateDeploymentVaultsAgainstConfig(vaults []GenesisDeploymentVault, cfg config.FrostConfig, epoch uint64) error {
	expected := 0
	for _, chainName := range genesisCeremonyChainOrder {
		chainCfg, ok := cfg.Chains[chainName]
		if !ok {
			return fmt.Errorf("final FROST config is missing chain %s", chainName)
		}
		groupPubkeys, err := config.DecodeInitialFrostGroupPubkeys(chainName, chainCfg)
		if err != nil || len(groupPubkeys) != chainCfg.VaultsPerChain {
			return fmt.Errorf("final FROST config lacks complete configured result for %s", chainName)
		}
		for vaultID := 0; vaultID < chainCfg.VaultsPerChain; vaultID++ {
			if expected >= len(vaults) {
				return errors.New("signed deployment statement is missing a configured vault")
			}
			vault := vaults[expected]
			guardian, guardianErr := cfg.GuardianPublicKey(chainName)
			if guardianErr != nil || !bytes.Equal(vault.GuardianPubkey, guardian) {
				return fmt.Errorf("signed guardian mismatch for %s/%d", chainName, vaultID)
			}
			expected++
			if vault.Chain != chainName || vault.VaultID != uint32(vaultID) || vault.Epoch != epoch || vault.ExternalChainID != chainCfg.ChainID || !bytes.Equal(vault.RawGroupPubkey, groupPubkeys[vaultID]) {
				return fmt.Errorf("signed deployment statement disagrees with final config for %s/%d", chainName, vaultID)
			}
			if chainName == "btc" {
				if vault.VaultRef != "" {
					return errors.New("BTC deployment statement unexpectedly contains a vault reference")
				}
			} else if vault.VaultRef != chainCfg.VaultRefs[vaultID] {
				return fmt.Errorf("signed vault reference mismatch for %s/%d", chainName, vaultID)
			}
			                                                                      
			if vault.AuthorityProtocol != chainCfg.AuthorityProtocol {
				return fmt.Errorf("signed authority protocol mismatch for %s/%d", chainName, vaultID)
			}
			if chainCfg.DepositProtocol == 2 {
				if vault.DepositProtocol != "DEX_VAULT_DEPOSIT_V1" || vault.DEXChainID == 0 || vault.DEXChainID != chainCfg.DepositDexChainID {
					return fmt.Errorf("signed unified Vault deposit identity mismatch for %s/%d", chainName, vaultID)
				}
			} else if vault.DepositProtocol != "" || vault.DEXChainID != 0 {
				return fmt.Errorf("unified Vault proof has no matching deposit configuration for %s/%d", chainName, vaultID)
			}
		}
	}
	if expected != len(vaults) {
		return errors.New("signed deployment statement contains extra or reordered vaults")
	}
	return nil
}

func validateDeploymentVaultsAgainstManifest(vaults []GenesisDeploymentVault, manifest *PublicManifest) error {
	if manifest == nil || len(vaults) != len(manifest.Vaults) {
		return errors.New("deployment statement vault set does not match ceremony manifest")
	}
	for i := range vaults {
		got, want := vaults[i], manifest.Vaults[i]
		if got.Chain != want.Chain || got.VaultID != want.VaultID || got.Epoch != want.Epoch || got.SignAlgo != want.SignAlgo ||
			!bytes.Equal(got.CommitteeDigest, want.CommitteeDigest) || !bytes.Equal(got.RawGroupPubkey, want.RawGroupPubkey) {
			return fmt.Errorf("deployment statement vault %d does not match ceremony manifest", i)
		}
	}
	return nil
}

func validateDeploymentParticipants(got []string, want []Participant) error {
	if len(got) != len(want) {
		return errors.New("deployment participant set mismatch")
	}
	for i := range want {
		if got[i] != want[i].ID || len(want[i].PublicKey) == 0 {
			return errors.New("deployment participant order or identity mismatch")
		}
	}
	return nil
}

func participantByIDFrom(participants []Participant, id string) *Participant {
	for i := range participants {
		if participants[i].ID == id {
			return &participants[i]
		}
	}
	return nil
}

func approvalPointers(approvals []GenesisDeploymentApproval) []*GenesisDeploymentApproval {
	result := make([]*GenesisDeploymentApproval, len(approvals))
	for i := range approvals {
		result[i] = &approvals[i]
	}
	return result
}

func cloneGenesisDeploymentStatement(statement GenesisDeploymentStatement) GenesisDeploymentStatement {
	result := statement
	result.CeremonyManifestDigest = bytes.Clone(statement.CeremonyManifestDigest)
	result.PlanDigest = bytes.Clone(statement.PlanDigest)
	result.PeerManifestDigest = bytes.Clone(statement.PeerManifestDigest)
	result.ConfiguredFrostDigest = bytes.Clone(statement.ConfiguredFrostDigest)
	result.Participants = append([]string(nil), statement.Participants...)
	result.Vaults = make([]GenesisDeploymentVault, len(statement.Vaults))
	for i, vault := range statement.Vaults {
		result.Vaults[i] = vault
		result.Vaults[i].CommitteeDigest = bytes.Clone(vault.CommitteeDigest)
		result.Vaults[i].RawGroupPubkey = bytes.Clone(vault.RawGroupPubkey)
		result.Vaults[i].GuardianPubkey = bytes.Clone(vault.GuardianPubkey)
		result.Vaults[i].CanonicalAuthority = bytes.Clone(vault.CanonicalAuthority)
		result.Vaults[i].TransactionHash = bytes.Clone(vault.TransactionHash)
		result.Vaults[i].DeploymentBlockHash = bytes.Clone(vault.DeploymentBlockHash)
		result.Vaults[i].CreationCodeHash = bytes.Clone(vault.CreationCodeHash)
		result.Vaults[i].RuntimeCodeHash = bytes.Clone(vault.RuntimeCodeHash)
		result.Vaults[i].ArtifactDigest = bytes.Clone(vault.ArtifactDigest)
		result.Vaults[i].CompilerProvenanceDigest = bytes.Clone(vault.CompilerProvenanceDigest)
		result.Vaults[i].FinalityBlockHash = bytes.Clone(vault.FinalityBlockHash)
	}
	return result
}

func cloneGenesisDeploymentApproval(approval GenesisDeploymentApproval) GenesisDeploymentApproval {
	approval.StatementDigest = bytes.Clone(approval.StatementDigest)
	approval.Signature = bytes.Clone(approval.Signature)
	return approval
}

func emptyContractDeployment(vault GenesisDeploymentVault) bool {
	return vault.AuthorityProtocol == 0 && vault.Contract == "" && vault.Protocol == "" && vault.DepositProtocol == "" && vault.DEXChainID == 0 && vault.Deployer == "" && len(vault.TransactionHash) == 0 && vault.DeploymentBlockNumber == 0 &&
		len(vault.DeploymentBlockHash) == 0 && len(vault.CreationCodeHash) == 0 && len(vault.RuntimeCodeHash) == 0 && len(vault.ArtifactDigest) == 0 &&
		len(vault.CompilerProvenanceDigest) == 0 && vault.NonstandardToken == "" && vault.FinalityPolicy == "" && vault.FinalityBlockNumber == 0 &&
		len(vault.FinalityBlockHash) == 0 && vault.MinimumConfirmations == 0
}

func allZeroBytes(value []byte) bool {
	var aggregate byte
	for _, item := range value {
		aggregate |= item
	}
	return aggregate == 0
}

func decodeConfiguredGroupKey(encoded string) ([]byte, error) {
	raw := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(encoded), "0x"), "0X")
	return hex.DecodeString(raw)
}
