package frost

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"dex/frost/core/curve"
	"dex/frost/core/dkg"
	"dex/pb"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

func Test_CompareWithBtcecSchnorr(t *testing.T) {
	grp := curve.NewSecp256k1Group()
	                               
	privScalar := dkg.RandomScalar(grp.Order())
	privKey, _ := btcec.PrivKeyFromBytes(privScalar.Bytes())

	                              
	msg := []byte("consistency check")
	                                                                        
	digest := sha256.Sum256(msg)

	Rx, Ry, z, _ := SchnorrSign(grp, privScalar, digest[:])

	sig, err := schnorr.Sign(privKey, digest[:])
	if err != nil {
		t.Fatalf("婢舵牠鍎?schnorr.Sign 婢惰精瑙? %v", err)
	}
	sigBytes := sig.Serialize()                    
	if len(sigBytes) != 64 {
		t.Fatalf("婢舵牠鍎寸粵鎯ф倳闂€鍨娑撳秴顕? got %d, want 64", len(sigBytes))
	}

	                               
	RxExt := new(big.Int).SetBytes(sigBytes[:32])
	sExt := new(big.Int).SetBytes(sigBytes[32:])

	                               
	t.Logf(`
=== 閸愬懘鍎?SchnorrSign ===
  R = (%s, %s)
  z = %s
=== 婢舵牠鍎?schnorr.Sign ===
  R.x = %s
  s   = %s
`, Rx.Text(16), Ry.Text(16), z.Text(16), RxExt.Text(16), sExt.Text(16))

	                              
	if Rx.Cmp(RxExt) != 0 {
		t.Errorf("R.x 娑撳秳绔撮懛杈剧窗閸愬懘鍎?%s閿涘苯顦婚柈?%s", Rx.Text(16), RxExt.Text(16))
	}
	if z.Cmp(sExt) != 0 {
		t.Errorf("s/z 娑撳秳绔撮懛杈剧窗閸愬懘鍎?%s閿涘苯顦婚柈?%s", z.Text(16), sExt.Text(16))
	}
}

                                                      
func TestBIP340Verify(t *testing.T) {
	grp := curve.NewSecp256k1Group()

	                                           
	privScalar := dkg.RandomScalar(grp.Order())
	privKey, _ := btcec.PrivKeyFromBytes(privScalar.Bytes())

	pubKey := privKey.PubKey()
	pubKeyBytes := schnorr.SerializePubKey(pubKey)
	if len(pubKeyBytes) != 32 {
		t.Fatalf("閸忣剟鎸滈梹鍨娑撳秴顕? got %d, want 32", len(pubKeyBytes))
	}

	msg := sha256.Sum256([]byte("test message for BIP-340"))

	                                      
	sig, err := schnorr.Sign(privKey, msg[:])
	if err != nil {
		t.Fatalf("缁涙儳鎮曟径杈Е: %v", err)
	}
	sigBytes := sig.Serialize()                    

	                                                         
	t.Run("ValidSignature", func(t *testing.T) {
		valid, err := VerifyBIP340(pubKeyBytes, msg[:], sigBytes)
		if err != nil {
			t.Fatalf("妤犲瞼顒锋潻鏂挎礀闁挎瑨顕? %v", err)
		}
		if !valid {
			t.Error("valid signature should verify")
		}
	})

	t.Run("WrongMessage", func(t *testing.T) {
		wrongMsg := sha256.Sum256([]byte("wrong message"))
		valid, err := VerifyBIP340(pubKeyBytes, wrongMsg[:], sigBytes)
		if err != nil {
			t.Fatalf("妤犲瞼顒锋潻鏂挎礀闁挎瑨顕? %v", err)
		}
		if valid {
			t.Error("modified message should fail verification")
		}
	})

	t.Run("WrongSignature", func(t *testing.T) {
		wrongSig := make([]byte, 64)
		copy(wrongSig, sigBytes)
		wrongSig[0] ^= 0x01
		valid, err := VerifyBIP340(pubKeyBytes, msg[:], wrongSig)
		if err != nil {
			                                                                                                    
			return
		}
		if valid {
			t.Error("modified signature should fail verification")
		}
	})

	t.Run("InvalidPubkeyLength", func(t *testing.T) {
		_, err := VerifyBIP340([]byte{1, 2, 3}, msg[:], sigBytes)
		if !errors.Is(err, ErrInvalidPublicKey) {
			t.Errorf("閺堢喐婀?ErrInvalidPublicKey, 瀵版鍩?%v", err)
		}
	})

	t.Run("InvalidMessageLength", func(t *testing.T) {
		_, err := VerifyBIP340(pubKeyBytes, []byte("short"), sigBytes)
		if !errors.Is(err, ErrInvalidMessage) {
			t.Errorf("閺堢喐婀?ErrInvalidMessage, 瀵版鍩?%v", err)
		}
	})

	t.Run("InvalidSignatureLength", func(t *testing.T) {
		_, err := VerifyBIP340(pubKeyBytes, msg[:], []byte{1, 2, 3})
		if !errors.Is(err, ErrInvalidSignature) {
			t.Errorf("閺堢喐婀?ErrInvalidSignature, 瀵版鍩?%v", err)
		}
	})
}

