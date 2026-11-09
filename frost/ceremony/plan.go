                                                                          
                                                                              
                                                                           
package ceremony

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"dex/config"
	"dex/frost/core/curve"
	"dex/pb"

	"github.com/btcsuite/btcd/btcec/v2"
)

const (
	Version         = uint32(1)
	MaxParticipants = 256
	MaxVaults       = 256
)

type Participant struct {
	Index               uint64 `json:"index"`
	ID                  string `json:"id"`
	PublicKey           []byte `json:"public_key"`
	EncryptionPublicKey []byte `json:"encryption_public_key"`
}

                                                                       
                                                                             
                                                                      
                                                        
type Registration struct {
	Version             uint32 `json:"version"`
	ContextDigest       []byte `json:"context_digest"`
	ParticipantID       string `json:"participant_id"`
	EncryptionPublicKey []byte `json:"encryption_public_key"`
	Signature           []byte `json:"signature"`
}

type Vault struct {
	Chain     string      `json:"chain"`
	VaultID   uint32      `json:"vault_id"`
	SignAlgo  pb.SignAlgo `json:"sign_algo"`
	Committee []string    `json:"committee"`
}

                                                                            
                                                                              
type Plan struct {
	Version            uint32         `json:"version"`
	ID                 string         `json:"id"`
	NetworkChainID     uint64         `json:"network_chain_id"`
	PeerManifestDigest []byte         `json:"peer_manifest_digest"`
	FrostConfigDigest  []byte         `json:"frost_config_digest"`
	Epoch              uint64         `json:"epoch"`
	Threshold          uint32         `json:"threshold"`
	Participants       []Participant  `json:"participants"`
	Registrations      []Registration `json:"registrations"`
	Vaults             []Vault        `json:"vaults"`
}

func ValidatePlan(plan *Plan) error {
	if plan == nil || plan.Version != Version || !validID(plan.ID) || plan.NetworkChainID == 0 ||
		!validDigest(plan.PeerManifestDigest) || !validDigest(plan.FrostConfigDigest) || plan.Epoch == 0 {
		return fmt.Errorf("invalid ceremony version, identity or epoch")
	}
	if len(plan.Participants) == 0 || len(plan.Participants) > MaxParticipants || len(plan.Vaults) == 0 || len(plan.Vaults) > MaxVaults || plan.Threshold == 0 {
		return fmt.Errorf("invalid ceremony dimensions")
	}
	participants, publicKeys := make(map[string]bool), make(map[string]bool)
	var previousIndex uint64
	for position, participant := range plan.Participants {
		if !validID(participant.ID) || participants[participant.ID] || len(participant.PublicKey) != 33 ||
			len(participant.EncryptionPublicKey) != 33 || publicKeys[string(participant.PublicKey)] ||
			publicKeys[string(participant.EncryptionPublicKey)] || bytes.Equal(participant.PublicKey, participant.EncryptionPublicKey) ||
			position > 0 && participant.Index <= previousIndex {
			return fmt.Errorf("invalid or duplicate participant")
		}
		identity, err := btcec.ParsePubKey(participant.PublicKey)
		if err != nil || !bytes.Equal(identity.SerializeCompressed(), participant.PublicKey) {
			return fmt.Errorf("participant %s has invalid identity public key", participant.ID)
		}
		encryption, err := btcec.ParsePubKey(participant.EncryptionPublicKey)
		if err != nil || !bytes.Equal(encryption.SerializeCompressed(), participant.EncryptionPublicKey) {
			return fmt.Errorf("participant %s has invalid ceremony encryption public key", participant.ID)
		}
		participants[participant.ID] = true
		publicKeys[string(participant.PublicKey)] = true
		publicKeys[string(participant.EncryptionPublicKey)] = true
		previousIndex = participant.Index
	}
	used, vaults := make(map[string]bool), make(map[string]bool)
	for _, vault := range plan.Vaults {
		key := vaultKey(vault.Chain, vault.VaultID)
		if vaults[key] || len(vault.Committee) < int(plan.Threshold) || len(vault.Committee) > len(plan.Participants) {
			return fmt.Errorf("invalid or duplicate vault %s", key)
		}
		expectedThreshold, err := config.ComputeVaultCommitteeThreshold(len(vault.Committee))
		if err != nil || plan.Threshold != uint32(expectedThreshold) {
			return fmt.Errorf("vault %s must use an official committee tier and exact 3/4 threshold", key)
		}
		if _, err := vaultGroup(vault); err != nil {
			return err
		}
		members := make(map[string]bool)
		for _, id := range vault.Committee {
			if !participants[id] || members[id] {
				return fmt.Errorf("invalid committee member in %s", key)
			}
			members[id], used[id] = true, true
		}
		vaults[key] = true
	}
	if len(used) != len(participants) {
		return fmt.Errorf("participant is not assigned to any vault")
	}
	contextDigest, err := RegistrationContextDigest(plan)
	if err != nil {
		return err
	}
	if len(plan.Registrations) != len(plan.Participants) {
		return fmt.Errorf("plan requires one signed registration from every participant")
	}
	for i := range plan.Participants {
		participant := &plan.Participants[i]
		registration := &plan.Registrations[i]
		if registration.Version != Version || registration.ParticipantID != participant.ID ||
			!bytes.Equal(registration.ContextDigest, contextDigest) ||
			!bytes.Equal(registration.EncryptionPublicKey, participant.EncryptionPublicKey) ||
			len(registration.Signature) != 64 {
			return fmt.Errorf("participant %s has an invalid or reordered registration", participant.ID)
		}
		digest, err := RegistrationDigest(registration)
		if err != nil || !verifyRegistrationSignature(participant.PublicKey, digest, registration.Signature) {
			return fmt.Errorf("participant %s registration signature is invalid", participant.ID)
		}
	}
	return nil
}

                                                                             
                                                                              
                                                                         
type planRegistrationContext struct {
	Version            uint32                        `json:"version"`
	ID                 string                        `json:"id"`
	NetworkChainID     uint64                        `json:"network_chain_id"`
	PeerManifestDigest []byte                        `json:"peer_manifest_digest"`
	FrostConfigDigest  []byte                        `json:"frost_config_digest"`
	Epoch              uint64                        `json:"epoch"`
	Threshold          uint32                        `json:"threshold"`
	Participants       []planRegistrationParticipant `json:"participants"`
	Vaults             []Vault                       `json:"vaults"`
}

