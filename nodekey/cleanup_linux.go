//go:build linux

package nodekey

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const ImportCleanupSupported = true

                                                                               
type ImportSource struct{ snapshot *cleanupFile }

                                                                              
type VerifiedKeystore struct{ snapshot *cleanupFile }

type cleanupFile struct {
	file, parent  *os.File
	path          string
	limit         int
	stat          unix.Stat_t
	contentDigest [sha256.Size]byte
	keyDigest     [sha256.Size]byte
}

func (source *ImportSource) Close() {
	if source != nil {
		source.snapshot.close()
	}
}

func (verified *VerifiedKeystore) Close() {
	if verified != nil {
		verified.snapshot.close()
	}
}

func (f *cleanupFile) close() {
	if f == nil {
		return
	}
	if f.file != nil {
		_ = f.file.Close()
	}
	if f.parent != nil {
		_ = f.parent.Close()
	}
	*f = cleanupFile{}
}

func cleanupKeyDigest(key []byte) [sha256.Size]byte {
	                                                                    
	                                                     
	const tag = "dex/nodekey/import-cleanup/v1\x00"
	var input [len(tag) + 32]byte
	copy(input[:], tag)
	copy(input[len(tag):], key)
	defer Wipe(input[:])
	return sha256.Sum256(input[:])
}

func ReadPrivateKeyFileForCleanup(path string) ([]byte, *ImportSource, error) {
	f, raw, err := openCleanupFile(path, MaxPrivateKeyBytes, true)
	if err != nil {
		return nil, nil, err
	}
	defer Wipe(raw)
	key, err := decodePrivateKeyFile(raw)
	if err != nil {
		f.close()
		return nil, nil, err
	}
	f.keyDigest = cleanupKeyDigest(key)
	return key, &ImportSource{snapshot: f}, nil
}

func ReadEncryptedFileForCleanup(path string, password []byte) ([]byte, *VerifiedKeystore, error) {
	if err := validatePassword(password); err != nil {
		return nil, nil, err
	}
	f, raw, err := openCleanupFile(path, MaxKeystoreBytes, false)
	if err != nil {
		return nil, nil, err
	}
	defer Wipe(raw)
	key, err := decryptKeyFile(raw, password)
	if err != nil {
		f.close()
		return nil, nil, err
	}
	f.keyDigest = cleanupKeyDigest(key)
	return key, &VerifiedKeystore{snapshot: f}, nil
}

func cleanupOwnerTrusted(uid uint32) bool { return uid == 0 || uid == uint32(os.Geteuid()) }

func checkCleanupDirectory(fd int) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	                                                                      
	                                                                     
	stickyRoot := st.Uid == 0 && st.Mode&unix.S_ISVTX != 0
	if !cleanupOwnerTrusted(st.Uid) || st.Mode&0o022 != 0 && !stickyRoot {
		return errors.New("key directory permits renames by an untrusted user")
	}
	return nil
}

func openCleanupFile(path string, limit int, private bool) (*cleanupFile, []byte, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	dir, err := openKeyParent(abs, checkCleanupDirectory)
	if err != nil {
		return nil, nil, fmt.Errorf("open cleanup key directory: %w", err)
	}
	fd, err := unix.Openat(dir, filepath.Base(abs), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		_ = unix.Close(dir)
		return nil, nil, fmt.Errorf("open cleanup key file: %w", err)
	}
	f := &cleanupFile{
		file: os.NewFile(uintptr(fd), abs), parent: os.NewFile(uintptr(dir), filepath.Dir(abs)),
		path: abs, limit: limit,
	}
	if err := unix.Fstat(fd, &f.stat); err != nil {
		f.close()
		return nil, nil, fmt.Errorf("stat cleanup key file: %w", err)
	}
	if !cleanupOwnerTrusted(f.stat.Uid) || f.stat.Mode&unix.S_IFMT != unix.S_IFREG ||
		f.stat.Size <= 0 || f.stat.Size > int64(limit) || private && f.stat.Nlink != 1 {
		f.close()
		return nil, nil, errors.New("cleanup requires a trusted regular key file within the size limit and a single-link plaintext source")
	}
	if err := checkFilePermissions(os.FileMode(f.stat.Mode&0o777), private); err != nil {
		f.close()
		return nil, nil, err
	}
	raw, err := f.readUnchanged()
	if err != nil {
		f.close()
		return nil, nil, err
	}
	f.contentDigest = sha256.Sum256(raw)
	return f, raw, nil
}

