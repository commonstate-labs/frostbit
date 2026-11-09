package statedb

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	pathutil "path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const CheckpointChunkSize int64 = 10 << 20

type CheckpointFile struct {
	Name string
	Size int64
}

func checkpointDirName(height uint64) string {
	return fmt.Sprintf("h_%020d", height)
}

func (s *DB) ListCheckpointFiles(height uint64) ([]CheckpointFile, int64, error) {
	if height == 0 {
		return nil, 0, fmt.Errorf("checkpoint height must be > 0")
	}

	s.mu.RLock()
	if err := s.validateCheckpointManifestFastLocked(height, false); err != nil {
		s.mu.RUnlock()
		return nil, 0, err
	}
	store, err := s.openCheckpointStore(height)
	cpDir := s.checkpointPath(height)
	manifestPath := s.checkpointManifestPath(height)
	s.mu.RUnlock()
	if err != nil {
		return nil, 0, err
	}
	defer store.Close()

	rootName := checkpointDirName(height)
	files := make([]CheckpointFile, 0, 16)
	var total int64
	addFile := func(fsPath, name string) error {
		info, err := os.Stat(fsPath)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("checkpoint file sync only supports regular files: %s", fsPath)
		}
		files = append(files, CheckpointFile{Name: name, Size: info.Size()})
		total += info.Size()
		return nil
	}
	if err := addFile(manifestPath, rootName+".manifest.json"); err != nil {
		return nil, 0, err
	}
	if err := filepath.WalkDir(cpDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() == "LOCK" {
			return nil
		}
		rel, err := filepath.Rel(cpDir, path)
		if err != nil {
			return err
		}
		return addFile(path, rootName+"/"+filepath.ToSlash(rel))
	}); err != nil {
		return nil, 0, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, total, nil
}

func (s *DB) WriteCheckpointChunk(height uint64, name string, offset, limit int64, w io.Writer) (int64, error) {
	if height == 0 {
		return 0, fmt.Errorf("checkpoint height must be > 0")
	}
	if limit <= 0 || limit > CheckpointChunkSize {
		return 0, fmt.Errorf("invalid checkpoint chunk limit %d", limit)
	}
	if offset < 0 {
		return 0, fmt.Errorf("invalid checkpoint chunk offset %d", offset)
	}
	if w == nil {
		return 0, fmt.Errorf("nil checkpoint chunk writer")
	}
	fsPath, size, err := s.checkpointFilePath(height, name)
	if err != nil {
		return 0, err
	}
	if offset > size {
		return 0, fmt.Errorf("checkpoint chunk offset %d beyond file size %d", offset, size)
	}
	if offset+limit > size {
		limit = size - offset
	}
	f, err := os.Open(fsPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	return io.CopyN(w, f, limit)
}

func (s *DB) checkpointFilePath(height uint64, name string) (string, int64, error) {
	clean, err := cleanCheckpointFileName(name)
	if err != nil {
		return "", 0, err
	}
	rootName := checkpointDirName(height)

	s.mu.RLock()
	if err := s.validateCheckpointManifestFastLocked(height, false); err != nil {
		s.mu.RUnlock()
		return "", 0, err
	}
	cpDir := s.checkpointPath(height)
	manifestPath := s.checkpointManifestPath(height)
	s.mu.RUnlock()

	var fsPath string
	switch {
	case clean == rootName+".manifest.json":
		fsPath = manifestPath
	case strings.HasPrefix(clean, rootName+"/"):
		rel := strings.TrimPrefix(clean, rootName+"/")
		if rel == "" || rel == "LOCK" || strings.Contains(rel, "/LOCK") {
			return "", 0, fmt.Errorf("invalid checkpoint file: %s", name)
		}
		dst, err := safeCheckpointJoin(cpDir, rel)
		if err != nil {
			return "", 0, err
		}
		fsPath = dst
	default:
		return "", 0, fmt.Errorf("unexpected checkpoint file: %s", name)
	}
	info, err := os.Stat(fsPath)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("checkpoint file is not regular: %s", name)
	}
	return fsPath, info.Size(), nil
}

