package security

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateTestKeyPair(t *testing.T) (*btcec.PrivateKey, []byte) {
	privKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	pubKey := privKey.PubKey().SerializeCompressed()
	return privKey, pubKey
}

func randomBytes(t *testing.T, n int) []byte {
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return b
}

func TestECIESEncrypt_Basic(t *testing.T) {
	_, recipientPubKey := generateTestKeyPair(t)
	plaintext := randomBytes(t, 32)
	randomness := randomBytes(t, 32)

	ciphertext, err := ECIESEncrypt(recipientPubKey, plaintext, randomness)
	require.NoError(t, err)
	assert.Equal(t, 97, len(ciphertext))
}

func TestECIESEncrypt_Deterministic(t *testing.T) {
	_, recipientPubKey := generateTestKeyPair(t)
	plaintext := randomBytes(t, 32)
	randomness := randomBytes(t, 32)

	ciphertext1, err := ECIESEncrypt(recipientPubKey, plaintext, randomness)
	require.NoError(t, err)
	ciphertext2, err := ECIESEncrypt(recipientPubKey, plaintext, randomness)
	require.NoError(t, err)
	assert.Equal(t, ciphertext1, ciphertext2)
}

func TestECIESWireCompatibilityVector(t *testing.T) {
	recipientPrivateBytes := make([]byte, 32)
	recipientPrivateBytes[31] = 1
	recipientPrivate, _ := btcec.PrivKeyFromBytes(recipientPrivateBytes)
	defer recipientPrivate.Zero()
	randomness := make([]byte, 32)
	randomness[31] = 2
	plaintext := make([]byte, 32)
	for index := range plaintext {
		plaintext[index] = byte(index)
	}

	ciphertext, err := ECIESEncrypt(recipientPrivate.PubKey().SerializeCompressed(), plaintext, randomness)
	require.NoError(t, err)
	assert.Equal(t,
		"02c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5"+
			"8559c4b89ee0c278793f8faddf740d1227143513d542b7fec3ccf5cb2305e1d0f2"+
			"0d6bf0b21113e9c479b6b3dba75a006b633976c54072d853f73805864c1c08",
		hex.EncodeToString(ciphertext),
	)
}

func TestECIESRoundTripPreservesCallerSecrets(t *testing.T) {
	recipientPrivate, recipientPubKey := generateTestKeyPair(t)
	defer recipientPrivate.Zero()
	recipientPrivateBytes := recipientPrivate.Serialize()
	plaintext := randomBytes(t, 64)
	randomness := randomBytes(t, 32)
	                                                                      
	                                             
	ephemeralPrivate, _ := btcec.PrivKeyFromBytes(randomness)
	copy(randomness, ephemeralPrivate.Serialize())
	ephemeralPrivate.Zero()

	wantPrivate := bytes.Clone(recipientPrivateBytes)
	wantPlaintext := bytes.Clone(plaintext)
	wantRandomness := bytes.Clone(randomness)
	ciphertext, err := ECIESEncrypt(recipientPubKey, plaintext, randomness)
	require.NoError(t, err)
	decrypted, err := ECIESDecrypt(recipientPrivateBytes, ciphertext)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
	assert.Equal(t, wantPrivate, recipientPrivateBytes)
	assert.Equal(t, wantPlaintext, plaintext)
	assert.Equal(t, wantRandomness, randomness)
}

