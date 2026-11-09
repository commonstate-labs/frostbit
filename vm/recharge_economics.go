package vm

import (
	"fmt"

	"dex/pb"
	"dex/protocol"
)

                                                                             
                                                                            
func networkChainIDFromView(sv StateView) uint64 {
	if bound, ok := sv.(interface{ NetworkChainID() uint64 }); ok {
		return bound.NetworkChainID()
	}
	return 0
}

func validateRechargeEconomicsPolicy(sv StateView, req *pb.RechargeRequest) error {
	if req == nil {
		return fmt.Errorf("missing recharge request economics")
	}
	chainID := networkChainIDFromView(sv)
	if protocol.WitnessRechargeEconomicsActive(chainID) {
		if req.EconomicsPolicy != protocol.RechargeEconomicsLaunchV1 ||
			req.RequesterBondAmount != "1000000000" ||
			req.RequesterBondStatus == pb.RequesterBondStatus_REQUESTER_BOND_NONE {
			return fmt.Errorf("51025 recharge policy/bond mismatch: request=%s policy=%d bond=%q status=%s", req.RequestId, req.EconomicsPolicy, req.RequesterBondAmount, req.RequesterBondStatus)
		}
		switch req.Status {
		case pb.RechargeRequestStatus_RECHARGE_FINALIZED:
			if req.RequesterBondStatus != pb.RequesterBondStatus_REQUESTER_BOND_RETURNED && req.RequesterBondStatus != pb.RequesterBondStatus_REQUESTER_BOND_RECOVERED {
				return fmt.Errorf("finalized recharge bond was not returned or recovered: %s", req.RequestId)
			}
		case pb.RechargeRequestStatus_RECHARGE_REJECTED:
			if req.RequesterBondStatus != pb.RequesterBondStatus_REQUESTER_BOND_FORFEITED && req.RequesterBondStatus != pb.RequesterBondStatus_REQUESTER_BOND_RECOVERED {
				return fmt.Errorf("rejected recharge bond was not forfeited or recovered: %s", req.RequestId)
			}
		default:
			if req.RequesterBondStatus != pb.RequesterBondStatus_REQUESTER_BOND_HELD && req.RequesterBondStatus != pb.RequesterBondStatus_REQUESTER_BOND_RECOVERED {
				return fmt.Errorf("pending recharge bond already settled: %s", req.RequestId)
			}
		}
		return nil
	}
	if req.EconomicsPolicy == protocol.RechargeEconomicsLaunchV1 {
		return fmt.Errorf("launch recharge policy requires trusted 51025 executor context: request=%s chain=%d", req.RequestId, chainID)
	}
	if req.EconomicsPolicy != protocol.RechargeEconomicsLegacy {
		return fmt.Errorf("unknown recharge economics policy: request=%s policy=%d", req.RequestId, req.EconomicsPolicy)
	}
	if req.RequesterBondAmount != "" || req.RequesterBondStatus != pb.RequesterBondStatus_REQUESTER_BOND_NONE {
		return fmt.Errorf("legacy recharge cannot carry requester bond: request=%s chain=%d", req.RequestId, chainID)
	}
	return nil
}

func inheritRechargeEconomicsPolicy(dst, canonical *pb.RechargeRequest) error {
	if dst == nil || canonical == nil {
		return nil
	}
	if dst.EconomicsPolicy != 0 && dst.EconomicsPolicy != canonical.EconomicsPolicy {
		return fmt.Errorf("recharge economics policy mutation: request=%s have=%d canonical=%d", dst.RequestId, dst.EconomicsPolicy, canonical.EconomicsPolicy)
	}
	dst.EconomicsPolicy = canonical.EconomicsPolicy
	return nil
}
