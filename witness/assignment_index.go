package witness

import (
	"dex/keys"
	"dex/pb"
	"sort"
)

                                                                           
                                                                         
                                                                        
                                                                               
func RequestWitnessAssignments(req *pb.RechargeRequest) []string {
	if req == nil || req.Status == pb.RechargeRequestStatus_RECHARGE_FINALIZED || req.Status == pb.RechargeRequestStatus_RECHARGE_REJECTED {
		return nil
	}
	seen := make(map[string]struct{})
	add := func(address string) {
		if address != "" {
			seen[address] = struct{}{}
		}
	}
	for _, address := range req.SelectedWitnesses {
		add(address)
	}
	for _, entries := range [][]*pb.WitnessVote{req.Votes, req.OriginalPositions} {
		for _, vote := range entries {
			if vote != nil {
				add(vote.WitnessAddress)
			}
		}
	}
	for _, entry := range req.CaseCollateral {
		if entry != nil && entry.RoleMask&1 != 0 {
			add(entry.Address)
		}
	}
	addresses := make([]string, 0, len(seen))
	for address := range seen {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	return addresses
}

                                                                              
                                                                           
func RequestWitnessAssignmentKeys(req *pb.RechargeRequest) []string {
	addresses := RequestWitnessAssignments(req)
	selected := make(map[string]bool)
	if req != nil && (req.Status == pb.RechargeRequestStatus_RECHARGE_PENDING || req.Status == pb.RechargeRequestStatus_RECHARGE_VOTING) {
		for _, address := range req.SelectedWitnesses {
			selected[address] = true
		}
	}
	result := make([]string, 0, len(addresses)*2)
	for _, address := range addresses {
		result = append(result, keys.KeyWitnessAssigned(address, req.RequestId))
		if selected[address] {
			result = append(result, keys.KeyWitnessAssignedCurrent(address, req.RequestId))
		}
	}
	sort.Strings(result)
	return result
}
