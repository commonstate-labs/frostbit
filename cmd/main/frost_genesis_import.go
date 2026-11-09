package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"dex/config"
	"dex/frost/ceremony"
	"dex/frost/runtime/adapters"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

const (
	genesisManifestName    = ceremony.NodeBundleManifestFilename
	maxGenesisManifestSize = int64(32 << 20)
	maxGenesisShareSize    = int64(16 << 10)
)

type frostGenesisVaultReader interface {
	GetFrostVaultState(key string) (*pb.FrostVaultState, error)
	Read(key string) (string, error)
}

type frostGenesisShareStore interface {
	LoadLocalShare(chain string, vaultID uint32, epoch uint64) ([]byte, error)
	SaveLocalShare(chain string, vaultID uint32, epoch uint64, share []byte) error
}

                                                                        
                                                                             
                                                                             
                                                                             
type frostGenesisManifestStore interface {
	Get(key string) ([]byte, error)
	EnqueueSet(key, value string)
	ForceFlush() error
}

type configuredGenesisShareRequirement struct {
	chain   string
	vaultID uint32
	state   *pb.FrostVaultState
}

type openedGenesisShare struct {
	vault   ceremony.PublicVaultResult
	share   []byte
	present bool
}

type plannedGenesisShare struct {
	vault      ceremony.PublicVaultResult
	importable bool
}

func importFrostGenesisShares(node *NodeInstance, cfg *config.Config, store *adapters.DBLocalShareStore, wrapKey []byte) error {
	if node == nil || node.DBManager == nil || store == nil {
		return errors.New("FROST genesis share import dependencies are unavailable")
	}
	return importFrostGenesisSharesFromDirWithFormat(
		strings.TrimSpace(node.FrostGenesisSharesDir), node.Address, node.NetworkChainID,
		node.FrostPeerManifestDigest, cfg, node.DBManager, store, node.DBManager, wrapKey, node.FrostPlaintextGenesisShares,
	)
}

                                                                         
                                                                         
                                                                              
                                                                                
