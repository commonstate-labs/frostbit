package consensus

import (
	"dex/config"
	"dex/db"
	"dex/interfaces"
	"dex/keys"
	"dex/lifecycle"
	"dex/logs"
	"dex/pb"
	statedb "dex/stateDB"
	"dex/txpool"
	"dex/txverify"
	"dex/types"
	"dex/utils"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cespare/xxhash/v2"
	"google.golang.org/protobuf/proto"
)

                                                                   
type RealBlockProposer struct {
	maxBlocksPerHeight  int
	targetTxsPerBlock   int
	maxTxsPerBlock      int
	maxTxsLimitPerBlock int
	pool                *txpool.TxPool
	dbManager           *db.Manager
	vrfProvider         *utils.VRFProvider
	windowConfig        config.WindowConfig
	livenessTracker     interfaces.LivenessTracker
	livenessReporter    interfaces.LivenessHeaderReporter
	keyMgr              *utils.KeyManager
	selfAddress         string
	emergency           *lifecycle.EmergencyStop
	privateTxProvider   interfaces.PrivateTxProvider
	proposalPreflight   interfaces.ProposalPreflight
}

                                                                                    
                               
func (p *RealBlockProposer) SetEmergencyStop(e *lifecycle.EmergencyStop) {
	if p == nil {
		return
	}
	p.emergency = e
}

type proposePrefixDigestForLog struct {
	prefix     string
	keyCount   int
	xorHash    uint64
	valueBytes int64
	firstKey   string
	lastKey    string
	samples    []string
	allKeys    []string
	err        error
}

func summarizeStateValueForProposeLog(v []byte) string {
	if len(v) == 0 {
		return "len=0 xxh=0000000000000000 head="
	}
	head := v
	if len(head) > 8 {
		head = head[:8]
	}
	return fmt.Sprintf("len=%d xxh=%016x head=%x", len(v), xxhash.Sum64(v), head)
}

func xxHashKVForProposeLog(key string, value []byte) uint64 {
	d := xxhash.New()
	_, _ = d.WriteString(key)
	_, _ = d.Write(value)
	return d.Sum64()
}

func shortKeyForProposeLog(key string) string {
	const max = 96
	if len(key) <= max {
		return key
	}
	return key[:max] + "..."
}

func collectStatePrefixDigestForProposeLog(prefix string, kv map[string][]byte, sampleLimit int, allKeysLimit int) proposePrefixDigestForLog {
	out := proposePrefixDigestForLog{
		prefix:  prefix,
		samples: make([]string, 0, sampleLimit),
	}
	if len(kv) == 0 {
		return out
	}

	keysSorted := make([]string, 0, len(kv))
	for k := range kv {
		keysSorted = append(keysSorted, k)
	}
	sort.Strings(keysSorted)
	out.keyCount = len(keysSorted)
	out.firstKey = shortKeyForProposeLog(keysSorted[0])
	out.lastKey = shortKeyForProposeLog(keysSorted[len(keysSorted)-1])
	if len(keysSorted) <= allKeysLimit {
		out.allKeys = make([]string, 0, len(keysSorted))
	}

	for i, k := range keysSorted {
		v := kv[k]
		h := xxHashKVForProposeLog(k, v)
		out.xorHash ^= h
		out.valueBytes += int64(len(v))
		if i < sampleLimit {
			out.samples = append(out.samples, fmt.Sprintf("%s:%016x(len=%d)", shortKeyForProposeLog(k), h, len(v)))
		}
		if out.allKeys != nil {
			out.allKeys = append(out.allKeys, shortKeyForProposeLog(k))
		}
	}
	return out
}

func shouldTraceProposePrefix(prefix string) bool {
	switch prefix {
	case "v1_acc_order_",
		"v1_account_",
		"v1_balance_",
		"v1_frost_",
		"v1_liveness_",
		"v1_order_",
		"v1_orderstate_",
		"v1_token_":
		return true
	default:
		return false
	}
}

func stateRootTraceEnabled() bool {
	switch strings.TrimSpace(strings.ToLower(os.Getenv("DEX_DEBUG_STATE_ROOT"))) {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
	}
}

func (p *RealBlockProposer) logProposePrefixDigests(height uint64, blockID string) {
	if p.dbManager == nil || !stateRootTraceEnabled() {
		return
	}
	specs := keys.StateDBBusinessKeySpecs()
	sort.Slice(specs, func(i, j int) bool { return specs[i].Prefix < specs[j].Prefix })

	const sampleLimit = 4
	const allKeysLimit = 24
	for _, spec := range specs {
		if !shouldTraceProposePrefix(spec.Prefix) {
			continue
		}
		kv, err := p.dbManager.Scan(spec.Prefix)
		digest := collectStatePrefixDigestForProposeLog(spec.Prefix, kv, sampleLimit, allKeysLimit)
		digest.err = err
		if digest.err != nil {
			logs.Info(
				"[StateRootTrace][ProposePrefix] height=%d block=%s prefix=%s err=%v",
				height,
				blockID,
				digest.prefix,
				digest.err,
			)
			continue
		}
		logs.Info(
			"[StateRootTrace][ProposePrefix] height=%d block=%s prefix=%s keys=%d xor=%016x valueBytes=%d first=%s last=%s samples=%s",
			height,
			blockID,
			digest.prefix,
			digest.keyCount,
			digest.xorHash,
			digest.valueBytes,
			digest.firstKey,
			digest.lastKey,
			strings.Join(digest.samples, ","),
		)
		if len(digest.allKeys) > 0 {
			logs.Info(
				"[StateRootTrace][ProposePrefixKeys] height=%d block=%s prefix=%s keys=%s",
				height,
				blockID,
				digest.prefix,
				strings.Join(digest.allKeys, ","),
			)
		}
	}
}

                                              
func (p *RealBlockProposer) SetLivenessTracker(lt interfaces.LivenessTracker) {
	p.livenessTracker = lt
}

                                                                                                  
func (p *RealBlockProposer) SetLivenessReporter(reporter interfaces.LivenessHeaderReporter) {
	p.livenessReporter = reporter
}

                                                                              
                                                                       
                                                           
func (p *RealBlockProposer) SetPrivateTxProvider(provider interfaces.PrivateTxProvider) {
	if p == nil {
		return
	}
	p.privateTxProvider = provider
}

                                                                              
                                                                              
func (p *RealBlockProposer) SetProposalPreflight(preflight interfaces.ProposalPreflight) {
	if p == nil {
		return
	}
	p.proposalPreflight = preflight
}

