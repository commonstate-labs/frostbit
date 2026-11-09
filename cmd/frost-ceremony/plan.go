package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"dex/config"
	"dex/frost/ceremony"
	"dex/pb"
	"dex/utils"

	"github.com/btcsuite/btcd/btcec/v2"
)

var ceremonyChains = ceremony.GenesisCeremonyChainOrder()

type normalizedValidator struct {
	index     uint64
	address   string
	publicKey []byte
}

type contextParticipant struct {
	Index             uint64 `json:"index"`
	ID                string `json:"id"`
	IdentityPublicKey []byte `json:"identity_public_key"`
}

type ceremonyContext struct {
	Version            uint32               `json:"version"`
	ID                 string               `json:"id"`
	NetworkChainID     uint64               `json:"network_chain_id"`
	PeerManifestDigest []byte               `json:"peer_manifest_digest"`
	FrostConfigDigest  []byte               `json:"frost_config_digest"`
	Epoch              uint64               `json:"epoch"`
	Threshold          uint32               `json:"threshold"`
	Participants       []contextParticipant `json:"participants"`
	Vaults             []ceremony.Vault     `json:"vaults"`
}

type ceremonyRegistration = ceremony.Registration

func runContext(args []string) error {
	fs := flag.NewFlagSet("context", flag.ContinueOnError)
	var peersPath, genesisPath, frostPath, out string
	var epoch uint64
	fs.Uint64Var(&epoch, "epoch", 1, "genesis key epoch (must be 1)")
	fs.StringVar(&peersPath, "peers", "", "public peers.json path")
	fs.StringVar(&genesisPath, "genesis", "", "public genesis.json (defaults beside peers.json)")
	fs.StringVar(&frostPath, "frost-config", "", "base frost_default.json path")
	fs.StringVar(&out, "out", "", "new public context file")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if peersPath == "" || frostPath == "" || out == "" {
		return errors.New("context requires --peers, --frost-config, and --out")
	}
	randomID := make([]byte, 16)
	if _, err := rand.Read(randomID); err != nil {
		return errors.New("generate ceremony ID failed")
	}
	id := "ceremony-" + hex.EncodeToString(randomID)
	wipe(randomID)
	manifest, err := loadPeerManifestStrict(peersPath)
	if err != nil {
		return err
	}
	frostConfig, err := loadFrostConfigStrict(frostPath)
	if err != nil {
		return err
	}
	if err := validateFreshGenesisInput(genesisPath, peersPath, frostConfig, manifest); err != nil {
		return err
	}
	context, err := buildCeremonyContext(id, epoch, manifest, frostConfig)
	if err != nil {
		return err
	}
	return writeExclusiveJSON(out, context, 0o644)
}

func runRegister(args []string) error {
	fs := flag.NewFlagSet("register", flag.ContinueOnError)
	var contextPath, nodeID, out string
	var identityFD, encryptionOutFD int
	fs.StringVar(&contextPath, "context", "", "trusted public ceremony context")
	fs.StringVar(&nodeID, "node-id", "", "this validator's canonical DEX address")
	fs.IntVar(&identityFD, "identity-key-fd", -1, "inherited FD >= 3 containing one 32-byte hex identity key")
	fs.IntVar(&encryptionOutFD, "encryption-key-out-fd", -1, "different inherited writable FD >= 3 for the one-time private key")
	fs.StringVar(&out, "out", "", "new public registration file")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if contextPath == "" || nodeID == "" || identityFD < 3 || encryptionOutFD < 3 || out == "" {
		return errors.New("register requires --context, --node-id, --identity-key-fd >= 3, --encryption-key-out-fd >= 3, and --out")
	}
	if identityFD == encryptionOutFD {
		return errors.New("identity input and encryption-key output must use different descriptors")
	}
	out, err := checkedNewPath(out)
	if err != nil {
		return err
	}
	context, err := loadCeremonyContext(contextPath)
	if err != nil {
		return err
	}
	identityKey, identityRaw, err := readIdentityKey(identityFD)
	if err != nil {
		return err
	}
	defer identityKey.Zero()
	defer wipe(identityRaw)
	if err := verifyContextIdentity(context, nodeID, identityKey); err != nil {
		return err
	}
	encryptionKey, err := btcec.NewPrivateKey()
	if err != nil {
		return errors.New("generate one-time ceremony encryption key failed")
	}
	defer encryptionKey.Zero()
	encryptionRaw := encryptionKey.Serialize()
	defer wipe(encryptionRaw)
	contextDigest, err := ceremonyContextDigest(context)
	if err != nil {
		return err
	}
	registration := &ceremonyRegistration{
		Version: ceremony.Version, ContextDigest: contextDigest, ParticipantID: nodeID,
		EncryptionPublicKey: encryptionKey.PubKey().SerializeCompressed(),
	}
	registrationDigest, err := ceremonyRegistrationDigest(context, registration)
	if err != nil {
		return err
	}
	registration.Signature, err = signIdentityDigest(identityKey, registrationDigest)
	if err != nil {
		return err
	}
	if err := writeSecretHexFD(encryptionOutFD, encryptionRaw, "one-time ceremony encryption key"); err != nil {
		return err
	}
	return writeExclusiveJSON(out, registration, 0o644)
}

func runPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	var contextPath, registrationsDir, out string
	fs.StringVar(&contextPath, "context", "", "trusted public ceremony context")
	fs.StringVar(&registrationsDir, "registrations-dir", "", "directory containing exactly one registration per participant")
	fs.StringVar(&out, "out", "", "new final public plan file")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if contextPath == "" || registrationsDir == "" || out == "" {
		return errors.New("plan requires --context, --registrations-dir, and --out")
	}
	out, err := checkedNewPath(out)
	if err != nil {
		return err
	}
	context, err := loadCeremonyContext(contextPath)
	if err != nil {
		return err
	}
	registrations, err := loadRegistrations(context, registrationsDir)
	if err != nil {
		return err
	}
	plan := &ceremony.Plan{
		Version: context.Version, ID: context.ID, NetworkChainID: context.NetworkChainID,
		PeerManifestDigest: bytes.Clone(context.PeerManifestDigest), FrostConfigDigest: bytes.Clone(context.FrostConfigDigest),
		Epoch: context.Epoch, Threshold: context.Threshold, Vaults: cloneVaults(context.Vaults),
	}
	for _, participant := range context.Participants {
		registration := registrations[participant.ID]
		plan.Participants = append(plan.Participants, ceremony.Participant{
			Index: participant.Index, ID: participant.ID, PublicKey: bytes.Clone(participant.IdentityPublicKey),
			EncryptionPublicKey: bytes.Clone(registration.EncryptionPublicKey),
		})
		plan.Registrations = append(plan.Registrations, ceremony.Registration{
			Version: registration.Version, ContextDigest: bytes.Clone(registration.ContextDigest),
			ParticipantID: registration.ParticipantID, EncryptionPublicKey: bytes.Clone(registration.EncryptionPublicKey),
			Signature: bytes.Clone(registration.Signature),
		})
	}
	if err := ceremony.ValidatePlan(plan); err != nil {
		return err
	}
	if err := validatePlanIdentities(plan); err != nil {
		return err
	}
	return writeExclusiveJSON(out, plan, 0o644)
}