func ensureConfiguredGenesisLocalShares(
	cfg *config.Config,
	nodeID string,
	networkChainID uint64,
	peerManifestDigest []byte,
	vaultReader frostGenesisVaultReader,
	shareStore frostGenesisShareStore,
	manifestStore frostGenesisManifestStore,
) error {
	if cfg == nil || vaultReader == nil || shareStore == nil || manifestStore == nil {
		return errors.New("invalid configured-genesis share preflight dependencies")
	}
	markerPresent, err := loadConfiguredFrostGenesisStateMarker(vaultReader, cfg.Frost)
	if err != nil {
		return fmt.Errorf("load configured-genesis consensus marker: %w", err)
	}
	requirements := make([]configuredGenesisShareRequirement, 0)
	chainNames := make([]string, 0, len(cfg.Frost.Chains))
	for chainName := range cfg.Frost.Chains {
		chainNames = append(chainNames, chainName)
	}
	sort.Strings(chainNames)
	groupPubkeysByChain := make(map[string][][]byte, len(chainNames))
	configuredChains := 0
	for _, chainName := range chainNames {
		chainCfg := cfg.Frost.Chains[chainName]
		groupPubkeys, err := config.DecodeInitialFrostGroupPubkeys(chainName, chainCfg)
		if err != nil {
			return fmt.Errorf("validate configured genesis keys for %s: %w", chainName, err)
		}
		if len(groupPubkeys) != 0 && len(groupPubkeys) != chainCfg.VaultsPerChain {
			return fmt.Errorf("configured genesis vault count mismatch for %s", chainName)
		}
		if len(groupPubkeys) != 0 {
			configuredChains++
		}
		groupPubkeysByChain[chainName] = groupPubkeys
	}
	configuredMode := configuredChains > 0 && configuredChains == len(chainNames)
	if configuredChains > 0 && !configuredMode {
		return errors.New("configured genesis ceremony results must be all-or-none across chains")
	}
	if markerPresent != configuredMode {
		return errors.New("configured-genesis consensus marker and public configuration disagree")
	}
	for _, chainName := range chainNames {
		chainCfg := cfg.Frost.Chains[chainName]
		groupPubkeys := groupPubkeysByChain[chainName]
		for vaultID := 0; vaultID < chainCfg.VaultsPerChain; vaultID++ {
			state, err := vaultReader.GetFrostVaultState(keys.KeyFrostVaultState(chainName, uint32(vaultID)))
			if err != nil {
				return fmt.Errorf("load configured genesis vault %s/%d: %w", chainName, vaultID, err)
			}
			if state == nil || state.Chain != chainName || state.VaultId != uint32(vaultID) || state.KeyEpoch == 0 ||
				state.SignAlgo != parseSignAlgo(chainCfg.SignAlgo) {
				return fmt.Errorf("FROST vault state mismatch for %s/%d", chainName, vaultID)
			}
			if state.Status != "ACTIVE" {
				if len(groupPubkeys) != 0 {
					return fmt.Errorf("configured epoch-1 vault %s/%d is not ACTIVE", chainName, vaultID)
				}
				continue
			}
			if len(state.CommitteeMembers) == 0 {
				return fmt.Errorf("ACTIVE FROST vault %s/%d has no committee", chainName, vaultID)
			}
			if err := ceremony.ValidateGroupPublicKey(state.Chain, state.SignAlgo, state.GroupPubkey); err != nil {
				return fmt.Errorf("ACTIVE FROST vault %s/%d has an invalid group key: %w", chainName, vaultID, err)
			}
			if state.KeyEpoch == 1 && len(groupPubkeys) != 0 && !bytes.Equal(state.GroupPubkey, groupPubkeys[vaultID]) {
				return fmt.Errorf("configured epoch-1 group key mismatch for %s/%d", chainName, vaultID)
			}
			if !containsString(state.CommitteeMembers, nodeID) {
				continue
			}
			if state.KeyEpoch == 1 && len(groupPubkeys) != 0 {
				requirements = append(requirements, configuredGenesisShareRequirement{
					chain: chainName, vaultID: uint32(vaultID), state: state,
				})
				continue
			}
			share, err := shareStore.LoadLocalShare(chainName, uint32(vaultID), state.KeyEpoch)
			if err != nil {
				return fmt.Errorf("load current local share %s/%d epoch %d: %w", chainName, vaultID, state.KeyEpoch, err)
			}
			if err := ceremony.ValidateLocalShare(share, state.SignAlgo); err != nil {
				clear(share)
				return fmt.Errorf("current local share %s/%d epoch %d is unavailable or invalid: %w", chainName, vaultID, state.KeyEpoch, err)
			}
			clear(share)
		}
	}
	if len(requirements) == 0 {
		return nil
	}

	manifest, _, present, err := loadPersistedFrostGenesisManifest(manifestStore)
	if err != nil {
		return fmt.Errorf("load standalone-genesis import receipt: %w", err)
	}
	if !present {
		return errors.New("configured epoch-1 committee share lacks a signed standalone-genesis import receipt; re-run startup once with the node ceremony bundle")
	}
	if err := validateFrostGenesisManifestEnvironment(manifest, cfg, networkChainID, peerManifestDigest, vaultReader); err != nil {
		return fmt.Errorf("validate standalone-genesis import receipt environment: %w", err)
	}
	if err := validateManifestIdentityAddresses(manifest); err != nil {
		return fmt.Errorf("validate standalone-genesis import receipt identities: %w", err)
	}
	if manifestParticipant(manifest, nodeID) == nil {
		return fmt.Errorf("local node %s is not a participant in its standalone-genesis import receipt", nodeID)
	}
	for _, requirement := range requirements {
		vault := manifestVault(manifest, requirement.chain, requirement.vaultID)
		if vault == nil {
			return fmt.Errorf("standalone-genesis import receipt lacks vault %s/%d", requirement.chain, requirement.vaultID)
		}
		stillGenesis, err := matchManifestVaultState(*vault, requirement.state)
		if err != nil {
			return err
		}
		if !stillGenesis || !containsString(vault.Committee, nodeID) {
			return fmt.Errorf("standalone-genesis import receipt does not bind node %s to current vault %s/%d", nodeID, requirement.chain, requirement.vaultID)
		}
		verificationShare, err := manifestVerificationShareForNode(manifest, nodeID, requirement.chain, requirement.vaultID)
		if err != nil {
			return err
		}
		share, err := shareStore.LoadLocalShare(requirement.chain, requirement.vaultID, requirement.state.KeyEpoch)
		if err != nil {
			clear(verificationShare)
			return fmt.Errorf("load current local share %s/%d epoch %d: %w", requirement.chain, requirement.vaultID, requirement.state.KeyEpoch, err)
		}
		if err := ceremony.ValidateLocalShare(share, requirement.state.SignAlgo); err != nil {
			clear(share)
			clear(verificationShare)
			return fmt.Errorf("current local share %s/%d epoch %d is unavailable or invalid: %w", requirement.chain, requirement.vaultID, requirement.state.KeyEpoch, err)
		}
		verifyErr := ceremony.VerifyLocalShareAgainstPoint(
			requirement.chain, requirement.state.SignAlgo, share, verificationShare,
		)
		clear(share)
		clear(verificationShare)
		if verifyErr != nil {
			return fmt.Errorf("current local share %s/%d epoch %d disagrees with the signed standalone-genesis receipt: %w", requirement.chain, requirement.vaultID, requirement.state.KeyEpoch, verifyErr)
		}
	}
	return nil
}

                                                                                
                                                                                
                                                                           
func importFrostGenesisSharesFromDir(
	directory string,
	nodeID string,
	networkChainID uint64,
	peerManifestDigest []byte,
	cfg *config.Config,
	vaultReader frostGenesisVaultReader,
	shareStore frostGenesisShareStore,
	manifestStore frostGenesisManifestStore,
	wrapKey []byte,
) error {
	return importFrostGenesisSharesFromDirWithFormat(
		directory, nodeID, networkChainID, peerManifestDigest, cfg,
		vaultReader, shareStore, manifestStore, wrapKey, false,
	)
}