type planRegistrationParticipant struct {
	Index             uint64 `json:"index"`
	ID                string `json:"id"`
	IdentityPublicKey []byte `json:"identity_public_key"`
}

                                                                             
                                                                          
                                  
func RegistrationContextDigest(plan *Plan) ([]byte, error) {
	if plan == nil {
		return nil, fmt.Errorf("nil ceremony plan")
	}
	context := planRegistrationContext{
		Version: plan.Version, ID: plan.ID, NetworkChainID: plan.NetworkChainID,
		PeerManifestDigest: plan.PeerManifestDigest, FrostConfigDigest: plan.FrostConfigDigest,
		Epoch: plan.Epoch, Threshold: plan.Threshold, Vaults: plan.Vaults,
		Participants: make([]planRegistrationParticipant, len(plan.Participants)),
	}
	for i, participant := range plan.Participants {
		context.Participants[i] = planRegistrationParticipant{
			Index: participant.Index, ID: participant.ID, IdentityPublicKey: participant.PublicKey,
		}
	}
	return canonicalDigest("DEX_CEREMONY_CONTEXT_V1", &context)
}

                                                                       
                                                             
func RegistrationDigest(registration *Registration) ([]byte, error) {
	if registration == nil {
		return nil, fmt.Errorf("nil ceremony registration")
	}
	unsigned := *registration
	unsigned.Signature = nil
	return canonicalDigest("DEX_CEREMONY_REGISTRATION_V1", &unsigned)
}

func verifyRegistrationSignature(publicKey, digest, signature []byte) bool {
	if len(digest) != sha256.Size || !IsCanonicalIdentitySignature(signature) {
		return false
	}
	parsed, err := btcec.ParsePubKey(publicKey)
	if err != nil {
		return false
	}
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	return ecdsa.Verify(parsed.ToECDSA(), digest, r, s)
}

                                                                         
                                                                               
                                                                              
                                                                            
func IsCanonicalIdentitySignature(signature []byte) bool {
	if len(signature) != 64 {
		return false
	}
	order := btcec.S256().Params().N
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	if r.Sign() <= 0 || r.Cmp(order) >= 0 || s.Sign() <= 0 || s.Cmp(order) >= 0 {
		return false
	}
	halfOrder := new(big.Int).Rsh(new(big.Int).Set(order), 1)
	return s.Cmp(halfOrder) <= 0
}

func PlanDigest(plan *Plan) ([]byte, error) {
	if err := ValidatePlan(plan); err != nil {
		return nil, err
	}
	return canonicalDigest("DEX_CEREMONY_PLAN_V1", plan)
}

func canonicalDigest(domain string, value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	_, _ = h.Write([]byte(domain + "\x00"))
	_, _ = h.Write(encoded)
	return h.Sum(nil), nil
}

func validID(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}

func vaultKey(chain string, id uint32) string { return fmt.Sprintf("%s/%d", chain, id) }

func vaultGroup(vault Vault) (curve.Group, error) {
	switch vault.Chain {
	case "btc":
		if vault.SignAlgo == pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340 {
			return curve.NewSecp256k1Group(), nil
		}
	case "eth", "bnb", "base", "arb", "trx":
		if vault.SignAlgo == pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128 {
			return curve.NewBN256Group(), nil
		}
	}
	return nil, fmt.Errorf("unsupported or mismatched chain/signing algorithm for %s", vault.Chain)
}

func memberIndex(committee []string, id string) int {
	for i, member := range committee {
		if member == id {
			return i + 1
		}
	}
	return 0
}

func participantByID(plan *Plan, id string) *Participant {
	for i := range plan.Participants {
		if plan.Participants[i].ID == id {
			return &plan.Participants[i]
		}
	}
	return nil
}
