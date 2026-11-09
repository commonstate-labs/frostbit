package txverify

import (
	"crypto/sha256"
	"dex/interfaces"
	"dex/pb"
	"dex/utils"
	"fmt"
	"strings"
)

                                                                                                   
func SignWithPrivateKey(tx *pb.AnyTx, privateKey string) (string, error) {
	privateKey = strings.TrimSpace(privateKey)
	if privateKey == "" {
		return "", fmt.Errorf("empty private key")
	}
	km := utils.NewKeyManager()
	if err := km.InitKey(privateKey); err != nil {
		return "", err
	}
	return SignWithKeyManager(tx, km)
}

                                                                                                   
func SignWithKeyManager(tx *pb.AnyTx, km *utils.KeyManager) (string, error) {
	if !km.HasPrivateKey() {
		return "", fmt.Errorf("missing private key")
	}
	return SignWithSigner(tx, km)
}

                                                                                                      
func SignWithSigner(tx *pb.AnyTx, signer interfaces.NodeSigner) (string, error) {
	if tx == nil {
		return "", fmt.Errorf("nil transaction")
	}
	base := tx.GetBase()
	if base == nil {
		return "", fmt.Errorf("missing base message")
	}
	if signer == nil {
		return "", fmt.Errorf("missing private key")
	}
	pubBytes := signer.PublicKeyBytes()
	if len(pubBytes) == 0 {
		return "", fmt.Errorf("missing secp256k1 public key")
	}
	if base.PublicKeys == nil {
		base.PublicKeys = &pb.PublicKeys{Keys: make(map[int32][]byte)}
	}
	if base.PublicKeys.Keys == nil {
		base.PublicKeys.Keys = make(map[int32][]byte)
	}
	base.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)] = append([]byte(nil), pubBytes...)

	txID := tx.GenerateTxID()
	base.TxId = txID
	digest := sha256.Sum256([]byte(txID))
	sig, err := signer.Sign(digest[:])
	if err != nil {
		return "", err
	}
	base.Signature = sig
	return txID, nil
}
