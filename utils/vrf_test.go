package utils

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"dex/types"
	"testing"
)

                                                                   
func TestVRFGenerateAndVerify(t *testing.T) {
	                          
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate ECDSA key: %v", err)
	}

	                        
	vrfProvider := NewVRFProvider()

	                  
	height := uint64(1)
	window := 2
	parentBlockHash := "genesis"
	nodeID := types.NodeID("test-node-1")

	               
	t.Logf("Generating VRF with:")
	t.Logf("  Height: %d", height)
	t.Logf("  Window: %d", window)
	t.Logf("  ParentHash: %s", parentBlockHash)
	t.Logf("  NodeID: %s", nodeID)

	vrfOutput, vrfProof, err := vrfProvider.GenerateVRF(privateKey, height, window, parentBlockHash, nodeID)
	if err != nil {
		t.Fatalf("Failed to generate VRF: %v", err)
	}
	if len(vrfProof) != VRFProofSize {
		t.Fatalf("VRF proof length = %d, want %d", len(vrfProof), VRFProofSize)
	}
	if len(vrfOutput) != VRFOutputSize {
		t.Fatalf("VRF output length = %d, want %d", len(vrfOutput), VRFOutputSize)
	}

	t.Logf("VRF generated successfully:")
	t.Logf("  Output length: %d bytes", len(vrfOutput))
	t.Logf("  Proof length: %d bytes", len(vrfProof))
	t.Logf("  Output: %x", vrfOutput)
	t.Logf("  Proof: %x", vrfProof)

	                                    
	t.Logf("\nVerifying VRF with BLS public key...")

	                                
	pubKey, err := GetPublicKey(privateKey)
	if err != nil {
		t.Fatalf("Failed to get BLS public key: %v", err)
	}

	err = vrfProvider.VerifyVRFWithPublicKey(pubKey, height, window, parentBlockHash, nodeID, vrfProof, vrfOutput)
	if err != nil {
		t.Fatalf("VRF verification failed: %v", err)
	}

	t.Logf("✓ VRF verification succeeded!")
}

                                                                            
func TestVRFWithDifferentKeys(t *testing.T) {
	                                 
	privateKey1, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	privateKey2, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	publicKey2, err := GetPublicKey(privateKey2)
	if err != nil {
		t.Fatalf("Failed to get second BLS public key: %v", err)
	}

	vrfProvider := NewVRFProvider()

	height := uint64(1)
	window := 2
	parentBlockHash := "genesis"
	nodeID := types.NodeID("test-node-1")

	                             
	vrfOutput, vrfProof, err := vrfProvider.GenerateVRF(privateKey1, height, window, parentBlockHash, nodeID)
	if err != nil {
		t.Fatalf("Failed to generate VRF: %v", err)
	}

	                                           
	err = vrfProvider.VerifyVRFWithPublicKey(publicKey2, height, window, parentBlockHash, nodeID, vrfProof, vrfOutput)
	if err == nil {
		t.Fatalf("VRF verification should have failed with different key, but succeeded")
	}

	t.Logf("✓ VRF correctly failed verification with different key: %v", err)
}

                                                                                  
func TestVRFWithDifferentParameters(t *testing.T) {
	privateKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	publicKey, err := GetPublicKey(privateKey)
	if err != nil {
		t.Fatalf("Failed to get BLS public key: %v", err)
	}

	vrfProvider := NewVRFProvider()

	height := uint64(1)
	window := 2
	parentBlockHash := "genesis"
	nodeID := types.NodeID("test-node-1")

	            
	vrfOutput, vrfProof, err := vrfProvider.GenerateVRF(privateKey, height, window, parentBlockHash, nodeID)
	if err != nil {
		t.Fatalf("Failed to generate VRF: %v", err)
	}

	                                                         
	err = vrfProvider.VerifyVRFWithPublicKey(publicKey, height+1, window, parentBlockHash, nodeID, vrfProof, vrfOutput)
	if err == nil {
		t.Errorf("VRF verification should have failed with different height")
	} else {
		t.Logf("✓ Correctly failed with different height: %v", err)
	}

	                                                         
	err = vrfProvider.VerifyVRFWithPublicKey(publicKey, height, window+1, parentBlockHash, nodeID, vrfProof, vrfOutput)
	if err == nil {
		t.Errorf("VRF verification should have failed with different window")
	} else {
		t.Logf("✓ Correctly failed with different window: %v", err)
	}

	                                                                  
	err = vrfProvider.VerifyVRFWithPublicKey(publicKey, height, window, "different-parent", nodeID, vrfProof, vrfOutput)
	if err == nil {
		t.Errorf("VRF verification should have failed with different parent hash")
	} else {
		t.Logf("✓ Correctly failed with different parent hash: %v", err)
	}

	                                                         
	err = vrfProvider.VerifyVRFWithPublicKey(publicKey, height, window, parentBlockHash, types.NodeID("different-node"), vrfProof, vrfOutput)
	if err == nil {
		t.Errorf("VRF verification should have failed with different nodeID")
	} else {
		t.Logf("✓ Correctly failed with different nodeID: %v", err)
	}
}

                                                         
func TestVRFKeyDerivation(t *testing.T) {
	                       
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate ECDSA key: %v", err)
	}

	                        
	blsPrivKey1, err := GetBLSPrivateKey(privateKey)
	if err != nil {
		t.Fatalf("Failed to derive BLS private key (1st time): %v", err)
	}

	blsPrivKey2, err := GetBLSPrivateKey(privateKey)
	if err != nil {
		t.Fatalf("Failed to derive BLS private key (2nd time): %v", err)
	}

	                                          
	if blsPrivKey1.String() != blsPrivKey2.String() {
		t.Errorf("BLS private key derivation is not deterministic")
	} else {
		t.Logf("✓ BLS private key derivation is deterministic")
	}

	                                                                                     
	                                                               
	pubKey1, err := GetPublicKey(privateKey)
	if err != nil {
		t.Fatalf("Failed to derive BLS public key (1st time): %v", err)
	}

	pubKey2, err := GetPublicKey(privateKey)
	if err != nil {
		t.Fatalf("Failed to derive BLS public key (2nd time): %v", err)
	}

	                                          
	if pubKey1.String() != pubKey2.String() {
		t.Errorf("BLS public key derivation is not deterministic")
	} else {
		t.Logf("✓ BLS public key derivation is deterministic")
	}

	t.Logf("BLS Private Key: %s", blsPrivKey1.String())
	t.Logf("BLS Public Key: %s", pubKey1.String())
}

                                                                                
func TestVRFRealScenario(t *testing.T) {
	t.Log("=== Simulating Real VRF Scenario ===")

	                                                     

	                                     
	proposerPrivKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	                  
	height := uint64(100)
	window := 2
	parentBlockHash := "block-99-hash"
	proposerNodeID := types.NodeID("fbc1qxdr4f5mr230u2j6hu2pv4zpvr29ade8vxzvgx3")

	t.Logf("\n--- Proposer (Node A) ---")
	t.Logf("Proposer NodeID: %s", proposerNodeID)
	t.Logf("Block Height: %d", height)
	t.Logf("Window: %d", window)
	t.Logf("Parent Hash: %s", parentBlockHash)

	                      
	vrfProvider := NewVRFProvider()
	vrfOutput, vrfProof, err := vrfProvider.GenerateVRF(
		proposerPrivKey,
		height,
		window,
		parentBlockHash,
		proposerNodeID,
	)
	if err != nil {
		t.Fatalf("Proposer failed to generate VRF: %v", err)
	}

	t.Logf("\nVRF Generated:")
	t.Logf("  Output: %x", vrfOutput)
	t.Logf("  Proof: %x", vrfProof)

	                                                                   
	                     
	block := struct {
		Height    uint64
		Window    int
		ParentID  string
		Proposer  string
		VRFProof  []byte
		VRFOutput []byte
	}{
		Height:    height,
		Window:    window,
		ParentID:  parentBlockHash,
		Proposer:  string(proposerNodeID),
		VRFProof:  vrfProof,
		VRFOutput: vrfOutput,
	}

	t.Logf("\n--- Validator (Node B) ---")
	t.Logf("Received block from proposer: %s", block.Proposer)

	                      
	                                                                            
	                                                                                         
	proposerPubKey, err := GetPublicKey(proposerPrivKey)
	if err != nil {
		t.Fatalf("Failed to get proposer's BLS public key: %v", err)
	}

	                                                      
	err = vrfProvider.VerifyVRFWithPublicKey(
		proposerPubKey,
		block.Height,
		block.Window,
		block.ParentID,
		types.NodeID(block.Proposer),
		block.VRFProof,
		block.VRFOutput,
	)

	if err != nil {
		t.Fatalf("Validator failed to verify VRF: %v", err)
	}

	t.Logf("✓ VRF verification succeeded!")
	t.Logf("\n=== Real Scenario Test Passed ===")
}

                                                         
func TestVRFProbabilityCalculation(t *testing.T) {
	privateKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	vrfProvider := NewVRFProvider()

	height := uint64(1)
	window := 2
	parentBlockHash := "genesis"
	nodeID := types.NodeID("test-node")

	            
	vrfOutput, _, err := vrfProvider.GenerateVRF(privateKey, height, window, parentBlockHash, nodeID)
	if err != nil {
		t.Fatalf("Failed to generate VRF: %v", err)
	}

	                              
	thresholds := []float64{0.05, 0.15, 0.30, 0.50, 1.0}

	t.Logf("VRF Output: %x", vrfOutput)

	for _, threshold := range thresholds {
		shouldPropose := CalculateBlockProbability(vrfOutput, threshold)
		t.Logf("Threshold %.2f: Should propose = %v", threshold, shouldPropose)
	}
}
