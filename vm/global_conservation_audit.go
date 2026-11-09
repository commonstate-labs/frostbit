package vm

import (
	"dex/future"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/protocol"
	"fmt"
	"math/big"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const conservationAuditDetailLimit = 8

type GlobalConservationAuditStats struct {
	InputOps             int
	CoalescedOps         int
	TokenOps             int
	BalanceOps           int
	PositionOps          int
	InsuranceOps         int
	ChallengeOps         int
	OtherOps             int
	OldValueInline       int
	OldValueFallbacks    int
	OldValueReadOps      int
	OldValueReadMisses   int
	NewValueBytes        int
	OldValueBytes        int
	OldValueReadDuration time.Duration
	CoalesceDuration     time.Duration
	LoopDuration         time.Duration
}

type conservationDeltaDetail struct {
	balance, position, insurance, challenge, supplyAdj *big.Int
	samples                                            []conservationDeltaSample
}

type conservationDeltaSample struct {
	source string
	key    string
	delta  *big.Int
}

type conservationAuditShard struct {
	stats                 GlobalConservationAuditStats
	deltas                map[string]*big.Int
	details               map[string]*conservationDeltaDetail
	invalidBalanceRecords []string
}

                                                                                         
func AuditGlobalConservationDiff(diff []WriteOp, height uint64, readFn ReadThroughFn) {
	_ = AuditGlobalConservationDiffWithStats(diff, height, readFn)
}

func AuditGlobalConservationDiffWithStats(diff []WriteOp, height uint64, readFn ReadThroughFn) GlobalConservationAuditStats {
	stats := GlobalConservationAuditStats{InputOps: len(diff)}
	coalesceAt := time.Now()
	diff = coalesceWriteOpsForConservation(diff)
	stats.CoalescedOps = len(diff)
	stats.CoalesceDuration = time.Since(coalesceAt)
	deltas := make(map[string]*big.Int)
	details := make(map[string]*conservationDeltaDetail)
	var invalidBalanceRecords []string
	loopAt := time.Now()
	futureQuotes := buildFutureMarketQuoteAuditIndex(diff, readFn)
	for _, shard := range runConservationAuditShards(diff, readFn, futureQuotes) {
		stats.addShard(shard.stats)
		mergeConservationDeltas(deltas, shard.deltas)
		mergeConservationDetails(details, shard.details)
		invalidBalanceRecords = append(invalidBalanceRecords, shard.invalidBalanceRecords...)
	}
	stats.LoopDuration = time.Since(loopAt)
	if len(invalidBalanceRecords) > 0 {
		sort.Strings(invalidBalanceRecords)
		logs.Error("[VM][GlobalConservationDiff][InvalidBalanceRecord] height=%d count=%d first=%s", height, len(invalidBalanceRecords), invalidBalanceRecords[0])
		panic(fmt.Sprintf("[VM][GlobalConservationDiff] INVALID_BALANCE_RECORD count=%d first=%s height=%d", len(invalidBalanceRecords), invalidBalanceRecords[0], height))
	}

	tokens := make([]string, 0, len(deltas))
	for token := range deltas {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	for _, token := range tokens {
		delta := deltas[token]
		if delta.Sign() != 0 {
			logs.Error("[VM][GlobalConservationDiff][Trace] height=%d token=%s unbalanced delta=%s detail{%s}",
				height, token, delta.String(), formatConservationDeltaDetail(details[token]))
			panic(fmt.Sprintf("[VM][GlobalConservationDiff] UNBALANCED token=%s delta=%s height=%d", token, delta.String(), height))
		}
	}
	return stats
}

func runConservationAuditShards(diff []WriteOp, readFn ReadThroughFn, futureQuotes map[string]string) []conservationAuditShard {
	workers := conservationAuditWorkerCount(len(diff))
	if workers <= 1 {
		return []conservationAuditShard{processConservationAuditRange(diff, readFn, futureQuotes)}
	}
	shards := make([]conservationAuditShard, workers)
	var wg sync.WaitGroup
	chunk := (len(diff) + workers - 1) / workers
	for i := 0; i < workers; i++ {
		start := i * chunk
		end := start + chunk
		if end > len(diff) {
			end = len(diff)
		}
		if start >= len(diff) {
			shards = shards[:i]
			break
		}
		wg.Add(1)
		go func(idx, from, to int) {
			defer wg.Done()
			shards[idx] = processConservationAuditRange(diff[from:to], readFn, futureQuotes)
		}(i, start, end)
	}
	wg.Wait()
	return shards
}

func conservationAuditWorkerCount(n int) int {
	if n < 2048 {
		return 1
	}
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		return 1
	}
	maxByOps := (n + 2047) / 2048
	if workers > maxByOps {
		workers = maxByOps
	}
	if workers > n {
		workers = n
	}
	return workers
}

func processConservationAuditRange(diff []WriteOp, readFn ReadThroughFn, futureQuotes map[string]string) conservationAuditShard {
	shard := conservationAuditShard{
		deltas:  make(map[string]*big.Int),
		details: make(map[string]*conservationDeltaDetail),
	}
	for _, op := range diff {
		processConservationAuditOp(op, readFn, futureQuotes, &shard.stats, shard.deltas, shard.details, &shard.invalidBalanceRecords)
	}
	return shard
}

func processConservationAuditOp(op WriteOp, readFn ReadThroughFn, futureQuotes map[string]string, stats *GlobalConservationAuditStats, deltas map[string]*big.Int, details map[string]*conservationDeltaDetail, invalidBalanceRecords *[]string) {
	stats.NewValueBytes += len(op.Value)
	plan := planConservationAuditOp(op.Key, futureQuotes)
	switch plan.kind {
	case conservationAuditToken:
		stats.TokenOps++
	case conservationAuditBalance:
		stats.BalanceOps++
	case conservationAuditPosition:
		stats.PositionOps++
	case conservationAuditInsurance:
		stats.InsuranceOps++
	case conservationAuditChallenge:
		stats.ChallengeOps++
	default:
		stats.OtherOps++
	}

	var oldValue []byte
	if plan.needsOldValue {
		oldValue = oldValueForConservationOp(op, readFn, stats)
	}
	result := evaluateConservationAuditTransition(conservationAuditTransitionInput{
		Key:          op.Key,
		NewValue:     op.Value,
		OldValue:     oldValue,
		FutureQuotes: futureQuotes,
		plan:         plan,
	})
	if result.invalidBalanceRecord != "" {
		*invalidBalanceRecords = append(*invalidBalanceRecords, result.invalidBalanceRecord)
		return
	}
	for _, contribution := range result.contributions {
		addConservationDelta(deltas, details, contribution.token, contribution.source, contribution.key, contribution.delta)
	}
}

func oldValueForConservationOp(op WriteOp, readFn ReadThroughFn, stats *GlobalConservationAuditStats) []byte {
	oldValue, usedInline, readDur, didRead := oldValueFromOpForAuditStats(op, readFn)
	stats.OldValueBytes += len(oldValue)
	if usedInline {
		stats.OldValueInline++
	} else if !op.HasOldValue {
		stats.OldValueFallbacks++
	}
	if didRead {
		stats.OldValueReadOps++
		stats.OldValueReadDuration += readDur
		if len(oldValue) == 0 {
			stats.OldValueReadMisses++
		}
	}
	return oldValue
}

func (s *GlobalConservationAuditStats) addShard(shard GlobalConservationAuditStats) {
	s.TokenOps += shard.TokenOps
	s.BalanceOps += shard.BalanceOps
	s.PositionOps += shard.PositionOps
	s.InsuranceOps += shard.InsuranceOps
	s.ChallengeOps += shard.ChallengeOps
	s.OtherOps += shard.OtherOps
	s.OldValueInline += shard.OldValueInline
	s.OldValueFallbacks += shard.OldValueFallbacks
	s.OldValueReadOps += shard.OldValueReadOps
	s.OldValueReadMisses += shard.OldValueReadMisses
	s.NewValueBytes += shard.NewValueBytes
	s.OldValueBytes += shard.OldValueBytes
	s.OldValueReadDuration += shard.OldValueReadDuration
}

func mergeConservationDeltas(dst, src map[string]*big.Int) {
	tokens := make([]string, 0, len(src))
	for token := range src {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	for _, token := range tokens {
		addBig(dst, token, src[token])
	}
}

func mergeConservationDetails(dst, src map[string]*conservationDeltaDetail) {
	tokens := make([]string, 0, len(src))
	for token := range src {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	for _, token := range tokens {
		sd := src[token]
		if sd == nil {
			continue
		}
		dd := dst[token]
		if dd == nil {
			dd = newConservationDeltaDetail()
			dst[token] = dd
		}
		dd.balance.Add(dd.balance, sd.balance)
		dd.position.Add(dd.position, sd.position)
		dd.insurance.Add(dd.insurance, sd.insurance)
		dd.challenge.Add(dd.challenge, sd.challenge)
		dd.supplyAdj.Add(dd.supplyAdj, sd.supplyAdj)
		samples := append([]conservationDeltaSample(nil), sd.samples...)
		sort.Slice(samples, func(i, j int) bool {
			if cmp := absBig(samples[i].delta).Cmp(absBig(samples[j].delta)); cmp != 0 {
				return cmp > 0
			}
			if samples[i].source != samples[j].source {
				return samples[i].source < samples[j].source
			}
			return samples[i].key < samples[j].key
		})
		for _, sample := range samples {
			dd.addSample(sample.source, sample.key, sample.delta)
		}
	}
}

func coalesceWriteOpsForConservation(diff []WriteOp) []WriteOp {
	if len(diff) <= 1 {
		return diff
	}
	last := make(map[string]WriteOp, len(diff))
	firstOld := make(map[string]WriteOp, len(diff))
	seen := make(map[string]struct{}, len(diff))
	keys := make([]string, 0, len(diff))
	for _, op := range diff {
		if _, ok := seen[op.Key]; !ok {
			seen[op.Key] = struct{}{}
			keys = append(keys, op.Key)
		}
		if op.HasOldValue {
			if _, ok := firstOld[op.Key]; !ok {
				firstOld[op.Key] = op
			}
		}
		last[op.Key] = op
	}
	sort.Strings(keys)
	out := make([]WriteOp, 0, len(keys))
	for _, key := range keys {
		op := last[key]
		if old, ok := firstOld[key]; ok {
			op.OldValue = cloneBytes(old.OldValue)
			op.OldExists = old.OldExists
			op.HasOldValue = true
			op.OldValueAuthoritative = old.OldValueAuthoritative
		}
		out = append(out, op)
	}
	return out
}

func readOldValue(readFn ReadThroughFn, key string) []byte {
	if readFn == nil {
		return nil
	}
	data, err := readFn(key)
	if err != nil || len(data) == 0 {
		return nil
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	return cp
}

func oldValueFromOp(op WriteOp, readFn ReadThroughFn) []byte {
	if op.HasOldValue {
		if !op.OldExists || len(op.OldValue) == 0 {
			return nil
		}
		return cloneBytes(op.OldValue)
	}
	return readOldValue(readFn, op.Key)
}

func oldValueFromOpForAuditStats(op WriteOp, readFn ReadThroughFn) ([]byte, bool, time.Duration, bool) {
	if op.HasOldValue {
		if !op.OldExists || len(op.OldValue) == 0 {
			return nil, true, 0, false
		}
		return cloneBytes(op.OldValue), true, 0, false
	}
	readAt := time.Now()
	oldValue := readOldValue(readFn, op.Key)
	return oldValue, false, time.Since(readAt), readFn != nil
}

func addConservationDelta(deltas map[string]*big.Int, details map[string]*conservationDeltaDetail, token, source, key string, delta *big.Int) {
	if token == "" || delta == nil || delta.Sign() == 0 {
		return
	}
	addBig(deltas, token, delta)
	detail := details[token]
	if detail == nil {
		detail = newConservationDeltaDetail()
		details[token] = detail
	}
	detail.add(source, key, delta)
}

func newConservationDeltaDetail() *conservationDeltaDetail {
	return &conservationDeltaDetail{
		balance:   new(big.Int),
		position:  new(big.Int),
		insurance: new(big.Int),
		challenge: new(big.Int),
		supplyAdj: new(big.Int),
	}
}

func (d *conservationDeltaDetail) add(source, key string, delta *big.Int) {
	switch source {
	case "balance":
		d.balance.Add(d.balance, delta)
	case "position":
		d.position.Add(d.position, delta)
	case "insurance":
		d.insurance.Add(d.insurance, delta)
	case "challenge":
		d.challenge.Add(d.challenge, delta)
	case "supplyAdj":
		d.supplyAdj.Add(d.supplyAdj, delta)
	}
	d.addSample(source, key, delta)
}

func (d *conservationDeltaDetail) addSample(source, key string, delta *big.Int) {
	sample := conservationDeltaSample{source: source, key: key, delta: new(big.Int).Set(delta)}
	if len(d.samples) < conservationAuditDetailLimit {
		d.samples = append(d.samples, sample)
		return
	}
	minIdx := 0
	minAbs := absBig(d.samples[0].delta)
	for i := 1; i < len(d.samples); i++ {
		if cur := absBig(d.samples[i].delta); cur.Cmp(minAbs) < 0 {
			minIdx, minAbs = i, cur
		}
	}
	if absBig(sample.delta).Cmp(minAbs) > 0 {
		d.samples[minIdx] = sample
	}
}

func formatConservationDeltaDetail(d *conservationDeltaDetail) string {
	if d == nil {
		return ""
	}
	samples := append([]conservationDeltaSample(nil), d.samples...)
	sort.Slice(samples, func(i, j int) bool {
		return absBig(samples[i].delta).Cmp(absBig(samples[j].delta)) > 0
	})
	parts := make([]string, 0, len(samples))
	for _, s := range samples {
		parts = append(parts, fmt.Sprintf("%s:%s key=%s", s.source, s.delta.String(), shortenAuditKey(s.key)))
	}
	return fmt.Sprintf("balance=%s position=%s insurance=%s challenge=%s supplyAdj=%s top=[%s]",
		d.balance.String(), d.position.String(), d.insurance.String(), d.challenge.String(), d.supplyAdj.String(), strings.Join(parts, "; "))
}

func absBig(v *big.Int) *big.Int {
	if v == nil {
		return new(big.Int)
	}
	return new(big.Int).Abs(v)
}

func shortenAuditKey(key string) string {
	const max = 120
	if len(key) <= max {
		return key
	}
	return key[:max] + "..."
}

func balanceRecordTotal(data []byte) *big.Int {
	rec, err := parseBalanceRecordForAudit(data)
	if err != nil {
		return new(big.Int)
	}
	return balanceRecordTotalValue(rec)
}

func parseBalanceRecordForAudit(data []byte) (*pb.TokenBalanceRecord, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var rec pb.TokenBalanceRecord
	if err := unmarshalProtoCompat(data, &rec); err != nil {
		return nil, fmt.Errorf("decode balance record: %w", err)
	}
	return &rec, nil
}

func consistentBalanceRecordToken(newRecord, oldRecord *pb.TokenBalanceRecord) (string, error) {
	var newToken, oldToken string
	if newRecord != nil {
		newToken = newRecord.Token
	}
	if oldRecord != nil {
		oldToken = oldRecord.Token
	}
	if newToken != "" && oldToken != "" && newToken != oldToken {
		return "", fmt.Errorf("balance record token mismatch: new=%s old=%s", newToken, oldToken)
	}
	if newToken != "" {
		return newToken, nil
	}
	if oldToken != "" {
		return oldToken, nil
	}
	return "", fmt.Errorf("missing balance record token")
}

func balanceRecordTotalValue(rec *pb.TokenBalanceRecord) *big.Int {
	if rec == nil || rec.Balance == nil {
		return new(big.Int)
	}
	total := new(big.Int)
	total.Add(total, parseBigOrZero(rec.Balance.Balance))
	total.Add(total, parseBigOrZero(rec.Balance.OrderFrozenBalance))
	total.Add(total, parseBigOrZero(rec.Balance.MinerLockedBalance))
	total.Add(total, parseBigOrZero(rec.Balance.WitnessLockedBalance))
	total.Add(total, parseBigOrZero(rec.Balance.LiquidLockedBalance))
	total.Add(total, parseBigOrZero(rec.Balance.LeverageLockedBalance))
	return total
}

func tokenSupplyDelta(newData, oldData []byte) *big.Int {
	var newToken, oldToken pb.Token
	if len(newData) > 0 {
		if err := unmarshalProtoCompat(newData, &newToken); err != nil {
			return new(big.Int)
		}
	}
	if len(oldData) > 0 {
		_ = unmarshalProtoCompat(oldData, &oldToken)
	}
	delta := parseBigOrZero(newToken.TotalSupply)
	delta.Sub(delta, parseBigOrZero(oldToken.TotalSupply))
	return delta
}

func addPositionConservationDelta(deltas map[string]*big.Int, details map[string]*conservationDeltaDetail, key string, data []byte, sign int64, futureQuotes map[string]string) {
	token, delta := positionDeltaWithMarketQuotes(data, sign, futureQuotes)
	addConservationDelta(deltas, details, token, "position", key, delta)
}

func positionDelta(data []byte, sign int64) (string, *big.Int) {
	return positionDeltaWithMarketQuotes(data, sign, nil)
}

func positionDeltaWithMarketQuotes(data []byte, sign int64, futureQuotes map[string]string) (string, *big.Int) {
	if len(data) == 0 {
		return "", new(big.Int)
	}
	var pos pb.FuturePositionState
	if err := unmarshalProtoCompat(data, &pos); err != nil || pos.Status != int32(future.StatusOpen) {
		return "", new(big.Int)
	}
	margin := parseBigOrZero(pos.Margin)
	if margin.Sign() <= 0 {
		return "", new(big.Int)
	}
	quote := futureQuotes[pos.Pair]
	if quote == "" {
		quote = quoteTokenFromPair(pos.Pair)
	}
	if quote == "" {
		return "", new(big.Int)
	}
	if sign < 0 {
		margin.Neg(margin)
	}
	return quote, margin
}

func challengeEscrowDelta(data []byte, sign int64) *big.Int {
	stake := activeChallengeStake(data)
	if stake.Sign() <= 0 {
		return new(big.Int)
	}
	if sign < 0 {
		stake.Neg(stake)
	}
	return stake
}

func activeChallengeStake(data []byte) *big.Int {
	if len(data) == 0 {
		return new(big.Int)
	}
	var rec pb.ChallengeRecord
	if err := unmarshalProtoCompat(data, &rec); err != nil {
		return new(big.Int)
	}
	if rec.Finalized || rec.Status == pb.ChallengeStatus_CHALLENGE_FINALIZED {
		return new(big.Int)
	}
	stake := parseBigOrZero(rec.StakeAmount)
	if stake.Sign() <= 0 {
		return new(big.Int)
	}
	return stake
}

func quoteTokenFromPair(pair string) string {
	if strings.Count(pair, "_") != 1 {
		return ""
	}
	_, quote, _ := strings.Cut(pair, "_")
	return quote
}

func buildFutureMarketQuoteAuditIndex(diff []WriteOp, readFn ReadThroughFn) map[string]string {
	quotes := make(map[string]string)
	pairs := make(map[string]struct{})
	configPrefix := keys.KeyFutureMarketConfigPrefix()
	insurancePrefix := keys.KeyFutureInsuranceFund("")
	collectPositionPair := func(data []byte) {
		if len(data) == 0 {
			return
		}
		var pos pb.FuturePositionState
		if unmarshalProtoCompat(data, &pos) == nil && pos.Pair != "" {
			pairs[pos.Pair] = struct{}{}
		}
	}
	for _, op := range diff {
		switch {
		case strings.HasPrefix(op.Key, configPrefix):
			pair := strings.TrimPrefix(op.Key, configPrefix)
			if identity, err := protocol.DecodeMarketIdentity(pair, op.Value); err == nil {
				quotes[pair] = identity.QuoteToken
			}
		case strings.HasPrefix(op.Key, keys.KeyFuturePositionPrefix()):
			collectPositionPair(op.Value)
			if op.HasOldValue {
				collectPositionPair(op.OldValue)
			}
		case strings.HasPrefix(op.Key, insurancePrefix):
			pair := strings.TrimPrefix(op.Key, insurancePrefix)
			if pair != "" && !strings.HasPrefix(pair, "shortfall_") {
				pairs[pair] = struct{}{}
			}
		}
	}
	for pair := range pairs {
		if quotes[pair] != "" {
			continue
		}
		if legacyQuote := quoteTokenFromPair(pair); legacyQuote != "" {
			quotes[pair] = legacyQuote
			continue
		}
		if readFn != nil {
			if data, err := readFn(keys.KeyFutureMarketConfig(pair)); err == nil {
				if identity, decodeErr := protocol.DecodeMarketIdentity(pair, data); decodeErr == nil {
					quotes[pair] = identity.QuoteToken
					continue
				}
			}
		}
		quotes[pair] = quoteTokenFromPair(pair)
	}
	return quotes
}

func bytesBig(data []byte) *big.Int {
	if len(data) == 0 {
		return new(big.Int)
	}
	return new(big.Int).SetBytes(data)
}

func addBig(totals map[string]*big.Int, token string, v *big.Int) {
	if cur, ok := totals[token]; ok {
		cur.Add(cur, v)
		return
	}
	totals[token] = new(big.Int).Set(v)
}
