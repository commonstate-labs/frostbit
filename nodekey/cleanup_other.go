//go:build !linux

package nodekey

import (
	"context"
	"errors"
)

const ImportCleanupSupported = false

var errImportCleanupUnsupported = errors.New("automatic plaintext import cleanup requires Linux")

type ImportSource struct{}
type VerifiedKeystore struct{}

func (*ImportSource) Close()     {}
func (*VerifiedKeystore) Close() {}

func ReadPrivateKeyFileForCleanup(string) ([]byte, *ImportSource, error) {
	return nil, nil, errImportCleanupUnsupported
}

func ReadEncryptedFileForCleanup(string, []byte) ([]byte, *VerifiedKeystore, error) {
	return nil, nil, errImportCleanupUnsupported
}

func RemoveVerifiedImportSource(context.Context, *ImportSource, *VerifiedKeystore) (bool, error) {
	return false, errImportCleanupUnsupported
}
