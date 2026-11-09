package txpool

import (
	"dex/db"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/rent"
	statedb "dex/stateDB"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"
)

var ErrPendingAdmission = errors.New("txpool admission rejected")

                                                                                                         
type pendingAdmission struct {
	owner         string
	nonce         uint64
	fee           *big.Int
	bytes         int64
	expires       time.Time
	accountNonce  bool
	localProtocol bool
}

type pendingAccount struct {
	txs     map[string]*pendingAdmission
	nonces  map[uint64]string
	fees    big.Int
	bytes   int64
	version uint64
}

type admissionState struct {
	nonce   uint64
	balance *big.Int
}

type admissionRentStore struct{ manager *db.Manager }

func (s admissionRentStore) Get(key string) ([]byte, bool, error) {
	if s.manager == nil {
		return nil, false, nil
	}
	data, err := s.manager.Get(key)
	if errors.Is(err, pebble.ErrNotFound) || errors.Is(err, statedb.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (s admissionRentStore) Scan(prefix string, limit int) (map[string][]byte, error) {
	out := make(map[string][]byte)
	if s.manager == nil {
		return out, nil
	}
	rows, err := s.manager.ScanByPrefix(prefix, limit)
	if err != nil {
		return nil, err
	}
	for key, value := range rows {
		out[key] = []byte(value)
	}
	return out, nil
}

func (p *TxPool) readAdmissionState(owner string) (admissionState, error) {
	state := admissionState{balance: new(big.Int)}
	raw, err := p.dbManager.Get(keys.KeyAccount(owner))
	if err != nil && !errors.Is(err, pebble.ErrNotFound) && !errors.Is(err, statedb.ErrNotFound) {
		return state, err
	}
	if len(raw) > 0 {
		var account pb.Account
		if err := proto.Unmarshal(raw, &account); err != nil {
			return state, err
		}
		state.nonce = account.Nonce
	}
	raw, err = p.dbManager.Get(keys.KeyBalance(owner, "FB"))
	if err != nil && !errors.Is(err, pebble.ErrNotFound) && !errors.Is(err, statedb.ErrNotFound) {
		return state, err
	}
	if len(raw) > 0 {
		var record pb.TokenBalanceRecord
		if err := proto.Unmarshal(raw, &record); err != nil {
			return state, err
		}
		if record.Balance != nil && record.Balance.Balance != "" {
			if _, ok := state.balance.SetString(record.Balance.Balance, 10); !ok || state.balance.Sign() < 0 {
				return state, fmt.Errorf("invalid FB balance")
			}
		}
	}
	if rent.IsRentManagedAccount(owner) {
		                                                                       
		                                                                        
		                                                                          
		projectionHeight := p.dbManager.GetCurrentHeight()
		if projectionHeight < ^uint64(0) {
			projectionHeight++
		}
		projection, err := rent.NewService(admissionRentStore{manager: p.dbManager}, rent.WithClaimMarkerFastPath()).ProjectRent(
			owner, projectionHeight, rent.DefaultPolicy(),
		)
		if err != nil {
			return state, err
		}
		if _, ok := state.balance.SetString(projection.SpendableFB, 10); !ok || state.balance.Sign() < 0 {
			return state, fmt.Errorf("invalid projected spendable FB")
		}
	}
	return state, nil
}

                                                                                                                        
func (p *TxPool) admitPendingLocked(tx *pb.AnyTx, state admissionState, now time.Time) (*pendingAdmission, error) {
	base := tx.GetBase()
	if base == nil || base.FromAddress == "" {
		return nil, fmt.Errorf("%w: missing sender", ErrPendingAdmission)
	}
	fee, ok := new(big.Int).SetString(base.Fee, 10)
	if !ok || fee.Cmp(big.NewInt(protocol.MinRuntimeFeeRaw)) < 0 {
		return nil, fmt.Errorf("%w: invalid runtime fee", ErrPendingAdmission)
	}
	entry := &pendingAdmission{owner: base.FromAddress, nonce: base.Nonce, fee: fee, bytes: int64(proto.Size(tx)), expires: now.Add(p.cfg.TxPool.TxExpirationTime), accountNonce: pb.TxRequiresAccountNonce(tx)}
	entry.localProtocol = !entry.accountNonce && p.address != "" && base.FromAddress == p.address
	if entry.accountNonce && (base.Nonce < state.nonce || base.Nonce-state.nonce > p.cfg.TxPool.MaxFutureNonceGap || base.Nonce == ^uint64(0)) {
		return nil, fmt.Errorf("%w: nonce outside pending window", ErrPendingAdmission)
	}
	limit := p.MaxPendingLimit()
	reserve := limit / 10
	if reserve > 256 {
		reserve = 256
	}
	count := p.pendingAnyTxCache.Len()
	if limit > 0 && (count >= limit || (!entry.localProtocol && count-p.localProtocolPending >= limit-reserve)) {
		return nil, fmt.Errorf("txpool pending is full (%d/%d)", count, limit)
	}
	byteLimit := p.cfg.TxPool.MaxPendingBytes
	if !entry.localProtocol {
		byteLimit -= byteLimit / 10
	}
	if entry.bytes > byteLimit-p.pendingBytes {
		return nil, fmt.Errorf("%w: pending byte budget full", ErrPendingAdmission)
	}
	account := p.pendingAccounts[entry.owner]
	if account != nil {
		if entry.accountNonce {
			if _, exists := account.nonces[entry.nonce]; exists {
				return nil, fmt.Errorf("%w: account nonce already pending", ErrPendingAdmission)
			}
		}
		if !entry.localProtocol && (len(account.txs) >= p.cfg.TxPool.MaxPendingPerAccount || entry.bytes > p.cfg.TxPool.MaxPendingAccountBytes-account.bytes) {
			return nil, fmt.Errorf("%w: account pending budget full", ErrPendingAdmission)
		}
	}
	if !entry.localProtocol && entry.bytes > p.cfg.TxPool.MaxPendingAccountBytes {
		return nil, fmt.Errorf("%w: account byte budget full", ErrPendingAdmission)
	}
	required := new(big.Int).Set(fee)
	if account != nil && !entry.localProtocol {
		required.Add(required, &account.fees)
	}
	if required.Cmp(state.balance) > 0 {
		return nil, fmt.Errorf("%w: insufficient available FB for pending fees", ErrPendingAdmission)
	}
	return entry, nil
}

func (p *TxPool) trackAdmissionLocked(txID string, entry *pendingAdmission) {
	account := p.pendingAccounts[entry.owner]
	if account == nil {
		account = &pendingAccount{txs: make(map[string]*pendingAdmission), nonces: make(map[uint64]string)}
		p.pendingAccounts[entry.owner] = account
	}
	account.txs[txID] = entry
	account.version++
	if entry.accountNonce {
		account.nonces[entry.nonce] = txID
	}
	account.fees.Add(&account.fees, entry.fee)
	account.bytes += entry.bytes
	p.pendingAdmissions[txID] = entry
	p.pendingBytes += entry.bytes
	if entry.localProtocol {
		p.localProtocolPending++
	}
}

                                                                                                      
func (p *TxPool) removePendingLocked(txID string) {
	p.pendingAnyTxCache.Remove(txID)
	p.untrackExecutionGuardUnlocked(txID)
	if len(txID) >= 18 {
		p.shortPendingAnyTxCache.Remove(txID[2:18])
	}
	entry := p.pendingAdmissions[txID]
	if entry == nil {
		return
	}
	delete(p.pendingAdmissions, txID)
	p.pendingBytes -= entry.bytes
	if entry.localProtocol {
		p.localProtocolPending--
	}
	account := p.pendingAccounts[entry.owner]
	account.version++
	delete(account.txs, txID)
	if entry.accountNonce {
		delete(account.nonces, entry.nonce)
	}
	account.fees.Sub(&account.fees, entry.fee)
	account.bytes -= entry.bytes
	if len(account.txs) == 0 {
		delete(p.pendingAccounts, entry.owner)
	}
}

                                                                                             
func (p *TxPool) prunePendingAccount(owner string, state admissionState, now time.Time) {
	p.mu.RLock()
	account := p.pendingAccounts[owner]
	if account == nil {
		p.mu.RUnlock()
		return
	}
	ids := make([]string, 0, len(account.txs))
	version := account.version
	entries := make(map[string]*pendingAdmission, len(account.txs))
	for id, entry := range account.txs {
		ids = append(ids, id)
		entries[id] = entry
	}
	p.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool {
		a, b := entries[ids[i]], entries[ids[j]]
		if a.localProtocol != b.localProtocol {
			return a.localProtocol
		}
		if a.nonce != b.nonce {
			return a.nonce < b.nonce
		}
		return ids[i] < ids[j]
	})
	fees := new(big.Int)
	for _, id := range ids {
		entry := entries[id]
		invalid := !now.Before(entry.expires) || (entry.accountNonce && (entry.nonce < state.nonce || entry.nonce-state.nonce > p.cfg.TxPool.MaxFutureNonceGap))
		required := new(big.Int).Add(fees, entry.fee)
		if entry.localProtocol {
			required.Set(entry.fee)
		}
		if required.Cmp(state.balance) > 0 {
			invalid = true
		}
		if !invalid {
			fees.Add(fees, entry.fee)
			continue
		}
		pLock := p.pendingPersistenceLock(id)
		pLock.Lock()
		p.mu.Lock()
		                                                                      
		                                                                         
		if p.pendingAccounts[owner] != account || account.version != version {
			p.mu.Unlock()
			pLock.Unlock()
			return
		}
		removed := p.pendingAdmissions[id] == entry
		if removed {
			p.removePendingLocked(id)
			version = account.version
		}
		p.mu.Unlock()
		if removed {
			_ = p.dbManager.DeletePendingAnyTx(id)
		}
		pLock.Unlock()
	}
}

func (p *TxPool) maintainPending(now time.Time) {
	p.mu.RLock()
	owners := make([]string, 0, len(p.pendingAccounts))
	for owner := range p.pendingAccounts {
		owners = append(owners, owner)
	}
	p.mu.RUnlock()
	sort.Strings(owners)
	                                                                                                  
	if len(owners) == 0 {
		p.maintenanceCursor = 0
		return
	}
	count := min(64, len(owners))
	for i := 0; i < count; i++ {
		owner := owners[(p.maintenanceCursor+i)%len(owners)]
		state, err := p.readAdmissionState(owner)
		if err == nil {
			p.prunePendingAccount(owner, state, now)
		}
	}
	p.maintenanceCursor = (p.maintenanceCursor + count) % len(owners)
}

func (p *TxPool) runPendingMaintenance() {
	defer p.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopChan:
			return
		case now := <-ticker.C:
			p.maintainPending(now)
		}
	}
}
