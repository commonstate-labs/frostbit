package servicepeer

import (
	"bytes"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"dex/utils"
)

var (
	challengeDomain = []byte("DEX_SERVICE_PEER_CHALLENGE_V1")
	responseDomain  = []byte("DEX_SERVICE_PEER_CHALLENGE_RESPONSE_V1")
)

                                                                     
                                                                         
                                                    
const ConnectivityProbeHeight uint64 = 0

                                                                              
                                                                           
                                                                             
                                                                      
                                                                           
                                                         
type Challenge struct {
	Version             uint32 `json:"version"`
	ChainID             uint64 `json:"chain_id"`
	ChallengerID        string `json:"challenger_id"`
	ChallengerPublicKey []byte `json:"challenger_public_key"`
	ResponderID         string `json:"responder_id"`
	RecordSequence      uint64 `json:"record_sequence"`
	Nonce               []byte `json:"nonce"`
	RequestedHeight     uint64 `json:"requested_height"`
	Signature           []byte `json:"signature"`
}

                                                                              
                                                                             
                                                                             
                                                                          
                                                                   
type ChallengeResponse struct {
	Version            uint32 `json:"version"`
	ChainID            uint64 `json:"chain_id"`
	RequestDigest      []byte `json:"request_digest"`
	ChallengerID       string `json:"challenger_id"`
	ResponderID        string `json:"responder_id"`
	ResponderPublicKey []byte `json:"responder_public_key"`
	RecordSequence     uint64 `json:"record_sequence"`
	Nonce              []byte `json:"nonce"`
	RequestedHeight    uint64 `json:"requested_height"`
	Available          bool   `json:"available"`
	PayloadDigest      []byte `json:"payload_digest,omitempty"`
	Signature          []byte `json:"signature"`
}

                                                               
func NewNonce() ([]byte, error) {
	return NewNonceFrom(cryptorand.Reader)
}

                                                                      
                                                                             
func NewNonceFrom(reader io.Reader) ([]byte, error) {
	if reader == nil {
		return nil, fmt.Errorf("servicepeer: nil nonce reader")
	}
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(reader, nonce); err != nil {
		return nil, fmt.Errorf("servicepeer: generate nonce: %w", err)
	}
	return nonce, nil
}

                                                                           
                                                                               
                                                                             
                                                                              
                                                            
func NewSignedChallenge(signer Signer, expectedChainID uint64, responder Record, nonce []byte, requestedHeight uint64) (Challenge, error) {
	if err := VerifyRecord(responder, expectedChainID); err != nil {
		return Challenge{}, fmt.Errorf("servicepeer: challenge responder record: %w", err)
	}
	if requestedHeight != ConnectivityProbeHeight && !CoversArchive(responder, HeightRange{From: requestedHeight, To: requestedHeight}) {
		return Challenge{}, fmt.Errorf("servicepeer: requested height %d is outside responder block range %d..%d", requestedHeight, responder.ArchiveFrom, responder.ArchiveTo)
	}
	pub, nodeID, err := signerIdentity(signer)
	if err != nil {
		return Challenge{}, err
	}
	challenge := Challenge{
		Version:             ProtocolVersion,
		ChainID:             responder.ChainID,
		ChallengerID:        nodeID,
		ChallengerPublicKey: pub,
		ResponderID:         responder.NodeID,
		RecordSequence:      responder.Sequence,
		Nonce:               append([]byte(nil), nonce...),
		RequestedHeight:     requestedHeight,
	}
	if err := validateUnsignedChallenge(challenge, challenge.ChainID, challenge.ResponderID); err != nil {
		return Challenge{}, err
	}
	challenge.Signature, err = signer.Sign(ChallengeDigest(challenge))
	if err != nil {
		return Challenge{}, fmt.Errorf("%w: sign challenge: %v", ErrInvalidSignature, err)
	}
	if len(challenge.Signature) != SignatureSize {
		return Challenge{}, fmt.Errorf("%w: signature length %d", ErrInvalidSignature, len(challenge.Signature))
	}
	return cloneChallenge(challenge), nil
}

                                                                            
                                                                               
                                                   
func VerifyChallenge(challenge Challenge, expectedChainID uint64, expectedResponderID string) error {
	if err := validateUnsignedChallenge(challenge, expectedChainID, expectedResponderID); err != nil {
		return err
	}
	if len(challenge.Signature) != SignatureSize || !utils.VerifyECDSASignature(challenge.ChallengerPublicKey, ChallengeDigest(challenge), challenge.Signature) {
		return ErrInvalidSignature
	}
	return nil
}

