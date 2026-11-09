package vm

import (
	"dex/keys"
	"dex/pb"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"

	"google.golang.org/protobuf/proto"
)

const (
	WitnessCaseRoleOriginal uint32 = 1
	WitnessCaseRoleOrdinary uint32 = 2
	WitnessCaseRoleCourt    uint32 = 4
	WitnessCaseBondRaw             = "1000000000"                          
)

var errWitnessCaseUnavailable = errors.New("witness cannot accept case collateral assignment")

                                                                               
                                                                                 
type WitnessCaseAssignment struct {
	Address string
	Role    uint32
	Amount  string
}

type witnessCollateralUpdate struct {
	info    *pb.WitnessInfo
	balance *pb.TokenBalance
	debit   *big.Int
}

                                                                              
                                                                            
func parseWitnessCollateralAmount(field, raw string) (*big.Int, error) {
	if raw == "" {
		return new(big.Int), nil
	}
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return nil, fmt.Errorf("%s must be an unsigned integer", field)
		}
	}
	value, ok := new(big.Int).SetString(raw, 10)
	if !ok || value.Cmp(MaxUint256) > 0 {
		return nil, fmt.Errorf("invalid or overflowing %s", field)
	}
	return value, nil
}

func cloneWitnessCaseCollateral(req *pb.RechargeRequest) (map[string]*pb.WitnessCaseCollateral, error) {
	if req == nil || req.RequestId == "" {
		return nil, fmt.Errorf("case collateral requires a recharge request ID")
	}
	entries := make(map[string]*pb.WitnessCaseCollateral, len(req.CaseCollateral))
	for _, entry := range req.CaseCollateral {
		if entry == nil || entry.Address == "" || entry.RoleMask == 0 || entry.RoleMask & ^uint32(7) != 0 {
			return nil, fmt.Errorf("invalid case collateral member")
		}
		if _, duplicate := entries[entry.Address]; duplicate {
			return nil, fmt.Errorf("duplicate case collateral member %s", entry.Address)
		}
		committed, err := parseWitnessCollateralAmount("case committed amount", entry.CommittedAmount)
		if err != nil {
			return nil, err
		}
		reserved, err := parseWitnessCollateralAmount("case reserved amount", entry.ReservedAmount)
		if err != nil {
			return nil, err
		}
		slashed, err := parseWitnessCollateralAmount("case slashed amount", entry.SlashedAmount)
		if err != nil {
			return nil, err
		}
		original, err := parseWitnessCollateralAmount("original case amount", entry.OriginalAmount)
		if err != nil {
			return nil, err
		}
		minimum, _ := new(big.Int).SetString(WitnessCaseBondRaw, 10)
		if entry.RoleMask&WitnessCaseRoleOriginal != 0 {
			if original.Cmp(minimum) != 0 {
				return nil, fmt.Errorf("original collateral snapshot must equal 10 FB for %s", entry.Address)
			}
		} else if original.Sign() != 0 {
			return nil, fmt.Errorf("non-original member has original collateral snapshot")
		}
		if entry.RoleMask&(WitnessCaseRoleOriginal|WitnessCaseRoleOrdinary) != 0 {
			if committed.Cmp(minimum) < 0 {
				return nil, fmt.Errorf("bonded case member has insufficient commitment")
			}
		} else if committed.Sign() != 0 {
			return nil, fmt.Errorf("court-only member cannot have collateral")
		}
		if entry.SlashApplied {
			if slashed.Cmp(committed) != 0 || reserved.Sign() != 0 || committed.Sign() == 0 {
				return nil, fmt.Errorf("inconsistent slashed collateral for %s", entry.Address)
			}
		} else if slashed.Sign() != 0 || (!entry.Released && reserved.Cmp(committed) != 0) {
			return nil, fmt.Errorf("inconsistent unspent collateral for %s", entry.Address)
		}
		if entry.Released && reserved.Sign() != 0 {
			return nil, fmt.Errorf("released collateral still reserved for %s", entry.Address)
		}
		entries[entry.Address] = proto.Clone(entry).(*pb.WitnessCaseCollateral)
	}
	return entries, nil
}

