package vm

import (
	"dex/db"
	"dex/future"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"encoding/binary"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
)

                                                                                 

                                                             

                                                               
type svPositionStore struct {
	sv              StateView
	positionCache   map[string]*future.Position
	ownerCache      map[string][]*future.Position
	ownerPairCache  map[string]*future.Position
	ownerPairMisses map[string]struct{}
	pairOpenCache   map[string][]*future.Position
	cacheSnap       int
	cacheRevision   uint64
}

func cloneFuturePosition(pos *future.Position) *future.Position {
	if pos == nil {
		return nil
	}
	out := *pos
	if pos.EntryPrice != nil {
		out.EntryPrice = new(big.Int).Set(pos.EntryPrice)
	}
	if pos.EntryNotionalNumerator != nil {
		out.EntryNotionalNumerator = new(big.Int).Set(pos.EntryNotionalNumerator)
	}
	if pos.Quantity != nil {
		out.Quantity = new(big.Int).Set(pos.Quantity)
	}
	if pos.Margin != nil {
		out.Margin = new(big.Int).Set(pos.Margin)
	}
	if pos.MarginBasisAmount != nil {
		out.MarginBasisAmount = new(big.Int).Set(pos.MarginBasisAmount)
	}
	if pos.MarginBasisClosed != nil {
		out.MarginBasisClosed = new(big.Int).Set(pos.MarginBasisClosed)
	}
	if pos.LastCumulativeFunding != nil {
		out.LastCumulativeFunding = new(big.Int).Set(pos.LastCumulativeFunding)
	}
	if pos.FirstCumulativeFunding != nil {
		out.FirstCumulativeFunding = new(big.Int).Set(pos.FirstCumulativeFunding)
	}
	if pos.InitialMargin != nil {
		out.InitialMargin = new(big.Int).Set(pos.InitialMargin)
	}
	if pos.RealizedPnL != nil {
		out.RealizedPnL = new(big.Int).Set(pos.RealizedPnL)
	}
	if pos.ADLWithheldPnL != nil {
		out.ADLWithheldPnL = new(big.Int).Set(pos.ADLWithheldPnL)
	}
	if pos.ClosedQty != nil {
		out.ClosedQty = new(big.Int).Set(pos.ClosedQty)
	}
	if pos.ExitNotional != nil {
		out.ExitNotional = new(big.Int).Set(pos.ExitNotional)
	}
	if pos.ClosePnLRemainder != nil {
		out.ClosePnLRemainder = new(big.Int).Set(pos.ClosePnLRemainder)
	}
	if pos.MaxQuantity != nil {
		out.MaxQuantity = new(big.Int).Set(pos.MaxQuantity)
	}
	if pos.SettledFunding != nil {
		out.SettledFunding = new(big.Int).Set(pos.SettledFunding)
	}
	if pos.LastSettledFunding != nil {
		out.LastSettledFunding = new(big.Int).Set(pos.LastSettledFunding)
	}
	return &out
}

func cloneFuturePositions(in []*future.Position) []*future.Position {
	out := make([]*future.Position, 0, len(in))
	for _, pos := range in {
		out = append(out, cloneFuturePosition(pos))
	}
	return out
}

func futureOwnerPairSideCacheKey(owner, pair string, side future.Side) string {
	return owner + "\x00" + pair + "\x00" + strconv.Itoa(int(side))
}

func (s *svPositionStore) resetCaches() {
	s.positionCache = nil
	s.ownerCache = nil
	s.ownerPairCache = nil
	s.ownerPairMisses = nil
	s.pairOpenCache = nil
	if s.sv != nil {
		s.cacheSnap = s.sv.Snapshot()
		s.cacheRevision = s.sv.Revision()
	}
}

func (s *svPositionStore) ensureCacheFresh() {
	if s.sv == nil {
		return
	}
	snap := s.sv.Snapshot()
	revision := s.sv.Revision()
	if snap < s.cacheSnap || revision < s.cacheRevision {
		s.resetCaches()
		return
	}
	                                                                       
	                                                                       
	                                                                            
	                                                                            
	                                  
	if revision-s.cacheRevision != uint64(snap-s.cacheSnap) {
		s.resetCaches()
		return
	}
	if s.cacheSnap != snap {
		for key := range stateViewChangedKeysSince(s.sv, s.cacheSnap) {
			if futurePositionCacheKeyTouched(key) {
				s.resetCaches()
				return
			}
		}
		s.cacheSnap = snap
		s.cacheRevision = revision
	}
}

func futurePositionCacheKeyTouched(key string) bool {
	return strings.HasPrefix(key, keys.KeyFuturePositionPrefix()) ||
		strings.HasPrefix(key, "v1_future_owner_") ||
		strings.HasPrefix(key, "v1_future_pair_pos_")
}