func importFrostGenesisSharesFromDirWithFormat(
	directory string,
	nodeID string,
	networkChainID uint64,
	peerManifestDigest []byte,
	cfg *config.Config,
	vaultReader frostGenesisVaultReader,
	shareStore frostGenesisShareStore,
	manifestStore frostGenesisManifestStore,
	wrapKey []byte,
	plaintext bool,
) error {
	if strings.TrimSpace(directory) == "" {
		return nil
	}
	if cfg == nil || vaultReader == nil || shareStore == nil || manifestStore == nil {
		return errors.New("invalid FROST genesis share import dependencies")
	}
	if plaintext {
		if networkChainID != ceremony.PlaintextGenesisChainID || len(wrapKey) != 0 {
			return errors.New("plaintext genesis share import requires DEX chain 51025 and no wrap key")
		}
	} else if len(wrapKey) != 32 {
		return errors.New("sealed genesis share import requires a 32-byte wrap key")
	}
	absDirectory, err := validateGenesisShareDirectory(directory)
	if err != nil {
		return err
	}
	manifestData, err := readGenesisArtifact(absDirectory, genesisManifestName, maxGenesisManifestSize)
	if err != nil {
		return fmt.Errorf("read FROST genesis manifest: %w", err)
	}
	manifest, err := ceremony.StrictDecodeManifest(manifestData)
	clear(manifestData)
	if err != nil {
		return fmt.Errorf("decode FROST genesis manifest: %w", err)
	}
	if err := ceremony.ValidateManifest(manifest, utils.VerifyECDSASignature); err != nil {
		return fmt.Errorf("verify FROST genesis manifest: %w", err)
	}
	if err := validateFrostGenesisManifestEnvironment(manifest, cfg, networkChainID, peerManifestDigest, vaultReader); err != nil {
		return err
	}
	if err := validateManifestIdentityAddresses(manifest); err != nil {
		return err
	}
	participant := manifestParticipant(manifest, nodeID)
	if participant == nil {
		return fmt.Errorf("local node %s is not a ceremony participant", nodeID)
	}
	canonicalManifest, err := canonicalFrostGenesisManifest(manifest)
	if err != nil {
		return fmt.Errorf("canonicalize FROST genesis manifest: %w", err)
	}
	_, existingManifest, markerPresent, err := loadPersistedFrostGenesisManifest(manifestStore)
	if err != nil {
		return fmt.Errorf("load existing standalone-genesis import receipt: %w", err)
	}
	if markerPresent && !bytes.Equal(existingManifest, canonicalManifest) {
		return errors.New("existing standalone-genesis import receipt conflicts with the supplied ceremony manifest")
	}

	expectedFiles := map[string]struct{}{genesisManifestName: {}}
	localVaults := make([]plannedGenesisShare, 0)
	for _, vault := range manifest.Vaults {
		state, err := vaultReader.GetFrostVaultState(keys.KeyFrostVaultState(vault.Chain, vault.VaultID))
		if err != nil {
			return fmt.Errorf("load configured vault %s/%d: %w", vault.Chain, vault.VaultID, err)
		}
		stillGenesis, err := matchManifestVaultState(vault, state)
		if err != nil {
			return err
		}
		if containsString(vault.Committee, nodeID) {
			name := genesisShareFilename(vault.Chain, vault.VaultID)
			expectedFiles[name] = struct{}{}
			localVaults = append(localVaults, plannedGenesisShare{vault: vault, importable: stillGenesis})
		}
	}
	if err := validateGenesisDirectoryEntries(absDirectory, expectedFiles); err != nil {
		return err
	}

	opened := make([]openedGenesisShare, 0, len(localVaults))
	defer func() {
		for i := range opened {
			clear(opened[i].share)
		}
	}()
	ceremonyDigest := hex.EncodeToString(manifest.TranscriptDigest)
	for _, planned := range localVaults {
		vault := planned.vault
		artifact, err := readGenesisArtifact(absDirectory, genesisShareFilename(vault.Chain, vault.VaultID), maxGenesisShareSize)
		if err != nil {
			return fmt.Errorf("read FROST genesis share %s/%d: %w", vault.Chain, vault.VaultID, err)
		}
		verificationShare, err := manifestVerificationShareForNode(manifest, nodeID, vault.Chain, vault.VaultID)
		if err != nil {
			clear(artifact)
			return err
		}
		expected := ceremony.ShareMetadata{
			CeremonyDigest:    ceremonyDigest,
			NodeID:            nodeID,
			Chain:             vault.Chain,
			VaultID:           vault.VaultID,
			Epoch:             vault.Epoch,
			SignAlgo:          vault.SignAlgo,
			CommitteeDigest:   hex.EncodeToString(vault.CommitteeDigest),
			GroupPubkey:       bytes.Clone(vault.RawGroupPubkey),
			VerificationShare: verificationShare,
		}
		var share []byte
		var openErr error
		if plaintext {
			share, openErr = ceremony.OpenPlaintextGenesisShare(artifact, expected)
		} else {
			share, openErr = ceremony.OpenShare(artifact, expected, wrapKey)
		}
		clear(artifact)
		clear(expected.GroupPubkey)
		clear(expected.VerificationShare)
		if openErr != nil {
			return fmt.Errorf("open FROST genesis share %s/%d: %w", vault.Chain, vault.VaultID, openErr)
		}
		if !planned.importable {
			clear(share)
			continue
		}
		existing, err := shareStore.LoadLocalShare(vault.Chain, vault.VaultID, vault.Epoch)
		if err != nil {
			clear(share)
			return fmt.Errorf("load existing FROST genesis share %s/%d: %w", vault.Chain, vault.VaultID, err)
		}
		present := len(existing) > 0
		if present && (len(existing) != len(share) || subtle.ConstantTimeCompare(existing, share) != 1) {
			clear(existing)
			clear(share)
			return fmt.Errorf("existing FROST share conflicts with ceremony for %s/%d epoch %d", vault.Chain, vault.VaultID, vault.Epoch)
		}
		clear(existing)
		opened = append(opened, openedGenesisShare{vault: vault, share: share, present: present})
	}

	for i := range opened {
		item := &opened[i]
		if item.present {
			continue
		}
		if err := shareStore.SaveLocalShare(item.vault.Chain, item.vault.VaultID, item.vault.Epoch, item.share); err != nil {
			return fmt.Errorf("persist FROST genesis share %s/%d: %w", item.vault.Chain, item.vault.VaultID, err)
		}
	}
	                                                                         
	                                                                       
	                                                                            
	                                                                             
	for i := range opened {
		item := &opened[i]
		persisted, err := shareStore.LoadLocalShare(item.vault.Chain, item.vault.VaultID, item.vault.Epoch)
		if err != nil {
			return fmt.Errorf("reload imported FROST genesis share %s/%d: %w", item.vault.Chain, item.vault.VaultID, err)
		}
		matches := len(persisted) == len(item.share) && subtle.ConstantTimeCompare(persisted, item.share) == 1
		clear(persisted)
		if !matches {
			return fmt.Errorf("durable FROST genesis share verification failed for %s/%d epoch %d", item.vault.Chain, item.vault.VaultID, item.vault.Epoch)
		}
	}
	if !markerPresent {
		if err := persistFrostGenesisManifest(manifestStore, canonicalManifest); err != nil {
			return fmt.Errorf("persist standalone-genesis import receipt: %w", err)
		}
	}
	return nil
}

