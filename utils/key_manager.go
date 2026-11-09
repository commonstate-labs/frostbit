package utils

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"dex/frost/security"
	"dex/logs"
	"dex/pb"
	"dex/types"
	"encoding/hex"
	"fmt"
	"math/big"
	"runtime"
	"strings"
	"sync"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/bech32"
)

const (
	DEXAddressHRP    = "fbc"
	DEXAddressPrefix = DEXAddressHRP + "1"
	DEXAddressLength = 43
)

                                                              
type KeyManager struct {
	mu           sync.RWMutex
	privateKey   *ecdsa.PrivateKey                                                                   
	address      string
	publicKey    []byte
	blsPublicKey []byte
}

                                 
var (
	keyManagerInstance *KeyManager
	keyManagerOnce     sync.Once
)

                                                                          
func GetKeyManager() *KeyManager {
	keyManagerOnce.Do(func() {
		keyManagerInstance = &KeyManager{}
	})
	return keyManagerInstance
}

                                                                                               
func NewKeyManager() *KeyManager {
	return &KeyManager{}
}

func (km *KeyManager) InitKey(priKey string) error {
	privb, err := ParseSecp256k1PrivateKey(priKey)
	if err != nil {
		return err
	}
	defer privb.Zero()
	raw := privb.Serialize()
	defer clearKeyBytes(raw)
	return km.InitKeyBytes(raw)
}

                                                                                                               
func (km *KeyManager) InitKeyBytes(raw []byte) error {
	if km == nil || len(raw) != 32 {
		return fmt.Errorf("private key must contain exactly 32 bytes")
	}
	d := new(big.Int).SetBytes(raw)
	if d.Sign() <= 0 || d.Cmp(btcec.S256().Params().N) >= 0 {
		clearKeyInt(d)
		return fmt.Errorf("private key scalar out of range")
	}
	x, y := btcec.S256().ScalarBaseMult(raw)
	key := &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: btcec.S256(), X: x, Y: y}, D: d}
	public := make([]byte, 33)
	public[0] = 2 + byte(y.Bit(0))
	x.FillBytes(public[1:])
	address, err := AddressFromSecp256k1PubKey(public)
	if err != nil {
		clearKeyInt(d)
		return err
	}
	km.mu.Lock()
	if km.privateKey != nil {
		clearKeyInt(km.privateKey.D)
	}
	km.privateKey, km.publicKey, km.blsPublicKey, km.address = key, public, nil, address
	km.mu.Unlock()
	logs.Debug("[KeyManager] InitKey success. Address=%s\n", address)
	return nil
}

                                                                         
func (km *KeyManager) InitKeyRandom() error {
	privb, err := btcec.NewPrivateKey()
	if err != nil {
		return err
	}
	defer privb.Zero()
	raw := privb.Serialize()
	defer clearKeyBytes(raw)
	return km.InitKeyBytes(raw)
}

func (km *KeyManager) HasPrivateKey() bool {
	if km == nil {
		return false
	}
	km.mu.RLock()
	defer km.mu.RUnlock()
	return km.privateKey != nil
}

                                                              
func (km *KeyManager) GetPublicKey() string {
	return hex.EncodeToString(km.PublicKeyBytes())
}

                                               
func (km *KeyManager) GetAddress() string {
	if km == nil {
		return ""
	}
	km.mu.RLock()
	defer km.mu.RUnlock()
	return km.address
}

                                               
                                                     
                                               

                                                                                       
func (km *KeyManager) Sign(digest []byte) ([]byte, error) {
	if km == nil {
		return nil, fmt.Errorf("private key is not initialized")
	}
	km.mu.RLock()
	defer km.mu.RUnlock()
	if km.privateKey == nil {
		return nil, fmt.Errorf("private key is not initialized")
	}
	r, s, err := ecdsa.Sign(rand.Reader, km.privateKey, digest)
	if err != nil {
		return nil, err
	}
	sig := make([]byte, 64)
	rBytes, sBytes := r.Bytes(), s.Bytes()
	copy(sig[32-len(rBytes):32], rBytes)
	copy(sig[64-len(sBytes):64], sBytes)
	return sig, nil
}

                                                                    
func (km *KeyManager) PublicKeyBytes() []byte {
	if km == nil {
		return nil
	}
	km.mu.RLock()
	defer km.mu.RUnlock()
	return append([]byte(nil), km.publicKey...)
}

