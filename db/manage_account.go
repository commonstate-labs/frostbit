package db

import (
	"dex/keys"
	"dex/pb"
	"errors"
	"strings"

	"github.com/cockroachdb/pebble"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
)

                                                
  
                                                   
func (mgr *Manager) SaveAccount(account *pb.Account) error {
	key := keys.KeyAccount(account.Address)
	data, err := ProtoMarshal(account)
	if err != nil {
		return err
	}
	mgr.EnqueueSet(key, string(data))
	return nil
}

                                                    
func (mgr *Manager) GetAccount(address string) (*pb.Account, error) {
	key := keys.KeyAccount(address)
	val, err := mgr.Read(key)
	if err != nil {
		return nil, err
	}
	account := &pb.Account{}
	if err := ProtoUnmarshal([]byte(val), account); err != nil {
		return nil, err
	}
	return account, nil
}

                                     
func (mgr *Manager) CalcStake(addr string) (decimal.Decimal, error) {
	balKey := keys.KeyBalance(addr, "FB")
	val, err := mgr.Read(balKey)
	if err != nil || val == "" {
		return decimal.Zero, nil
	}
	var record pb.TokenBalanceRecord
	if err := ProtoUnmarshal([]byte(val), &record); err != nil || record.Balance == nil {
		return decimal.Zero, nil
	}
	ml, err := decimal.NewFromString(record.Balance.MinerLockedBalance)
	if err != nil {
		ml = decimal.Zero
	}
	return ml, nil
}

                                           
func (mgr *Manager) UpdateStakeIndex(oldStake, newStake decimal.Decimal, address string) error {
	oldKey := buildStakeIndexKey(oldStake, address)
	newKey := buildStakeIndexKey(newStake, address)
	if oldKey != newKey {
		_ = mgr.DeleteKey(oldKey)
		mgr.EnqueueSet(newKey, "")
	}
	return nil
}

var maxStake, _ = decimal.NewFromString("1e30")

func buildStakeIndexKey(stake decimal.Decimal, address string) string {
	if stake.Cmp(decimal.Zero) < 0 {
		stake = decimal.Zero
	}
	inv := maxStake.Sub(stake)
	if inv.Cmp(decimal.Zero) < 0 {
		inv = decimal.Zero
	}
	invStr := inv.String()
	padNeeded := 32 - len(invStr)
	if padNeeded < 0 {
		padNeeded = 0
	}
	return keys.KeyStakeIndex(strings.Repeat("0", padNeeded)+invStr, address)
}

func (mgr *Manager) getAccountByIndex(idx uint64) (*pb.Account, error) {
	indexKey := []byte(keys.KeyIndexToAccount(idx))

	runtimeDB, err := mgr.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return nil, err
	}
	raw, closer, err := runtimeDB.Get(indexKey)
	if err != nil {
		return nil, err
	}
	addrBytes := make([]byte, len(raw))
	copy(addrBytes, raw)
	closer.Close()

	accountBytes, err := mgr.Get(string(addrBytes))
	if err != nil {
		return nil, err
	}

	var acc pb.Account
	if err := proto.Unmarshal(accountBytes, &acc); err != nil {
		return nil, err
	}
	return &acc, nil
}

                               
func (mgr *Manager) GetToken(tokenAddress string) (*pb.Token, error) {
	key := keys.KeyToken(tokenAddress)
	val, err := mgr.Read(key)
	if err != nil {
		return nil, err
	}
	if val == "" {
		return nil, nil
	}
	token := &pb.Token{}
	if err := ProtoUnmarshal([]byte(val), token); err != nil {
		return nil, err
	}
	return token, nil
}

                                          
func (mgr *Manager) GetTokenRegistry() (*pb.TokenRegistry, error) {
	key := keys.KeyTokenRegistry()
	val, err := mgr.Read(key)
	if err != nil {
		return nil, err
	}
	if val == "" {
		return nil, nil
	}
	registry := &pb.TokenRegistry{}
	if err := ProtoUnmarshal([]byte(val), registry); err != nil {
		return nil, err
	}
	return registry, nil
}

                         
var _ = errors.Is
var _ = pebble.ErrNotFound
