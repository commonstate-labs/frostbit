package ceremony

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"dex/pb"

	"github.com/btcsuite/btcd/btcec/v2"
	bn256 "github.com/ethereum/go-ethereum/crypto/bn256/cloudflare"
)

func testShareMetadata() ShareMetadata {
	return ShareMetadata{
		CeremonyDigest: strings.Repeat("a", 64), NodeID: "node-1", Chain: "base",
		VaultID: 0, Epoch: 1, SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		CommitteeDigest:   strings.Repeat("b", 64),
		GroupPubkey:       new(bn256.G1).ScalarBaseMult(big.NewInt(1)).Marshal(),
		VerificationShare: new(bn256.G1).ScalarBaseMult(big.NewInt(7)).Marshal(),
	}
}

func testSealedShare(t *testing.T) ([]byte, ShareMetadata, []byte) {
	t.Helper()
	metadata := testShareMetadata()
	wrapKey := bytes.Repeat([]byte{0x42}, 32)
	sealed, err := SealShare(metadata, []byte{7}, wrapKey)
	if err != nil {
		t.Fatalf("seal fixture: %v", err)
	}
	return sealed, metadata, wrapKey
}

func TestSealedShareRoundTripAndRandomness(t *testing.T) {
	for _, chain := range []string{"eth", "bnb", "base", "arb", "trx", "btc"} {
		t.Run(chain, func(t *testing.T) {
			metadata := testShareMetadata()
			metadata.Chain = chain
			if chain == "btc" {
				metadata.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
				_, groupPubkey := btcec.PrivKeyFromBytes([]byte{1})
				_, verificationShare := btcec.PrivKeyFromBytes([]byte{7})
				metadata.GroupPubkey = groupPubkey.SerializeCompressed()
				metadata.VerificationShare = verificationShare.SerializeCompressed()
			}
			key := bytes.Repeat([]byte{0x62}, 32)
			secret := []byte{7}
			beforePubkey := bytes.Clone(metadata.GroupPubkey)
			first, err := SealShare(metadata, secret, key)
			if err != nil {
				t.Fatalf("first seal: %v", err)
			}
			second, err := SealShare(metadata, secret, key)
			if err != nil {
				t.Fatalf("second seal: %v", err)
			}
			if bytes.Equal(first, second) {
				t.Fatal("independent seals must use fresh nonces")
			}
			opened, err := OpenShare(append(first, '\n', ' '), metadata, key)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer clear(opened)
			want := make([]byte, 32)
			want[31] = 7
			if !bytes.Equal(opened, want) {
				t.Fatal("opened scalar does not match the padded input")
			}
			if !bytes.Equal(key, bytes.Repeat([]byte{0x62}, 32)) || !bytes.Equal(secret, []byte{7}) || !bytes.Equal(metadata.GroupPubkey, beforePubkey) {
				t.Fatal("seal/open changed caller-owned inputs")
			}
		})
	}
}

func TestSealedShareWrongKeyAndCiphertext(t *testing.T) {
	sealed, expected, key := testSealedShare(t)
	wrongKey := bytes.Clone(key)
	wrongKey[0] ^= 1
	if plain, err := OpenShare(sealed, expected, wrongKey); err == nil || plain != nil {
		t.Fatal("wrong wrap key must not return plaintext")
	}
	for _, field := range []string{"nonce", "ciphertext"} {
		t.Run(field, func(t *testing.T) {
			var envelope sealedShareEnvelope
			if err := json.Unmarshal(sealed, &envelope); err != nil {
				t.Fatal(err)
			}
			if field == "nonce" {
				envelope.Nonce[0] ^= 1
			} else {
				envelope.Ciphertext[0] ^= 1
			}
			mutated, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			if plain, err := OpenShare(mutated, expected, key); err == nil || plain != nil {
				t.Fatal("modified envelope must not return plaintext")
			}
		})
	}
}

