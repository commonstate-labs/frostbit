package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"dex/config"
	"dex/frost/ceremony"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"github.com/btcsuite/btcd/btcec/v2"
	"google.golang.org/protobuf/proto"
)

type genesisImportReader struct {
	states map[string]*pb.FrostVaultState
	values map[string]string
}

func (r *genesisImportReader) GetFrostVaultState(key string) (*pb.FrostVaultState, error) {
	state := r.states[key]
	if state == nil {
		return nil, errors.New("not found")
	}
	return proto.Clone(state).(*pb.FrostVaultState), nil
}

func (r *genesisImportReader) Read(key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", errors.New("not found")
	}
	return value, nil
}

type genesisImportStore struct {
	shares          map[string][]byte
	saves           int
	manifest        []byte
	pendingManifest []byte
	manifestWrites  int
	flushErr        error
}

func (s *genesisImportStore) LoadLocalShare(chain string, vaultID uint32, epoch uint64) ([]byte, error) {
	return bytes.Clone(s.shares[fmt.Sprintf("%s/%d/%d", chain, vaultID, epoch)]), nil
}

func (s *genesisImportStore) SaveLocalShare(chain string, vaultID uint32, epoch uint64, share []byte) error {
	s.saves++
	s.shares[fmt.Sprintf("%s/%d/%d", chain, vaultID, epoch)] = bytes.Clone(share)
	return nil
}

func (s *genesisImportStore) Get(key string) ([]byte, error) {
	if key != keys.KeyFrostGenesisImportManifest() {
		return nil, errors.New("not found")
	}
	if len(s.manifest) == 0 {
		return nil, errors.New("not found")
	}
	return bytes.Clone(s.manifest), nil
}

func (s *genesisImportStore) EnqueueSet(key, value string) {
	if key != keys.KeyFrostGenesisImportManifest() {
		return
	}
	clear(s.pendingManifest)
	s.pendingManifest = []byte(value)
}

func (s *genesisImportStore) ForceFlush() error {
	if s.flushErr != nil {
		return s.flushErr
	}
	if len(s.pendingManifest) != 0 {
		clear(s.manifest)
		s.manifest = bytes.Clone(s.pendingManifest)
		clear(s.pendingManifest)
		s.pendingManifest = nil
		s.manifestWrites++
	}
	return nil
}

type genesisImportFixture struct {
	directory  string
	nodeID     string
	otherID    string
	chainID    uint64
	peerDigest []byte
	wrapKey    []byte
	config     *config.Config
	manifest   *ceremony.PublicManifest
	reader     *genesisImportReader
	store      *genesisImportStore
	identities map[string]*btcec.PrivateKey
	                                                                          
	networkParticipants []ceremony.Participant
}

func newGenesisImportFixture(t *testing.T, planThreshold uint32) *genesisImportFixture {
	return newGenesisImportFixtureWithFormat(t, planThreshold, 771, false)
}

func newGenesisImportFixtureWithFormat(t *testing.T, planThreshold uint32, chainID uint64, plaintext bool) *genesisImportFixture {
	return newGenesisImportFixtureWithAssignment(t, planThreshold, chainID, plaintext, false)
}

                                                                              
func newGenesisImportFixtureWithAssignment(t *testing.T, planThreshold uint32, chainID uint64, plaintext, oldRotation bool) *genesisImportFixture {
	participantCount := 4
	if chainID == ceremony.PlaintextGenesisChainID {
		participantCount = 5
	}
	return newGenesisImportFixtureWithRoster(t, planThreshold, chainID, plaintext, oldRotation, participantCount)
}

