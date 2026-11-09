                                                                                                    
package nodekey

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	gethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
	gethcrypto "github.com/ethereum/go-ethereum/crypto"
)

const (
	MaxKeystoreBytes   = 64 * 1024
	MaxPrivateKeyBytes = 4096
	MaxPassphraseBytes = 1024
	                                                                                                
	MinNewPassphraseBytes = 12
)

                                                                                      
type Metadata struct {
	Version         int    `json:"version"`
	EthereumAddress string `json:"ethereum_address"`
	Cipher          string `json:"cipher"`
	KDF             string `json:"kdf"`
	ScryptN         int    `json:"scrypt_n"`
	ScryptR         int    `json:"scrypt_r"`
	ScryptP         int    `json:"scrypt_p"`
}

type encryptedFile struct {
	Address string `json:"address"`
	ID      string `json:"id"`
	Version int    `json:"version"`
	Crypto  struct {
		Cipher       string `json:"cipher"`
		CipherText   string `json:"ciphertext"`
		CipherParams struct {
			IV string `json:"iv"`
		} `json:"cipherparams"`
		KDF       string `json:"kdf"`
		KDFParams struct {
			N     int    `json:"n"`
			R     int    `json:"r"`
			P     int    `json:"p"`
			DKLen int    `json:"dklen"`
			Salt  string `json:"salt"`
		} `json:"kdfparams"`
		MAC string `json:"mac"`
	} `json:"crypto"`
}

                                                                                                      
func Wipe(data []byte) { clear(data); runtime.KeepAlive(data) }

func clearPrivateKey(key *ecdsa.PrivateKey) {
	if key != nil && key.D != nil {
		clear(key.D.Bits())
		key.D.SetInt64(0)
		runtime.KeepAlive(key)
	}
}

func validatePassword(password []byte) error {
	if len(password) == 0 || len(password) > MaxPassphraseBytes {
		return fmt.Errorf("passphrase must contain 1..%d bytes", MaxPassphraseBytes)
	}
	if bytes.ContainsAny(password, "\x00\r\n") {
		return errors.New("passphrase must be a single line without NUL")
	}
	return nil
}

func validateHex(name, value string, minBytes, maxBytes int) error {
	if len(value)%2 != 0 || len(value) < minBytes*2 || len(value) > maxBytes*2 {
		return fmt.Errorf("invalid keystore %s length", name)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("invalid keystore %s encoding", name)
	}
	return nil
}

                                                                                                                        
func inspectJSON(raw []byte) (*encryptedFile, []byte, error) {
	if len(raw) == 0 || len(raw) > MaxKeystoreBytes {
		return nil, nil, errors.New("keystore is empty or exceeds size limit")
	}
	var f encryptedFile
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		return nil, nil, errors.New("invalid keystore JSON")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, nil, errors.New("keystore must contain one JSON object")
	}
	if f.Version != 3 || f.Crypto.Cipher != "aes-128-ctr" || f.Crypto.KDF != "scrypt" {
		return nil, nil, errors.New("only Geth V3 scrypt/aes-128-ctr keystores are supported")
	}
	p := f.Crypto.KDFParams
	                                                                                                
	if p.N != gethkeystore.StandardScryptN || p.R != 8 || p.P != gethkeystore.StandardScryptP || p.DKLen != 32 {
		return nil, nil, errors.New("keystore requires standard scrypt parameters (N=262144 r=8 p=1 dklen=32)")
	}
	for _, v := range []struct {
		name, value string
		min, max    int
	}{
		{"address", f.Address, 20, 20}, {"IV", f.Crypto.CipherParams.IV, 16, 16},
		{"ciphertext", f.Crypto.CipherText, 32, 32}, {"MAC", f.Crypto.MAC, 32, 32}, {"salt", p.Salt, 16, 64},
	} {
		if err := validateHex(v.name, v.value, v.min, v.max); err != nil {
			return nil, nil, err
		}
	}
	if len(f.ID) != 36 {
		return nil, nil, errors.New("invalid keystore UUID")
	}
	canonical, err := json.Marshal(&f)
	if err != nil {
		return nil, nil, errors.New("cannot encode keystore")
	}
	return &f, canonical, nil
}

