package handlers

import (
	"dex/keys"
	"dex/pb"
	"dex/witness"
	"fmt"

	"google.golang.org/protobuf/proto"
)

func (hm *HandlerManager) assignedWitnessRequests(address string, history bool, limit int) ([]*pb.RechargeRequest, error) {
	prefix, after := keys.KeyWitnessAssignedPrefix(address), ""
	indexKey := keys.KeyWitnessAssigned
	if !history {
		prefix = keys.KeyWitnessAssignedCurrentPrefix(address)
		indexKey = keys.KeyWitnessAssignedCurrent
	}
	requests := make([]*pb.RechargeRequest, 0, limit)
	budget := limit + rechargeQueryRepairReadAllowance
	for budget > 0 && len(requests) < limit {
		want := min(budget, 128)
		rows, err := hm.dbManager.ScanKVWithLimitOrderedAfter(prefix, after, want, false)
		if err != nil {
			return nil, err
		}
		budget -= len(rows)
		for _, row := range rows {
			after = row.Key
			id := string(row.Value)
			if id == "" || row.Key != indexKey(address, id) {
				continue
			}
			raw, found, err := hm.frostResultGet(keys.KeyRechargeRequest(id))
			if err != nil {
				return nil, err
			}
			var req pb.RechargeRequest
			if !found || proto.Unmarshal(raw, &req) != nil || req.RequestId != id {
				continue
			}
			eligible := false
			if history {
				for _, member := range witness.RequestWitnessAssignments(&req) {
					if member == address {
						eligible = true
						break
					}
				}
			} else if req.Status == pb.RechargeRequestStatus_RECHARGE_PENDING || req.Status == pb.RechargeRequestStatus_RECHARGE_VOTING {
				for _, member := range req.SelectedWitnesses {
					if member == address {
						eligible = true
						break
					}
				}
			}
			if eligible {
				requests = append(requests, &req)
			}
			if len(requests) == limit {
				return requests, nil
			}
		}
		if len(rows) < want {
			return requests, nil
		}
	}
	return nil, fmt.Errorf("witness assignment query scan budget exhausted; index rebuild or narrower paging required")
}
