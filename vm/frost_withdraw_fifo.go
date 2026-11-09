package vm

import (
	"dex/frost/chain"
	"dex/keys"
	"fmt"
	"strconv"
	"strings"
)

func validateWithdrawFIFOEntry(sv StateView, withdraw *FrostWithdrawRequest) error {
	if withdraw == nil || withdraw.Seq == 0 {
		return fmt.Errorf("invalid withdraw FIFO sequence")
	}
	chainName := chain.NormalizeChain(withdraw.Chain)
	asset := strings.TrimSpace(withdraw.Asset)
	if chainName == "" || asset == "" || strings.TrimSpace(withdraw.WithdrawID) == "" {
		return fmt.Errorf("invalid withdraw FIFO identity")
	}
	if chainName != chain.ChainBTC {
		if withdraw.LiveSeq == 0 || withdraw.Seq != withdraw.LiveSeq {
			return fmt.Errorf("invalid account withdraw live sequence")
		}
		indexData, exists, err := sv.Get(keys.KeyFrostWithdrawLiveIndex(chainName, withdraw.VaultID, withdraw.LiveSeq))
		if err != nil {
			return fmt.Errorf("read account withdraw live index: %w", err)
		}
		if !exists || string(indexData) != withdraw.WithdrawID {
			return fmt.Errorf("account withdraw live index mismatch at seq %d", withdraw.LiveSeq)
		}
		return nil
	}
	tail, err := readWithdrawQueueUint64(sv, keys.KeyFrostWithdrawFIFOSeq(chainName, asset))
	if err != nil {
		return fmt.Errorf("read withdraw FIFO tail: %w", err)
	}
	if withdraw.Seq > tail {
		return fmt.Errorf("withdraw FIFO sequence %d exceeds tail %d", withdraw.Seq, tail)
	}
	indexData, exists, err := sv.Get(keys.KeyFrostWithdrawFIFOIndex(chainName, asset, withdraw.Seq))
	if err != nil {
		return fmt.Errorf("read withdraw FIFO index %d: %w", withdraw.Seq, err)
	}
	if !exists || string(indexData) != withdraw.WithdrawID {
		return fmt.Errorf("withdraw FIFO index mismatch at seq %d", withdraw.Seq)
	}
	return nil
}

                                                                              
                                                                               
                                                                                
                                                                         
                                         
  
                                                                          
                                                                    
func resolveCancelledWithdrawsFrom(sv StateView, chainName, asset string, start uint64, override *FrostWithdrawRequest) (uint64, []WriteOp, bool, error) {
	chainName = chain.NormalizeChain(chainName)
	asset = strings.TrimSpace(asset)
	if start == 0 {
		start = 1
	}

	tail, err := readWithdrawQueueUint64(sv, keys.KeyFrostWithdrawFIFOSeq(chainName, asset))
	if err != nil {
		return 0, nil, false, err
	}
	if start > tail {
		return start, nil, false, nil
	}

	head := start
	ops := make([]WriteOp, 0)
	overrideCrossed := false
	for head <= tail {
		indexKey := keys.KeyFrostWithdrawFIFOIndex(chainName, asset, head)
		withdrawIDData, exists, err := sv.Get(indexKey)
		if err != nil {
			return 0, nil, false, fmt.Errorf("read withdraw FIFO index %d: %w", head, err)
		}
		if !exists || len(withdrawIDData) == 0 {
			return 0, nil, false, fmt.Errorf("withdraw FIFO index missing at seq %d", head)
		}
		withdrawID := string(withdrawIDData)

		var withdraw *FrostWithdrawRequest
		if override != nil && override.WithdrawID == withdrawID {
			copyValue := *override
			withdraw = &copyValue
		} else {
			withdrawData, exists, err := sv.Get(keys.KeyFrostWithdraw(withdrawID))
			if err != nil {
				return 0, nil, false, fmt.Errorf("read withdraw %s: %w", withdrawID, err)
			}
			if !exists || len(withdrawData) == 0 {
				return 0, nil, false, fmt.Errorf("withdraw state missing at seq %d", head)
			}
			withdraw, err = unmarshalWithdrawRequest(withdrawData)
			if err != nil {
				return 0, nil, false, fmt.Errorf("parse withdraw %s: %w", withdrawID, err)
			}
		}

		if withdraw.WithdrawID != withdrawID || withdraw.Seq != head || chain.NormalizeChain(withdraw.Chain) != chainName || !strings.EqualFold(withdraw.Asset, asset) {
			return 0, nil, false, fmt.Errorf("withdraw FIFO state mismatch at seq %d", head)
		}
		if withdraw.Status != WithdrawStatusCancelled {
			break
		}

		cancelledData, err := marshalWithdrawRequest(withdraw)
		if err != nil {
			return 0, nil, false, fmt.Errorf("marshal cancelled withdraw %s: %w", withdrawID, err)
		}
		ops = append(ops,
			archiveWriteOp(keys.KeyArchiveFrostWithdraw(withdrawID), cancelledData, "archive_frost_withdraw"),
			archiveWriteOp(keys.KeyArchiveFrostWithdrawDone(chainName, asset, withdraw.Seq, withdrawID), []byte(withdrawID), "archive_frost_withdraw_done"),
			deleteWriteOp(keys.KeyFrostWithdraw(withdrawID), "frost_withdraw_cancel_prune"),
			deleteWriteOp(indexKey, "frost_withdraw_fifo_prune"),
		)
		if override != nil && override.WithdrawID == withdrawID {
			overrideCrossed = true
		}
		head++
	}
	return head, ops, overrideCrossed, nil
}

func readWithdrawQueueUint64(sv StateView, key string) (uint64, error) {
	data, exists, err := sv.Get(key)
	if err != nil {
		return 0, err
	}
	if !exists || len(data) == 0 {
		return 0, nil
	}
	value, err := strconv.ParseUint(string(data), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid uint64 state %s: %w", key, err)
	}
	return value, nil
}
