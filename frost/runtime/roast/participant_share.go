                                           
                                                                                        

package roast

import (
	"dex/logs"
)

                                    
func (p *Participant) SetShare(chain string, vaultID uint32, epoch uint64, share []byte) {
	if len(share) == 0 {
		return
	}
	shareCopy := cloneBytes(share)

	p.mu.Lock()
	key := shareKey(chain, vaultID, epoch)
	p.shares[key] = shareCopy
	store := p.shareStore
	p.mu.Unlock()
	logs.Debug("[Participant] set local share node=%s chain=%s vault=%d epoch=%d key=%s share_fp=%s",
		p.nodeID, chain, vaultID, epoch, key, shareFingerprintForLog(shareCopy))

	if store == nil {
		logs.Warn("[Participant] local share store is nil node=%s chain=%s vault=%d epoch=%d key=%s",
			p.nodeID, chain, vaultID, epoch, key)
		return
	}
	if err := store.SaveLocalShare(chain, vaultID, epoch, shareCopy); err != nil {
		p.Logger.Warn("[Participant] failed to persist local share for %s/%d/%d: %v", chain, vaultID, epoch, err)
		return
	}
	logs.Debug("[Participant] persisted local share node=%s chain=%s vault=%d epoch=%d key=%s share_fp=%s",
		p.nodeID, chain, vaultID, epoch, key, shareFingerprintForLog(shareCopy))
}

                                                                                                                 
func (p *Participant) GetShare(chain string, vaultID uint32, epoch uint64) []byte {
	p.mu.RLock()
	key := shareKey(chain, vaultID, epoch)
	cached := cloneBytes(p.shares[key])
	store := p.shareStore
	p.mu.RUnlock()

	if store != nil {
		if persisted, err := store.LoadLocalShare(chain, vaultID, epoch); err == nil && len(persisted) > 0 {
			persistedCopy := cloneBytes(persisted)
			p.mu.Lock()
			p.shares[key] = persistedCopy
			p.mu.Unlock()
			logs.Debug("[Participant] loaded local share from store node=%s chain=%s vault=%d epoch=%d key=%s persisted_fp=%s cache_fp=%s",
				p.nodeID, chain, vaultID, epoch, key, shareFingerprintForLog(persistedCopy), shareFingerprintForLog(cached))
			return persistedCopy
		} else if err != nil {
			logs.Warn("[Participant] load local share failed node=%s chain=%s vault=%d epoch=%d key=%s err=%v cache_fp=%s",
				p.nodeID, chain, vaultID, epoch, key, err, shareFingerprintForLog(cached))
		} else {
			logs.Debug("[Participant] local share not found in store node=%s chain=%s vault=%d epoch=%d key=%s cache_fp=%s",
				p.nodeID, chain, vaultID, epoch, key, shareFingerprintForLog(cached))
		}
	}

	if len(cached) > 0 {
		logs.Debug("[Participant] using cached local share node=%s chain=%s vault=%d epoch=%d key=%s share_fp=%s",
			p.nodeID, chain, vaultID, epoch, key, shareFingerprintForLog(cached))
	} else {
		logs.Warn("[Participant] local share missing node=%s chain=%s vault=%d epoch=%d key=%s",
			p.nodeID, chain, vaultID, epoch, key)
	}

	return cached
}
