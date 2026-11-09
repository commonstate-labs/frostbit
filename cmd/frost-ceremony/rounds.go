package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"

	"dex/frost/ceremony"
	"dex/utils"

	"github.com/btcsuite/btcd/btcec/v2"
)

func runRound1(args []string) error {
	fs := flag.NewFlagSet("round1", flag.ContinueOnError)
	var planPath, nodeID, out string
	var identityFD int
	fs.StringVar(&planPath, "plan", "", "trusted public plan file")
	fs.StringVar(&nodeID, "node-id", "", "this validator's canonical DEX address")
	fs.IntVar(&identityFD, "identity-key-fd", -1, "inherited FD >= 3 containing one 32-byte hex identity key")
	fs.StringVar(&out, "out", "", "new signed round1 package file")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if planPath == "" || nodeID == "" || out == "" || identityFD < 3 {
		return errors.New("round1 requires --plan, --node-id, --identity-key-fd >= 3, and --out")
	}
	out, err := checkedNewPath(out)
	if err != nil {
		return err
	}
	plan, err := loadPlan(planPath)
	if err != nil {
		return err
	}
	privateKey, rawPrivateKey, err := readIdentityKey(identityFD)
	if err != nil {
		return err
	}
	defer privateKey.Zero()
	defer wipe(rawPrivateKey)
	if err := verifyLocalIdentity(plan, nodeID, privateKey); err != nil {
		return err
	}
	item, err := ceremony.Round1(plan, nodeID)
	if err != nil {
		return err
	}
	digest, err := ceremony.Round1PackageDigest(plan, item)
	if err != nil {
		return err
	}
	item.Signature, err = signIdentityDigest(privateKey, digest)
	if err != nil {
		return err
	}
	return writeExclusiveJSON(out, item, 0o644)
}

