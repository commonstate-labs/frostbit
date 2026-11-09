package db

import (
	"errors"
	"fmt"
	"strings"

	"dex/keys"
	statedb "dex/stateDB"

	"github.com/cockroachdb/pebble"
)

                                                                           
                                                                              
                                                                          
                                                                         
func (manager *Manager) RebuildReferralInviteeIndex() error {
	manager.referralIndexMu.Lock()
	defer manager.referralIndexMu.Unlock()
	return manager.rebuildReferralInviteeIndexLocked()
}

func (manager *Manager) rebuildReferralInviteeIndexLocked() (err error) {
	defer func() { manager.referralIndexErr = err }()
	if err = manager.ForceFlush(); err != nil {
		return err
	}
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return fmt.Errorf("referral index requires StateDB")
	}
	queryDB, err := manager.dbForStorageClass(keys.StorageQuery)
	if err != nil {
		return err
	}
	batch := queryDB.NewBatch()
	defer func() { _ = batch.Close() }()
	prefix := []byte(keys.KeyReferralInviteePrefix(""))
	if err = batch.DeleteRange(prefix, prefixUpperBound(prefix), pebble.NoSync); err != nil {
		return err
	}
	flush := func() error {
		if err := batch.Commit(pebble.Sync); err != nil {
			return err
		}
		if err := batch.Close(); err != nil {
			return err
		}
		batch = queryDB.NewBatch()
		return nil
	}
	err = stateStore.IterateLatestByPrefix(keys.KeyReferrerPrefix(), func(key string, value []byte) error {
		invitee, referrer := strings.TrimPrefix(key, keys.KeyReferrerPrefix()), string(value)
		if invitee == "" || referrer == "" {
			return fmt.Errorf("invalid authoritative referral binding %q", key)
		}
		if err := batch.Set([]byte(keys.KeyReferralInvitee(referrer, invitee)), []byte{1}, pebble.NoSync); err != nil {
			return err
		}
		if batch.Count() >= 1000 || len(batch.Repr()) >= 1<<20 {
			return flush()
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

                                                                           
                                                                            
func (manager *Manager) ReferralInfo(address string, limit int) (string, []string, error) {
	manager.referralIndexMu.RLock()
	defer manager.referralIndexMu.RUnlock()
	if manager.referralIndexErr != nil {
		return "", nil, fmt.Errorf("referral index unavailable: %w", manager.referralIndexErr)
	}
	if limit <= 0 || limit > 101 {
		limit = 101
	}
	referrer, err := manager.Get(keys.KeyReferrer(address))
	if err != nil && !errors.Is(err, statedb.ErrNotFound) && !errors.Is(err, pebble.ErrNotFound) {
		return "", nil, err
	}
	invitees, err := manager.scanKVKeysByPrefixOrderedAfter(keys.KeyReferralInviteePrefix(address), "", limit, false)
	return string(referrer), invitees, err
}
