package vm

import (
	"dex/pb"
	"dex/witness"
	"fmt"
)

func quoteChallengeStakeFromState(sv StateView, svc *witness.Service, req *pb.RechargeRequest) (*witness.ChallengeStakeQuote, error) {
	if req == nil {
		return nil, fmt.Errorf("nil recharge request")
	}
	stakes := make([]string, 0, len(req.CaseCollateral))
	seen := make(map[string]bool)
	for _, bond := range req.CaseCollateral {
		if bond == nil || seen[bond.Address] || bond.RoleMask&WitnessCaseRoleOriginal == 0 {
			continue
		}
		seen[bond.Address] = true
		stakes = append(stakes, bond.OriginalAmount)
	}
	                                                                            
	                                                                           
	if len(req.CaseCollateral) == 0 {
		for _, address := range req.SelectedWitnesses {
			if address != "" && !seen[address] {
				seen[address] = true
				stakes = append(stakes, "1000000000")
			}
		}
	}
	cfg := witness.DefaultConfig()
	if svc != nil {
		cfg = svc.ConfigSnapshot()
	}
	return witness.CalculateChallengeStakeRequirement(cfg, stakes)
}