func readWitnessCollateralAccount(sv StateView, address string) (*witnessCollateralUpdate, *big.Int, *big.Int, error) {
	raw, exists, err := sv.Get(keys.KeyWitnessInfo(address))
	if err != nil {
		return nil, nil, nil, err
	}
	if !exists {
		return nil, nil, nil, fmt.Errorf("%w: unregistered %s", errWitnessCaseUnavailable, address)
	}
	var info pb.WitnessInfo
	if err := unmarshalProtoCompat(raw, &info); err != nil {
		return nil, nil, nil, fmt.Errorf("decode witness collateral account %s: %w", address, err)
	}
	if info.Address != address {
		return nil, nil, nil, fmt.Errorf("witness collateral account identity mismatch")
	}
	stake, err := parseWitnessCollateralAmount("witness stake", info.StakeAmount)
	if err != nil {
		return nil, nil, nil, err
	}
	reserved, err := parseWitnessCollateralAmount("witness reserved stake", info.ReservedStakeAmount)
	if err != nil {
		return nil, nil, nil, err
	}
	bal, err := getBalanceStrict(sv, address, FeeToken)
	if err != nil {
		return nil, nil, nil, err
	}
	locked, err := parseWitnessCollateralAmount("witness locked balance", bal.WitnessLockedBalance)
	if err != nil {
		return nil, nil, nil, err
	}
	if locked.Cmp(stake) != 0 || reserved.Cmp(stake) > 0 {
		return nil, nil, nil, fmt.Errorf("witness collateral backing mismatch for %s", address)
	}
	return &witnessCollateralUpdate{info: &info, balance: bal}, stake, reserved, nil
}

