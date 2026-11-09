package vm

import (
	"dex/future"
	iface "dex/interfaces"
	"dex/keys"
	"dex/pb"
	"dex/rent"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"
)

type rentStateViewStore struct {
	sv StateView
}

func (s rentStateViewStore) Get(key string) ([]byte, bool, error) {
	return s.sv.Get(key)
}

func (s rentStateViewStore) GetMany(keys []string) (map[string][]byte, error) {
	if getter, ok := s.sv.(interface {
		GetMany([]string) (map[string][]byte, error)
	}); ok {
		return getter.GetMany(keys)
	}
	out := make(map[string][]byte, len(keys))
	for _, k := range keys {
		if data, exists, err := s.sv.Get(k); err != nil {
			return nil, err
		} else if exists {
			out[k] = data
		}
	}
	return out, nil
}

func (s rentStateViewStore) Scan(prefix string, limit int) (map[string][]byte, error) {
	if limit > 0 {
		return s.sv.ScanWithLimit(prefix, limit)
	}
	return s.sv.Scan(prefix)
}

func (s rentStateViewStore) ScanRentOrderedAfter(prefix, startAfter string, limit int) ([]iface.KVPair, error) {
	if scanner, ok := s.sv.(interface {
		ScanWithLimitOrderedAfter(string, string, int, bool) ([]KVPair, error)
	}); ok {
		return scanner.ScanWithLimitOrderedAfter(prefix, startAfter, limit, false)
	}
	rows, err := s.Scan(prefix, limit)
	if err != nil {
		return nil, err
	}
	keys := sortedRentKeys(rows)
	out := make([]iface.KVPair, 0, len(keys))
	for _, key := range keys {
		if startAfter != "" && key <= startAfter {
			continue
		}
		out = append(out, iface.KVPair{Key: key, Value: rows[key]})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s rentStateViewStore) Set(key string, value []byte) {
	s.sv.Set(key, value)
}

func (s rentStateViewStore) Del(key string) {
	s.sv.Del(key)
}

type vmRentCleaner struct {
	sv StateView
}

                                                                              
                                                                              
                        
func settleRentOwner(sv StateView, owner string, height uint64) (*rent.RentSettlement, error) {
	runtime := rent.NewRuntime(rentStateViewStore{sv: sv})
	return runtime.SettleRent(owner, height)
}

                                                                               
                                                                                
                                                        
func refreshRentOwner(sv StateView, owner string, height uint64) (*rent.RentSettlement, error) {
	runtime := rent.NewRuntime(rentStateViewStore{sv: sv})
	return runtime.RefreshRentEligibility(owner, height)
}

func (c vmRentCleaner) PrepareAccountSweep(owner string, height uint64) error {
	return c.prepareAccountSweep(owner, height, nil)
}

func (c vmRentCleaner) PrepareAccountSweepWithClaims(owner string, height uint64, claims map[string][]byte) error {
	return c.prepareAccountSweep(owner, height, claims)
}

                                                                            
                                                                              
                                                                           
                                                                         
func (c vmRentCleaner) PrepareAccountSweepClaimPage(owner string, _ uint64, claims map[string][]byte) error {
	if err := c.sweepFuturePositionsFromClaims(owner, claims); err != nil {
		return err
	}
	if err := c.sweepChallengeContributionsFromClaims(owner, claims); err != nil {
		return err
	}
	return c.sweepRequesterBondsFromClaims(owner, claims)
}

func (c vmRentCleaner) FinalizeAccountSweep(owner string, height uint64) error {
	if err := c.clearReferralBinding(owner); err != nil {
		return err
	}
	recordUserActivity(c.sv, owner, &UserActivityRecord{
		Kind:   ActKindRentSweep,
		Height: height,
		Ref:    rent.RecycleFundAccount,
		Note:   "Account removed after rent funding failed",
	})
	return nil
}

func (c vmRentCleaner) prepareAccountSweep(owner string, height uint64, claims map[string][]byte) error {
	if err := c.cancelSpotOrders(owner, height); err != nil {
		return err
	}
	if err := c.cancelFutureOrders(owner, height); err != nil {
		return err
	}
	if err := c.sweepFuturePositionsFromClaims(owner, claims); err != nil {
		return err
	}
	if err := c.sweepChallengeContributionsFromClaims(owner, claims); err != nil {
		return err
	}
	if err := c.sweepRequesterBondsFromClaims(owner, claims); err != nil {
		return err
	}
	return c.clearReferralBinding(owner)
}

func (c vmRentCleaner) clearReferralBinding(owner string) error {
	data, exists, err := c.sv.Get(keys.KeyReferrer(owner))
	if err != nil || !exists || len(data) == 0 {
		return err
	}
	c.sv.Del(keys.KeyReferrer(owner))
	c.sv.Del(keys.KeyReferralInvitee(string(data), owner))
	return nil
}

func (c vmRentCleaner) cancelSpotOrders(owner string, height uint64) error {
	kvs, err := c.sv.Scan(keys.KeyAccountOrdersPrefix(owner))
	if err != nil {
		return err
	}
	prefix := keys.KeyAccountOrdersPrefix(owner)
	for _, k := range sortedRentKeys(kvs) {
		orderID := strings.TrimPrefix(k, prefix)
		data, exists, err := c.sv.Get(keys.KeyOrderState(orderID))
		if err != nil {
			return err
		}
		if !exists || len(data) == 0 {
			c.sv.Del(k)
			continue
		}
		var state pb.OrderState
		if err := proto.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("rent: parse spot order %s: %w", orderID, err)
		}
		if state.Owner != owner || state.Status != pb.OrderStateStatus_ORDER_OPEN || state.IsFilled {
			c.sv.Del(k)
			continue
		}
		if err := cancelSpotOpenOrderState(c.sv, &state, height, "rent_sweep_spot_unfreeze"); err != nil {
			return fmt.Errorf("rent: cancel spot order %s: %w", orderID, err)
		}
	}
	return nil
}