func canonicalFrostGenesisManifest(manifest *ceremony.PublicManifest) ([]byte, error) {
	if err := ceremony.ValidateManifest(manifest, utils.VerifyECDSASignature); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if len(encoded) == 0 || int64(len(encoded)) > maxGenesisManifestSize {
		clear(encoded)
		return nil, errors.New("canonical standalone-genesis manifest exceeds its size limit")
	}
	return encoded, nil
}

                                                                          
                                                                              
                                                                         
                                                                       
func loadPersistedFrostGenesisManifest(store frostGenesisManifestStore) (*ceremony.PublicManifest, []byte, bool, error) {
	if store == nil {
		return nil, nil, false, errors.New("standalone-genesis manifest store is unavailable")
	}
	raw, err := store.Get(keys.KeyFrostGenesisImportManifest())
	if err != nil {
		if isNotFoundError(err) {
			return nil, nil, false, nil
		}
		return nil, nil, false, err
	}
	if len(raw) == 0 || int64(len(raw)) > maxGenesisManifestSize {
		clear(raw)
		return nil, nil, false, errors.New("persisted standalone-genesis manifest is empty or exceeds its size limit")
	}
	manifest, err := ceremony.StrictDecodeManifest(raw)
	if err != nil {
		clear(raw)
		return nil, nil, false, fmt.Errorf("decode persisted standalone-genesis manifest: %w", err)
	}
	canonical, err := canonicalFrostGenesisManifest(manifest)
	if err != nil {
		clear(raw)
		return nil, nil, false, fmt.Errorf("verify persisted standalone-genesis manifest: %w", err)
	}
	if !bytes.Equal(raw, canonical) {
		clear(raw)
		clear(canonical)
		return nil, nil, false, errors.New("persisted standalone-genesis manifest is not canonical")
	}
	clear(raw)
	return manifest, canonical, true, nil
}

