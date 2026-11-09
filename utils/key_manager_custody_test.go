package utils

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"dex/frost/security"
	"dex/pb"
	"dex/types"
	"encoding/hex"
	"fmt"
	"math/big"
	"sync"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/stretchr/testify/require"
	"go.dedis.ch/kyber/v3/group/mod"
	"go.dedis.ch/kyber/v3/pairing/bn256"
)

                                                                              
func custodyTestKey(t testing.TB, value byte) (*KeyManager, *ecdsa.PrivateKey) {
	t.Helper()
	var raw [32]byte
	raw[31] = value
	km := NewKeyManager()
	require.NoError(t, km.InitKeyBytes(raw[:]))
	legacy, _ := btcec.PrivKeyFromBytes(raw[:])
	defer legacy.Zero()
	return km, legacy.ToECDSA()
}

func TestKeyManagerIdentityAndCryptoCompatibility(t *testing.T) {
	for _, value := range []byte{1, 17} {
		t.Run(fmt.Sprintf("leading-zero-scalar-%d", value), func(t *testing.T) {
			km, legacy := custodyTestKey(t, value)
			defer clearKeyInt(legacy.D)
			legacyPublic := make([]byte, 33)
			legacyPublic[0] = 2 + byte(legacy.Y.Bit(0))
			legacy.X.FillBytes(legacyPublic[1:])
			wantAddress, err := AddressFromSecp256k1PubKey(legacyPublic)
			require.NoError(t, err)
			require.Equal(t, legacyPublic, km.PublicKeyBytes())
			require.Equal(t, wantAddress, km.GetAddress())
			require.Equal(t, hex.EncodeToString(legacyPublic), km.GetPublicKey())

			digest := sha256.Sum256([]byte("custody compatibility"))
			sig, err := km.Sign(digest[:])
			require.NoError(t, err)
			require.Len(t, sig, 64)
			require.True(t, VerifyECDSASignature(legacyPublic, digest[:], sig))
			digest[0] ^= 1
			require.False(t, VerifyECDSASignature(legacyPublic, digest[:], sig))

			legacyBLS, err := GetPublicKey(legacy)
			require.NoError(t, err)
			wantBLS, err := SerializePublicKey(legacyBLS)
			require.NoError(t, err)
			gotBLS, err := km.BLSPublicKeyBytes()
			require.NoError(t, err)
			require.Equal(t, wantBLS, gotBLS)
			for _, message := range [][]byte{nil, []byte("miner-registration-proof"), {0, 1, 255}} {
				want, err := BLSSignWithCache(legacy, string(message))
				require.NoError(t, err)
				got, err := km.SignBLS(message)
				require.NoError(t, err)
				require.Equal(t, want, got)
				require.NoError(t, BLSVerifySignature(legacyBLS, string(message), got))
			}

			for _, height := range []uint64{0, 20, 5000} {
				wantOutput, wantProof, err := NewVRFProvider().GenerateVRF(legacy, height, 3, "parent-block", types.NodeID(wantAddress))
				require.NoError(t, err)
				output, proof, err := km.GenerateVRF(height, 3, "parent-block", types.NodeID(wantAddress))
				require.NoError(t, err)
				require.Equal(t, wantOutput, output)
				require.Equal(t, wantProof, proof)
				require.NoError(t, NewVRFProvider().VerifyVRFWithPublicKey(legacyBLS, height, 3, "parent-block", types.NodeID(wantAddress), proof, output))
				require.Error(t, NewVRFProvider().VerifyVRFWithPublicKey(legacyBLS, height+1, 3, "parent-block", types.NodeID(wantAddress), proof, output))
			}
		})
	}
}

