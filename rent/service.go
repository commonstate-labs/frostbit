package rent

import (
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
)

const (
	FeeToken         = "FB"
	DefaultScanLimit = 10000
)

type Store interface {
	Get(key string) ([]byte, bool, error)
	Scan(prefix string, limit int) (map[string][]byte, error)
}

type BatchGetter interface {
	GetMany(keys []string) (map[string][]byte, error)
}

type Writer interface {
	Set(key string, value []byte)
	Del(key string)
}

type Service struct {
	store                    Store
	scanLimit                int
	skipClaimScanWithoutMark bool
	balanceRead              map[string]cachedRead
	claimMarkerRead          map[string]cachedRead
	claimTotalRead           map[string]cachedRead
	rentStateRead            map[string]cachedRentState
	stats                    *ServiceStats
}

type Option func(*Service)

type ServiceStats struct {
	BalanceReads        int
	BalanceCacheHits    int
	BalanceReadDuration time.Duration
	ClaimMarkerReads    int
	ClaimMarkerHits     int
	ClaimMarkerDuration time.Duration
	ClaimTotalReads     int
	ClaimTotalHits      int
	ClaimTotalDuration  time.Duration
	ClaimScans          int
	ClaimScanKeys       int
	ClaimScanDuration   time.Duration
}

type cachedRead struct {
	data   []byte
	exists bool
}

func WithScanLimit(limit int) Option {
	return func(s *Service) {
		if limit > 0 {
			s.scanLimit = limit
		}
	}
}

func WithClaimMarkerFastPath() Option {
	return func(s *Service) {
		s.skipClaimScanWithoutMark = true
	}
}

func withPrefetchedReads(balanceRead, claimMarkerRead, claimTotalRead map[string]cachedRead) Option {
	return func(s *Service) {
		s.balanceRead = balanceRead
		s.claimMarkerRead = claimMarkerRead
		s.claimTotalRead = claimTotalRead
	}
}

func withPrefetchedRentStates(states map[string]cachedRentState) Option {
	return func(s *Service) { s.rentStateRead = states }
}

func withServiceStats(stats *ServiceStats) Option {
	return func(s *Service) { s.stats = stats }
}