func NewRealBlockProposer(dbManager *db.Manager, pool *txpool.TxPool, keyMgr *utils.KeyManager, selfAddress string) interfaces.BlockProposer {
	return NewRealBlockProposerWithConfig(dbManager, pool, keyMgr, selfAddress, nil)
}

func NewRealBlockProposerWithConfig(dbManager *db.Manager, pool *txpool.TxPool, keyMgr *utils.KeyManager, selfAddress string, cfg *config.Config) interfaces.BlockProposer {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	return &RealBlockProposer{
		maxBlocksPerHeight:  2,
		targetTxsPerBlock:   cfg.TxPool.TargetTxsPerBlock,
		maxTxsPerBlock:      cfg.TxPool.MaxTxsPerBlock,
		maxTxsLimitPerBlock: cfg.TxPool.MaxTxsLimitPerBlock,
		pool:                pool,
		dbManager:           dbManager,
		vrfProvider:         utils.NewVRFProvider(),
		windowConfig:        cfg.Window,
		keyMgr:              keyMgr,
		selfAddress:         selfAddress,
	}
}

func (p *RealBlockProposer) ConfigureLocalKey(keyMgr *utils.KeyManager, selfAddress string) {
	p.keyMgr = keyMgr
	p.selfAddress = selfAddress
}

type proposalNonceCandidate struct {
	tx       *pb.AnyTx
	txID     string
	addr     string
	nonce    uint64
	priority int
}

func (p *RealBlockProposer) selectNonceReadyTxs(candidates []*pb.AnyTx, limit int) []*pb.AnyTx {
	if limit <= 0 || len(candidates) == 0 {
		return nil
	}
	seenTxIDs := make(map[string]struct{}, len(candidates))
	byAddr := make(map[string][]proposalNonceCandidate)
	queues := make(map[string][]proposalNonceCandidate)
	queueKeys := make([]string, 0)
	enqueue := func(key string, candidate proposalNonceCandidate) {
		if _, exists := queues[key]; !exists {
			queueKeys = append(queueKeys, key)
		}
		queues[key] = append(queues[key], candidate)
	}

	for _, tx := range candidates {
		if tx == nil {
			continue
		}
		txID := tx.GetTxId()
		if txID != "" {
			if _, exists := seenTxIDs[txID]; exists {
				p.pool.RemoveAnyTx(txID)
				continue
			}
			seenTxIDs[txID] = struct{}{}
		}
		base := tx.GetBase()
		candidate := proposalNonceCandidate{tx: tx, txID: txID, priority: proposalTxPriority(tx)}
		if base == nil || base.FromAddress == "" {
			enqueue(fmt.Sprintf("noaddr:%06d:%s", len(queueKeys), txID), candidate)
			continue
		}
		if !proposalTxRequiresAccountNonce(tx) {
			enqueue(fmt.Sprintf("nonce-exempt:%06d:%s", len(queueKeys), txID), candidate)
			continue
		}
		candidate.addr = base.FromAddress
		candidate.nonce = base.Nonce
		byAddr[candidate.addr] = append(byAddr[candidate.addr], candidate)
	}

	for addr, list := range byAddr {
		sort.Slice(list, func(i, j int) bool {
			if list[i].nonce != list[j].nonce {
				return list[i].nonce < list[j].nonce
			}
			return list[i].txID < list[j].txID
		})
		expected := p.loadAccountNonce(addr)
		for _, candidate := range list {
			if candidate.nonce < expected {
				if candidate.txID != "" {
					p.pool.RemoveAnyTx(candidate.txID)
				}
				continue
			}
			if candidate.nonce > expected {
				break
			}
			enqueue("addr:"+addr, candidate)
			expected++
		}
	}

	sort.Strings(queueKeys)
	out := make([]*pb.AnyTx, 0, minInt(limit, len(candidates)))
	for len(out) < limit {
		bestKey := ""
		var best proposalNonceCandidate
		found := false
		for _, key := range queueKeys {
			queue := queues[key]
			if len(queue) == 0 {
				continue
			}
			head := queue[0]
			if !found || proposalCandidateLess(head, best) {
				bestKey = key
				best = head
				found = true
			}
		}
		if !found {
			break
		}
		out = append(out, best.tx)
		queues[bestKey] = queues[bestKey][1:]
	}
	return out
}

func proposalCandidateLess(a, b proposalNonceCandidate) bool {
	if a.priority != b.priority {
		return a.priority < b.priority
	}
	if a.addr != b.addr {
		return a.addr < b.addr
	}
	if a.nonce != b.nonce {
		return a.nonce < b.nonce
	}
	return a.txID < b.txID
}

func (p *RealBlockProposer) loadAccountNonce(addr string) uint64 {
	if p == nil || p.dbManager == nil || addr == "" {
		return 0
	}
	data, err := p.dbManager.Get(keys.KeyAccount(addr))
	if err != nil || len(data) == 0 {
		return 0
	}
	var account pb.Account
	if err := proto.Unmarshal(data, &account); err != nil {
		logs.Debug("[RealBlockProposer] decode account nonce failed addr=%s err=%v", addr, err)
		return 0
	}
	return account.Nonce
}

func proposalTxPriority(anyTx *pb.AnyTx) int {
	if anyTx == nil {
		return 100
	}
	switch anyTx.GetContent().(type) {
	case *pb.AnyTx_FrostVaultDkgCommitTx,
		*pb.AnyTx_FrostVaultDkgShareTx,
		*pb.AnyTx_FrostVaultDkgComplaintTx,
		*pb.AnyTx_FrostVaultDkgRevealTx,
		*pb.AnyTx_FrostVaultDkgValidationSignedTx,
		*pb.AnyTx_FrostVaultTransitionSignedTx,
		*pb.AnyTx_CertSignTx:
		return 0
	default:
		return 100
	}
}

func proposalTxRequiresAccountNonce(anyTx *pb.AnyTx) bool { return pb.TxRequiresAccountNonce(anyTx) }

func proposalContext(parentID string, height uint64, proposer types.NodeID, window, maxTxs int) interfaces.ProposalContext {
	return interfaces.ProposalContext{
		ParentID: parentID,
		Height:   height,
		Proposer: proposer,
		Window:   window,
		MaxTxs:   maxTxs,
	}
}

const (
	maxPrivateCandidatesPerProposal        = 4
	maxPrivatePreflightAttemptsPerProposal = 1
	maxProjectedOrdersPerBook              = 4096
	minPrivateTransactionSlots             = 2
	reservedPrivateTransactionSlots        = 100
)

