package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"

	"dex/logs"
	"dex/nodekey"
	"dex/utils"
)

                                                                                 
                                                                              
                                                                          
type runtimeKeystoreOperations struct {
	lstat             func(string) (os.FileInfo, error)
	readPrivateKey    func(string) ([]byte, error)
	readImportSource  func(string) ([]byte, *nodekey.ImportSource, error)
	encryptFile       func(string, []byte, []byte) error
	readEncryptedFile func(string, []byte) ([]byte, error)
	readVerifiedFile  func(string, []byte) ([]byte, *nodekey.VerifiedKeystore, error)
	readPassphraseTTY func(string) ([]byte, error)
	readPassphraseFD  func(int) ([]byte, error)
	report            func(string, ...any)
}

func defaultRuntimeKeystoreOperations() runtimeKeystoreOperations {
	return runtimeKeystoreOperations{
		lstat: os.Lstat, readPrivateKey: nodekey.ReadPrivateKeyFile,
		readImportSource: nodekey.ReadPrivateKeyFileForCleanup,
		encryptFile:      nodekey.EncryptFile, readEncryptedFile: nodekey.ReadEncryptedFile,
		readVerifiedFile:  nodekey.ReadEncryptedFileForCleanup,
		readPassphraseTTY: nodekey.ReadPassphraseTTY, readPassphraseFD: nodekey.ReadPassphraseFD,
		report: logs.Info,
	}
}

                                                                               
                                                 
type keystoreImportCleanup struct {
	source   *nodekey.ImportSource
	verified *nodekey.VerifiedKeystore
}

func (cleanup *keystoreImportCleanup) close() {
	if cleanup == nil {
		return
	}
	cleanup.source.Close()
	cleanup.verified.Close()
	cleanup.source, cleanup.verified = nil, nil
}

func (cleanup *keystoreImportCleanup) remove(ctx context.Context) (bool, error) {
	if cleanup == nil {
		return false, fmt.Errorf("no keystore import cleanup was prepared")
	}
	defer cleanup.close()
	return nodekey.RemoveVerifiedImportSource(ctx, cleanup.source, cleanup.verified)
}

func readRuntimeKeystore(path string, password []byte, cleanup *keystoreImportCleanup, ops runtimeKeystoreOperations) ([]byte, error) {
	if cleanup == nil {
		return ops.readEncryptedFile(path, password)
	}
	raw, proof, err := ops.readVerifiedFile(path, password)
	cleanup.verified = proof
	return raw, err
}

                                                                                  
                                                                        
