package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"

	"dex/nodekey"
	"dex/utils"
)

const (
	maxPrivateKeyFDBytes = 4 << 10
	maxWrapKeyFDBytes    = 256
	frostWrapKeyBytes    = 32
)

                                                                             
                                                                        
func validateRuntimeSecretOptions(opts *runtimeOptions) error {
	if opts == nil {
		return fmt.Errorf("runtime options are nil")
	}
	if opts.PrivateKeyFD < -1 || (opts.PrivateKeyFD >= 0 && opts.PrivateKeyFD < 3) {
		return fmt.Errorf("--private-key-fd must be -1 or an inherited descriptor >= 3")
	}
	if opts.FrostWrapKeyFD < -1 || (opts.FrostWrapKeyFD >= 0 && opts.FrostWrapKeyFD < 3) {
		return fmt.Errorf("--frost-wrap-key-fd must be -1 or an inherited descriptor >= 3")
	}
	if opts.ArbitragePrivateKeyFD < -1 || (opts.ArbitragePrivateKeyFD >= 0 && opts.ArbitragePrivateKeyFD < 3) {
		return fmt.Errorf("--arbitrage-private-key-fd must be -1 or an inherited descriptor >= 3")
	}
	if opts.KeyManager != nil {
		return fmt.Errorf("node private key must only be loaded from an approved runtime secret source")
	}
	if opts.ArbitrageKeyManager != nil {
		return fmt.Errorf("arbitrage private key must only be loaded from an approved runtime secret source")
	}
	if opts.KeystorePasswordFD < -1 || (opts.KeystorePasswordFD >= 0 && opts.KeystorePasswordFD < 3) {
		return fmt.Errorf("--keystore-password-fd must be -1 or an inherited descriptor >= 3")
	}
	if opts.DeleteKeystoreImportSource {
		if opts.KeystorePath == "" || opts.KeystoreImportFrom == "" {
			return fmt.Errorf("--delete-keystore-import-source requires --keystore and --keystore-import-from")
		}
		if !nodekey.ImportCleanupSupported {
			return fmt.Errorf("--delete-keystore-import-source is supported only on Linux")
		}
	}
	if opts.KeystoreImportFrom != "" && opts.KeystorePath == "" {
		return fmt.Errorf("--keystore-import-from requires --keystore")
	}
	if opts.KeystorePath == "" && opts.KeystorePasswordFD >= 0 {
		return fmt.Errorf("--keystore-password-fd requires --keystore")
	}
	if opts.KeystorePath != "" && (opts.PrivateKeyFD != -1 || opts.WindowsLocalSecretDir != "" || (opts.AllowWindowsLocalSecretDir && opts.WindowsArbitrageSecretDir == "")) {
		return fmt.Errorf("--keystore conflicts with raw private-key sources")
	}
	if err := validateDistinctSecretFDs(
		runtimeSecretFD{"node private key", opts.PrivateKeyFD},
		runtimeSecretFD{"keystore password", opts.KeystorePasswordFD},
		runtimeSecretFD{"arbitrage private key", opts.ArbitragePrivateKeyFD},
		runtimeSecretFD{"FROST wrap key", opts.FrostWrapKeyFD},
	); err != nil {
		return err
	}

	return validatePlatformSecretOptions(opts)
}

