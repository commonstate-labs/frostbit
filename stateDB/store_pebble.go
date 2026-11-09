package statedb

import (
	"bytes"
	"errors"
	"io"

	"github.com/cockroachdb/pebble"
)

type pebbleReadable interface {
	Get(key []byte) ([]byte, io.Closer, error)
	NewIter(o *pebble.IterOptions) (*pebble.Iterator, error)
}

type pebbleStore struct {
	db *pebble.DB
}

func newPebbleStore(path string, tableCacheShards int, blockCacheSize int64) (kvStore, error) {
	opts := pebbleStoreOptions(false, tableCacheShards, blockCacheSize)
	if opts.Cache != nil {
		defer opts.Cache.Unref()
	}
	db, err := pebble.Open(path, opts)
	if err != nil {
		return nil, err
	}
	return &pebbleStore{db: db}, nil
}

func newPebbleStoreReadOnly(path string, tableCacheShards int, blockCacheSize int64) (kvStore, error) {
	opts := pebbleStoreOptions(true, tableCacheShards, blockCacheSize)
	if opts.Cache != nil {
		defer opts.Cache.Unref()
	}
	db, err := pebble.Open(path, opts)
	if err != nil {
		return nil, err
	}
	return &pebbleStore{db: db}, nil
}

func pebbleStoreOptions(readOnly bool, tableCacheShards int, blockCacheSize int64) *pebble.Options {
	opts := &pebble.Options{
		MaxOpenFiles: 500,
	}
	if readOnly {
		opts.ReadOnly = true
	}
	if tableCacheShards > 0 {
		opts.Experimental.TableCacheShards = tableCacheShards
	}
	if blockCacheSize <= 0 {
		blockCacheSize = 32 << 20
	}
	opts.Cache = pebble.NewCache(blockCacheSize)
	return opts
}

type pebbleReader struct {
	src pebbleReadable
}

func (r *pebbleReader) Get(key []byte) ([]byte, error) {
	v, closer, err := r.src.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, ErrKVNotFound
		}
		return nil, err
	}
	defer closer.Close()
	                                                                                                                        
	                                                                                                                        
	out := make([]byte, len(v))
	copy(out, v)
	return out, nil
}

func (r *pebbleReader) IteratePrefix(prefix []byte, startAfter []byte, fn func(key []byte, value []byte) error) error {
	opts := &pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	}
	iter, err := r.src.NewIter(opts)
	if err != nil {
		return err
	}
	defer iter.Close()

	seek := append([]byte(nil), prefix...)
	if len(startAfter) > 0 {
		seek = append(seek, startAfter...)
	}
	skipExact := len(startAfter) > 0

	for ok := iter.SeekGE(seek); ok; ok = iter.Next() {
		k := iter.Key()
		if skipExact && bytes.Equal(k, seek) {
			skipExact = false
			continue
		}
		skipExact = false

		key := append([]byte(nil), k...)
		val := append([]byte(nil), iter.Value()...)
		if err := fn(key, val); err != nil {
			return err
		}
	}
	return iter.Error()
}

type pebbleWriter struct {
	*pebbleReader
	batch *pebble.Batch
}

func (w *pebbleWriter) Set(key []byte, value []byte) error {
	return w.batch.Set(key, value, pebble.NoSync)
}

func (w *pebbleWriter) Delete(key []byte) error {
	return w.batch.Delete(key, pebble.NoSync)
}

func (s *pebbleStore) View(fn func(tx kvReader) error) error {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	return fn(&pebbleReader{src: snap})
}

func (s *pebbleStore) Get(key []byte) ([]byte, error) {
	v, closer, err := s.db.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, ErrKVNotFound
		}
		return nil, err
	}
	defer closer.Close()
	                                                           
	out := make([]byte, len(v))
	copy(out, v)
	return out, nil
}

func (s *pebbleStore) Update(fn func(tx kvWriter) error) error {
	batch := s.db.NewIndexedBatch()
	defer batch.Close()

	writer := &pebbleWriter{
		pebbleReader: &pebbleReader{src: batch},
		batch:        batch,
	}
	if err := fn(writer); err != nil {
		return err
	}
	return batch.Commit(pebble.NoSync)
}

func (s *pebbleStore) Checkpoint(destDir string, flushWAL bool) error {
	if flushWAL {
		return s.db.Checkpoint(destDir, pebble.WithFlushedWAL())
	}
	return s.db.Checkpoint(destDir)
}

func (s *pebbleStore) IsNotFound(err error) bool {
	return errors.Is(err, ErrKVNotFound) || errors.Is(err, pebble.ErrNotFound)
}

func (s *pebbleStore) Close() error {
	return s.db.Close()
}
