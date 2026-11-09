package vm

import (
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
)

                                                                                                       
                                                        
  
                                                      
  
                                                      
                                                                             
                                                                        
                                                              
                                                                                       
                                                                                           
  
                                                                                                         
                                                                                                        
                                                                                                     
                                                                                                                 
                                                
  
                                                                                             
                                                                                             
                                                          

type Bucket int

const (
	BucketUnspecified Bucket = iota
	BucketAvailable
	BucketOrderFrozen
	BucketMargin
	BucketInsurance
	BucketFeePool
	BucketExternal
	BucketMinerLocked                                                                   
	BucketWitnessLocked                                                               
)

func (b Bucket) String() string {
	switch b {
	case BucketAvailable:
		return "available"
	case BucketOrderFrozen:
		return "order_frozen"
	case BucketMargin:
		return "margin"
	case BucketInsurance:
		return "insurance"
	case BucketFeePool:
		return "fee_pool"
	case BucketExternal:
		return "external"
	case BucketMinerLocked:
		return "miner_locked"
	case BucketWitnessLocked:
		return "witness_locked"
	default:
		return "unspecified"
	}
}

                                                                                             
                                                                                                                     
                                                                                                 
type LedgerEntry struct {
	Height  uint64 `json:"height"`
	TxID    string `json:"tx_id,omitempty"`
	Reason  string `json:"reason"`
	Account string `json:"account"`
	Token   string `json:"token"`
	Amount  string `json:"amount"`
	From    Bucket `json:"from"`
	To      Bucket `json:"to"`
	Ref     string `json:"ref,omitempty"`
	Note    string `json:"note,omitempty"`
	                                                                                                              
	                                        
	Kind string `json:"kind,omitempty"`
}

                                                             
type LedgerFilter struct {
	Account   string
	TxID      string
	Reason    string
	MinHeight uint64
	MaxHeight uint64
}

                                                                                                     
                                                                                         
type MoveRequest struct {
	Account string
	Token   string
	Amount  *big.Int
	From    Bucket
	To      Bucket
	Reason  string
	Height  uint64
	TxID    string
	Ref     string
	Note    string
}

                                                
                                                                                              
                                                                                                                  
type Ledger interface {
	Post(LedgerEntry) error
	Move(MoveRequest) error
	Query(filter LedgerFilter) ([]LedgerEntry, error)
}

                                                 
type svLedger struct{ sv StateView }

                                                                                                     
func NewLedger(sv StateView) Ledger { return &svLedger{sv: sv} }

                                      
                                                                    
                                                                                                  
  
                                                                                                            
                                                                                 
func (l *svLedger) Post(e LedgerEntry) error {
	if e.Account == "" {
		return fmt.Errorf("ledger: account required")
	}
	if e.Token == "" {
		return fmt.Errorf("ledger: token required")
	}
	amt, ok := new(big.Int).SetString(e.Amount, 10)
	if !ok || amt.Sign() <= 0 {
		return fmt.Errorf("ledger: amount must be positive integer, got %q", e.Amount)
	}
	if e.From == BucketUnspecified && e.To == BucketUnspecified {
		return fmt.Errorf("ledger: at least one of from/to must be specified")
	}
	if e.From == e.To && e.From != BucketUnspecified {
		return fmt.Errorf("ledger: from == to is a no-op (%s)", e.From)
	}
	if e.Reason == "" {
		return fmt.Errorf("ledger: reason required")
	}

	                                                                        
	if e.Kind == "" {
		e.Kind = e.Reason
	}

	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("ledger: marshal entry failed: %w", err)
	}
	seq, err := nextUserActivitySeq(l.sv, e.Account, e.Height)
	if err != nil {
		return fmt.Errorf("ledger: allocate seq failed: %w", err)
	}
	writeUserActivity(l.sv, e.Account, e.Height, seq, data)
	return nil
}

                                                                 
  
                  
  
                                                                                                            
                                                                    
                                                                                                 
                                                                                                      
  
                  
  
                                                                            
                                                                            
                                                                                                          
                                                                                                            
                                                                                                   
                                                                                                       
                                                                       
                                                                       
  
                                                                                           
func (l *svLedger) Move(req MoveRequest) error {
	if req.Account == "" || req.Token == "" {
		return fmt.Errorf("ledger.Move: account/token required")
	}
	if req.Amount == nil || req.Amount.Sign() < 0 {
		return fmt.Errorf("ledger.Move: amount must be non-negative, got %v", req.Amount)
	}
	if req.Amount.Sign() == 0 {
		                                                                                                    
		                                                                                
		return nil
	}
	if req.From == req.To {
		return fmt.Errorf("ledger.Move: from == to (%s) is a no-op", req.From)
	}
	if req.From == BucketUnspecified || req.To == BucketUnspecified {
		return fmt.Errorf("ledger.Move: from/to must be specified")
	}
	if req.Reason == "" {
		return fmt.Errorf("ledger.Move: reason required")
	}

	                                 
	if isRealBucket(req.From) {
		if err := deductRealBucket(l.sv, req.Account, req.Token, req.From, req.Amount); err != nil {
			return err
		}
	}
	                               
	if isRealBucket(req.To) {
		if err := addRealBucket(l.sv, req.Account, req.Token, req.To, req.Amount); err != nil {
			return err
		}
	}

	                   
	return l.Post(LedgerEntry{
		Height: req.Height, TxID: req.TxID,
		Reason: req.Reason, Account: req.Account, Token: req.Token,
		Amount: req.Amount.String(),
		From:   req.From, To: req.To,
		Ref: req.Ref, Note: req.Note,
	})
}

                                                                                                                   
                                                               
