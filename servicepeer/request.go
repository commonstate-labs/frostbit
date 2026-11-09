package servicepeer

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"

	"dex/utils"
)

var requestAuthDomain = []byte("DEX_SERVICE_PEER_REQUEST_AUTH_V1")

const MaxRequestPurposeLength = 128

                                                                          
                                                                            
                                                                               
                         
type RequestAuth struct {
	Version        uint32 `json:"version"`
	ChainID        uint64 `json:"chain_id"`
	NodeID         string `json:"node_id"`
	RecordSequence uint64 `json:"record_sequence"`
	Purpose        string `json:"purpose"`
	Nonce          []byte `json:"nonce"`
	PayloadDigest  []byte `json:"payload_digest"`
	Signature      []byte `json:"signature"`
}

                                                                            
                                                                              
type SignedPayload struct {
	Record  Record      `json:"record"`
	Auth    RequestAuth `json:"auth"`
	Payload []byte      `json:"payload"`
}

                                                                         
                                
func NewRequestAuth(record Record, purpose string, payload []byte, signer Signer) (RequestAuth, error) {
	if err := VerifyRecord(record, record.ChainID); err != nil {
		return RequestAuth{}, fmt.Errorf("servicepeer: request record: %w", err)
	}
	pub, nodeID, err := signerIdentity(signer)
	if err != nil {
		return RequestAuth{}, err
	}
	if nodeID != record.NodeID || !bytes.Equal(pub, record.SecpPublicKey) {
		return RequestAuth{}, ErrIdentityMismatch
	}
	nonce, err := NewNonce()
	if err != nil {
		return RequestAuth{}, err
	}
	auth := RequestAuth{
		Version:        ProtocolVersion,
		ChainID:        record.ChainID,
		NodeID:         record.NodeID,
		RecordSequence: record.Sequence,
		Purpose:        purpose,
		Nonce:          nonce,
		PayloadDigest:  payloadHash(payload),
	}
	if err := validateRequestAuth(auth, record, record.ChainID, purpose, payload); err != nil {
		return RequestAuth{}, err
	}
	auth.Signature, err = signer.Sign(RequestAuthDigest(auth))
	if err != nil {
		return RequestAuth{}, fmt.Errorf("%w: sign service request: %v", ErrInvalidSignature, err)
	}
	if len(auth.Signature) != SignatureSize {
		return RequestAuth{}, fmt.Errorf("%w: signature length %d", ErrInvalidSignature, len(auth.Signature))
	}
	return cloneRequestAuth(auth), nil
}

                                                                             
                                                                             
                                          
func VerifyRequestAuth(auth RequestAuth, record Record, expectedChainID uint64, purpose string, payload []byte) error {
	if err := VerifyRecord(record, expectedChainID); err != nil {
		return err
	}
	if err := validateRequestAuth(auth, record, expectedChainID, purpose, payload); err != nil {
		return err
	}
	if len(auth.Signature) != SignatureSize || !bytes.Equal(auth.PayloadDigest, payloadHash(payload)) ||
		!verifyRequestSignature(record, auth) {
		return ErrInvalidSignature
	}
	return nil
}

func validateRequestAuth(auth RequestAuth, record Record, expectedChainID uint64, purpose string, payload []byte) error {
	if auth.Version != ProtocolVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedVersion, auth.Version)
	}
	if expectedChainID == 0 || auth.ChainID != expectedChainID || record.ChainID != expectedChainID {
		return fmt.Errorf("%w: request=%d record=%d want=%d", ErrWrongChain, auth.ChainID, record.ChainID, expectedChainID)
	}
	if auth.NodeID != record.NodeID || auth.RecordSequence != record.Sequence {
		return fmt.Errorf("servicepeer: request record binding mismatch")
	}
	if purpose == "" || len(purpose) > MaxRequestPurposeLength || strings.IndexByte(purpose, 0) >= 0 || auth.Purpose != purpose {
		return fmt.Errorf("servicepeer: invalid request purpose")
	}
	if len(auth.Nonce) != NonceSize {
		return fmt.Errorf("servicepeer: request nonce length %d", len(auth.Nonce))
	}
	if len(auth.PayloadDigest) != sha256.Size || !bytes.Equal(auth.PayloadDigest, payloadHash(payload)) {
		return fmt.Errorf("servicepeer: request payload digest mismatch")
	}
	return nil
}

func verifyRequestSignature(record Record, auth RequestAuth) bool {
	return len(record.SecpPublicKey) == PublicKeySize &&
		len(auth.Signature) == SignatureSize &&
		utils.VerifyECDSASignature(record.SecpPublicKey, RequestAuthDigest(auth), auth.Signature)
}

                                                                   
func RequestAuthDigest(auth RequestAuth) []byte {
	h := sha256.New()
	writeBytes(h, requestAuthDomain)
	writeUint32(h, auth.Version)
	writeUint64(h, auth.ChainID)
	writeString(h, auth.NodeID)
	writeUint64(h, auth.RecordSequence)
	writeString(h, auth.Purpose)
	writeBytes(h, auth.Nonce)
	writeBytes(h, auth.PayloadDigest)
	return h.Sum(nil)
}

func payloadHash(payload []byte) []byte {
	digest := sha256.Sum256(payload)
	return digest[:]
}

func cloneRequestAuth(auth RequestAuth) RequestAuth {
	auth.Nonce = append([]byte(nil), auth.Nonce...)
	auth.PayloadDigest = append([]byte(nil), auth.PayloadDigest...)
	auth.Signature = append([]byte(nil), auth.Signature...)
	return auth
}
