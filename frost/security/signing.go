                            
                        

package security

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"dex/pb"
	"fmt"
	"math/big"
)

                                    
func SignMessage(privateKey *ecdsa.PrivateKey, msg []byte) ([]byte, error) {
	hash := sha256.Sum256(msg)

	r, s, err := ecdsa.Sign(rand.Reader, privateKey, hash[:])
	if err != nil {
		return nil, fmt.Errorf("sign failed: %w", err)
	}

	                                               
	sig := make([]byte, 64)
	rBytes := r.Bytes()
	sBytes := s.Bytes()
	copy(sig[32-len(rBytes):32], rBytes)
	copy(sig[64-len(sBytes):64], sBytes)

	return sig, nil
}

                                   
func VerifyMessage(publicKey *ecdsa.PublicKey, msg, sig []byte) bool {
	if len(sig) != 64 {
		return false
	}

	hash := sha256.Sum256(msg)

	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])

	return ecdsa.Verify(publicKey, hash[:], r, s)
}

                                                   
func SignFrostEnvelope(privateKey *ecdsa.PrivateKey, env *pb.FrostEnvelope) error {
	                     
	env.Sig = nil

	                                    
	msgToSign := serializeEnvelopeForSigning(env)

	sig, err := SignMessage(privateKey, msgToSign)
	if err != nil {
		return err
	}

	env.Sig = sig
	return nil
}

                                                  
func VerifyFrostEnvelope(publicKey *ecdsa.PublicKey, env *pb.FrostEnvelope) bool {
	if len(env.Sig) == 0 {
		return false
	}

	               
	sig := env.Sig
	env.Sig = nil

	                                    
	msgToSign := serializeEnvelopeForSigning(env)

	               
	env.Sig = sig

	return VerifyMessage(publicKey, msgToSign, sig)
}

                                                              
func serializeEnvelopeForSigning(env *pb.FrostEnvelope) []byte {
	h := sha256.New()
	h.Write([]byte(env.From))
	h.Write([]byte(env.To))
	h.Write([]byte(fmt.Sprintf("%d", env.Kind)))
	h.Write(env.Payload)
	h.Write([]byte(env.JobId))
	h.Write([]byte(fmt.Sprintf("%d", env.Seq)))
	return h.Sum(nil)
}

                                                        
func BindTemplateHash(jobID string, templateHash []byte) []byte {
	h := sha256.New()
	h.Write([]byte("frost_template_binding"))
	h.Write([]byte(jobID))
	h.Write(templateHash)
	return h.Sum(nil)
}

                                                    
func VerifyTemplateBinding(jobID string, expectedHash, actualHash []byte) bool {
	expected := BindTemplateHash(jobID, expectedHash)
	actual := BindTemplateHash(jobID, actualHash)

	if len(expected) != len(actual) {
		return false
	}

	for i := range expected {
		if expected[i] != actual[i] {
			return false
		}
	}
	return true
}