func isRealBucket(b Bucket) bool {
	return b == BucketAvailable || b == BucketOrderFrozen || b == BucketMinerLocked || b == BucketWitnessLocked
}

                                                                                
func deductRealBucket(sv StateView, account, token string, bucket Bucket, amount *big.Int) error {
	bal, err := getBalanceStrict(sv, account, token)
	if err != nil {
		return fmt.Errorf("ledger.Move: load %s: %w", bucket, err)
	}
	cur, err := parseBucketField(bal, bucket)
	if err != nil {
		return err
	}
	if cur.Cmp(amount) < 0 {
		return fmt.Errorf("ledger.Move: insufficient %s: have %s need %s", bucket, cur, amount)
	}
	cur.Sub(cur, amount)
	setBucketField(bal, bucket, cur.String())
	SetBalance(sv, account, token, bal)
	return nil
}

                                   
func addRealBucket(sv StateView, account, token string, bucket Bucket, amount *big.Int) error {
	bal, err := getBalanceStrict(sv, account, token)
	if err != nil {
		return fmt.Errorf("ledger.Move: load %s: %w", bucket, err)
	}
	cur, err := parseBucketField(bal, bucket)
	if err != nil {
		return err
	}
	cur.Add(cur, amount)
	setBucketField(bal, bucket, cur.String())
	SetBalance(sv, account, token, bal)
	return nil
}

                                                                               
                                                                                              
                                                     
func parseBucketField(bal *pb.TokenBalance, bucket Bucket) (*big.Int, error) {
	var raw string
	switch bucket {
	case BucketAvailable:
		raw = bal.Balance
	case BucketOrderFrozen:
		raw = bal.OrderFrozenBalance
	case BucketMinerLocked:
		raw = bal.MinerLockedBalance
	case BucketWitnessLocked:
		return parseWitnessCollateralAmount("witness_locked_bucket", bal.WitnessLockedBalance)
	default:
		return new(big.Int), nil
	}
	if raw == "" {
		return new(big.Int), nil
	}
	v, err := parseBalanceStrict(bucket.String()+"_bucket", raw)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func setBucketField(bal *pb.TokenBalance, bucket Bucket, val string) {
	switch bucket {
	case BucketAvailable:
		bal.Balance = val
	case BucketOrderFrozen:
		bal.OrderFrozenBalance = val
	case BucketMinerLocked:
		bal.MinerLockedBalance = val
	case BucketWitnessLocked:
		bal.WitnessLockedBalance = val
	}
}

                                                                                    
                                                                     
func (l *svLedger) Query(filter LedgerFilter) ([]LedgerEntry, error) {
	if filter.Account == "" {
		return nil, fmt.Errorf("ledger: Query requires Account")
	}
	prefix := keys.KeyUserActivityPrefix(filter.Account)
	kvs, err := l.sv.Scan(prefix)
	if err != nil {
		return nil, err
	}
	sortedKeys := make([]string, 0, len(kvs))
	for k := range kvs {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	out := make([]LedgerEntry, 0, len(sortedKeys))
	for _, k := range sortedKeys {
		var e LedgerEntry
		if err := json.Unmarshal(kvs[k], &e); err != nil {
			continue                                                     
		}
		if filter.TxID != "" && e.TxID != filter.TxID {
			continue
		}
		if filter.Reason != "" && e.Reason != filter.Reason {
			continue
		}
		if filter.MinHeight > 0 && e.Height < filter.MinHeight {
			continue
		}
		if filter.MaxHeight > 0 && e.Height > filter.MaxHeight {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}