func replacePositionInSlice(in []*future.Position, pos *future.Position, keep func(*future.Position) bool) []*future.Position {
	out := in[:0]
	replaced := false
	for _, existing := range in {
		if existing == nil || existing.ID == pos.ID {
			if !replaced && keep(pos) {
				out = append(out, cloneFuturePosition(pos))
				replaced = true
			}
			continue
		}
		out = append(out, existing)
	}
	if !replaced && keep(pos) {
		out = append(out, cloneFuturePosition(pos))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func removePositionFromSlice(in []*future.Position, positionID string) []*future.Position {
	out := in[:0]
	for _, pos := range in {
		if pos != nil && pos.ID != positionID {
			out = append(out, pos)
		}
	}
	return out
}

func futureBigIntIsZero(v *big.Int) bool {
	return v == nil || v.Sign() == 0
}

func futurePositionColdHistoryCandidate(pos *future.Position) bool {
	return pos != nil && pos.Status != future.StatusOpen && futureBigIntIsZero(pos.Quantity) && futureBigIntIsZero(pos.Margin)
}

func (s *svPositionStore) cacheSetPosition(pos *future.Position) {
	if pos == nil {
		return
	}
	if s.positionCache == nil {
		s.positionCache = make(map[string]*future.Position)
	}
	s.positionCache[pos.ID] = cloneFuturePosition(pos)

	if s.ownerCache != nil {
		if positions, ok := s.ownerCache[pos.Owner]; ok {
			s.ownerCache[pos.Owner] = replacePositionInSlice(positions, pos, func(*future.Position) bool { return true })
		}
	}

	ownerPairKey := futureOwnerPairSideCacheKey(pos.Owner, pos.Pair, pos.Side)
	if s.ownerPairMisses != nil {
		delete(s.ownerPairMisses, ownerPairKey)
	}
	if s.ownerPairCache != nil {
		if pos.Status == future.StatusOpen {
			s.ownerPairCache[ownerPairKey] = cloneFuturePosition(pos)
		} else {
			delete(s.ownerPairCache, ownerPairKey)
		}
	}

	if s.pairOpenCache != nil {
		if positions, ok := s.pairOpenCache[pos.Pair]; ok {
			s.pairOpenCache[pos.Pair] = replacePositionInSlice(positions, pos, func(p *future.Position) bool {
				return p.Status == future.StatusOpen
			})
		}
	}
	if s.sv != nil {
		s.cacheSnap = s.sv.Snapshot()
		s.cacheRevision = s.sv.Revision()
	}
}

func (s *svPositionStore) cacheDeletePosition(pos *future.Position) {
	if pos == nil {
		return
	}
	if s.positionCache != nil {
		delete(s.positionCache, pos.ID)
	}
	if s.ownerCache != nil {
		if positions, ok := s.ownerCache[pos.Owner]; ok {
			s.ownerCache[pos.Owner] = removePositionFromSlice(positions, pos.ID)
		}
	}
	ownerPairKey := futureOwnerPairSideCacheKey(pos.Owner, pos.Pair, pos.Side)
	if s.ownerPairCache != nil {
		delete(s.ownerPairCache, ownerPairKey)
	}
	if s.ownerPairMisses != nil {
		delete(s.ownerPairMisses, ownerPairKey)
	}
	if s.pairOpenCache != nil {
		if positions, ok := s.pairOpenCache[pos.Pair]; ok {
			s.pairOpenCache[pos.Pair] = removePositionFromSlice(positions, pos.ID)
		}
	}
	if s.sv != nil {
		s.cacheSnap = s.sv.Snapshot()
		s.cacheRevision = s.sv.Revision()
	}
}

func (s *svPositionStore) AllocatePositionID(owner, pair string, side future.Side, height uint64) (string, error) {
	key := keys.KeyFuturePositionIDSequence(owner, pair, int32(side))
	data, exists, err := s.sv.Get(key)
	if err != nil {
		return "", err
	}

	ordinal := uint64(1)
	if exists {
		if len(data) != 16 {
			return "", fmt.Errorf("malformed future position ID sequence for %s/%s/%d: %d bytes", owner, pair, side, len(data))
		}
		lastHeight := binary.BigEndian.Uint64(data[:8])
		lastOrdinal := binary.BigEndian.Uint64(data[8:])
		if lastOrdinal == 0 {
			return "", fmt.Errorf("malformed future position ID sequence for %s/%s/%d: zero ordinal", owner, pair, side)
		}
		if lastHeight > height {
			return "", fmt.Errorf("future position ID sequence height regression for %s/%s/%d: last=%d current=%d", owner, pair, side, lastHeight, height)
		}
		if lastHeight == height {
			if lastOrdinal == ^uint64(0) {
				return "", fmt.Errorf("future position ID sequence overflow for %s/%s/%d at height %d", owner, pair, side, height)
			}
			ordinal = lastOrdinal + 1
		}
	}

	base := fmt.Sprintf("%s_%s_%d_%d", owner, pair, side, height)
	positionID := base
	if ordinal > 1 {
		positionID = fmt.Sprintf("%s_%d", base, ordinal)
	}
	                                                                           
	                                                                      
	if _, occupied, err := s.sv.Get(keys.KeyFuturePosition(positionID)); err != nil {
		return "", fmt.Errorf("check future position ID collision %s: %w", positionID, err)
	} else if occupied {
		return "", fmt.Errorf("future position ID collision: %s", positionID)
	}

	encoded := make([]byte, 16)
	binary.BigEndian.PutUint64(encoded[:8], height)
	binary.BigEndian.PutUint64(encoded[8:], ordinal)
	s.sv.Set(key, encoded)
	return positionID, nil
}

func (s *svPositionStore) GetPosition(positionID string) (*future.Position, error) {
	s.ensureCacheFresh()
	if s.positionCache != nil {
		if pos, ok := s.positionCache[positionID]; ok {
			return cloneFuturePosition(pos), nil
		}
	}
	key := keys.KeyFuturePosition(positionID)
	data, exists, err := s.sv.Get(key)
	if err != nil {
		return nil, fmt.Errorf("read future position %s: %w", positionID, err)
	}
	if !exists || len(data) == 0 {
		return nil, nil
	}
	var ps pb.FuturePositionState
	if err := proto.Unmarshal(data, &ps); err != nil {
		return nil, err
	}
	pos := protoToPosition(&ps)
	if s.positionCache == nil {
		s.positionCache = make(map[string]*future.Position)
	}
	s.positionCache[positionID] = cloneFuturePosition(pos)
	return pos, nil
}

func (s *svPositionStore) SetPosition(pos *future.Position) error {
	                                                                            
	                                                                             
	                                                      
	s.ensureCacheFresh()
	ps := positionToProto(pos)
	data, err := proto.Marshal(ps)
	if err != nil {
		return err
	}
	if futurePositionColdHistoryCandidate(pos) {
		s.sv.Set(keys.KeyFuturePositionHistory(pos.ID), data)
		s.sv.Del(keys.KeyFuturePosition(pos.ID))
		s.sv.Del(keys.KeyFutureOwnerPosition(pos.Owner, pos.ID))
		s.sv.Del(keys.KeyFuturePairPosition(pos.Pair, pos.ID))
		s.sv.Del(keys.KeyFutureOwnerPairSidePosition(pos.Owner, pos.Pair, int32(pos.Side)))
		if err := clearFuturePositionRentClaim(s.sv, pos); err != nil {
			return err
		}
		s.cacheDeletePosition(pos)
		return nil
	}
	                                                                             
	                                                                          
	                                                                        
	                                                                             
	                            
	s.sv.Set(keys.KeyFuturePosition(pos.ID), data)
	s.sv.Set(keys.KeyFutureOwnerPosition(pos.Owner, pos.ID), data)
	ownerPairSideKey := keys.KeyFutureOwnerPairSidePosition(pos.Owner, pos.Pair, int32(pos.Side))
	if pos.Status == future.StatusOpen {
		s.sv.Set(keys.KeyFuturePairPosition(pos.Pair, pos.ID), data)
		s.sv.Set(ownerPairSideKey, []byte(pos.ID))
	} else {
		s.sv.Del(keys.KeyFuturePairPosition(pos.Pair, pos.ID))
		s.sv.Del(ownerPairSideKey)
	}
	if err := syncFuturePositionRentClaim(s.sv, pos); err != nil {
		return err
	}
	s.cacheSetPosition(pos)
	return nil
}

func (s *svPositionStore) DeletePosition(positionID string) error {
	pos, err := s.GetPosition(positionID)
	if err != nil || pos == nil {
		return err
	}
	if err := clearFuturePositionRentClaim(s.sv, pos); err != nil {
		return err
	}
	s.sv.Del(keys.KeyFuturePosition(positionID))
	s.sv.Del(keys.KeyFutureOwnerPosition(pos.Owner, positionID))
	s.sv.Del(keys.KeyFuturePairPosition(pos.Pair, positionID))
	s.sv.Del(keys.KeyFutureOwnerPairSidePosition(pos.Owner, pos.Pair, int32(pos.Side)))
	s.cacheDeletePosition(pos)
	return nil
}

func (s *svPositionStore) GetPositionsByOwner(owner string) ([]*future.Position, error) {
	s.ensureCacheFresh()
	if s.ownerCache != nil {
		if positions, ok := s.ownerCache[owner]; ok {
			return cloneFuturePositions(positions), nil
		}
	}
	prefix := keys.KeyFutureOwnerPositionPrefix(owner)
	kvs, err := s.sv.Scan(prefix)
	if err != nil {
		return nil, err
	}
	sortedKeys := make([]string, 0, len(kvs))
	for k := range kvs {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	var result []*future.Position
	for _, k := range sortedKeys {
		if len(kvs[k]) == 0 {
			return nil, fmt.Errorf("decode future owner position index %s: empty value", k)
		}
		var ps pb.FuturePositionState
		if err := proto.Unmarshal(kvs[k], &ps); err != nil {
			return nil, fmt.Errorf("decode future owner position index %s: %w", k, err)
		}
		result = append(result, protoToPosition(&ps))
	}
	if s.ownerCache == nil {
		s.ownerCache = make(map[string][]*future.Position)
	}
	s.ownerCache[owner] = cloneFuturePositions(result)
	return result, nil
}

func (s *svPositionStore) GetPositionByOwnerAndPairSide(owner, pair string, side future.Side) (*future.Position, error) {
	s.ensureCacheFresh()
	cacheKey := futureOwnerPairSideCacheKey(owner, pair, side)
	if s.ownerPairCache != nil {
		if pos, ok := s.ownerPairCache[cacheKey]; ok {
			return cloneFuturePosition(pos), nil
		}
	}
	if s.ownerPairMisses != nil {
		if _, ok := s.ownerPairMisses[cacheKey]; ok {
			return nil, nil
		}
	}
	data, exists, err := s.sv.Get(keys.KeyFutureOwnerPairSidePosition(owner, pair, int32(side)))
	if err != nil {
		return nil, err
	}
	if exists && len(data) > 0 {
		pos, err := s.GetPosition(string(data))
		if err != nil {
			return nil, err
		}
		if pos != nil && pos.Pair == pair && pos.Side == side && pos.Status == future.StatusOpen {
			if s.ownerPairCache == nil {
				s.ownerPairCache = make(map[string]*future.Position)
			}
			s.ownerPairCache[cacheKey] = cloneFuturePosition(pos)
			return pos, nil
		}
	}
	if s.ownerPairMisses == nil {
		s.ownerPairMisses = make(map[string]struct{})
	}
	s.ownerPairMisses[cacheKey] = struct{}{}
	return nil, nil
}

func (s *svPositionStore) GetAllOpenPositions(pair string) ([]*future.Position, error) {
	s.ensureCacheFresh()
	if s.pairOpenCache != nil {
		if positions, ok := s.pairOpenCache[pair]; ok {
			return cloneFuturePositions(positions), nil
		}
	}
	prefix := keys.KeyFuturePairPositionPrefix(pair)
	kvs, err := s.sv.Scan(prefix)
	if err != nil {
		return nil, err
	}
	sortedKeys := make([]string, 0, len(kvs))
	for k := range kvs {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	var result []*future.Position
	for _, k := range sortedKeys {
		if len(kvs[k]) == 0 {
			return nil, fmt.Errorf("decode future pair position index %s: empty value", k)
		}
		var ps pb.FuturePositionState
		if err := proto.Unmarshal(kvs[k], &ps); err != nil {
			return nil, fmt.Errorf("decode future pair position index %s: %w", k, err)
		}
		                                                                         
		                                                                       
		                                                                         
		                                                          
		if ps.Pair != pair || future.PositionStatus(ps.Status) != future.StatusOpen {
			continue
		}
		result = append(result, protoToPosition(&ps))
	}
	if s.pairOpenCache == nil {
		s.pairOpenCache = make(map[string][]*future.Position)
	}
	s.pairOpenCache[pair] = cloneFuturePositions(result)
	return result, nil
}

                                                                    
type svInsuranceFund struct{ sv StateView }

func (s *svInsuranceFund) GetBalance(pair string) (*big.Int, error) {
	key := keys.KeyFutureInsuranceFund(pair)
	data, exists, err := s.sv.Get(key)
	if err != nil {
		return nil, fmt.Errorf("read future insurance balance for %s: %w", pair, err)
	}
	if !exists || len(data) == 0 {
		return new(big.Int), nil
	}
	return new(big.Int).SetBytes(data), nil
}

func (s *svInsuranceFund) SetBalance(pair string, amount *big.Int) error {
	s.sv.Set(keys.KeyFutureInsuranceFund(pair), amount.Bytes())
	return nil
}

func (s *svInsuranceFund) GetShortfall(pair string) (*big.Int, error) {
	data, exists, err := s.sv.Get(keys.KeyFutureInsuranceShortfall(pair))
	if err != nil {
		return nil, fmt.Errorf("read future insurance shortfall for %s: %w", pair, err)
	}
	if !exists || len(data) == 0 {
		return new(big.Int), nil
	}
	return new(big.Int).SetBytes(data), nil
}

func (s *svInsuranceFund) AddShortfall(pair string, amount *big.Int) error {
	if amount == nil || amount.Sign() <= 0 {
		return nil
	}
	cur, err := s.GetShortfall(pair)
	if err != nil {
		return err
	}
	s.sv.Set(keys.KeyFutureInsuranceShortfall(pair), new(big.Int).Add(cur, amount).Bytes()) // safe-math-ok: 累计未偿风险缺口是非负统计量，不计入资产余额或铸币
	return nil
}

                                                                      
  
                                                     
                                                                                               
                                                          
                                                                                            
                                                                                          
  
                                                                                                 
                                                                                                
                                                       
type svBalanceAccessor struct {
	sv     StateView
	height uint64
}

func (s *svBalanceAccessor) GetAvailableBalance(owner, token string) (*big.Int, error) {
	tb, err := getBalanceStrict(s.sv, owner, token)
	if err != nil {
		return nil, err
	}
	bal, err := parseBucketField(tb, BucketAvailable)
	if err != nil {
		return nil, fmt.Errorf("parse available balance for %s/%s: %w", owner, token, err)
	}
	return bal, nil
}

func (s *svBalanceAccessor) DeductBalance(owner, token string, amount *big.Int) error {
	return NewLedger(s.sv).Move(MoveRequest{
		Account: owner, Token: token, Amount: amount,
		From: BucketAvailable, To: BucketMargin,
		Reason: "future_margin_deposit",
		Height: s.height,
	})
}

func (s *svBalanceAccessor) AddBalance(owner, token string, amount *big.Int) error {
	return NewLedger(s.sv).Move(MoveRequest{
		Account: owner, Token: token, Amount: amount,
		From: BucketMargin, To: BucketAvailable,
		Reason: "future_margin_withdraw",
		Height: s.height,
	})
}

                                                                       
type svMarkPriceProvider struct{ sv StateView }

func (s *svMarkPriceProvider) GetMarkPrice(pair string) (*big.Int, error) {
	key := keys.KeyFutureMarkPrice(pair)
	data, exists, err := s.sv.Get(key)
	if err != nil || !exists || len(data) == 0 {
		return nil, fmt.Errorf("no mark price for pair: %s", pair)
	}
	return new(big.Int).SetBytes(data), nil
}

                                                                                     
                                                                
func (s *svMarkPriceProvider) GetSettleMarkPrice(pair string) (*big.Int, error) {
	data, exists, err := s.sv.Get(keys.KeyFutureSettleMarkPrice(pair))
	if err != nil {
		return nil, fmt.Errorf("read settle mark price for %s: %w", pair, err)
	}
	if exists && len(data) > 0 {
		return new(big.Int).SetBytes(data), nil
	}
	return s.GetMarkPrice(pair)
}

                                                                                 
type svCumulativeFundingStore struct{ sv StateView }

                                                                         
                                                                               
                                                                              
                     
                                                                                                                 
func (s *svCumulativeFundingStore) loadFutureCumFunding(pair string) (*pb.FutureCumulativeFunding, error) {
	data, exists, err := s.sv.Get(keys.KeyFutureCumulativeFunding(pair))
	if err != nil {
		return nil, fmt.Errorf("read cumulative funding for %s: %w", pair, err)
	}
	if !exists || len(data) == 0 {
		return nil, nil
	}
	var cf pb.FutureCumulativeFunding
	if err := proto.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("decode cumulative funding for %s: %w", pair, err)
	}
	return &cf, nil
}

func parseBigOrEmpty(s string) *big.Int {
	if s == "" {
		return new(big.Int)
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return new(big.Int)
	}
	return v
}

func (s *svCumulativeFundingStore) GetCumulativeFunding(pair string) (*big.Int, *big.Int, error) {
	                                                                              
	                                                                            
	                                               
	cf, err := s.loadFutureCumFunding(pair)
	if err != nil {
		return nil, nil, err
	}
	if cf == nil {
		return new(big.Int), new(big.Int), nil
	}
	return parseBigOrEmpty(cf.CumulativeLong), parseBigOrEmpty(cf.CumulativeShort), nil
}

func (s *svCumulativeFundingStore) SetCumulativeFunding(pair string, cumLong, cumShort *big.Int) error {
	                                                                                           
	                                                      
	cf, err := s.loadFutureCumFunding(pair)
	if err != nil {
		return err
	}
	if cf == nil {
		cf = &pb.FutureCumulativeFunding{Pair: pair}
	}
	cf.CumulativeLong = cumLong.String()
	cf.CumulativeShort = cumShort.String()
	data, err := proto.Marshal(cf)
	if err != nil {
		return err
	}
	s.sv.Set(keys.KeyFutureCumulativeFunding(pair), data)
	return nil
}

func (s *svCumulativeFundingStore) GetPrevCumulativeFunding(pair string) (*big.Int, *big.Int, error) {
	cf, err := s.loadFutureCumFunding(pair)
	if err != nil {
		return nil, nil, err
	}
	if cf == nil {
		return new(big.Int), new(big.Int), nil
	}
	return parseBigOrEmpty(cf.PrevCumulativeLong), parseBigOrEmpty(cf.PrevCumulativeShort), nil
}

func (s *svCumulativeFundingStore) SetPrevCumulativeFunding(pair string, prevLong, prevShort *big.Int) error {
	cf, err := s.loadFutureCumFunding(pair)
	if err != nil {
		return err
	}
	if cf == nil {
		cf = &pb.FutureCumulativeFunding{Pair: pair}
	}
	cf.PrevCumulativeLong = prevLong.String()
	cf.PrevCumulativeShort = prevShort.String()
	data, err := proto.Marshal(cf)
	if err != nil {
		return err
	}
	s.sv.Set(keys.KeyFutureCumulativeFunding(pair), data)
	return nil
}

                                                  

func bigToString(v *big.Int) string {
	if v == nil {
		return "0"
	}
	return v.String()
}

func nonZeroBigToString(v *big.Int) string {
	if v == nil || v.Sign() == 0 {
		return ""
	}
	return v.String()
}

func protoToPosition(ps *pb.FuturePositionState) *future.Position {
	return &future.Position{
		ID: ps.PositionId, Owner: ps.Owner, Pair: ps.Pair,
		Side:                   future.Side(ps.Side),
		EntryPrice:             parseBigOrZero(ps.EntryPrice),
		EntryNotionalNumerator: parseBigOrZero(ps.EntryNotionalNumerator),
		Quantity:               parseBigOrZero(ps.Quantity),
		Margin:                 parseBigOrZero(ps.Margin),
		MarginBasisAmount:      parseBigOrZero(ps.MarginBasisAmount),
		MarginBasisClosed:      parseBigOrZero(ps.MarginBasisClosed),
		Status:                 future.PositionStatus(ps.Status),
		OpenHeight:             ps.OpenHeight,
		LastCumulativeFunding:  parseBigOrZero(ps.LastCumulativeFunding),
		FirstCumulativeFunding: parseBigOrZero(ps.FirstCumulativeFunding),
		SettledFunding:         parseBigOrZero(ps.SettledFunding),
		LastSettledFunding:     parseBigOrZero(ps.LastSettledFunding),
		RealizedPnL:            parseBigOrZero(ps.RealizedPnl),
		ADLWithheldPnL:         parseBigOrZero(ps.AdlWithheldPnl),
		ClosedQty:              parseBigOrZero(ps.ClosedQty),
		ExitNotional:           parseBigOrZero(ps.ExitNotional),
		ClosePnLRemainder:      parseBigOrZero(ps.ClosePnlRemainder),
		MaxQuantity:            parseBigOrZero(ps.MaxQuantity),
		InitialMargin:          parseBigOrZero(ps.InitialMargin),
		CloseHeight:            ps.CloseHeight,
	}
}

func positionToProto(pos *future.Position) *pb.FuturePositionState {
	return &pb.FuturePositionState{
		PositionId: pos.ID, Owner: pos.Owner, Pair: pos.Pair,
		Side:                   int32(pos.Side),
		EntryPrice:             bigToString(pos.EntryPrice),
		EntryNotionalNumerator: nonZeroBigToString(pos.EntryNotionalNumerator),
		Quantity:               bigToString(pos.Quantity),
		Margin:                 bigToString(pos.Margin),
		MarginBasisAmount:      nonZeroBigToString(pos.MarginBasisAmount),
		MarginBasisClosed:      nonZeroBigToString(pos.MarginBasisClosed),
		Status:                 int32(pos.Status),
		OpenHeight:             pos.OpenHeight,
		LastCumulativeFunding:  bigToString(pos.LastCumulativeFunding),
		FirstCumulativeFunding: bigToString(pos.FirstCumulativeFunding),
		SettledFunding:         bigToString(pos.SettledFunding),
		LastSettledFunding:     bigToString(pos.LastSettledFunding),
		RealizedPnl:            bigToString(pos.RealizedPnL),
		AdlWithheldPnl:         bigToString(pos.ADLWithheldPnL),
		ClosedQty:              bigToString(pos.ClosedQty),
		ExitNotional:           bigToString(pos.ExitNotional),
		ClosePnlRemainder:      nonZeroBigToString(pos.ClosePnLRemainder),
		MaxQuantity:            bigToString(pos.MaxQuantity),
		InitialMargin:          bigToString(pos.InitialMargin),
		CloseHeight:            pos.CloseHeight,
	}
}

                                                                        
  
                                                                                                            
                                                                                                               
                                                                                              
                                                                                        
                                                                                                               
  
            
                                                                                           
                                                                                                             
                                                                           
const (
	                                                                                             
	FutureLiqScanLimit = 10000
	                                                                           
	                                                                          
	FutureLiqScanBuffer = 1000
)

                                                                     
type svLiquidationIndex struct{ sv StateView }

func sideTag(s future.Side) string {
	if s == future.Long {
		return "long"
	}
	return "short"
}

func (s *svLiquidationIndex) Upsert(positionID, pair string, side future.Side, liqPrice *big.Int) error {
	tag := sideTag(side)
	priceKey, err := db.PriceToKey128(liqPrice.String())
	if err != nil {
		return err
	}

	                                                       
	refKey := keys.KeyFutureLiqPosRef(positionID)
	if oldKey, exists, _ := s.sv.Get(refKey); exists && len(oldKey) > 0 {
		s.sv.Del(string(oldKey))
	}

	                  
	newKey := keys.KeyFutureLiqIndex(tag, pair, priceKey, positionID)
	s.sv.Set(newKey, []byte(positionID))

	                     
	s.sv.Set(refKey, []byte(newKey))
	return nil
}

func (s *svLiquidationIndex) Remove(positionID, pair string, side future.Side, _ *big.Int) error {
	refKey := keys.KeyFutureLiqPosRef(positionID)
	if oldKey, exists, _ := s.sv.Get(refKey); exists && len(oldKey) > 0 {
		s.sv.Del(string(oldKey))
	}
	s.sv.Del(refKey)
	return nil
}

                                                                                     
                           
                                                                            
                                                                                            
                                                                                          
                                                       
  
                                                                                                 
                                                                                              
                                                                                         
func (s *svLiquidationIndex) FindLongsAtRisk(pair string, markPrice *big.Int) ([]string, error) {
	prefix := keys.KeyFutureLiqIndexPrefix("long", pair)
	markKey, err := db.PriceToKey128(markPrice.String())
	if err != nil {
		return nil, err
	}

	entries, err := scanStateViewOrdered(s.sv, prefix, FutureLiqScanLimit+FutureLiqScanBuffer, true)
	if err != nil {
		return nil, err
	}

	var atRisk, buffer []string
	for _, entry := range entries {
		key := entry.Key
		afterPrefix := key[len(prefix):]
		if len(afterPrefix) < 67 {
			continue
		}
		liqPrice67 := afterPrefix[:67]
		if liqPrice67 >= markKey {
			atRisk = append(atRisk, string(entry.Value))
			continue
		}
		                                                                        
		if len(buffer) >= FutureLiqScanBuffer {
			break
		}
		buffer = append(buffer, string(entry.Value))
	}
	return append(atRisk, buffer...), nil
}

                                                                        
func (s *svLiquidationIndex) FindShortsAtRisk(pair string, markPrice *big.Int) ([]string, error) {
	prefix := keys.KeyFutureLiqIndexPrefix("short", pair)
	markKey, err := db.PriceToKey128(markPrice.String())
	if err != nil {
		return nil, err
	}

	entries, err := scanStateViewOrdered(s.sv, prefix, FutureLiqScanLimit+FutureLiqScanBuffer, false)
	if err != nil {
		return nil, err
	}

	var atRisk, buffer []string
	for _, entry := range entries {
		key := entry.Key
		afterPrefix := key[len(prefix):]
		if len(afterPrefix) < 67 {
			continue
		}
		liqPrice67 := afterPrefix[:67]
		if liqPrice67 <= markKey {
			atRisk = append(atRisk, string(entry.Value))
			continue
		}
		if len(buffer) >= FutureLiqScanBuffer {
			break
		}
		buffer = append(buffer, string(entry.Value))
	}
	return append(atRisk, buffer...), nil
}

                                                          

                                                                                    
                                                                     
                                                
func newFutureEngine(sv StateView, height uint64) *future.Engine {
	eng := future.NewEngine(
		&svPositionStore{sv: sv},
		&svInsuranceFund{sv: sv},
		&svBalanceAccessor{sv: sv, height: height},
		&svMarkPriceProvider{sv: sv},
		&svCumulativeFundingStore{sv: sv},
		&svLiquidationIndex{sv: sv},
	)
	eng.SetMarketConfigLoader(func(pair string) (*future.MarketConfig, error) {
		return loadFutureMarketConfig(sv, pair)
	})
	eng.SetCurrentHeight(height)
	return eng
}

                                                                                             
                                                                                                
                                                                                                
func newFutureEngineWithActivity(sv StateView, height uint64) *future.Engine {
	eng := newFutureEngine(sv, height)
	eng.OnFundingSettled = func(owner, token, pair string, delta *big.Int) {
		if owner == "" || delta == nil || delta.Sign() == 0 {
			return
		}
		recordUserActivity(sv, owner, &UserActivityRecord{
			Kind: ActKindFundingSettle, Height: height,
			Amount: delta.String(), Token: token,
			Ref: pair,
		})
	}
	eng.OnFundingLiquidated = func(pair string, liq *future.LiquidationResult, adl *future.ADLResult) {
		if adl != nil {
			writeFutureADLActivities(sv, pair, height, []future.ADLResult{*adl})
		}
		if liq != nil {
			writeMarkPriceLiquidationRecords(sv, pair, height, []future.LiquidationResult{*liq})
		}
	}
	return eng
}

                                                                                           
func ensureFutureMarket(sv StateView, pair string, height uint64) error {
	key := keys.KeyFutureMarketConfig(pair)
	if existing, exists, err := sv.Get(key); err != nil {
		return fmt.Errorf("read future market config for %s: %w", pair, err)
	} else if exists {
		if _, decodeErr := protocol.DecodeMarketIdentity(pair, existing); decodeErr == nil || string(existing) == pair {
			return nil
		}
		return fmt.Errorf("decode future market config for %s", pair)
	}
	identity, err := loadSpotMarketIdentity(sv, pair)
	if err != nil {
		return err
	}
	encoded, err := protocol.EncodeMarketIdentity(identity.BaseToken, identity.QuoteToken)
	if err != nil {
		return fmt.Errorf("encode future market config for %s: %w", pair, err)
	}
	sv.Set(key, encoded)
	heightBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(heightBytes, height)
	sv.Set(keys.KeyFutureFundingHeight(pair), heightBytes)
	scheduleFutureFundingDue(sv, pair, height+future.DefaultFundingInterval)
	return nil
}

                                       

                                                                                   
type FutureOrderTxHandler struct{}

func (h *FutureOrderTxHandler) Kind() string            { return "future_order" }
func (h *FutureOrderTxHandler) Apply(_ *pb.AnyTx) error { return ErrNotImplemented }

func (h *FutureOrderTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	ftx := tx.GetFutureOrderTx()
	if ftx == nil {
		return nil, nil, fmt.Errorf("not a FutureOrderTx")
	}
	base := ftx.Base
	from := base.FromAddress
	pair := ftx.Pair

	                                                                           
	if _, spotExists, _ := sv.Get(keys.KeySpotLastPrice(pair)); !spotExists {
		err := fmt.Errorf("no spot market for pair: %s", pair)
		return nil, failedReceipt(base, err.Error()), err
	}

	                                                                     
	if err := ensureFutureMarket(sv, pair, base.ExecutedHeight); err != nil {
		return nil, failedReceipt(base, err.Error()), err
	}

	                                          
	                                                                              
	side := future.Side(ftx.Side)
	if side != future.Long && side != future.Short {
		err := fmt.Errorf("invalid side: %d", ftx.Side)
		return nil, failedReceipt(base, err.Error()), err
	}

	qty, ok := new(big.Int).SetString(ftx.Quantity, 10)
	if !ok || qty.Sign() <= 0 {
		err := fmt.Errorf("invalid quantity: %s", ftx.Quantity)
		return nil, failedReceipt(base, err.Error()), err
	}
	var price *big.Int
	if !ftx.IsMarket {
		var pOk bool
		price, pOk = new(big.Int).SetString(ftx.Price, 10)
		if !pOk || price.Sign() <= 0 {
			err := fmt.Errorf("invalid price: %s", ftx.Price)
			return nil, failedReceipt(base, err.Error()), err
		}
	} else {
		mp := &svMarkPriceProvider{sv: sv}
		markPrice, err := mp.GetMarkPrice(pair)
		if err != nil || markPrice == nil || markPrice.Sign() <= 0 {
			err := fmt.Errorf("no mark price available for market order: %s", pair)
			return nil, failedReceipt(base, err.Error()), err
		}
		                                                                                                              
		price = new(big.Int).Set(markPrice)
	}

	matchEng := NewFutureMatchEngine(sv, base.ExecutedHeight)
	cfg, cfgErr := matchEng.eng.GetMarketConfig(pair)
	if cfgErr != nil || cfg == nil || cfg.QuoteToken == "" {
		if cfgErr == nil {
			cfgErr = fmt.Errorf("missing quote token")
		}
		err := fmt.Errorf("load future market config for %s: %w", pair, cfgErr)
		return nil, failedReceipt(base, err.Error()), err
	}
	quoteToken := cfg.QuoteToken

	                                                                                                 
	if IsFrozen(sv, from, quoteToken) {
		return nil, failedReceipt(base, "account is frozen for token: "+quoteToken), fmt.Errorf("account is frozen for token: %s", quoteToken)
	}

	if ftx.IsClose {
		res, err := matchEng.CloseOrder(
			from, pair, quoteToken, side, qty, price, ftx.Leverage,
			ftx.IsMarket, ftx.MaxSlippageBps, ftx.ClosePositionId, base,
		)
		if err != nil {
			return nil, failedReceipt(base, err.Error()), err
		}
		receipt := &Receipt{
			TxID: base.TxId, Status: "SUCCEED", BlockHeight: base.ExecutedHeight,
			Logs: []string{fmt.Sprintf(
				"future_close: pair=%s side=%d requested=%s matched=%s remain=%s notional=%s realized_pnl=%s pnl_shortfall=%s fully_closed=%t",
				pair, side, qty, res.MatchedQty, res.RemainQty, res.Notional,
				res.RealizedPnL, res.PnLShortfall, res.FullyClosed,
			)},
		}
		receipt.Events = append(receipt.Events, res.Events...)
		return nil, receipt, nil
	}

	                                          
	if err := matchEng.eng.ValidateOpenOrder(pair, qty, price, ftx.Leverage); err != nil {
		return nil, failedReceipt(base, err.Error()), err
	}
	requiredMargin := future.CalcRequiredMargin(qty, price, ftx.Leverage)

	ba := &svBalanceAccessor{sv: sv, height: base.ExecutedHeight}
	if err := ba.DeductBalance(from, quoteToken, requiredMargin); err != nil {
		err = fmt.Errorf("insufficient margin: %w", err)
		return nil, failedReceipt(base, err.Error()), err
	}

	res, err := matchEng.OpenOrder(from, pair, quoteToken, side, qty, price, ftx.Leverage, ftx.IsMarket, ftx.MaxSlippageBps, requiredMargin, base)
	if err != nil {
		return nil, failedReceipt(base, err.Error()), err
	}

	                                                                                                                         
	unusedMargin := res.RemainingMargin
	if unusedMargin == nil {
		unusedMargin = new(big.Int)
	}
	if res.Status == pb.FutureOrderStatus_FUTURE_ORDER_OPEN && !ftx.IsMarket {
		if unusedMargin.Sign() > 0 {
			if err := NewLedger(sv).Move(MoveRequest{
				Account: from, Token: quoteToken, Amount: unusedMargin,
				From: BucketMargin, To: BucketOrderFrozen,
				Reason: "future_order_margin_freeze",
				Height: base.ExecutedHeight, Ref: base.TxId,
			}); err != nil {
				return nil, failedReceipt(base, err.Error()), err
			}
		}
		allocation, err := newFutureReservedMarginAllocation(unusedMargin, res.RemainQty, price, ftx.Leverage)
		if err != nil {
			return nil, failedReceipt(base, err.Error()), err
		}
		                                                                       
		                                                                      
		                                                                           
		                                   
		sv.Set(keys.KeyFutureOrderReservedMargin(base.TxId), encodeFutureReservedMarginAllocation(allocation))
	} else if unusedMargin.Sign() > 0 {
		if err := ba.AddBalance(from, quoteToken, unusedMargin); err != nil {
			refundErr := fmt.Errorf("refund unused future order margin: %w", err)
			return nil, failedReceipt(base, refundErr.Error()), refundErr
		}
	}

	receipt := &Receipt{
		TxID: base.TxId, Status: "SUCCEED", BlockHeight: base.ExecutedHeight,
		Logs: []string{fmt.Sprintf("future_order: pair=%s side=%d qty=%s price=%s", pair, side, qty, price)},
	}
	if res.MatchedQty.Sign() > 0 {
		receipt.AppendEvent(EventPositionOpened, map[string]string{
			"pair":        pair,
			"owner":       from,
			"side":        side.String(),
			"quantity":    res.MatchedQty.String(),
			"notional":    res.MatchedQuote.String(),
			"leverage":    strconv.FormatUint(ftx.Leverage, 10),
			"status":      res.Status.String(),
			"open_height": strconv.FormatUint(base.ExecutedHeight, 10),
		})
	}
	receipt.Events = append(receipt.Events, res.Events...)
	return nil, receipt, nil
}

                                           
type FutureAddMarginTxHandler struct{}

func requireFuturePositionOwner(sv StateView, positionID, owner string) (*future.Position, error) {
	if positionID == "" {
		return nil, fmt.Errorf("missing position_id")
	}
	pos, err := (&svPositionStore{sv: sv}).GetPosition(positionID)
	if err != nil {
		return nil, err
	}
	if pos == nil || pos.Status != future.StatusOpen {
		return nil, fmt.Errorf("position not found or not open: %s", positionID)
	}
	if pos.Owner != owner {
		return nil, fmt.Errorf("position owner mismatch: position=%s owner=%s tx_from=%s", positionID, pos.Owner, owner)
	}
	return pos, nil
}

func (h *FutureAddMarginTxHandler) Kind() string            { return "future_add_margin" }
func (h *FutureAddMarginTxHandler) Apply(_ *pb.AnyTx) error { return ErrNotImplemented }

func (h *FutureAddMarginTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	ftx := tx.GetFutureAddMarginTx()
	if ftx == nil {
		return nil, nil, fmt.Errorf("not a FutureAddMarginTx")
	}
	amount, ok := new(big.Int).SetString(ftx.Amount, 10)
	if !ok || amount.Sign() <= 0 {
		err := fmt.Errorf("invalid amount: %s", ftx.Amount)
		return nil, failedReceipt(ftx.Base, err.Error()), err
	}
	if _, err := requireFuturePositionOwner(sv, ftx.PositionId, ftx.Base.FromAddress); err != nil {
		return nil, failedReceipt(ftx.Base, err.Error()), err
	}

	eng := newFutureEngineWithActivity(sv, ftx.Base.ExecutedHeight)
	if err := eng.AddMargin(ftx.PositionId, amount); err != nil {
		return nil, failedReceipt(ftx.Base, err.Error()), err
	}

	receipt := &Receipt{
		TxID: ftx.Base.TxId, Status: "SUCCEED",
		BlockHeight: ftx.Base.ExecutedHeight,
		Logs:        []string{fmt.Sprintf("future_add_margin: pos=%s amount=%s", ftx.PositionId, amount)},
	}
	return nil, receipt, nil
}

                                              
type FutureRemoveMarginTxHandler struct{}

func (h *FutureRemoveMarginTxHandler) Kind() string            { return "future_remove_margin" }
func (h *FutureRemoveMarginTxHandler) Apply(_ *pb.AnyTx) error { return ErrNotImplemented }

func (h *FutureRemoveMarginTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	ftx := tx.GetFutureRemoveMarginTx()
	if ftx == nil {
		return nil, nil, fmt.Errorf("not a FutureRemoveMarginTx")
	}
	amount, ok := new(big.Int).SetString(ftx.Amount, 10)
	if !ok || amount.Sign() <= 0 {
		err := fmt.Errorf("invalid amount: %s", ftx.Amount)
		return nil, failedReceipt(ftx.Base, err.Error()), err
	}
	if _, err := requireFuturePositionOwner(sv, ftx.PositionId, ftx.Base.FromAddress); err != nil {
		return nil, failedReceipt(ftx.Base, err.Error()), err
	}

	eng := newFutureEngineWithActivity(sv, ftx.Base.ExecutedHeight)
	if err := eng.RemoveMargin(ftx.PositionId, amount); err != nil {
		return nil, failedReceipt(ftx.Base, err.Error()), err
	}

	receipt := &Receipt{
		TxID: ftx.Base.TxId, Status: "SUCCEED",
		BlockHeight: ftx.Base.ExecutedHeight,
		Logs:        []string{fmt.Sprintf("future_remove_margin: pos=%s amount=%s", ftx.PositionId, amount)},
	}
	return nil, receipt, nil
}

                                                               
type FutureOrderCancelTxHandler struct{}

func (h *FutureOrderCancelTxHandler) Kind() string            { return "future_order_cancel" }
func (h *FutureOrderCancelTxHandler) Apply(_ *pb.AnyTx) error { return ErrNotImplemented }

func cancelFutureOpenOrderState(sv StateView, state *pb.FutureOrderState, height uint64, reason string) (*big.Int, string, error) {
	if state == nil || state.OrderId == "" {
		return nil, "", fmt.Errorf("missing order state")
	}
	if state.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN {
		return nil, "", fmt.Errorf("order not open, status=%d", state.Status)
	}
	if state.IsClose {
		return nil, "", fmt.Errorf("close order cannot be cancelled")
	}
	owner := state.OwnerAddress
	if owner == "" {
		owner = state.Owner
	}
	if owner == "" {
		return nil, "", fmt.Errorf("missing order owner")
	}
	amountBI, ok := new(big.Int).SetString(state.Amount, 10)
	if !ok {
		return nil, "", fmt.Errorf("invalid amount")
	}
	filledBI := parseFutureOrderBigInt(state.FilledBase)
	remainQty := new(big.Int).Sub(amountBI, filledBI) // safe-math-ok: 紧随其后拒绝无正剩余量；实际解冻金额经过 Ledger.Move
	if remainQty.Sign() <= 0 {
		return nil, "", fmt.Errorf("no unfilled quantity")
	}
	priceBI, ok := new(big.Int).SetString(state.Price, 10)
	if !ok {
		return nil, "", fmt.Errorf("invalid price")
	}

	eng := newFutureEngine(sv, height)
	cfg, cfgErr := eng.GetMarketConfig(state.Pair)
	if cfgErr != nil || cfg == nil || cfg.QuoteToken == "" {
		if cfgErr == nil {
			cfgErr = fmt.Errorf("missing quote token")
		}
		return nil, "", fmt.Errorf("load future market config for %s: %w", state.Pair, cfgErr)
	}
	quoteToken := cfg.QuoteToken
	refundMargin := future.CalcRequiredMargin(remainQty, priceBI, state.Leverage)
	reservedKey := keys.KeyFutureOrderReservedMargin(state.OrderId)
	reservedData, exists, getErr := sv.Get(reservedKey)
	if getErr != nil {
		return nil, "", getErr
	}
	if exists {
		decodedMargin, decodeErr := futureReservedMarginAmount(reservedData)
		if decodeErr != nil {
			return nil, "", fmt.Errorf("decode future reserved margin for cancel: %w", decodeErr)
		}
		refundMargin = decodedMargin
	}
	if refundMargin.Sign() > 0 {
		if reason == "" {
			reason = "future_order_margin_unfreeze"
		}
		if err := NewLedger(sv).Move(MoveRequest{
			Account: owner, Token: quoteToken, Amount: refundMargin,
			From: BucketOrderFrozen, To: BucketAvailable,
			Reason: reason, Height: height, Ref: state.OrderId,
		}); err != nil {
			return nil, "", err
		}
	}
	sv.Del(reservedKey)

	cancelled := proto.Clone(state).(*pb.FutureOrderState)
	cancelled.Status = pb.FutureOrderStatus_FUTURE_ORDER_CANCELLED
	if err := writeFutureOrderSnapshot(sv, cancelled, state); err != nil {
		return nil, "", err
	}
	return refundMargin, quoteToken, nil
}

func (h *FutureOrderCancelTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	ftx := tx.GetFutureOrderCancelTx()
	if ftx == nil {
		return nil, nil, fmt.Errorf("not a FutureOrderCancelTx")
	}
	if ftx.OrderId == "" {
		return nil, failedReceipt(ftx.Base, "missing order_id"), fmt.Errorf("missing order_id")
	}
	from := ftx.Base.FromAddress

	data, exists, err := sv.Get(keys.KeyFutureOrderState(ftx.OrderId))
	if err != nil || !exists || len(data) == 0 {
		return nil, failedReceipt(ftx.Base, "order not found"), fmt.Errorf("order not found: %s", ftx.OrderId)
	}
	var state pb.FutureOrderState
	if err := proto.Unmarshal(data, &state); err != nil {
		return nil, failedReceipt(ftx.Base, "failed to parse order state"), err
	}
	if state.OwnerAddress != from {
		return nil, failedReceipt(ftx.Base, "only order creator can cancel"), fmt.Errorf("permission denied")
	}
	if state.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN {
		return nil, failedReceipt(ftx.Base, "order not open"), fmt.Errorf("order not open, status=%d", state.Status)
	}
	if state.IsClose {
		return nil, failedReceipt(ftx.Base, "close order cannot be cancelled"), fmt.Errorf("close order cannot be cancelled")
	}
	amountBI := parseFutureOrderBigInt(state.Amount)
	filledBI := parseFutureOrderBigInt(state.FilledBase)
	remainQty := new(big.Int).Sub(amountBI, filledBI) // safe-math-ok: 紧随其后拒绝无正剩余量；实际解冻金额经过 Ledger.Move
	if remainQty.Sign() <= 0 {
		return nil, failedReceipt(ftx.Base, "no unfilled quantity"), fmt.Errorf("no unfilled quantity")
	}

	refundMargin, quoteToken, err := cancelFutureOpenOrderState(sv, &state, ftx.Base.ExecutedHeight, "future_order_margin_unfreeze")
	if err != nil {
		return nil, failedReceipt(ftx.Base, err.Error()), err
	}

	receipt := &Receipt{
		TxID: ftx.Base.TxId, Status: "SUCCEED",
		BlockHeight: ftx.Base.ExecutedHeight,
		Logs:        []string{fmt.Sprintf("future_order_cancel: order=%s refund=%s %s", state.OrderId, refundMargin, quoteToken)},
	}
	receipt.AppendEvent(EventOrderCancelled, map[string]string{
		"order_id":      state.OrderId,
		"owner":         from,
		"pair":          state.Pair,
		"remain_qty":    remainQty.String(),
		"refund_margin": refundMargin.String(),
		"token":         quoteToken,
	})
	return nil, receipt, nil
}