func validateUnsignedChallenge(challenge Challenge, expectedChainID uint64, expectedResponderID string) error {
	if challenge.Version != ProtocolVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedVersion, challenge.Version)
	}
	if expectedChainID == 0 || challenge.ChainID != expectedChainID {
		return fmt.Errorf("%w: got %d want %d", ErrWrongChain, challenge.ChainID, expectedChainID)
	}
	if expectedResponderID == "" || challenge.ResponderID != expectedResponderID {
		return fmt.Errorf("servicepeer: challenge responder mismatch")
	}
	if challenge.RecordSequence == 0 {
		return fmt.Errorf("servicepeer: challenge record sequence must be positive")
	}
	if len(challenge.Nonce) != NonceSize {
		return fmt.Errorf("servicepeer: challenge nonce length %d", len(challenge.Nonce))
	}
	if err := verifyIdentity(challenge.ChallengerID, challenge.ChallengerPublicKey); err != nil {
		return err
	}
	if !utils.IsDexAddress(challenge.ResponderID) {
		return fmt.Errorf("servicepeer: invalid responder ID")
	}
	return nil
}

                                                        
func ChallengeDigest(challenge Challenge) []byte {
	h := sha256.New()
	writeBytes(h, challengeDomain)
	writeUint32(h, challenge.Version)
	writeUint64(h, challenge.ChainID)
	writeString(h, challenge.ChallengerID)
	writeBytes(h, challenge.ChallengerPublicKey)
	writeString(h, challenge.ResponderID)
	writeUint64(h, challenge.RecordSequence)
	writeBytes(h, challenge.Nonce)
	writeUint64(h, challenge.RequestedHeight)
	return h.Sum(nil)
}

                                                                               
                                                                                
                                                       
func NewSignedChallengeResponse(challenge Challenge, expectedChainID uint64, signer Signer, available bool, payloadDigest []byte) (ChallengeResponse, error) {
	pub, nodeID, err := signerIdentity(signer)
	if err != nil {
		return ChallengeResponse{}, err
	}
	if err := VerifyChallenge(challenge, expectedChainID, nodeID); err != nil {
		return ChallengeResponse{}, fmt.Errorf("servicepeer: response challenge: %w", err)
	}
	response := ChallengeResponse{
		Version:            ProtocolVersion,
		ChainID:            challenge.ChainID,
		RequestDigest:      ChallengeDigest(challenge),
		ChallengerID:       challenge.ChallengerID,
		ResponderID:        nodeID,
		ResponderPublicKey: pub,
		RecordSequence:     challenge.RecordSequence,
		Nonce:              append([]byte(nil), challenge.Nonce...),
		RequestedHeight:    challenge.RequestedHeight,
		Available:          available,
		PayloadDigest:      append([]byte(nil), payloadDigest...),
	}
	if err := validateUnsignedResponse(challenge, response); err != nil {
		return ChallengeResponse{}, err
	}
	response.Signature, err = signer.Sign(ChallengeResponseDigest(response))
	if err != nil {
		return ChallengeResponse{}, fmt.Errorf("%w: sign challenge response: %v", ErrInvalidSignature, err)
	}
	if len(response.Signature) != SignatureSize {
		return ChallengeResponse{}, fmt.Errorf("%w: signature length %d", ErrInvalidSignature, len(response.Signature))
	}
	return cloneChallengeResponse(response), nil
}

                                                                           
                                                                          
                                                                              
func VerifyChallengeResponse(challenge Challenge, response ChallengeResponse, expectedChainID uint64) error {
	if err := VerifyChallenge(challenge, expectedChainID, challenge.ResponderID); err != nil {
		return err
	}
	if err := validateUnsignedResponse(challenge, response); err != nil {
		return err
	}
	if len(response.Signature) != SignatureSize || !utils.VerifyECDSASignature(response.ResponderPublicKey, ChallengeResponseDigest(response), response.Signature) {
		return ErrInvalidSignature
	}
	return nil
}

