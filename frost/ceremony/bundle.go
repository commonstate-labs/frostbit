package ceremony

import (
	"bytes"
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
)

const (
	                                                                          
	                                                                    
	NodeBundleManifestFilename = "manifest.json"
	nodeBundleDirectoryMode    = os.FileMode(0o700)
	nodeBundleFileMode         = os.FileMode(0o600)
)

type nodeBundleFile struct {
	name string
	data []byte
}

type NodeBundleShareFormat uint8

const (
	NodeBundleSealed NodeBundleShareFormat = iota
	NodeBundlePlaintextGenesis51025
)

func validateNodeBundleFormat(manifest *PublicManifest, format NodeBundleShareFormat) error {
	switch format {
	case NodeBundleSealed:
		return nil
	case NodeBundlePlaintextGenesis51025:
		if manifest == nil || manifest.Plan.NetworkChainID != PlaintextGenesisChainID {
			return errors.New("plaintext genesis shares are permitted only for DEX chain 51025")
		}
		return nil
	default:
		return errors.New("unsupported node bundle share format")
	}
}

                                                                              
                                                                              
                                          
func NodeBundleShareFilename(chain string, vaultID uint32) (string, error) {
	switch chain {
	case "btc", "eth", "bnb", "base", "arb", "trx":
		return fmt.Sprintf("%s-%d.share.json", chain, vaultID), nil
	default:
		return "", errors.New("unsupported node-bundle share chain")
	}
}

                                                                             
                                                                              
                                                                               
                                                                               
                                                                               
                                                                               
               
func WriteNodeBundle(
	outDir string,
	manifest *PublicManifest,
	nodeID string,
	sealedSharesDir string,
	verify SignatureVerifier,
) error {
	return WriteNodeBundleWithFormat(outDir, manifest, nodeID, sealedSharesDir, NodeBundleSealed, verify)
}

func WriteNodeBundleWithFormat(
	outDir string,
	manifest *PublicManifest,
	nodeID string,
	sharesDir string,
	format NodeBundleShareFormat,
	verify SignatureVerifier,
) error {
	canonical, manifestData, err := canonicalNodeBundleManifest(manifest, verify)
	if err != nil {
		return err
	}
	defer clear(manifestData)
	if err := validateNodeBundleFormat(canonical, format); err != nil {
		return err
	}
	if participantByID(&canonical.Plan, nodeID) == nil {
		return fmt.Errorf("node %q is not a ceremony participant", nodeID)
	}

	expected := make(map[string]PublicVaultResult)
	for _, vault := range canonical.Vaults {
		if memberIndex(vault.Committee, nodeID) == 0 {
			continue
		}
		name, err := NodeBundleShareFilename(vault.Chain, vault.VaultID)
		if err != nil {
			return err
		}
		if _, duplicate := expected[name]; duplicate {
			return fmt.Errorf("duplicate node-bundle share filename %q", name)
		}
		expected[name] = vault
	}
	if len(expected) == 0 {
		return fmt.Errorf("node %q is not assigned to any ceremony vault", nodeID)
	}

	sourceDir, err := secureExistingBundleDirectory(sharesDir)
	if err != nil {
		return fmt.Errorf("inspect sealed-share source directory: %w", err)
	}
	shareFiles, err := loadNodeBundleShares(sourceDir, canonical, nodeID, expected, format)
	if err != nil {
		return err
	}
	defer clearNodeBundleFiles(shareFiles)

	destination, err := secureNewBundlePath(outDir)
	if err != nil {
		return err
	}
	if err := os.Mkdir(destination, nodeBundleDirectoryMode); err != nil {
		return fmt.Errorf("create node bundle directory: %w", err)
	}
	if err := os.Chmod(destination, nodeBundleDirectoryMode); err != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("set node bundle directory permissions: %w", err)
	}

	created := make([]string, 0, len(shareFiles)+1)
	success := false
	defer func() {
		if success {
			return
		}
		for _, path := range created {
			_ = os.Remove(path)
		}
		_ = os.Remove(destination)
	}()
	manifestPath := filepath.Join(destination, NodeBundleManifestFilename)
	if err := writeNodeBundleFile(manifestPath, manifestData); err != nil {
		return err
	}
	created = append(created, manifestPath)
	for _, share := range shareFiles {
		path := filepath.Join(destination, share.name)
		if err := writeNodeBundleFile(path, share.data); err != nil {
			return err
		}
		created = append(created, path)
	}
	if err := syncNodeBundleDirectory(destination); err != nil {
		return err
	}
	if err := syncNodeBundleDirectory(filepath.Dir(destination)); err != nil {
		return err
	}
	success = true
	return nil
}

                                                                            
                                                                              
                                                                             
                                                                             
                                                                       
                                                                         