func (s *DB) InstallCheckpointDir(ctx context.Context, height uint64, expectedBlockID string, checkpointDir string, expectedStateRoot []byte) error {
	if height == 0 {
		return fmt.Errorf("checkpoint height must be > 0")
	}
	if checkpointDir == "" {
		return fmt.Errorf("empty checkpoint directory path")
	}
	if expectedBlockID == "" {
		return fmt.Errorf("empty checkpoint block identity")
	}
	if s.conf.ReadOnly {
		return fmt.Errorf("cannot install checkpoint directory into read-only stateDB")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := os.MkdirAll(s.conf.DataDir, 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(checkpointDir); err != nil {
		return err
	} else if info.IsDir() {
		tmpDir, err := os.MkdirTemp(s.conf.DataDir, ".checkpoint-import-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)
		if err := copyCheckpointImportDir(ctx, checkpointDir, tmpDir); err != nil {
			return err
		}
		return s.installCheckpointDir(ctx, height, expectedBlockID, tmpDir, expectedStateRoot)
	}
	return fmt.Errorf("checkpoint install path must be a directory")
}

func (s *DB) installCheckpointDir(ctx context.Context, height uint64, expectedBlockID string, rootDir string, expectedStateRoot []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cpDir := filepath.Join(rootDir, checkpointDirName(height))
	manifestPath := filepath.Join(rootDir, checkpointDirName(height)+".manifest.json")
	manifest, err := s.validateExtractedCheckpoint(height, cpDir, manifestPath, expectedStateRoot)
	if err != nil {
		return err
	}
	return s.replaceLiveWithExtractedCheckpoint(height, expectedBlockID, cpDir, manifest)
}

func copyCheckpointImportDir(ctx context.Context, srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dst := filepath.Join(dstDir, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("checkpoint import only supports regular files: %s", path)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode()&0o777)
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeOutErr := out.Close()
		closeInErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeOutErr != nil {
			return closeOutErr
		}
		return closeInErr
	})
}

func cleanCheckpointFileName(name string) (string, error) {
	if name == "" || strings.Contains(name, "\x00") || strings.Contains(name, "\\") {
		return "", fmt.Errorf("invalid checkpoint file name: %q", name)
	}
	clean := pathutil.Clean(name)
	if clean == "." || clean == ".." || pathutil.IsAbs(clean) || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid checkpoint file path: %q", name)
	}
	return clean, nil
}

func safeCheckpointJoin(root, name string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(rootAbs, filepath.FromSlash(name))
	rel, err := filepath.Rel(rootAbs, dst)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("checkpoint file path escapes target: %s", name)
	}
	return dst, nil
}

func (s *DB) validateExtractedCheckpoint(height uint64, cpDir, manifestPath string, expectedStateRoot []byte) (checkpointManifest, error) {
	manifest, err := readCheckpointManifestFile(manifestPath)
	if err != nil {
		return checkpointManifest{}, err
	}
	if manifest.Version != checkpointManifestVersion {
		return checkpointManifest{}, fmt.Errorf("manifest version mismatch: got=%d want=%d", manifest.Version, checkpointManifestVersion)
	}
	if manifest.Height != height {
		return checkpointManifest{}, fmt.Errorf("manifest height mismatch: got=%d want=%d", manifest.Height, height)
	}
	if len(manifest.StateRoot) != StateRootSize {
		return checkpointManifest{}, fmt.Errorf("manifest state root length mismatch: got=%d want=%d", len(manifest.StateRoot), StateRootSize)
	}
	if len(expectedStateRoot) > 0 {
		if len(expectedStateRoot) < StateRootSize {
			return checkpointManifest{}, fmt.Errorf("expected state root length mismatch: got=%d want=%d", len(expectedStateRoot), StateRootSize)
		}
		if IsZeroFingerprintBytes(expectedStateRoot) {
			return checkpointManifest{}, fmt.Errorf("expected state root is zero")
		}
		_, _, matched := CompareFingerprintBytes(manifest.StateRoot, expectedStateRoot)
		if !matched {
			return checkpointManifest{}, fmt.Errorf("checkpoint state root mismatch")
		}
	}

	store, err := newPebbleStoreReadOnly(cpDir, s.conf.TableCacheShards, s.conf.BlockCacheSize)
	if err != nil {
		return checkpointManifest{}, err
	}
	defer store.Close()
	want, err := s.buildCheckpointManifestFromStore(height, manifest.CreatedUnixNano, store)
	if err != nil {
		return checkpointManifest{}, err
	}
	if err := compareCheckpointManifest(manifest, want); err != nil {
		return checkpointManifest{}, err
	}
	return manifest, nil
}

