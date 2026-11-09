package ceremony

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"runtime"
	"strings"

	"dex/frost/core/curve"
	"dex/pb"
)

const (
	maxPlanJSONSize        = 2 << 20
	maxRound1JSONSize      = 16 << 20
	maxAttestationJSONSize = 2 << 20
	maxManifestJSONSize    = 32 << 20
)

                                                                      
                                                                        
type SignatureVerifier func(publicKey, digest, signature []byte) bool

                                                                             
                                                                             
                                                                         
type PublicAttestation struct {
	Version          uint32              `json:"version"`
	PlanDigest       []byte              `json:"plan_digest"`
	TranscriptDigest []byte              `json:"transcript_digest"`
	ParticipantID    string              `json:"participant_id"`
	Epoch            uint64              `json:"epoch"`
	Vaults           []PublicVaultResult `json:"vaults"`
	Signature        []byte              `json:"signature,omitempty"`
}

                                                                           
                                                                                
type PublicVaultResult struct {
	Chain                string      `json:"chain"`
	VaultID              uint32      `json:"vault_id"`
	Epoch                uint64      `json:"epoch"`
	SignAlgo             pb.SignAlgo `json:"sign_algo"`
	Committee            []string    `json:"committee"`
	SignerIndex          uint32      `json:"signer_index"`
	RawGroupPubkey       []byte      `json:"raw_group_pubkey"`
	AggregateCommitments [][]byte    `json:"aggregate_commitments"`
	VerificationShare    []byte      `json:"verification_share,omitempty"`
	CommitteeDigest      []byte      `json:"committee_digest,omitempty"`
}

                                                                        
                                                                      
                                                                          
type PublicManifest struct {
	Version          uint32              `json:"version"`
	Plan             Plan                `json:"plan"`
	PlanDigest       []byte              `json:"plan_digest"`
	TranscriptDigest []byte              `json:"transcript_digest"`
	Vaults           []PublicVaultResult `json:"vaults"`
	Attestations     []PublicAttestation `json:"attestations"`
}

                                                                              
                                                                              
                                                                              
func ValidateLocalShare(localShare []byte, signAlgo pb.SignAlgo) error {
	if len(localShare) == 0 || len(localShare) > shareScalarSize {
		return errors.New("local share must be a 1..32-byte scalar")
	}
	scalar := make([]byte, shareScalarSize)
	defer clear(scalar)
	copy(scalar[shareScalarSize-len(localShare):], localShare)
	if !validShareScalar(scalar, signAlgo) {
		return errors.New("local share scalar is outside the signing group")
	}
	return nil
}

                                                                               
                                                                               
                                                                                
                                                                    
func LocalShareVerificationPoint(chain string, signAlgo pb.SignAlgo, localShare []byte) ([]byte, error) {
	if err := ValidateLocalShare(localShare, signAlgo); err != nil {
		return nil, err
	}
	group, err := vaultGroup(Vault{Chain: chain, SignAlgo: signAlgo})
	if err != nil {
		return nil, err
	}
	scalar := new(big.Int).SetBytes(localShare)
	defer clearCeremonyBigInt(scalar)
	point := group.ScalarBaseMult(scalar)
	if isInfinity(point) {
		return nil, errors.New("local share derives the point at infinity")
	}
	encoded := group.SerializePoint(point)
	if _, err := decodePoint(group, encoded); err != nil {
		clear(encoded)
		return nil, fmt.Errorf("derive local verification share: %w", err)
	}
	return encoded, nil
}

                                                                                
                                                                    
func VerifyLocalShareAgainstPoint(chain string, signAlgo pb.SignAlgo, localShare, expected []byte) error {
	group, err := vaultGroup(Vault{Chain: chain, SignAlgo: signAlgo})
	if err != nil {
		return err
	}
	if _, err := decodePoint(group, expected); err != nil {
		return fmt.Errorf("invalid local-share verification point: %w", err)
	}
	actual, err := LocalShareVerificationPoint(chain, signAlgo, localShare)
	if err != nil {
		return err
	}
	defer clear(actual)
	if !bytes.Equal(actual, expected) {
		return errors.New("local share does not match the signed ceremony verification point")
	}
	return nil
}

func clearCeremonyBigInt(value *big.Int) {
	if value == nil {
		return
	}
	words := value.Bits()
	for i := range words {
		words[i] = 0
	}
	value.SetInt64(0)
	runtime.KeepAlive(words)
	runtime.KeepAlive(value)
}

                                                                              
                                                   
