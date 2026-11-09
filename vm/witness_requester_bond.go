package vm

import (
	"fmt"
	"math/big"

	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/rent"
	"google.golang.org/protobuf/proto"
)

                                                                          
                                                                      
const WitnessRequesterBondPoolAccount = "__witness_requester_bond_pool__"

const rentClaimRequesterBond = "witness_requester_bond"

func requesterBondAmount(req *pb.RechargeRequest) (*big.Int, error) {
	if req == nil || req.RequesterBondStatus == pb.RequesterBondStatus_REQUESTER_BOND_NONE {
		return new(big.Int), nil
	}
	amount, err := ParseBalance(req.RequesterBondAmount)
	if err != nil || amount.Cmp(big.NewInt(protocol.RequesterBondRaw)) != 0 {
		return nil, fmt.Errorf("invalid requester bond for %s: amount=%q status=%s", req.RequestId, req.RequesterBondAmount, req.RequesterBondStatus)
	}
	return amount, nil
}

func inheritRequesterBond(dst, canonical *pb.RechargeRequest) {
	if dst != nil && canonical != nil {
		dst.RequesterBondAmount = canonical.RequesterBondAmount
		dst.RequesterBondStatus = canonical.RequesterBondStatus
	}
}

                                                                              
                                                                               
                                                                       
func moveRequesterBondBalance(sv StateView, address string, amount *big.Int, debit bool) error {
	if sv == nil || address == "" || amount == nil || amount.Sign() <= 0 {
		return fmt.Errorf("invalid requester bond balance movement")
	}
	bal, err := getBalanceStrict(sv, address, FeeToken)
	if err != nil {
		return err
	}
	current, err := parseBalanceStrict("requester bond available balance", bal.Balance)
	if err != nil {
		return err
	}
	var next *big.Int
	if debit {
		if current.Cmp(amount) < 0 {
			return fmt.Errorf("insufficient requester bond balance: %s", address)
		}
		next = new(big.Int).Sub(current, amount) // safe-math-ok: 前面已校验 current >= amount，扣保证金不下溢
	} else {
		next, err = SafeAdd(current, amount)
		if err != nil {
			return err
		}
	}
	bal.Balance = next.String()
	SetBalance(sv, address, FeeToken, bal)
	return nil
}

                                                                            
                                                                              
func collectRequesterBond(sv StateView, req *pb.RechargeRequest) error {
	if sv == nil || req == nil || req.RequestId == "" || req.RequesterAddress == "" {
		return fmt.Errorf("missing requester bond identity")
	}
	if req.EconomicsPolicy != protocol.RechargeEconomicsLaunchV1 {
		return fmt.Errorf("requester bond requires launch recharge policy: %s", req.RequestId)
	}
	stored, err := readWitnessSettlementRequest(sv, req.RequestId)
	if err != nil {
		return err
	}
	if stored.RequesterAddress != req.RequesterAddress || stored.Status != pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION || stored.RequesterBondStatus != pb.RequesterBondStatus_REQUESTER_BOND_NONE || stored.RequesterBondAmount != "" {
		return fmt.Errorf("requester bond admission mismatch: %s", req.RequestId)
	}
	amount := big.NewInt(protocol.RequesterBondRaw)
	if err := moveRequesterBondBalance(sv, req.RequesterAddress, amount, true); err != nil {
		return err
	}
	if err := moveRequesterBondBalance(sv, WitnessRequesterBondPoolAccount, amount, false); err != nil {
		return err
	}
	if err := rent.SetClaim(sv, req.RequesterAddress, FeeToken, rentClaimRequesterBond, req.RequestId, amount); err != nil {
		return err
	}
	req.RequesterBondAmount = amount.String()
	req.RequesterBondStatus = pb.RequesterBondStatus_REQUESTER_BOND_HELD
	return persistRequesterBondState(sv, req)
}

func persistRequesterBondState(sv StateView, req *pb.RechargeRequest) error {
	data, err := proto.Marshal(req)
	if err != nil {
		return err
	}
	sv.Set(keys.KeyRechargeRequest(req.RequestId), data)
	return nil
}

                                                                             
                                                               
