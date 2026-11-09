package utils

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"dex/pb"
	"dex/types"
	"encoding/binary"
	"fmt"
	"math"

	"go.dedis.ch/kyber/v3"
	"go.dedis.ch/kyber/v3/pairing/bn256"
	"go.dedis.ch/kyber/v3/sign/bls"
)

const (
	                                                                      
	                                                                
	VRFProofSize = BLSSignatureSize
	                                                                    
	VRFOutputSize = sha256.Size

	vrfInputDomain = "dex/consensus-vrf/v1"
)

                                 
type VRFProvider struct{}

                                    
func NewVRFProvider() *VRFProvider {
	return &VRFProvider{}
}

                                       
                                 
func (v *VRFProvider) GenerateVRF(privateKey *ecdsa.PrivateKey, height uint64, window int, parentBlockHash string, nodeID types.NodeID) ([]byte, []byte, error) {
	if privateKey == nil {
		return nil, nil, fmt.Errorf("private key is nil")
	}

	                     
	vrfInput, err := v.constructVRFInput(height, window, parentBlockHash, nodeID)
	if err != nil {
		return nil, nil, err
	}

	                              
	suite := bn256.NewSuite()

	                                   
	blsPrivateKey, err := GetBLSPrivateKey(privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get BLS private key: %w", err)
	}
	defer clearBLSPrivateScalar(blsPrivateKey)

	                                          
	vrfProof, err := bls.Sign(suite, blsPrivateKey, vrfInput)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate VRF proof: %w", err)
	}
	if err := validateVRFProofEncoding(suite, vrfProof); err != nil {
		return nil, nil, fmt.Errorf("generated invalid VRF proof: %w", err)
	}

	                                                            
	vrfOutput := sha256.Sum256(vrfProof)

	return vrfOutput[:], vrfProof, nil
}

                                                     
                                                                             
                                                      
func (v *VRFProvider) VerifyVRFWithPublicKey(publicKey kyber.Point, height uint64, window int, parentBlockHash string, nodeID types.NodeID, proof []byte, output []byte) error {
	                     
	vrfInput, err := v.constructVRFInput(height, window, parentBlockHash, nodeID)
	if err != nil {
		return err
	}

	suite := bn256.NewSuite()
	return verifyVRFProofAndOutput(suite, publicKey, vrfInput, proof, output)
}

                                                                           
                                                                        
                                                                   
func verifyVRFProofAndOutput(suite *bn256.Suite, publicKey kyber.Point, vrfInput, proof, output []byte) error {
	canonicalPublicKey, err := canonicalBLSPublicKeyPoint(suite, publicKey)
	if err != nil {
		return fmt.Errorf("invalid VRF public key: %w", err)
	}
	if err := validateVRFProofEncoding(suite, proof); err != nil {
		return err
	}
	if len(output) != VRFOutputSize {
		return fmt.Errorf("invalid VRF output length: got %d, want %d", len(output), VRFOutputSize)
	}

	if err := bls.Verify(suite, canonicalPublicKey, vrfInput, proof); err != nil {
		return fmt.Errorf("VRF proof verification failed: %w", err)
	}

	expectedOutput := sha256.Sum256(proof)
	if !bytes.Equal(output, expectedOutput[:]) {
		return fmt.Errorf("VRF output mismatch")
	}
	return nil
}

func validateVRFProofEncoding(suite *bn256.Suite, proof []byte) error {
	_, err := decodeCanonicalBLSG1Point(suite, proof, "VRF proof")
	return err
}

                                                                            
                                                                            
                                                                             
                               
func (v *VRFProvider) constructVRFInput(height uint64, window int, parentBlockHash string, nodeID types.NodeID) ([]byte, error) {
	return constructVRFInputForChain(pb.GetDefaultChainID(), height, window, parentBlockHash, nodeID)
}

func constructVRFInputForChain(chainID, height uint64, window int, parentBlockHash string, nodeID types.NodeID) ([]byte, error) {
	if window < 0 || uint64(window) > math.MaxUint32 {
		return nil, fmt.Errorf("invalid VRF window %d", window)
	}
	parent := []byte(parentBlockHash)
	proposer := []byte(nodeID)
	if uint64(len(parent)) > math.MaxUint32 {
		return nil, fmt.Errorf("VRF parent block ID is too long: %d", len(parent))
	}
	if uint64(len(proposer)) > math.MaxUint32 {
		return nil, fmt.Errorf("VRF node ID is too long: %d", len(proposer))
	}

	preimage := make([]byte, 0, len(vrfInputDomain)+1+8+8+4+4+len(parent)+4+len(proposer))
	preimage = append(preimage, vrfInputDomain...)
	preimage = append(preimage, 0)
	preimage = binary.BigEndian.AppendUint64(preimage, chainID)
	preimage = binary.BigEndian.AppendUint64(preimage, height)
	preimage = binary.BigEndian.AppendUint32(preimage, uint32(window))
	preimage = binary.BigEndian.AppendUint32(preimage, uint32(len(parent)))
	preimage = append(preimage, parent...)
	preimage = binary.BigEndian.AppendUint32(preimage, uint32(len(proposer)))
	preimage = append(preimage, proposer...)

	digest := sha256.Sum256(preimage)
	return digest[:], nil
}

                                                                    
                                             
                       
                                 
func CalculateBlockProbability(vrfOutput []byte, threshold float64) bool {
	if len(vrfOutput) == 0 {
		return false
	}
	if threshold <= 0 {
		return false
	}
	if threshold >= 1 {
		return true
	}

	                                           
	var outputValue uint64
	if len(vrfOutput) >= 8 {
		outputValue = binary.BigEndian.Uint64(vrfOutput[:8])
	} else {
		                                         
		temp := make([]byte, 8)
		copy(temp, vrfOutput)
		outputValue = binary.BigEndian.Uint64(temp)
	}

	                                                     
	                                                                            
	maxUint64 := uint64(0xFFFFFFFFFFFFFFFF)
	thresholdValue := uint64(threshold * float64(maxUint64))

	return outputValue < thresholdValue
}