func ValidateGroupPublicKey(chain string, signAlgo pb.SignAlgo, groupPublicKey []byte) error {
	vault := Vault{Chain: chain, SignAlgo: signAlgo}
	group, err := vaultGroup(vault)
	if err != nil {
		return err
	}
	if _, err := decodePoint(group, groupPublicKey); err != nil {
		return err
	}
	return nil
}

                                                                        
                                                                 
func CommitteeDigest(chain string, vaultID uint32, epoch uint64, signAlgo pb.SignAlgo, committee []string) ([]byte, error) {
	if epoch == 0 || len(committee) == 0 || len(committee) > MaxParticipants {
		return nil, errors.New("invalid committee dimensions")
	}
	vault := Vault{Chain: chain, VaultID: vaultID, SignAlgo: signAlgo, Committee: committee}
	if _, err := vaultGroup(vault); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(committee))
	for _, member := range committee {
		if !validID(member) {
			return nil, errors.New("invalid committee member identity")
		}
		if _, duplicate := seen[member]; duplicate {
			return nil, errors.New("duplicate committee member identity")
		}
		seen[member] = struct{}{}
	}
	return canonicalDigest("DEX_CEREMONY_COMMITTEE_V1", struct {
		Chain     string      `json:"chain"`
		VaultID   uint32      `json:"vault_id"`
		Epoch     uint64      `json:"epoch"`
		SignAlgo  pb.SignAlgo `json:"sign_algo"`
		Committee []string    `json:"committee"`
	}{chain, vaultID, epoch, signAlgo, append([]string(nil), committee...)})
}

                                                                        
                                                                             
func BuildAttestation(plan *Plan, result *Result) (*PublicAttestation, error) {
	if result == nil {
		return nil, errors.New("nil ceremony result")
	}
	att := &PublicAttestation{
		Version:          result.Version,
		PlanDigest:       bytes.Clone(result.PlanDigest),
		TranscriptDigest: bytes.Clone(result.TranscriptDigest),
		ParticipantID:    result.ParticipantID,
		Epoch:            result.Epoch,
		Vaults:           make([]PublicVaultResult, len(result.Vaults)),
	}
	for i, vault := range result.Vaults {
		if err := ValidateLocalShare(vault.LocalShare, vault.SignAlgo); err != nil {
			return nil, fmt.Errorf("build public attestation for %s/%d: %w", vault.Chain, vault.VaultID, err)
		}
		verificationShare, err := LocalShareVerificationPoint(vault.Chain, vault.SignAlgo, vault.LocalShare)
		if err != nil {
			return nil, fmt.Errorf("build verification share for %s/%d: %w", vault.Chain, vault.VaultID, err)
		}
		att.Vaults[i] = PublicVaultResult{
			Chain: vault.Chain, VaultID: vault.VaultID, Epoch: vault.Epoch,
			SignAlgo: vault.SignAlgo, Committee: append([]string(nil), vault.Committee...),
			SignerIndex: vault.SignerIndex, RawGroupPubkey: bytes.Clone(vault.RawGroupPubkey),
			AggregateCommitments: cloneByteSlices(vault.AggregateCommitments), VerificationShare: verificationShare,
		}
	}
	if err := validateAttestationFields(plan, att, false); err != nil {
		return nil, fmt.Errorf("build public attestation: %w", err)
	}
	return att, nil
}

                                                                         
                                                                  
                    
func AttestationDigest(plan *Plan, attestation *PublicAttestation) ([]byte, error) {
	if err := validateAttestationFields(plan, attestation, false); err != nil {
		return nil, err
	}
	unsigned := cloneAttestation(*attestation)
	unsigned.Signature = nil
	return canonicalDigest("DEX_CEREMONY_ATTESTATION_V1", &unsigned)
}

                                                                              
                      
func ValidateAttestation(plan *Plan, attestation *PublicAttestation, verify SignatureVerifier) error {
	if err := validateAttestationFields(plan, attestation, true); err != nil {
		return err
	}
	if !IsCanonicalIdentitySignature(attestation.Signature) {
		return errors.New("participant attestation signature is not canonical low-S ECDSA")
	}
	if verify == nil {
		return errors.New("attestation signature verifier is required")
	}
	digest, err := AttestationDigest(plan, attestation)
	if err != nil {
		return err
	}
	participant := participantByID(plan, attestation.ParticipantID)
	if participant == nil || !verify(participant.PublicKey, digest, attestation.Signature) {
		return errors.New("invalid participant attestation signature")
	}
	return nil
}