func settleRequesterBond(sv StateView, req *pb.RechargeRequest, success bool) error {
	if sv == nil || req == nil || req.RequestId == "" {
		return fmt.Errorf("missing requester bond request")
	}
	stored, err := readWitnessSettlementRequest(sv, req.RequestId)
	if err != nil {
		return err
	}
	if req.RequesterAddress != "" && req.RequesterAddress != stored.RequesterAddress {
		return fmt.Errorf("requester bond owner mismatch: %s", req.RequestId)
	}
	req.RequesterAddress = stored.RequesterAddress
	inheritRequesterBond(req, stored)
	amount, err := requesterBondAmount(stored)
	if err != nil {
		return err
	}
	switch stored.RequesterBondStatus {
	case pb.RequesterBondStatus_REQUESTER_BOND_NONE:
		if stored.RequesterBondAmount != "" {
			return fmt.Errorf("legacy request has bond amount: %s", req.RequestId)
		}
		return nil
	case pb.RequesterBondStatus_REQUESTER_BOND_RETURNED:
		if !success {
			return fmt.Errorf("requester bond already returned before rejection: %s", req.RequestId)
		}
		return nil
	case pb.RequesterBondStatus_REQUESTER_BOND_FORFEITED:
		if success {
			return fmt.Errorf("requester bond already forfeited before finalization: %s", req.RequestId)
		}
		return nil
	case pb.RequesterBondStatus_REQUESTER_BOND_RECOVERED:
		return nil
	case pb.RequesterBondStatus_REQUESTER_BOND_HELD:
		if err := moveRequesterBondBalance(sv, WitnessRequesterBondPoolAccount, amount, true); err != nil {
			return err
		}
		if success {
			if err := moveRequesterBondBalance(sv, stored.RequesterAddress, amount, false); err != nil {
				return err
			}
			req.RequesterBondStatus = pb.RequesterBondStatus_REQUESTER_BOND_RETURNED
		} else {
			if _, err := adjustTokenTotalSupplyForAudit(sv, FeeToken, new(big.Int).Neg(amount)); err != nil {
				return err
			}
			req.RequesterBondStatus = pb.RequesterBondStatus_REQUESTER_BOND_FORFEITED
		}
		return rent.SetClaim(sv, stored.RequesterAddress, FeeToken, rentClaimRequesterBond, req.RequestId, new(big.Int))
	default:
		return fmt.Errorf("unknown requester bond status: %s", stored.RequesterBondStatus)
	}
}

                                                                          
                                                                      
func (c vmRentCleaner) sweepRequesterBondsFromClaims(owner string, claims map[string][]byte) (err error) {
	if claims == nil {
		return nil
	}
	snap := c.sv.Snapshot()
	defer func() {
		if err != nil {
			_ = c.sv.Revert(snap)
		}
	}()
	prefix := keys.KeyAssetClaim(owner, FeeToken, rentClaimRequesterBond, "")
	for _, key := range sortedRentKeys(claims) {
		if len(key) < len(prefix) || key[:len(prefix)] != prefix {
			continue
		}
		requestID := key[len(prefix):]
		if requestID == "" {
			return fmt.Errorf("empty requester bond claim id")
		}
		req, err := readWitnessSettlementRequest(c.sv, requestID)
		if err != nil {
			return err
		}
		amount, err := requesterBondAmount(req)
		if err != nil {
			return err
		}
		claimAmount, err := ParseBalance(string(claims[key]))
		if err != nil || req.RequesterAddress != owner || req.RequesterBondStatus != pb.RequesterBondStatus_REQUESTER_BOND_HELD || amount.Cmp(claimAmount) != 0 {
			return fmt.Errorf("requester bond rent claim mismatch: %s", requestID)
		}
		if err := moveRequesterBondBalance(c.sv, WitnessRequesterBondPoolAccount, amount, true); err != nil {
			return err
		}
		if err := addRentRecycleBalance(c.sv, FeeToken, amount); err != nil {
			return err
		}
		req.RequesterBondStatus = pb.RequesterBondStatus_REQUESTER_BOND_RECOVERED
		if err := persistRequesterBondState(c.sv, req); err != nil {
			return err
		}
		if err := rent.SetClaim(c.sv, owner, FeeToken, rentClaimRequesterBond, requestID, new(big.Int)); err != nil {
			return err
		}
	}
	return nil
}