func VerifyNodeBundle(
	bundleDir string,
	nodeID string,
	wrapKey []byte,
	verify SignatureVerifier,
) (*PublicManifest, error) {
	return VerifyNodeBundleWithFormat(bundleDir, nodeID, wrapKey, NodeBundleSealed, verify)
}

func VerifyNodeBundleWithFormat(
	bundleDir string,
	nodeID string,
	wrapKey []byte,
	format NodeBundleShareFormat,
	verify SignatureVerifier,
) (*PublicManifest, error) {
	if format == NodeBundlePlaintextGenesis51025 && wrapKey != nil {
		return nil, errors.New("plaintext genesis bundle must not receive a wrap key")
	}
	directory, err := secureExistingBundleDirectory(bundleDir)
	if err != nil {
		return nil, fmt.Errorf("inspect node bundle directory: %w", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("list node bundle directory: %w", err)
	}
	if len(entries) == 0 || len(entries) > MaxVaults+1 {
		return nil, errors.New("node bundle has an invalid artifact count")
	}
	entryByName := make(map[string]os.DirEntry, len(entries))
	for _, entry := range entries {
		if _, duplicate := entryByName[entry.Name()]; duplicate {
			return nil, fmt.Errorf("duplicate node bundle entry %q", entry.Name())
		}
		entryByName[entry.Name()] = entry
	}
	manifestEntry := entryByName[NodeBundleManifestFilename]
	if manifestEntry == nil {
		return nil, errors.New("node bundle is missing manifest.json")
	}
	manifestData, err := readSecureBundleSourceFile(directory, manifestEntry, maxManifestJSONSize)
	if err != nil {
		return nil, fmt.Errorf("read node bundle manifest: %w", err)
	}
	defer clear(manifestData)
	manifest, err := StrictDecodeManifest(manifestData)
	if err != nil {
		return nil, fmt.Errorf("decode node bundle manifest: %w", err)
	}
	if err := ValidateManifest(manifest, verify); err != nil {
		return nil, fmt.Errorf("verify node bundle manifest: %w", err)
	}
	if err := validateNodeBundleFormat(manifest, format); err != nil {
		return nil, err
	}
	if participantByID(&manifest.Plan, nodeID) == nil {
		return nil, fmt.Errorf("node %q is not a ceremony participant", nodeID)
	}

	expected := make(map[string]PublicVaultResult)
	for _, vault := range manifest.Vaults {
		if memberIndex(vault.Committee, nodeID) == 0 {
			continue
		}
		name, err := NodeBundleShareFilename(vault.Chain, vault.VaultID)
		if err != nil {
			return nil, err
		}
		if _, duplicate := expected[name]; duplicate {
			return nil, fmt.Errorf("duplicate expected node-bundle share %q", name)
		}
		expected[name] = vault
	}
	if len(expected) == 0 {
		return nil, fmt.Errorf("node %q is not assigned to any ceremony vault", nodeID)
	}
	if len(entries) != len(expected)+1 {
		return nil, fmt.Errorf("node bundle contains %d entries; node requires exactly %d shares plus manifest", len(entries), len(expected))
	}
	for name := range entryByName {
		if name == NodeBundleManifestFilename {
			continue
		}
		if _, ok := expected[name]; !ok {
			return nil, fmt.Errorf("unexpected node bundle entry %q", name)
		}
	}
	for name, vault := range expected {
		entry := entryByName[name]
		if entry == nil {
			return nil, fmt.Errorf("node bundle is missing sealed share %q", name)
		}
		data, err := readSecureBundleSourceFile(directory, entry, maxSealedShareSize)
		if err != nil {
			return nil, fmt.Errorf("read sealed share %q: %w", name, err)
		}
		if err := validateNodeBundleShareMetadataForFormat(data, manifest, nodeID, vault, format); err != nil {
			clear(data)
			return nil, fmt.Errorf("validate sealed share %q: %w", name, err)
		}
		if wrapKey != nil {
			expectedMetadata, err := nodeBundleExpectedShareMetadata(manifest, nodeID, vault)
			if err != nil {
				clear(data)
				return nil, err
			}
			plain, openErr := OpenShare(data, expectedMetadata, wrapKey)
			clear(expectedMetadata.GroupPubkey)
			clear(expectedMetadata.VerificationShare)
			clear(plain)
			if openErr != nil {
				clear(data)
				return nil, fmt.Errorf("open sealed share %q: %w", name, openErr)
			}
		}
		clear(data)
	}
	return manifest, nil
}

func canonicalNodeBundleManifest(manifest *PublicManifest, verify SignatureVerifier) (*PublicManifest, []byte, error) {
	if err := ValidateManifest(manifest, verify); err != nil {
		return nil, nil, fmt.Errorf("verify node-bundle manifest: %w", err)
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, nil, err
	}
	canonical, err := StrictDecodeManifest(raw)
	clear(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("freeze node-bundle manifest: %w", err)
	}
	if err := ValidateManifest(canonical, verify); err != nil {
		return nil, nil, fmt.Errorf("reverify node-bundle manifest: %w", err)
	}
	formatted, err := json.MarshalIndent(canonical, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	formatted = append(formatted, '\n')
	if len(formatted) > maxManifestJSONSize {
		clear(formatted)
		return nil, nil, errors.New("node-bundle manifest exceeds the size limit")
	}
	return canonical, formatted, nil
}

func loadNodeBundleShares(
	directory string,
	manifest *PublicManifest,
	nodeID string,
	expected map[string]PublicVaultResult,
	format NodeBundleShareFormat,
) ([]nodeBundleFile, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("list sealed-share source directory: %w", err)
	}
	if len(entries) != len(expected) {
		return nil, fmt.Errorf("sealed-share source contains %d entries; node requires exactly %d", len(entries), len(expected))
	}
	byName := make(map[string]nodeBundleFile, len(entries))
	loaded := false
	defer func() {
		if loaded {
			return
		}
		for _, file := range byName {
			clear(file.data)
		}
	}()
	for _, entry := range entries {
		vault, ok := expected[entry.Name()]
		if !ok {
			return nil, fmt.Errorf("unexpected sealed-share source entry %q", entry.Name())
		}
		data, err := readSecureBundleSourceFile(directory, entry, maxSealedShareSize)
		if err != nil {
			return nil, err
		}
		if err := validateNodeBundleShareMetadataForFormat(data, manifest, nodeID, vault, format); err != nil {
			clear(data)
			return nil, fmt.Errorf("validate sealed share %q: %w", entry.Name(), err)
		}
		byName[entry.Name()] = nodeBundleFile{name: entry.Name(), data: data}
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	files := make([]nodeBundleFile, 0, len(names))
	for _, name := range names {
		files = append(files, byName[name])
	}
	loaded = true
	return files, nil
}

func validateNodeBundleShareMetadataForFormat(data []byte, manifest *PublicManifest, nodeID string, vault PublicVaultResult, format NodeBundleShareFormat) error {
	switch format {
	case NodeBundleSealed:
		return validateNodeBundleShareMetadata(data, manifest, nodeID, vault)
	case NodeBundlePlaintextGenesis51025:
		expected, err := nodeBundleExpectedShareMetadata(manifest, nodeID, vault)
		if err != nil {
			return err
		}
		defer clear(expected.GroupPubkey)
		defer clear(expected.VerificationShare)
		share, err := OpenPlaintextGenesisShare(data, expected)
		clear(share)
		return err
	default:
		return errors.New("unsupported node bundle share format")
	}
}

func validateNodeBundleShareMetadata(data []byte, manifest *PublicManifest, nodeID string, vault PublicVaultResult) error {
	envelope, err := decodeSealedShare(data)
	if err != nil {
		return err
	}
	if len(envelope.Nonce) != sealedShareNonceSize || len(envelope.Ciphertext) != shareScalarSize+sealedShareTagSize {
		return errors.New("invalid sealed-share nonce or ciphertext length")
	}
	expected, err := nodeBundleExpectedShareMetadata(manifest, nodeID, vault)
	if err != nil {
		return err
	}
	defer clear(expected.GroupPubkey)
	defer clear(expected.VerificationShare)
	want, err := shareMetadataAAD(expected)
	if err != nil {
		return err
	}
	defer clear(want)
	got, err := shareMetadataAAD(envelope.Metadata)
	if err != nil {
		return err
	}
	defer clear(got)
	if !bytes.Equal(got, want) {
		return errors.New("sealed-share metadata does not match manifest committee assignment")
	}
	return nil
}

func nodeBundleExpectedShareMetadata(manifest *PublicManifest, nodeID string, vault PublicVaultResult) (ShareMetadata, error) {
	verificationShare, err := manifestParticipantVerificationShare(manifest, nodeID, vault.Chain, vault.VaultID)
	if err != nil {
		return ShareMetadata{}, err
	}
	return ShareMetadata{
		CeremonyDigest:    hex.EncodeToString(manifest.TranscriptDigest),
		NodeID:            nodeID,
		Chain:             vault.Chain,
		VaultID:           vault.VaultID,
		Epoch:             vault.Epoch,
		SignAlgo:          vault.SignAlgo,
		CommitteeDigest:   hex.EncodeToString(vault.CommitteeDigest),
		GroupPubkey:       bytes.Clone(vault.RawGroupPubkey),
		VerificationShare: verificationShare,
	}, nil
}

func manifestParticipantVerificationShare(manifest *PublicManifest, nodeID, chain string, vaultID uint32) ([]byte, error) {
	if manifest == nil {
		return nil, errors.New("nil ceremony manifest")
	}
	for i := range manifest.Attestations {
		attestation := &manifest.Attestations[i]
		if attestation.ParticipantID != nodeID {
			continue
		}
		vault := findAttestedVault(attestation, chain, vaultID)
		if vault == nil || len(vault.VerificationShare) == 0 {
			return nil, fmt.Errorf("manifest lacks %s's verification share for %s/%d", nodeID, chain, vaultID)
		}
		return bytes.Clone(vault.VerificationShare), nil
	}
	return nil, fmt.Errorf("manifest lacks participant attestation for %s", nodeID)
}

func secureExistingBundleDirectory(path string) (string, error) {
	abs, err := cleanBundlePath(path)
	if err != nil {
		return "", err
	}
	if err := rejectBundleSymlinkAncestors(abs); err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("sealed-share source is not a regular directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("sealed-share source directory is writable by group or others")
	}
	return abs, nil
}

func secureNewBundlePath(path string) (string, error) {
	abs, err := cleanBundlePath(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(abs); err == nil {
		return "", fmt.Errorf("refusing to overwrite existing node bundle %s", abs)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(abs)
	if err := rejectBundleSymlinkAncestors(parent); err != nil {
		return "", err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return "", fmt.Errorf("inspect node bundle parent: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("node bundle parent is not a directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("node bundle parent directory is writable by group or others")
	}
	return abs, nil
}

func cleanBundlePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("node bundle path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func rejectBundleSymlinkAncestors(path string) error {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect node bundle path %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are forbidden in node bundle path %s", current)
		}
		if runtime.GOOS != "windows" && info.IsDir() && info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0 {
			return fmt.Errorf("node bundle path ancestor is writable by group or others: %s", current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func readSecureBundleSourceFile(directory string, entry os.DirEntry, maxBytes int) ([]byte, error) {
	if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
		return nil, fmt.Errorf("sealed-share source entry %q is not a regular file", entry.Name())
	}
	path := filepath.Join(directory, entry.Name())
	before, err := os.Lstat(path)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("sealed-share source entry %q is not a regular file", entry.Name())
	}
	if runtime.GOOS != "windows" && before.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("sealed-share source entry %q is accessible by group or others", entry.Name())
	}
	if before.Size() <= 0 || before.Size() > int64(maxBytes) {
		return nil, fmt.Errorf("sealed-share source entry %q has invalid size", entry.Name())
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, fmt.Errorf("sealed-share source entry %q changed while opening", entry.Name())
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes+1)))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxBytes {
		clear(data)
		return nil, fmt.Errorf("sealed-share source entry %q has invalid size", entry.Name())
	}
	final, err := os.Lstat(path)
	if err != nil || final.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, final) {
		clear(data)
		return nil, fmt.Errorf("sealed-share source entry %q changed while reading", entry.Name())
	}
	return data, nil
}

func writeNodeBundleFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, nodeBundleFileMode)
	if err != nil {
		return fmt.Errorf("create node bundle file %s: %w", filepath.Base(path), err)
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	written, err := file.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Chmod(nodeBundleFileMode); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	keep = true
	return nil
}

func syncNodeBundleDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func clearNodeBundleFiles(files []nodeBundleFile) {
	for i := range files {
		clear(files[i].data)
		files[i].data = nil
	}
}