func validateAttestationFields(plan *Plan, attestation *PublicAttestation, requireSignature bool) error {
	planDigest, err := PlanDigest(plan)
	if err != nil {
		return err
	}
	if attestation == nil || attestation.Version != Version || attestation.Epoch != plan.Epoch ||
		!bytes.Equal(attestation.PlanDigest, planDigest) || !validDigest(attestation.TranscriptDigest) {
		return errors.New("attestation header does not match plan")
	}
	if requireSignature && len(attestation.Signature) != 64 {
		return errors.New("attestation must contain one 64-byte identity signature")
	}
	if !requireSignature && len(attestation.Signature) != 0 && len(attestation.Signature) != 64 {
		return errors.New("invalid attestation signature length")
	}
	if participantByID(plan, attestation.ParticipantID) == nil {
		return errors.New("attestation participant is not in plan")
	}
	expected := 0
	for _, vault := range plan.Vaults {
		index := memberIndex(vault.Committee, attestation.ParticipantID)
		if index == 0 {
			continue
		}
		if expected >= len(attestation.Vaults) {
			return errors.New("attestation is missing a committee vault")
		}
		got := &attestation.Vaults[expected]
		expected++
		if err := validatePublicVaultResult(got, vault, plan.Epoch, plan.Threshold, uint32(index), false); err != nil {
			return err
		}
	}
	if expected != len(attestation.Vaults) {
		return errors.New("attestation contains an unexpected or reordered vault")
	}
	return nil
}

func validatePublicVaultResult(got *PublicVaultResult, want Vault, epoch uint64, threshold, signerIndex uint32, requireCommitteeDigest bool) error {
	if got == nil || got.Chain != want.Chain || got.VaultID != want.VaultID || got.Epoch != epoch ||
		got.SignAlgo != want.SignAlgo || got.SignerIndex != signerIndex || !equalStrings(got.Committee, want.Committee) {
		return fmt.Errorf("public result does not match planned vault %s", vaultKey(want.Chain, want.VaultID))
	}
	if threshold == 0 || len(got.AggregateCommitments) != int(threshold) {
		return fmt.Errorf("invalid aggregate commitment count for %s", vaultKey(want.Chain, want.VaultID))
	}
	group, err := vaultGroup(want)
	if err != nil {
		return err
	}
	if _, err := decodePoint(group, got.RawGroupPubkey); err != nil {
		return fmt.Errorf("invalid public result group key for %s: %w", vaultKey(want.Chain, want.VaultID), err)
	}
	for i, encoded := range got.AggregateCommitments {
		if _, err := decodePoint(group, encoded); err != nil {
			return fmt.Errorf("invalid aggregate commitment %d for %s: %w", i, vaultKey(want.Chain, want.VaultID), err)
		}
	}
	if !bytes.Equal(got.RawGroupPubkey, got.AggregateCommitments[0]) {
		return fmt.Errorf("group key does not match constant aggregate commitment for %s", vaultKey(want.Chain, want.VaultID))
	}
	if _, err := decodePoint(group, got.VerificationShare); err != nil {
		return fmt.Errorf("invalid participant verification share for %s: %w", vaultKey(want.Chain, want.VaultID), err)
	}
	expectedVerification, err := verificationShareFromCommitments(group, got.AggregateCommitments, signerIndex)
	if err != nil {
		return fmt.Errorf("derive participant verification share for %s: %w", vaultKey(want.Chain, want.VaultID), err)
	}
	defer clear(expectedVerification)
	if !bytes.Equal(got.VerificationShare, expectedVerification) {
		return fmt.Errorf("participant verification share does not match aggregate commitments for %s", vaultKey(want.Chain, want.VaultID))
	}
	digest, err := CommitteeDigest(want.Chain, want.VaultID, epoch, want.SignAlgo, want.Committee)
	if err != nil {
		return err
	}
	if requireCommitteeDigest {
		if !bytes.Equal(got.CommitteeDigest, digest) {
			return fmt.Errorf("committee digest mismatch for %s", vaultKey(want.Chain, want.VaultID))
		}
	} else if len(got.CommitteeDigest) != 0 {
		return fmt.Errorf("participant attestation must not supply assembled committee digest for %s", vaultKey(want.Chain, want.VaultID))
	}
	return nil
}

                                                                          
                                                                              
                                                                  
                                                                    