func TestSealedShareAuthenticatesEveryMetadataField(t *testing.T) {
	sealed, original, key := testSealedShare(t)
	mutators := map[string]func(*ShareMetadata){
		"ceremony":  func(m *ShareMetadata) { m.CeremonyDigest = strings.Repeat("c", 64) },
		"node":      func(m *ShareMetadata) { m.NodeID = "node-2" },
		"chain":     func(m *ShareMetadata) { m.Chain = "eth" },
		"vault":     func(m *ShareMetadata) { m.VaultID++ },
		"epoch":     func(m *ShareMetadata) { m.Epoch++ },
		"committee": func(m *ShareMetadata) { m.CommitteeDigest = strings.Repeat("d", 64) },
		"pubkey":    func(m *ShareMetadata) { m.GroupPubkey = new(bn256.G1).ScalarBaseMult(big.NewInt(2)).Marshal() },
		"verification share": func(m *ShareMetadata) {
			m.VerificationShare = new(bn256.G1).ScalarBaseMult(big.NewInt(8)).Marshal()
		},
		"algorithm": func(m *ShareMetadata) {
			m.Chain = "btc"
			m.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
			_, groupPubkey := btcec.PrivKeyFromBytes([]byte{1})
			_, verificationShare := btcec.PrivKeyFromBytes([]byte{7})
			m.GroupPubkey = groupPubkey.SerializeCompressed()
			m.VerificationShare = verificationShare.SerializeCompressed()
		},
	}
	for name, mutate := range mutators {
		t.Run(name, func(t *testing.T) {
			expected := copyShareMetadata(original)
			mutate(&expected)
			if plain, err := OpenShare(sealed, expected, key); err == nil || plain != nil {
				t.Fatal("different trusted metadata must reject the artifact")
			}
			var envelope sealedShareEnvelope
			if err := json.Unmarshal(sealed, &envelope); err != nil {
				t.Fatal(err)
			}
			envelope.Metadata = expected
			mutated, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			                                                                    
			                                                              
			if plain, err := OpenShare(mutated, expected, key); err == nil || plain != nil {
				t.Fatal("modified AAD must fail authentication")
			}
		})
	}
}

func TestSealedShareStrictJSON(t *testing.T) {
	sealed, metadata, key := testSealedShare(t)
	text := string(sealed)
	cases := map[string]string{
		"duplicate top field":      strings.Replace(text, `"version":1`, `"version":1,"version":1`, 1),
		"duplicate metadata field": strings.Replace(text, `"node_id":"node-1"`, `"node_id":"node-1","node_id":"node-1"`, 1),
		"escaped duplicate":        strings.Replace(text, `"node_id":"node-1"`, `"node_id":"node-1","\u006eode_id":"node-1"`, 1),
		"unknown top field":        strings.Replace(text, `"version":1`, `"unknown":1,"version":1`, 1),
		"unknown metadata field":   strings.Replace(text, `"node_id":"node-1"`, `"node_id":"node-1","unknown":1`, 1),
		"case alias":               strings.Replace(text, `"version":1`, `"Version":1`, 1),
		"metadata case alias":      strings.Replace(text, `"node_id":`, `"Node_ID":`, 1),
		"missing top field":        strings.Replace(text, `"version":1,`, "", 1),
		"missing metadata field":   strings.Replace(text, `"vault_id":0,`, "", 1),
		"null zero-value field":    strings.Replace(text, `"vault_id":0`, `"vault_id":null`, 1),
		"unsupported version":      strings.Replace(text, `"version":1`, `"version":2`, 1),
		"unsupported cipher":       strings.Replace(text, `"AES-256-GCM"`, `"AES-128-GCM"`, 1),
		"fractional epoch":         strings.Replace(text, `"epoch":1`, `"epoch":1.5`, 1),
		"epoch overflow":           strings.Replace(text, `"epoch":1`, `"epoch":18446744073709551616`, 1),
		"trailing object":          text + `{}`,
		"trailing scalar":          text + ` true`,
		"trailing garbage":         text + `garbage`,
		"truncated object":         text[:len(text)-1],
		"array":                    `[]`,
		"null":                     `null`,
		"empty":                    "",
		"oversized":                strings.Repeat(" ", maxSealedShareSize+1),
	}
	for name, malformed := range cases {
		t.Run(name, func(t *testing.T) {
			if malformed == text {
				t.Fatal("test mutation failed to change fixture")
			}
			if plain, err := OpenShare([]byte(malformed), metadata, key); err == nil || plain != nil {
				t.Fatal("malformed JSON must not return plaintext")
			}
		})
	}
}