func runFinalize(args []string) error {
	fs := flag.NewFlagSet("finalize", flag.ContinueOnError)
	var planPath, nodeID, packagesDir, sharesOutDir, attestationOut string
	var identityFD, encryptionFD, wrapFD int
	var plaintextGenesisShares bool
	fs.StringVar(&planPath, "plan", "", "trusted public plan file")
	fs.StringVar(&nodeID, "node-id", "", "this validator's canonical DEX address")
	fs.IntVar(&identityFD, "identity-key-fd", -1, "inherited FD >= 3 containing one 32-byte hex identity key")
	fs.IntVar(&encryptionFD, "encryption-key-fd", -1, "different inherited FD >= 3 containing the one-time ceremony decryption key")
	fs.IntVar(&wrapFD, "wrap-key-fd", -1, "different inherited FD >= 3 containing one 32-byte hex wrap key")
	fs.BoolVar(&plaintextGenesisShares, "plaintext-genesis-shares", false, "explicit development mode: write unencrypted genesis shares for DEX chain 51025")
	fs.StringVar(&packagesDir, "packages-dir", "", "directory containing exactly one signed package per participant")
	fs.StringVar(&sharesOutDir, "shares-out-dir", "", "new private directory for this validator's share files")
	fs.StringVar(&attestationOut, "attestation-out", "", "new public attestation file")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if planPath == "" || nodeID == "" || packagesDir == "" || sharesOutDir == "" || attestationOut == "" || identityFD < 3 || encryptionFD < 3 || (!plaintextGenesisShares && wrapFD < 3) {
		return errors.New("finalize requires --plan, --node-id, identity/encryption secret FDs >= 3, --packages-dir, --shares-out-dir, --attestation-out, and either --wrap-key-fd or --plaintext-genesis-shares")
	}
	if plaintextGenesisShares && wrapFD != -1 {
		return errors.New("plaintext genesis mode rejects --wrap-key-fd")
	}
	if identityFD == encryptionFD || (!plaintextGenesisShares && (identityFD == wrapFD || encryptionFD == wrapFD)) {
		return errors.New("identity, one-time encryption, and wrap keys must use different inherited descriptors")
	}
	sharesOutDir, err := checkedNewPath(sharesOutDir)
	if err != nil {
		return err
	}
	attestationOut, err = checkedNewPath(attestationOut)
	if err != nil {
		return err
	}
	plan, err := loadPlan(planPath)
	if err != nil {
		return err
	}
	if plaintextGenesisShares && plan.NetworkChainID != ceremony.PlaintextGenesisChainID {
		return errors.New("plaintext genesis shares are permitted only for DEX chain 51025")
	}
	packages, err := loadAndVerifyRound1Packages(plan, packagesDir)
	if err != nil {
		return err
	}
	privateKey, rawPrivateKey, err := readIdentityKey(identityFD)
	if err != nil {
		return err
	}
	defer privateKey.Zero()
	defer wipe(rawPrivateKey)
	if err := verifyLocalIdentity(plan, nodeID, privateKey); err != nil {
		return err
	}
	encryptionKey, encryptionRaw, err := readIdentityKey(encryptionFD)
	if err != nil {
		return fmt.Errorf("read one-time ceremony encryption key: %w", err)
	}
	defer encryptionKey.Zero()
	defer wipe(encryptionRaw)
	if err := verifyEncryptionIdentity(plan, nodeID, encryptionKey); err != nil {
		return err
	}
	var wrapKey []byte
	if !plaintextGenesisShares {
		wrapKey, err = readWrapKey(wrapFD)
		if err != nil {
			return err
		}
		defer wipe(wrapKey)
		if allZero(wrapKey) {
			return errors.New("FROST wrap key must not be all zero")
		}
		if bytes.Equal(wrapKey, rawPrivateKey) || bytes.Equal(wrapKey, encryptionRaw) {
			return errors.New("FROST wrap key must be independent from identity and one-time ceremony keys")
		}
	}
	result, err := ceremony.Finalize(plan, nodeID, encryptionRaw, packages)
	if err != nil {
		return err
	}
	defer clearCeremonyResult(result)
	attestation, err := ceremony.BuildAttestation(plan, result)
	if err != nil {
		return err
	}
	attestationDigest, err := ceremony.AttestationDigest(plan, attestation)
	if err != nil {
		return err
	}
	attestation.Signature, err = signIdentityDigest(privateKey, attestationDigest)
	if err != nil {
		return err
	}

	type sealedOutput struct {
		name string
		data []byte
	}
	sealed := make([]sealedOutput, 0, len(result.Vaults))
	defer func() {
		for i := range sealed {
			wipe(sealed[i].data)
		}
	}()
	for i := range result.Vaults {
		vault := &result.Vaults[i]
		if i >= len(attestation.Vaults) || attestation.Vaults[i].Chain != vault.Chain || attestation.Vaults[i].VaultID != vault.VaultID {
			return errors.New("public attestation does not align with finalized local shares")
		}
		committeeDigest, err := ceremony.CommitteeDigest(vault.Chain, vault.VaultID, vault.Epoch, vault.SignAlgo, vault.Committee)
		if err != nil {
			return err
		}
		metadata := ceremony.ShareMetadata{
			CeremonyDigest:    hex.EncodeToString(result.TranscriptDigest),
			NodeID:            nodeID,
			Chain:             vault.Chain,
			VaultID:           vault.VaultID,
			Epoch:             vault.Epoch,
			SignAlgo:          vault.SignAlgo,
			CommitteeDigest:   hex.EncodeToString(committeeDigest),
			GroupPubkey:       bytes.Clone(vault.RawGroupPubkey),
			VerificationShare: bytes.Clone(attestation.Vaults[i].VerificationShare),
		}
		var sealedShare []byte
		if plaintextGenesisShares {
			sealedShare, err = ceremony.EncodePlaintextGenesisShare(metadata, vault.LocalShare)
		} else {
			sealedShare, err = ceremony.SealShare(metadata, vault.LocalShare, wrapKey)
		}
		wipe(metadata.GroupPubkey)
		wipe(metadata.VerificationShare)
		if err != nil {
			return fmt.Errorf("encode share for %s/%d: %w", vault.Chain, vault.VaultID, err)
		}
		sealed = append(sealed, sealedOutput{name: fmt.Sprintf("%s-%d.share.json", vault.Chain, vault.VaultID), data: sealedShare})
		wipe(vault.LocalShare)
	}

	createdDir, err := createExclusiveDirectory(sharesOutDir, 0o700)
	if err != nil {
		return err
	}
	createdFiles := make([]string, 0, len(sealed))
	success := false
	defer func() {
		if success {
			return
		}
		for _, path := range createdFiles {
			_ = os.Remove(path)
		}
		_ = os.Remove(createdDir)
	}()
	for _, output := range sealed {
		path := filepath.Join(createdDir, output.name)
		if err := writeExclusive(path, output.data, 0o600); err != nil {
			return err
		}
		createdFiles = append(createdFiles, path)
	}
	if err := writeExclusiveJSON(attestationOut, attestation, 0o644); err != nil {
		return err
	}
	success = true
	return nil
}