func sortedWitnessCollateralAddresses(entries map[string]*pb.WitnessCaseCollateral) []string {
	addresses := make([]string, 0, len(entries))
	for address := range entries {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	return addresses
}

func prepareWitnessCaseAssignments(sv StateView, req *pb.RechargeRequest, assignments []WitnessCaseAssignment, minStake string) (map[string]*pb.WitnessCaseCollateral, map[string]*witnessCollateralUpdate, error) {
	entries, err := cloneWitnessCaseCollateral(req)
	if err != nil {
		return nil, nil, err
	}
	minimum, err := parseWitnessCollateralAmount("minimum registered stake", minStake)
	if err != nil || minimum.Sign() <= 0 {
		return nil, nil, fmt.Errorf("invalid minimum registered stake %q", minStake)
	}
	bond, _ := new(big.Int).SetString(WitnessCaseBondRaw, 10)
	wanted := make(map[string]*pb.WitnessCaseCollateral)
	for _, assignment := range assignments {
		if assignment.Address == "" || (assignment.Role != WitnessCaseRoleOriginal && assignment.Role != WitnessCaseRoleOrdinary && assignment.Role != WitnessCaseRoleCourt) {
			return nil, nil, fmt.Errorf("invalid witness case assignment")
		}
		amount, err := parseWitnessCollateralAmount("assignment collateral", assignment.Amount)
		if err != nil {
			return nil, nil, err
		}
		if (assignment.Role == WitnessCaseRoleOriginal && amount.Cmp(bond) != 0) ||
			(assignment.Role == WitnessCaseRoleOrdinary && amount.Cmp(bond) < 0) ||
			(assignment.Role == WitnessCaseRoleCourt && amount.Sign() != 0) {
			return nil, nil, fmt.Errorf("invalid collateral amount for role %d", assignment.Role)
		}
		entry := wanted[assignment.Address]
		if entry == nil {
			entry = &pb.WitnessCaseCollateral{Address: assignment.Address, CommittedAmount: "0", OriginalAmount: "0"}
			wanted[assignment.Address] = entry
		}
		entry.RoleMask |= assignment.Role
		current, _ := parseWitnessCollateralAmount("merged assignment", entry.CommittedAmount)
		if amount.Cmp(current) > 0 {
			entry.CommittedAmount = amount.String()
		}
		if assignment.Role == WitnessCaseRoleOriginal {
			entry.OriginalAmount = WitnessCaseBondRaw
		}
	}
	updates := make(map[string]*witnessCollateralUpdate, len(wanted))
	for _, address := range sortedWitnessCollateralAddresses(wanted) {
		target := wanted[address]
		previous := entries[address]
		if previous != nil && previous.Released {
			return nil, nil, fmt.Errorf("%w: closed case member %s", errWitnessCaseUnavailable, address)
		}
		update, stake, aggregate, err := readWitnessCollateralAccount(sv, address)
		if err != nil {
			return nil, nil, err
		}
		currentCommitment := new(big.Int)
		currentReservation := new(big.Int)
		previousRoles := uint32(0)
		if previous != nil {
			currentCommitment, _ = parseWitnessCollateralAmount("existing commitment", previous.CommittedAmount)
			currentReservation, _ = parseWitnessCollateralAmount("existing reservation", previous.ReservedAmount)
			previousRoles = previous.RoleMask
			if update.info.ActiveCaseCount == 0 || aggregate.Cmp(currentReservation) < 0 {
				return nil, nil, fmt.Errorf("missing active collateral responsibility for %s", address)
			}
		}
		wantedAmount, _ := parseWitnessCollateralAmount("wanted commitment", target.CommittedAmount)
		if currentCommitment.Cmp(wantedAmount) > 0 {
			wantedAmount.Set(currentCommitment)
		}
		extra := new(big.Int).Sub(wantedAmount, currentCommitment) // safe-math-ok: wantedAmount 已提升为不低于现有承诺额
		newResponsibility := previous == nil || target.RoleMask & ^previousRoles != 0 || extra.Sign() > 0
		if newResponsibility && (update.info.Status != pb.WitnessStatus_WITNESS_ACTIVE || stake.Cmp(minimum) < 0 || (previous != nil && previous.SlashApplied)) {
			return nil, nil, fmt.Errorf("%w: ineligible new responsibility for %s", errWitnessCaseUnavailable, address)
		}
		nextAggregate, err := SafeAdd(aggregate, extra)
		if err != nil {
			return nil, nil, err
		}
		if nextAggregate.Cmp(stake) > 0 {
			return nil, nil, fmt.Errorf("%w: insufficient unreserved stake for %s", errWitnessCaseUnavailable, address)
		}
		if previous == nil {
			if update.info.ActiveCaseCount == math.MaxUint64 {
				return nil, nil, fmt.Errorf("active case count overflow")
			}
			update.info.ActiveCaseCount++
			previous = &pb.WitnessCaseCollateral{Address: address, OriginalAmount: "0", SlashedAmount: "0"}
			entries[address] = previous
		}
		previous.RoleMask |= target.RoleMask
		previous.CommittedAmount = wantedAmount.String()
		previous.ReservedAmount = new(big.Int).Add(currentReservation, extra).String() // safe-math-ok: 同一增量先经 SafeAdd 聚合并校验不超过实际 stake
		if target.RoleMask&WitnessCaseRoleOriginal != 0 {
			previous.OriginalAmount = WitnessCaseBondRaw
		}
		update.info.ReservedStakeAmount = nextAggregate.String()
		updates[address] = update
	}
	return entries, updates, nil
}

                                                                              
                                                                          
func canReserveWitnessCaseAssignment(sv StateView, req *pb.RechargeRequest, assignment WitnessCaseAssignment, minStake string) (bool, error) {
	_, _, err := prepareWitnessCaseAssignments(sv, req, []WitnessCaseAssignment{assignment}, minStake)
	if errors.Is(err, errWitnessCaseUnavailable) {
		return false, nil
	}
	return err == nil, err
}

                                                                            
                                                                                 
func commitWitnessCaseCollateral(sv StateView, req *pb.RechargeRequest, entries map[string]*pb.WitnessCaseCollateral, updates map[string]*witnessCollateralUpdate, writeBalances bool) error {
	addresses := make([]string, 0, len(updates))
	serialized := make(map[string][]byte, len(updates))
	for address, update := range updates {
		data, err := proto.Marshal(update.info)
		if err != nil {
			return err
		}
		addresses = append(addresses, address)
		serialized[address] = data
	}
	sort.Strings(addresses)
	snapshot := 0
	if writeBalances {
		snapshot = sv.Snapshot()
	}
	for _, address := range addresses {
		if writeBalances {
			if err := NewLedger(sv).Move(MoveRequest{
				Account: address, Token: FeeToken, Amount: updates[address].debit,
				From: BucketWitnessLocked, To: BucketExternal, Reason: "witness_case_slash",
				Height: req.FinalizeHeight, TxID: req.RequestId, Ref: req.RequestId,
			}); err != nil {
				if revertErr := sv.Revert(snapshot); revertErr != nil {
					return fmt.Errorf("case slash failed: %v; rollback failed: %w", err, revertErr)
				}
				return err
			}
		}
		sv.Set(keys.KeyWitnessInfo(address), serialized[address])
	}
	result := make([]*pb.WitnessCaseCollateral, 0, len(entries))
	for _, address := range sortedWitnessCollateralAddresses(entries) {
		result = append(result, entries[address])
	}
	req.CaseCollateral = result
	return nil
}

func reserveWitnessCaseCollateral(sv StateView, req *pb.RechargeRequest, assignments []WitnessCaseAssignment, minStake string) error {
	entries, updates, err := prepareWitnessCaseAssignments(sv, req, assignments, minStake)
	if err != nil {
		return err
	}
	return commitWitnessCaseCollateral(sv, req, entries, updates, false)
}

func releaseWitnessCaseCollateral(sv StateView, req *pb.RechargeRequest) error {
	entries, err := cloneWitnessCaseCollateral(req)
	if err != nil {
		return err
	}
	updates := make(map[string]*witnessCollateralUpdate)
	for _, address := range sortedWitnessCollateralAddresses(entries) {
		entry := entries[address]
		if entry.Released {
			continue
		}
		update, _, aggregate, err := readWitnessCollateralAccount(sv, address)
		if err != nil {
			return err
		}
		reserved, _ := parseWitnessCollateralAmount("case reserved amount", entry.ReservedAmount)
		if update.info.ActiveCaseCount == 0 || aggregate.Cmp(reserved) < 0 {
			return fmt.Errorf("invalid case collateral release for %s", address)
		}
		update.info.ReservedStakeAmount = new(big.Int).Sub(aggregate, reserved).String() // safe-math-ok: 前面已校验 aggregate >= reserved，释放不会下溢
		update.info.ActiveCaseCount--
		entry.ReservedAmount = "0"
		entry.Released = true
		updates[address] = update
	}
	return commitWitnessCaseCollateral(sv, req, entries, updates, false)
}

                                                                           
                                                                                  
                                                                               
func slashWitnessCaseCollateral(sv StateView, req *pb.RechargeRequest, wrongAddresses []string) (map[string]*big.Int, error) {
	entries, err := cloneWitnessCaseCollateral(req)
	if err != nil {
		return nil, err
	}
	wrong := make(map[string]struct{}, len(wrongAddresses))
	for _, address := range wrongAddresses {
		wrong[address] = struct{}{}
	}
	addresses := make([]string, 0, len(wrong))
	for address := range wrong {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	updates := make(map[string]*witnessCollateralUpdate)
	actual := make(map[string]*big.Int)
	for _, address := range addresses {
		entry := entries[address]
		if entry == nil || entry.RoleMask&(WitnessCaseRoleOriginal|WitnessCaseRoleOrdinary) == 0 {
			return nil, fmt.Errorf("cannot penalize unbonded case member %s", address)
		}
		if entry.SlashApplied {
			continue
		}
		if entry.Released {
			return nil, fmt.Errorf("cannot penalize released case member %s", address)
		}
		update, stake, aggregate, err := readWitnessCollateralAccount(sv, address)
		if err != nil {
			return nil, err
		}
		reserved, _ := parseWitnessCollateralAmount("case reserved amount", entry.ReservedAmount)
		if update.info.ActiveCaseCount == 0 || aggregate.Cmp(reserved) < 0 || stake.Cmp(reserved) < 0 || update.info.SlashedCount == math.MaxUint64 {
			return nil, fmt.Errorf("invalid collateral debit for %s", address)
		}
		remaining := new(big.Int).Sub(stake, reserved) // safe-math-ok: 前面已校验 stake >= reserved，扣款不超过质押
		update.info.StakeAmount = remaining.String()
		update.debit = new(big.Int).Set(reserved)
		update.info.ReservedStakeAmount = new(big.Int).Sub(aggregate, reserved).String() // safe-math-ok: 前面已校验 aggregate >= reserved，释放不会下溢
		update.info.SlashedCount++
		                                                                        
		                                                                    
		entry.ReservedAmount = "0"
		entry.SlashedAmount = reserved.String()
		entry.SlashApplied = true
		updates[address] = update
		actual[address] = new(big.Int).Set(reserved)
	}
	if err := commitWitnessCaseCollateral(sv, req, entries, updates, true); err != nil {
		return nil, err
	}
	return actual, nil
}

func validateWitnessStakeClaim(info *pb.WitnessInfo) error {
	if info == nil {
		return fmt.Errorf("witness not found")
	}
	reserved, err := parseWitnessCollateralAmount("witness reserved stake", info.ReservedStakeAmount)
	if err != nil {
		return err
	}
	if info.ActiveCaseCount != 0 || reserved.Sign() != 0 {
		return fmt.Errorf("witness has unfinished case responsibilities")
	}
	return nil
}
