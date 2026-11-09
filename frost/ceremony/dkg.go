package ceremony

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"math/big"

	"dex/frost/core/curve"
	"dex/frost/core/dkg"
	"dex/frost/security"
	"dex/pb"

	"github.com/btcsuite/btcd/btcec/v2"
)

const encryptedSharePlaintextSize = 64                            

type EncryptedShare struct {
	RecipientID string `json:"recipient_id"`
	Ciphertext  []byte `json:"ciphertext"`
}

type VaultContribution struct {
	Chain       string           `json:"chain"`
	VaultID     uint32           `json:"vault_id"`
	Commitments [][]byte         `json:"commitments"`
	Shares      []EncryptedShare `json:"shares"`
}

type Round1Package struct {
	Version    uint32              `json:"version"`
	PlanDigest []byte              `json:"plan_digest"`
	DealerID   string              `json:"dealer_id"`
	Vaults     []VaultContribution `json:"vaults"`
	Signature  []byte              `json:"signature,omitempty"`
}

type VaultResult struct {
	Chain                string      `json:"chain"`
	VaultID              uint32      `json:"vault_id"`
	Epoch                uint64      `json:"epoch"`
	SignAlgo             pb.SignAlgo `json:"sign_algo"`
	Committee            []string    `json:"committee"`
	SignerIndex          uint32      `json:"signer_index"`
	LocalShare           []byte      `json:"local_share"`
	RawGroupPubkey       []byte      `json:"raw_group_pubkey"`
	AggregateCommitments [][]byte    `json:"aggregate_commitments"`
}

                                                                                 
                                                                                
                                                                                
type Result struct {
	Version          uint32        `json:"version"`
	PlanDigest       []byte        `json:"plan_digest"`
	TranscriptDigest []byte        `json:"transcript_digest"`
	ParticipantID    string        `json:"participant_id"`
	Epoch            uint64        `json:"epoch"`
	Vaults           []VaultResult `json:"vaults"`
}

                                                                             
                                                                                
                                                                                  
func Round1(plan *Plan, dealerID string) (*Round1Package, error) {
	digest, err := PlanDigest(plan)
	if err != nil {
		return nil, err
	}
	if participantByID(plan, dealerID) == nil {
		return nil, fmt.Errorf("unknown dealer")
	}
	result := &Round1Package{Version: Version, PlanDigest: digest, DealerID: dealerID}
	for _, vault := range plan.Vaults {
		if memberIndex(vault.Committee, dealerID) == 0 {
			continue
		}
		group, _ := vaultGroup(vault)
		contribution, err := createContribution(plan, digest, vault, dealerID, group)
		if err != nil {
			return nil, fmt.Errorf("round1 %s: %w", vaultKey(vault.Chain, vault.VaultID), err)
		}
		result.Vaults = append(result.Vaults, contribution)
	}
	return result, nil
}

func createContribution(plan *Plan, digest []byte, vault Vault, dealer string, group curve.Group) (VaultContribution, error) {
	result := VaultContribution{Chain: vault.Chain, VaultID: vault.VaultID}
	poly := &dkg.Polynomial{Coefficients: make([]*big.Int, int(plan.Threshold))}
	defer func() {
		for _, scalar := range poly.Coefficients {
			if scalar != nil {
				scalar.SetInt64(0)
			}
		}
	}()
	for i := range poly.Coefficients {
		coefficient, err := randomNonzeroScalar(group.Order())
		if err != nil {
			return result, err
		}
		poly.Coefficients[i] = coefficient
		result.Commitments = append(result.Commitments, group.SerializePoint(group.ScalarBaseMult(coefficient)))
	}
	for i, receiver := range vault.Committee {
		share := poly.Evaluate(big.NewInt(int64(i+1)), group)
		if share.Sign() == 0 {
			return result, fmt.Errorf("zero dealer share; restart ceremony")
		}
		context, err := shareContext(digest, vault, dealer, receiver)
		if err != nil {
			return result, err
		}
		plain := append(context, share.FillBytes(make([]byte, 32))...)
		share.SetInt64(0)
		randomness, err := randomNonzeroScalar(btcec.S256().Params().N)
		if err != nil {
			wipe(plain)
			return result, err
		}
		randomBytes := randomness.FillBytes(make([]byte, 32))
		randomness.SetInt64(0)
		ciphertext, err := security.ECIESEncrypt(participantByID(plan, receiver).EncryptionPublicKey, plain, randomBytes)
		wipe(plain)
		wipe(randomBytes)
		if err != nil {
			return result, err
		}
		result.Shares = append(result.Shares, EncryptedShare{RecipientID: receiver, Ciphertext: ciphertext})
	}
	return result, nil
}

                                                                                
                                                                              
                                                                                
func ValidateRound1Package(plan *Plan, pkg *Round1Package) error {
	digest, err := PlanDigest(plan)
	if err != nil {
		return err
	}
	return validatePackage(plan, digest, pkg)
}

