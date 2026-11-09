package vm

import (
	"dex/keys"
	"dex/pb"
	"fmt"

	"github.com/shopspring/decimal"
)

                                                                    
func (x *Executor) IsBlockCommitted(height uint64) (bool, string) {
	                                                                           
	                                                                            
	                                                                     
	if reader, ok := x.DB.(finalizedStateBlockReader); ok {
		if blockID, exists, err := reader.FinalizedStateBlockID(height); err == nil && exists {
			return true, blockID
		}
	}
	return x.isRuntimeBlockCommitted(height)
}

func (x *Executor) isRuntimeBlockCommitted(height uint64) (bool, string) {
	blockID, err := x.DB.Get(keys.KeyVMCommitHeight(height))
	if err != nil || blockID == nil {
		return false, ""
	}
	return true, string(blockID)
}

                                                
func (x *Executor) GetTransactionStatus(txID string) (string, error) {
	key := keys.KeyVMAppliedTx(txID)
	status, err := x.DB.Get(key)
	if err != nil {
		return "", err
	}
	if status == nil {
		return "PENDING", nil
	}
	return string(status), nil
}

                            
func (x *Executor) CleanupCache(finalizedHeight uint64) {
	if finalizedHeight > 100 {
		                                            
		x.Cache.EvictBelow(finalizedHeight - 100)
	}
}

func (x *Executor) resolveBaseCommittedState(b *pb.Block) (uint64, string) {
	if b == nil || b.Header.Height == 0 {
		return 0, ""
	}
	parentHeight := b.Header.Height - 1
	if ok, hash := x.IsBlockCommitted(parentHeight); ok {
		return parentHeight, hash
	}
	return 0, ""
}

func (x *Executor) isCachedSpecUsable(b *pb.Block, cached *SpecResult) bool {
	if b == nil || cached == nil {
		return false
	}
	if !cached.Valid {
		return false
	}
	if cached.BlockID != b.BlockHash || cached.ParentID != b.Header.PrevBlockHash || cached.Height != b.Header.Height {
		return false
	}

	baseHeight, baseHash := x.resolveBaseCommittedState(b)
	if baseHeight == 0 && baseHash == "" {
		return cached.BaseCommittedHeight == 0 && cached.BaseCommittedHash == ""
	}
	return cached.BaseCommittedHeight == baseHeight && cached.BaseCommittedHash == baseHash
}

                                                                           
func calcStake(sv StateView, addr string) (decimal.Decimal, error) {
	fbBal := GetBalance(sv, addr, "FB")
	ml, err := decimal.NewFromString(fbBal.MinerLockedBalance)
	if err != nil {
		ml = decimal.Zero
	}
	return ml, nil
}

                                            
func ValidateBlock(b *pb.Block) error {
	if b == nil {
		return ErrNilBlock
	}
	if b.BlockHash == "" {
		return fmt.Errorf("empty block hash")
	}
	if b.Header.Height == 0 && b.Header.PrevBlockHash != "" {
		return fmt.Errorf("genesis block should not have parent")
	}
	if b.Header.Height > 0 && b.Header.PrevBlockHash == "" {
		return fmt.Errorf("non-genesis block should have parent")
	}
	return nil
}