func validateUnsignedResponse(challenge Challenge, response ChallengeResponse) error {
	if response.Version != ProtocolVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedVersion, response.Version)
	}
	if response.ChainID != challenge.ChainID {
		return fmt.Errorf("%w: response chain %d request chain %d", ErrWrongChain, response.ChainID, challenge.ChainID)
	}
	expectedDigest := ChallengeDigest(challenge)
	if len(response.RequestDigest) != sha256.Size || !bytes.Equal(response.RequestDigest, expectedDigest) {
		return fmt.Errorf("servicepeer: challenge response request digest mismatch")
	}
	if response.ChallengerID != challenge.ChallengerID || response.ResponderID != challenge.ResponderID || response.RecordSequence != challenge.RecordSequence || response.RequestedHeight != challenge.RequestedHeight || !bytes.Equal(response.Nonce, challenge.Nonce) {
		return fmt.Errorf("servicepeer: challenge response binding mismatch")
	}
	if err := verifyIdentity(response.ResponderID, response.ResponderPublicKey); err != nil {
		return err
	}
	if challenge.RequestedHeight == ConnectivityProbeHeight {
		if response.Available || len(response.PayloadDigest) != 0 {
			return fmt.Errorf("servicepeer: connectivity-only response must not include block payload")
		}
		return nil
	}
	if response.Available {
		if len(response.PayloadDigest) != sha256.Size {
			return fmt.Errorf("servicepeer: available response payload digest length %d", len(response.PayloadDigest))
		}
	} else if len(response.PayloadDigest) != 0 {
		return fmt.Errorf("servicepeer: unavailable response must not include payload digest")
	}
	return nil
}

                                                                 
func ChallengeResponseDigest(response ChallengeResponse) []byte {
	h := sha256.New()
	writeBytes(h, responseDomain)
	writeUint32(h, response.Version)
	writeUint64(h, response.ChainID)
	writeBytes(h, response.RequestDigest)
	writeString(h, response.ChallengerID)
	writeString(h, response.ResponderID)
	writeBytes(h, response.ResponderPublicKey)
	writeUint64(h, response.RecordSequence)
	writeBytes(h, response.Nonce)
	writeUint64(h, response.RequestedHeight)
	if response.Available {
		writeUint32(h, 1)
	} else {
		writeUint32(h, 0)
	}
	writeBytes(h, response.PayloadDigest)
	return h.Sum(nil)
}

func cloneChallenge(challenge Challenge) Challenge {
	challenge.ChallengerPublicKey = append([]byte(nil), challenge.ChallengerPublicKey...)
	challenge.Nonce = append([]byte(nil), challenge.Nonce...)
	challenge.Signature = append([]byte(nil), challenge.Signature...)
	return challenge
}

func cloneChallengeResponse(response ChallengeResponse) ChallengeResponse {
	response.RequestDigest = append([]byte(nil), response.RequestDigest...)
	response.ResponderPublicKey = append([]byte(nil), response.ResponderPublicKey...)
	response.Nonce = append([]byte(nil), response.Nonce...)
	response.PayloadDigest = append([]byte(nil), response.PayloadDigest...)
	response.Signature = append([]byte(nil), response.Signature...)
	return response
}