func clampPrivateProposalLimits(limits interfaces.PrivateProposalLimits) (int, int, error) {
	if limits.MaxTransactions < minPrivateTransactionSlots {
		return 0, 0, fmt.Errorf(
			"private max transactions %d is below the required candidate minimum %d",
			limits.MaxTransactions,
			minPrivateTransactionSlots,
		)
	}
	if limits.MaxOrdersPerBook <= 0 {
		return 0, 0, fmt.Errorf("private max orders per book must be positive, got %d", limits.MaxOrdersPerBook)
	}
	                                                                          
	                                                                         
	                                                                          
	return limits.MaxTransactions, minInt(limits.MaxOrdersPerBook, maxProjectedOrdersPerBook), nil
}

func leadingProtocolTransactionCount(normal []*pb.AnyTx) (int, bool) {
	count := 0
	seenOrdinary := false
	for _, tx := range normal {
		if proposalTxPriority(tx) == 0 {
			if seenOrdinary {
				return 0, false
			}
			count++
			continue
		}
		seenOrdinary = true
	}
	return count, true
}

func cloneProposalTransactions(txs []*pb.AnyTx) ([]*pb.AnyTx, error) {
	if len(txs) == 0 {
		return nil, nil
	}
	cloned := make([]*pb.AnyTx, 0, len(txs))
	for i, tx := range txs {
		if tx == nil {
			return nil, fmt.Errorf("public prefix transaction %d is nil", i)
		}
		copyTx, ok := proto.Clone(tx).(*pb.AnyTx)
		if !ok || copyTx == nil {
			return nil, fmt.Errorf("clone public prefix transaction %d", i)
		}
		cloned = append(cloned, copyTx)
	}
	return cloned, nil
}

func (p *RealBlockProposer) validatePrivateBatch(ctx interfaces.ProposalContext, batch *interfaces.PrivateTxBatch) (string, error) {
	if batch == nil || len(batch.Transactions) == 0 {
		return "", fmt.Errorf("private transaction batch is empty")
	}
	if ctx.PrivateTxLimit <= 0 || len(batch.Transactions) > ctx.PrivateTxLimit {
		return "", fmt.Errorf("private transaction batch size %d exceeds reserved limit %d", len(batch.Transactions), ctx.PrivateTxLimit)
	}
	if batch.PublicPrefixCount != ctx.PublicPrefixCount || batch.PublicPrefixHash != ctx.PublicPrefixHash {
		return "", fmt.Errorf(
			"private transaction batch public prefix binding %d/%s does not match proposal %d/%s",
			batch.PublicPrefixCount,
			batch.PublicPrefixHash,
			ctx.PublicPrefixCount,
			ctx.PublicPrefixHash,
		)
	}
	if len(batch.InsertAfterPublic) != 0 {
		if len(batch.InsertAfterPublic) != len(batch.Transactions) {
			return "", fmt.Errorf(
				"private transaction batch has %d insertion points for %d transactions",
				len(batch.InsertAfterPublic),
				len(batch.Transactions),
			)
		}
		previous := -1
		for i, insertAfter := range batch.InsertAfterPublic {
			if insertAfter < 0 || insertAfter > ctx.PublicPrefixCount {
				return "", fmt.Errorf(
					"private transaction %d insertion point %d is outside public prefix [0,%d]",
					i,
					insertAfter,
					ctx.PublicPrefixCount,
				)
			}
			if insertAfter < previous {
				return "", fmt.Errorf(
					"private transaction %d insertion point %d precedes prior point %d",
					i,
					insertAfter,
					previous,
				)
			}
			previous = insertAfter
		}
	}

	chainID := pb.GetDefaultChainID()
	seenTxIDs := make(map[string]struct{}, len(batch.Transactions))
	sender := ""
	expectedNonce := uint64(0)
	for i, tx := range batch.Transactions {
		if tx == nil || !proposalTxRequiresAccountNonce(tx) {
			return "", fmt.Errorf("private transaction %d must be an account-nonce transaction", i)
		}
		base := tx.GetBase()
		if base == nil || base.FromAddress == "" {
			return "", fmt.Errorf("private transaction %d is missing sender", i)
		}
		if base.TargetHeight != ctx.Height {
			return "", fmt.Errorf("private transaction %d target_height=%d, want %d", i, base.TargetHeight, ctx.Height)
		}
		if base.OnlyProposer != string(ctx.Proposer) {
			return "", fmt.Errorf("private transaction %d only_proposer=%q, want %q", i, base.OnlyProposer, ctx.Proposer)
		}
		if sender == "" {
			sender = base.FromAddress
			expectedNonce = p.loadAccountNonce(sender)
		} else if base.FromAddress != sender {
			return "", fmt.Errorf("private transaction %d sender=%q differs from %q", i, base.FromAddress, sender)
		}
		if base.Nonce != expectedNonce {
			return "", fmt.Errorf("private transaction %d nonce=%d, want %d", i, base.Nonce, expectedNonce)
		}
		if expectedNonce == ^uint64(0) {
			return "", fmt.Errorf("private transaction %d uses unadvanceable max nonce", i)
		}
		expectedNonce++

		txID := tx.GetTxId()
		if txID == "" {
			return "", fmt.Errorf("private transaction %d has empty tx_id", i)
		}
		if _, exists := seenTxIDs[txID]; exists {
			return "", fmt.Errorf("private transaction batch contains duplicate tx_id %s", txID)
		}
		seenTxIDs[txID] = struct{}{}

		                                                                   
		                                                                       
		                                                                          
		                                                                      
		if chainID != 0 {
			if err := txverify.Verify(tx, chainID); err != nil {
				return "", fmt.Errorf("private transaction %d rejected: %w", i, err)
			}
		}
	}
	return sender, nil
}

func (p *RealBlockProposer) pruneExpiredProposalCandidates(candidates []*pb.AnyTx, height uint64) []*pb.AnyTx {
	if len(candidates) == 0 {
		return nil
	}
	out := make([]*pb.AnyTx, 0, len(candidates))
	for _, tx := range candidates {
		if tx == nil {
			continue
		}
		if proposalTxRequiresAccountNonce(tx) {
			base := tx.GetBase()
			if base != nil && base.TargetHeight != 0 && base.TargetHeight < height {
				if txID := tx.GetTxId(); txID != "" {
					p.pool.RemoveAnyTx(txID)
				}
				continue
			}
		}
		out = append(out, tx)
	}
	return out
}