func verificationShareFromCommitments(group curve.Group, commitments [][]byte, signerIndex uint32) ([]byte, error) {
	if group == nil || len(commitments) == 0 || signerIndex == 0 {
		return nil, errors.New("invalid public polynomial evaluation request")
	}
	x := new(big.Int).SetUint64(uint64(signerIndex))
	defer clearCeremonyBigInt(x)
	power := big.NewInt(1)
	defer clearCeremonyBigInt(power)
	var aggregate curve.Point
	for i, encoded := range commitments {
		point, err := decodePoint(group, encoded)
		if err != nil {
			return nil, fmt.Errorf("commitment %d: %w", i, err)
		}
		term := group.ScalarMult(point, power)
		if isInfinity(term) {
			return nil, fmt.Errorf("commitment %d evaluates to infinity", i)
		}
		if isInfinity(aggregate) {
			aggregate = term
		} else {
			aggregate = group.Add(aggregate, term)
		}
		power.Mul(power, x).Mod(power, group.Order())
	}
	if isInfinity(aggregate) {
		return nil, errors.New("aggregate verification share is the point at infinity")
	}
	encoded := group.SerializePoint(aggregate)
	if _, err := decodePoint(group, encoded); err != nil {
		clear(encoded)
		return nil, err
	}
	return encoded, nil
}

                                                                        
                                                                              
                                                                         
func Assemble(plan *Plan, attestations []*PublicAttestation, verify SignatureVerifier) (*PublicManifest, error) {
	planDigest, err := PlanDigest(plan)
	if err != nil {
		return nil, err
	}
	if len(attestations) != len(plan.Participants) {
		return nil, errors.New("manifest requires exactly one attestation from every participant")
	}
	byParticipant := make(map[string]*PublicAttestation, len(attestations))
	for _, attestation := range attestations {
		if err := ValidateAttestation(plan, attestation, verify); err != nil {
			return nil, err
		}
		if byParticipant[attestation.ParticipantID] != nil {
			return nil, errors.New("duplicate participant attestation")
		}
		byParticipant[attestation.ParticipantID] = attestation
	}
	var transcript []byte
	canonicalAttestations := make([]PublicAttestation, 0, len(plan.Participants))
	for _, participant := range plan.Participants {
		attestation := byParticipant[participant.ID]
		if attestation == nil {
			return nil, fmt.Errorf("missing attestation from %s", participant.ID)
		}
		if transcript == nil {
			transcript = bytes.Clone(attestation.TranscriptDigest)
		} else if !bytes.Equal(transcript, attestation.TranscriptDigest) {
			return nil, errors.New("participants acknowledged different DKG transcripts")
		}
		canonicalAttestations = append(canonicalAttestations, cloneAttestation(*attestation))
	}

	vaultResults := make([]PublicVaultResult, 0, len(plan.Vaults))
	for _, planned := range plan.Vaults {
		var groupKey []byte
		var aggregateCommitments [][]byte
		for _, member := range planned.Committee {
			attestation := byParticipant[member]
			memberResult := findAttestedVault(attestation, planned.Chain, planned.VaultID)
			if memberResult == nil {
				return nil, fmt.Errorf("committee member %s did not attest vault %s", member, vaultKey(planned.Chain, planned.VaultID))
			}
			if groupKey == nil {
				groupKey = bytes.Clone(memberResult.RawGroupPubkey)
				aggregateCommitments = cloneByteSlices(memberResult.AggregateCommitments)
			} else if !bytes.Equal(groupKey, memberResult.RawGroupPubkey) {
				return nil, fmt.Errorf("committee members disagree on group key for %s", vaultKey(planned.Chain, planned.VaultID))
			} else if !equalByteSlices(aggregateCommitments, memberResult.AggregateCommitments) {
				return nil, fmt.Errorf("committee members disagree on aggregate commitments for %s", vaultKey(planned.Chain, planned.VaultID))
			}
		}
		committeeDigest, err := CommitteeDigest(planned.Chain, planned.VaultID, plan.Epoch, planned.SignAlgo, planned.Committee)
		if err != nil {
			return nil, err
		}
		vaultResults = append(vaultResults, PublicVaultResult{
			Chain: planned.Chain, VaultID: planned.VaultID, Epoch: plan.Epoch,
			SignAlgo: planned.SignAlgo, Committee: append([]string(nil), planned.Committee...),
			RawGroupPubkey: groupKey, AggregateCommitments: aggregateCommitments, CommitteeDigest: committeeDigest,
		})
	}
	return &PublicManifest{
		Version: Version, Plan: clonePlan(*plan), PlanDigest: planDigest,
		TranscriptDigest: transcript, Vaults: vaultResults, Attestations: canonicalAttestations,
	}, nil
}

                                                                         
                                                                               