func newGenesisImportFixtureWithRoster(t *testing.T, planThreshold uint32, chainID uint64, plaintext, oldRotation bool, participantCount int) *genesisImportFixture {
	t.Helper()
	peerDigest := bytes.Repeat([]byte{0x31}, 32)
	wrapKey := bytes.Repeat([]byte{0x42}, 32)
	groupPrivate, groupPublicKey := btcec.PrivKeyFromBytes(append(bytes.Repeat([]byte{0}, 31), 1))
	groupPublic := groupPrivate.PubKey().SerializeCompressed()
	groupPrivate.Zero()
	if !bytes.Equal(groupPublic, groupPublicKey.SerializeCompressed()) {
		t.Fatal("deterministic group public key mismatch")
	}
	aggregateCommitments := make([][]byte, planThreshold)
	for i := range aggregateCommitments {
		aggregateCommitments[i] = bytes.Clone(groupPublic)
	}
	bnGroup := curve.NewBN256Group()
	accountGroupPublic := bnGroup.SerializePoint(bnGroup.ScalarBaseMult(big.NewInt(1)))
	accountAggregateCommitments := make([][]byte, planThreshold)
	for i := range accountAggregateCommitments {
		accountAggregateCommitments[i] = bytes.Clone(accountGroupPublic)
	}

	cfg := config.DefaultConfig()
	cfg.Frost = testDualCustodyFrostConfig()
	cfg.Frost.Chains = map[string]config.ChainFrostConfig{}
	externalChainIDs := map[string]uint64{"arb": 42161, "base": 8453, "bnb": 56, "eth": 1, "trx": 728126428}
	for _, chainName := range ceremony.GenesisCeremonyChainOrder() {
		chainCfg := config.ChainFrostConfig{VaultsPerChain: 1, AuthorityProtocol: 3, DepositProtocol: 2, DepositDexChainID: chainID}
		if chainName == "btc" {
			chainCfg.AuthorityProtocol, chainCfg.DepositProtocol, chainCfg.DepositDexChainID = 0, 0, 0
			chainCfg.SignAlgo = "SCHNORR_SECP256K1_BIP340"
			chainCfg.FrostVariant = "frost-secp256k1"
			chainCfg.BTCNetwork = "mainnet"
			chainCfg.InitialGroupPubkeys = []string{hex.EncodeToString(groupPublic)}
		} else {
			chainCfg.SignAlgo = "SCHNORR_ALT_BN128"
			chainCfg.FrostVariant = "frost-bn128"
			chainCfg.ChainID = externalChainIDs[chainName]
			chainCfg.InitialGroupPubkeys = []string{hex.EncodeToString(accountGroupPublic)}
			if chainName == "trx" {
				tronRef, err := evm.NormalizeAccountAddress("trx", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", false)
				if err != nil {
					t.Fatal(err)
				}
				chainCfg.VaultRefs = []string{tronRef}
			} else {
				chainCfg.VaultRefs = []string{"0x1111111111111111111111111111111111111111"}
			}
		}
		cfg.Frost.Chains[chainName] = chainCfg
	}
	frostDigest, err := ceremony.ComputeFrostConfigDigest(cfg.Frost)
	if err != nil {
		t.Fatal(err)
	}
	plan := &ceremony.Plan{
		Version: ceremony.Version, ID: "import-test", NetworkChainID: chainID,
		PeerManifestDigest: peerDigest, FrostConfigDigest: frostDigest,
		Epoch: 1, Threshold: planThreshold,
	}
	identities := make(map[string]*btcec.PrivateKey)
	encryptionPrivate := make(map[string]*btcec.PrivateKey)
	networkParticipants := make([]ceremony.Participant, 0, participantCount)
	for participantIndex := range participantCount {
		                                                                      
		                                                                    
		var seed [32]byte
		seed[30], seed[31] = byte((participantIndex+9)/256), byte(participantIndex+9)
		identity, _ := btcec.PrivKeyFromBytes(seed[:])
		encryption, err := btcec.NewPrivateKey()
		if err != nil {
			t.Fatal(err)
		}
		address, err := utils.AddressFromSecp256k1PubKey(identity.PubKey().SerializeCompressed())
		if err != nil {
			t.Fatal(err)
		}
		identities[address] = identity
		encryptionPrivate[address] = encryption
		networkParticipants = append(networkParticipants, ceremony.Participant{
			Index: uint64(participantIndex), ID: address, PublicKey: identity.PubKey().SerializeCompressed(),
			EncryptionPublicKey: encryption.PubKey().SerializeCompressed(),
		})
	}
	sort.Slice(networkParticipants, func(i, j int) bool { return networkParticipants[i].ID < networkParticipants[j].ID })
	indices := make([]uint64, len(networkParticipants))
	addresses := make([]string, len(networkParticipants))
	for i := range networkParticipants {
		networkParticipants[i].Index = uint64(i)
		indices[i], addresses[i] = uint64(i), networkParticipants[i].ID
	}
	assignments, err := ceremony.InitialCommitteeAssignments(cfg.Frost, indices, addresses)
	if err != nil {
		t.Fatal(err)
	}
	used := make(map[uint64]bool)
	participation := make(map[string]int)
	for ordinal, chainName := range ceremony.GenesisCeremonyChainOrder() {
		algorithm := pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128
		members := make([]string, 4)
		for offset := range members {
			index := assignments[chainName][0][offset]
			if oldRotation {
				index = uint64((ordinal + offset) % len(networkParticipants))
			}
			members[offset] = networkParticipants[index].ID
			used[index] = true
			participation[members[offset]]++
		}
		if chainName == "btc" {
			algorithm = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
		}
		plan.Vaults = append(plan.Vaults, ceremony.Vault{
			Chain: chainName, VaultID: 0, SignAlgo: algorithm, Committee: members,
		})
	}
	for i, participant := range networkParticipants {
		if used[uint64(i)] {
			plan.Participants = append(plan.Participants, participant)
		}
	}
	nodeID, otherID := plan.Participants[0].ID, plan.Participants[1].ID
	common := make([]string, 0, len(plan.Participants))
	for _, participant := range plan.Participants {
		if participation[participant.ID] == len(plan.Vaults) {
			common = append(common, participant.ID)
		}
	}
	if len(common) > 0 {
		nodeID = common[0]
	}
	if len(common) > 1 {
		otherID = common[1]
	}
	registrationContext, err := ceremony.RegistrationContextDigest(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, participant := range plan.Participants {
		registration := ceremony.Registration{
			Version: ceremony.Version, ContextDigest: bytes.Clone(registrationContext),
			ParticipantID: participant.ID, EncryptionPublicKey: bytes.Clone(participant.EncryptionPublicKey),
		}
		digest, err := ceremony.RegistrationDigest(&registration)
		if err != nil {
			t.Fatal(err)
		}
		registration.Signature = signGenesisImportDigest(t, identities[participant.ID], digest)
		plan.Registrations = append(plan.Registrations, registration)
	}
	planDigest, err := ceremony.PlanDigest(plan)
	if err != nil {
		t.Fatal(err)
	}
	transcriptDigest := bytes.Repeat([]byte{0x53}, 32)
	attestations := make([]*ceremony.PublicAttestation, 0, len(plan.Participants))
	for _, participant := range plan.Participants {
		result := &ceremony.Result{
			Version: ceremony.Version, PlanDigest: planDigest, TranscriptDigest: transcriptDigest,
			ParticipantID: participant.ID, Epoch: 1,
		}
		for _, vault := range plan.Vaults {
			signerIndex := 0
			for memberIndex, member := range vault.Committee {
				if member == participant.ID {
					signerIndex = memberIndex + 1
					break
				}
			}
			if signerIndex == 0 {
				continue
			}
			localShare := genesisImportFixtureShare(signerIndex, planThreshold)
			groupKey := accountGroupPublic
			commitments := accountAggregateCommitments
			if vault.Chain == "btc" {
				groupKey = groupPublic
				commitments = aggregateCommitments
			}
			result.Vaults = append(result.Vaults, ceremony.VaultResult{
				Chain: vault.Chain, VaultID: vault.VaultID, Epoch: 1, SignAlgo: vault.SignAlgo,
				Committee: append([]string(nil), vault.Committee...), SignerIndex: uint32(signerIndex),
				LocalShare: []byte{localShare}, RawGroupPubkey: bytes.Clone(groupKey),
				AggregateCommitments: commitments,
			})
		}
		attestation, err := ceremony.BuildAttestation(plan, result)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := ceremony.AttestationDigest(plan, attestation)
		if err != nil {
			t.Fatal(err)
		}
		attestation.Signature = signGenesisImportDigest(t, identities[participant.ID], digest)
		attestations = append(attestations, attestation)
	}
	manifest, err := ceremony.Assemble(plan, attestations, utils.VerifyECDSASignature)
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest, err := ceremony.ManifestDigest(manifest, utils.VerifyECDSASignature)
	if err != nil {
		t.Fatal(err)
	}
	configuredDigest, err := ceremony.ComputeConfiguredFrostConfigDigest(cfg.Frost)
	if err != nil {
		t.Fatal(err)
	}
	statement := &ceremony.GenesisDeploymentStatement{
		Version: ceremony.GenesisDeploymentVersion, CeremonyManifestDigest: manifestDigest,
		PlanDigest: planDigest, NetworkChainID: chainID, PeerManifestDigest: peerDigest,
		Epoch: 1, ConfiguredFrostDigest: configuredDigest,
	}
	for _, participant := range plan.Participants {
		statement.Participants = append(statement.Participants, participant.ID)
	}
	for i, publicVault := range manifest.Vaults {
		record := ceremony.GenesisDeploymentVault{
			Chain: publicVault.Chain, VaultID: publicVault.VaultID, Epoch: 1,
			SignAlgo: publicVault.SignAlgo, CommitteeDigest: publicVault.CommitteeDigest,
			RawGroupPubkey: publicVault.RawGroupPubkey, Kind: ceremony.GenesisDeploymentKindDerived,
		}
		if publicVault.Chain != "btc" {
			canonicalAuthority, err := evm.CanonicalBN128PublicKey(publicVault.RawGroupPubkey)
			if err != nil {
				t.Fatal(err)
			}
			chainCfg := cfg.Frost.Chains[publicVault.Chain]
			anchor := bytes.Repeat([]byte{byte(0x60 + i)}, 32)
			record.Kind = ceremony.GenesisDeploymentKindContract
			record.ExternalChainID = chainCfg.ChainID
			record.VaultRef = chainCfg.VaultRefs[0]
			record.Contract = "SchnorrExampleV3"
			record.AuthorityProtocol = 3
			record.DepositProtocol = "DEX_VAULT_DEPOSIT_V1"
			record.DEXChainID = chainID
			record.Protocol = "DEX_VAULT_MIXED_WITHDRAW_BATCH_V1"
			record.Deployer = chainCfg.VaultRefs[0]
			record.CanonicalAuthority = canonicalAuthority
			record.TransactionHash = bytes.Clone(anchor)
			record.DeploymentBlockNumber = 100
			record.DeploymentBlockHash = bytes.Clone(anchor)
			record.CreationCodeHash = bytes.Clone(anchor)
			record.RuntimeCodeHash = bytes.Clone(anchor)
			record.ArtifactDigest = bytes.Clone(anchor)
			record.CompilerProvenanceDigest = bytes.Clone(anchor)
			record.FinalityPolicy = ceremony.GenesisFinalityRPCFinalized
			record.FinalityBlockNumber = record.DeploymentBlockNumber
			record.FinalityBlockHash = bytes.Clone(anchor)
			record.MinimumConfirmations = 1
			if publicVault.Chain == "trx" {
				record.Contract = "SchnorrExampleTronV3"
				record.NonstandardToken = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
				record.FinalityPolicy = ceremony.GenesisFinalityTronSolidity
			}
		}
		record.GuardianPubkey, err = cfg.Frost.GuardianPublicKey(record.Chain)
		if err != nil {
			t.Fatal(err)
		}
		statement.Vaults = append(statement.Vaults, record)
	}
	statementDigest, err := ceremony.GenesisDeploymentStatementDigest(statement)
	if err != nil {
		t.Fatal(err)
	}
	approvals := make([]*ceremony.GenesisDeploymentApproval, 0, len(plan.Participants))
	for _, participant := range plan.Participants {
		approval := &ceremony.GenesisDeploymentApproval{
			Version: ceremony.GenesisDeploymentVersion, StatementDigest: statementDigest, ParticipantID: participant.ID,
		}
		approvalDigest, err := ceremony.GenesisDeploymentApprovalDigest(approval)
		if err != nil {
			t.Fatal(err)
		}
		approval.Signature = signGenesisImportDigest(t, identities[participant.ID], approvalDigest)
		approvals = append(approvals, approval)
	}
	deploymentProof, err := ceremony.AssembleGenesisDeploymentManifest(statement, approvals, plan.Participants, utils.VerifyECDSASignature)
	if err != nil {
		t.Fatal(err)
	}
	proofJSON, err := json.Marshal(deploymentProof)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Frost.GenesisDeploymentManifest = proofJSON
	configuredMarker, err := configuredFrostGenesisMarkerValue(cfg.Frost)
	if err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	writeGenesisImportJSON(t, filepath.Join(directory, genesisManifestName), manifest)
	for _, publicVault := range manifest.Vaults {
		if !containsString(publicVault.Committee, nodeID) {
			continue
		}
		signerIndex := 0
		for i, member := range publicVault.Committee {
			if member == nodeID {
				signerIndex = i + 1
				break
			}
		}
		localShare := []byte{genesisImportFixtureShare(signerIndex, planThreshold)}
		verificationShare, err := manifestVerificationShareForNode(
			manifest, nodeID, publicVault.Chain, publicVault.VaultID,
		)
		if err != nil {
			t.Fatal(err)
		}
		metadata := ceremony.ShareMetadata{
			CeremonyDigest: hex.EncodeToString(transcriptDigest), NodeID: nodeID,
			Chain: publicVault.Chain, VaultID: publicVault.VaultID, Epoch: 1,
			SignAlgo:          publicVault.SignAlgo,
			CommitteeDigest:   hex.EncodeToString(publicVault.CommitteeDigest),
			GroupPubkey:       bytes.Clone(publicVault.RawGroupPubkey),
			VerificationShare: verificationShare,
		}
		var sealed []byte
		if plaintext {
			sealed, err = ceremony.EncodePlaintextGenesisShare(metadata, localShare)
		} else {
			sealed, err = ceremony.SealShare(metadata, localShare, wrapKey)
		}
		clear(verificationShare)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, genesisShareFilename(publicVault.Chain, publicVault.VaultID)), sealed, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	reader := &genesisImportReader{
		states: map[string]*pb.FrostVaultState{},
		values: map[string]string{
			keys.KeyFrostConfiguredGenesisMarker(): configuredMarker,
		},
	}
	vaultConfigs := buildVaultConfigs(cfg.Frost, len(networkParticipants))
	for i, publicVault := range manifest.Vaults {
		stateConfigBytes, err := proto.Marshal(vaultConfigs[publicVault.Chain])
		if err != nil {
			t.Fatal(err)
		}
		reader.values[keys.KeyFrostVaultConfig(publicVault.Chain, publicVault.VaultID)] = string(stateConfigBytes)
		reader.states[keys.KeyFrostVaultState(publicVault.Chain, publicVault.VaultID)] = &pb.FrostVaultState{
			Chain: publicVault.Chain, VaultId: publicVault.VaultID, KeyEpoch: 1, Status: "ACTIVE",
			SignAlgo: publicVault.SignAlgo, GroupPubkey: bytes.Clone(publicVault.RawGroupPubkey),
			CommitteeMembers: append([]string(nil), plan.Vaults[i].Committee...), CommitteeThreshold: 3,
		}
	}
	fixture := &genesisImportFixture{
		directory: directory, nodeID: nodeID, otherID: otherID,
		chainID: chainID, peerDigest: peerDigest, wrapKey: wrapKey, config: cfg,
		manifest: manifest, reader: reader, store: &genesisImportStore{shares: make(map[string][]byte)},
		identities: identities, networkParticipants: networkParticipants,
	}
	t.Cleanup(func() {
		for _, private := range identities {
			private.Zero()
		}
		for _, private := range encryptionPrivate {
			private.Zero()
		}
		clear(wrapKey)
	})
	return fixture
}

func genesisImportFixtureShare(signerIndex int, threshold uint32) byte {
	share, power := 0, 1
	for range threshold {
		share += power
		power *= signerIndex
	}
	return byte(share)
}

func genesisImportNodeShareForTest(t *testing.T, fixture *genesisImportFixture, nodeID, chain string) byte {
	t.Helper()
	for _, vault := range fixture.manifest.Vaults {
		if vault.Chain == chain {
			for i, member := range vault.Committee {
				if member == nodeID {
					return genesisImportFixtureShare(i+1, fixture.manifest.Plan.Threshold)
				}
			}
		}
	}
	t.Fatalf("fixture node %s has no %s share", nodeID, chain)
	return 0
}

func signGenesisImportDigest(t *testing.T, private *btcec.PrivateKey, digest []byte) []byte {
	t.Helper()
	r, s, err := ecdsa.Sign(rand.Reader, private.ToECDSA(), digest)
	if err != nil {
		t.Fatal(err)
	}
	if s.Cmp(new(big.Int).Rsh(new(big.Int).Set(btcec.S256().Params().N), 1)) > 0 {
		s.Sub(btcec.S256().Params().N, s)
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return signature
}

func writeGenesisImportJSON(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runGenesisImport(fixture *genesisImportFixture) error {
	return importFrostGenesisSharesFromDir(
		fixture.directory, fixture.nodeID, fixture.chainID, fixture.peerDigest, fixture.config,
		fixture.reader, fixture.store, fixture.store, fixture.wrapKey,
	)
}

func runGenesisPreflight(fixture *genesisImportFixture, nodeID string, store *genesisImportStore) error {
	return ensureConfiguredGenesisLocalShares(
		fixture.config, nodeID, fixture.chainID, fixture.peerDigest,
		fixture.reader, store, store,
	)
}

func TestPlaintextFrostGenesisImportIsExplicitAndBoundTo51025(t *testing.T) {
	fixture := newGenesisImportFixtureWithFormat(t, 3, ceremony.PlaintextGenesisChainID, true)
	if err := runGenesisImport(fixture); err == nil || fixture.store.saves != 0 {
		t.Fatal("default sealed importer accepted plaintext material")
	}
	importPlaintext := func() error {
		return importFrostGenesisSharesFromDirWithFormat(
			fixture.directory, fixture.nodeID, fixture.chainID, fixture.peerDigest,
			fixture.config, fixture.reader, fixture.store, fixture.store, nil, true,
		)
	}
	if err := importPlaintext(); err != nil {
		t.Fatalf("import plaintext genesis shares: %v", err)
	}
	if fixture.store.saves != 6 || fixture.store.manifestWrites != 1 {
		t.Fatalf("plaintext import saves=%d receiptWrites=%d", fixture.store.saves, fixture.store.manifestWrites)
	}
	if err := importPlaintext(); err != nil {
		t.Fatalf("idempotent plaintext import: %v", err)
	}
	if fixture.store.saves != 6 || fixture.store.manifestWrites != 1 {
		t.Fatal("idempotent plaintext import wrote a second copy")
	}
	if err := runGenesisPreflight(fixture, fixture.nodeID, fixture.store); err != nil {
		t.Fatalf("signed receipt preflight after plaintext import: %v", err)
	}
	if err := importFrostGenesisSharesFromDirWithFormat(
		fixture.directory, fixture.nodeID, 771, fixture.peerDigest,
		fixture.config, fixture.reader, fixture.store, fixture.store, nil, true,
	); err == nil {
		t.Fatal("plaintext importer accepted another DEX chain")
	}
	if err := importFrostGenesisSharesFromDirWithFormat(
		fixture.directory, fixture.nodeID, fixture.chainID, fixture.peerDigest,
		fixture.config, fixture.reader, fixture.store, fixture.store, fixture.wrapKey, true,
	); err == nil {
		t.Fatal("plaintext importer accepted a wrap key")
	}
}

func TestFrostGenesisShareImportAndIdempotency(t *testing.T) {
	fixture := newGenesisImportFixture(t, 3)
	if err := runGenesisImport(fixture); err != nil {
		t.Fatal(err)
	}
	imported := fixture.store.shares["btc/0/1"]
	wantShare := genesisImportNodeShareForTest(t, fixture, fixture.nodeID, "btc")
	if fixture.store.saves != 6 || len(imported) != 32 || imported[31] != wantShare || !bytes.Equal(imported[:31], make([]byte, 31)) {
		t.Fatal("share was not imported")
	}
	if fixture.store.manifestWrites != 1 || len(fixture.store.manifest) == 0 {
		t.Fatal("signed standalone-genesis import receipt was not persisted")
	}
	if err := runGenesisImport(fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.store.saves != 6 || fixture.store.manifestWrites != 1 {
		t.Fatal("idempotent import rewrote the local share or signed receipt")
	}
}

func TestFrostGenesisUnusedValidatorNeedsNoLocalShare(t *testing.T) {
	fixture := newGenesisImportFixtureWithRoster(t, 3, ceremony.PlaintextGenesisChainID, true, false, 25)
	used := make(map[string]bool, len(fixture.manifest.Plan.Participants))
	for _, participant := range fixture.manifest.Plan.Participants {
		used[participant.ID] = true
	}
	unused := 0
	for _, participant := range fixture.networkParticipants {
		if used[participant.ID] {
			if err := runGenesisPreflight(fixture, participant.ID, fixture.store); err == nil {
				t.Fatal("a selected signer started without its authenticated local share")
			}
			continue
		}
		unused++
		if err := runGenesisPreflight(fixture, participant.ID, fixture.store); err != nil {
			t.Fatalf("genuinely unused validator incorrectly requires a local share: %v", err)
		}
		if err := importFrostGenesisSharesFromDirWithFormat(fixture.directory, participant.ID, fixture.chainID, fixture.peerDigest,
			fixture.config, fixture.reader, fixture.store, fixture.store, nil, true); err == nil {
			t.Fatal("an unused validator imported another signer's bundle")
		}
	}
	if unused == 0 {
		t.Fatal("the full-roster fixture did not exercise an unused validator")
	}
	if fixture.store.saves != 0 || fixture.store.manifestWrites != 0 {
		t.Fatal("preflight or rejected import wrote a local custody artifact")
	}
}

func TestFrostGenesisShareImportConsumesCeremonyNodeBundle(t *testing.T) {
	fixture := newGenesisImportFixture(t, 3)
	source := filepath.Join(t.TempDir(), "sealed-source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, vault := range fixture.manifest.Vaults {
		shareName := genesisShareFilename(vault.Chain, vault.VaultID)
		sealed, err := os.ReadFile(filepath.Join(fixture.directory, shareName))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, shareName), sealed, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(source, shareName), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bundle := filepath.Join(t.TempDir(), "node-bundle")
	if err := ceremony.WriteNodeBundle(bundle, fixture.manifest, fixture.nodeID, source, utils.VerifyECDSASignature); err != nil {
		t.Fatalf("assemble node bundle: %v", err)
	}
	fixture.directory = bundle
	if err := runGenesisImport(fixture); err != nil {
		t.Fatalf("import assembled node bundle: %v", err)
	}
	if got := fixture.store.shares["btc/0/1"]; len(got) != 32 || got[31] != genesisImportNodeShareForTest(t, fixture, fixture.nodeID, "btc") {
		t.Fatal("bundle share was not imported")
	}
}

func TestFrostGenesisShareImportRejectsConflictAndMismatches(t *testing.T) {
	t.Run("conflicting local share", func(t *testing.T) {
		fixture := newGenesisImportFixture(t, 3)
		fixture.store.shares["btc/0/1"] = append(bytes.Repeat([]byte{0}, 31), 4)
		if err := runGenesisImport(fixture); err == nil {
			t.Fatal("conflicting local share accepted")
		}
	})
	t.Run("wrong vault state", func(t *testing.T) {
		fixture := newGenesisImportFixture(t, 3)
		fixture.reader.states[keys.KeyFrostVaultState("btc", 0)].CommitteeMembers[0] = fixture.otherID
		if err := runGenesisImport(fixture); err == nil {
			t.Fatal("mismatched vault state accepted")
		}
	})
	t.Run("wrong trusted peer digest", func(t *testing.T) {
		fixture := newGenesisImportFixture(t, 3)
		fixture.peerDigest[0] ^= 1
		if err := runGenesisImport(fixture); err == nil {
			t.Fatal("wrong environment digest accepted")
		}
	})
	t.Run("threshold downgrade", func(t *testing.T) {
		fixture := newGenesisImportFixture(t, 3)
		fixture.manifest.Plan.Threshold = 2
		writeGenesisImportJSON(t, filepath.Join(fixture.directory, genesisManifestName), fixture.manifest)
		if err := runGenesisImport(fixture); err == nil {
			t.Fatal("downgraded ceremony threshold accepted")
		}
	})
	t.Run("unknown local node", func(t *testing.T) {
		fixture := newGenesisImportFixture(t, 3)
		fixture.nodeID = "fbc1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqmql8k8"
		if err := runGenesisImport(fixture); err == nil {
			t.Fatal("unknown local node accepted")
		}
	})
	t.Run("sealed share for another node", func(t *testing.T) {
		fixture := newGenesisImportFixture(t, 3)
		vault := *manifestVault(fixture.manifest, "btc", 0)
		verificationShare, err := manifestVerificationShareForNode(fixture.manifest, fixture.otherID, vault.Chain, vault.VaultID)
		if err != nil {
			t.Fatal(err)
		}
		sealed, err := ceremony.SealShare(ceremony.ShareMetadata{
			CeremonyDigest: hex.EncodeToString(fixture.manifest.TranscriptDigest), NodeID: fixture.otherID,
			Chain: vault.Chain, VaultID: vault.VaultID, Epoch: vault.Epoch, SignAlgo: vault.SignAlgo,
			CommitteeDigest: hex.EncodeToString(vault.CommitteeDigest), GroupPubkey: vault.RawGroupPubkey,
			VerificationShare: verificationShare,
		}, []byte{genesisImportNodeShareForTest(t, fixture, fixture.otherID, "btc")}, fixture.wrapKey)
		clear(verificationShare)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixture.directory, genesisShareFilename("btc", 0)), sealed, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runGenesisImport(fixture); err == nil {
			t.Fatal("share sealed for another node accepted")
		}
	})
	t.Run("tampered manifest", func(t *testing.T) {
		fixture := newGenesisImportFixture(t, 3)
		fixture.manifest.Attestations[0].Signature[0] ^= 1
		writeGenesisImportJSON(t, filepath.Join(fixture.directory, genesisManifestName), fixture.manifest)
		if err := runGenesisImport(fixture); err == nil {
			t.Fatal("tampered public manifest accepted")
		}
	})
	t.Run("resigned verification share cannot substitute sealed scalar", func(t *testing.T) {
		fixture := newGenesisImportFixture(t, 3)
		attestation := &fixture.manifest.Attestations[0]
		wrongPoint, err := ceremony.LocalShareVerificationPoint(
			"btc", pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, []byte{4},
		)
		if err != nil {
			t.Fatal(err)
		}
		attestation.Vaults[0].VerificationShare = wrongPoint
		attestation.Signature = nil
		if _, err := ceremony.AttestationDigest(&fixture.manifest.Plan, attestation); err == nil {
			t.Fatal("verification share detached from aggregate commitments")
		}
		writeGenesisImportJSON(t, filepath.Join(fixture.directory, genesisManifestName), fixture.manifest)
		if err := runGenesisImport(fixture); err == nil {
			t.Fatal("sealed scalar that disagrees with the signed verification point was accepted")
		}
		if fixture.store.saves != 0 {
			t.Fatal("verification-share mismatch wrote a local share")
		}
	})
}

func TestFrostGenesisShareImportRejectsMissingAndExtraFiles(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		fixture := newGenesisImportFixture(t, 3)
		if err := os.Remove(filepath.Join(fixture.directory, genesisShareFilename("btc", 0))); err != nil {
			t.Fatal(err)
		}
		if err := runGenesisImport(fixture); err == nil {
			t.Fatal("missing share accepted")
		}
	})
	t.Run("extra", func(t *testing.T) {
		fixture := newGenesisImportFixture(t, 3)
		if err := os.WriteFile(filepath.Join(fixture.directory, "extra.share.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runGenesisImport(fixture); err == nil {
			t.Fatal("extra share accepted")
		}
	})
}

func TestEnsureConfiguredGenesisLocalShares(t *testing.T) {
	fixture := newGenesisImportFixture(t, 3)
	if err := runGenesisPreflight(fixture, fixture.nodeID, fixture.store); err == nil {
		t.Fatal("committee node without local share passed preflight")
	}
	fixture.store.shares["btc/0/1"] = append(bytes.Repeat([]byte{0}, 31), 2)
	if err := runGenesisPreflight(fixture, fixture.nodeID, fixture.store); err == nil {
		t.Fatal("valid-looking scalar without a signed import receipt passed preflight")
	}
	delete(fixture.store.shares, "btc/0/1")
	if err := runGenesisImport(fixture); err != nil {
		t.Fatal(err)
	}
	if err := runGenesisPreflight(fixture, fixture.nodeID, fixture.store); err != nil {
		t.Fatal("safe restart without the ceremony directory failed", err)
	}
	if err := runGenesisPreflight(fixture, "observer", &genesisImportStore{shares: map[string][]byte{}}); err != nil {
		t.Fatal("observer unexpectedly required a share", err)
	}
	fixture.store.shares["btc/0/1"] = make([]byte, 32)
	if err := runGenesisPreflight(fixture, fixture.nodeID, fixture.store); err == nil {
		t.Fatal("zero local scalar passed preflight")
	}
}

func TestConfiguredGenesisReceiptRejectsTamperAndStaleShare(t *testing.T) {
	t.Run("tampered persisted manifest", func(t *testing.T) {
		fixture := newGenesisImportFixture(t, 3)
		if err := runGenesisImport(fixture); err != nil {
			t.Fatal(err)
		}
		fixture.store.manifest[len(fixture.store.manifest)/2] ^= 1
		if err := runGenesisPreflight(fixture, fixture.nodeID, fixture.store); err == nil {
			t.Fatal("tampered persisted ceremony manifest passed restart preflight")
		}
	})

	t.Run("valid but stale scalar", func(t *testing.T) {
		fixture := newGenesisImportFixture(t, 3)
		if err := runGenesisImport(fixture); err != nil {
			t.Fatal(err)
		}
		fixture.store.shares["btc/0/1"] = append(bytes.Repeat([]byte{0}, 31), 4)
		if err := runGenesisPreflight(fixture, fixture.nodeID, fixture.store); err == nil {
			t.Fatal("valid scalar that disagrees with the signed verification share passed preflight")
		}
	})

	t.Run("changed trusted environment", func(t *testing.T) {
		fixture := newGenesisImportFixture(t, 3)
		if err := runGenesisImport(fixture); err != nil {
			t.Fatal(err)
		}
		fixture.peerDigest[0] ^= 1
		if err := runGenesisPreflight(fixture, fixture.nodeID, fixture.store); err == nil {
			t.Fatal("receipt from a different peer manifest passed restart preflight")
		}
	})
}

func TestConfiguredGenesisConsensusMarkerPreventsLegacyDowngrade(t *testing.T) {
	fixture := newGenesisImportFixture(t, 3)
	if err := runGenesisImport(fixture); err != nil {
		t.Fatal(err)
	}
	chainCfg := fixture.config.Frost.Chains["btc"]
	chainCfg.InitialGroupPubkeys = nil
	fixture.config.Frost.Chains["btc"] = chainCfg
	fixture.store.shares["btc/0/1"] = append(bytes.Repeat([]byte{0}, 31), 3)
	if err := runGenesisPreflight(fixture, fixture.nodeID, fixture.store); err == nil {
		t.Fatal("standalone-DKG state was downgraded to legacy mode with an arbitrary valid scalar")
	}
}

func TestConfiguredGenesisImportRequiresExactConsensusMarkerBeforeWrites(t *testing.T) {
	for _, test := range []struct {
		name   string
		marker *string
	}{
		{name: "missing"},
		{name: "conflicting", marker: func() *string { value := "unknown-mode"; return &value }()},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newGenesisImportFixture(t, 3)
			if test.marker == nil {
				delete(fixture.reader.values, keys.KeyFrostConfiguredGenesisMarker())
			} else {
				fixture.reader.values[keys.KeyFrostConfiguredGenesisMarker()] = *test.marker
			}
			if err := runGenesisImport(fixture); err == nil {
				t.Fatal("standalone-genesis import accepted a missing or conflicting consensus marker")
			}
			if fixture.store.saves != 0 || len(fixture.store.manifest) != 0 {
				t.Fatal("marker mismatch was detected after local import state was written")
			}
			if err := runGenesisPreflight(fixture, fixture.nodeID, fixture.store); err == nil {
				t.Fatal("configured-genesis preflight accepted a missing or conflicting consensus marker")
			}
		})
	}
}

func TestFrostGenesisReceiptConflictFailsBeforeShareWrites(t *testing.T) {
	fixture := newGenesisImportFixture(t, 3)
	other := newGenesisImportFixture(t, 3)
	if err := runGenesisImport(other); err != nil {
		t.Fatal(err)
	}
	fixture.store.manifest = bytes.Clone(other.store.manifest)
	if err := runGenesisImport(fixture); err == nil {
		t.Fatal("conflicting persisted ceremony receipt was accepted")
	}
	if fixture.store.saves != 0 {
		t.Fatal("conflicting receipt was detected only after writing a share")
	}
}

func TestFrostGenesisReceiptFlushFailureIsSafelyRetryable(t *testing.T) {
	fixture := newGenesisImportFixture(t, 3)
	fixture.store.flushErr = errors.New("injected flush failure")
	if err := runGenesisImport(fixture); err == nil {
		t.Fatal("receipt flush failure was ignored")
	}
	if fixture.store.saves != 6 || len(fixture.store.manifest) != 0 {
		t.Fatal("expected durable shares without a committed receipt after interrupted import")
	}
	if err := runGenesisPreflight(fixture, fixture.nodeID, fixture.store); err == nil {
		t.Fatal("shares without a committed signed receipt passed restart preflight")
	}
	fixture.store.flushErr = nil
	if err := runGenesisImport(fixture); err != nil {
		t.Fatal("retry after receipt flush failure did not recover", err)
	}
	if fixture.store.saves != 6 || fixture.store.manifestWrites != 1 {
		t.Fatal("retry rewrote the shares or failed to commit exactly one receipt")
	}
}

func TestConfiguredGenesisPreflightAfterLegitimateRotation(t *testing.T) {
	fixture := newGenesisImportFixture(t, 3)
	for _, state := range fixture.reader.states {
		state.KeyEpoch = 2
		fixture.store.shares[fmt.Sprintf("%s/%d/2", state.Chain, state.VaultId)] = append(bytes.Repeat([]byte{0}, 31), 3)
	}
	if err := runGenesisPreflight(fixture, fixture.nodeID, fixture.store); err != nil {
		t.Fatal("epoch rotation was incorrectly blocked by a missing epoch-1 import receipt", err)
	}
	if err := runGenesisImport(fixture); err != nil {
		t.Fatal("genesis directory should not overwrite or block a rotated vault", err)
	}
	if fixture.store.saves != 0 {
		t.Fatal("epoch-1 artifact was restored after rotation")
	}
	if err := runGenesisPreflight(fixture, fixture.nodeID, fixture.store); err != nil {
		t.Fatal("valid current-epoch share failed restart preflight", err)
	}
	delete(fixture.store.shares, "btc/0/2")
	if err := runGenesisPreflight(fixture, fixture.nodeID, fixture.store); err == nil {
		t.Fatal("missing rotated share passed restart preflight")
	}
}