func validatePackage(plan *Plan, digest []byte, pkg *Round1Package) error {
	if pkg == nil || pkg.Version != Version || !bytes.Equal(pkg.PlanDigest, digest) || participantByID(plan, pkg.DealerID) == nil || len(pkg.Signature) > 128 {
		return fmt.Errorf("invalid round1 package identity")
	}
	next := 0
	for _, vault := range plan.Vaults {
		if memberIndex(vault.Committee, pkg.DealerID) == 0 {
			continue
		}
		if next >= len(pkg.Vaults) {
			return fmt.Errorf("missing dealer vault contribution")
		}
		contribution := pkg.Vaults[next]
		next++
		if contribution.Chain != vault.Chain || contribution.VaultID != vault.VaultID || len(contribution.Commitments) != int(plan.Threshold) || len(contribution.Shares) != len(vault.Committee) {
			return fmt.Errorf("invalid dealer vault contribution")
		}
		group, _ := vaultGroup(vault)
		for _, commitment := range contribution.Commitments {
			if _, err := decodePoint(group, commitment); err != nil {
				return fmt.Errorf("invalid commitment: %w", err)
			}
		}
		for i, encrypted := range contribution.Shares {
			if encrypted.RecipientID != vault.Committee[i] {
				return fmt.Errorf("ciphertext recipient/order mismatch")
			}
			parsed, err := security.ParseECIESCiphertext(encrypted.Ciphertext, encryptedSharePlaintextSize)
			if err != nil {
				return err
			}
			if _, err := btcec.ParsePubKey(parsed.EphemeralPubKey); err != nil {
				return fmt.Errorf("invalid ciphertext ephemeral key")
			}
		}
	}
	if next != len(pkg.Vaults) {
		return fmt.Errorf("unexpected dealer vault contribution")
	}
	return nil
}

                                                                                 
func Round1PackageDigest(plan *Plan, pkg *Round1Package) ([]byte, error) {
	if err := ValidateRound1Package(plan, pkg); err != nil {
		return nil, err
	}
	unsigned := *pkg
	unsigned.Signature = nil
	return canonicalDigest("DEX_CEREMONY_ROUND1_V1", &unsigned)
}

                                                                              
                                                                               
                                                                                
func Finalize(plan *Plan, recipientID string, privateKey []byte, packages []*Round1Package) (*Result, error) {
	digest, err := PlanDigest(plan)
	if err != nil {
		return nil, err
	}
	recipient := participantByID(plan, recipientID)
	secret := new(big.Int).SetBytes(privateKey)
	defer secret.SetInt64(0)
	if recipient == nil || len(privateKey) != 32 || secret.Sign() <= 0 || secret.Cmp(btcec.S256().Params().N) >= 0 {
		return nil, fmt.Errorf("invalid recipient private key")
	}
	priv, _ := btcec.PrivKeyFromBytes(privateKey)
	defer priv.Zero()
	if !bytes.Equal(priv.PubKey().SerializeCompressed(), recipient.EncryptionPublicKey) {
		return nil, fmt.Errorf("recipient ceremony encryption private key does not match plan")
	}
	if len(packages) != len(plan.Participants) {
		return nil, fmt.Errorf("missing or extra dealer packages")
	}
	byDealer := make(map[string]*Round1Package, len(packages))
	for _, pkg := range packages {
		if err := validatePackage(plan, digest, pkg); err != nil {
			return nil, err
		}
		if byDealer[pkg.DealerID] != nil {
			return nil, fmt.Errorf("duplicate dealer package")
		}
		byDealer[pkg.DealerID] = pkg
	}
	packageDigests := make([][]byte, 0, len(packages))
	for _, participant := range plan.Participants {
		pkg := byDealer[participant.ID]
		if pkg == nil {
			return nil, fmt.Errorf("missing dealer %s", participant.ID)
		}
		unsigned := *pkg
		unsigned.Signature = nil
		pkgDigest, err := canonicalDigest("DEX_CEREMONY_ROUND1_V1", &unsigned)
		if err != nil {
			return nil, err
		}
		packageDigests = append(packageDigests, pkgDigest)
	}
	transcript, err := canonicalDigest("DEX_CEREMONY_TRANSCRIPT_V1", packageDigests)
	if err != nil {
		return nil, err
	}
	result := &Result{Version: Version, PlanDigest: digest, TranscriptDigest: transcript, ParticipantID: recipientID, Epoch: plan.Epoch}
	success := false
	defer func() {
		if !success {
			for i := range result.Vaults {
				wipe(result.Vaults[i].LocalShare)
			}
		}
	}()
	for _, vault := range plan.Vaults {
		index := memberIndex(vault.Committee, recipientID)
		if index == 0 {
			continue
		}
		value, err := finalizeVault(digest, vault, recipientID, index, privateKey, byDealer)
		if err != nil {
			return nil, fmt.Errorf("finalize %s: %w", vaultKey(vault.Chain, vault.VaultID), err)
		}
		value.Epoch = plan.Epoch
		result.Vaults = append(result.Vaults, value)
	}
	success = true
	return result, nil
}