func (c vmRentCleaner) cancelFutureOrders(owner string, height uint64) error {
	                                                                         
	                                                                       
	                                                                       
	kvs, err := c.sv.Scan(keys.KeyFutureOrderStatePrefix())
	if err != nil {
		return err
	}
	for _, k := range sortedRentKeys(kvs) {
		var state pb.FutureOrderState
		if err := proto.Unmarshal(kvs[k], &state); err != nil {
			return fmt.Errorf("rent: parse future order %s: %w", k, err)
		}
		orderOwner := state.OwnerAddress
		if orderOwner == "" {
			orderOwner = state.Owner
		}
		if orderOwner != owner || state.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN || state.IsClose {
			continue
		}
		if _, _, err := cancelFutureOpenOrderState(c.sv, &state, height, "rent_sweep_future_unfreeze"); err != nil {
			return fmt.Errorf("rent: cancel future order %s: %w", state.OrderId, err)
		}
	}
	return nil
}

func (c vmRentCleaner) sweepFuturePositions(owner string) error {
	kvs, err := c.sv.Scan(keys.KeyAssetClaimOwnerPrefix(owner))
	if err != nil {
		return err
	}
	return c.sweepFuturePositionsFromClaims(owner, kvs)
}

func (c vmRentCleaner) sweepFuturePositionsFromClaims(owner string, kvs map[string][]byte) error {
	if kvs == nil {
		return c.sweepFuturePositions(owner)
	}
	store := &svPositionStore{sv: c.sv}
	liqIndex := &svLiquidationIndex{sv: c.sv}
	for _, k := range sortedRentKeys(kvs) {
		claimToken, positionID, ok := parseFuturePositionRentClaimKey(owner, k)
		if !ok {
			continue
		}
		if _, err := parseBalanceStrict("future position rent claim", string(kvs[k])); err != nil {
			return fmt.Errorf("rent: parse future position claim amount %s: %w", k, err)
		}
		pos, err := store.GetPosition(positionID)
		if err != nil {
			return fmt.Errorf("rent: read future position %s: %w", positionID, err)
		}
		if pos == nil || pos.Owner != owner || pos.Status != future.StatusOpen || pos.Margin == nil || pos.Margin.Sign() <= 0 {
			if err := rent.SetClaim(c.sv, owner, claimToken, rentClaimFuturePositionMargin, positionID, big.NewInt(0)); err != nil {
				return err
			}
			continue
		}
		token, tokenErr := futureMarketQuoteToken(c.sv, pos.Pair)
		if tokenErr != nil {
			return fmt.Errorf("rent: future position %s has invalid market %q: %w", pos.ID, pos.Pair, tokenErr)
		}
		if claimToken != token {
			return fmt.Errorf("rent: future position %s claim token mismatch: claim=%s pair=%s", pos.ID, claimToken, token)
		}
		if err := addRentRecycleBalance(c.sv, token, pos.Margin); err != nil {
			return fmt.Errorf("rent: recycle future position %s margin: %w", pos.ID, err)
		}
		if err := liqIndex.Remove(pos.ID, pos.Pair, pos.Side, nil); err != nil {
			return fmt.Errorf("rent: remove future liquidation index %s: %w", pos.ID, err)
		}
		if err := store.DeletePosition(pos.ID); err != nil {
			return fmt.Errorf("rent: delete future position %s: %w", pos.ID, err)
		}
	}
	return nil
}

