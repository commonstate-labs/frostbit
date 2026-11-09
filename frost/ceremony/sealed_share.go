package ceremony

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"dex/pb"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	bn256 "github.com/ethereum/go-ethereum/crypto/bn256/cloudflare"
)

const (
	sealedShareVersion   = 1
	sealedShareCipher    = "AES-256-GCM"
	sealedShareDomain    = "DEX_FROST_CEREMONY_SHARE_V1"
	maxSealedShareSize   = 16 * 1024
	shareScalarSize      = 32
	sealedShareNonceSize = 12
	sealedShareTagSize   = 16
)

                                                                               
                                                                                 
                                                                              
                                                                               
type ShareMetadata struct {
	CeremonyDigest    string      `json:"ceremony_digest"`
	NodeID            string      `json:"node_id"`
	Chain             string      `json:"chain"`
	VaultID           uint32      `json:"vault_id"`
	Epoch             uint64      `json:"epoch"`
	SignAlgo          pb.SignAlgo `json:"sign_algo"`
	CommitteeDigest   string      `json:"committee_digest"`
	GroupPubkey       []byte      `json:"group_pubkey"`
	VerificationShare []byte      `json:"verification_share"`
}

type sealedShareEnvelope struct {
	Version    uint32        `json:"version"`
	Cipher     string        `json:"cipher"`
	Metadata   ShareMetadata `json:"metadata"`
	Nonce      []byte        `json:"nonce"`
	Ciphertext []byte        `json:"ciphertext"`
}

                                                                             
                                                                               
                                                                          
                                                                              
                                                                               
                                                                                  
func SealShare(metadata ShareMetadata, localShare, wrapKey []byte) ([]byte, error) {
	metadata = copyShareMetadata(metadata)
	if err := ValidateShareMetadata(metadata); err != nil {
		return nil, err
	}
	if len(localShare) == 0 || len(localShare) > shareScalarSize {
		return nil, errors.New("local share must be a 1..32-byte scalar")
	}
	plain := make([]byte, shareScalarSize)
	copy(plain[shareScalarSize-len(localShare):], localShare)
	defer clear(plain)
	if !validShareScalar(plain, metadata.SignAlgo) {
		return nil, errors.New("local share scalar is outside the signing group")
	}
	if err := VerifyLocalShareAgainstPoint(metadata.Chain, metadata.SignAlgo, plain, metadata.VerificationShare); err != nil {
		return nil, fmt.Errorf("seal local share: %w", err)
	}
	gcm, err := newSealedShareAEAD(wrapKey)
	if err != nil {
		return nil, err
	}
	aad, err := shareMetadataAAD(metadata)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate sealed-share nonce: %w", err)
	}
	envelope := sealedShareEnvelope{
		Version: sealedShareVersion, Cipher: sealedShareCipher, Metadata: metadata,
		Nonce: nonce, Ciphertext: gcm.Seal(nil, nonce, plain, aad),
	}
	return json.Marshal(envelope)
}

                                                                            
                                                                                
                                                                                  
                                                                               
                                                        
func OpenShare(sealed []byte, expected ShareMetadata, wrapKey []byte) ([]byte, error) {
	expected = copyShareMetadata(expected)
	if err := ValidateShareMetadata(expected); err != nil {
		return nil, fmt.Errorf("invalid expected share metadata: %w", err)
	}
	gcm, err := newSealedShareAEAD(wrapKey)
	if err != nil {
		return nil, err
	}
	envelope, err := decodeSealedShare(sealed)
	if err != nil {
		return nil, err
	}
	aad, err := shareMetadataAAD(envelope.Metadata)
	if err != nil {
		return nil, err
	}
	expectedAAD, err := shareMetadataAAD(expected)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(aad, expectedAAD) {
		return nil, errors.New("sealed-share metadata does not match the expected ceremony participant")
	}
	if len(envelope.Nonce) != gcm.NonceSize() || len(envelope.Ciphertext) != shareScalarSize+gcm.Overhead() {
		return nil, errors.New("invalid sealed-share nonce or ciphertext length")
	}
	plain, err := gcm.Open(nil, envelope.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		clear(plain)
		return nil, errors.New("sealed-share authentication failed")
	}
	if !validShareScalar(plain, expected.SignAlgo) {
		clear(plain)
		return nil, errors.New("decrypted local share scalar is outside the signing group")
	}
	if err := VerifyLocalShareAgainstPoint(expected.Chain, expected.SignAlgo, plain, expected.VerificationShare); err != nil {
		clear(plain)
		return nil, fmt.Errorf("verify decrypted local share: %w", err)
	}
	return plain, nil
}

                                                                                
                                                                                   
                                                  
func ValidateShareMetadata(metadata ShareMetadata) error {
	if !isShareDigest(metadata.CeremonyDigest) || !isShareDigest(metadata.CommitteeDigest) {
		return errors.New("ceremony and committee digests must be 32-byte lowercase hex")
	}
	if len(metadata.NodeID) == 0 || len(metadata.NodeID) > 256 {
		return errors.New("invalid share node identity")
	}
	for _, char := range []byte(metadata.NodeID) {
		if char < 0x21 || char > 0x7e {
			return errors.New("share node identity must contain only visible ASCII characters")
		}
	}
	if metadata.Epoch == 0 {
		return errors.New("share epoch must be positive")
	}
	switch metadata.Chain {
	case "btc":
		if metadata.SignAlgo != pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340 {
			return errors.New("BTC share requires the BIP340 signing algorithm")
		}
		switch len(metadata.GroupPubkey) {
		case 32:
			if _, err := schnorr.ParsePubKey(metadata.GroupPubkey); err != nil {
				return errors.New("invalid BTC group public key")
			}
		case 33:
			if _, err := btcec.ParsePubKey(metadata.GroupPubkey); err != nil {
				return errors.New("invalid BTC group public key")
			}
		default:
			return errors.New("BTC group public key must be 32-byte x-only or 33-byte compressed")
		}
	case "eth", "bnb", "base", "arb", "trx":
		if metadata.SignAlgo != pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128 {
			return errors.New("account-chain share requires the BN128 signing algorithm")
		}
		if len(metadata.GroupPubkey) != 64 || bytes.Equal(metadata.GroupPubkey, make([]byte, 64)) {
			return errors.New("BN128 group public key must be a non-infinity 64-byte point")
		}
		if _, err := new(bn256.G1).Unmarshal(metadata.GroupPubkey); err != nil {
			return errors.New("invalid BN128 group public key")
		}
	default:
		return errors.New("unsupported share chain")
	}
	if err := validateVerificationSharePoint(metadata.Chain, metadata.SignAlgo, metadata.VerificationShare); err != nil {
		return fmt.Errorf("invalid participant verification share: %w", err)
	}
	return nil
}