func (s *DB) replaceLiveWithExtractedCheckpoint(height uint64, expectedBlockID string, importedDir string, manifest checkpointManifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureNoCheckpointReadersLocked(); err != nil {
		return err
	}
	if s.store != nil {
		if err := s.store.Close(); err != nil {
			return err
		}
		s.store = nil
	}

	backupDir := fmt.Sprintf("%s.backup_%d", s.liveDir, time.Now().UnixNano())
	backupCreated := false
	if _, err := os.Stat(s.liveDir); err == nil {
		if err := os.Rename(s.liveDir, backupDir); err != nil {
			return s.reopenLiveAfterInstallFailure(fmt.Errorf("backup live stateDB: %w", err), "", false)
		}
		backupCreated = true
	} else if err != nil && !os.IsNotExist(err) {
		return s.reopenLiveAfterInstallFailure(err, "", false)
	}

	if err := os.Rename(importedDir, s.liveDir); err != nil {
		return s.reopenLiveAfterInstallFailure(fmt.Errorf("install checkpoint live dir: %w", err), backupDir, backupCreated)
	}
	store, err := newPebbleStore(s.liveDir, s.conf.TableCacheShards, s.conf.BlockCacheSize)
	if err != nil {
		return s.reopenLiveAfterInstallFailure(fmt.Errorf("open installed live stateDB: %w", err), backupDir, backupCreated)
	}
	s.store = store
	if err := s.rebuildLiveFingerprints(); err != nil {
		return s.reopenLiveAfterInstallFailure(fmt.Errorf("rebuild installed live fingerprints: %w", err), backupDir, backupCreated)
	}
	if !bytes.Equal(s.getLiveFingerprintBytesLocked(), manifest.StateRoot) {
		return s.reopenLiveAfterInstallFailure(fmt.Errorf("installed live state root mismatch"), backupDir, backupCreated)
	}
	                                                                             
	                                                                           
	                                                                             
	if err := s.normalizeInstalledCheckpointMetadataLocked(height, expectedBlockID); err != nil {
		return s.reopenLiveAfterInstallFailure(fmt.Errorf("normalize installed checkpoint metadata: %w", err), backupDir, backupCreated)
	}
	if err := s.createCheckpointLocked(height, true); err != nil {
		return s.reopenLiveAfterInstallFailure(fmt.Errorf("create local checkpoint after directory install: %w", err), backupDir, backupCreated)
	}
	if backupCreated {
		_ = os.RemoveAll(backupDir)
	}
	return nil
}

func (s *DB) normalizeInstalledCheckpointMetadataLocked(height uint64, blockID string) error {
	if height == 0 || blockID == "" {
		return fmt.Errorf("invalid installed checkpoint identity height=%d block=%q", height, blockID)
	}
	return s.store.Update(func(tx kvWriter) error {
		var oldKeys [][]byte
		if err := tx.IteratePrefix(kMetaAppliedBlockPrefix(), nil, func(key []byte, _ []byte) error {
			oldKeys = append(oldKeys, append([]byte(nil), key...))
			return nil
		}); err != nil {
			return err
		}
		for _, key := range oldKeys {
			if err := tx.Delete(key); err != nil && !s.store.IsNotFound(err) {
				return err
			}
		}
		hb := make([]byte, 8)
		binary.BigEndian.PutUint64(hb, height)
		if err := tx.Set(kMetaLastAppliedHeight(), hb); err != nil {
			return err
		}
		return tx.Set(kMetaAppliedBlock(height), []byte(blockID))
	})
}

func (s *DB) ensureNoCheckpointReadersLocked() error {
	s.cpRefMu.Lock()
	defer s.cpRefMu.Unlock()
	for height, entry := range s.cpCache {
		if entry != nil && entry.refCount > 0 {
			return fmt.Errorf("checkpoint %d is in use", height)
		}
		delete(s.cpCache, height)
	}
	return nil
}

func (s *DB) reopenLiveAfterInstallFailure(cause error, backupDir string, backupCreated bool) error {
	if s.store != nil {
		_ = s.store.Close()
		s.store = nil
	}
	_ = os.RemoveAll(s.liveDir)
	if backupCreated {
		_ = os.Rename(backupDir, s.liveDir)
	} else {
		_ = os.MkdirAll(s.liveDir, 0o755)
	}
	store, err := newPebbleStore(s.liveDir, s.conf.TableCacheShards, s.conf.BlockCacheSize)
	if err != nil {
		return fmt.Errorf("%w; rollback reopen failed: %v", cause, err)
	}
	s.store = store
	_ = s.rebuildLiveFingerprints()
	return cause
}