func TestSealedShareStrictBinaryLengths(t *testing.T) {
	sealed, metadata, key := testSealedShare(t)
	for _, mutation := range []string{"short nonce", "long nonce", "short ciphertext", "long ciphertext", "empty ciphertext"} {
		t.Run(mutation, func(t *testing.T) {
			var envelope sealedShareEnvelope
			if err := json.Unmarshal(sealed, &envelope); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "short nonce":
				envelope.Nonce = envelope.Nonce[:11]
			case "long nonce":
				envelope.Nonce = append(envelope.Nonce, 1)
			case "short ciphertext":
				envelope.Ciphertext = envelope.Ciphertext[:47]
			case "long ciphertext":
				envelope.Ciphertext = append(envelope.Ciphertext, 1)
			case "empty ciphertext":
				envelope.Ciphertext = []byte{}
			}
			mutated, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			if plain, err := OpenShare(mutated, metadata, key); err == nil || plain != nil {
				t.Fatal("invalid binary length must not return plaintext")
			}
		})
	}
	for _, length := range []int{0, 16, 31, 33} {
		if _, err := SealShare(metadata, []byte{1}, make([]byte, length)); err == nil {
			t.Errorf("seal accepted wrap key length %d", length)
		}
		if plain, err := OpenShare(sealed, metadata, make([]byte, length)); err == nil || plain != nil {
			t.Errorf("open accepted wrap key length %d", length)
		}
	}
}

func TestSealedShareRejectsNoncanonicalBase64(t *testing.T) {
	sealed, metadata, key := testSealedShare(t)
	canonical := base64.StdEncoding.EncodeToString(metadata.GroupPubkey)
	if !strings.HasSuffix(canonical, "Ag==") {
		t.Fatal("unexpected fixture encoding")
	}
	                                                                        
	                                                                           
	noncanonical := canonical[:len(canonical)-4] + "Ah=="
	if decoded, err := base64.StdEncoding.DecodeString(noncanonical); err != nil || !bytes.Equal(decoded, metadata.GroupPubkey) {
		t.Fatal("test mutation must decode to the original public key")
	}
	mutated := strings.Replace(string(sealed), canonical, noncanonical, 1)
	if plain, err := OpenShare([]byte(mutated), metadata, key); err == nil || plain != nil {
		t.Fatal("noncanonical base64 must be rejected")
	}
}

func TestSealedShareInvalidMetadata(t *testing.T) {
	mutators := map[string]func(*ShareMetadata){
		"empty ceremony digest":  func(m *ShareMetadata) { m.CeremonyDigest = "" },
		"uppercase digest":       func(m *ShareMetadata) { m.CeremonyDigest = strings.Repeat("A", 64) },
		"invalid digest":         func(m *ShareMetadata) { m.CommitteeDigest = strings.Repeat("z", 64) },
		"empty node":             func(m *ShareMetadata) { m.NodeID = "" },
		"oversized node":         func(m *ShareMetadata) { m.NodeID = strings.Repeat("x", 257) },
		"node whitespace":        func(m *ShareMetadata) { m.NodeID = "node 1" },
		"node control":           func(m *ShareMetadata) { m.NodeID = "node\x00" },
		"zero epoch":             func(m *ShareMetadata) { m.Epoch = 0 },
		"unsupported chain":      func(m *ShareMetadata) { m.Chain = "BASE" },
		"wrong algo":             func(m *ShareMetadata) { m.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340 },
		"unknown algo":           func(m *ShareMetadata) { m.SignAlgo = pb.SignAlgo(999) },
		"infinity":               func(m *ShareMetadata) { m.GroupPubkey = make([]byte, 64) },
		"wrong pubkey length":    func(m *ShareMetadata) { m.GroupPubkey = m.GroupPubkey[:63] },
		"point off curve":        func(m *ShareMetadata) { m.GroupPubkey[63] = 3 },
		"missing verification":   func(m *ShareMetadata) { m.VerificationShare = nil },
		"bad verification point": func(m *ShareMetadata) { m.VerificationShare = make([]byte, 64) },
		"btc wrong algo":         func(m *ShareMetadata) { m.Chain = "btc" },
		"btc invalid point": func(m *ShareMetadata) {
			m.Chain = "btc"
			m.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
			m.GroupPubkey = bytes.Repeat([]byte{0xff}, 32)
		},
	}
	for name, mutate := range mutators {
		t.Run(name, func(t *testing.T) {
			metadata := testShareMetadata()
			mutate(&metadata)
			if _, err := SealShare(metadata, []byte{7}, make([]byte, 32)); err == nil {
				t.Fatal("invalid metadata must not be sealed")
			}
		})
	}
}