func sameCleanupStat(a, b *unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Uid == b.Uid &&
		a.Gid == b.Gid && a.Nlink == b.Nlink && a.Size == b.Size && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}

func (f *cleanupFile) readUnchanged() ([]byte, error) {
	raw, err := readBounded(io.NewSectionReader(f.file, 0, int64(f.limit)+1), f.limit)
	if err != nil {
		return nil, fmt.Errorf("read cleanup key file: %w", err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(int(f.file.Fd()), &st); err != nil {
		Wipe(raw)
		return nil, fmt.Errorf("restat cleanup key file: %w", err)
	}
	if !sameCleanupStat(&f.stat, &st) || int64(len(raw)) != f.stat.Size {
		Wipe(raw)
		return nil, errors.New("cleanup key file metadata changed")
	}
	return raw, nil
}

func (f *cleanupFile) verifyContents() error {
	raw, err := f.readUnchanged()
	if err != nil {
		return err
	}
	defer Wipe(raw)
	if sha256.Sum256(raw) != f.contentDigest {
		return errors.New("cleanup key file contents changed")
	}
	return nil
}

func (f *cleanupFile) verifyPath() error {
	dir, err := openKeyParent(f.path, checkCleanupDirectory)
	if err != nil {
		return err
	}
	defer unix.Close(dir)
	var current, original unix.Stat_t
	if err := unix.Fstat(dir, &current); err != nil {
		return err
	}
	if err := unix.Fstat(int(f.parent.Fd()), &original); err != nil {
		return err
	}
	if current.Dev != original.Dev || current.Ino != original.Ino {
		return errors.New("cleanup key directory was replaced")
	}
	var st unix.Stat_t
	if err := unix.Fstatat(dir, filepath.Base(f.path), &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if !sameCleanupStat(&f.stat, &st) {
		return errors.New("cleanup key path no longer identifies the same file")
	}
	return nil
}

                                                                            
                                                                              
                                                                
func RemoveVerifiedImportSource(ctx context.Context, source *ImportSource, verified *VerifiedKeystore) (removed bool, err error) {
	defer source.Close()
	defer verified.Close()
	if source == nil || verified == nil {
		return false, errors.New("missing cleanup source or verified keystore")
	}
	plain, encrypted := source.snapshot, verified.snapshot
	if plain == nil || plain.file == nil || encrypted == nil || encrypted.file == nil {
		return false, errors.New("missing cleanup source or verified keystore")
	}
	if subtle.ConstantTimeCompare(plain.keyDigest[:], encrypted.keyDigest[:]) != 1 {
		return false, errors.New("cleanup source does not match the verified keystore")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := encrypted.verifyContents(); err != nil {
		return false, fmt.Errorf("recheck verified keystore: %w", err)
	}
	if err := encrypted.file.Sync(); err != nil {
		return false, fmt.Errorf("sync verified keystore: %w", err)
	}
	if err := encrypted.parent.Sync(); err != nil {
		return false, fmt.Errorf("sync verified keystore directory: %w", err)
	}
	if err := plain.verifyContents(); err != nil {
		return false, fmt.Errorf("recheck plaintext source: %w", err)
	}
	if err := encrypted.verifyPath(); err != nil {
		return false, fmt.Errorf("recheck verified keystore path: %w", err)
	}
	if err := plain.verifyPath(); err != nil {
		return false, fmt.Errorf("recheck plaintext source path: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := unix.Unlinkat(int(plain.parent.Fd()), filepath.Base(plain.path), 0); err != nil {
		return false, fmt.Errorf("remove plaintext source: %w", err)
	}
	                                                                             
	_ = plain.file.Close()
	plain.file = nil
	if err := plain.parent.Sync(); err != nil {
		return true, fmt.Errorf("plaintext source removed, but directory sync failed: %w", err)
	}
	return true, nil
}