func runAssemble(args []string) error {
	fs := flag.NewFlagSet("assemble", flag.ContinueOnError)
	var planPath, attestationsDir, out string
	fs.StringVar(&planPath, "plan", "", "trusted public plan file")
	fs.StringVar(&attestationsDir, "attestations-dir", "", "directory containing exactly one signed attestation per participant")
	fs.StringVar(&out, "out", "", "new public manifest file")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if planPath == "" || attestationsDir == "" || out == "" {
		return errors.New("assemble requires --plan, --attestations-dir, and --out")
	}
	out, err := checkedNewPath(out)
	if err != nil {
		return err
	}
	plan, err := loadPlan(planPath)
	if err != nil {
		return err
	}
	files, err := listStrictJSONFiles(attestationsDir)
	if err != nil {
		return err
	}
	if len(files) != len(plan.Participants) {
		return fmt.Errorf("attestations directory contains %d files; plan requires %d", len(files), len(plan.Participants))
	}
	attestations := make([]*ceremony.PublicAttestation, 0, len(files))
	for _, path := range files {
		data, err := readRegularFile(path, maxSmallJSONBytes)
		if err != nil {
			return err
		}
		attestation, err := ceremony.StrictDecodeAttestation(data)
		if err != nil {
			return fmt.Errorf("decode attestation %s: %w", filepath.Base(path), err)
		}
		attestations = append(attestations, attestation)
	}
	manifest, err := ceremony.Assemble(plan, attestations, utils.VerifyECDSASignature)
	if err != nil {
		return err
	}
	if err := ceremony.ValidateManifest(manifest, utils.VerifyECDSASignature); err != nil {
		return err
	}
	return writeExclusiveJSON(out, manifest, 0o644)
}

