package handlers

import (
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func (hm *HandlerManager) blockRewardAmount(height uint64, fallback string) string {
	if hm == nil || hm.dbManager == nil {
		return fallback
	}
	data, err := hm.dbManager.GetKV(keys.KeyBlockReward(height))
	if err != nil || len(data) == 0 {
		return fallback
	}
	var reward pb.BlockReward
	if err := proto.Unmarshal(data, &reward); err != nil || reward.BlockReward == "" {
		return fallback
	}
	return reward.BlockReward
}

func (hm *HandlerManager) cloneBlockWithReward(block *pb.Block) *pb.Block {
	if block == nil {
		return nil
	}
	clone, ok := proto.Clone(block).(*pb.Block)
	if !ok || clone == nil {
		return nil
	}
	if clone.Header != nil {
		clone.AccumulatedReward = hm.blockRewardAmount(clone.Header.Height, clone.AccumulatedReward)
	}
	return clone
}