func TestECIESDecryptMACFailureDoesNotPoisonNextAttempt(t *testing.T) {
	recipientPrivate, recipientPubKey := generateTestKeyPair(t)
	defer recipientPrivate.Zero()
	privateBytes := recipientPrivate.Serialize()
	plaintext := randomBytes(t, 64)
	randomness := randomBytes(t, 32)

	ciphertext, err := ECIESEncrypt(recipientPubKey, plaintext, randomness)
	require.NoError(t, err)
	tampered := bytes.Clone(ciphertext)
	tampered[len(tampered)-1] ^= 1
	_, err = ECIESDecrypt(privateBytes, tampered)
	require.ErrorIs(t, err, ErrMacVerificationFailed)

	decrypted, err := ECIESDecrypt(privateBytes, ciphertext)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestECIESDerivedKeyBuffersCanBeCleared(t *testing.T) {
	recipientPrivate, _ := generateTestKeyPair(t)
	defer recipientPrivate.Zero()
	ephemeralPrivate, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	defer ephemeralPrivate.Zero()

	encKey, macKey, err := deriveECIESKeys(recipientPrivate.PubKey(), ephemeralPrivate)
	require.NoError(t, err)
	require.Len(t, encKey, 16)
	require.Len(t, macKey, 16)
	assert.False(t, allZeroECIES(encKey))
	assert.False(t, allZeroECIES(macKey))
	wipeECIESBytes(encKey)
	wipeECIESBytes(macKey)
	assert.True(t, allZeroECIES(encKey))
	assert.True(t, allZeroECIES(macKey))
}

func allZeroECIES(data []byte) bool {
	var combined byte
	for _, value := range data {
		combined |= value
	}
	return combined == 0
}

func TestECIESVerifyCiphertext_Valid(t *testing.T) {
	_, recipientPubKey := generateTestKeyPair(t)
	plaintext := randomBytes(t, 32)
	randomness := randomBytes(t, 32)

	ciphertext, err := ECIESEncrypt(recipientPubKey, plaintext, randomness)
	require.NoError(t, err)

	valid := ECIESVerifyCiphertext(recipientPubKey, plaintext, randomness, ciphertext)
	assert.True(t, valid)
}

func TestECIESVerifyCiphertext_WrongPlaintext(t *testing.T) {
	_, recipientPubKey := generateTestKeyPair(t)
	plaintext := randomBytes(t, 32)
	randomness := randomBytes(t, 32)

	ciphertext, err := ECIESEncrypt(recipientPubKey, plaintext, randomness)
	require.NoError(t, err)

	wrongPlaintext := randomBytes(t, 32)
	valid := ECIESVerifyCiphertext(recipientPubKey, wrongPlaintext, randomness, ciphertext)
	assert.False(t, valid)
}

func TestECIESVerifyCiphertext_WrongRandomness(t *testing.T) {
	_, recipientPubKey := generateTestKeyPair(t)
	plaintext := randomBytes(t, 32)
	randomness := randomBytes(t, 32)

	ciphertext, err := ECIESEncrypt(recipientPubKey, plaintext, randomness)
	require.NoError(t, err)

	wrongRandomness := randomBytes(t, 32)
	valid := ECIESVerifyCiphertext(recipientPubKey, plaintext, wrongRandomness, ciphertext)
	assert.False(t, valid)
}

func generateFeldmanVSSTestData(t *testing.T, threshold int, receiverIndex int) ([]byte, [][]byte) {
	curve := btcec.S256()
	n := curve.Params().N

	coefficients := make([]*big.Int, threshold)
	commitmentPoints := make([][]byte, threshold)

	for k := 0; k < threshold; k++ {
		                                 
		coeffBytes := randomBytes(t, 32)
		coefficients[k] = new(big.Int).SetBytes(coeffBytes)
		coefficients[k].Mod(coefficients[k], n)
		                       
		if coefficients[k].Sign() == 0 {
			coefficients[k].SetInt64(1)
		}

		                                                                               
		privKey, _ := btcec.PrivKeyFromBytes(coefficients[k].Bytes())
		commitmentPoints[k] = privKey.PubKey().SerializeCompressed()
	}

	x := big.NewInt(int64(receiverIndex))
	share := big.NewInt(0)
	xPower := big.NewInt(1)

	for k := 0; k < threshold; k++ {
		term := new(big.Int).Mul(coefficients[k], xPower)
		share.Add(share, term)
		share.Mod(share, n)
		xPower.Mul(xPower, x)
		xPower.Mod(xPower, n)
	}

	shareBytes := make([]byte, 32)
	shareBytesBig := share.Bytes()
	copy(shareBytes[32-len(shareBytesBig):], shareBytesBig)

	return shareBytes, commitmentPoints
}

func TestVerifyShareAgainstCommitment_Valid(t *testing.T) {
	threshold := 3
	receiverIndex := 5

	share, commitmentPoints := generateFeldmanVSSTestData(t, threshold, receiverIndex)

	valid := VerifyShareAgainstCommitment(share, commitmentPoints, big.NewInt(int64(receiverIndex)))
	assert.True(t, valid)
}

func TestVerifyShareAgainstCommitment_WrongShare(t *testing.T) {
	threshold := 3
	receiverIndex := 5

	_, commitmentPoints := generateFeldmanVSSTestData(t, threshold, receiverIndex)
	wrongShare := randomBytes(t, 32)

	valid := VerifyShareAgainstCommitment(wrongShare, commitmentPoints, big.NewInt(int64(receiverIndex)))
	assert.False(t, valid)
}

func TestVerifyShareAgainstCommitment_WrongIndex(t *testing.T) {
	threshold := 3
	receiverIndex := 5

	share, commitmentPoints := generateFeldmanVSSTestData(t, threshold, receiverIndex)

	wrongIndex := big.NewInt(int64(receiverIndex + 1))
	valid := VerifyShareAgainstCommitment(share, commitmentPoints, wrongIndex)
	assert.False(t, valid)
}