func persistFrostGenesisManifest(store frostGenesisManifestStore, canonical []byte) error {
	if store == nil || len(canonical) == 0 || int64(len(canonical)) > maxGenesisManifestSize {
		return errors.New("invalid standalone-genesis manifest persistence request")
	}
	store.EnqueueSet(keys.KeyFrostGenesisImportManifest(), string(canonical))
	if err := store.ForceFlush(); err != nil {
		return err
	}
	readback, err := store.Get(keys.KeyFrostGenesisImportManifest())
	if err != nil {
		return err
	}
	matches := bytes.Equal(readback, canonical)
	clear(readback)
	if !matches {
		return errors.New("standalone-genesis import receipt failed durable readback")
	}
	return nil
}

func validateManifestIdentityAddresses(manifest *ceremony.PublicManifest) error {
	for _, participant := range manifest.Plan.Participants {
		address, err := utils.AddressFromSecp256k1PubKey(participant.PublicKey)
		if err != nil || address != participant.ID {
			return fmt.Errorf("ceremony participant %q public key does not derive its DEX address", participant.ID)
		}
	}
	return nil
}

func validateFrostGenesisManifestEnvironment(
	manifest *ceremony.PublicManifest,
	cfg *config.Config,
	networkChainID uint64,
	peerManifestDigest []byte,
	vaultReader frostGenesisVaultReader,
) error {
	allowLegacyBTCFee, err := pendingLegacyBTCFeeCompatibility(vaultReader, networkChainID)
	if err != nil {
		return fmt.Errorf("validate protocol upgrade compatibility: %w", err)
	}
	markerPresent, err := loadConfiguredFrostGenesisStateMarker(vaultReader, cfg.Frost)
	if err != nil {
		return fmt.Errorf("load configured-genesis consensus marker: %w", err)
	}
	if !markerPresent {
		return errors.New("standalone genesis ceremony import requires the configured-genesis consensus marker")
	}
	proof, err := ceremony.StrictDecodeGenesisDeploymentManifest(cfg.Frost.GenesisDeploymentManifest)
	if err != nil {
		return fmt.Errorf("decode signed genesis deployment manifest: %w", err)
	}
	if err := ceremony.ValidateGenesisDeploymentManifest(
		proof, cfg.Frost, manifest, nil, networkChainID, peerManifestDigest, utils.VerifyECDSASignature,
	); err != nil {
		return fmt.Errorf("validate ceremony-bound genesis deployment manifest: %w", err)
	}
	if networkChainID == 0 || manifest.Plan.NetworkChainID != networkChainID ||
		len(peerManifestDigest) != 32 || !bytes.Equal(manifest.Plan.PeerManifestDigest, peerManifestDigest) {
		return errors.New("ceremony plan does not match the trusted network chain or peer manifest")
	}
	if manifest.Plan.Epoch != 1 {
		return errors.New("standalone genesis ceremony manifest must use epoch 1")
	}
	if err := ceremony.ValidateCeremonyConfigBinding(cfg.Frost, manifest); err != nil {
		return err
	}
	if cfg.Frost.Vault.ThresholdRatio != 0.75 {
		return errors.New("trusted FROST configuration must use the exact 0.75 vault threshold ratio")
	}
	expectedThreshold, err := config.ComputeVaultCommitteeThreshold(cfg.Frost.Vault.DefaultK)
	if err != nil || manifest.Plan.Threshold != uint32(expectedThreshold) {
		return errors.New("ceremony threshold does not match the trusted FROST configuration")
	}

	chainNames := make([]string, 0, len(cfg.Frost.Chains))
	for chainName := range cfg.Frost.Chains {
		chainNames = append(chainNames, chainName)
	}
	sort.Strings(chainNames)
	expectedVaultCount := 0
	planVaults := make(map[string]ceremony.Vault, len(manifest.Plan.Vaults))
	for _, vault := range manifest.Plan.Vaults {
		planVaults[fmt.Sprintf("%s/%d", vault.Chain, vault.VaultID)] = vault
	}
	for _, chainName := range chainNames {
		chainCfg := cfg.Frost.Chains[chainName]
		groupPubkeys, err := config.DecodeInitialFrostGroupPubkeys(chainName, chainCfg)
		if err != nil {
			return fmt.Errorf("validate configured ceremony output for %s: %w", chainName, err)
		}
		if len(groupPubkeys) != chainCfg.VaultsPerChain {
			return fmt.Errorf("chain %s does not contain a complete configured ceremony result", chainName)
		}
		rawConfig, err := vaultReader.Read(keys.KeyFrostVaultConfig(chainName, 0))
		if err != nil {
			return fmt.Errorf("load on-chain FROST vault config for %s: %w", chainName, err)
		}
		var stateConfig pb.FrostVaultConfig
		if err := proto.Unmarshal([]byte(rawConfig), &stateConfig); err != nil {
			return fmt.Errorf("decode on-chain FROST vault config for %s: %w", chainName, err)
		}
		stateThreshold, err := config.ComputeVaultCommitteeThreshold(int(stateConfig.CommitteeSize))
		if err != nil {
			return fmt.Errorf("invalid on-chain FROST threshold for %s: %w", chainName, err)
		}
		expectedStateConfig := buildVaultConfigs(cfg.Frost, 0)[chainName]
		if expectedStateConfig == nil || !frostVaultConfigMatchesProtocol(&stateConfig, expectedStateConfig, allowLegacyBTCFee && strings.EqualFold(chainName, "btc")) ||
			stateConfig.ThresholdRatio != 0.75 || stateThreshold != expectedThreshold {
			return fmt.Errorf("on-chain FROST vault config disagrees with ceremony parameters for %s", chainName)
		}
		for vaultID := 0; vaultID < chainCfg.VaultsPerChain; vaultID++ {
			planned, ok := planVaults[fmt.Sprintf("%s/%d", chainName, vaultID)]
			if !ok || planned.SignAlgo != stateConfig.SignAlgo || len(planned.Committee) != int(stateConfig.CommitteeSize) {
				return fmt.Errorf("ceremony vault set or committee size disagrees with FROST config for %s/%d", chainName, vaultID)
			}
			expectedVaultCount++
		}
	}
	if len(manifest.Plan.Vaults) != expectedVaultCount {
		return errors.New("ceremony plan vault set does not exactly match configured chains")
	}
	return nil
}

