package utils

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"dex/logs"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"runtime"
	"sync"

	"go.dedis.ch/kyber/v3"
	"go.dedis.ch/kyber/v3/group/mod"
	"go.dedis.ch/kyber/v3/pairing/bn256"
	"go.dedis.ch/kyber/v3/sign/bls"
)

const (
	maxCacheSize = 100

	                                                                       
	                                                            
	BLSSignatureSize = 64
	                                                                       
	                                                             
	BLSPublicKeySize = 128
)

                        
                                                                                                                    
var (
	blsSignCache = make(map[string][]byte)
	cacheKeys    = make([]string, 0, maxCacheSize)
	cacheMutex   sync.Mutex
)

                                                                               
                                                                                                               
            
                                                         
                                             
  
                                                   
func BLSSignWithCache(priv *ecdsa.PrivateKey, msg string) ([]byte, error) {
	if priv == nil || priv.D == nil || priv.Curve == nil || priv.PublicKey.X == nil || priv.PublicKey.Y == nil {
		return nil, errors.New("invalid ECDSA private key")
	}
	                                                                        
	                                                                      
	cacheDigest := sha256.New()
	_, _ = cacheDigest.Write([]byte("dex/bls-sign-cache/v1\x00"))
	_, _ = cacheDigest.Write(elliptic.MarshalCompressed(priv.Curve, priv.PublicKey.X, priv.PublicKey.Y))
	_, _ = cacheDigest.Write([]byte(msg))
	cacheKey := hex.EncodeToString(cacheDigest.Sum(nil))

	                                       
	cacheMutex.Lock()
	if sig, exists := blsSignCache[cacheKey]; exists {
		cacheMutex.Unlock()
		return sig, nil
	}
	cacheMutex.Unlock()

	                                                
	suite := bn256.NewSuite()

	                                              
	                                                                                   
	                                                            
	privScalar, err := GetBLSPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	defer clearBLSPrivateScalar(privScalar)
	                                                                     
	signature, err := bls.Sign(suite, privScalar, []byte(msg))
	if err != nil {
		return nil, err
	}

	                        
	cacheMutex.Lock()
	                                                                    
	if len(blsSignCache) >= maxCacheSize {
		oldestKey := cacheKeys[0]
		cacheKeys = cacheKeys[1:]
		delete(blsSignCache, oldestKey)
	}
	                                                  
	blsSignCache[cacheKey] = signature
	cacheKeys = append(cacheKeys, cacheKey)
	cacheMutex.Unlock()

	return signature, nil
}

                                                    
func GetBLSPrivateKey(priv *ecdsa.PrivateKey) (kyber.Scalar, error) {
	if priv == nil || priv.D == nil || priv.D.Sign() <= 0 {
		return nil, errors.New("invalid ECDSA private key")
	}
	                                                      
	                                                                                                  
	raw := priv.D.Bytes()
	defer clearKeyBytes(raw)
	hash := sha256.Sum256(raw)
	defer clearKeyBytes(hash[:])
	suite := bn256.NewSuite()
	blsPrivateKey := suite.G2().Scalar().SetBytes(hash[:])

	return blsPrivateKey, nil
}

                                                                                                
            
                                                  
                                       
                              
  
               
                                                                  