func filterProposalPoolCandidates(candidates []*pb.AnyTx, height uint64, proposer types.NodeID) []*pb.AnyTx {
	if len(candidates) == 0 {
		return nil
	}
	out := make([]*pb.AnyTx, 0, len(candidates))
	for _, tx := range candidates {
		if tx == nil || pb.ValidateTxExecutionGuard(tx, height, string(proposer)) != nil {
			continue
		}
		out = append(out, tx)
	}
	return out
}

func hasPublicNonceConflict(candidates []*pb.AnyTx, batch *interfaces.PrivateTxBatch) bool {
	if batch == nil || len(batch.Transactions) == 0 {
		return false
	}
	firstBase := batch.Transactions[0].GetBase()
	lastBase := batch.Transactions[len(batch.Transactions)-1].GetBase()
	if firstBase == nil || lastBase == nil || firstBase.FromAddress == "" ||
		lastBase.FromAddress != firstBase.FromAddress || lastBase.Nonce < firstBase.Nonce {
		                                                                          
		                                                 
		return true
	}
	sender := firstBase.FromAddress
	firstNonce, lastNonce := firstBase.Nonce, lastBase.Nonce
	for _, tx := range candidates {
		if tx == nil || !proposalTxRequiresAccountNonce(tx) {
			continue
		}
		if base := tx.GetBase(); base != nil && base.FromAddress == sender &&
			base.Nonce >= firstNonce && base.Nonce <= lastNonce {
			return true
		}
	}
	return false
}

func mergePrivateTransactionsAtPrefix(normal []*pb.AnyTx, private []*pb.AnyTx, publicPrefixCount, limit int) ([]*pb.AnyTx, bool) {
	if len(private) == 0 || limit <= 0 || len(private) > limit {
		return normal, false
	}
	if publicPrefixCount < 0 || publicPrefixCount > len(normal) || publicPrefixCount+len(private) > limit {
		return normal, false
	}
	protocolCount, prioritySorted := leadingProtocolTransactionCount(normal)
	if !prioritySorted || publicPrefixCount < protocolCount {
		                                                                   
		                                                                      
		                                           
		return normal, false
	}

	tailLimit := minInt(len(normal), limit-len(private))
	body := make([]*pb.AnyTx, 0, tailLimit+len(private))
	body = append(body, normal[:publicPrefixCount]...)
	body = append(body, private...)
	body = append(body, normal[publicPrefixCount:tailLimit]...)
	return body, true
}

                                                                              
                                                                            
                                                                               
                                                                                
                                                                                
                                                                              
func mergePrivateTransactionBatch(normal []*pb.AnyTx, batch *interfaces.PrivateTxBatch, publicPrefixCount, limit int) ([]*pb.AnyTx, bool) {
	if batch == nil || len(batch.Transactions) == 0 {
		return normal, false
	}
	if len(batch.InsertAfterPublic) == 0 {
		return mergePrivateTransactionsAtPrefix(normal, batch.Transactions, publicPrefixCount, limit)
	}
	if len(batch.InsertAfterPublic) != len(batch.Transactions) || limit <= 0 || len(batch.Transactions) > limit {
		return normal, false
	}
	if publicPrefixCount < 0 || publicPrefixCount > len(normal) {
		return normal, false
	}
	protocolCount, prioritySorted := leadingProtocolTransactionCount(normal)
	if !prioritySorted || publicPrefixCount < protocolCount {
		return normal, false
	}
	previous := -1
	for _, insertAfter := range batch.InsertAfterPublic {
		if insertAfter < protocolCount || insertAfter > publicPrefixCount || insertAfter < previous {
			return normal, false
		}
		previous = insertAfter
	}

	publicLimit := minInt(len(normal), limit-len(batch.Transactions))
	if publicLimit < protocolCount || previous > publicLimit {
		return normal, false
	}
	body := make([]*pb.AnyTx, 0, publicLimit+len(batch.Transactions))
	privateIndex := 0
	for publicCount := 0; publicCount <= publicLimit; publicCount++ {
		for privateIndex < len(batch.Transactions) && batch.InsertAfterPublic[privateIndex] == publicCount {
			body = append(body, batch.Transactions[privateIndex])
			privateIndex++
		}
		if publicCount < publicLimit {
			body = append(body, normal[publicCount])
		}
	}
	if privateIndex != len(batch.Transactions) {
		return normal, false
	}
	return body, true
}

                                                                           
                                                                               
                                                  
func mergePrivateTransactions(normal []*pb.AnyTx, private []*pb.AnyTx, limit int) ([]*pb.AnyTx, bool) {
	protocolCount, ok := leadingProtocolTransactionCount(normal)
	if !ok {
		return normal, false
	}
	return mergePrivateTransactionsAtPrefix(normal, private, protocolCount, limit)
}

func validatePrivateSimulation(batch *interfaces.PrivateTxBatch, result *interfaces.ProposalSimulationResult) error {
	if result == nil {
		return fmt.Errorf("proposal preflight returned no result")
	}
	if !result.Valid {
		return fmt.Errorf("proposal preflight rejected candidate: %s", result.Reason)
	}
	receipts := make(map[string]interfaces.ProposalSimulationReceipt, len(result.Receipts))
	for _, receipt := range result.Receipts {
		if receipt.TxID != "" {
			receipts[receipt.TxID] = receipt
		}
	}
	for _, tx := range batch.Transactions {
		txID := tx.GetTxId()
		receipt, exists := receipts[txID]
		if !exists {
			return fmt.Errorf("proposal preflight omitted private transaction receipt %s", txID)
		}
		status := strings.ToUpper(strings.TrimSpace(receipt.Status))
		if status != "SUCCEED" && status != "SUCCESS" {
			return fmt.Errorf("private transaction %s failed preflight: status=%s error=%s", txID, receipt.Status, receipt.Error)
		}
	}
	return nil
}

func clonePrivateTxBatch(batch *interfaces.PrivateTxBatch) (*interfaces.PrivateTxBatch, error) {
	if batch == nil {
		return nil, fmt.Errorf("clone nil private transaction batch")
	}
	cloned := &interfaces.PrivateTxBatch{
		ID:                batch.ID,
		Transactions:      make([]*pb.AnyTx, len(batch.Transactions)),
		InsertAfterPublic: append([]int(nil), batch.InsertAfterPublic...),
		PublicPrefixHash:  batch.PublicPrefixHash,
		PublicPrefixCount: batch.PublicPrefixCount,
	}
	for i, tx := range batch.Transactions {
		if tx == nil {
			return nil, fmt.Errorf("clone private transaction %d: nil transaction", i)
		}
		copyTx, ok := proto.Clone(tx).(*pb.AnyTx)
		if !ok || copyTx == nil {
			return nil, fmt.Errorf("clone private transaction %d", i)
		}
		cloned.Transactions[i] = copyTx
	}
	return cloned, nil
}