func NewService(store Store, opts ...Option) *Service {
	s := &Service{store: store, scanLimit: DefaultScanLimit}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type AssetSummary struct {
	Owner          string `json:"owner"`
	Token          string `json:"token"`
	BalanceBuckets string `json:"balance_buckets"`
	IndexedClaims  string `json:"indexed_claims"`
	Total          string `json:"total"`
}

type AccountRentStatus struct {
	Owner              string    `json:"owner"`
	Token              string    `json:"token"`
	Managed            bool      `json:"managed"`
	State              string    `json:"state"`
	AsOfHeight         uint64    `json:"as_of_height"`
	CurrentHeight      uint64    `json:"current_height"`
	LastSettledHeight  uint64    `json:"last_settled_height,omitempty"`
	LastAccruedHeight  uint64    `json:"last_accrued_height,omitempty"`
	ExpireHeight       uint64    `json:"expire_height,omitempty"`
	BlocksRemaining    uint64    `json:"blocks_remaining,omitempty"`
	NominalFB          string    `json:"nominal_fb"`
	EffectiveNominalFB string    `json:"effective_nominal_fb"`
	AvailableFB        string    `json:"available_fb"`
	SpendableFB        string    `json:"spendable_fb"`
	PendingRentFB      string    `json:"pending_rent_fb"`
	UnpaidRentFB       string    `json:"unpaid_rent_fb"`
	RequiredFundingFB  string    `json:"required_funding_fb"`
	BalanceBuckets     string    `json:"balance_buckets"`
	IndexedClaims      string    `json:"indexed_claims"`
	ExemptMin          string    `json:"exempt_min"`
	RentPerBlock       string    `json:"rent_per_block"`
	ActivationFee      string    `json:"activation_fee"`
	RecycleAccount     string    `json:"recycle_account"`
	Warning            string    `json:"warning,omitempty"`
	ExemptAsset        string    `json:"exempt_asset,omitempty"`
	ExemptAssetMin     string    `json:"exempt_asset_min,omitempty"`
	ExemptAssetTotal   string    `json:"exempt_asset_total,omitempty"`
	Work               *RentWork `json:"work,omitempty"`
}

func (s *Service) NominalFBAsset(owner string) (*AssetSummary, error) {
	return s.NominalAsset(owner, FeeToken)
}

func (s *Service) RentStatus(owner string, currentHeight uint64) (*AccountRentStatus, error) {
	policy := DefaultPolicy()
	projection, err := s.ProjectRent(owner, currentHeight, policy)
	if err != nil {
		return nil, err
	}
	summary, err := s.NominalFBAsset(owner)
	if err != nil {
		return nil, err
	}
	status := &AccountRentStatus{
		Owner:              owner,
		Token:              FeeToken,
		Managed:            projection.Managed,
		State:              "unmanaged",
		AsOfHeight:         currentHeight,
		CurrentHeight:      currentHeight,
		LastSettledHeight:  projection.LastSettledHeight,
		LastAccruedHeight:  projection.LastAccruedHeight,
		ExpireHeight:       projection.ExpireHeight,
		NominalFB:          projection.NominalFB,
		EffectiveNominalFB: projection.EffectiveNominalFB,
		AvailableFB:        projection.AvailableFB,
		SpendableFB:        projection.SpendableFB,
		PendingRentFB:      projection.PendingRentFB,
		UnpaidRentFB:       projection.UnpaidRentFB,
		RequiredFundingFB:  projection.RequiredFundingFB,
		BalanceBuckets:     summary.BalanceBuckets,
		IndexedClaims:      summary.IndexedClaims,
		ExemptMin:          policy.ExemptMin.String(),
		RentPerBlock:       policy.RentPerBlock.String(),
		ActivationFee:      DefaultActivationFeeRaw,
		RecycleAccount:     policy.RecycleAccount,
		Work:               projection.Work,
	}
	if !status.Managed {
		return status, nil
	}
	if projection.ExemptAsset != "" {
		status.ExemptAsset = projection.ExemptAsset
		status.ExemptAssetMin = projection.ExemptAssetMin
		status.ExemptAssetTotal = projection.ExemptAssetTotal
	}
	switch projection.Mode {
	case RentModeExempt:
		status.State = "exempt"
		status.Warning = "Account meets a fixed rent exemption threshold."
	case RentModeSweeping:
		status.State = "sweeping"
		status.Warning = "Account cleanup is being processed in bounded pages."
	case RentModeExemptDebt:
		status.State = "needs_funding"
		status.Warning = "Account is exempt from new accrual but must still pay previously accrued rent."
	case RentModeNeedsFunding:
		status.State = "needs_funding"
		status.Warning = "Available FB cannot cover accrued rent and the renewal buffer."
	default:
		status.State = "renting"
		if projection.ExpireHeight > currentHeight {
			status.BlocksRemaining = projection.ExpireHeight - currentHeight
		}
		status.Warning = "Below rent-exempt minimum. Spendable FB already excludes projected rent."
	}
	return status, nil
}

func (s *Service) NominalAsset(owner, token string) (*AssetSummary, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("rent: nil store")
	}
	owner = strings.TrimSpace(owner)
	token = strings.TrimSpace(token)
	if owner == "" || token == "" {
		return nil, fmt.Errorf("rent: owner/token required")
	}

	buckets, err := s.balanceBucketTotal(owner, token)
	if err != nil {
		return nil, err
	}
	claims, err := s.indexedClaimTotal(owner, token)
	if err != nil {
		return nil, err
	}
	total := new(big.Int).Add(buckets, claims)
	return &AssetSummary{
		Owner:          owner,
		Token:          token,
		BalanceBuckets: buckets.String(),
		IndexedClaims:  claims.String(),
		Total:          total.String(),
	}, nil
}

func (s *Service) balanceBucketTotal(owner, token string) (*big.Int, error) {
	key := keys.KeyBalance(owner, token)
	data, exists, ok := cachedGet(s.balanceRead, key)
	if s.stats != nil {
		s.stats.BalanceReads++
		if ok {
			s.stats.BalanceCacheHits++
		}
	}
	if !ok {
		start := time.Now()
		var err error
		data, exists, err = s.store.Get(key)
		if s.stats != nil {
			s.stats.BalanceReadDuration += time.Since(start)
		}
		if err != nil {
			return nil, err
		}
	}
	if !exists || len(data) == 0 {
		return new(big.Int), nil
	}
	var record pb.TokenBalanceRecord
	if err := proto.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("rent: parse balance record: %w", err)
	}
	total, err := tokenBalanceTotalCanonical("balance "+owner+"/"+token, record.Balance)
	if err != nil {
		return nil, err
	}
	return total, nil
}

func (s *Service) indexedClaimTotal(owner, token string) (*big.Int, error) {
	if s.skipClaimScanWithoutMark {
		key := keys.KeyAssetClaimMarker(owner, token)
		_, exists, ok := cachedGet(s.claimMarkerRead, key)
		if s.stats != nil {
			s.stats.ClaimMarkerReads++
			if ok {
				s.stats.ClaimMarkerHits++
			}
		}
		if !ok {
			start := time.Now()
			var err error
			_, exists, err = s.store.Get(key)
			if s.stats != nil {
				s.stats.ClaimMarkerDuration += time.Since(start)
			}
			if err != nil || !exists {
				return new(big.Int), err
			}
		} else if !exists {
			return new(big.Int), nil
		}
	}
	key := keys.KeyAssetClaimTotal(owner, token)
	data, exists, ok := cachedGet(s.claimTotalRead, key)
	if s.stats != nil {
		s.stats.ClaimTotalReads++
		if ok {
			s.stats.ClaimTotalHits++
		}
	}
	if !ok {
		start := time.Now()
		var err error
		data, exists, err = s.store.Get(key)
		if s.stats != nil {
			s.stats.ClaimTotalDuration += time.Since(start)
		}
		if err != nil {
			return nil, err
		}
	}
	if !exists || len(data) == 0 {
		return new(big.Int), nil
	}
	total, err := parseAmountBytes(data)
	if err != nil {
		return nil, fmt.Errorf("rent: parse claim total %s: %w", key, err)
	}
	return total, nil
}