func ValidateManifest(manifest *PublicManifest, verify SignatureVerifier) error {
	if manifest == nil || manifest.Version != Version {
		return errors.New("unsupported public manifest")
	}
	attestations := make([]*PublicAttestation, len(manifest.Attestations))
	for i := range manifest.Attestations {
		attestations[i] = &manifest.Attestations[i]
	}
	rebuilt, err := Assemble(&manifest.Plan, attestations, verify)
	if err != nil {
		return err
	}
	want, err := json.Marshal(rebuilt)
	if err != nil {
		return err
	}
	got, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return errors.New("public manifest is not the canonical assembled result")
	}
	return nil
}

                                                                          
                                                                            
                                                                          
func ManifestDigest(manifest *PublicManifest, verify SignatureVerifier) ([]byte, error) {
	if err := ValidateManifest(manifest, verify); err != nil {
		return nil, err
	}
	return canonicalDigest("DEX_CEREMONY_MANIFEST_V1", manifest)
}

func findAttestedVault(attestation *PublicAttestation, chain string, vaultID uint32) *PublicVaultResult {
	if attestation == nil {
		return nil
	}
	for i := range attestation.Vaults {
		if attestation.Vaults[i].Chain == chain && attestation.Vaults[i].VaultID == vaultID {
			return &attestation.Vaults[i]
		}
	}
	return nil
}