func buildCeremonyContext(id string, epoch uint64, peerManifest *config.PeerManifest, frostConfig config.FrostConfig) (*ceremonyContext, error) {
	if peerManifest == nil {
		return nil, errors.New("peer manifest is nil")
	}
	if epoch != 1 {
		return nil, fmt.Errorf("first-generation genesis ceremony epoch must be 1, got %d", epoch)
	}
	if !frostConfig.Enabled {
		return nil, errors.New("FROST must be enabled for a ceremony")
	}
	if err := requireSixChains(frostConfig.Chains); err != nil {
		return nil, err
	}
	for _, chainName := range ceremonyChains {
		chainConfig := frostConfig.Chains[chainName]
		if len(chainConfig.VaultRefs) != 0 || len(chainConfig.InitialGroupPubkeys) != 0 {
			return nil, fmt.Errorf("base FROST config chain %s must not contain vaultRefs or initialGroupPubkeys before ceremony", chainName)
		}
	}
	validators, err := normalizeValidators(peerManifest.GenesisValidators)
	if err != nil {
		return nil, err
	}
	if frostConfig.Committee.TopN < 0 {
		return nil, errors.New("committee.topN must not be negative")
	}
	if frostConfig.Committee.TopN > 0 && len(validators) > frostConfig.Committee.TopN {
		validators = validators[:frostConfig.Committee.TopN]
	}
	k := frostConfig.Vault.DefaultK
	threshold, err := configuredCeremonyThreshold(k, frostConfig.Vault.ThresholdRatio)
	if err != nil {
		return nil, err
	}

	peerDigest, err := ceremony.ComputePeerManifestDigest(peerManifest)
	if err != nil {
		return nil, err
	}
	frostDigest, err := ceremony.ComputeFrostConfigDigest(frostConfig)
	if err != nil {
		return nil, err
	}
	context := &ceremonyContext{
		Version: ceremony.Version, ID: id, NetworkChainID: peerManifest.ChainID,
		PeerManifestDigest: peerDigest, FrostConfigDigest: frostDigest,
		Epoch: epoch, Threshold: uint32(threshold),
	}
	used := make(map[uint64]struct{})
	indices := make([]uint64, len(validators))
	addresses := make([]string, len(validators))
	byIndex := make(map[uint64]normalizedValidator, len(validators))
	for i, validator := range validators {
		indices[i] = validator.index
		addresses[i] = validator.address
		byIndex[validator.index] = validator
	}
	if len(indices) < k {
		return nil, fmt.Errorf("genesis committee requires %d validator indices, but topN provides %d", k, len(indices))
	}
	assignments, err := ceremony.InitialCommitteeAssignments(frostConfig, indices, addresses)
	if err != nil {
		return nil, fmt.Errorf("assign genesis committees: %w", err)
	}
	for _, chainName := range ceremonyChains {
		chainConfig := frostConfig.Chains[chainName]
		if chainConfig.VaultsPerChain <= 0 {
			return nil, fmt.Errorf("chain %s vaultsPerChain must be positive", chainName)
		}
		if chainConfig.VaultsPerChain > ceremony.MaxVaults || len(context.Vaults)+chainConfig.VaultsPerChain > ceremony.MaxVaults {
			return nil, fmt.Errorf("six-chain ceremony exceeds the maximum of %d vaults", ceremony.MaxVaults)
		}
		algorithm, err := ceremonySignAlgo(chainName, chainConfig.SignAlgo)
		if err != nil {
			return nil, err
		}
		for vaultID := 0; vaultID < chainConfig.VaultsPerChain; vaultID++ {
			assignment := assignments[chainName][vaultID]
			if len(assignment) != k {
				return nil, fmt.Errorf("chain %s vault %d has an incomplete committee", chainName, vaultID)
			}
			members := make([]string, len(assignment))
			for i, index := range assignment {
				validator, ok := byIndex[index]
				if !ok {
					return nil, fmt.Errorf("chain %s vault %d references an unknown validator index", chainName, vaultID)
				}
				members[i] = validator.address
				used[index] = struct{}{}
			}
			context.Vaults = append(context.Vaults, ceremony.Vault{Chain: chainName, VaultID: uint32(vaultID), SignAlgo: algorithm, Committee: members})
		}
	}
	for _, validator := range validators {
		if _, ok := used[validator.index]; !ok {
			continue
		}
		context.Participants = append(context.Participants, contextParticipant{Index: validator.index, ID: validator.address, IdentityPublicKey: bytes.Clone(validator.publicKey)})
	}
	if err := validateCeremonyContext(context); err != nil {
		return nil, err
	}
	return context, nil
}

func configuredCeremonyThreshold(committeeSize int, thresholdRatio float64) (int, error) {
	if thresholdRatio != 0.75 {
		return 0, fmt.Errorf("vault.thresholdRatio must be exactly 0.75, got %v", thresholdRatio)
	}
	return config.ComputeVaultCommitteeThreshold(committeeSize)
}