func (s *Service) rentState(owner string) (*RentState, error) {
	if s.rentStateRead != nil {
		if cached, ok := s.rentStateRead[owner]; ok {
			return cloneRentState(cached.state), nil
		}
	}
	data, exists, err := s.store.Get(keys.KeyRentState(owner))
	if err != nil || !exists || len(data) == 0 {
		return nil, err
	}
	var st RentState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("rent: parse state %s: %w", owner, err)
	}
	return &st, nil
}

func TokenBalanceTotal(bal *pb.TokenBalance) *big.Int {
	total := new(big.Int)
	if bal == nil {
		return total
	}
	for _, raw := range []string{
		bal.Balance,
		bal.MinerLockedBalance,
		bal.LiquidLockedBalance,
		bal.WitnessLockedBalance,
		bal.LeverageLockedBalance,
		bal.OrderFrozenBalance,
	} {
		total.Add(total, parseAmountString(raw))
	}
	return total
}

func cachedGet(cache map[string]cachedRead, key string) ([]byte, bool, bool) {
	if cache == nil {
		return nil, false, false
	}
	v, ok := cache[key]
	if !ok {
		return nil, false, false
	}
	if !v.exists {
		return nil, false, true
	}
	return cloneBytes(v.data), true, true
}

func cloneBytes(data []byte) []byte {
	if data == nil {
		return nil
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out
}

func SetClaim(w Writer, owner, token, source, sourceID string, amount *big.Int) error {
	if w == nil {
		return fmt.Errorf("rent: nil writer")
	}
	reader, ok := w.(interface {
		Get(string) ([]byte, bool, error)
	})
	if !ok {
		return fmt.Errorf("rent: claim writer must support Get")
	}
	owner, token, source, sourceID = strings.TrimSpace(owner), strings.TrimSpace(token), strings.TrimSpace(source), strings.TrimSpace(sourceID)
	if owner == "" || token == "" || source == "" || sourceID == "" {
		return fmt.Errorf("rent: owner/token/source/sourceID required")
	}
	if amount == nil || amount.Sign() < 0 {
		return fmt.Errorf("rent: amount must be non-negative")
	}
	key := keys.KeyAssetClaim(owner, token, source, sourceID)
	totalKey := keys.KeyAssetClaimTotal(owner, token)
	oldAmount, oldExists, err := readAmountKey(reader, key)
	if err != nil {
		return fmt.Errorf("rent: read claim %s: %w", key, err)
	}
	oldTotal, totalExists, err := readAmountKey(reader, totalKey)
	if err != nil {
		return fmt.Errorf("rent: read claim total %s: %w", totalKey, err)
	}
	if !totalExists && oldExists {
		oldTotal.Set(oldAmount)
	}
	nextTotal := new(big.Int).Sub(oldTotal, oldAmount)
	nextTotal.Add(nextTotal, amount)
	if nextTotal.Sign() < 0 {
		nextTotal.SetInt64(0)
	}
	if amount.Sign() == 0 {
		w.Del(key)
	} else {
		MarkOwnerActivity(w, owner, OwnerMarkerClaim)
		w.Set(keys.KeyAssetClaimMarker(owner, token), []byte{1})
		w.Set(key, []byte(amount.String()))
	}
	if nextTotal.Sign() == 0 {
		w.Del(totalKey)
		return nil
	}
	w.Set(totalKey, []byte(nextTotal.String()))
	return nil
}

func readAmountKey(r interface {
	Get(string) ([]byte, bool, error)
}, key string) (*big.Int, bool, error) {
	data, exists, err := r.Get(key)
	if err != nil || !exists || len(data) == 0 {
		return new(big.Int), exists, err
	}
	amt, err := parseAmountBytes(data)
	if err != nil {
		return nil, exists, err
	}
	return amt, exists, nil
}

func parseAmountBytes(data []byte) (*big.Int, error) {
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return new(big.Int), nil
	}
	out, ok := new(big.Int).SetString(raw, 10)
	if !ok || out.Sign() < 0 {
		return nil, fmt.Errorf("invalid amount %q", raw)
	}
	return out, nil
}

func parseAmountString(raw string) *big.Int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return new(big.Int)
	}
	out, ok := new(big.Int).SetString(raw, 10)
	if !ok || out.Sign() < 0 {
		return new(big.Int)
	}
	return out
}