func loadPlan(path string) (*ceremony.Plan, error) {
	data, err := readRegularFile(path, maxSmallJSONBytes)
	if err != nil {
		return nil, err
	}
	plan, err := ceremony.StrictDecodePlan(data)
	if err != nil {
		return nil, err
	}
	if err := validatePlanIdentities(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func loadAndVerifyRound1Packages(plan *ceremony.Plan, directory string) ([]*ceremony.Round1Package, error) {
	files, err := listStrictJSONFiles(directory)
	if err != nil {
		return nil, err
	}
	if len(files) != len(plan.Participants) {
		return nil, fmt.Errorf("packages directory contains %d files; plan requires %d", len(files), len(plan.Participants))
	}
	byDealer := make(map[string]*ceremony.Round1Package, len(files))
	participants := make(map[string]ceremony.Participant, len(plan.Participants))
	for _, participant := range plan.Participants {
		participants[participant.ID] = participant
	}
	for _, path := range files {
		data, err := readRegularFile(path, maxLargeJSONBytes)
		if err != nil {
			return nil, err
		}
		item, err := ceremony.StrictDecodeRound1Package(data)
		if err != nil {
			return nil, fmt.Errorf("decode package %s: %w", filepath.Base(path), err)
		}
		if err := ceremony.ValidateRound1Package(plan, item); err != nil {
			return nil, fmt.Errorf("validate package %s: %w", filepath.Base(path), err)
		}
		participant, ok := participants[item.DealerID]
		if !ok || len(item.Signature) != 64 {
			return nil, fmt.Errorf("package %s lacks a valid dealer identity signature", filepath.Base(path))
		}
		digest, err := ceremony.Round1PackageDigest(plan, item)
		if err != nil {
			return nil, err
		}
		if !ceremony.IsCanonicalIdentitySignature(item.Signature) || !utils.VerifyECDSASignature(participant.PublicKey, digest, item.Signature) {
			return nil, fmt.Errorf("package %s has an invalid dealer identity signature", filepath.Base(path))
		}
		if _, duplicate := byDealer[item.DealerID]; duplicate {
			return nil, fmt.Errorf("duplicate dealer package for %s", item.DealerID)
		}
		byDealer[item.DealerID] = item
	}
	ordered := make([]*ceremony.Round1Package, len(plan.Participants))
	for i, participant := range plan.Participants {
		item := byDealer[participant.ID]
		if item == nil {
			return nil, fmt.Errorf("missing dealer package for %s", participant.ID)
		}
		ordered[i] = item
	}
	return ordered, nil
}

func verifyLocalIdentity(plan *ceremony.Plan, nodeID string, privateKey *btcec.PrivateKey) error {
	if privateKey == nil {
		return errors.New("identity private key is unavailable")
	}
	publicKey := privateKey.PubKey().SerializeCompressed()
	derived, err := utils.AddressFromSecp256k1PubKey(publicKey)
	if err != nil || derived != nodeID {
		return errors.New("node ID does not match the supplied identity private key")
	}
	for _, participant := range plan.Participants {
		if participant.ID == nodeID {
			if !bytes.Equal(participant.PublicKey, publicKey) {
				return errors.New("identity private key does not match the plan public key")
			}
			return nil
		}
	}
	return errors.New("node ID is not a ceremony participant")
}

func verifyEncryptionIdentity(plan *ceremony.Plan, nodeID string, privateKey *btcec.PrivateKey) error {
	if privateKey == nil {
		return errors.New("one-time ceremony encryption key is unavailable")
	}
	publicKey := privateKey.PubKey().SerializeCompressed()
	for _, participant := range plan.Participants {
		if participant.ID == nodeID {
			if !bytes.Equal(participant.EncryptionPublicKey, publicKey) {
				return errors.New("one-time ceremony encryption key does not match the registered plan key")
			}
			return nil
		}
	}
	return errors.New("node ID is not a ceremony participant")
}

func signIdentityDigest(privateKey *btcec.PrivateKey, digest []byte) ([]byte, error) {
	if privateKey == nil || len(digest) != 32 {
		return nil, errors.New("invalid identity signing input")
	}
	r, s, err := ecdsa.Sign(rand.Reader, privateKey.ToECDSA(), digest)
	if err != nil {
		return nil, errors.New("identity signing failed")
	}
	defer r.SetInt64(0)
	defer s.SetInt64(0)
	halfOrder := new(big.Int).Rsh(new(big.Int).Set(btcec.S256().Params().N), 1)
	defer halfOrder.SetInt64(0)
	if s.Cmp(halfOrder) > 0 {
		s.Sub(btcec.S256().Params().N, s)
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return signature, nil
}

func clearCeremonyResult(result *ceremony.Result) {
	if result == nil {
		return
	}
	for i := range result.Vaults {
		wipe(result.Vaults[i].LocalShare)
	}
}

func allZero(data []byte) bool {
	accumulator := byte(0)
	for _, value := range data {
		accumulator |= value
	}
	return accumulator == 0
}