func loadConfiguredFrostGenesisStateMarker(vaultReader frostGenesisVaultReader, frostCfg config.FrostConfig) (bool, error) {
	if vaultReader == nil {
		return false, errors.New("FROST vault reader is unavailable")
	}
	value, err := vaultReader.Read(keys.KeyFrostConfiguredGenesisMarker())
	if err != nil {
		if isNotFoundError(err) {
			return false, nil
		}
		return false, err
	}
	expected, err := configuredFrostGenesisMarkerValue(frostCfg)
	if err != nil {
		return false, fmt.Errorf("derive configured-genesis consensus marker: %w", err)
	}
	if value != expected {
		return false, errors.New("invalid or conflicting configured-genesis consensus marker")
	}
	return true, nil
}

func manifestParticipant(manifest *ceremony.PublicManifest, nodeID string) *ceremony.Participant {
	for i := range manifest.Plan.Participants {
		if manifest.Plan.Participants[i].ID == nodeID {
			return &manifest.Plan.Participants[i]
		}
	}
	return nil
}

func manifestVault(manifest *ceremony.PublicManifest, chain string, vaultID uint32) *ceremony.PublicVaultResult {
	if manifest == nil {
		return nil
	}
	for i := range manifest.Vaults {
		if manifest.Vaults[i].Chain == chain && manifest.Vaults[i].VaultID == vaultID {
			return &manifest.Vaults[i]
		}
	}
	return nil
}