func loadCeremonyContext(path string) (*ceremonyContext, error) {
	data, err := readRegularFile(path, maxSmallJSONBytes)
	if err != nil {
		return nil, err
	}
	var context ceremonyContext
	if err := strictJSON(data, &context); err != nil {
		return nil, err
	}
	if err := validateCeremonyContext(&context); err != nil {
		return nil, err
	}
	return &context, nil
}

func validateCeremonyContext(context *ceremonyContext) error {
	if context == nil || context.Version != ceremony.Version || context.ID == "" || len(context.ID) > 128 ||
		context.NetworkChainID == 0 || context.Epoch == 0 || context.Threshold == 0 ||
		!validDigest(context.PeerManifestDigest) || !validDigest(context.FrostConfigDigest) {
		return errors.New("invalid ceremony context header")
	}
	for _, char := range []byte(context.ID) {
		if char < 0x21 || char > 0x7e {
			return errors.New("ceremony context ID must use visible ASCII")
		}
	}
	if len(context.Participants) == 0 || len(context.Participants) > ceremony.MaxParticipants || len(context.Vaults) == 0 || len(context.Vaults) > ceremony.MaxVaults {
		return errors.New("invalid ceremony context dimensions")
	}
	participants := make(map[string]contextParticipant, len(context.Participants))
	var previousIndex uint64
	for position, participant := range context.Participants {
		publicKey, err := btcec.ParsePubKey(participant.IdentityPublicKey)
		if err != nil || !bytes.Equal(publicKey.SerializeCompressed(), participant.IdentityPublicKey) {
			return errors.New("context contains an invalid identity public key")
		}
		address, err := utils.AddressFromSecp256k1PubKey(participant.IdentityPublicKey)
		if err != nil || address != participant.ID || position > 0 && participant.Index <= previousIndex {
			return errors.New("context participant identities are invalid or not ordered by genesis index")
		}
		if _, duplicate := participants[participant.ID]; duplicate {
			return errors.New("context contains a duplicate participant")
		}
		participants[participant.ID] = participant
		previousIndex = participant.Index
	}
	used := make(map[string]struct{})
	chainOrder := make(map[string]int, len(ceremonyChains))
	for i, chainName := range ceremonyChains {
		chainOrder[chainName] = i
	}
	lastChainPosition := -1
	nextVaultID := make(map[string]uint32, len(ceremonyChains))
	seenChains := make(map[string]struct{}, len(ceremonyChains))
	committeeSize := -1
	for position, vault := range context.Vaults {
		chainPosition, supported := chainOrder[vault.Chain]
		if !supported || chainPosition < lastChainPosition || vault.VaultID != nextVaultID[vault.Chain] || !validVaultChainAlgorithm(vault) {
			return fmt.Errorf("context vault %d has an invalid chain/signing algorithm", position)
		}
		lastChainPosition = chainPosition
		nextVaultID[vault.Chain]++
		seenChains[vault.Chain] = struct{}{}
		if committeeSize == -1 {
			committeeSize = len(vault.Committee)
		}
		if len(vault.Committee) != committeeSize || len(vault.Committee) < int(context.Threshold) || len(vault.Committee) == 0 {
			return fmt.Errorf("context vault %s/%d has an invalid committee", vault.Chain, vault.VaultID)
		}
		expectedThreshold, err := config.ComputeVaultCommitteeThreshold(len(vault.Committee))
		if err != nil || context.Threshold != uint32(expectedThreshold) {
			return fmt.Errorf("context vault %s/%d must use an official committee tier and exact 3/4 threshold", vault.Chain, vault.VaultID)
		}
		members := make(map[string]struct{}, len(vault.Committee))
		for _, member := range vault.Committee {
			if _, ok := participants[member]; !ok {
				return errors.New("context vault references a nonparticipant")
			}
			if _, duplicate := members[member]; duplicate {
				return errors.New("context vault contains a duplicate member")
			}
			members[member] = struct{}{}
			used[member] = struct{}{}
		}
	}
	if len(used) != len(participants) {
		return errors.New("context contains an unused participant")
	}
	if len(seenChains) != len(ceremonyChains) {
		return errors.New("context does not contain all six supported chains")
	}
	assignmentCfg := config.FrostConfig{Chains: make(map[string]config.ChainFrostConfig, len(ceremonyChains))}
	assignmentCfg.Vault.DefaultK = committeeSize
	for chainName, count := range nextVaultID {
		assignmentCfg.Chains[chainName] = config.ChainFrostConfig{VaultsPerChain: int(count)}
	}
	indices := make([]uint64, len(context.Participants))
	addresses := make([]string, len(context.Participants))
	byIndex := make(map[uint64]string, len(context.Participants))
	for i, participant := range context.Participants {
		indices[i] = participant.Index
		addresses[i] = participant.ID
		byIndex[participant.Index] = participant.ID
	}
	assignments, err := ceremony.InitialCommitteeAssignments(assignmentCfg, indices, addresses)
	if err != nil {
		return fmt.Errorf("validate context committees: %w", err)
	}
	for _, vault := range context.Vaults {
		expected := assignments[vault.Chain][vault.VaultID]
		if len(expected) != len(vault.Committee) {
			return fmt.Errorf("context committee size disagrees with deterministic assignment for %s/%d", vault.Chain, vault.VaultID)
		}
		for i, index := range expected {
			if vault.Committee[i] != byIndex[index] {
				return fmt.Errorf("context committee order disagrees with deterministic assignment for %s/%d", vault.Chain, vault.VaultID)
			}
		}
	}
	return nil
}

