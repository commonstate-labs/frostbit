package db

import (
	"dex/keys"
	"dex/logs"
	"dex/pb"
	statedb "dex/stateDB"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/cockroachdb/pebble"
)

                                                                              
func (mgr *Manager) GetRandomMinersFast(k int) ([]*pb.Account, error) {
	if mgr == nil {
		return nil, fmt.Errorf("GetRandomMiners: db manager is nil")
	}
	if k <= 0 {
		return []*pb.Account{}, nil
	}
	miners, err := mgr.minerParticipantsSnapshot()
	if err != nil {
		return nil, err
	}

	if len(miners) == 0 {
		logs.Warn("[GetRandomMinersFast] no miners available, k=%d", k)
		return nil, fmt.Errorf("GetRandomMiners: no miners available")
	}
	if k > len(miners) {
		k = len(miners)
	}

	accounts := make([]*pb.Account, 0, k)
	mgr.minerSampleRandMu.Lock()
	if mgr.minerSampleRand == nil {
		mgr.minerSampleRand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	if k*2 >= len(miners) {
		perm := mgr.minerSampleRand.Perm(len(miners))
		for i := 0; i < k; i++ {
			accounts = append(accounts, miners[perm[i]])
		}
		mgr.minerSampleRandMu.Unlock()
		return accounts, nil
	}
	seen := make(map[int]struct{}, k)
	for len(accounts) < k {
		i := mgr.minerSampleRand.Intn(len(miners))
		if _, exists := seen[i]; exists {
			continue
		}
		seen[i] = struct{}{}
		accounts = append(accounts, miners[i])
	}
	mgr.minerSampleRandMu.Unlock()
	return accounts, nil
}

                                                                                   
func (mgr *Manager) GetMinerParticipantsSnapshot() ([]*pb.Account, error) {
	if mgr == nil {
		return nil, fmt.Errorf("GetMinerParticipantsSnapshot: db manager is nil")
	}
	return mgr.minerParticipantsSnapshot()
}

                                                                           
                                      
func (mgr *Manager) GetRandomGossipIPs(count int, excludeIP string) ([]string, error) {
	if mgr == nil {
		return nil, fmt.Errorf("GetRandomGossipIPs: db manager is nil")
	}
	if count <= 0 {
		return []string{}, nil
	}

	                                                                    
	miners, err := mgr.minerParticipantsSnapshot()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(miners))
	for _, m := range miners {
		if m != nil && m.Ip != "" && m.Ip != excludeIP {
			seen[m.Ip] = struct{}{}
		}
	}

	if len(seen) == 0 {
		return nil, fmt.Errorf("GetRandomGossipIPs: no peers available")
	}

	                    
	pool := make([]string, 0, len(seen))
	for ip := range seen {
		pool = append(pool, ip)
	}

	if count > len(pool) {
		count = len(pool)
	}

	                       
	mgr.minerSampleRandMu.Lock()
	if mgr.minerSampleRand == nil {
		mgr.minerSampleRand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	for i := len(pool) - 1; i > 0; i-- {
		j := mgr.minerSampleRand.Intn(i + 1)
		pool[i], pool[j] = pool[j], pool[i]
	}
	mgr.minerSampleRandMu.Unlock()

	return pool[:count], nil
}

                                                                                              
func (mgr *Manager) RefreshMinerParticipantsByHeight(height uint64) error {
	if mgr == nil {
		return fmt.Errorf("RefreshMinerParticipantsByHeight: db manager is nil")
	}
	return mgr.RefreshMinerParticipantsForEpoch(mgr.minerEpochByHeight(height))
}

                                                                                              
func (mgr *Manager) RefreshMinerParticipantsForEpoch(epoch uint64) error {
	if mgr == nil {
		return fmt.Errorf("RefreshMinerParticipantsForEpoch: db manager is nil")
	}
	for {
		mgr.minerCacheMu.RLock()
		if mgr.minerCacheReady && mgr.minerCacheEpoch >= epoch {
			mgr.minerCacheMu.RUnlock()
			return nil
		}
		generation := mgr.minerCacheGeneration
		mgr.minerCacheMu.RUnlock()

		miners, err := mgr.loadMinerParticipantsFromDB()
		if err != nil {
			return err
		}
		if !mgr.publishMinerParticipants(epoch, generation, miners) {
			continue
		}
		if mgr.minerCacheReadyForEpoch(epoch) {
			return nil
		}
	}
}

func (mgr *Manager) publishMinerParticipants(epoch, generation uint64, miners []*pb.Account) bool {
	mgr.minerCacheMu.Lock()
	defer mgr.minerCacheMu.Unlock()
	if mgr.minerCacheGeneration != generation {
		return false
	}
	if mgr.minerCacheReady && mgr.minerCacheEpoch >= epoch {
		return true
	}
	mgr.minerParticipants = miners
	mgr.minerCacheEpoch = epoch
	mgr.minerCacheReady = true
	logs.Debug("[MinerCache] refreshed epoch=%d miners=%d", epoch, len(miners))
	return true
}

func (mgr *Manager) minerCacheReadyForEpoch(epoch uint64) bool {
	mgr.minerCacheMu.RLock()
	defer mgr.minerCacheMu.RUnlock()
	return mgr.minerCacheReady && mgr.minerCacheEpoch >= epoch
}

func (mgr *Manager) ensureMinerCacheReady() error {
	mgr.minerCacheMu.RLock()
	ready := mgr.minerCacheReady
	mgr.minerCacheMu.RUnlock()
	if ready {
		return nil
	}
	height, err := mgr.GetLatestBlockHeight()
	if err != nil {
		return mgr.RefreshMinerParticipantsForEpoch(0)
	}
	return mgr.RefreshMinerParticipantsForEpoch(mgr.minerEpochByHeight(height))
}

                                                                             
                                                                         
                                                                               
                                                         
func (mgr *Manager) minerParticipantsSnapshot() ([]*pb.Account, error) {
	for {
		if err := mgr.ensureMinerCacheReady(); err != nil {
			return nil, err
		}
		mgr.minerIndexMu.RLock()
		mgr.minerCacheMu.RLock()
		if !mgr.minerCacheReady {
			mgr.minerCacheMu.RUnlock()
			mgr.minerIndexMu.RUnlock()
			continue
		}
		if len(mgr.minerParticipants) == 0 {
			mgr.minerCacheMu.RUnlock()
			mgr.minerIndexMu.RUnlock()
			return []*pb.Account{}, nil
		}
		out := make([]*pb.Account, len(mgr.minerParticipants))
		copy(out, mgr.minerParticipants)
		mgr.minerCacheMu.RUnlock()
		mgr.minerIndexMu.RUnlock()
		return out, nil
	}
}

func (mgr *Manager) minerEpochByHeight(height uint64) uint64 {
	epochBlocks := uint64(1)
	if mgr != nil && mgr.cfg != nil && mgr.cfg.Frost.Committee.EpochBlocks > 0 {
		epochBlocks = uint64(mgr.cfg.Frost.Committee.EpochBlocks)
	}
	return height / epochBlocks
}

                                                                                           
                                                                    
                                                                                         
func (mgr *Manager) InvalidateMinerCache() {
	if mgr == nil {
		return
	}
	mgr.minerCacheMu.Lock()
	mgr.minerCacheGeneration++
	mgr.minerCacheReady = false
	mgr.minerCacheMu.Unlock()
}

func (mgr *Manager) loadMinerParticipantsFromDB() ([]*pb.Account, error) {
	mgr.minerIndexMu.RLock()
	defer mgr.minerIndexMu.RUnlock()
	if mgr.IndexMgr == nil {
		return nil, fmt.Errorf("index manager is not initialized")
	}
	if mgr.Db == nil {
		return nil, fmt.Errorf("database is not initialized or closed")
	}

	indices := mgr.IndexMgr.SnapshotIndices()
	if len(indices) == 0 {
		return []*pb.Account{}, nil
	}

	snap := mgr.Db.NewSnapshot()
	defer snap.Close()

	miners := make([]*pb.Account, 0, len(indices))
	for _, idx := range indices {
		raw, closer, err := snap.Get([]byte(keys.KeyIndexToAccount(idx)))
		if err != nil {
			if errors.Is(err, pebble.ErrNotFound) {
				return nil, fmt.Errorf("miner index %d missing account mapping", idx)
			}
			return nil, err
		}
		accountKey := make([]byte, len(raw))
		copy(accountKey, raw)
		closer.Close()

		accountBytes, err := mgr.Get(string(accountKey))
		if err != nil {
			mgr.mu.RLock()
			stateReady := mgr.stateDB != nil
			mgr.mu.RUnlock()

			if !stateReady {
				                                                                      
				raw2, closer2, err2 := snap.Get(accountKey)
				if err2 != nil {
					if errors.Is(err2, pebble.ErrNotFound) {
						continue
					}
					return nil, err2
				}
				accountBytes = make([]byte, len(raw2))
				copy(accountBytes, raw2)
				closer2.Close()
			} else {
				if errors.Is(err, statedb.ErrNotFound) || errors.Is(err, pebble.ErrNotFound) {
					continue
				}
				return nil, err
			}
		}

		account := &pb.Account{}
		if err := ProtoUnmarshal(accountBytes, account); err != nil {
			logs.Warn("[MinerCache] failed to unmarshal account idx=%d: %v", idx, err)
			continue
		}
		if !account.IsMiner {
			continue
		}
		miners = append(miners, account)
	}
	return miners, nil
}