func readFile(path string, limit int64, private bool) ([]byte, error) {
	f, err := openSafe(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("open key file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat key file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return nil, errors.New("key file must be a non-empty regular file within size limit")
	}
	if err := checkFilePermissions(info.Mode(), private); err != nil {
		return nil, err
	}
	raw, err := readBounded(f, int(limit))
	if err != nil {
		Wipe(raw)
		return nil, errors.New("read key file failed")
	}
	if len(raw) == 0 {
		Wipe(raw)
		return nil, errors.New("key file exceeds size limit or became empty")
	}
	return raw, nil
}

                                                                                            
func InspectFile(path string) (Metadata, error) {
	raw, err := readFile(path, MaxKeystoreBytes, false)
	if err != nil {
		return Metadata{}, err
	}
	f, _, err := inspectJSON(raw)
	if err != nil {
		return Metadata{}, err
	}
	p := f.Crypto.KDFParams
	return Metadata{f.Version, f.Address, f.Crypto.Cipher, f.Crypto.KDF, p.N, p.R, p.P}, nil
}

                                                                                                
func ReadEncryptedFile(path string, password []byte) ([]byte, error) {
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	raw, err := readFile(path, MaxKeystoreBytes, false)
	if err != nil {
		return nil, err
	}
	return decryptKeyFile(raw, password)
}

                                                              
func decryptKeyFile(raw, password []byte) ([]byte, error) {
	f, canonical, err := inspectJSON(raw)
	if err != nil {
		return nil, err
	}
	                                                                                                   
	key, err := gethkeystore.DecryptKey(canonical, string(password))
	if err != nil {
		return nil, errors.New("cannot unlock keystore: wrong passphrase or invalid file")
	}
	defer clearPrivateKey(key.PrivateKey)
	if !strings.EqualFold(f.Address, hex.EncodeToString(key.Address[:])) {
		return nil, errors.New("keystore address does not match decrypted key")
	}
	return key.PrivateKey.D.FillBytes(make([]byte, 32)), nil
}

                                                                                                  
func EncryptFile(path string, key32, password []byte) error {
	if err := validatePassword(password); err != nil {
		return err
	}
	if len(password) < MinNewPassphraseBytes {
		return fmt.Errorf("new keystore passphrase must contain at least %d bytes; use a long random passphrase", MinNewPassphraseBytes)
	}
	return encryptFile(path, key32, password, gethkeystore.StandardScryptN, gethkeystore.StandardScryptP)
}

func encryptFile(path string, key32, password []byte, n, p int) error {
	if err := validatePassword(password); err != nil {
		return err
	}
	key, err := gethcrypto.ToECDSA(key32)
	if err != nil {
		return errors.New("private key must be a valid 32-byte secp256k1 scalar")
	}
	defer clearPrivateKey(key)
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return errors.New("generate keystore UUID failed")
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	encrypted, err := gethkeystore.EncryptKey(&gethkeystore.Key{Id: id, Address: gethcrypto.PubkeyToAddress(key.PublicKey), PrivateKey: key}, string(password), n, p)
	if err != nil {
		return errors.New("encrypt keystore failed")
	}
	f, err := openSafe(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create new keystore: %w", err)
	}
	if _, err = f.Write(encrypted); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("write new keystore failed; original key was not changed: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close new keystore: %w", closeErr)
	}
	return nil
}

                                                                                                     
func ReadPrivateKeyFile(path string) ([]byte, error) {
	raw, err := readFile(path, MaxPrivateKeyBytes, true)
	if err != nil {
		return nil, err
	}
	defer Wipe(raw)
	return decodePrivateKeyFile(raw)
}

func decodePrivateKeyFile(raw []byte) ([]byte, error) {
	fields := bytes.Fields(raw)
	if len(fields) != 1 {
		return nil, errors.New("private key file must contain one hex token")
	}
	token := fields[0]
	if bytes.HasPrefix(token, []byte("0x")) || bytes.HasPrefix(token, []byte("0X")) {
		token = token[2:]
	}
	if len(token) != 64 {
		return nil, errors.New("private key must contain 64 hex characters")
	}
	key32 := make([]byte, 32)
	if _, err := hex.Decode(key32, token); err != nil {
		Wipe(key32)
		return nil, errors.New("invalid private key hex")
	}
	key, err := gethcrypto.ToECDSA(key32)
	if err != nil {
		Wipe(key32)
		return nil, errors.New("invalid secp256k1 private scalar")
	}
	clearPrivateKey(key)
	return key32, nil
}