func validVaultChainAlgorithm(vault ceremony.Vault) bool {
	if vault.Chain == "btc" {
		return vault.SignAlgo == pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
	}
	switch vault.Chain {
	case "arb", "base", "bnb", "eth", "trx":
		return vault.SignAlgo == pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128
	default:
		return false
	}
}

func ceremonyContextDigest(context *ceremonyContext) ([]byte, error) {
	if err := validateCeremonyContext(context); err != nil {
		return nil, err
	}
	plan := &ceremony.Plan{
		Version: context.Version, ID: context.ID, NetworkChainID: context.NetworkChainID,
		PeerManifestDigest: context.PeerManifestDigest, FrostConfigDigest: context.FrostConfigDigest,
		Epoch: context.Epoch, Threshold: context.Threshold, Vaults: context.Vaults,
	}
	for _, participant := range context.Participants {
		plan.Participants = append(plan.Participants, ceremony.Participant{
			Index: participant.Index, ID: participant.ID, PublicKey: participant.IdentityPublicKey,
		})
	}
	return ceremony.RegistrationContextDigest(plan)
}

func ceremonyRegistrationDigest(context *ceremonyContext, registration *ceremonyRegistration) ([]byte, error) {
	if err := validateRegistrationFields(context, registration, false); err != nil {
		return nil, err
	}
	return ceremony.RegistrationDigest(registration)
}

func loadRegistrations(context *ceremonyContext, directory string) (map[string]*ceremonyRegistration, error) {
	files, err := listStrictJSONFiles(directory)
	if err != nil {
		return nil, err
	}
	if len(files) != len(context.Participants) {
		return nil, fmt.Errorf("registrations directory contains %d files; context requires %d", len(files), len(context.Participants))
	}
	registrations := make(map[string]*ceremonyRegistration, len(files))
	for _, path := range files {
		data, err := readRegularFile(path, maxSmallJSONBytes)
		if err != nil {
			return nil, err
		}
		var registration ceremonyRegistration
		if err := strictJSON(data, &registration); err != nil {
			return nil, fmt.Errorf("decode registration %s: %w", filepath.Base(path), err)
		}
		if err := validateRegistrationFields(context, &registration, true); err != nil {
			return nil, fmt.Errorf("validate registration %s: %w", filepath.Base(path), err)
		}
		digest, err := ceremonyRegistrationDigest(context, &registration)
		if err != nil {
			return nil, err
		}
		participant := findContextParticipant(context, registration.ParticipantID)
		if participant == nil || !ceremony.IsCanonicalIdentitySignature(registration.Signature) || !utils.VerifyECDSASignature(participant.IdentityPublicKey, digest, registration.Signature) {
			return nil, fmt.Errorf("registration %s has an invalid identity signature", filepath.Base(path))
		}
		if registrations[registration.ParticipantID] != nil {
			return nil, fmt.Errorf("duplicate registration for %s", registration.ParticipantID)
		}
		registrations[registration.ParticipantID] = &registration
	}
	for _, participant := range context.Participants {
		if registrations[participant.ID] == nil {
			return nil, fmt.Errorf("missing registration for %s", participant.ID)
		}
	}
	return registrations, nil
}

