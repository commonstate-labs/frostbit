                                
                                                 

package vm

import (
	"dex/keys"
	"dex/pb"
	"math/big"
	"testing"

	"google.golang.org/protobuf/proto"
)

                                  
func TestSlashBond(t *testing.T) {
	sv := NewMockStateView()

	                                       
	account := &pb.Account{
		Address: "0x0001",
	}
	accountKey := keys.KeyAccount("0x0001")
	accountData, _ := proto.Marshal(account)
	sv.Set(accountKey, accountData)

	                                 
	bal := &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{
			Balance:            "1000000000000000000000",           
			MinerLockedBalance: "0",
		},
	}
	balKey := keys.KeyBalance("0x0001", "FB")
	balData, _ := proto.Marshal(bal)
	sv.Set(balKey, balData)

	                    
	bondAmount, _ := new(big.Int).SetString("100000000000000000000", 10)          
	ops, err := slashBond(sv, "0x0001", bondAmount, "test false complaint")
	if err != nil {
		t.Fatalf("slashBond failed: %v", err)
	}

	if len(ops) != 1 {
		t.Fatalf("expected 1 WriteOp, got %d", len(ops))
	}

	                                                      
	updatedBal := GetBalance(sv, "0x0001", "FB")
	newBalance, _ := new(big.Int).SetString(updatedBal.Balance, 10)
	expectedBalance, _ := new(big.Int).SetString("900000000000000000000", 10)          
	if newBalance.Cmp(expectedBalance) != 0 {
		t.Fatalf("expected balance %s, got %s", expectedBalance.String(), newBalance.String())
	}
}

                                                  
func TestSlashMinerStake(t *testing.T) {
	sv := NewMockStateView()

	                                       
	account := &pb.Account{
		Address: "0x0002",
	}
	accountKey := keys.KeyAccount("0x0002")
	accountData, _ := proto.Marshal(account)
	sv.Set(accountKey, accountData)

	                                                   
	bal := &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{
			Balance:            "500000000000000000000",           
			MinerLockedBalance: "1000000000000000000000",                  
		},
	}
	balKey := keys.KeyBalance("0x0002", "FB")
	balData, _ := proto.Marshal(bal)
	sv.Set(balKey, balData)

	                                  
	ops, err := slashMinerStake(sv, "0x0002", 1, "test dealer fault")
	if err != nil {
		t.Fatalf("slashMinerStake failed: %v", err)
	}

	if len(ops) != 3 {
		t.Fatalf("expected hot stake, slash-pool, and forced-exit WriteOps, got %d", len(ops))
	}
	if _, exists, _ := sv.Get(keys.KeyMinerExitRecord("0x0002")); !exists {
		t.Fatal("slashed miner has no forced-exit record")
	}

	                                                         
	updatedBal := GetBalance(sv, "0x0002", "FB")
	if updatedBal.MinerLockedBalance != "0" {
		t.Fatalf("expected MinerLockedBalance to be 0, got %s", updatedBal.MinerLockedBalance)
	}

	                           
	if updatedBal.Balance != "500000000000000000000" {
		t.Fatalf("expected Balance to remain 500 FB, got %s", updatedBal.Balance)
	}
	pool := GetBalance(sv, DkgSlashPoolAccount, "FB")
	if pool.Balance != "1000000000000000000000" {
		t.Fatalf("expected slashed stake in protocol pool, got %s", pool.Balance)
	}
}

func TestSlashMinerStakeZeroStillRevokesMiner(t *testing.T) {
	sv := NewMockStateView()
	address := "zero-stake-dealer"
	accountData, _ := proto.Marshal(&pb.Account{Address: address, IsMiner: true})
	sv.Set(keys.KeyAccount(address), accountData)

	ops, err := slashMinerStake(sv, address, 1, "test zero stake dealer fault")
	if err != nil {
		t.Fatalf("slash zero-stake dealer: %v", err)
	}
	for _, op := range ops {
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, op.Value)
		}
	}
	raw, _, _ := sv.Get(keys.KeyAccount(address))
	var account pb.Account
	if err := proto.Unmarshal(raw, &account); err != nil || account.IsMiner {
		t.Fatalf("zero-stake dealer retained miner flag: is_miner=%v err=%v", account.IsMiner, err)
	}
}

func TestSlashMinerStakeMissingAccountFailsClosed(t *testing.T) {
	if _, err := slashMinerStake(NewMockStateView(), "missing-dealer", 1, "test"); err == nil {
		t.Fatal("missing dealer account must fail closed")
	}
}

                                                          
type MockStateView struct {
	data     map[string][]byte
	revision uint64
}

func NewMockStateView() *MockStateView {
	return &MockStateView{
		data: make(map[string][]byte),
	}
}

func (m *MockStateView) Get(key string) ([]byte, bool, error) {
	val, exists := m.data[key]
	return val, exists, nil
}

func (m *MockStateView) Set(key string, val []byte) {
	m.data[key] = val
	m.revision++
}

func (m *MockStateView) Del(key string) {
	delete(m.data, key)
	m.revision++
}

func (m *MockStateView) Snapshot() int {
	return 0
}

func (m *MockStateView) Revision() uint64 {
	return m.revision
}

func (m *MockStateView) Revert(snap int) error {
	m.revision++
	return nil
}

func (m *MockStateView) Diff() []WriteOp {
	return nil
}

func (m *MockStateView) Scan(prefix string) (map[string][]byte, error) {
	result := make(map[string][]byte)
	for k, v := range m.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			result[k] = v
		}
	}
	return result, nil
}

func (m *MockStateView) ScanWithLimit(prefix string, limit int) (map[string][]byte, error) {
	return m.Scan(prefix)
}

func (m *MockStateView) ScanWithLimitReverse(prefix string, limit int) (map[string][]byte, error) {
	return m.Scan(prefix)
}
