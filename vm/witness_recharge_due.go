package vm

import (
	"dex/keys"
	"dex/pb"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
)

func (x *Executor) applyRechargeChallengeFinalizations(sv StateView, height, mix uint64) (resultErr error) {
	snap := sv.Snapshot()
	defer func() {
		if resultErr != nil {
			_ = sv.Revert(snap)
		}
	}()
	rows, err := sv.Scan(keys.KeyRechargeChallengeDuePrefix(height))
	if err != nil || len(rows) == 0 {
		if err != nil {
			return fmt.Errorf("scan recharge challenge due index: %w", err)
		}
		return nil
	}
	indexKeys := make([]string, 0, len(rows))
	for k := range rows {
		indexKeys = append(indexKeys, k)
	}
	sort.Strings(indexKeys)

	for _, indexKey := range indexKeys {
		requestID := string(rows[indexKey])
		if requestID == "" {
			sv.Del(indexKey)
			continue
		}
		requestKey := keys.KeyRechargeRequest(requestID)
		req := loadRechargeRequestAtKey(sv, requestKey)
		if req == nil {
			sv.Del(indexKey)
			continue
		}
		if req.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD {
			sv.Del(indexKey)
			continue
		}
		finalizeHeight := rechargeChallengeFinalizeHeight(req)
		if finalizeHeight == 0 {
			sv.Del(indexKey)
			continue
		}
		if height < finalizeHeight {
			sv.Del(indexKey)
			sv.Set(keys.KeyRechargeChallengeDue(finalizeHeight, req.RequestId), []byte(req.RequestId))
			continue
		}
		_, active, err := rechargeActivatedChallengeInState(sv, req)
		if err != nil {
			return err
		}
		if active {
			updated := proto.Clone(req).(*pb.RechargeRequest)
			updated.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGED
			if err := setRechargeRequestWithIndexes(sv, updated, mix); err != nil {
				return err
			}
			sv.Del(indexKey)
			continue
		}

		if err := refundUnfundedRechargeChallenge(sv, req, height, mix); err != nil {
			return err
		}
		finalized := proto.Clone(req).(*pb.RechargeRequest)
		finalized.FinalizeHeight = height
		if err := applyRechargeFinalized(sv, finalized, height, mix); err != nil {
			return err
		}
		sv.Del(indexKey)
		current := loadRechargeRequestAtKey(sv, requestKey)
		if current == nil {
			continue
		}
		if current.Status == pb.RechargeRequestStatus_RECHARGE_FINALIZED {
			continue
		}
		if current.Status == pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD && height != ^uint64(0) {
			sv.Set(keys.KeyRechargeChallengeDue(height+1, current.RequestId), []byte(current.RequestId))
		}
	}
	return nil
}

func rechargeActivatedChallengeInState(sv StateView, req *pb.RechargeRequest) (*pb.ChallengeRecord, bool, error) {
	if req == nil || req.ChallengeId == "" {
		return nil, false, nil
	}
	data, exists, err := sv.Get(keys.KeyChallengeRecord(req.ChallengeId))
	if err != nil || !exists || len(data) == 0 {
		return nil, false, err
	}
	var challenge pb.ChallengeRecord
	if err := unmarshalProtoCompat(data, &challenge); err != nil {
		return nil, false, fmt.Errorf("parse recharge challenge record %s: %w", req.ChallengeId, err)
	}
	if challenge.Finalized || challenge.Status == pb.ChallengeStatus_CHALLENGE_FINALIZED {
		return &challenge, false, nil
	}
	return &challenge, challenge.Status == pb.ChallengeStatus_CHALLENGE_ACTIVE || (challenge.Status == pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION && challenge.SelectionHeight > 0), nil
}
