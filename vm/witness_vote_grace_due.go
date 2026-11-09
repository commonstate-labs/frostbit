package vm

import (
	"dex/keys"
	"dex/pb"
	"fmt"
	"math"
	"sort"

	"google.golang.org/protobuf/proto"
)

                                                                             
                                                                                
                                                              
func (x *Executor) applyRechargeVoteGraceClosures(sv StateView, height, mix uint64) error {
	rows, err := sv.Scan(keys.KeyRechargeVoteGraceDuePrefix(height))
	if err != nil {
		return fmt.Errorf("scan recharge vote grace due index: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	indexKeys := make([]string, 0, len(rows))
	for key := range rows {
		indexKeys = append(indexKeys, key)
	}
	sort.Strings(indexKeys)

	challengePeriod := uint64(0)
	if x.WitnessService != nil {
		challengePeriod = x.WitnessService.ConfigSnapshot().ChallengePeriodBlocks
	}

	for _, indexKey := range indexKeys {
		requestID := string(rows[indexKey])
		if requestID == "" {
			sv.Del(indexKey)
			continue
		}
		req := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(requestID))
		if req == nil {
			sv.Del(indexKey)
			continue
		}
		if req.Status != pb.RechargeRequestStatus_RECHARGE_CONSENSUS_PASS &&
			req.Status != pb.RechargeRequestStatus_RECHARGE_CONSENSUS_FAIL {
			sv.Del(indexKey)
			continue
		}
		if req.VoteAcceptUntilHeight == 0 {
			sv.Del(indexKey)
			continue
		}
		if height < req.VoteAcceptUntilHeight {
			sv.Del(indexKey)
			sv.Set(keys.KeyRechargeVoteGraceDue(req.VoteAcceptUntilHeight, req.RequestId), []byte(req.RequestId))
			continue
		}

		updated := proto.Clone(req).(*pb.RechargeRequest)
		sv.Del(indexKey)
		switch req.Status {
		case pb.RechargeRequestStatus_RECHARGE_CONSENSUS_PASS:
			updated.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD
			updated.DeadlineHeight = addRechargeHeightSaturating(height, challengePeriod)
			if err := setRechargeRequestWithIndexes(sv, updated, mix); err != nil {
				return err
			}
		case pb.RechargeRequestStatus_RECHARGE_CONSENSUS_FAIL:
			updated.Status = pb.RechargeRequestStatus_RECHARGE_REJECTED
			updated.FinalizeHeight = height
			if err := applyRechargeRejected(sv, updated, mix); err != nil {
				return err
			}
		}

		if x.WitnessService != nil {
			x.WitnessService.LoadRequest(proto.Clone(updated).(*pb.RechargeRequest))
		}
	}
	return nil
}

func addRechargeHeightSaturating(height, blocks uint64) uint64 {
	if blocks > math.MaxUint64-height {
		return math.MaxUint64
	}
	return height + blocks
}
