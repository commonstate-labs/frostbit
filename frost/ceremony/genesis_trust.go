package ceremony

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"dex/config"
	"dex/pb"
	"dex/utils"

	"github.com/btcsuite/btcd/btcec/v2"
)

                                                                            
                                                                             
                                                                               
                   
func ValidateConfiguredGenesisDeploymentEnvironment(cfg config.FrostConfig, peerManifest *config.PeerManifest, networkChainID uint64, verify SignatureVerifier) error {
	if peerManifest == nil || networkChainID == 0 || peerManifest.ChainID != networkChainID {
		return errors.New("genesis deployment proof environment has inconsistent chain identity")
	}
	proof, err := StrictDecodeGenesisDeploymentManifest(cfg.GenesisDeploymentManifest)
	if err != nil {
		return fmt.Errorf("decode signed genesis deployment manifest: %w", err)
	}
	validators := make([]GenesisTrustValidator, len(peerManifest.GenesisValidators))
	for i, validator := range peerManifest.GenesisValidators {
		raw := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(validator.SecpPublicKey), "0x"), "0X")
		publicKey, err := hex.DecodeString(raw)
		if err != nil {
			return fmt.Errorf("decode genesis validator %d identity key: %w", i, err)
		}
		validators[i] = GenesisTrustValidator{Address: strings.TrimSpace(validator.Address), PublicKey: publicKey, IsMiner: validator.IsMiner}
	}
	participants, committeeDigests, err := ExpectedGenesisDeploymentTrust(validators, cfg)
	if err != nil {
		return err
	}
	peerDigest, err := ComputePeerManifestDigest(peerManifest)
	if err != nil {
		return err
	}
	if err := ValidateGenesisDeploymentManifest(proof, cfg, nil, participants, networkChainID, peerDigest, verify); err != nil {
		return err
	}
	if len(proof.Statement.Vaults) != len(committeeDigests) {
		return errors.New("deployment proof vault set does not match deterministic genesis assignment")
	}
	for i := range committeeDigests {
		if !bytes.Equal(proof.Statement.Vaults[i].CommitteeDigest, committeeDigests[i]) {
			return fmt.Errorf("deployment proof committee mismatch for %s/%d", proof.Statement.Vaults[i].Chain, proof.Statement.Vaults[i].VaultID)
		}
	}
	return nil
}

                                                                           
                                                                       
type GenesisTrustValidator struct {
	Address   string
	PublicKey []byte
	IsMiner   bool
}

                                                                             
                                                                                  
                                                                        
func ExpectedGenesisDeploymentTrust(validators []GenesisTrustValidator, cfg config.FrostConfig) ([]Participant, [][]byte, error) {
	if !cfg.Enabled || len(cfg.Chains) != len(genesisCeremonyChainOrder) {
		return nil, nil, errors.New("configured genesis requires the canonical six-chain FROST profile")
	}
	for _, chainName := range genesisCeremonyChainOrder {
		if _, ok := cfg.Chains[chainName]; !ok {
			return nil, nil, fmt.Errorf("configured FROST genesis is missing chain %s", chainName)
		}
	}
	normalized := make([]GenesisTrustValidator, 0, len(validators))
	seen := make(map[string]struct{}, len(validators))
	for _, validator := range validators {
		if !validator.IsMiner {
			return nil, nil, errors.New("all genesis validators must be miners for the standalone ceremony")
		}
		publicKey, err := btcec.ParsePubKey(validator.PublicKey)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid genesis validator identity key: %w", err)
		}
		canonical := publicKey.SerializeCompressed()
		address, err := utils.AddressFromSecp256k1PubKey(canonical)
		if err != nil || address != validator.Address {
			return nil, nil, errors.New("genesis validator identity key does not derive its address")
		}
		if _, duplicate := seen[address]; duplicate {
			return nil, nil, errors.New("duplicate genesis validator address")
		}
		seen[address] = struct{}{}
		normalized = append(normalized, GenesisTrustValidator{Address: address, PublicKey: canonical, IsMiner: true})
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Address < normalized[j].Address })
	if cfg.Committee.TopN < 0 {
		return nil, nil, errors.New("committee.topN must not be negative")
	}
	if cfg.Committee.TopN > 0 && len(normalized) > cfg.Committee.TopN {
		normalized = normalized[:cfg.Committee.TopN]
	}
	k := cfg.Vault.DefaultK
	if cfg.Vault.ThresholdRatio != 0.75 {
		return nil, nil, fmt.Errorf("vault.thresholdRatio must be exactly 0.75, got %v", cfg.Vault.ThresholdRatio)
	}
	if _, err := config.ComputeVaultCommitteeThreshold(k); err != nil {
		return nil, nil, err
	}
	indices := make([]uint64, len(normalized))
	addresses := make([]string, len(normalized))
	for i := range normalized {
		indices[i] = uint64(i)
		addresses[i] = normalized[i].Address
	}
	if len(indices) < k {
		return nil, nil, fmt.Errorf("genesis committee requires the first %d validator indices, but topN provides %d", k, len(indices))
	}
	assignments, err := InitialCommitteeAssignments(cfg, indices, addresses)
	if err != nil {
		return nil, nil, err
	}
	used := make(map[uint64]struct{}, len(normalized))
	digests := make([][]byte, 0)
	for _, chainName := range genesisCeremonyChainOrder {
		chainCfg := cfg.Chains[chainName]
		if chainCfg.VaultsPerChain <= 0 {
			return nil, nil, fmt.Errorf("chain %s must configure at least one genesis vault", chainName)
		}
		algorithm, err := configuredGenesisSignAlgo(chainName, chainCfg.SignAlgo)
		if err != nil {
			return nil, nil, err
		}
		for vaultID := 0; vaultID < chainCfg.VaultsPerChain; vaultID++ {
			assignment := assignments[chainName][vaultID]
			members := make([]string, len(assignment))
			for i, index := range assignment {
				if index >= uint64(len(normalized)) {
					return nil, nil, errors.New("genesis committee assignment references an unknown validator")
				}
				members[i] = normalized[index].Address
				used[index] = struct{}{}
			}
			digest, err := CommitteeDigest(chainName, uint32(vaultID), 1, algorithm, members)
			if err != nil {
				return nil, nil, err
			}
			digests = append(digests, digest)
		}
	}
	participants := make([]Participant, 0, len(used))
	for index, validator := range normalized {
		if _, ok := used[uint64(index)]; ok {
			participants = append(participants, Participant{Index: uint64(index), ID: validator.Address, PublicKey: bytes.Clone(validator.PublicKey)})
		}
	}
	return participants, digests, nil
}

func configuredGenesisSignAlgo(chainName, raw string) (pb.SignAlgo, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "SCHNORR_SECP256K1_BIP340", "SIGN_ALGO_SCHNORR_SECP256K1_BIP340":
		if chainName != "btc" {
			return pb.SignAlgo_SIGN_ALGO_UNSPECIFIED, fmt.Errorf("chain %s cannot use BIP340", chainName)
		}
		return pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, nil
	case "SCHNORR_ALT_BN128", "SIGN_ALGO_SCHNORR_ALT_BN128":
		if chainName == "btc" {
			return pb.SignAlgo_SIGN_ALGO_UNSPECIFIED, errors.New("BTC cannot use BN128")
		}
		return pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128, nil
	default:
		return pb.SignAlgo_SIGN_ALGO_UNSPECIFIED, fmt.Errorf("chain %s has unsupported signing algorithm %q", chainName, raw)
	}
}