func TestSealedShareScalarValidation(t *testing.T) {
	metadata := testShareMetadata()
	key := bytes.Repeat([]byte{0x24}, 32)
	for _, scalar := range [][]byte{nil, make([]byte, 32), make([]byte, 33), bn256.Order.Bytes(), bytes.Repeat([]byte{0xff}, 32)} {
		if _, err := SealShare(metadata, scalar, key); err == nil {
			t.Fatal("invalid scalar was accepted")
		}
	}
	maxValid := new(big.Int).Sub(new(big.Int).Set(bn256.Order), big.NewInt(1)).Bytes()
	metadata.VerificationShare = new(bn256.G1).ScalarBaseMult(new(big.Int).SetBytes(maxValid)).Marshal()
	if _, err := SealShare(metadata, maxValid, key); err != nil {
		t.Fatalf("maximum valid BN128 scalar was rejected: %v", err)
	}
	                                                                           
	                                                              
	gcm, err := newSealedShareAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	aad, err := shareMetadataAAD(metadata)
	if err != nil {
		t.Fatal(err)
	}
	nonce := bytes.Repeat([]byte{0x33}, gcm.NonceSize())
	envelope := sealedShareEnvelope{
		Version: sealedShareVersion, Cipher: sealedShareCipher, Metadata: metadata,
		Nonce: nonce, Ciphertext: gcm.Seal(nil, nonce, make([]byte, 32), aad),
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := OpenShare(encoded, metadata, key); err == nil || plain != nil {
		t.Fatal("authenticated zero scalar must not be returned")
	}
}

func TestOpenShareRejectsAuthenticatedWrongValidScalar(t *testing.T) {
	metadata := testShareMetadata()                                          
	key := bytes.Repeat([]byte{0x28}, 32)
	gcm, err := newSealedShareAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	aad, err := shareMetadataAAD(metadata)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(aad)
	wrong := make([]byte, 32)
	wrong[len(wrong)-1] = 8                                                
	defer clear(wrong)
	nonce := bytes.Repeat([]byte{0x39}, gcm.NonceSize())
	envelope := sealedShareEnvelope{
		Version: sealedShareVersion, Cipher: sealedShareCipher, Metadata: metadata,
		Nonce: nonce, Ciphertext: gcm.Seal(nil, nonce, wrong, aad),
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := OpenShare(encoded, metadata, key); err == nil || plain != nil {
		t.Fatal("authenticated valid scalar that disagrees with verification share was accepted")
	}
}

func TestSealedShareMetadataCopyDoesNotAlias(t *testing.T) {
	metadata := testShareMetadata()
	cloned := copyShareMetadata(metadata)
	cloned.GroupPubkey[0] ^= 1
	if bytes.Equal(cloned.GroupPubkey, metadata.GroupPubkey) {
		t.Fatal("metadata copy aliases the original public key slice")
	}
	cloned.VerificationShare[0] ^= 1
	if bytes.Equal(cloned.VerificationShare, metadata.VerificationShare) {
		t.Fatal("metadata copy aliases the original verification-share slice")
	}
}

func TestSealedShareBTCXOnlyKey(t *testing.T) {
	metadata := testShareMetadata()
	metadata.Chain = "btc"
	metadata.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
	_, pubkey := btcec.PrivKeyFromBytes([]byte{1})
	metadata.GroupPubkey = pubkey.SerializeCompressed()[1:]
	metadata.VerificationShare = pubkey.SerializeCompressed()
	key := make([]byte, 32)
	sealed, err := SealShare(metadata, []byte{1}, key)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := OpenShare(sealed, metadata, key)
	if err != nil {
		t.Fatal(err)
	}
	clear(plain)
}
