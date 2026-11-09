package utils

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"golang.org/x/crypto/sha3"
)

var Port = ""

                                                                                                     
func DeriveEthereumAddress(privKey *secp256k1.PrivateKey) string {
	                                                                                   
	pubUncompressed := privKey.PubKey().SerializeUncompressed()

	             
	hash := sha3.NewLegacyKeccak256()
	                                                   
	hash.Write(pubUncompressed[1:])
	digest := hash.Sum(nil)            

	                             
	addr := digest[12:]                  
	return "0x" + hex.EncodeToString(addr)
}

                                                                                          
func DeriveDexAddress(privKey *secp256k1.PrivateKey) (string, error) {
	return AddressFromSecp256k1PubKey(privKey.PubKey().SerializeCompressed())
}

                                                                               
func DeriveBtcBech32Address(privKey *secp256k1.PrivateKey) (string, error) {
	return DeriveDexAddress(privKey)
}

                                                                                            
func DeriveBtcTaprootAddress(privKey *btcec.PrivateKey) (string, error) {
	curve := btcec.S256()

	                    
	P := privKey.PubKey()
	                                                              
	comp := P.SerializeCompressed()
	xOnly := comp[1:]

	                                                    
	tagHash := sha256.Sum256([]byte("TapTweak"))
	h := sha256.New()
	h.Write(tagHash[:])
	h.Write(tagHash[:])
	h.Write(xOnly)                                  
	tweakBytes := h.Sum(nil)

	                 
	n := curve.Params().N
	tweak := new(big.Int).SetBytes(tweakBytes)
	tweak.Mod(tweak, n)

	                                   
	tPx, tPy := curve.ScalarBaseMult(tweak.Bytes())
	Qx, _ := curve.Add(P.X(), P.Y(), tPx, tPy)

	                            
	xOnlyOut := make([]byte, 32)
	bx := Qx.Bytes()
	copy(xOnlyOut[32-len(bx):], bx)

	                                      
	addr, err := btcutil.NewAddressTaproot(xOnlyOut, &chaincfg.MainNetParams)
	if err != nil {
		return "", err
	}
	return addr.EncodeAddress(), nil
}

                                                                                                           
func ParseSecp256k1PrivateKey(keyStr string) (*secp256k1.PrivateKey, error) {
	                           
	if wif, err := btcutil.DecodeWIF(keyStr); err == nil {
		                                                                
		return wif.PrivKey, nil
	}

	                                                   
	if strings.HasPrefix(keyStr, "0x") || strings.HasPrefix(keyStr, "0X") {
		keyStr = keyStr[2:]
	}
	raw, err := hex.DecodeString(keyStr)
	defer clearKeyBytes(raw)                                                           
	if err != nil {
		return nil, errors.New("invalid key (neither valid WIF nor valid hex): " + err.Error())
	}
	if len(raw) != 32 {
		return nil, errors.New("invalid private key length in hex (must be 32 bytes)")
	}

	                                  
	privKey := secp256k1.PrivKeyFromBytes(raw)
	return privKey, nil
}

                                                                                     
func ParseECDSAPrivateKey(priKey string) (*ecdsa.PrivateKey, error) {
	secpPriv, err := ParseSecp256k1PrivateKey(priKey)
	if err != nil {
		return nil, err
	}
	defer secpPriv.Zero()
	return secpPriv.ToECDSA(), nil
}

                                                                             
func GeneratePairKey(baseToken, quoteToken string) string {
	return baseToken + "_" + quoteToken
}

                                                           
func NowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

                                                   
func NowUnixNano() int64 {
	return time.Now().UnixNano()
}

                                                           
                                                                                 
func ComputeUserTweak(groupPubkeyXOnly []byte, userAddress string) []byte {
	tagHash := sha256.Sum256([]byte("TapTweak"))
	h := sha256.New()
	h.Write(tagHash[:])
	h.Write(tagHash[:])
	h.Write(groupPubkeyXOnly)
	h.Write([]byte(userAddress))
	tweakBytes := h.Sum(nil)

	        
	n := btcec.S256().Params().N
	t := new(big.Int).SetBytes(tweakBytes)
	t.Mod(t, n)
	out := make([]byte, 32)
	t.FillBytes(out)
	return out
}

                                                                                  
func ComputeTweakedPubkey(groupPubkey []byte, tweak []byte) ([]byte, error) {
	                                                                                   
	var xOnly []byte
	switch len(groupPubkey) {
	case 33:
		if groupPubkey[0] != 0x02 && groupPubkey[0] != 0x03 {
			return nil, errors.New("invalid compressed pubkey prefix")
		}
		xOnly = groupPubkey[1:33]
	case 32:
		xOnly = groupPubkey
	default:
		return nil, errors.New("invalid pubkey length")
	}
	compressed := make([]byte, 33)
	compressed[0] = 0x02
	copy(compressed[1:], xOnly)
	pub, err := btcec.ParsePubKey(compressed)
	if err != nil {
		return nil, err
	}

	           
	var tweakScalar btcec.ModNScalar
	tweakScalar.SetByteSlice(tweak)
	var tG, pJ, result btcec.JacobianPoint
	btcec.ScalarBaseMultNonConst(&tweakScalar, &tG)
	pub.AsJacobian(&pJ)
	btcec.AddNonConst(&pJ, &tG, &result)
	result.ToAffine()
	tweaked := btcec.NewPublicKey(&result.X, &result.Y)
	return tweaked.SerializeCompressed(), nil
}