func (p *RealBlockProposer) ProposeBlock(parentID string, height uint64, proposer types.NodeID, window int) (*types.Block, error) {
	if p.emergency.IsStopped() {                                                                                    
		return nil, lifecycle.ErrEmergencyStop
	}
	pendingCount := p.pool.PendingLen()
	effectiveLimit := p.adaptiveTxLimit(pendingCount)
	ctx := proposalContext(parentID, height, proposer, window, effectiveLimit)

	scanLimit := effectiveLimit
	if pendingCount > scanLimit {
		scanLimit = pendingCount
	}
	poolCandidates := p.pruneExpiredProposalCandidates(p.pool.GetPendingTxsLimit(scanLimit), height)
	normalCandidates := filterProposalPoolCandidates(poolCandidates, height, proposer)
	                                                                    
	                                                                          
	                                                   
	normalBody := p.selectNonceReadyTxs(normalCandidates, effectiveLimit)
	var (
		preparedCandidates []*interfaces.PrivateTxBatch
		providerSender     string
		publicPrefixCount  int
	)
	if p.privateTxProvider != nil {
		providerSender = strings.TrimSpace(p.privateTxProvider.SenderAddress())
		maxPrivateTxs, maxOrdersPerBook, limitsErr := clampPrivateProposalLimits(p.privateTxProvider.Limits())
		protocolCount, prioritySorted := leadingProtocolTransactionCount(normalBody)
		switch {
		case providerSender == "":
			logs.Warn("[RealBlockProposer] Private proposal skipped at height=%d parent=%s: provider sender is empty", height, parentID)
		case limitsErr != nil:
			logs.Warn("[RealBlockProposer] Private proposal policy skipped at height=%d parent=%s: %v", height, parentID, limitsErr)
		case !prioritySorted:
			logs.Warn("[RealBlockProposer] Private proposal skipped at height=%d: ordinary candidate is not priority sorted", height)
		case p.proposalPreflight == nil:
			logs.Warn("[RealBlockProposer] Private proposal skipped at height=%d: proposal preflight is not configured", height)
		default:
			                                                                
			                                                                      
			                                                                   
			                                                                       
			ctx.PrivateTxLimit = minInt(
				reservedPrivateTransactionSlots,
				minInt(maxPrivateTxs, effectiveLimit-protocolCount),
			)
			if ctx.PrivateTxLimit < minPrivateTransactionSlots {
				logs.Debug(
					"[RealBlockProposer] Private proposal skipped at height=%d: only %d slots remain after %d protocol-priority transactions",
					height,
					ctx.PrivateTxLimit,
					protocolCount,
				)
				break
			}

			                                                                       
			                                                                  
			                                                                  
			publicPrefixCount = minInt(len(normalBody), effectiveLimit-ctx.PrivateTxLimit)
			if publicPrefixCount < protocolCount {
				logs.Warn(
					"[RealBlockProposer] Private proposal skipped at height=%d: public prefix %d would displace %d protocol-priority transactions",
					height,
					publicPrefixCount,
					protocolCount,
				)
				break
			}
			publicPrefix := normalBody[:publicPrefixCount]
			ctx.PublicPrefixCount = publicPrefixCount
			ctx.PublicPrefixHash = txpool.ComputeTxsHash(publicPrefix)

			projectedPrefix, cloneErr := cloneProposalTransactions(publicPrefix)
			if cloneErr != nil {
				logs.Warn("[RealBlockProposer] Private public-prefix clone failed at height=%d parent=%s: %v", height, parentID, cloneErr)
				break
			}
			market, projectionErr := p.proposalPreflight.ProjectSpotPrefix(ctx, projectedPrefix, maxOrdersPerBook, providerSender)
			if projectionErr != nil {
				logs.Warn("[RealBlockProposer] Private public-prefix projection failed at height=%d parent=%s: %v", height, parentID, projectionErr)
				break
			}
			if market == nil {
				logs.Warn("[RealBlockProposer] Private public-prefix projection returned no market at height=%d parent=%s", height, parentID)
				break
			}
			if market.PublicPrefixCount != ctx.PublicPrefixCount || market.PublicPrefixHash != ctx.PublicPrefixHash {
				logs.Warn(
					"[RealBlockProposer] Private public-prefix projection binding mismatch at height=%d: market=%d/%s proposal=%d/%s",
					height,
					market.PublicPrefixCount,
					market.PublicPrefixHash,
					ctx.PublicPrefixCount,
					ctx.PublicPrefixHash,
				)
				break
			}

			orderedPublic, cloneErr := cloneProposalTransactions(publicPrefix)
			if cloneErr != nil {
				logs.Warn("[RealBlockProposer] Private ordered-public clone failed at height=%d parent=%s: %v", height, parentID, cloneErr)
				break
			}
			var prepared []*interfaces.PrivateTxBatch
			var prepareErr error
			if candidateProvider, ok := p.privateTxProvider.(interfaces.PrivateTxCandidateProvider); ok {
				prepared, prepareErr = candidateProvider.PrepareCandidates(ctx, orderedPublic, market)
			} else {
				batch, err := p.privateTxProvider.Prepare(ctx, orderedPublic, market)
				prepareErr = err
				if batch != nil {
					prepared = []*interfaces.PrivateTxBatch{batch}
				}
			}
			if prepareErr != nil {
				logs.Warn("[RealBlockProposer] Private candidate preparation failed at height=%d parent=%s: %v", height, parentID, prepareErr)
				break
			}
			if len(prepared) == 0 {
				break
			}
			if len(prepared) > maxPrivateCandidatesPerProposal {
				logs.Debug(
					"[RealBlockProposer] Private provider returned %d candidates at height=%d; limiting structural inspection to %d",
					len(prepared),
					height,
					maxPrivateCandidatesPerProposal,
				)
				prepared = prepared[:maxPrivateCandidatesPerProposal]
			}
			preparedCandidates = prepared
		}
	}
	if len(normalBody) == 0 && len(preparedCandidates) == 0 {
		logs.Debug("[RealBlockProposer] Generating empty block at height %d window %d", height, window)
	}

	keyMgr := p.keyMgr
	if !keyMgr.HasPrivateKey() {
		logs.Error("[RealBlockProposer] Private key not initialized")
		return nil, fmt.Errorf("private key not initialized")
	}
	vrfOutput, vrfProof, err := keyMgr.GenerateVRF(height,
		window,
		parentID,
		proposer,
	)
	if err != nil {
		logs.Error("[RealBlockProposer] Failed to generate VRF: %v", err)
		return nil, err
	}

	pubKeyBytes, err := keyMgr.BLSPublicKeyBytes()
	if err != nil {
		logs.Error("[RealBlockProposer] Failed to get public key: %v", err)
		return nil, err
	}

	                                                                           
	stateFingerprint, stateFP := statedb.ReadFingerprintFromProvider(p.dbManager)

	                                              
	                                                                  
	                                                                                 
	                                                                          
	var sampledReports []uint32
	var offlineReports []uint32
	var onlineReports []uint32
	if p.livenessReporter != nil {
		if atomic, ok := p.livenessReporter.(interfaces.LivenessOnlineHeaderReporter); ok {
			sampled, offline, online := atomic.GetLivenessReportsWithOnline()
			sampledReports = append([]uint32(nil), sampled...)
			offlineReports = append([]uint32(nil), offline...)
			onlineReports = append([]uint32(nil), online...)
		} else if atomic, ok := p.livenessReporter.(interfaces.LivenessAtomicHeaderReporter); ok {
			sampled, offline := atomic.GetLivenessReports()
			sampledReports = append([]uint32(nil), sampled...)
			offlineReports = append([]uint32(nil), offline...)
		} else {
			if sampled := p.livenessReporter.GetSampledReports(); len(sampled) > 0 {
				sampledReports = append([]uint32(nil), sampled...)
			}
			if reports := p.livenessReporter.GetOfflineReports(); len(reports) > 0 {
				offlineReports = append([]uint32(nil), reports...)
			}
		}
	}

	buildCandidate := func(body []*pb.AnyTx) (*types.Block, *pb.Block, error) {
		txsHash := txpool.ComputeTxsHash(body)
		shortTxs := p.pool.ConcatFirst8Bytes(body)
		blockHeader := types.BlockHeader{
			Height:         height,
			ParentID:       parentID,
			Proposer:       string(proposer),
			Miner:          fmt.Sprintf(keys.KeyNode()+"%s", proposer),
			Window:         window,
			StateRoot:      string(stateFingerprint),
			TxHash:         txsHash,
			VRFProof:       vrfProof,
			VRFOutput:      vrfOutput,
			PublicKey:      pubKeyBytes,
			OfflineReports: append([]uint32(nil), offlineReports...),
			SampledReports: append([]uint32(nil), sampledReports...),
			OnlineReports:  append([]uint32(nil), onlineReports...),
		}
		                                                                     
		                                                                 
		blockID := blockHeader.ComputeIDForChain(pb.GetDefaultChainID())
		minerSignature, err := signBlockProposal(keyMgr, blockID)
		if err != nil {
			return nil, nil, err
		}
		block := &types.Block{
			ID:             blockID,
			Header:         blockHeader,
			MinerSignature: append([]byte(nil), minerSignature...),
		}
		dbBlock := &pb.Block{
			BlockHash:      blockID,
			MinerSignature: append([]byte(nil), minerSignature...),
			Header: &pb.BlockHeader{
				Height:         height,
				TxsHash:        txsHash,
				PrevBlockHash:  parentID,
				Miner:          blockHeader.Miner,
				Window:         int32(window),
				StateRoot:      append([]byte(nil), stateFingerprint...),
				VrfProof:       append([]byte(nil), vrfProof...),
				VrfOutput:      append([]byte(nil), vrfOutput...),
				PublicKey:      append([]byte(nil), pubKeyBytes...),
				OfflineReports: append([]uint32(nil), offlineReports...),
				SampledReports: append([]uint32(nil), sampledReports...),
				OnlineReports:  append([]uint32(nil), onlineReports...),
			},
			Body:     append([]*pb.AnyTx(nil), body...),
			ShortTxs: append([]byte(nil), shortTxs...),
		}
		canonicalPayload, err := proto.Marshal(dbBlock)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal proposal payload: %w", err)
		}
		block.CanonicalPayload = canonicalPayload
		return block, dbBlock, nil
	}

	var (
		block                    *types.Block
		dbBlock                  *pb.Block
		candidateBody            = normalBody
		privateIncluded          bool
		privatePreflightAttempts int
	)
	for candidateIndex, candidate := range preparedCandidates {
		if candidate == nil || len(candidate.Transactions) == 0 {
			logs.Debug("[RealBlockProposer] Private candidate %d omitted at height=%d: empty batch", candidateIndex, height)
			continue
		}
		ownedCandidate, cloneErr := clonePrivateTxBatch(candidate)
		if cloneErr != nil {
			logs.Warn("[RealBlockProposer] Private candidate %d clone failed at height=%d: %v", candidateIndex, height, cloneErr)
			continue
		}
		candidate = ownedCandidate
		sender, validateErr := p.validatePrivateBatch(ctx, candidate)
		if validateErr != nil {
			logs.Warn("[RealBlockProposer] Private batch %s rejected before assembly at height=%d: %v", candidate.ID, height, validateErr)
			continue
		}
		if sender != providerSender {
			logs.Warn("[RealBlockProposer] Private batch %s rejected before assembly at height=%d: sender=%q differs from provider sender=%q", candidate.ID, height, sender, providerSender)
			continue
		}
		if hasPublicNonceConflict(poolCandidates, candidate) {
			                                                                  
			                                                                    
			                                                 
			logs.Debug("[RealBlockProposer] Private batch %s omitted at height=%d: sender %s has an overlapping public nonce transaction", candidate.ID, height, sender)
			continue
		}
		merged, ok := mergePrivateTransactionBatch(normalBody, candidate, publicPrefixCount, effectiveLimit)
		if !ok {
			logs.Debug("[RealBlockProposer] Private batch %s omitted at height=%d: invalid insertion schedule or insufficient capacity", candidate.ID, height)
			continue
		}

		candidateBlock, candidateDBBlock, buildErr := buildCandidate(merged)
		if buildErr != nil {
			logs.Warn(
				"[RealBlockProposer] Private batch %s candidate %d failed commitment build at height=%d: %v",
				candidate.ID,
				candidateIndex,
				height,
				buildErr,
			)
			continue
		}
		if privatePreflightAttempts >= maxPrivatePreflightAttemptsPerProposal {
			break
		}
		privatePreflightAttempts++

		var preflightErr error
		if p.proposalPreflight == nil {
			preflightErr = fmt.Errorf("proposal preflight is not configured")
		} else {
			cloned, ok := proto.Clone(candidateDBBlock).(*pb.Block)
			if !ok || cloned == nil {
				preflightErr = fmt.Errorf("clone candidate for proposal preflight")
			} else {
				result, simulateErr := p.proposalPreflight.Simulate(cloned)
				if simulateErr != nil {
					preflightErr = simulateErr
				} else if validationErr := validatePrivateSimulation(candidate, result); validationErr != nil {
					preflightErr = validationErr
				} else if validationBatch, cloneErr := clonePrivateTxBatch(candidate); cloneErr != nil {
					preflightErr = cloneErr
				} else if validationErr := p.privateTxProvider.ValidateCandidate(ctx, validationBatch, result); validationErr != nil {
					preflightErr = validationErr
				}
			}
		}
		if preflightErr != nil {
			logs.Warn(
				"[RealBlockProposer] Private batch %s candidate %d failed full candidate preflight at height=%d: %v",
				candidate.ID,
				candidateIndex,
				height,
				preflightErr,
			)
			break
		}

		block = candidateBlock
		dbBlock = candidateDBBlock
		candidateBody = merged
		privateIncluded = true
		break
	}
	if block == nil || dbBlock == nil {
		block, dbBlock, err = buildCandidate(normalBody)
		if err != nil {
			return nil, err
		}
		candidateBody = normalBody
		privateIncluded = false
	}

	logs.Info(
		"[StateRootTrace][Propose] height=%d proposer=%s parent=%s fp=%016x len=%d txs=%d private=%v",
		height, proposer, parentID, stateFP, len(stateFingerprint), len(candidateBody), privateIncluded,
	)
	debugKeys := []struct {
		name string
		key  string
	}{
		{name: "frost_active_miners", key: keys.KeyFrostActiveMiners()},
		{name: "total_miner_stake", key: keys.KeyTotalMinerStake()},
		{name: "liveness_global", key: keys.KeyLivenessGlobal()},
		{name: "cert_global", key: keys.KeyCertGlobal()},
		{name: "cert_pending", key: keys.KeyCertPending()},
	}
	for _, dk := range debugKeys {
		val, err := p.dbManager.Get(dk.key)
		if err != nil {
			logs.Info("[StateRootTrace][ProposeKey] height=%d name=%s key=%s err=%v", height, dk.name, dk.key, err)
			continue
		}
		logs.Info("[StateRootTrace][ProposeKey] height=%d name=%s key=%s %s", height, dk.name, dk.key, summarizeStateValueForProposeLog(val))
	}
	p.logProposePrefixDigests(height, block.ID)
	p.cacheBlock(block.ID, dbBlock)

	logs.Info("[RealBlockProposer] Proposer:%s Proposed block %s with %d txs at height %d window %d",
		proposer, block.ID, len(candidateBody), height, window)

	return block, nil
}