func TestKeyManagerFrostEnvelopeAndECIESCompatibility(t *testing.T) {
	km, legacy := custodyTestKey(t, 1)
	defer clearKeyInt(legacy.D)
	env := &pb.FrostEnvelope{From: km.GetAddress(), To: "peer", Kind: 1, Payload: []byte{1, 2, 3}, JobId: "job", Seq: 7, Sig: []byte("stale")}
	require.NoError(t, km.SignFrostEnvelope(env))
	require.Len(t, env.Sig, 64)
	require.True(t, security.VerifyFrostEnvelope(&legacy.PublicKey, env))
	env.Seq++
	require.False(t, security.VerifyFrostEnvelope(&legacy.PublicKey, env))

	                                                                               
	ciphertext, err := hex.DecodeString("02c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5" +
		"8559c4b89ee0c278793f8faddf740d1227143513d542b7fec3ccf5cb2305e1d0f2" +
		"0d6bf0b21113e9c479b6b3dba75a006b633976c54072d853f73805864c1c08")
	require.NoError(t, err)
	plaintext, err := km.DecryptECIES(ciphertext)
	require.NoError(t, err)
	want := make([]byte, 32)
	for i := range want {
		want[i] = byte(i)
	}
	require.Equal(t, want, plaintext)
	tampered := bytes.Clone(ciphertext)
	tampered[len(tampered)-1] ^= 1
	_, err = km.DecryptECIES(tampered)
	require.ErrorIs(t, err, security.ErrMacVerificationFailed)
	_, err = km.DecryptECIES(ciphertext)
	require.NoError(t, err, "a rejected ciphertext must not change the identity")
}

func TestKeyManagerRejectsInvalidInputWithoutReplacingIdentity(t *testing.T) {
	km, legacy := custodyTestKey(t, 1)
	defer clearKeyInt(legacy.D)
	wantAddress := km.GetAddress()
	for _, raw := range [][]byte{nil, make([]byte, 31), make([]byte, 32), btcec.S256().Params().N.FillBytes(make([]byte, 32)), bytes.Repeat([]byte{255}, 32)} {
		require.Error(t, km.InitKeyBytes(raw))
		require.Equal(t, wantAddress, km.GetAddress())
	}
	require.Error(t, km.SignFrostEnvelope(nil))
	_, _, err := km.GenerateVRF(1, -1, "parent", "node")
	require.Error(t, err)
	for _, uninitialized := range []*KeyManager{nil, NewKeyManager()} {
		require.False(t, uninitialized.HasPrivateKey())
		require.Empty(t, uninitialized.PublicKeyBytes())
		_, err := uninitialized.Sign([]byte("digest"))
		require.Error(t, err)
		_, err = uninitialized.SignBLS(nil)
		require.Error(t, err)
		_, err = uninitialized.BLSPublicKeyBytes()
		require.Error(t, err)
		_, _, err = uninitialized.GenerateVRF(1, 0, "parent", "node")
		require.Error(t, err)
		require.Error(t, uninitialized.SignFrostEnvelope(&pb.FrostEnvelope{}))
		_, err = uninitialized.DecryptECIES(nil)
		require.Error(t, err)
	}
}

func TestKeyManagerOwnsBuffersAndClearsReplacedScalar(t *testing.T) {
	var raw [32]byte
	raw[31] = 1
	km := NewKeyManager()
	require.NoError(t, km.InitKeyBytes(raw[:]))
	require.Equal(t, byte(1), raw[31], "the input remains caller-owned")
	wantPublic := km.PublicKeyBytes()
	wantBLS, err := km.BLSPublicKeyBytes()
	require.NoError(t, err)
	public := km.PublicKeyBytes()
	public[0] ^= 1
	blsPublic, err := km.BLSPublicKeyBytes()
	require.NoError(t, err)
	blsPublic[0] ^= 1
	signature, err := km.SignBLS([]byte("cache ownership"))
	require.NoError(t, err)
	wantSignature := bytes.Clone(signature)
	signature[0] ^= 1
	signature, err = km.SignBLS([]byte("cache ownership"))
	require.NoError(t, err)
	require.Equal(t, wantSignature, signature)
	require.Equal(t, wantPublic, km.PublicKeyBytes())
	blsPublic, err = km.BLSPublicKeyBytes()
	require.NoError(t, err)
	require.Equal(t, wantBLS, blsPublic)

	oldScalar := km.privateKey.D                                                       
	raw[31] = 2
	require.NoError(t, km.InitKeyBytes(raw[:]))
	clear(raw[:])
	require.Zero(t, oldScalar.Sign())
	require.NotEqual(t, wantPublic, km.PublicKeyBytes())
	blsPublic, err = km.BLSPublicKeyBytes()
	require.NoError(t, err)
	require.NotEqual(t, wantBLS, blsPublic)
}