func validateRegistrationFields(context *ceremonyContext, registration *ceremonyRegistration, requireSignature bool) error {
	contextDigest, err := ceremonyContextDigest(context)
	if err != nil {
		return err
	}
	if registration == nil || registration.Version != ceremony.Version || !bytes.Equal(registration.ContextDigest, contextDigest) || findContextParticipant(context, registration.ParticipantID) == nil {
		return errors.New("registration header does not match context")
	}
	publicKey, err := btcec.ParsePubKey(registration.EncryptionPublicKey)
	if err != nil || !bytes.Equal(publicKey.SerializeCompressed(), registration.EncryptionPublicKey) {
		return errors.New("registration encryption public key is invalid")
	}
	participant := findContextParticipant(context, registration.ParticipantID)
	if bytes.Equal(participant.IdentityPublicKey, registration.EncryptionPublicKey) {
		return errors.New("ceremony encryption key must be independent from validator identity")
	}
	if requireSignature && len(registration.Signature) != 64 || !requireSignature && len(registration.Signature) != 0 && len(registration.Signature) != 64 {
		return errors.New("registration signature length is invalid")
	}
	return nil
}

func verifyContextIdentity(context *ceremonyContext, nodeID string, privateKey *btcec.PrivateKey) error {
	participant := findContextParticipant(context, nodeID)
	if participant == nil || privateKey == nil || !bytes.Equal(participant.IdentityPublicKey, privateKey.PubKey().SerializeCompressed()) {
		return errors.New("identity private key does not match this context participant")
	}
	return nil
}

func findContextParticipant(context *ceremonyContext, id string) *contextParticipant {
	for i := range context.Participants {
		if context.Participants[i].ID == id {
			return &context.Participants[i]
		}
	}
	return nil
}

func cloneVaults(vaults []ceremony.Vault) []ceremony.Vault {
	result := make([]ceremony.Vault, len(vaults))
	for i, vault := range vaults {
		result[i] = ceremony.Vault{Chain: vault.Chain, VaultID: vault.VaultID, SignAlgo: vault.SignAlgo, Committee: append([]string(nil), vault.Committee...)}
	}
	return result
}

func validDigest(digest []byte) bool {
	return len(digest) == 32 && !allZero(digest)
}

func writeSecretHexFD(fd int, secret []byte, label string) error {
	if fd < 3 || len(secret) != 32 {
		return fmt.Errorf("%s output requires a descriptor >= 3 and a 32-byte value", label)
	}
	file := os.NewFile(uintptr(fd), label)
	if file == nil {
		return fmt.Errorf("%s output descriptor is unavailable", label)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() || info.Mode()&os.ModeDevice != 0 {
		return fmt.Errorf("%s output descriptor must be a regular file or pipe", label)
	}
	regular := info.Mode().IsRegular()
	if regular {
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("%s output file permissions must not grant group or other access", label)
		}
		if info.Size() != 0 {
			return fmt.Errorf("%s output file must be empty", label)
		}
		offset, err := file.Seek(0, io.SeekCurrent)
		if err != nil || offset != 0 {
			return fmt.Errorf("%s output file offset must be zero", label)
		}
	}
	encoded := make([]byte, hex.EncodedLen(len(secret))+1)
	hex.Encode(encoded, secret)
	encoded[len(encoded)-1] = '\n'
	defer wipe(encoded)
	for len(encoded) > 0 {
		written, err := file.Write(encoded)
		if err != nil {
			return fmt.Errorf("write %s output descriptor: %w", label, err)
		}
		if written == 0 {
			return fmt.Errorf("write %s output descriptor made no progress", label)
		}
		encoded = encoded[written:]
	}
	if regular {
		if err := file.Sync(); err != nil {
			return fmt.Errorf("sync %s output descriptor: %w", label, err)
		}
	}
	return nil
}