func (p *RealBlockProposer) ShouldPropose(
	nodeID types.NodeID,
	window int,
	currentBlocks int,
	currentHeight int,
	proposeHeight int,
	lastBlockTime time.Time,
	parentID string,
) bool {
	if currentHeight != proposeHeight-1 {
		logs.Debug("[RealBlockProposer] Height check failed: currentHeight=%d, proposeHeight=%d",
			currentHeight, proposeHeight)
		return false
	}

	                                                                          
	                                                                           
	                                                                           
	                                                                   
	if p.livenessTracker == nil {
		logs.Warn("[RealBlockProposer] Liveness registry unavailable; skipping proposal")
		return false
	}
	localAddress := p.selfAddress
	if localAddress == "" && p.keyMgr != nil {
		localAddress = p.keyMgr.GetAddress()
	}
	if localAddress == "" || !p.livenessTracker.IsActive(localAddress) {
		logs.Warn("[RealBlockProposer] Miner %s not active (observer/probation/frozen), skipping proposal", localAddress)
		return false
	}

	                                                                    
	                                                                      
	                                                 
	allowedBlocks := p.maxBlocksPerHeight
	if allowedBlocks <= 0 {
		allowedBlocks = 2
	}
	if currentBlocks >= allowedBlocks {
		return false
	}

	pendingCount := len(p.pool.GetPendingAnyTx())
	maxTxsPerBlock := p.maxTxsPerBlock
	if maxTxsPerBlock <= 0 {
		maxTxsPerBlock = 2500
	}

	                                                                        
	if currentBlocks > 0 && window == 0 && pendingCount >= maxTxsPerBlock {
		return false
	}

	                                                                         
	                                                                           
	                                                                        
	                              
	lastWindow := 3
	if p.windowConfig.Enabled && len(p.windowConfig.Stages) > 0 {
		lastWindow = len(p.windowConfig.Stages) - 1
	}
	if parentID == "" {
		if currentHeight == 0 {
			parentID = "genesis"
		} else {
			parentID = fmt.Sprintf("block-%d", currentHeight)
		}
	}

	eligible := false
	if !p.windowConfig.Enabled || len(p.windowConfig.Stages) == 0 {
		eligible = int(nodeID.Last2Mod100())%100 < 5
	} else {
		if window < 0 || window >= len(p.windowConfig.Stages) {
			logs.Debug("[RealBlockProposer] Invalid window %d", window)
			return false
		}

		keyMgr := p.keyMgr
		if !keyMgr.HasPrivateKey() {
			logs.Error("[RealBlockProposer] Private key not initialized")
			return false
		}
		vrfOutput, _, err := keyMgr.GenerateVRF(uint64(proposeHeight),
			window,
			parentID,
			nodeID,
		)
		if err != nil {
			logs.Error("[RealBlockProposer] Failed to generate VRF for proposal check: %v", err)
			return false
		}

		addr := p.selfAddress
		if addr == "" {
			addr = keyMgr.GetAddress()
		}
		shouldPropose, threshold, err := evaluateProposalEligibility(
			p.dbManager,
			p.windowConfig,
			addr,
			nodeID,
			window,
			vrfOutput,
		)
		if err != nil {
			logs.Warn("[RealBlockProposer] Cannot verify local proposal eligibility: %v", err)
			return false
		}
		eligible = shouldPropose
		if eligible {
			stage := p.windowConfig.Stages[window]
			logs.Debug("[RealBlockProposer] Node %s should propose at window %d (threshold=%.6f, expected=%.0f)",
				nodeID, window, threshold, stage.ExpectedProposers)
		}
	}
	if !eligible {
		return false
	}

	if pendingCount < 1 && window < lastWindow {
		hasPrivate := false
		if p.privateTxProvider != nil {
			hasPrivate = p.privateTxProvider.HasPending(proposalContext(
				parentID,
				uint64(proposeHeight),
				nodeID,
				window,
				p.adaptiveTxLimit(pendingCount),
			))
		}
		if !hasPrivate {
			return false
		}
	}

	return true
}