func parseFuturePositionRentClaimKey(owner, key string) (token, positionID string, ok bool) {
	prefix := keys.KeyAssetClaimOwnerPrefix(owner)
	if !strings.HasPrefix(key, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(key, prefix)
	marker := "_" + rentClaimFuturePositionMargin + "_"
	idx := strings.Index(rest, marker)
	if idx <= 0 {
		return "", "", false
	}
	token = rest[:idx]
	positionID = rest[idx+len(marker):]
	return token, positionID, token != "" && positionID != ""
}

func (c vmRentCleaner) sweepChallengeContributions(owner string) error {
	prefix := keys.KeyAssetClaim(owner, FeeToken, rentClaimChallengeStake, "")
	kvs, err := c.sv.Scan(prefix)
	if err != nil {
		return err
	}
	return c.sweepChallengeContributionsFromClaims(owner, kvs)
}

func (c vmRentCleaner) sweepChallengeContributionsFromClaims(owner string, kvs map[string][]byte) error {
	if kvs == nil {
		return c.sweepChallengeContributions(owner)
	}
	prefix := keys.KeyAssetClaim(owner, FeeToken, rentClaimChallengeStake, "")
	for _, k := range sortedRentKeys(kvs) {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		sourceID := strings.TrimPrefix(k, prefix)
		challengeID, txID, err := parseRentChallengeContributionSourceID(sourceID)
		if err != nil {
			return fmt.Errorf("rent: parse challenge claim %s: %w", k, err)
		}
		claimAmount, err := parseBalanceStrict("challenge rent claim", string(kvs[k]))
		if err != nil {
			return fmt.Errorf("rent: parse challenge claim amount %s: %w", k, err)
		}
		if err := c.sweepChallengeContribution(owner, challengeID, txID, claimAmount); err != nil {
			return err
		}
	}
	return nil
}

func (c vmRentCleaner) sweepChallengeContribution(owner, challengeID, txID string, claimAmount *big.Int) error {
	sourceID := rentChallengeContributionSourceID(challengeID, txID)
	contributionKey := keys.KeyChallengeContribution(challengeID, txID)
	amount := new(big.Int)
	if data, exists, err := c.sv.Get(contributionKey); err != nil {
		return err
	} else if exists && len(data) > 0 {
		var tx pb.WitnessChallengeTx
		if err := unmarshalProtoCompat(data, &tx); err != nil {
			return fmt.Errorf("rent: parse challenge contribution %s: %w", contributionKey, err)
		}
		if tx.Base == nil || tx.Base.FromAddress != owner {
			if err := rent.SetClaim(c.sv, owner, FeeToken, rentClaimChallengeStake, sourceID, big.NewInt(0)); err != nil {
				return err
			}
			return nil
		}
		parsed, err := parseBalanceStrict("challenge contribution", tx.StakeAmount)
		if err != nil {
			return fmt.Errorf("rent: parse challenge contribution amount %s: %w", contributionKey, err)
		}
		amount.Set(parsed)
	}
	if amount.Sign() <= 0 && claimAmount != nil {
		amount.Set(claimAmount)
	}
	if amount.Sign() > 0 {
		if err := c.recycleActiveChallengeStake(challengeID, amount); err != nil {
			return err
		}
	}
	c.sv.Del(contributionKey)
	if err := rent.SetClaim(c.sv, owner, FeeToken, rentClaimChallengeStake, sourceID, big.NewInt(0)); err != nil {
		return err
	}
	return nil
}

func (c vmRentCleaner) recycleActiveChallengeStake(challengeID string, amount *big.Int) error {
	recordKey := keys.KeyChallengeRecord(challengeID)
	data, exists, err := c.sv.Get(recordKey)
	if err != nil || !exists || len(data) == 0 {
		return err
	}
	var rec pb.ChallengeRecord
	if err := unmarshalProtoCompat(data, &rec); err != nil {
		return fmt.Errorf("rent: parse challenge record %s: %w", challengeID, err)
	}
	if rec.Finalized || rec.Status == pb.ChallengeStatus_CHALLENGE_FINALIZED {
		return nil
	}
	stake, err := parseBalanceStrict("challenge stake", rec.StakeAmount)
	if err != nil {
		return fmt.Errorf("rent: parse challenge stake %s: %w", challengeID, err)
	}
	actual := new(big.Int).Set(amount)
	if stake.Cmp(actual) < 0 {
		actual.Set(stake)
	}
	if actual.Sign() <= 0 {
		return nil
	}
	if err := addRentRecycleBalance(c.sv, FeeToken, actual); err != nil {
		return fmt.Errorf("rent: recycle challenge stake %s: %w", challengeID, err)
	}
	next, err := SafeSub(stake, actual)
	if err != nil {
		return err
	}
	rec.StakeAmount = next.String()
	updated, err := proto.Marshal(&rec)
	if err != nil {
		return err
	}
	c.sv.Set(recordKey, updated)
	return nil
}

func (x *Executor) processRentHooks(b *pb.Block, sv StateView, seedDiff []WriteOp) (int, rent.ProcessBlockStats, error) {
	if b == nil || b.Header == nil {
		return 0, rent.ProcessBlockStats{}, nil
	}
	                                                                        
	                                                                         
	owners := collectRentTouchedOwners(b, sv.Diff())
	rt := rent.NewRuntime(rentStateViewStore{sv: sv}, rent.WithCleaner(vmRentCleaner{sv: sv}), rent.WithFundingAttempt(newRentFundingAttempt(sv, seedDiff)))
	err := rt.ProcessBlock(b.Header.Height, owners)
	return len(owners), rt.LastStats(), err
}

func collectRentTouchedOwners(_ *pb.Block, diff []WriteOp) []string {
	owners := make(map[string]struct{})
	for _, op := range diff {
		if owner, ok := rentOwnerFromKey(op.Key); ok {
			owners[owner] = struct{}{}
		}
	}
	out := make([]string, 0, len(owners))
	for owner := range owners {
		if rent.IsRentManagedAccount(owner) {
			out = append(out, owner)
		}
	}
	sort.Strings(out)
	return out
}

func rentOwnerFromKey(key string) (string, bool) {
	if strings.HasPrefix(key, keys.KeyAccount("")) {
		return validRentOwner(strings.TrimPrefix(key, keys.KeyAccount("")))
	}
	if owner, ok := rentOwnerAfterPrefix(key, rentKeyPrefix(keys.KeyBalancePrefix, "__rent_owner__")); ok {
		return validRentOwner(owner)
	}
	if owner, ok := rentOwnerAfterPrefix(key, rentKeyPrefix(keys.KeyAssetClaimOwnerPrefix, "__rent_owner__")); ok {
		return validRentOwner(owner)
	}
	if owner, ok := rentOwnerAfterPrefix(key, rentKeyPrefix(keys.KeyAssetClaimTotalOwnerPrefix, "__rent_owner__")); ok {
		return validRentOwner(owner)
	}
	if owner, ok := rentOwnerAfterPrefix(key, rentKeyPrefix(keys.KeyRentOwnerMarkerPrefix, "__rent_owner__")); ok {
		return validRentOwner(owner)
	}
	if owner, ok := rentOwnerAfterPrefix(key, rentKeyPrefix(keys.KeyAccountOrdersPrefix, "__rent_owner__")); ok {
		return validRentOwner(owner)
	}
	if owner, ok := rentOwnerAfterPrefix(key, rentKeyPrefix(keys.KeyFutureOwnerPositionPrefix, "__rent_owner__")); ok {
		return validRentOwner(owner)
	}
	return "", false
}

func rentKeyPrefix(fn func(string) string, marker string) string {
	return strings.TrimSuffix(fn(marker), marker+"_")
}

func rentOwnerAfterPrefix(key, prefix string) (string, bool) {
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(key, prefix)
	idx := strings.IndexByte(rest, '_')
	if idx <= 0 {
		return "", false
	}
	return rest[:idx], true
}

func validRentOwner(owner string) (string, bool) {
	owner = strings.TrimSpace(owner)
	if !rent.IsRentManagedAccount(owner) {
		return "", false
	}
	return owner, true
}

func sortedRentKeys(kvs map[string][]byte) []string {
	keys := make([]string, 0, len(kvs))
	for k := range kvs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
