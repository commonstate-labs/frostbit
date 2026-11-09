package txverify

import (
	"crypto/sha256"
	"dex/pb"
	"dex/utils"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

                                                                                  
type testSigningCapability struct {
	public []byte
	sign   func([]byte) ([]byte, error)
}

func (s testSigningCapability) PublicKeyBytes() []byte { return s.public }
func (s testSigningCapability) Sign(digest []byte) ([]byte, error) {
	return s.sign(digest)
}

func TestSignWithSignerPreservesCanonicalTransaction(t *testing.T) {
	reference := buildSignedTransfer(t)
	tx := proto.Clone(reference).(*pb.AnyTx)
	tx.GetBase().PublicKeys = nil
	tx.GetBase().TxId = "stale"
	tx.GetBase().Signature = []byte("stale")
	km := utils.NewKeyManager()
	require.NoError(t, km.InitKey(utils.GenerateTestIdentity(1).PrivateKeyHex))
	var signedDigest []byte
	capability := testSigningCapability{public: km.PublicKeyBytes(), sign: func(digest []byte) ([]byte, error) {
		signedDigest = append([]byte(nil), digest...)
		return km.Sign(digest)
	}}
	txID, err := SignWithSigner(tx, capability)
	require.NoError(t, err)
	require.Equal(t, reference.GetBase().TxId, txID)
	wantDigest := sha256.Sum256([]byte(txID))
	require.Equal(t, wantDigest[:], signedDigest)
	require.NoError(t, Verify(tx, testChainID))
	capability.public[0] ^= 1
	require.NoError(t, Verify(tx, testChainID), "the transaction owns its public-key bytes")
}

func TestSignWithSignerRejectsUnavailableCapabilities(t *testing.T) {
	tx := buildSignedTransfer(t)
	_, err := SignWithSigner(nil, nil)
	require.ErrorContains(t, err, "nil transaction")
	_, err = SignWithSigner(&pb.AnyTx{}, nil)
	require.ErrorContains(t, err, "missing base")
	_, err = SignWithSigner(tx, nil)
	require.Error(t, err)
	_, err = SignWithSigner(tx, testSigningCapability{})
	require.ErrorContains(t, err, "public key")
	var nilKey *utils.KeyManager
	_, err = SignWithSigner(tx, nilKey)
	require.Error(t, err)
	_, err = SignWithKeyManager(tx, nilKey)
	require.Error(t, err)
	signFailure := errors.New("signer is locked")
	_, err = SignWithSigner(tx, testSigningCapability{public: []byte{2}, sign: func([]byte) ([]byte, error) { return nil, signFailure }})
	require.ErrorIs(t, err, signFailure)
}