func (km *KeyManager) BLSPublicKeyBytes() ([]byte, error) {
	if km == nil {
		return nil, fmt.Errorf("private key is not initialized")
	}
	km.mu.Lock()
	defer km.mu.Unlock()
	if km.privateKey == nil {
		return nil, fmt.Errorf("private key is not initialized")
	}
	if len(km.blsPublicKey) == 0 {
		point, err := GetPublicKey(km.privateKey)
		if err != nil {
			return nil, err
		}
		encoded, err := SerializePublicKey(point)
		if err != nil {
			return nil, err
		}
		km.blsPublicKey = encoded
	}
	return append([]byte(nil), km.blsPublicKey...), nil
}

func (km *KeyManager) SignBLS(message []byte) ([]byte, error) {
	if km == nil {
		return nil, fmt.Errorf("private key is not initialized")
	}
	km.mu.RLock()
	defer km.mu.RUnlock()
	if km.privateKey == nil {
		return nil, fmt.Errorf("private key is not initialized")
	}
	sig, err := BLSSignWithCache(km.privateKey, string(message))
	return append([]byte(nil), sig...), err
}

func (km *KeyManager) GenerateVRF(height uint64, window int, parent string, nodeID types.NodeID) ([]byte, []byte, error) {
	if km == nil {
		return nil, nil, fmt.Errorf("private key is not initialized")
	}
	km.mu.RLock()
	defer km.mu.RUnlock()
	return NewVRFProvider().GenerateVRF(km.privateKey, height, window, parent, nodeID)
}

func (km *KeyManager) SignFrostEnvelope(env *pb.FrostEnvelope) error {
	if km == nil {
		return fmt.Errorf("private key is not initialized")
	}
	km.mu.RLock()
	defer km.mu.RUnlock()
	if km.privateKey == nil {
		return fmt.Errorf("private key is not initialized")
	}
	if env == nil {
		return fmt.Errorf("missing frost envelope")
	}
	return security.SignFrostEnvelope(km.privateKey, env)
}

func (km *KeyManager) DecryptECIES(ciphertext []byte) ([]byte, error) {
	if km == nil {
		return nil, fmt.Errorf("private key is not initialized")
	}
	km.mu.RLock()
	defer km.mu.RUnlock()
	if km.privateKey == nil {
		return nil, fmt.Errorf("private key is not initialized")
	}
	var raw [32]byte
	km.privateKey.D.FillBytes(raw[:])
	defer clearKeyBytes(raw[:])
	return security.ECIESDecrypt(raw[:], ciphertext)
}

func clearKeyBytes(raw []byte) { clear(raw); runtime.KeepAlive(raw) }
func clearKeyInt(value *big.Int) {
	if value == nil {
		return
	}
	clear(value.Bits())
	value.SetInt64(0)
	runtime.KeepAlive(value)
}

                                                                                             
func VerifyECDSASignature(pubKeyBytes []byte, digest []byte, sig []byte) bool {
	if len(sig) != 64 {
		return false
	}
	pub, err := btcec.ParsePubKey(pubKeyBytes)
	if err != nil {
		return false
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	return ecdsa.Verify(pub.ToECDSA(), digest, r, s)
}

                                                                              
func AddressFromSecp256k1PubKey(pubKeyBytes []byte) (string, error) {
	pubKeyHash := btcutil.Hash160(pubKeyBytes)
	words, err := bech32.ConvertBits(pubKeyHash, 8, 5, true)
	if err != nil {
		return "", err
	}
	data := append([]byte{0}, words...)
	return bech32.Encode(DEXAddressHRP, data)
}

func IsDexAddress(address string) bool {
	if len(address) != DEXAddressLength || address != strings.ToLower(address) {
		return false
	}
	hrp, data, err := bech32.Decode(address)
	if err != nil || hrp != DEXAddressHRP || len(data) != 33 || data[0] != 0 {
		return false
	}
	payload, err := bech32.ConvertBits(data[1:], 5, 8, false)
	return err == nil && len(payload) == 20
}

                                                                       
func (km *KeyManager) SignMessage(message string) (string, error) {
	hash := sha256.Sum256([]byte(message))
	sig, err := km.Sign(hash[:])
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sig), nil
}
