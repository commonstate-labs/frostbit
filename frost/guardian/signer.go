package guardian

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"

	"dex/frost/chain"
	"dex/frost/core/curve"
	frostcrypto "dex/frost/core/frost"
	"dex/pb"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"golang.org/x/crypto/sha3"
)

                                                                            
                                                               
type KeyFile struct {
	BTCPrivateKey     string `json:"btcPrivateKey"`
	AccountPrivateKey string `json:"accountPrivateKey"`
}

func GenerateKeyFile(path string) (btcPublicKey, accountPublicKey string, err error) {
	btcKey, e := btcec.NewPrivateKey()
	if e != nil {
		return "", "", e
	}
	g := curve.NewBN256Group()
	d, e := rand.Int(rand.Reader, new(big.Int).Sub(g.Order(), big.NewInt(1)))
	if e != nil {
		return "", "", e
	}
	d.Add(d, big.NewInt(1))
	keys := KeyFile{BTCPrivateKey: hex.EncodeToString(btcKey.Serialize()), AccountPrivateKey: hex.EncodeToString(d.FillBytes(make([]byte, 32)))}
	raw, e := json.MarshalIndent(keys, "", "  ")
	if e != nil {
		return "", "", e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return "", "", e
	}
	if _, e = f.Write(raw); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return "", "", e
	}
	_, account, e := publicKeys(keys)
	if e != nil {
		return "", "", e
	}
	return hex.EncodeToString(schnorr.SerializePubKey(btcKey.PubKey())), hex.EncodeToString(account), nil
}

func publicKeys(keys KeyFile) (btc, account []byte, err error) {
	b, err := hex.DecodeString(keys.BTCPrivateKey)
	if err != nil || len(b) != 32 {
		return nil, nil, fmt.Errorf("invalid BTC guardian private key")
	}
	d := new(big.Int).SetBytes(b)
	if d.Sign() == 0 || d.Cmp(btcec.S256().Params().N) >= 0 {
		return nil, nil, fmt.Errorf("BTC guardian scalar out of range")
	}
	k, _ := btcec.PrivKeyFromBytes(b)
	btc = schnorr.SerializePubKey(k.PubKey())
	b, err = hex.DecodeString(keys.AccountPrivateKey)
	if err != nil || len(b) != 32 {
		return nil, nil, fmt.Errorf("invalid account guardian private key")
	}
	g := curve.NewBN256Group()
	d.SetBytes(b)
	if d.Sign() == 0 || d.Cmp(g.Order()) >= 0 {
		return nil, nil, fmt.Errorf("account guardian scalar out of range")
	}
	p := g.ScalarBaseMult(d)
	if p.Y.Cmp(new(big.Int).Rsh(g.Modulus(), 1)) > 0 {
		p.Y.Sub(g.Modulus(), p.Y)
	}
	return btc, g.SerializePoint(p), nil
}

type Signer struct {
	Resolver                 Resolver
	keys                     KeyFile
	btcPublic, accountPublic []byte
	gate                     chan struct{}
}

func NewSigner(keys KeyFile, btcPublic, accountPublic []byte, resolver Resolver) (*Signer, error) {
	b, a, err := publicKeys(keys)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(b, btcPublic) || !bytes.Equal(a, accountPublic) {
		return nil, fmt.Errorf("guardian private keys do not match the pinned public configuration")
	}
	if resolver == nil {
		return nil, fmt.Errorf("guardian requires its own validating node")
	}
	return &Signer{Resolver: resolver, keys: keys, btcPublic: b, accountPublic: a, gate: make(chan struct{}, 1)}, nil
}

func (s *Signer) Sign(ctx context.Context, r Request) ([][]byte, error) {
	intent, err := s.Resolver.Resolve(ctx, r)
	if err != nil {
		return nil, err
	}
	if intent == nil || intent.Request.JobID != r.JobID || intent.Request.Chain != r.Chain || intent.Request.VaultID != r.VaultID || intent.Request.Epoch != r.Epoch || !bytes.Equal(intent.Request.TemplateHash, r.TemplateHash) || len(intent.Messages) == 0 {
		return nil, fmt.Errorf("validating node returned a different intent")
	}
	algo := pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128
	pub := s.accountPublic
	if r.Chain == chain.ChainBTC {
		algo = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
		pub = s.btcPublic
	} else if !chain.IsValidChain(r.Chain) {
		return nil, fmt.Errorf("unsupported guardian chain")
	}
	if intent.SignAlgo != int32(algo) || !bytes.Equal(intent.PublicKey, pub) {
		return nil, fmt.Errorf("finalized guardian authority differs from pinned key")
	}
	signatures := make([][]byte, len(intent.Messages))
	for i, m := range intent.Messages {
		if len(m) != 32 {
			return nil, fmt.Errorf("invalid finalized signing message")
		}
		if algo == pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340 {
			raw, _ := hex.DecodeString(s.keys.BTCPrivateKey)
			key, _ := btcec.PrivKeyFromBytes(raw)
			sig, e := schnorr.Sign(key, m)
			if e != nil {
				return nil, e
			}
			signatures[i] = sig.Serialize()
		} else {
			signatures[i], err = s.signAccount(m)
			if err != nil {
				return nil, err
			}
		}
		valid, e := frostcrypto.Verify(algo, pub, m, signatures[i])
		if e != nil || !valid {
			return nil, fmt.Errorf("guardian self-verification failed")
		}
	}
	return signatures, nil
}

func (s *Signer) signAccount(m []byte) ([]byte, error) {
	g := curve.NewBN256Group()
	raw, _ := hex.DecodeString(s.keys.AccountPrivateKey)
	d := new(big.Int).SetBytes(raw)
	p := g.ScalarBaseMult(d)
	if p.Y.Cmp(new(big.Int).Rsh(g.Modulus(), 1)) > 0 {
		d.Sub(g.Order(), d)
		p = g.ScalarBaseMult(d)
	}
	for {
		k, err := rand.Int(rand.Reader, g.Order())
		if err != nil {
			return nil, err
		}
		if k.Sign() == 0 {
			continue
		}
		r := g.ScalarBaseMult(k)
		h := sha3.NewLegacyKeccak256()
		h.Write(g.SerializePoint(r))
		h.Write(g.SerializePoint(p))
		h.Write(m)
		e := new(big.Int).SetBytes(h.Sum(nil))
		e.Mod(e, g.Order())
		z := new(big.Int).Mul(e, d)
		z.Add(z, k)
		z.Mod(z, g.Order())
		if z.Sign() == 0 {
			continue
		}
		sig := append(g.SerializePoint(r), z.FillBytes(make([]byte, 32))...)
		return sig, nil
	}
}

func (s *Signer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/sign" {
		http.NotFound(w, r)
		return
	}
	request, err := DecodeRequest(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	default:
		http.Error(w, "guardian busy", http.StatusServiceUnavailable)
		return
	}
	sigs, err := s.Sign(r.Context(), request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Response{Signatures: sigs})
}