func loadRuntimeSecrets(opts *runtimeOptions) (err error) {
	if err := validateRuntimeSecretOptions(opts); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			opts.keystoreImportCleanup.close()
			opts.keystoreImportCleanup = nil
		}
	}()

	var identity *utils.KeyManager
	if opts.KeystorePath != "" {
		identity, err = loadRuntimeKeystoreIdentity(opts, defaultRuntimeKeystoreOperations())
	} else {
		var rawKey []byte
		rawKey, err = loadPlatformPrivateKey(opts)
		defer wipeBytes(rawKey)
		if err == nil {
			identity = utils.NewKeyManager()
			err = identity.InitKeyBytes(rawKey)
			wipeBytes(rawKey)
			if err != nil {
				err = fmt.Errorf("initialize node identity: %w", err)
			}
		}
	}
	if err != nil {
		return err
	}
	arbitrageKey, err := loadPlatformArbitragePrivateKey(opts)
	if err != nil {
		return err
	}
	defer wipeBytes(arbitrageKey)
	var arbitrageIdentity *utils.KeyManager
	if len(arbitrageKey) != 0 {
		arbitrageIdentity = utils.NewKeyManager()
		if err := arbitrageIdentity.InitKeyBytes(arbitrageKey); err != nil {
			return fmt.Errorf("initialize arbitrage identity: %w", err)
		}
		wipeBytes(arbitrageKey)
	}

	if opts.FrostWrapKeyFD >= 0 {
		raw, err := readSecretFromFD(opts.FrostWrapKeyFD, maxWrapKeyFDBytes, "frost-wrap-key")
		if err != nil {
			return fmt.Errorf("read FROST wrap key FD: %w", err)
		}
		defer wipeBytes(raw)
		key, err := decodeFrostWrapKey(raw)
		if err != nil {
			return fmt.Errorf("parse FROST wrap key FD: %w", err)
		}
		opts.FrostWrapKey = key
	}
	opts.KeyManager = identity
	opts.ArbitrageKeyManager = arbitrageIdentity
	return nil
}

                                                                           
func decodePrivateKeyToken(raw []byte) ([]byte, error) {
	fields := bytes.Fields(raw)
	if len(fields) != 1 {
		return nil, fmt.Errorf("expected exactly one hex private key")
	}
	token := fields[0]
	if bytes.HasPrefix(token, []byte("0x")) || bytes.HasPrefix(token, []byte("0X")) {
		token = token[2:]
	}
	if len(token) == 0 || len(token) > 64 || len(token)%2 != 0 {
		return nil, fmt.Errorf("private key must be at most 64 hex characters")
	}
	key := make([]byte, 32)
	if _, err := hex.Decode(key[32-len(token)/2:], token); err != nil {
		wipeBytes(key)
		return nil, fmt.Errorf("private key is not valid hex")
	}
	return key, nil
}

type runtimeSecretFD struct {
	label string
	fd    int
}

func validateDistinctSecretFDs(entries ...runtimeSecretFD) error {
	seen := make(map[int]string, len(entries))
	for _, entry := range entries {
		if entry.fd < 0 {
			continue
		}
		if previous, exists := seen[entry.fd]; exists {
			return fmt.Errorf("%s and %s must use different inherited descriptors", previous, entry.label)
		}
		seen[entry.fd] = entry.label
	}
	return nil
}

func readSecretFromFD(fd, maxBytes int, label string) ([]byte, error) {
	file := os.NewFile(uintptr(fd), label)
	if file == nil {
		return nil, fmt.Errorf("descriptor is unavailable")
	}
	defer file.Close()
	return readBoundedSecret(file, maxBytes)
}

func readBoundedSecret(r io.Reader, maxBytes int) ([]byte, error) {
	if r == nil || maxBytes <= 0 {
		return nil, fmt.Errorf("invalid secret reader")
	}
	raw := make([]byte, maxBytes+1)
	n, err := io.ReadFull(r, raw)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		wipeBytes(raw)
		return nil, err
	}
	if n == 0 {
		wipeBytes(raw)
		return nil, fmt.Errorf("secret is empty")
	}
	if n > maxBytes {
		wipeBytes(raw)
		return nil, fmt.Errorf("secret exceeds %d bytes", maxBytes)
	}
	raw = raw[:n]
	return raw, nil
}

func decodeFrostWrapKey(raw []byte) ([]byte, error) {
	fields := bytes.Fields(raw)
	if len(fields) != 1 {
		return nil, fmt.Errorf("expected exactly one hex token")
	}
	hexBytes := fields[0]
	if bytes.HasPrefix(hexBytes, []byte("0x")) || bytes.HasPrefix(hexBytes, []byte("0X")) {
		hexBytes = hexBytes[2:]
	}
	if len(hexBytes) != frostWrapKeyBytes*2 {
		return nil, fmt.Errorf("wrap key must contain exactly %d hex characters", frostWrapKeyBytes*2)
	}
	out := make([]byte, frostWrapKeyBytes)
	if _, err := hex.Decode(out, hexBytes); err != nil {
		wipeBytes(out)
		return nil, fmt.Errorf("wrap key is not valid hex")
	}
	return out, nil
}

func wipeBytes(data []byte) {
	clear(data)
	runtime.KeepAlive(data)
}