func loadRuntimeKeystoreIdentity(opts *runtimeOptions, ops runtimeKeystoreOperations) (*utils.KeyManager, error) {
	var cleanup *keystoreImportCleanup
	if opts.DeleteKeystoreImportSource {
		cleanup = &keystoreImportCleanup{}
		defer func() { cleanup.close() }()
	}

	_, err := ops.lstat(opts.KeystorePath)
	if err == nil {
		                                                                   
		                                                                         
		password, err := readRuntimeKeystorePassphrase(opts, ops, false)
		if err != nil {
			return nil, err
		}
		defer wipeBytes(password)
		raw, err := readRuntimeKeystore(opts.KeystorePath, password, cleanup, ops)
		if err != nil {
			wipeBytes(raw)
			return nil, err
		}
		defer wipeBytes(raw)
		identity, err := runtimeKeystoreIdentity(raw, opts.ExpectedAddress)
		if err != nil {
			return nil, err
		}
		if cleanup != nil {
			sourceKey, source, err := ops.readImportSource(opts.KeystoreImportFrom)
			cleanup.source = source
			matches := err == nil && len(sourceKey) == 32 && subtle.ConstantTimeCompare(raw, sourceKey) == 1
			wipeBytes(sourceKey)
			switch {
			case matches:
				opts.keystoreImportCleanup, cleanup = cleanup, nil
			case err == nil:
				logs.Warn("Miner import source retained at %s: key does not match the unlocked keystore", opts.KeystoreImportFrom)
			case !errors.Is(err, os.ErrNotExist):
				logs.Warn("Miner import source retained at %s: %v", opts.KeystoreImportFrom, err)
			}
		}
		ops.report("Unlocked miner keystore for %s", identity.GetAddress())
		return identity, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect miner keystore path: %w", err)
	}
	if opts.KeystoreImportFrom == "" {
		return nil, fmt.Errorf("miner keystore does not exist; use --keystore-import-from for its first import")
	}

	var raw []byte
	if cleanup == nil {
		raw, err = ops.readPrivateKey(opts.KeystoreImportFrom)
	} else {
		raw, cleanup.source, err = ops.readImportSource(opts.KeystoreImportFrom)
	}
	if err != nil {
		wipeBytes(raw)
		return nil, fmt.Errorf("read miner keystore import source: %w", err)
	}
	defer wipeBytes(raw)
	identity, err := runtimeKeystoreIdentity(raw, opts.ExpectedAddress)
	if err != nil {
		return nil, err
	}
	password, err := readRuntimeKeystorePassphrase(opts, ops, true)
	if err != nil {
		return nil, err
	}
	defer wipeBytes(password)
	if err := ops.encryptFile(opts.KeystorePath, raw, password); err != nil {
		return nil, fmt.Errorf("create miner keystore: %w", err)
	}
	                                                                             
	                                                                            
	                                                                            
	verified, err := readRuntimeKeystore(opts.KeystorePath, password, cleanup, ops)
	if err != nil {
		wipeBytes(verified)
		return nil, fmt.Errorf("verify newly written miner keystore (source retained): %w", err)
	}
	defer wipeBytes(verified)
	if len(verified) != 32 || subtle.ConstantTimeCompare(raw, verified) != 1 {
		return nil, fmt.Errorf("new miner keystore does not reproduce the imported key; source retained")
	}
	if cleanup != nil {
		opts.keystoreImportCleanup, cleanup = cleanup, nil
		ops.report("Created and verified encrypted miner keystore for %s at %s. Source cleanup awaits node readiness", identity.GetAddress(), opts.KeystorePath)
	} else {
		ops.report("Created and verified encrypted miner keystore for %s at %s; original private key file was retained", identity.GetAddress(), opts.KeystorePath)
	}
	return identity, nil
}

func runtimeKeystoreIdentity(raw []byte, expectedAddress string) (*utils.KeyManager, error) {
	identity := utils.NewKeyManager()
	if err := identity.InitKeyBytes(raw); err != nil {
		return nil, fmt.Errorf("initialize node identity: %w", err)
	}
	if err := validateExpectedAddress(expectedAddress, identity.GetAddress()); err != nil {
		return nil, err
	}
	return identity, nil
}

func readRuntimeKeystorePassphrase(opts *runtimeOptions, ops runtimeKeystoreOperations, confirm bool) ([]byte, error) {
	if opts.KeystorePasswordFD >= 3 {
		password, err := ops.readPassphraseFD(opts.KeystorePasswordFD)
		if err != nil {
			wipeBytes(password)
			return nil, fmt.Errorf("read keystore passphrase: %w", err)
		}
		return password, nil
	}
	prompt := "Miner keystore passphrase: "
	if confirm {
		prompt = "New miner keystore passphrase (at least 12 bytes): "
	}
	password, err := ops.readPassphraseTTY(prompt)
	if err != nil {
		wipeBytes(password)
		return nil, fmt.Errorf("read keystore passphrase: %w", err)
	}
	if !confirm {
		return password, nil
	}
	confirmation, err := ops.readPassphraseTTY("Confirm miner keystore passphrase: ")
	defer wipeBytes(confirmation)
	if err != nil {
		wipeBytes(password)
		return nil, fmt.Errorf("confirm keystore passphrase: %w", err)
	}
	if len(password) != len(confirmation) || subtle.ConstantTimeCompare(password, confirmation) != 1 {
		wipeBytes(password)
		return nil, fmt.Errorf("keystore passphrases do not match")
	}
	return password, nil
}