func (p *RealBlockProposer) adaptiveTxLimit(pendingCount int) int {
	hardLimit := p.maxTxsLimitPerBlock
	if p.maxTxsPerBlock > 0 && (hardLimit <= 0 || p.maxTxsPerBlock < hardLimit) {
		hardLimit = p.maxTxsPerBlock
	}
	if hardLimit <= 0 {
		hardLimit = 2500
	}
	target := p.targetTxsPerBlock
	if target <= 0 || target > hardLimit {
		target = hardLimit
	}
	limit := target
	switch {
	case pendingCount >= target*8:
		limit = minInt(hardLimit, target*2)
	case pendingCount >= target*4:
		limit = minInt(hardLimit, maxInt((target*3)/2, target+1))
	}
	return limit
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var (
	blockCache               = make(map[string]*pb.Block)
	blockCacheHeights        = make(map[string]uint64)
	sealedBlockCache         = make(map[string]struct{})
	blockCacheMu             sync.RWMutex
	blockCacheAcceptedHeight atomic.Uint64
)

const MaxFutureBlockCacheWindow uint64 = 100

func maxCachedFutureHeight(acceptedHeight uint64) uint64 {
	if ^uint64(0)-acceptedHeight < MaxFutureBlockCacheWindow {
		return ^uint64(0)
	}
	return acceptedHeight + MaxFutureBlockCacheWindow
}

func IsTooFarFutureHeight(height, acceptedHeight uint64) bool {
	return height > maxCachedFutureHeight(acceptedHeight)
}

func SetBlockCacheAcceptedHeight(height uint64) int {
	blockCacheAcceptedHeight.Store(height)
	return CleanupBlockCacheAboveHeight(maxCachedFutureHeight(height))
}

func (p *RealBlockProposer) cacheBlock(blockID string, block *pb.Block) {
	if block == nil || block.BlockHash != blockID {
		return
	}
	CacheBlock(block)
}

func GetCachedBlock(blockID string) (*pb.Block, bool) {
	blockCacheMu.RLock()
	defer blockCacheMu.RUnlock()
	block, exists := blockCache[blockID]
	return block, exists
}

                                                                       
                                                                      
                                                                              
                                                                    
func GetSealedCachedBlock(blockID string) (*pb.Block, bool) {
	blockCacheMu.RLock()
	defer blockCacheMu.RUnlock()
	if _, sealed := sealedBlockCache[blockID]; !sealed {
		return nil, false
	}
	block, exists := blockCache[blockID]
	return block, exists && block != nil
}

func CacheBlock(block *pb.Block) bool {
	if block == nil || block.BlockHash == "" {
		return false
	}
	if block.Header != nil && IsTooFarFutureHeight(block.Header.Height, blockCacheAcceptedHeight.Load()) {
		return false
	}
	blockCacheMu.Lock()
	defer blockCacheMu.Unlock()
	if _, sealed := sealedBlockCache[block.BlockHash]; sealed {
		return true
	}
	blockCache[block.BlockHash] = block
	if block.Header != nil {
		blockCacheHeights[block.BlockHash] = block.Header.Height
	}
	return true
}

                                                                             
                                                                                
func SealCachedBlock(block *pb.Block) bool {
	if block == nil || block.BlockHash == "" {
		return false
	}
	if block.Header != nil && IsTooFarFutureHeight(block.Header.Height, blockCacheAcceptedHeight.Load()) {
		return false
	}
	blockCacheMu.Lock()
	defer blockCacheMu.Unlock()
	blockCache[block.BlockHash] = block
	if block.Header != nil {
		blockCacheHeights[block.BlockHash] = block.Header.Height
	}
	sealedBlockCache[block.BlockHash] = struct{}{}
	return true
}

                                                                              
                                                             
func ReplaceCachedBlock(block *pb.Block) bool {
	return replaceCachedBlock(block)
}

func replaceCachedBlock(block *pb.Block) bool {
	if block == nil || block.BlockHash == "" {
		return false
	}
	if block.Header != nil && IsTooFarFutureHeight(block.Header.Height, blockCacheAcceptedHeight.Load()) {
		return false
	}
	blockCacheMu.Lock()
	defer blockCacheMu.Unlock()
	blockCache[block.BlockHash] = block
	if block.Header != nil {
		blockCacheHeights[block.BlockHash] = block.Header.Height
	}
	sealedBlockCache[block.BlockHash] = struct{}{}
	return true
}

                                                                               
                                                                      
func RemoveCachedBlock(blockID string) {
	blockCacheMu.Lock()
	defer blockCacheMu.Unlock()
	if _, sealed := sealedBlockCache[blockID]; sealed {
		return
	}
	removeCachedBlockLocked(blockID)
}

func removeCachedBlockLocked(blockID string) {
	delete(blockCache, blockID)
	delete(blockCacheHeights, blockID)
	delete(sealedBlockCache, blockID)
}

func CleanupBlockCacheBelowHeight(height uint64) int {
	blockCacheMu.Lock()
	defer blockCacheMu.Unlock()

	toDelete := make([]string, 0)
	for blockID, h := range blockCacheHeights {
		if h < height {
			toDelete = append(toDelete, blockID)
		}
	}

	for _, blockID := range toDelete {
		delete(blockCache, blockID)
		delete(blockCacheHeights, blockID)
		delete(sealedBlockCache, blockID)
	}

	return len(toDelete)
}

func CleanupBlockCacheAboveHeight(height uint64) int {
	blockCacheMu.Lock()
	defer blockCacheMu.Unlock()

	toDelete := make([]string, 0)
	for blockID, h := range blockCacheHeights {
		if h > height {
			toDelete = append(toDelete, blockID)
		}
	}

	for _, blockID := range toDelete {
		delete(blockCache, blockID)
		delete(blockCacheHeights, blockID)
		delete(sealedBlockCache, blockID)
	}

	return len(toDelete)
}

func GetBlockCacheSize() int {
	blockCacheMu.RLock()
	defer blockCacheMu.RUnlock()
	return len(blockCache)
}
