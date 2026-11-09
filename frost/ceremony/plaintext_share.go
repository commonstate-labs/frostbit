package ceremony

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

                                                                           
                                               
const PlaintextGenesisChainID uint64 = 51025

const (
	plaintextShareVersion = 1
	plaintextShareFormat  = "DEX_FROST_PLAINTEXT_GENESIS_DEV_51025"
)

type plaintextShareEnvelope struct {
	Version  uint32        `json:"version"`
	Format   string        `json:"format"`
	Metadata ShareMetadata `json:"metadata"`
	Share    []byte        `json:"share"`
}

                                                                            
                                                                             
func EncodePlaintextGenesisShare(metadata ShareMetadata, localShare []byte) ([]byte, error) {
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
		return nil, fmt.Errorf("encode plaintext local share: %w", err)
	}
	return json.Marshal(plaintextShareEnvelope{
		Version: plaintextShareVersion, Format: plaintextShareFormat,
		Metadata: metadata, Share: plain,
	})
}

                                                                             
                                                                         
func OpenPlaintextGenesisShare(data []byte, expected ShareMetadata) ([]byte, error) {
	expected = copyShareMetadata(expected)
	if err := ValidateShareMetadata(expected); err != nil {
		return nil, fmt.Errorf("invalid expected plaintext share metadata: %w", err)
	}
	envelope, err := decodePlaintextGenesisShare(data)
	if err != nil {
		return nil, err
	}
	defer clear(envelope.Share)
	want, err := json.Marshal(expected)
	if err != nil {
		return nil, err
	}
	got, err := json.Marshal(envelope.Metadata)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(got, want) {
		return nil, errors.New("plaintext-share metadata does not match the signed ceremony participant")
	}
	if !validShareScalar(envelope.Share, expected.SignAlgo) {
		return nil, errors.New("plaintext local share scalar is outside the signing group")
	}
	if err := VerifyLocalShareAgainstPoint(expected.Chain, expected.SignAlgo, envelope.Share, expected.VerificationShare); err != nil {
		return nil, fmt.Errorf("verify plaintext local share: %w", err)
	}
	return bytes.Clone(envelope.Share), nil
}

func decodePlaintextGenesisShare(data []byte) (*plaintextShareEnvelope, error) {
	if len(data) == 0 || len(data) > maxSealedShareSize {
		return nil, errors.New("invalid plaintext-share envelope size")
	}
	fields, err := sealedShareObject(data, "version", "format", "metadata", "share")
	if err != nil {
		return nil, fmt.Errorf("invalid plaintext-share envelope: %w", err)
	}
	metadataFields, err := sealedShareObject(fields["metadata"], "ceremony_digest", "node_id", "chain", "vault_id", "epoch", "sign_algo", "committee_digest", "group_pubkey", "verification_share")
	if err != nil {
		return nil, fmt.Errorf("invalid plaintext-share metadata: %w", err)
	}
	var envelope plaintextShareEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, errors.New("invalid plaintext-share field encoding")
	}
	if envelope.Version != plaintextShareVersion || envelope.Format != plaintextShareFormat || len(envelope.Share) != shareScalarSize {
		return nil, errors.New("unsupported plaintext-share format or scalar length")
	}
	if err := ValidateShareMetadata(envelope.Metadata); err != nil {
		return nil, err
	}
	for _, raw := range []json.RawMessage{fields["share"], metadataFields["group_pubkey"], metadataFields["verification_share"]} {
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return nil, errors.New("invalid plaintext-share base64 encoding")
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || base64.StdEncoding.EncodeToString(decoded) != encoded {
			return nil, errors.New("noncanonical plaintext-share base64 encoding")
		}
	}
	envelope.Metadata = copyShareMetadata(envelope.Metadata)
	return &envelope, nil
}