func validateVerificationSharePoint(chain string, signAlgo pb.SignAlgo, encoded []byte) error {
	group, err := vaultGroup(Vault{Chain: chain, SignAlgo: signAlgo})
	if err != nil {
		return err
	}
	_, err = decodePoint(group, encoded)
	return err
}

func copyShareMetadata(metadata ShareMetadata) ShareMetadata {
	metadata.GroupPubkey = bytes.Clone(metadata.GroupPubkey)
	metadata.VerificationShare = bytes.Clone(metadata.VerificationShare)
	return metadata
}

func newSealedShareAEAD(wrapKey []byte) (cipher.AEAD, error) {
	if len(wrapKey) != 32 {
		return nil, errors.New("sealed-share wrap key must contain exactly 32 bytes")
	}
	key := bytes.Clone(wrapKey)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize sealed-share cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

func shareMetadataAAD(metadata ShareMetadata) ([]byte, error) {
	return json.Marshal(struct {
		Domain   string        `json:"domain"`
		Version  uint32        `json:"version"`
		Cipher   string        `json:"cipher"`
		Metadata ShareMetadata `json:"metadata"`
	}{sealedShareDomain, sealedShareVersion, sealedShareCipher, metadata})
}

func isShareDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range []byte(value) {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func validShareScalar(value []byte, algo pb.SignAlgo) bool {
	if len(value) != shareScalarSize {
		return false
	}
	var order []byte
	switch algo {
	case pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340:
		order = btcec.S256().Params().N.FillBytes(make([]byte, shareScalarSize))
	case pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128:
		order = bn256.Order.FillBytes(make([]byte, shareScalarSize))
	default:
		return false
	}
	                                                                               
	var borrow uint16
	var nonzero byte
	for i := shareScalarSize - 1; i >= 0; i-- {
		difference := uint16(value[i]) - uint16(order[i]) - borrow
		borrow = (difference >> 8) & 1
		nonzero |= value[i]
	}
	return borrow == 1 && nonzero != 0
}

func decodeSealedShare(data []byte) (*sealedShareEnvelope, error) {
	if len(data) == 0 || len(data) > maxSealedShareSize {
		return nil, errors.New("invalid sealed-share envelope size")
	}
	fields, err := sealedShareObject(data, "version", "cipher", "metadata", "nonce", "ciphertext")
	if err != nil {
		return nil, fmt.Errorf("invalid sealed-share envelope: %w", err)
	}
	metadataFields, err := sealedShareObject(fields["metadata"], "ceremony_digest", "node_id", "chain", "vault_id", "epoch", "sign_algo", "committee_digest", "group_pubkey", "verification_share")
	if err != nil {
		return nil, fmt.Errorf("invalid sealed-share metadata: %w", err)
	}
	var envelope sealedShareEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, errors.New("invalid sealed-share field encoding")
	}
	if envelope.Version != sealedShareVersion || envelope.Cipher != sealedShareCipher {
		return nil, errors.New("unsupported sealed-share version or cipher")
	}
	if err := ValidateShareMetadata(envelope.Metadata); err != nil {
		return nil, err
	}
	for _, raw := range []json.RawMessage{fields["nonce"], fields["ciphertext"], metadataFields["group_pubkey"], metadataFields["verification_share"]} {
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return nil, errors.New("invalid sealed-share base64 encoding")
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || base64.StdEncoding.EncodeToString(decoded) != encoded {
			return nil, errors.New("noncanonical sealed-share base64 encoding")
		}
	}
	envelope.Metadata = copyShareMetadata(envelope.Metadata)
	return &envelope, nil
}

                                                                             
                                                                               
                                                                           
                                           
func sealedShareObject(data []byte, names ...string) (map[string]json.RawMessage, error) {
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return nil, errors.New("expected an object")
	}
	fields := make(map[string]json.RawMessage, len(names))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, errors.New("invalid object field")
		}
		name, ok := token.(string)
		if !ok || !allowed[name] {
			return nil, errors.New("unknown object field")
		}
		if _, duplicate := fields[name]; duplicate {
			return nil, errors.New("duplicate object field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil || strings.TrimSpace(string(value)) == "null" {
			return nil, errors.New("invalid or null object field")
		}
		fields[name] = value
	}
	last, err := decoder.Token()
	if err != nil || last != json.Delim('}') {
		return nil, errors.New("unterminated object")
	}
	if len(fields) != len(allowed) {
		return nil, errors.New("missing object field")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing JSON data")
	}
	return fields, nil
}