func TestKeyManagerConcurrentSigningUsesOneIdentity(t *testing.T) {
	km, legacy := custodyTestKey(t, 1)
	defer clearKeyInt(legacy.D)
	public := km.PublicKeyBytes()
	digest := sha256.Sum256([]byte("parallel custody"))
	wantBLS, err := km.SignBLS(digest[:])
	require.NoError(t, err)
	var workers sync.WaitGroup
	for worker := 0; worker < 20; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for iteration := 0; iteration < 5; iteration++ {
				signature, err := km.Sign(digest[:])
				if err != nil || !VerifyECDSASignature(public, digest[:], signature) {
					t.Error("parallel ECDSA signing changed the identity or failed")
					return
				}
				blsSignature, err := km.SignBLS(digest[:])
				if err != nil || !bytes.Equal(wantBLS, blsSignature) {
					t.Error("parallel BLS signing changed the identity or failed")
					return
				}
			}
		}()
	}
	workers.Wait()
}

func TestBLSPrivateScalarCleanupWipesBackingWords(t *testing.T) {
	_, legacy := custodyTestKey(t, 1)
	defer clearKeyInt(legacy.D)
	scalar, err := GetBLSPrivateKey(legacy)
	require.NoError(t, err)
	integer, ok := scalar.(*mod.Int)
	require.True(t, ok, "BN256 scalar representation changed; revisit secret cleanup")
	words := integer.V.Bits()
	words = words[:cap(words)]
	require.NotEmpty(t, words)
	require.Positive(t, integer.V.Sign())
	modulus := new(big.Int).Set(integer.M)
	clearBLSPrivateScalar(scalar)
	require.Zero(t, integer.V.Sign())
	for _, word := range words {
		require.Zero(t, word, "old big.Int backing memory still contains scalar words")
	}
	require.Zero(t, integer.M.Cmp(modulus), "cleanup must not alter the shared public modulus")
	require.Zero(t, bn256.Order.Cmp(modulus))
}

var benchmarkIdentitySignature []byte

func BenchmarkMinerIdentityECDSASign(b *testing.B) {
	km, legacy := custodyTestKey(b, 1)
	defer clearKeyInt(legacy.D)
	digest := sha256.Sum256([]byte("signature benchmark"))
	for _, batch := range []int{1, 19, 20, 21, 5000} {
		b.Run(fmt.Sprintf("legacy/batch-%d", batch), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for j := 0; j < batch; j++ {
					r, s, err := ecdsa.Sign(rand.Reader, legacy, digest[:])
					if err != nil {
						b.Fatal(err)
					}
					sig := make([]byte, 64)
					rBytes, sBytes := r.Bytes(), s.Bytes()
					copy(sig[32-len(rBytes):32], rBytes)
					copy(sig[64-len(sBytes):64], sBytes)
					benchmarkIdentitySignature = sig
				}
			}
		})
		b.Run(fmt.Sprintf("converged/batch-%d", batch), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for j := 0; j < batch; j++ {
					sig, err := km.Sign(digest[:])
					if err != nil {
						b.Fatal(err)
					}
					benchmarkIdentitySignature = sig
				}
			}
		})
	}
}

func BenchmarkMinerIdentityBLSPublicKey(b *testing.B) {
	km, legacy := custodyTestKey(b, 1)
	defer clearKeyInt(legacy.D)
	if _, err := km.BLSPublicKeyBytes(); err != nil {
		b.Fatal(err)
	}
	b.Run("legacy-derive-and-validate", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			point, err := GetPublicKey(legacy)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkIdentitySignature, err = SerializePublicKey(point)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("converged-public-cache", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var err error
			benchmarkIdentitySignature, err = km.BLSPublicKeyBytes()
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