func TestBIP340VerifyLeadingZeroChallengeInputs(t *testing.T) {
	found := false

	for i := int64(1); i < 20000; i++ {
		privBytes := make([]byte, 32)
		big.NewInt(i).FillBytes(privBytes)
		privKey, _ := btcec.PrivKeyFromBytes(privBytes)

		pubKeyBytes := schnorr.SerializePubKey(privKey.PubKey())
		msg := sha256.Sum256([]byte(fmt.Sprintf("leading-zero-case-%d", i)))

		sig, err := schnorr.Sign(privKey, msg[:])
		if err != nil {
			t.Fatalf("sign failed: %v", err)
		}
		sigBytes := sig.Serialize()

		                                                                
		                                                                                  
		if pubKeyBytes[0] != 0x00 && sigBytes[0] != 0x00 {
			continue
		}

		found = true
		valid, err := VerifyBIP340(pubKeyBytes, msg[:], sigBytes)
		if err != nil {
			t.Fatalf("verify returned error for leading-zero case (i=%d): %v", i, err)
		}
		if !valid {
			t.Fatalf("verify failed for leading-zero case (i=%d, pub0=%02x, sig0=%02x)", i, pubKeyBytes[0], sigBytes[0])
		}
		break
	}

	if !found {
		t.Fatal("failed to find a leading-zero pubkey/signature case within 20000 attempts")
	}
}

                                                          
func TestVerifyAPI(t *testing.T) {
	grp := curve.NewSecp256k1Group()

	                                           
	privScalar := dkg.RandomScalar(grp.Order())
	privKey, _ := btcec.PrivKeyFromBytes(privScalar.Bytes())

	pubKey := privKey.PubKey()
	pubKeyBytes := schnorr.SerializePubKey(pubKey)

	msg := sha256.Sum256([]byte("test message for Verify API"))

	                                      
	sig, err := schnorr.Sign(privKey, msg[:])
	if err != nil {
		t.Fatalf("缁涙儳鎮曟径杈Е: %v", err)
	}
	sigBytes := sig.Serialize()

	                                    
	t.Run("BIP340", func(t *testing.T) {
		valid, err := Verify(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, pubKeyBytes, msg[:], sigBytes)
		if err != nil {
			t.Fatalf("妤犲瞼顒锋潻鏂挎礀闁挎瑨顕? %v", err)
		}
		if !valid {
			t.Error("valid signature should verify")
		}
	})

	                                                         
	t.Run("UnsupportedAlgo", func(t *testing.T) {
		_, err := Verify(pb.SignAlgo_SIGN_ALGO_UNSPECIFIED, pubKeyBytes, msg[:], sigBytes)
		if err != ErrUnsupportedSignAlgo {
			t.Errorf("閺堢喐婀?ErrUnsupportedSignAlgo, 瀵版鍩?%v", err)
		}
	})
}

                                                           
func TestVerifyBN128(t *testing.T) {
	grp := &curve.BN256Group{}

	                                           
	privKey := dkg.RandomScalar(grp.Order())

	                              
	pubPoint := grp.ScalarBaseMult(privKey)
	pubKey := make([]byte, 64)
	copy(pubKey[:32], pad32(pubPoint.X))
	copy(pubKey[32:], pad32(pubPoint.Y))

	                
	msg := []byte("test message for BN128 Schnorr verification")

	k := dkg.RandomScalar(grp.Order())
	R := grp.ScalarBaseMult(k)

	                                                                                                                            
	e := bn128Challenge(R.X, R.Y, pubPoint.X, pubPoint.Y, msg, grp)

	                            
	s := new(big.Int).Mul(e, privKey)
	s.Add(s, k)
	s.Mod(s, grp.Order())

	                                                        
	sig := make([]byte, 64)
	copy(sig[:32], pad32(R.X))
	copy(sig[32:], pad32(s))

	t.Run("ValidSignature", func(t *testing.T) {
		valid, err := VerifyBN128(pubKey, msg, sig)
		if err != nil {
			t.Fatalf("妤犲瞼顒锋潻鏂挎礀闁挎瑨顕? %v", err)
		}
		if !valid {
			t.Error("valid signature should verify")
		}
	})

	t.Run("WrongMessage", func(t *testing.T) {
		wrongMsg := []byte("wrong message")
		valid, err := VerifyBN128(pubKey, wrongMsg, sig)
		if err != nil {
			t.Fatalf("妤犲瞼顒锋潻鏂挎礀闁挎瑨顕? %v", err)
		}
		if valid {
			t.Error("wrong message should fail verification")
		}
	})

	t.Run("InvalidPubkeyLength", func(t *testing.T) {
		_, err := VerifyBN128([]byte{0x01, 0x02}, msg, sig)
		if err != ErrInvalidPublicKey {
			t.Errorf("閺堢喐婀?ErrInvalidPublicKey, 瀵版鍩?%v", err)
		}
	})

	t.Run("InvalidSignatureLength", func(t *testing.T) {
		_, err := VerifyBN128(pubKey, msg, []byte{0x01, 0x02})
		if err != ErrInvalidSignature {
			t.Errorf("閺堢喐婀?ErrInvalidSignature, 瀵版鍩?%v", err)
		}
	})

	                                         
	t.Run("ViaVerifyAPI", func(t *testing.T) {
		valid, err := Verify(pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128, pubKey, msg, sig)
		if err != nil {
			t.Fatalf("妤犲瞼顒锋潻鏂挎礀闁挎瑨顕? %v", err)
		}
		if !valid {
			t.Error("valid signature should verify")
		}
	})
}

                                                  
func pad32(x *big.Int) []byte {
	out := make([]byte, 32)
	b := x.Bytes()
	copy(out[32-len(b):], b)
	return out
}