func finalizeVault(digest []byte, vault Vault, recipient string, index int, privateKey []byte, packages map[string]*Round1Package) (VaultResult, error) {
	result := VaultResult{Chain: vault.Chain, VaultID: vault.VaultID, SignAlgo: vault.SignAlgo, Committee: append([]string(nil), vault.Committee...), SignerIndex: uint32(index)}
	group, _ := vaultGroup(vault)
	sum := new(big.Int)
	defer sum.SetInt64(0)
	aggregateCommitments := make([]curve.Point, 0)
	for _, dealer := range vault.Committee {
		var contribution *VaultContribution
		for i := range packages[dealer].Vaults {
			candidate := &packages[dealer].Vaults[i]
			if candidate.Chain == vault.Chain && candidate.VaultID == vault.VaultID {
				contribution = candidate
				break
			}
		}
		if contribution == nil {
			return result, fmt.Errorf("missing contribution")
		}
		plain, err := security.ECIESDecrypt(privateKey, contribution.Shares[index-1].Ciphertext)
		if err != nil {
			return result, fmt.Errorf("dealer %s ciphertext: %w", dealer, err)
		}
		context, _ := shareContext(digest, vault, dealer, recipient)
		if len(plain) != encryptedSharePlaintextSize || !bytes.Equal(plain[:32], context) {
			wipe(plain)
			return result, fmt.Errorf("dealer %s share context mismatch", dealer)
		}
		share := new(big.Int).SetBytes(plain[32:])
		valid := share.Sign() > 0 && share.Cmp(group.Order()) < 0 && security.VerifyShareAgainstCommitmentOnGroup(plain[32:], contribution.Commitments, big.NewInt(int64(index)), group)
		wipe(plain)
		if !valid {
			share.SetInt64(0)
			return result, fmt.Errorf("dealer %s Feldman share verification failed", dealer)
		}
		sum.Add(sum, share).Mod(sum, group.Order())
		share.SetInt64(0)
		if len(aggregateCommitments) == 0 {
			aggregateCommitments = make([]curve.Point, len(contribution.Commitments))
		}
		for commitmentIndex, encoded := range contribution.Commitments {
			point, decodeErr := decodePoint(group, encoded)
			if decodeErr != nil {
				return result, fmt.Errorf("dealer %s commitment %d: %w", dealer, commitmentIndex, decodeErr)
			}
			if isInfinity(aggregateCommitments[commitmentIndex]) {
				aggregateCommitments[commitmentIndex] = point
			} else {
				aggregateCommitments[commitmentIndex] = group.Add(aggregateCommitments[commitmentIndex], point)
			}
		}
	}
	if sum.Sign() == 0 || len(aggregateCommitments) == 0 {
		return result, fmt.Errorf("degenerate aggregate; restart ceremony")
	}
	result.AggregateCommitments = make([][]byte, len(aggregateCommitments))
	for i, point := range aggregateCommitments {
		if isInfinity(point) {
			return VaultResult{}, fmt.Errorf("degenerate aggregate commitment %d; restart ceremony", i)
		}
		result.AggregateCommitments[i] = group.SerializePoint(point)
	}
	result.LocalShare = sum.FillBytes(make([]byte, 32))
	result.RawGroupPubkey = bytes.Clone(result.AggregateCommitments[0])
	return result, nil
}

func shareContext(digest []byte, vault Vault, dealer, recipient string) ([]byte, error) {
	return canonicalDigest("DEX_CEREMONY_SHARE_V1", struct {
		PlanDigest []byte `json:"plan_digest"`
		Chain      string `json:"chain"`
		VaultID    uint32 `json:"vault_id"`
		Dealer     string `json:"dealer"`
		Recipient  string `json:"recipient"`
	}{digest, vault.Chain, vault.VaultID, dealer, recipient})
}

func decodePoint(group curve.Group, encoded []byte) (curve.Point, error) {
	point := group.DecompressPoint(encoded)
	if isInfinity(point) || !bytes.Equal(group.SerializePoint(point), encoded) {
		return curve.Point{}, fmt.Errorf("invalid, noncanonical or infinite curve point")
	}
	return point, nil
}

func isInfinity(point curve.Point) bool {
	return point.X == nil || point.Y == nil || point.X.Sign() == 0 && point.Y.Sign() == 0
}

func randomNonzeroScalar(order *big.Int) (*big.Int, error) {
	for {
		value, err := rand.Int(rand.Reader, order)
		if err != nil {
			return nil, err
		}
		if value.Sign() != 0 {
			return value, nil
		}
	}
}

func wipe(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
