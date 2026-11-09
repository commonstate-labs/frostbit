package utils

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"go.dedis.ch/kyber/v3"
	"go.dedis.ch/kyber/v3/pairing/bn256"
)

                                                            
func TestBLSSignWithCache(t *testing.T) {
	                                                
	cacheMutex.Lock()
	blsSignCache = make(map[string][]byte)
	cacheKeys = make([]string, 0, maxCacheSize)
	cacheMutex.Unlock()

	                                                                       
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey 失败: %v", err)
	}

	                                                            
	message := "test message"
	sig1, err := BLSSignWithCache(priv, message)
	if err != nil {
		t.Fatalf("BLSSignWithCache 返回错误: %v", err)
	}
	if len(sig1) == 0 {
		t.Fatal("BLSSignWithCache 返回了空的签名")
	}

	                                                                                                            
	sig2, err := BLSSignWithCache(priv, message)
	if err != nil {
		t.Fatalf("BLSSignWithCache 第二次调用返回错误: %v", err)
	}
	if !bytes.Equal(sig1, sig2) {
		t.Error("对于相同私钥和消息，多次签名返回的结果不一致，缓存机制可能存在问题")
	}

	                                                         
	anotherMessage := "another message"
	sig3, err := BLSSignWithCache(priv, anotherMessage)
	if err != nil {
		t.Fatalf("BLSSignWithCache 对不同消息签名返回错误: %v", err)
	}
	if bytes.Equal(sig1, sig3) {
		t.Error("不同消息的签名结果不应相同")
	}
}
func TestBLSVerifySignature(t *testing.T) {
	                         
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ECDSA key: %v", err)
	}

	message := "test message"

	                                    
	sig, err := BLSSignWithCache(priv, message)
	if err != nil {
		t.Fatalf("BLSSignWithCache returned error: %v", err)
	}
	if len(sig) == 0 {
		t.Fatal("BLSSignWithCache returned empty signature")
	}

	                                
	pub, err := GetPublicKey(priv)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}

	                                    
	if err := BLSVerifySignature(pub, message, sig); err != nil {
		t.Errorf("valid signature verification failed: %v", err)
	}

	                                                       
	wrongMsg := "wrong message"
	if err := BLSVerifySignature(pub, wrongMsg, sig); err == nil {
		t.Error("verification succeeded for wrong message; expected failure")
	}

	                                                       
	sigTampered := append([]byte{}, sig...)
	if len(sigTampered) > 0 {
		sigTampered[0] ^= 0xFF
	}
	if err := BLSVerifySignature(pub, message, sigTampered); err == nil {
		t.Error("verification succeeded for tampered signature; expected failure")
	}

	                                                                                        
	                                            
	sigWithSuffix := append(append([]byte(nil), sig...), 0x01)
	if err := BLSVerifySignature(pub, message, sigWithSuffix); err == nil || !strings.Contains(err.Error(), "invalid BLS signature length") {
		t.Fatalf("signature suffix was not rejected canonically: %v", err)
	}
}

func TestBLSCanonicalPublicKeyAndSignatureGate(t *testing.T) {
	suite := bn256.NewSuite()
	if got := suite.G1().Point().MarshalSize(); got != BLSSignatureSize {
		t.Fatalf("G1 signature size=%d want=%d", got, BLSSignatureSize)
	}
	if got := suite.G2().Point().MarshalSize(); got != BLSPublicKeySize {
		t.Fatalf("G2 public key size=%d want=%d", got, BLSPublicKeySize)
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := GetPublicKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pubRaw, err := pub.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DeserializePublicKey(pubRaw); err != nil {
		t.Fatalf("canonical public key rejected: %v", err)
	}

	for _, raw := range [][]byte{
		nil,
		append([]byte(nil), pubRaw[:BLSPublicKeySize-1]...),
		append(append([]byte(nil), pubRaw...), 0),
	} {
		if _, err := DeserializePublicKey(raw); err == nil || !strings.Contains(err.Error(), "invalid BLS public key length") {
			t.Fatalf("public key length %d error=%v", len(raw), err)
		}
	}

	identityPubRaw, err := suite.G2().Point().Null().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DeserializePublicKey(identityPubRaw); err == nil || !strings.Contains(err.Error(), "identity point") {
		t.Fatalf("identity public key error=%v", err)
	}

	nonCanonicalPub := equivalentNonCanonicalBN256Encoding(t, pubRaw)
	decoded := suite.G2().Point()
	if err := decoded.UnmarshalBinary(nonCanonicalPub); err != nil || !decoded.Equal(pub) {
		t.Fatalf("bad non-canonical G2 fixture: decoded=%v err=%v", decoded, err)
	}
	if _, err := DeserializePublicKey(nonCanonicalPub); err == nil || !strings.Contains(err.Error(), "non-canonical") {
		t.Fatalf("non-canonical public key error=%v", err)
	}

	identitySig, err := suite.G1().Point().Null().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err := BLSVerifySignature(pub, "identity", identitySig); err == nil || !strings.Contains(err.Error(), "identity point") {
		t.Fatalf("identity signature error=%v", err)
	}
	if err := BLSVerifySignature(suite.G2().Point().Null(), "identity", identitySig); err == nil || !strings.Contains(err.Error(), "identity point") {
		t.Fatalf("identity public key/signature pair error=%v", err)
	}
}

func TestBLSAggregateGateRejectsMalleableInputs(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const message = "aggregate-canonical-gate"
	sig, err := BLSSignWithCache(priv, message)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := GetPublicKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	agg, err := AggregateBLS([][]byte{sig})
	if err != nil {
		t.Fatalf("aggregate canonical signature: %v", err)
	}
	if err := BLSAggregateVerify([]kyber.Point{pub}, []byte(message), agg); err != nil {
		t.Fatalf("verify canonical aggregate: %v", err)
	}

	suffixed := append(append([]byte(nil), sig...), 0)
	if _, err := AggregateBLS([][]byte{suffixed}); err == nil || !strings.Contains(err.Error(), "length") {
		t.Fatalf("aggregate accepted suffixed member: %v", err)
	}
	if err := BLSAggregateVerify([]kyber.Point{pub}, []byte(message), append(append([]byte(nil), agg...), 0)); err == nil || !strings.Contains(err.Error(), "length") {
		t.Fatalf("aggregate verify accepted suffixed aggregate: %v", err)
	}
}

func TestBLSSignCacheKeyDoesNotRetainPrivateScalar(t *testing.T) {
	if _, err := BLSSignWithCache(nil, "invalid"); err == nil {
		t.Fatal("nil private key must be rejected")
	}
	cacheMutex.Lock()
	blsSignCache = make(map[string][]byte)
	cacheKeys = make([]string, 0, maxCacheSize)
	cacheMutex.Unlock()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}
	if _, err := BLSSignWithCache(priv, "cache-key-regression"); err != nil {
		t.Fatalf("sign: %v", err)
	}

	privateHex := hex.EncodeToString(priv.D.Bytes())
	cacheMutex.Lock()
	defer cacheMutex.Unlock()
	for key := range blsSignCache {
		if strings.Contains(key, privateHex) {
			t.Fatal("BLS cache key contains the private scalar")
		}
		if len(key) != sha256.Size*2 {
			t.Fatalf("cache key should be a fixed SHA-256 fingerprint, got length %d", len(key))
		}
	}
}