func normalizeValidators(input []config.GenesisValidatorConfig) ([]normalizedValidator, error) {
	validators := make([]normalizedValidator, 0, len(input))
	addresses := make(map[string]struct{}, len(input))
	publicKeys := make(map[string]struct{}, len(input))
	blsPublicKeys := make(map[string]struct{}, len(input))
	for sourceIndex, validator := range input {
		if !validator.IsMiner {
			return nil, fmt.Errorf("genesis validator %d must be a miner", sourceIndex)
		}
		publicKey, err := decodeCanonicalSecpPublicKey(validator.SecpPublicKey)
		if err != nil {
			return nil, fmt.Errorf("genesis validator %d secp public key: %w", sourceIndex, err)
		}
		address, err := utils.AddressFromSecp256k1PubKey(publicKey)
		if err != nil {
			return nil, fmt.Errorf("genesis validator %d derive address: %w", sourceIndex, err)
		}
		if strings.TrimSpace(validator.Address) != address {
			return nil, fmt.Errorf("genesis validator %d address does not match its secp public key", sourceIndex)
		}
		if !utils.IsDexAddress(strings.TrimSpace(validator.RewardAddress)) {
			return nil, fmt.Errorf("genesis validator %d reward address is not canonical", sourceIndex)
		}
		blsPublicKey, err := canonicalBLSPublicKey(validator.BLSPublicKey)
		if err != nil {
			return nil, fmt.Errorf("genesis validator %d BLS public key: %w", sourceIndex, err)
		}
		if _, duplicate := addresses[address]; duplicate {
			return nil, fmt.Errorf("duplicate genesis validator address %s", address)
		}
		if _, duplicate := publicKeys[string(publicKey)]; duplicate {
			return nil, errors.New("duplicate genesis validator secp public key")
		}
		if _, duplicate := blsPublicKeys[string(blsPublicKey)]; duplicate {
			return nil, errors.New("duplicate genesis validator BLS public key")
		}
		addresses[address] = struct{}{}
		publicKeys[string(publicKey)] = struct{}{}
		blsPublicKeys[string(blsPublicKey)] = struct{}{}
		validators = append(validators, normalizedValidator{address: address, publicKey: publicKey})
	}
	sort.Slice(validators, func(i, j int) bool { return validators[i].address < validators[j].address })
	for index := range validators {
		validators[index].index = uint64(index)
	}
	return validators, nil
}

func decodeCanonicalSecpPublicKey(raw string) ([]byte, error) {
	decoded, err := decodePublicHex(raw)
	if err != nil {
		return nil, err
	}
	publicKey, err := btcec.ParsePubKey(decoded)
	if err != nil {
		return nil, err
	}
	canonical := publicKey.SerializeCompressed()
	if !bytes.Equal(decoded, canonical) {
		return nil, errors.New("public key must use canonical compressed encoding")
	}
	return canonical, nil
}

func canonicalBLSPublicKey(raw string) ([]byte, error) {
	decoded, err := decodePublicHex(raw)
	if err != nil {
		return nil, err
	}
	point, err := utils.DeserializePublicKey(decoded)
	if err != nil {
		return nil, err
	}
	canonical, err := utils.SerializePublicKey(point)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(decoded, canonical) {
		return nil, errors.New("public key is not canonical")
	}
	return canonical, nil
}

func decodePublicHex(raw string) ([]byte, error) {
	value := strings.TrimSpace(raw)
	value = strings.TrimPrefix(strings.TrimPrefix(value, "0x"), "0X")
	if value == "" {
		return nil, errors.New("public key is empty")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return nil, errors.New("public key is not valid hex")
	}
	return decoded, nil
}