func manifestVerificationShareForNode(manifest *ceremony.PublicManifest, nodeID, chain string, vaultID uint32) ([]byte, error) {
	if manifest == nil {
		return nil, errors.New("nil FROST genesis manifest")
	}
	for i := range manifest.Attestations {
		attestation := &manifest.Attestations[i]
		if attestation.ParticipantID != nodeID {
			continue
		}
		for j := range attestation.Vaults {
			vault := &attestation.Vaults[j]
			if vault.Chain == chain && vault.VaultID == vaultID {
				if len(vault.VerificationShare) == 0 {
					return nil, fmt.Errorf("manifest lacks local verification share for %s/%d", chain, vaultID)
				}
				return bytes.Clone(vault.VerificationShare), nil
			}
		}
		return nil, fmt.Errorf("participant attestation lacks local vault %s/%d", chain, vaultID)
	}
	return nil, fmt.Errorf("manifest lacks participant attestation for %s", nodeID)
}

func matchManifestVaultState(vault ceremony.PublicVaultResult, state *pb.FrostVaultState) (bool, error) {
	if state == nil || state.Status != "ACTIVE" || state.Chain != vault.Chain || state.VaultId != vault.VaultID ||
		state.KeyEpoch < vault.Epoch || len(state.CommitteeMembers) == 0 {
		return false, fmt.Errorf("configured ACTIVE vault state is invalid for %s/%d", vault.Chain, vault.VaultID)
	}
	if err := ceremony.ValidateGroupPublicKey(state.Chain, state.SignAlgo, state.GroupPubkey); err != nil {
		return false, fmt.Errorf("configured ACTIVE vault group key is invalid for %s/%d: %w", vault.Chain, vault.VaultID, err)
	}
	if state.KeyEpoch > vault.Epoch {
		                                                                     
		                                                                     
		                                                                  
		return false, nil
	}
	if state.SignAlgo != vault.SignAlgo || !bytes.Equal(state.GroupPubkey, vault.RawGroupPubkey) ||
		!equalGenesisStrings(state.CommitteeMembers, vault.Committee) {
		return false, fmt.Errorf("configured epoch-1 ACTIVE vault state does not match ceremony manifest for %s/%d", vault.Chain, vault.VaultID)
	}
	return true, nil
}