func validDigest(digest []byte) bool {
	if len(digest) != 32 {
		return false
	}
	var nonzero byte
	for _, value := range digest {
		nonzero |= value
	}
	return nonzero != 0
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalByteSlices(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func cloneByteSlices(input [][]byte) [][]byte {
	if input == nil {
		return nil
	}
	result := make([][]byte, len(input))
	for i := range input {
		result[i] = bytes.Clone(input[i])
	}
	return result
}

func clonePlan(plan Plan) Plan {
	result := plan
	result.PeerManifestDigest = bytes.Clone(plan.PeerManifestDigest)
	result.FrostConfigDigest = bytes.Clone(plan.FrostConfigDigest)
	result.Participants = make([]Participant, len(plan.Participants))
	for i, participant := range plan.Participants {
		result.Participants[i] = Participant{
			Index: participant.Index, ID: participant.ID, PublicKey: bytes.Clone(participant.PublicKey),
			EncryptionPublicKey: bytes.Clone(participant.EncryptionPublicKey),
		}
	}
	result.Registrations = make([]Registration, len(plan.Registrations))
	for i, registration := range plan.Registrations {
		result.Registrations[i] = Registration{
			Version: registration.Version, ContextDigest: bytes.Clone(registration.ContextDigest),
			ParticipantID:       registration.ParticipantID,
			EncryptionPublicKey: bytes.Clone(registration.EncryptionPublicKey),
			Signature:           bytes.Clone(registration.Signature),
		}
	}
	result.Vaults = make([]Vault, len(plan.Vaults))
	for i, vault := range plan.Vaults {
		result.Vaults[i] = Vault{Chain: vault.Chain, VaultID: vault.VaultID, SignAlgo: vault.SignAlgo, Committee: append([]string(nil), vault.Committee...)}
	}
	return result
}

func cloneAttestation(attestation PublicAttestation) PublicAttestation {
	result := attestation
	result.PlanDigest = bytes.Clone(attestation.PlanDigest)
	result.TranscriptDigest = bytes.Clone(attestation.TranscriptDigest)
	result.Signature = bytes.Clone(attestation.Signature)
	result.Vaults = make([]PublicVaultResult, len(attestation.Vaults))
	for i, vault := range attestation.Vaults {
		result.Vaults[i] = vault
		result.Vaults[i].Committee = append([]string(nil), vault.Committee...)
		result.Vaults[i].RawGroupPubkey = bytes.Clone(vault.RawGroupPubkey)
		result.Vaults[i].AggregateCommitments = cloneByteSlices(vault.AggregateCommitments)
		result.Vaults[i].VerificationShare = bytes.Clone(vault.VerificationShare)
		result.Vaults[i].CommitteeDigest = bytes.Clone(vault.CommitteeDigest)
	}
	return result
}

                                                                            
                                                                      
func StrictDecodePlan(data []byte) (*Plan, error) {
	var plan Plan
	if err := strictJSON(data, maxPlanJSONSize, &plan); err != nil {
		return nil, err
	}
	if err := ValidatePlan(&plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func StrictDecodeRound1Package(data []byte) (*Round1Package, error) {
	var pkg Round1Package
	if err := strictJSON(data, maxRound1JSONSize, &pkg); err != nil {
		return nil, err
	}
	return &pkg, nil
}

func StrictDecodeAttestation(data []byte) (*PublicAttestation, error) {
	var attestation PublicAttestation
	if err := strictJSON(data, maxAttestationJSONSize, &attestation); err != nil {
		return nil, err
	}
	return &attestation, nil
}

func StrictDecodeManifest(data []byte) (*PublicManifest, error) {
	var manifest PublicManifest
	if err := strictJSON(data, maxManifestJSONSize, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func strictJSON(data []byte, limit int, destination any) error {
	if len(data) == 0 || len(data) > limit {
		return errors.New("JSON artifact is empty or exceeds its size limit")
	}
	duplicateDecoder := json.NewDecoder(bytes.NewReader(data))
	duplicateDecoder.UseNumber()
	if err := rejectDuplicateValue(duplicateDecoder, 0); err != nil {
		return err
	}
	if _, err := duplicateDecoder.Token(); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	var fieldTree any
	fieldDecoder := json.NewDecoder(bytes.NewReader(data))
	fieldDecoder.UseNumber()
	if err := fieldDecoder.Decode(&fieldTree); err != nil {
		return fmt.Errorf("invalid JSON artifact: %w", err)
	}
	if err := validateExactJSONFields(fieldTree, reflect.TypeOf(destination)); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON artifact: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func validateExactJSONFields(value any, target reflect.Type) error {
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	if value == nil {
		return nil
	}
	                                                                             
	                                                                            
	if target == reflect.TypeOf(json.RawMessage{}) {
		return nil
	}
	switch target.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return errors.New("JSON artifact field has the wrong structural type")
		}
		fields := make(map[string]reflect.Type)
		for i := 0; i < target.NumField(); i++ {
			field := target.Field(i)
			if field.PkgPath != "" {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			fields[name] = field.Type
		}
		for name, child := range object {
			fieldType, ok := fields[name]
			if !ok {
				return fmt.Errorf("unknown or noncanonical JSON field %q", name)
			}
			if err := validateExactJSONFields(child, fieldType); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		if target.Kind() == reflect.Slice && target.Elem().Kind() == reflect.Uint8 {
			encoded, ok := value.(string)
			if !ok {
				return errors.New("binary JSON artifact field must use base64 string encoding")
			}
			decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
			if err != nil || base64.StdEncoding.EncodeToString(decoded) != encoded {
				return errors.New("binary JSON artifact field uses noncanonical base64 encoding")
			}
			return nil
		}
		array, ok := value.([]any)
		if !ok {
			                                                                     
			return nil
		}
		for _, child := range array {
			if err := validateExactJSONFields(child, target.Elem()); err != nil {
				return err
			}
		}
	case reflect.Map:
		object, ok := value.(map[string]any)
		if !ok {
			return errors.New("JSON artifact field has the wrong structural type")
		}
		for _, child := range object {
			if err := validateExactJSONFields(child, target.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

func rejectDuplicateValue(decoder *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("JSON artifact nesting is too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("invalid JSON artifact: %w", err)
	}
	delimiter, structured := token.(json.Delim)
	if !structured {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return errors.New("invalid JSON object field")
			}
			name, ok := nameToken.(string)
			if !ok {
				return errors.New("invalid JSON object field name")
			}
			if _, duplicate := seen[name]; duplicate {
				return fmt.Errorf("duplicate JSON field %q", name)
			}
			seen[name] = struct{}{}
			if err := rejectDuplicateValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("unterminated JSON object")
		}
	case '[':
		for decoder.More() {
			if err := rejectDuplicateValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("unterminated JSON array")
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	return nil
}