func BLSVerifySignature(pub kyber.Point, msg string, signature []byte) error {
	suite := bn256.NewSuite()
	canonicalPub, err := canonicalBLSPublicKeyPoint(suite, pub)
	if err != nil {
		return err
	}
	if _, err := decodeCanonicalBLSG1Point(suite, signature, "BLS signature"); err != nil {
		return err
	}
	                                                                          
	                                                                      
	                                         
	return bls.Verify(suite, canonicalPub, []byte(msg), signature)
}

                                                 
func GetPublicKey(priv *ecdsa.PrivateKey) (kyber.Point, error) {
	blsPrivateKey, err := GetBLSPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	defer clearBLSPrivateScalar(blsPrivateKey)

	suite := bn256.NewSuite()
	blsPublicKey := suite.G2().Point().Mul(blsPrivateKey, nil)

	return blsPublicKey, nil
}

                                                                          
                                                                               
                                                                       
func clearBLSPrivateScalar(scalar kyber.Scalar) {
	if scalar == nil {
		return
	}
	if integer, ok := scalar.(*mod.Int); ok && integer != nil {
		words := integer.V.Bits()
		clear(words[:cap(words)])
		integer.Zero()
		runtime.KeepAlive(words)
	} else {
		scalar.Zero()
	}
	runtime.KeepAlive(scalar)
}

                         
func AggregateBLS(sigs [][]byte) ([]byte, error) {
	suite := bn256.NewSuite()
	if len(sigs) == 0 {
		return nil, fmt.Errorf("cannot aggregate empty BLS signature set")
	}
	for i, sig := range sigs {
		if _, err := decodeCanonicalBLSG1Point(suite, sig, "BLS signature"); err != nil {
			return nil, fmt.Errorf("invalid BLS signature %d: %w", i, err)
		}
	}
	aggSig, err := bls.AggregateSignatures(suite, sigs...)
	if err != nil {
		logs.Error("failed to aggregate signatures: %v", err)
		return nil, err
	}
	if _, err := decodeCanonicalBLSG1Point(suite, aggSig, "aggregate BLS signature"); err != nil {
		return nil, err
	}
	return aggSig, nil
}

                                                    
func SerializePublicKey(pub kyber.Point) ([]byte, error) {
	suite := bn256.NewSuite()
	canonicalPub, err := canonicalBLSPublicKeyPoint(suite, pub)
	if err != nil {
		return nil, err
	}
	return canonicalPub.MarshalBinary()
}

                                                         
func DeserializePublicKey(data []byte) (kyber.Point, error) {
	suite := bn256.NewSuite()
	return decodeCanonicalBLSPublicKey(suite, data)
}

                                                                                                             
func BLSAggregateVerify(pubKeys []kyber.Point, msg []byte, aggSig []byte) error {
	suite := bn256.NewSuite()
	if len(pubKeys) == 0 {
		return fmt.Errorf("cannot verify empty BLS public key set")
	}
	canonicalPubKeys := make([]kyber.Point, len(pubKeys))
	for i, pub := range pubKeys {
		canonical, err := canonicalBLSPublicKeyPoint(suite, pub)
		if err != nil {
			return fmt.Errorf("invalid BLS public key %d: %w", i, err)
		}
		canonicalPubKeys[i] = canonical
	}
	if _, err := decodeCanonicalBLSG1Point(suite, aggSig, "aggregate BLS signature"); err != nil {
		return err
	}
	aggPub := bls.AggregatePublicKeys(suite, canonicalPubKeys...)
	if _, err := canonicalBLSPublicKeyPoint(suite, aggPub); err != nil {
		return fmt.Errorf("invalid aggregate BLS public key: %w", err)
	}
	return bls.Verify(suite, aggPub, msg, aggSig)
}

func canonicalBLSPublicKeyPoint(suite *bn256.Suite, pub kyber.Point) (kyber.Point, error) {
	if pub == nil {
		return nil, fmt.Errorf("BLS public key is nil")
	}
	raw, err := pub.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("marshal BLS public key: %w", err)
	}
	return decodeCanonicalBLSPublicKey(suite, raw)
}

func decodeCanonicalBLSPublicKey(suite *bn256.Suite, raw []byte) (kyber.Point, error) {
	if len(raw) != BLSPublicKeySize {
		return nil, fmt.Errorf("invalid BLS public key length: got %d, want %d", len(raw), BLSPublicKeySize)
	}
	point := suite.G2().Point()
	if err := point.UnmarshalBinary(raw); err != nil {
		return nil, fmt.Errorf("invalid BLS public key encoding: %w", err)
	}
	canonical, err := point.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("marshal BLS public key for canonical check: %w", err)
	}
	if !bytes.Equal(raw, canonical) {
		return nil, fmt.Errorf("non-canonical BLS public key encoding")
	}
	if point.Equal(suite.G2().Point().Null()) {
		return nil, fmt.Errorf("invalid BLS public key: identity point")
	}
	if !isBN256PrimeOrderPoint(suite.G2(), point) {
		return nil, fmt.Errorf("invalid BLS public key: point is outside the prime-order subgroup")
	}
	return point, nil
}

func decodeCanonicalBLSG1Point(suite *bn256.Suite, raw []byte, label string) (kyber.Point, error) {
	if len(raw) != BLSSignatureSize {
		return nil, fmt.Errorf("invalid %s length: got %d, want %d", label, len(raw), BLSSignatureSize)
	}
	point := suite.G1().Point()
	if err := point.UnmarshalBinary(raw); err != nil {
		return nil, fmt.Errorf("invalid %s encoding: %w", label, err)
	}
	canonical, err := point.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("marshal %s for canonical check: %w", label, err)
	}
	if !bytes.Equal(raw, canonical) {
		return nil, fmt.Errorf("non-canonical %s encoding", label)
	}
	if point.Equal(suite.G1().Point().Null()) {
		return nil, fmt.Errorf("invalid %s: identity point", label)
	}
	if !isBN256PrimeOrderPoint(suite.G1(), point) {
		return nil, fmt.Errorf("invalid %s: point is outside the prime-order subgroup", label)
	}
	return point, nil
}

                                                                              
                                                                            
                                                                            
func isBN256PrimeOrderPoint(group kyber.Group, point kyber.Point) bool {
	order := new(big.Int).Set(bn256.Order)
	orderScalar := mod.NewInt(order, new(big.Int).Add(new(big.Int).Set(order), big.NewInt(1)))
	product := group.Point().Mul(orderScalar, point)
	return product.Equal(group.Point().Null())
}