func genesisShareFilename(chain string, vaultID uint32) string {
	name, _ := ceremony.NodeBundleShareFilename(chain, vaultID)
	return name
}

func validateGenesisShareDirectory(directory string) (string, error) {
	absDirectory, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolve FROST genesis share directory: %w", err)
	}
	absDirectory = filepath.Clean(absDirectory)
	if runtime.GOOS == "windows" {
		if err := rejectWindowsSymlinkComponents(absDirectory); err != nil {
			return "", err
		}
	} else {
		resolved, err := filepath.EvalSymlinks(absDirectory)
		if err != nil {
			return "", fmt.Errorf("resolve FROST genesis share directory symlinks: %w", err)
		}
		if !sameCleanPath(absDirectory, resolved) {
			return "", errors.New("FROST genesis share directory path contains a symbolic link")
		}
	}
	info, err := os.Lstat(absDirectory)
	if err != nil {
		return "", fmt.Errorf("inspect FROST genesis share directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("FROST genesis share path is not a regular directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("FROST genesis share directory is writable by group or others")
	}
	return absDirectory, nil
}

func rejectWindowsSymlinkComponents(path string) error {
	volume := filepath.VolumeName(path)
	remainder := strings.TrimPrefix(path, volume)
	remainder = strings.TrimLeft(remainder, `\/`)
	current := volume + string(os.PathSeparator)
	for _, component := range strings.FieldsFunc(remainder, func(char rune) bool { return char == '\\' || char == '/' }) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect FROST genesis directory path component: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("FROST genesis share directory path contains a symbolic link")
		}
	}
	return nil
}

func validateGenesisDirectoryEntries(directory string, expected map[string]struct{}) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("list FROST genesis share directory: %w", err)
	}
	if len(entries) != len(expected) || len(entries) > ceremony.MaxVaults+1 {
		return errors.New("FROST genesis share directory has missing or extra artifacts")
	}
	for _, entry := range entries {
		if _, ok := expected[entry.Name()]; !ok {
			return fmt.Errorf("unexpected artifact %q in FROST genesis share directory", entry.Name())
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return fmt.Errorf("FROST genesis artifact %q is not a regular file", entry.Name())
		}
	}
	return nil
}

func readGenesisArtifact(directory, name string, maxSize int64) ([]byte, error) {
	if filepath.Base(name) != name || name == "." || name == ".." {
		return nil, errors.New("invalid FROST genesis artifact name")
	}
	path := filepath.Join(directory, name)
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, errors.New("artifact is a symlink or non-regular file")
	}
	if runtime.GOOS != "windows" && before.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("artifact is writable by group or others")
	}
	if before.Size() <= 0 || before.Size() > maxSize {
		return nil, errors.New("artifact is empty or exceeds its size limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, errors.New("artifact changed while opening or is non-regular")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		clear(data)
		return nil, err
	}
	if int64(len(data)) != before.Size() || int64(len(data)) > maxSize {
		clear(data)
		return nil, errors.New("artifact changed while reading or exceeds its size limit")
	}
	return data, nil
}

func sameCleanPath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func equalGenesisStrings(left, right []string) bool {
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
