                          
                                                     
                                                                                       

package security

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"math/big"
	"runtime"

	"dex/frost/core/curve"

	"github.com/btcsuite/btcd/btcec/v2"
)

var (
	ErrInvalidCiphertext     = errors.New("invalid ciphertext format")
	ErrMacVerificationFailed = errors.New("mac verification failed")
	ErrInvalidPublicKey      = errors.New("invalid public key")
	ErrInvalidPrivateKey     = errors.New("invalid private key")
)

                                     
                                                                                                    
type ECIESCiphertext struct {
	EphemeralPubKey []byte                                  
	Encrypted       []byte                   
	Mac             []byte               
}

                                             
                                                              
                                                   
                                                                     
func ECIESEncrypt(recipientPubKey, plaintext, randomness []byte) ([]byte, error) {
	if len(recipientPubKey) != 33 {
		return nil, ErrInvalidPublicKey
	}
	if len(randomness) != 32 {
		return nil, errors.New("randomness must be 32 bytes")
	}

	                        
	pubKey, err := btcec.ParsePubKey(recipientPubKey)
	if err != nil {
		return nil, ErrInvalidPublicKey
	}

	                                       
	var privKeyBytes [32]byte
	copy(privKeyBytes[:], randomness)
	defer wipeECIESBytes(privKeyBytes[:])
	ephemeralPriv := secp256k1PrivKeyFromBytes(privKeyBytes[:])
	defer zeroECIESPrivateKey(ephemeralPriv)

	                     
	ephemeralPub := ephemeralPriv.PubKey()
	ephemeralPubBytes := ephemeralPub.SerializeCompressed()

	                            
	encKey, macKey, err := deriveECIESKeys(pubKey, ephemeralPriv)
	if err != nil {
		return nil, err
	}
	defer wipeECIESBytes(encKey)
	defer wipeECIESBytes(macKey)

	                 
	encrypted, err := aesCTREncrypt(encKey, plaintext)
	if err != nil {
		return nil, err
	}

	              
	mac := computeHMAC(macKey, encrypted)
	defer wipeECIESBytes(mac)

	                                                  
	result := make([]byte, 0, 33+len(encrypted)+32)
	result = append(result, ephemeralPubBytes...)
	result = append(result, encrypted...)
	result = append(result, mac...)

	return result, nil
}

                                                                               
                                                              
                          
                              
                                    
func ECIESVerifyCiphertext(recipientPubKey, plaintext, randomness, ciphertext []byte) bool {
	               
	recomputed, err := ECIESEncrypt(recipientPubKey, plaintext, randomness)
	if err != nil {
		return false
	}

	               
	return bytes.Equal(recomputed, ciphertext)
}

                                                             
func secp256k1PrivKeyFromBytes(privKeyBytes []byte) *btcec.PrivateKey {
	privKey, _ := btcec.PrivKeyFromBytes(privKeyBytes)
	return privKey
}

                               
func aesCTREncrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	                                                      
	iv := make([]byte, aes.BlockSize)
	defer wipeECIESBytes(iv)

	ciphertext := make([]byte, len(plaintext))
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(ciphertext, plaintext)

	return ciphertext, nil
}

                                 
func computeHMAC(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

                                           
func ParseECIESCiphertext(ciphertext []byte, plaintextLen int) (*ECIESCiphertext, error) {
	                                                       
	expectedLen := 33 + plaintextLen + 32
	if len(ciphertext) != expectedLen {
		return nil, ErrInvalidCiphertext
	}

	return &ECIESCiphertext{
		EphemeralPubKey: ciphertext[:33],
		Encrypted:       ciphertext[33 : 33+plaintextLen],
		Mac:             ciphertext[33+plaintextLen:],
	}, nil
}

                                             
                                                   
                                                              
                        
func ECIESDecrypt(recipientPrivKey, ciphertext []byte) ([]byte, error) {
	if len(recipientPrivKey) != 32 {
		return nil, ErrInvalidPrivateKey
	}
	if len(ciphertext) < 33+32+32 {                                                                      
		return nil, ErrInvalidCiphertext
	}

	                     
	ephemeralPubBytes := ciphertext[:33]
	ephemeralPub, err := btcec.ParsePubKey(ephemeralPubBytes)
	if err != nil {
		return nil, ErrInvalidCiphertext
	}

	                        
	recipientPriv := secp256k1PrivKeyFromBytes(recipientPrivKey)
	defer zeroECIESPrivateKey(recipientPriv)

	                            
	encKey, macKey, err := deriveECIESKeys(ephemeralPub, recipientPriv)
	if err != nil {
		return nil, err
	}
	defer wipeECIESBytes(encKey)
	defer wipeECIESBytes(macKey)

	                            
	encryptedLen := len(ciphertext) - 33 - 32
	encrypted := ciphertext[33 : 33+encryptedLen]
	mac := ciphertext[33+encryptedLen:]

	             
	expectedMac := computeHMAC(macKey, encrypted)
	defer wipeECIESBytes(expectedMac)
	if !hmac.Equal(mac, expectedMac) {
		return nil, ErrMacVerificationFailed
	}

	                 
	plaintext, err := aesCTRDecrypt(encKey, encrypted)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

                               
func aesCTRDecrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	                                          
	iv := make([]byte, aes.BlockSize)
	defer wipeECIESBytes(iv)

	plaintext := make([]byte, len(ciphertext))
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(plaintext, ciphertext)

	return plaintext, nil
}

                                                                             
                                                                        
                                                                         
                 
func deriveECIESKeys(peer *btcec.PublicKey, privateKey *btcec.PrivateKey) ([]byte, []byte, error) {
	if peer == nil || privateKey == nil {
		return nil, nil, ErrInvalidPrivateKey
	}
	privateScalar := privateKey.Serialize()
	defer wipeECIESBytes(privateScalar)

	sharedX, sharedY := btcec.S256().ScalarMult(peer.X(), peer.Y(), privateScalar)
	if sharedX != nil {
		defer zeroECIESBigInt(sharedX)
	}
	if sharedY != nil {
		defer zeroECIESBigInt(sharedY)
	}
	if sharedX == nil || sharedY == nil {
		return nil, nil, ErrInvalidPublicKey
	}
	sharedXBytes := sharedX.Bytes()
	defer wipeECIESBytes(sharedXBytes)
	sharedSecret := sha256.Sum256(sharedXBytes)
	defer wipeECIESBytes(sharedSecret[:])

	encKey := append([]byte(nil), sharedSecret[:16]...)
	macKey := append([]byte(nil), sharedSecret[16:]...)
	return encKey, macKey, nil
}

func wipeECIESBytes(data []byte) {
	clear(data)
	runtime.KeepAlive(data)
}

func zeroECIESBigInt(value *big.Int) {
	if value == nil {
		return
	}
	words := value.Bits()
	for index := range words {
		words[index] = 0
	}
	value.SetInt64(0)
	runtime.KeepAlive(words)
	runtime.KeepAlive(value)
}

func zeroECIESPrivateKey(key *btcec.PrivateKey) {
	if key == nil {
		return
	}
	key.Zero()
	runtime.KeepAlive(key)
}

                                                                                                                
                         
                                                              
                                                 
  
                                                                                                      
                                                                                                           
                                   
func VerifyShareAgainstCommitment(share []byte, commitmentPoints [][]byte, receiverIndex *big.Int) bool {
	if len(share) == 0 || len(commitmentPoints) == 0 {
		return false
	}

	                 
	shareInt := new(big.Int).SetBytes(share)
	gShareX, gShareY := btcec.S256().ScalarBaseMult(shareInt.Bytes())

	                                                     
	                                                               
	var expectedX, expectedY *big.Int

	xPower := big.NewInt(1)           
	curve := btcec.S256()
	n := curve.Params().N

	for k, pointBytes := range commitmentPoints {
		                  
		pubKey, err := btcec.ParsePubKey(pointBytes)
		if err != nil {
			return false
		}

		             
		termX, termY := curve.ScalarMult(pubKey.X(), pubKey.Y(), xPower.Bytes())

		if k == 0 {
			expectedX, expectedY = termX, termY
		} else {
			expectedX, expectedY = curve.Add(expectedX, expectedY, termX, termY)
		}

		                    
		xPower.Mul(xPower, receiverIndex)
		xPower.Mod(xPower, n)
	}

	                             
	return gShareX.Cmp(expectedX) == 0 && gShareY.Cmp(expectedY) == 0
}

                                                                                  
                                                                                                                        
  
                         
                                                                                                               
                                                 
                                                 
func VerifyShareAgainstCommitmentOnGroup(share []byte, commitmentPoints [][]byte, receiverIndex *big.Int, group curve.Group) bool {
	if len(share) == 0 || len(commitmentPoints) == 0 || receiverIndex == nil || group == nil {
		return false
	}

	          
	shareInt := new(big.Int).SetBytes(share)
	gSharePt := group.ScalarBaseMult(shareInt)

	                                    
	var expected curve.Point
	xPower := big.NewInt(1)
	n := group.Order()

	for k, pointBytes := range commitmentPoints {
		Ak := group.DecompressPoint(pointBytes)
		if Ak.X == nil || Ak.Y == nil {
			return false
		}
		term := group.ScalarMult(Ak, xPower)
		if k == 0 {
			expected = term
		} else {
			expected = group.Add(expected, term)
		}
		xPower.Mul(xPower, receiverIndex)
		xPower.Mod(xPower, n)
	}

	if expected.X == nil || expected.Y == nil {
		return false
	}
	return gSharePt.X.Cmp(expected.X) == 0 && gSharePt.Y.Cmp(expected.Y) == 0
}