func ceremonySignAlgo(chainName, configured string) (pb.SignAlgo, error) {
	want := pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128
	wantName := "SCHNORR_ALT_BN128"
	if chainName == "btc" {
		want = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
		wantName = "SCHNORR_SECP256K1_BIP340"
	}
	if configured != wantName {
		return pb.SignAlgo_SIGN_ALGO_UNSPECIFIED, fmt.Errorf("chain %s requires signAlgo %s", chainName, wantName)
	}
	return want, nil
}

func validatePlanIdentities(plan *ceremony.Plan) error {
	if err := ceremony.ValidatePlan(plan); err != nil {
		return err
	}
	for _, participant := range plan.Participants {
		address, err := utils.AddressFromSecp256k1PubKey(participant.PublicKey)
		if err != nil || address != participant.ID {
			return fmt.Errorf("plan participant %q does not match its identity public key", participant.ID)
		}
	}
	return nil
}

func requireSixChains(chains map[string]config.ChainFrostConfig) error {
	if len(chains) != len(ceremonyChains) {
		return fmt.Errorf("FROST config must contain exactly the six supported chains, got %d", len(chains))
	}
	for _, chainName := range ceremonyChains {
		if _, ok := chains[chainName]; !ok {
			return fmt.Errorf("FROST config is missing chain %s", chainName)
		}
	}
	return nil
}

func loadPeerManifestStrict(path string) (*config.PeerManifest, error) {
	before, err := readRegularFile(path, maxSmallJSONBytes)
	if err != nil {
		return nil, err
	}
	var syntax config.PeerManifest
	if err := strictJSON(before, &syntax); err != nil {
		return nil, fmt.Errorf("invalid peer manifest: %w", err)
	}
	manifest, err := config.LoadPeerManifest(path)
	if err != nil {
		return nil, err
	}
	after, err := readRegularFile(path, maxSmallJSONBytes)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(before, after) {
		return nil, errors.New("peer manifest changed while it was being validated")
	}
	return manifest, nil
}

func loadFrostConfigStrict(path string) (config.FrostConfig, error) {
	data, err := readRegularFile(path, maxSmallJSONBytes)
	if err != nil {
		return config.FrostConfig{}, err
	}
	return decodeFrostConfigStrict(data)
}

func decodeFrostConfigStrict(data []byte) (config.FrostConfig, error) {
	result := config.DefaultFrostConfig()
	if err := strictJSON(data, &result); err != nil {
		return config.FrostConfig{}, fmt.Errorf("invalid FROST config: %w", err)
	}
	mergeFrostChainDefaults(&result)
	return result, nil
}

                                                                             
                                                                              
                                                                         
func mergeFrostChainDefaults(result *config.FrostConfig) {
	defaults := config.DefaultFrostConfig()
	if result.Chains == nil {
		result.Chains = defaults.Chains
		return
	}
	for chainName, fallback := range defaults.Chains {
		current, ok := result.Chains[chainName]
		if !ok {
			result.Chains[chainName] = fallback
			continue
		}
		if current.SignAlgo == "" {
			current.SignAlgo = fallback.SignAlgo
		}
		if current.FrostVariant == "" {
			current.FrostVariant = fallback.FrostVariant
		}
		if current.ThresholdScheme == "" {
			current.ThresholdScheme = fallback.ThresholdScheme
		}
		if current.VaultsPerChain == 0 {
			current.VaultsPerChain = fallback.VaultsPerChain
		}
		if current.BTCNetwork == "" {
			current.BTCNetwork = fallback.BTCNetwork
		}
		if current.ChainID == 0 {
			current.ChainID = fallback.ChainID
		}
		if current.FeeSatsPerVByte == 0 {
			current.FeeSatsPerVByte = fallback.FeeSatsPerVByte
		}
		if current.GasPriceGwei == 0 {
			current.GasPriceGwei = fallback.GasPriceGwei
		}
		if current.GasLimit == 0 {
			current.GasLimit = fallback.GasLimit
		}
		if current.FeeLimitSun == 0 {
			current.FeeLimitSun = fallback.FeeLimitSun
		}
		result.Chains[chainName] = current
	}
}
