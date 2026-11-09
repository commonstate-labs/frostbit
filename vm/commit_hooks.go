package vm

import (
	"bytes"
	"crypto/sha256"
	"dex/config"
	dbpkg "dex/db"
	"dex/frost/artifact"
	chainpkg "dex/frost/chain"
	"dex/frost/chain/evm"
	iface "dex/interfaces"
	"dex/keys"
	"dex/liveness"
	"dex/logs"
	"dex/pb"
	"dex/rent"
	"dex/utils"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/cespare/xxhash/v2"
	"google.golang.org/protobuf/proto"
)

type minerIndexProvider interface {
	GetMinerIndex(address string) (uint32, bool)
}

const (
	dkgRetryFallbackWindowBlocks uint64 = 5
	dkgTimeoutRetryGraceBlocks   uint64 = 5
	maxDkgRestartsPerEpoch       uint32 = 3
	livenessSlashPoolAccount            = "__liveness_slash_pool__"
)

func dkgTransitionWindows(ts *pb.VaultTransitionState) (uint64, uint64, uint64) {
	commitWindow := dkgRetryFallbackWindowBlocks
	if ts != nil && ts.DkgCommitDeadline >= ts.TriggerHeight && ts.DkgCommitDeadline > 0 {
		commitWindow = ts.DkgCommitDeadline - ts.TriggerHeight + 1
	}
	sharingWindow := dkgRetryFallbackWindowBlocks
	if ts != nil && ts.DkgSharingDeadline > ts.DkgCommitDeadline {
		sharingWindow = ts.DkgSharingDeadline - ts.DkgCommitDeadline
	}
	disputeWindow := dkgRetryFallbackWindowBlocks
	if ts != nil && ts.DkgDisputeDeadline > ts.DkgSharingDeadline {
		disputeWindow = ts.DkgDisputeDeadline - ts.DkgSharingDeadline
	}
	return commitWindow, sharingWindow, disputeWindow
}

func resetDkgTransitionForRetry(ts *pb.VaultTransitionState, triggerHeight uint64) {
	commitWindow, sharingWindow, disputeWindow := dkgTransitionWindows(ts)
	nextAttempt := ts.DkgAttempt + 1
	if nextAttempt < 2 {
		nextAttempt = 2
	}
	commitDeadline := triggerHeight + commitWindow - 1
	ts.TriggerHeight = triggerHeight
	ts.DkgAttempt = nextAttempt
	ts.DkgStatus = DKGStatusNotStarted
	ts.DkgSessionId = fmt.Sprintf("%s_%d_%d_%d", ts.Chain, ts.VaultId, ts.EpochId, nextAttempt)
	ts.DkgCommitDeadline = commitDeadline
	ts.DkgSharingDeadline = commitDeadline + sharingWindow
	ts.DkgDisputeDeadline = ts.DkgSharingDeadline + disputeWindow
	ts.ValidationStatus = "NOT_STARTED"
	ts.NewGroupPubkey = nil
	ts.ValidationMsgHash = nil
	ts.MigrationJobId = ""
	ts.DkgN = uint32(len(ts.NewCommitteeMembers))
}

                                                                             
                                                                         
                                                                               
func canRestartDkg(ts *pb.VaultTransitionState) bool {
	if ts == nil {
		return false
	}
	attempt := ts.DkgAttempt
	if attempt == 0 {
		attempt = 1
	}
	return attempt-1 < maxDkgRestartsPerEpoch
}

func markDkgTransitionDraining(ts *pb.VaultTransitionState) {
	if ts == nil {
		return
	}
	clearDkgAttemptOutputs(ts)
	ts.DkgStatus = DKGStatusFailed
	ts.ValidationStatus = "FAILED"
	ts.Lifecycle = VaultLifecycleDraining
}

func frostCommitteeDigest(members []string) []byte {
	h := sha256.New()
	var size [8]byte
	for _, member := range members {
		binary.BigEndian.PutUint64(size[:], uint64(len(member)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(member))
	}
	return h.Sum(nil)
}

func frostCommitteeReplacementCount(current, target []string) int {
	targetSet := make(map[string]struct{}, len(target))
	for _, member := range target {
		targetSet[member] = struct{}{}
	}
	changed := 0
	for _, member := range current {
		if _, exists := targetSet[member]; !exists {
			changed++
		}
	}
	return changed
}

func persistFrostVaultState(sv StateView, vault *pb.FrostVaultState) error {
	data, err := MarshalProto(vault)
	if err != nil {
		return err
	}
	sv.Set(keys.KeyFrostVaultState(vault.Chain, vault.VaultId), data)
	return nil
}

func clearPendingFrostCommittee(vault *pb.FrostVaultState) bool {
	if vault == nil || (len(vault.PendingCommitteeDigest) == 0 && vault.PendingCommitteeSinceHeight == 0) {
		return false
	}
	vault.PendingCommitteeDigest = nil
	vault.PendingCommitteeSinceHeight = 0
	return true
}

func shouldAutoRetryFailedDkg(ts *pb.VaultTransitionState) bool {
	return ts != nil && ts.DkgStatus == DKGStatusFailed && ts.Lifecycle != VaultLifecycleDraining
}

func isRecoverablePendingInitialDkg(vault *pb.FrostVaultState, ts *pb.VaultTransitionState) bool {
	return vault != nil && ts != nil &&
		vault.Status == VaultStatusPending && len(vault.GroupPubkey) == 0 &&
		ts.DkgStatus == DKGStatusFailed && ts.Lifecycle == VaultLifecycleDraining &&
		len(ts.OldGroupPubkey) == 0 && ts.EpochId == vault.KeyEpoch
}

                                                                                
                                                                               
                                                                            
                                                                              
                                                                         
func pendingInitialDkgRecoveryCommittee(
	top *pb.FrostActiveMiners,
	cfg *pb.FrostVaultConfig,
	ts *pb.VaultTransitionState,
	initialCommittee []string,
) ([]string, error) {
	if top == nil || cfg == nil || ts == nil {
		return nil, fmt.Errorf("missing pending initial DKG recovery input")
	}

	targetSize := len(ts.OldCommitteeMembers)
	if configuredSize := int(cfg.CommitteeSize); configuredSize > targetSize {
		targetSize = configuredSize
	}
	if targetSize == 0 || targetSize < int(ts.DkgThresholdT) {
		return nil, fmt.Errorf("invalid recovery committee size: target=%d threshold=%d", targetSize, ts.DkgThresholdT)
	}

	survivorSet := make(map[string]struct{}, len(ts.NewCommitteeMembers))
	for _, member := range ts.NewCommitteeMembers {
		if member != "" {
			survivorSet[member] = struct{}{}
		}
	}
	excluded := make(map[string]bool)
	for _, member := range ts.DkgDisqualifiedMembers {
		excluded[member] = true
	}
	                                                                            
	                                                                          
	                                                                              
	for _, baseline := range [][]string{initialCommittee, ts.OldCommitteeMembers} {
		for _, member := range baseline {
			if member == "" {
				continue
			}
			if _, survived := survivorSet[member]; !survived {
				excluded[member] = true
			}
		}
	}

	if err := validateFrostCandidateSnapshot(top); err != nil {
		return nil, err
	}
	activeSet := make(map[string]struct{}, len(top.Addresses))
	for _, address := range top.Addresses {
		if !excluded[address] {
			activeSet[address] = struct{}{}
		}
	}
	if len(activeSet) < targetSize {
		return nil, fmt.Errorf("insufficient eligible active miners: have=%d need=%d excluded=%d", len(activeSet), targetSize, len(excluded))
	}

	recoveryCfg := proto.Clone(cfg).(*pb.FrostVaultConfig)
	recoveryCfg.CommitteeSize = uint32(targetSize)
	candidates, err := expectedTransitionCommitteeExcluding(top, recoveryCfg, excluded)
	if err != nil {
		return nil, err
	}

	committee := make([]string, 0, targetSize)
	selected := make(map[string]struct{}, targetSize)
	                                                                             
	                                                                   
	for _, member := range ts.NewCommitteeMembers {
		if len(committee) == targetSize {
			break
		}
		if _, active := activeSet[member]; !active || excluded[member] {
			continue
		}
		if _, duplicate := selected[member]; duplicate {
			continue
		}
		selected[member] = struct{}{}
		committee = append(committee, member)
	}
	for _, member := range candidates {
		if len(committee) == targetSize {
			break
		}
		if excluded[member] {
			continue
		}
		if _, duplicate := selected[member]; duplicate {
			continue
		}
		selected[member] = struct{}{}
		committee = append(committee, member)
	}
	if len(committee) != targetSize {
		return nil, fmt.Errorf("deterministic recovery assignment too small: have=%d need=%d", len(committee), targetSize)
	}
	return committee, nil
}

func recoverPendingInitialDkg(
	sv StateView,
	transitionKey string,
	vault *pb.FrostVaultState,
	ts *pb.VaultTransitionState,
	top *pb.FrostActiveMiners,
	cfg *pb.FrostVaultConfig,
	height uint64,
) (bool, error) {
	if !isRecoverablePendingInitialDkg(vault, ts) || !canRestartDkg(ts) {
		return false, nil
	}
	committee, err := pendingInitialDkgRecoveryCommittee(top, cfg, ts, vault.CommitteeMembers)
	if err != nil {
		return false, err
	}

	nextAttempt := ts.DkgAttempt + 1
	if nextAttempt < 2 {
		nextAttempt = 2
	}
	signAlgo := ts.SignAlgo
	if signAlgo == pb.SignAlgo_SIGN_ALGO_UNSPECIFIED {
		signAlgo = cfg.SignAlgo
	}
	retry, err := buildAutoTransitionState(
		ts.Chain, ts.VaultId, ts.EpochId, nextAttempt,
		signAlgo, height+1,
		append([]string(nil), committee...), append([]string(nil), committee...), nil,
		cfg,
	)
	if err != nil {
		return false, err
	}
	                                                                         
	if ts.DkgThresholdT == 0 {
		return false, fmt.Errorf("pending initial DKG is missing its frozen threshold")
	}
	retry.DkgThresholdT = ts.DkgThresholdT
	retry.DkgDisqualifiedMembers = append([]string(nil), ts.DkgDisqualifiedMembers...)
	if retry.DkgN < retry.DkgThresholdT {
		return false, fmt.Errorf("recovery committee cannot satisfy fixed threshold: n=%d t=%d", retry.DkgN, retry.DkgThresholdT)
	}

	oldSessionID := ts.DkgSessionId
	if _, err := cleanupFrostDkgSessionTempState(sv, ts.Chain, ts.VaultId, ts.EpochId, oldSessionID); err != nil {
		return false, err
	}
	if err := setTransitionStateWithIndex(sv, transitionKey, retry); err != nil {
		return false, err
	}
	return true, nil
}

type vaultMaturityPolicySetter interface {
	SetVaultMaturityBlocks(uint64)
}

                                                                           
                                                                           
                                                                              
                                                                              
                                                                                
func (x *Executor) applyChainVaultMaturityPolicy(sv StateView) error {
	prefix := strings.TrimSuffix(keys.KeyFrostVaultConfig("", 0), "_0")
	rows, err := sv.Scan(prefix)
	if err != nil {
		return fmt.Errorf("scan on-chain vault maturity policy: %w", err)
	}
	if len(rows) == 0 {
		                                                                    
		                                                                      
		return nil
	}

	rowKeys := make([]string, 0, len(rows))
	for key := range rows {
		rowKeys = append(rowKeys, key)
	}
	sort.Strings(rowKeys)

	var maturity uint64
	configured := 0
	for _, key := range rowKeys {
		var cfg pb.FrostVaultConfig
		if err := unmarshalProtoCompat(rows[key], &cfg); err != nil {
			return fmt.Errorf("invalid on-chain Frost vault config %s: %w", key, err)
		}
		if strings.TrimSpace(cfg.Chain) == "" || key != keys.KeyFrostVaultConfig(cfg.Chain, 0) {
			return fmt.Errorf("invalid on-chain Frost vault config identity at %s", key)
		}
		if cfg.VaultMaturityBlocks == 0 {
			return fmt.Errorf("on-chain Frost vault maturity is zero for chain %s", cfg.Chain)
		}
		if err := config.ValidateOnChainVaultCommitteePolicy(&cfg); err != nil {
			return fmt.Errorf("invalid on-chain Frost vault config for chain %s: %w", cfg.Chain, err)
		}
		if configured == 0 {
			maturity = cfg.VaultMaturityBlocks
		} else if cfg.VaultMaturityBlocks != maturity {
			return fmt.Errorf("on-chain Frost vault maturity mismatch: chain=%s have=%d want=%d", cfg.Chain, cfg.VaultMaturityBlocks, maturity)
		}
		configured++
	}
	if configured == 0 {
		return nil
	}
	setter, ok := x.LivenessTracker.(vaultMaturityPolicySetter)
	if !ok {
		return fmt.Errorf("liveness tracker cannot apply on-chain Frost vault maturity policy")
	}
	setter.SetVaultMaturityBlocks(maturity)
	return nil
}

                                                                                                                      
                                                                                                                                                                                                    
func (x *Executor) processLivenessHooks(b *pb.Block, sv StateView) (*pb.FrostActiveMiners, bool, error) {
	if x.LivenessTracker == nil || b.Header == nil {
		return nil, false, nil
	}
	                                                                        
	                                                                         
	                                                                          
	                                                                         
	if err := x.applyChainVaultMaturityPolicy(sv); err != nil {
		return nil, false, err
	}
	minerChanged := false
	stakeChanged := false
	var latestTop *pb.FrostActiveMiners

	                                                                                                   
	for _, tx := range b.Body {
		if tx == nil {
			continue
		}
		mtx := tx.GetMinerTx()
		if mtx == nil || mtx.Base == nil {
			continue
		}
		hot := ResolveMinerHot(mtx)
		switch mtx.Op {
		case pb.OrderOp_ADD:
			accountData, exists, readErr := sv.Get(keys.KeyAccount(hot))
			if readErr != nil || !exists || len(accountData) == 0 {
				continue
			}
			var account pb.Account
			if unmarshalProtoCompat(accountData, &account) != nil || !account.IsMiner {
				continue                                        
			}
			stakeChanged = true
			stake := minerLockedStakeForLiveness(sv, mtx, hot)
			if stake > 0 {
				newIdx, err := x.LivenessTracker.RegisterMinerAtHeight(sv, hot, stake, b.Header.Height)
				if err != nil {
					if reloader, ok := x.LivenessTracker.(interface{ ReloadFromDB() }); ok {
						reloader.ReloadFromDB()
					}
					return nil, false, fmt.Errorf("register finalized miner %s in liveness: %w", hot, err)
				} else {
					minerChanged = true
					                                                       
					                                                                                 
					                                                                      
					x.attachMinerToIndex(hot, uint64(newIdx), sv)
				}
			}
		case pb.OrderOp_REMOVE:
			if _, exists, readErr := sv.Get(keys.KeyMinerExitRecord(hot)); readErr != nil || !exists {
				continue                                          
			}
			stakeChanged = true
			if err := x.LivenessTracker.RemoveMiner(sv, hot, b.Header.Height); err != nil {
				logs.Debug("[VM][Liveness] RemoveMiner %s: %v", hot, err)
			} else {
				minerChanged = true
			}
		}
	}

	                                                                           
	                                                                            
	                                                                      
	                                       
	for _, address := range x.LivenessTracker.GetActiveMiners() {
		if !x.LivenessTracker.IsActiveOrProbation(address) {
			continue
		}
		                                                                          
		                                                                          
		                                                                
		if _, marked, markerErr := sv.Get(keys.KeyMinerExitRecord(address)); markerErr != nil || !marked {
			continue
		}
		accountData, exists, err := sv.Get(keys.KeyAccount(address))
		if err != nil || !exists || len(accountData) == 0 {
			continue
		}
		var account pb.Account
		if unmarshalProtoCompat(accountData, &account) != nil {
			continue
		}
		locked, parseErr := parseBalanceStrict("liveness reconciliation miner stake", GetBalance(sv, address, "FB").MinerLockedBalance)
		if parseErr != nil {
			continue
		}
		if locked.Sign() >= 0 && locked.BitLen() <= 64 {
			if stakeTracker, ok := x.LivenessTracker.(iface.LivenessStakeTracker); ok {
				if err := stakeTracker.UpdateMinerStake(sv, address, locked.Uint64(), b.Header.Height); err != nil {
					logs.Warn("[VM][Liveness] failed to reconcile stake buckets for %s: %v", address, err)
					continue
				}
			}
		}
		if account.IsMiner && locked.Sign() > 0 {
			continue
		}
		var accountUpdate []byte
		if account.IsMiner {
			account.IsMiner = false
			accountUpdate, err = MarshalProto(&account)
			if err != nil {
				logs.Warn("[VM][Liveness] failed to marshal protocol-slashed miner %s: %v", address, err)
				continue
			}
		}
		if err := x.LivenessTracker.RemoveMiner(sv, address, b.Header.Height); err != nil {
			logs.Warn("[VM][Liveness] failed to reconcile protocol-slashed miner %s: %v", address, err)
			continue
		}
		if err := ensureMinerExitRecord(sv, address, b.Header.Height); err != nil {
			logs.Warn("[VM][Liveness] failed to persist protocol exit for miner %s: %v", address, err)
			continue
		}
		if len(accountUpdate) > 0 {
			sv.Set(keys.KeyAccount(address), accountUpdate)
		}
		minerChanged = true
		stakeChanged = true
		logs.Info("[VM][Liveness] height=%d reconciled protocol-slashed miner %s", b.Header.Height, address)
	}

	                                                                              
	                                                                          
	                                                                           
	                                                                
	                                                                                 
	                                                                                                         
	activeBeforeReports := x.LivenessTracker.GetActiveCount()
	var kicked []uint32
	var slashed []iface.LivenessSlashEvent
	if onlineTracker, ok := x.LivenessTracker.(iface.LivenessOnlineReporterTracker); ok {
		kicked, slashed = onlineTracker.ProcessBlockWithOnlineReporter(
			sv, b.Header.Height, keys.MinerAddress(b.Header.Miner),
			b.Header.SampledReports, b.Header.OfflineReports, b.Header.OnlineReports,
		)
	} else if reporterTracker, ok := x.LivenessTracker.(iface.LivenessReporterTracker); ok {
		                                                                          
		                                                                    
		kicked, slashed = reporterTracker.ProcessBlockWithReporter(
			sv,
			b.Header.Height,
			keys.MinerAddress(b.Header.Miner),
			b.Header.SampledReports,
			b.Header.OfflineReports,
		)
	} else {
		kicked, slashed = x.LivenessTracker.ProcessBlock(
			sv,
			b.Header.Height,
			b.Header.SampledReports,
			b.Header.OfflineReports,
		)
	}
	if x.LivenessTracker.GetActiveCount() != activeBeforeReports {
		minerChanged = true
	}
	vaultEligibilityChanged := false
	if eligibilityTracker, ok := x.LivenessTracker.(iface.LivenessVaultEligibilityTracker); ok {
		vaultEligibilityChanged = eligibilityTracker.VaultEligibilityChangedAt() == b.Header.Height
	}
	                                                                            
	                                                                           
	                                                                            
	if !vaultEligibilityChanged && b.Header.Height > 0 {
		if raw, exists, readErr := sv.Get(keys.KeyFrostActiveMiners()); readErr == nil && exists && len(raw) > 0 {
			var persisted pb.FrostActiveMiners
			if unmarshalProtoCompat(raw, &persisted) == nil && persisted.Height == 0 && persisted.UpdateHeight == 0 {
				vaultEligibilityChanged = true
			}
		}
	}
	for _, idx := range kicked {
		addr, ok := x.LivenessTracker.GetAddressByIndex(idx)
		if !ok {
			continue
		}
		acctKey := keys.KeyAccount(addr)
		if acctData, ok, err := sv.Get(acctKey); err == nil && ok && len(acctData) > 0 {
			var acct pb.Account
			if unmarshalProtoCompat(acctData, &acct) == nil {
				acct.IsMiner = false
				if data, err := MarshalProto(&acct); err == nil {
					sv.Set(acctKey, data)
				}
			}
		}
		if err := ensureMinerExitRecord(sv, addr, b.Header.Height); err != nil {
			logs.Warn("[VM][Liveness] failed to persist kicked-miner exit for %s: %v", addr, err)
		}
		logs.Info("[VM][Liveness] height=%d kicked miner %d (%s), Account.IsMiner=false", b.Header.Height, idx, addr)
		recordUserActivity(sv, addr, &UserActivityRecord{
			Kind: ActKindKicked, Height: b.Header.Height,
			Ref: fmt.Sprintf("idx=%d", idx),
		})
	}
	if len(kicked) > 0 {
		minerChanged = true
		stakeChanged = true
	}

	                               
	                                                         
	                                                                                                                          
	                                                                                                                                     
	  
	                                                                                                     
	                                                                                              
	                                                                                  
	                                                              
	if len(slashed) > 0 {
		proposerAddr := keys.MinerAddress(b.Header.Miner)
		filtered := slashed[:0]
		for _, ev := range slashed {
			if ev.Address != proposerAddr {
				filtered = append(filtered, ev)
			}
		}
		if len(filtered) > 0 {
			slashedAddrs := make(map[string]struct{}, len(filtered))
			for _, ev := range filtered {
				slashedAddrs[ev.Address] = struct{}{}
			}
			slashedTotal := x.applyLivenessSlashes(b, filtered, sv)
			if slashedTotal != nil && slashedTotal.Sign() > 0 {
				x.distributeSlashedFB(b, slashedTotal, slashedAddrs, sv)
				stakeChanged = true
			}
		}
	}

	                                                                                                         
	if minerChanged || vaultEligibilityChanged {
		                                                             
		                                                           
		                                                                
		                                                                 
		if dbm, ok := x.DB.(*dbpkg.Manager); ok {
			dbm.InvalidateMinerCache()
		}

		var idxProvider minerIndexProvider
		if provider, ok := x.LivenessTracker.(minerIndexProvider); ok {
			idxProvider = provider
		}

		type activeMinerEntry struct {
			address           string
			index             uint64
			hasIndex          bool
			pubKey            []byte
			maturedStake      uint64
			activeSinceHeight uint64
		}
		activeMiners := x.LivenessTracker.GetActiveMiners()
		entries := make([]activeMinerEntry, 0, len(activeMiners))
		for _, addr := range activeMiners {
			                                                                   
			                                                           
			if !x.LivenessTracker.IsActive(addr) {
				continue
			}
			snapshot := x.LivenessTracker.GetMinerSnapshot(addr)
			if snapshot == nil || snapshot.MaturedStake == 0 || b.Header.Height < snapshot.VaultEligibleHeight {
				continue
			}
			entry := activeMinerEntry{address: addr}
			entry.maturedStake = snapshot.MaturedStake
			entry.activeSinceHeight = snapshot.ActiveSinceHeight
			if idxProvider != nil {
				if idx, ok := idxProvider.GetMinerIndex(addr); ok {
					entry.index = uint64(idx)
					entry.hasIndex = true
				}
			}

			acctKey := keys.KeyAccount(addr)
			if acctData, ok, err := sv.Get(acctKey); err == nil && ok && len(acctData) > 0 {
				var acct pb.Account
				if unmarshalProtoCompat(acctData, &acct) == nil {
					if !entry.hasIndex {
						entry.index = acct.Index
						entry.hasIndex = true
					}
					if acct.PublicKeys != nil {
						entry.pubKey = pickDeterministicPubKey(acct.PublicKeys.Keys)
					}
				}
			}
			                                                                      
			                                                              
			if locked := minerLockedStakeForLiveness(sv, nil, addr); locked < entry.maturedStake {
				entry.maturedStake = locked
			}
			if entry.maturedStake == 0 {
				continue
			}
			entries = append(entries, entry)
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].maturedStake != entries[j].maturedStake {
				return entries[i].maturedStake > entries[j].maturedStake
			}
			if entries[i].activeSinceHeight != entries[j].activeSinceHeight {
				return entries[i].activeSinceHeight < entries[j].activeSinceHeight
			}
			if entries[i].hasIndex != entries[j].hasIndex {
				return entries[i].hasIndex
			}
			if entries[i].hasIndex && entries[i].index != entries[j].index {
				return entries[i].index < entries[j].index
			}
			return entries[i].address < entries[j].address
		})
		eligibleCount := len(entries)
		if len(entries) > config.VaultCandidateSnapshotLimit {
			entries = entries[:config.VaultCandidateSnapshotLimit]
		}

		top := &pb.FrostActiveMiners{
			Height:             b.Header.Height,
			Indices:            make([]uint64, len(entries)),
			Addresses:          make([]string, len(entries)),
			PublicKeys:         make([][]byte, len(entries)),
			MaturedStakes:      make([]uint64, len(entries)),
			ActiveSinceHeights: make([]uint64, len(entries)),
			EligibleCount:      uint32(eligibleCount),
		}
		for i, entry := range entries {
			top.Addresses[i] = entry.address
			if entry.hasIndex {
				top.Indices[i] = entry.index
			} else {
				top.Indices[i] = uint64(i)
			}
			top.PublicKeys[i] = entry.pubKey
			top.MaturedStakes[i] = entry.maturedStake
			top.ActiveSinceHeights[i] = entry.activeSinceHeight
		}
		top.UpdateHeight = b.Header.Height
		if err := validateFrostCandidateSnapshot(top); err != nil {
			return nil, false, fmt.Errorf("build VaultEligible snapshot: %w", err)
		}
		if data, err := MarshalProto(top); err == nil {
			sv.Set(keys.KeyFrostActiveMiners(), data)
			logs.Info("[VM][Liveness] height=%d refreshed VaultEligible candidates: stored=%d eligible=%d active=%d limit=%d", b.Header.Height, len(entries), eligibleCount, x.LivenessTracker.GetActiveCount(), config.VaultCandidateSnapshotLimit)
			latestTop = top
		}
	}

	                                                                                                            
	if stakeChanged || minerChanged {
		activeMinersForStake := x.LivenessTracker.GetActiveMiners()
		totalStake := new(big.Int)
		debugEnabled := stateRootDebugEnabled()
		balanceXor := uint64(0)
		missingBalance := 0
		parseErr := 0
		samples := make([]string, 0, 6)
		prevTotalStake := ""
		qLen, qCap := 0, 0
		if debugEnabled {
			if oldTotal, ok, err := sv.Get(keys.KeyTotalMinerStake()); err != nil {
				prevTotalStake = "err=" + err.Error()
			} else if !ok || len(oldTotal) == 0 {
				prevTotalStake = "<empty>"
			} else {
				prevTotalStake = string(oldTotal)
			}
			if dbm, ok := x.DB.(*dbpkg.Manager); ok && dbm != nil {
				stats := dbm.GetChannelStats()
				if len(stats) > 0 {
					qLen = stats[0].Len
					qCap = stats[0].Cap
				}
			}
		}
		for _, addr := range activeMinersForStake {
			if !x.LivenessTracker.IsActive(addr) {
				continue
			}
			                                                           
			balKey := keys.KeyBalance(addr, "FB")
			balData, svOk, err := sv.Get(balKey)
			if err != nil || !svOk || len(balData) == 0 {
				if debugEnabled {
					missingBalance++
					if len(samples) < 6 {
						samples = append(samples, fmt.Sprintf("%s miss", shortKeyForLog(addr)))
					}
				}
				continue
			}
			balHash := xxhash.Sum64(balData)
			if debugEnabled {
				balanceXor ^= balHash
			}
			var balRec pb.TokenBalanceRecord
			if unmarshalProtoCompat(balData, &balRec) != nil || balRec.Balance == nil {
				if debugEnabled {
					parseErr++
					if len(samples) < 6 {
						samples = append(samples, fmt.Sprintf("%s parse_fail xxh=%016x", shortKeyForLog(addr), balHash))
					}
				}
				continue
			}
			mlStr := balRec.Balance.MinerLockedBalance
			if mlStr != "" {
				if ml, ok := new(big.Int).SetString(mlStr, 10); ok {
					totalStake.Add(totalStake, ml)
				} else if debugEnabled {
					parseErr++
				}
			}
			if debugEnabled && len(samples) < 6 {
				if mlStr == "" {
					mlStr = "0"
				}
				samples = append(samples, fmt.Sprintf("%s ml=%s xxh=%016x", shortKeyForLog(addr), mlStr, balHash))
			}
		}
		sv.Set(keys.KeyTotalMinerStake(), []byte(totalStake.String()))
		logs.Info("[VM][Liveness] height=%d updated total_miner_stake=%s miners=%d", b.Header.Height, totalStake, len(activeMinersForStake))
		if debugEnabled {
			if len(samples) == 0 {
				samples = append(samples, "-")
			}
			logs.Info(
				"[VM][LivenessStakeTrace] height=%d miners=%d prev_total=%s new_total=%s balance_xor=%016x miss=%d parse_err=%d q=%d/%d samples=%s",
				b.Header.Height, len(activeMinersForStake), prevTotalStake, totalStake.String(), balanceXor, missingBalance, parseErr, qLen, qCap, strings.Join(samples, ","),
			)
		}
	}
	return latestTop, minerChanged, nil
}

func minerLockedStakeForLiveness(sv StateView, mtx *pb.MinerTx, hot string) uint64 {
	if hot != "" {
		bal := GetBalance(sv, hot, "FB")
		if bal != nil {
			locked, err := parseBalanceStrict("miner locked balance", bal.MinerLockedBalance)
			if err == nil && locked.Sign() > 0 && locked.BitLen() <= 64 {
				return locked.Uint64()
			}
		}
	}
	if mtx == nil {
		return 0
	}
	stake, _ := ParseBalance(mtx.Amount)
	if stake == nil || stake.Sign() <= 0 || stake.BitLen() > 64 {
		return 0
	}
	return stake.Uint64()
}

func ensureMinerExitRecord(sv StateView, hot string, exitHeight uint64) error {
	if hot == "" {
		return fmt.Errorf("empty miner address")
	}
	key := keys.KeyMinerExitRecord(hot)
	if _, exists, err := sv.Get(key); err != nil {
		return err
	} else if exists {
		return nil
	}
	reward := RewardAddressOf(sv, hot)
	if reward == "" {
		return fmt.Errorf("empty reward address")
	}
	data, err := MarshalProto(&pb.MinerExitRecord{
		MinerAddress:  hot,
		ExitHeight:    exitHeight,
		RewardAddress: reward,
	})
	if err != nil {
		return err
	}
	sv.Set(key, data)
	return nil
}

                                                                                                            
                                                                                         
  
                                                                                        
                                                                                            
                                                                                                   
                                        
func (x *Executor) attachMinerToIndex(addr string, trackerIdx uint64, sv StateView) {
	                          
	acctKey := keys.KeyAccount(addr)
	if acctData, ok, err := sv.Get(acctKey); err == nil && ok && len(acctData) > 0 {
		var acct pb.Account
		if unmarshalProtoCompat(acctData, &acct) == nil {
			if acct.Index != trackerIdx {
				x.removeMinerIndexMappingIfOwned(acctKey, acct.Index, sv)
				acct.Index = trackerIdx
				if data, err := MarshalProto(&acct); err == nil {
					sv.Set(acctKey, data)
				}
			}
		}
	}

	                               
	sv.Set(keys.KeyIndexToAccount(trackerIdx), []byte(acctKey))

	                                                                   
	if dbm, ok := x.DB.(*dbpkg.Manager); ok && dbm != nil && dbm.IndexMgr != nil {
		dbm.IndexMgr.Add(trackerIdx)
		dbm.InvalidateMinerCache()
	}
}

func (x *Executor) removeMinerIndexMappingIfOwned(accountKey string, index uint64, sv StateView) {
	if accountKey == "" {
		return
	}
	key := keys.KeyIndexToAccount(index)
	value, exists, err := sv.Get(key)
	if err != nil || !exists || string(value) != accountKey {
		return
	}
	sv.Del(key)
	if dbm, ok := x.DB.(*dbpkg.Manager); ok && dbm != nil {
		if dbm.IndexMgr != nil {
			dbm.IndexMgr.Remove(index)
		}
		dbm.InvalidateMinerCache()
	}
}

                                                                 
                                                                                
                                                                                      
  
                                                                                             
                                                                  
func (x *Executor) applyLivenessSlashes(b *pb.Block, events []iface.LivenessSlashEvent, sv StateView) *big.Int {
	                                                                        
	                              
	type agg struct {
		owner  string
		amount *big.Int
		miners []string                                      
	}
	byOwner := make(map[string]*agg)
	order := make([]string, 0)
	for _, ev := range events {
		if ev.Amount == 0 || ev.Address == "" {
			continue
		}
		owner := ev.Address
		cur, ok := byOwner[owner]
		if !ok {
			cur = &agg{owner: owner, amount: new(big.Int)}
			byOwner[owner] = cur
			order = append(order, owner)
		}
		cur.amount.Add(cur.amount, new(big.Int).SetUint64(ev.Amount)) // safe-math-ok: 先聚合惩罚请求，实际扣款随后以现有锁仓余额为上限
		cur.miners = append(cur.miners, ev.Address)
	}

	total := new(big.Int)
	for _, owner := range order {
		a := byOwner[owner]
		balKey := keys.KeyBalance(owner, "FB")
		balData, ok, err := sv.Get(balKey)
		if err != nil || !ok || len(balData) == 0 {
			continue
		}
		var balRec pb.TokenBalanceRecord
		if unmarshalProtoCompat(balData, &balRec) != nil || balRec.Balance == nil {
			continue
		}
		locked, _ := new(big.Int).SetString(balRec.Balance.MinerLockedBalance, 10)
		if locked == nil {
			locked = new(big.Int)
		}
		actual := new(big.Int).Set(a.amount)
		if locked.Cmp(actual) < 0 {
			actual.Set(locked)
		}
		if actual.Sign() <= 0 {
			continue
		}
		locked.Sub(locked, actual)
		balRec.Balance.MinerLockedBalance = locked.String()
		SetBalance(sv, owner, "FB", balRec.Balance)
		total.Add(total, actual)
		logs.Info("[VM][Liveness] height=%d slashed %s FB from hot=%s (miners=%v new MinerLocked=%s)",
			b.Header.Height, actual.String(), owner, a.miners, locked.String())
		recordUserActivity(sv, owner, &UserActivityRecord{
			Kind: ActKindSlashPenalty, Height: b.Header.Height,
			Amount: actual.String(), Token: "FB",
			Ref: fmt.Sprintf("offline_report miners=%v", a.miners),
		})
	}
	return total
}

                                                                         
                                                 
                                                                                                      
                                                       
  
                                                                                                     
                                          
  
                                                                                                             
                                               
  
                                                                                              
                                                                                                       
                                                                              
            
                                                                                             
                                                                                                      
                                                                                                   
func (x *Executor) distributeSlashedFB(b *pb.Block, slashedTotal *big.Int, slashedAddrs map[string]struct{}, sv StateView) {
	if slashedTotal == nil || slashedTotal.Sign() <= 0 {
		return
	}
	if b == nil || b.Header == nil || b.Header.Miner == "" {
		                                                                           
		                                                                          
		                                                                           
		                                                              
		addFBBalanceToView(sv, livenessSlashPoolAccount, slashedTotal)
		height := uint64(0)
		if b != nil && b.Header != nil {
			height = b.Header.Height
		}
		logs.Warn("[VM][Liveness] height=%d retained slashed FB in pool=%s total=%s because proposer is missing",
			height, livenessSlashPoolAccount, slashedTotal.String())
		return
	}
	proposerHot := keys.MinerAddress(b.Header.Miner)

	                                                                               
	var otherHots []string
	if x.LivenessTracker != nil {
		for _, addr := range x.LivenessTracker.GetActiveMiners() {
			if addr == proposerHot {
				continue
			}
			if _, slashed := slashedAddrs[addr]; slashed {
				continue
			}
			if !x.LivenessTracker.IsActive(addr) {                                 
				continue
			}
			otherHots = append(otherHots, addr)
		}
	}

	                                                                       
	                                                                         
	                                                               
	proposerTotal, _, perMiner := SplitBlockReward(
		slashedTotal,
		len(otherHots),
		DefaultBlockRewardParams.OtherMinersRatio,
	)

	                                                                                                       
	                                                               
	type credit struct {
		amount *big.Int
		hots   []string
	}
	byReward := make(map[string]*credit)
	order := make([]string, 0)
	addCredit := func(rewardAddr, hot string, amt *big.Int) {
		if amt == nil || amt.Sign() <= 0 {
			return
		}
		c, ok := byReward[rewardAddr]
		if !ok {
			c = &credit{amount: new(big.Int)}
			byReward[rewardAddr] = c
			order = append(order, rewardAddr)
		}
		c.amount.Add(c.amount, amt)
		c.hots = append(c.hots, hot)
	}

	if perMiner != nil && perMiner.Sign() > 0 {
		for _, hot := range otherHots {
			addCredit(RewardAddressOf(sv, hot), hot, perMiner)
		}
	}
	addCredit(RewardAddressOf(sv, proposerHot), proposerHot, proposerTotal)

	for _, rewardAddr := range order {
		c := byReward[rewardAddr]
		addFBBalanceToView(sv, rewardAddr, c.amount)
		note := ""
		for _, hot := range c.hots {
			if hot == proposerHot {
				note = "as_proposer"
				break
			}
		}
		recordUserActivity(sv, rewardAddr, &UserActivityRecord{
			Kind: ActKindSlashDividend, Height: b.Header.Height,
			Amount: c.amount.String(), Token: "FB",
			Note: note,
			Ref:  fmt.Sprintf("hots=%v", c.hots),
		})
	}

	logs.Info("[VM][Liveness] height=%d distributed slashed FB: total=%s proposer_share=%s other_miners=%d",
		b.Header.Height, slashedTotal.String(), proposerTotal.String(), len(otherHots))
}

                                                                                        
func addFBBalanceToView(sv StateView, addr string, amount *big.Int) {
	if amount == nil || amount.Sign() <= 0 {
		return
	}
	bal := GetBalance(sv, addr, "FB")
	cur, _ := new(big.Int).SetString(bal.Balance, 10)
	if cur == nil {
		cur = new(big.Int)
	}
	cur.Add(cur, amount)
	bal.Balance = cur.String()
	SetBalance(sv, addr, "FB", bal)
}

                                                                                  
func (x *Executor) processCertChainHooks(b *pb.Block, sv StateView, checkpointStateRoot []byte, stateRootSource string) {
	if x.CertChainTracker == nil || b == nil || b.Header == nil {
		return
	}
	isCheckpoint := x.CertChainTracker.IsCheckpointHeight(b.Header.Height)
	checkpointStateRoot = append([]byte(nil), checkpointStateRoot...)
	if stateRootSource == "" {
		stateRootSource = "provided_post_state"
	}
	if dbm, ok := x.DB.(*dbpkg.Manager); ok && x.dumpCertEvidence {
		if path, err := dbm.DumpCertEvidence(b.Header.Height); err != nil {
			logs.Warn("[VM][CertChain] height=%d DumpCertEvidence failed: %v", b.Header.Height, err)
		} else {
			logs.Info("[VM][CertChain] height=%d evidence written to %s", b.Header.Height, path)
		}
	}
	for _, tx := range b.Body {
		if tx == nil {
			continue
		}
		certSign := tx.GetCertSignTx()
		if certSign == nil || certSign.Base == nil {
			continue
		}
		x.CertChainTracker.CollectCertSignTx(
			b.Header.Height,
			certSign.CheckpointHeight,
			certSign.Base.FromAddress,
			certSign.BlsSignature,
			certSign.CheckpointDigest,
		)
	}
	x.CertChainTracker.ProcessCollectedCertSigns(b.Header.Height, b.Header.Height)
	defer x.CertChainTracker.OnFinalizedHeight(b.Header.Height)
	if !isCheckpoint {
		return
	}
	if len(checkpointStateRoot) < 8 {
		logs.Error("[VM][CertChain] checkpoint=%d missing post-state fingerprint (source=%s len=%d), skip cert cycle", b.Header.Height, stateRootSource, len(checkpointStateRoot))
		return
	}
	activeMiners, err := BuildCertificateActiveMiners(x.LivenessTracker, sv)
	if err != nil {
		logs.Error("[VM][CertChain] checkpoint=%d cannot freeze strict Active set: %v", b.Header.Height, err)
		return
	}
	x.CertChainTracker.OnCheckpointExecuted(b.Header.Height, []byte(b.BlockHash), checkpointStateRoot, activeMiners)
	logs.Info("[VM][CertChain] Checkpoint %d: cert cycle uses %d strict Active miners", b.Header.Height, len(activeMiners))
}

                                                                               
func pickDeterministicPubKey(keysByAlgo map[int32][]byte) []byte {
	if len(keysByAlgo) == 0 {
		return nil
	}
	if pk, ok := keysByAlgo[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)]; ok && len(pk) > 0 {
		return append([]byte(nil), pk...)
	}
	algoIDs := make([]int, 0, len(keysByAlgo))
	for algo, pk := range keysByAlgo {
		if len(pk) > 0 {
			algoIDs = append(algoIDs, int(algo))
		}
	}
	if len(algoIDs) == 0 {
		return nil
	}
	sort.Ints(algoIDs)
	pk := keysByAlgo[int32(algoIDs[0])]
	if len(pk) == 0 {
		return nil
	}
	return append([]byte(nil), pk...)
}

                                                                                           
                                                
                                                                                                  
func (x *Executor) processFrostAutoTransitions(b *pb.Block, latestTop *pb.FrostActiveMiners, sv StateView) {
	if b.Header == nil || x.DB == nil {
		return
	}
	                                                                         
	                                                                            
	                                                                         
	                                                                              
	marker, _, markerErr := sv.Get(keys.KeyFrostConfiguredGenesisMarker())
	if markerErr != nil {
		panic(fmt.Errorf("read FROST genesis custody mode: %w", markerErr))
	}
	if bytes.HasPrefix(marker, []byte(config.RetainedFrostGenesisMarkerPrefix)) {
		return
	}
	height := b.Header.Height

	                                                                            
	                                                                          
	chains, configs, discoverErr := discoverFrostChainConfigs(sv.Scan)
	if discoverErr != nil {
		panic(fmt.Errorf("discover FROST term configs: %w", discoverErr))
	}
	if len(chains) == 0 {
		return
	}
	                                                                        
	                                                                      
	stateKeys := make([]string, 0, len(chains))
	for _, chain := range chains {
		stateKeys = append(stateKeys, keys.KeyFrostVaultState(chain, 0))
	}
	states, err := getStateMany(sv, uniqueSortedStateKeys(stateKeys))
	if err != nil {
		panic(fmt.Errorf("read FROST term state: %w", err))
	}
	transitionKeys := make([]string, 0, len(chains))
	vaults := make(map[string]*pb.FrostVaultState, len(chains))
	for _, chain := range chains {
		vault := &pb.FrostVaultState{}
		if unmarshalProtoCompat(states[keys.KeyFrostVaultState(chain, 0)], vault) != nil || vault.KeyEpoch == 0 {
			continue
		}
		vaults[chain] = vault
		epoch := vault.KeyEpoch + 1
		if vault.Status == VaultStatusPending && len(vault.GroupPubkey) == 0 {
			epoch = vault.KeyEpoch
		}
		transitionKeys = append(transitionKeys, keys.KeyFrostVaultTransition(chain, 0, epoch))
	}
	transitions, err := getStateMany(sv, uniqueSortedStateKeys(transitionKeys))
	if err != nil {
		panic(fmt.Errorf("read FROST pending transitions: %w", err))
	}

	var top *pb.FrostActiveMiners
	if latestTop != nil {
		top = latestTop
	} else {
		topData, ok, err := sv.Get(keys.KeyFrostActiveMiners())
		if err != nil || !ok || len(topData) == 0 {
			return
		}
		top = &pb.FrostActiveMiners{}
		if err := unmarshalProtoCompat(topData, top); err != nil {
			return
		}
	}
	if err := validateFrostCandidateSnapshot(top); err != nil {
		logs.Warn("[VM][AutoTransition] invalid VaultEligible snapshot: %v", err)
		return
	}

	for _, chain := range chains {
		vaultCfgData, ok := configs[keys.KeyFrostVaultConfig(chain, 0)]
		if !ok || len(vaultCfgData) == 0 {
			continue
		}
		var vaultCfg pb.FrostVaultConfig
		                                                                                                    
		if err := unmarshalProtoCompat(vaultCfgData, &vaultCfg); err != nil {
			continue
		}
		if err := config.ValidateOnChainVaultCommitteePolicy(&vaultCfg); err != nil {
			logs.Warn("[VM][AutoTransition] invalid on-chain vault config: chain=%s: %v", chain, err)
			continue
		}

		                        
		for vaultID := uint32(0); vaultID < vaultCfg.VaultCount; vaultID++ {
			                                                              
			vault := vaults[chain]
			if vault == nil {
				continue
			}

			                                                                       
			                                                                         
			                                                                        
			                                   
			if vault.Status == VaultStatusPending && len(vault.GroupPubkey) == 0 {
				initialEpoch := vault.KeyEpoch
				transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, initialEpoch)
				existingData, exists := transitions[transitionKey]
				if !exists || len(existingData) == 0 {
					continue
				}
				var ts pb.VaultTransitionState
				if err := unmarshalProtoCompat(existingData, &ts); err != nil || !isRecoverablePendingInitialDkg(vault, &ts) {
					continue
				}
				oldSessionID := ts.DkgSessionId
				recovered, recoverErr := recoverPendingInitialDkg(
					sv, transitionKey, vault, &ts, top, &vaultCfg, height,
				)
				if recoverErr != nil {
					logs.Debug("[VM][InitialDKGRecovery] remain fail-closed: chain=%s vault=%d epoch=%d attempt=%d n=%d t=%d: %v",
						chain, vaultID, initialEpoch, ts.DkgAttempt, ts.DkgN, ts.DkgThresholdT, recoverErr)
					continue
				}
				if recovered {
					logs.Info("[VM][InitialDKGRecovery] fresh retry: chain=%s vault=%d epoch=%d old_session=%s attempt=%d trigger=%d",
						chain, vaultID, initialEpoch, oldSessionID, ts.DkgAttempt+1, height+1)
				}
				continue
			}
			if vault.Status != VaultStatusActive {
				continue
			}
			targetEpoch := vault.KeyEpoch + 1
			transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, targetEpoch)
			existingData, exOk := transitions[transitionKey]

			if exOk && len(existingData) > 0 {
				                                                  
				var ts pb.VaultTransitionState
				                                                                                                                                          
				if err := unmarshalProtoCompat(existingData, &ts); err != nil {
					continue
				}
				                                                                                                                                                           
				if !shouldAutoRetryFailedDkg(&ts) {
					continue
				}

				                                 
				oldSessionID := ts.DkgSessionId
				if !canRestartDkg(&ts) {
					markDkgTransitionDraining(&ts)
					if err := setTransitionStateSV(sv, transitionKey, &ts); err != nil {
						logs.Warn("[VM][AutoTransition] failed to persist exhausted DKG epoch: chain=%s vault=%d epoch=%d attempt=%d: %v",
							chain, vaultID, targetEpoch, ts.DkgAttempt, err)
						continue
					}
					if _, err := cleanupFrostDkgSessionTempState(sv, chain, vaultID, targetEpoch, oldSessionID); err != nil {
						logs.Warn("[VM][AutoTransition] cleanup exhausted DKG session failed: chain=%s vault=%d epoch=%d session=%s: %v",
							chain, vaultID, targetEpoch, oldSessionID, err)
					}
					logs.Warn("[VM][AutoTransition] DKG restart limit reached; transition is draining: chain=%s vault=%d epoch=%d attempt=%d max_restarts=%d",
						chain, vaultID, targetEpoch, ts.DkgAttempt, maxDkgRestartsPerEpoch)
					continue
				}
				                                                                 
				                                                    
				plannedSize, sizeErr := committeeSizeForFrozenThreshold(ts.DkgThresholdT)
				if sizeErr != nil {
					markDkgTransitionDraining(&ts)
					_ = setTransitionStateSV(sv, transitionKey, &ts)
					continue
				}
				retryCfg := proto.Clone(&vaultCfg).(*pb.FrostVaultConfig)
				retryCfg.CommitteeSize = uint32(plannedSize)
				excluded := make(map[string]bool, len(ts.DkgDisqualifiedMembers))
				for _, member := range ts.DkgDisqualifiedMembers {
					excluded[member] = true
				}
				newCommittee, err := expectedTransitionCommitteeExcluding(top, retryCfg, excluded)
				                                                                                                                                                              
				if err != nil || len(newCommittee) == 0 {
					continue
				}

				newAttempt := ts.DkgAttempt + 1
				retryState, buildErr := buildAutoTransitionState(
					chain, vaultID, targetEpoch, newAttempt,
					vaultCfg.SignAlgo, height+1,
					vault.CommitteeMembers, newCommittee, vault.GroupPubkey,
					retryCfg,
				)
				if buildErr != nil {
					logs.Warn("[VM][AutoTransition] invalid retry committee: chain=%s vault=%d epoch=%d: %v", chain, vaultID, targetEpoch, buildErr)
					continue
				}
				if retryState.DkgThresholdT != ts.DkgThresholdT {
					logs.Warn("[VM][AutoTransition] retry threshold drift: chain=%s vault=%d epoch=%d old_t=%d new_t=%d", chain, vaultID, targetEpoch, ts.DkgThresholdT, retryState.DkgThresholdT)
					continue
				}
				retryState.DkgDisqualifiedMembers = append([]string(nil), ts.DkgDisqualifiedMembers...)

				                                                                                                                                                         
				if err := setTransitionStateSV(sv, transitionKey, retryState); err != nil {
					logs.Warn("[VM][AutoTransition] retry failed: chain=%s vault=%d epoch=%d attempt=%d: %v",
						chain, vaultID, targetEpoch, newAttempt, err)
					continue
				}
				if n, err := cleanupFrostDkgSessionTempState(sv, chain, vaultID, targetEpoch, oldSessionID); err != nil {
					logs.Warn("[VM][AutoTransition] cleanup old DKG session failed: chain=%s vault=%d epoch=%d session=%s: %v",
						chain, vaultID, targetEpoch, oldSessionID, err)
				} else if n > 0 {
					logs.Info("[VM][AutoTransition] cleaned %d temp keys for failed DKG session %s/%d epoch=%d session=%s",
						n, chain, vaultID, targetEpoch, oldSessionID)
				}
				logs.Info("[VM][AutoTransition] DKG retry: chain=%s vault=%d epoch=%d attempt=%d",
					chain, vaultID, targetEpoch, newAttempt)
				continue
			}

			                                                                     
			                                                                 
			if !vaultCommitteeTermDue(vault, height) {
				continue
			}
			if !strings.EqualFold(chain, "btc") && vault.KeyEpoch > 1 {
				complete, handoverErr := accountEpochAuthorityReady(sv, chain, vaultID, vault.KeyEpoch, &vaultCfg)
				if handoverErr != nil || !complete {
					logs.Warn("[VM][AutoTransition] current account epoch has no completed handover certificate: chain=%s vault=%d epoch=%d: %v", chain, vaultID, vault.KeyEpoch, handoverErr)
					continue
				}
			}

			                                                                   
			                                                                   
			currentSize := len(vault.CommitteeMembers)
			currentThreshold, thresholdErr := config.ComputeVaultCommitteeThreshold(currentSize)
			if thresholdErr != nil {
				logs.Warn("[VM][AutoTransition] unsupported current committee: chain=%s vault=%d k=%d", chain, vaultID, currentSize)
				continue
			}
			if vault.CommitteeThreshold != uint32(currentThreshold) {
				logs.Warn("[VM][AutoTransition] frozen threshold mismatch: chain=%s vault=%d k=%d t=%d", chain, vaultID, currentSize, vault.CommitteeThreshold)
				continue
			}

			targetSize, sizeErr := targetVaultCommitteeSize(top, &vaultCfg, currentSize)
			if sizeErr != nil {
				logs.Warn("[VM][AutoTransition] target size failed: chain=%s vault=%d: %v", chain, vaultID, sizeErr)
				continue
			}
			targetCfg := proto.Clone(&vaultCfg).(*pb.FrostVaultConfig)
			targetCfg.CommitteeSize = uint32(targetSize)
			newCommittee, committeeErr := expectedTransitionCommitteeFromActiveMiners(top, targetCfg, chain, vaultID, vault.KeyEpoch)
			if committeeErr != nil {
				                                                                  
				                                                     
				if clearPendingFrostCommittee(vault) {
					if err := persistFrostVaultState(sv, vault); err != nil {
						logs.Warn("[VM][AutoTransition] clear pending committee after candidate error failed: chain=%s vault=%d: %v", chain, vaultID, err)
					}
				}
				continue
			}

			changed := frostCommitteeReplacementCount(vault.CommitteeMembers, newCommittee)
			if sameVaultCommitteeMembers(vault.CommitteeMembers, newCommittee) {
				if err := renewVaultCommitteeTerm(vault, height); err != nil {
					panic(err)
				}
				if err := persistFrostVaultState(sv, vault); err != nil {
					panic(fmt.Errorf("renew vault committee term: %w", err))
				}
				continue
			}

			ts, buildErr := buildAutoTransitionState(
				chain, vaultID, targetEpoch, 1,
				vaultCfg.SignAlgo, height+1,
				vault.CommitteeMembers, newCommittee, vault.GroupPubkey,
				targetCfg,
			)
			if buildErr != nil {
				logs.Warn("[VM][AutoTransition] invalid target committee: chain=%s vault=%d epoch=%d: %v", chain, vaultID, targetEpoch, buildErr)
				continue
			}
			if err := setTransitionStateSV(sv, transitionKey, ts); err != nil {
				logs.Warn("[VM][AutoTransition] create failed: chain=%s vault=%d epoch=%d: %v", chain, vaultID, targetEpoch, err)
				continue
			}
			clearPendingFrostCommittee(vault)
			if err := persistFrostVaultState(sv, vault); err != nil {
				logs.Warn("[VM][AutoTransition] clear pending committee failed: chain=%s vault=%d: %v", chain, vaultID, err)
			}
			logs.Info("[VM][AutoTransition] term completed: chain=%s vault=%d epoch=%d n=%d t=%d changed=%d/%d eligible=%d",
				chain, vaultID, targetEpoch, ts.DkgN, ts.DkgThresholdT, changed, currentSize, top.EligibleCount)
			for _, m := range newCommittee {
				recordUserActivity(sv, m, &UserActivityRecord{
					Kind: ActKindTransitionJoin, Height: height,
					Ref: fmt.Sprintf("%s/%d/epoch=%d", chain, vaultID, targetEpoch),
				})
			}
		}
	}
}

func accountEpochHandoverCompleted(sv StateView, chainName string, vaultID uint32, epoch uint64) (bool, error) {
	chainName = strings.ToLower(strings.TrimSpace(chainName))
	if sv == nil || chainName == "" || chainName == "btc" || epoch <= 1 {
		return epoch == 1, nil
	}
	marker, found, err := sv.Get(keys.KeyFrostVaultHandoverConsumed(chainName, vaultID, epoch))
	if err != nil {
		return false, err
	}
	if !found || len(marker) == 0 {
		return false, nil
	}
	jobID := strings.TrimSpace(string(marker))
	jobData, found, err := sv.Get(keys.KeyFrostPermanentWithdrawJob(jobID))
	if err != nil {
		return false, err
	}
	var job pb.FrostWithdrawJobState
	if jobID == "" || !found || len(jobData) == 0 || proto.Unmarshal(jobData, &job) != nil ||
		job.JobId != jobID || chainpkg.NormalizeChain(job.Chain) != chainName || job.VaultId != vaultID ||
		job.KeyEpoch != epoch || job.Status != frostWithdrawJobStatusSigned || !job.IncludesHandover ||
		len(job.WithdrawIds) == 0 || len(job.WithdrawIds) > chainpkg.MaxWithdrawalsPerBatch {
		return false, fmt.Errorf("invalid permanent handover withdrawal job")
	}
	if _, err := evm.ParseBatchSeq(job.BatchSeq); err != nil {
		return false, fmt.Errorf("invalid permanent handover batch sequence: %w", err)
	}
	artifactRef, found, err := sv.Get(keys.KeyFrostPermanentWithdrawArtifact(job.WithdrawIds[0]))
	if err != nil {
		return false, err
	}
	artifactID := strings.TrimSpace(string(artifactRef))
	if !found || artifactID == "" {
		return false, fmt.Errorf("permanent handover withdrawal artifact reference is missing")
	}
	recordData, found, err := sv.Get(keys.KeyFrostPermanentArtifact(artifactID))
	if err != nil {
		return false, err
	}
	if !found || len(recordData) == 0 {
		return false, fmt.Errorf("permanent handover withdrawal artifact is missing")
	}
	record, err := artifact.UnmarshalRecord(recordData)
	if err != nil || record.Metadata.ArtifactID != artifactID || record.Metadata.Kind != artifact.KindWithdraw ||
		!strings.EqualFold(record.Metadata.Chain, chainName) || record.Metadata.VaultID != vaultID || record.Metadata.Epoch != epoch ||
		record.Metadata.SourceID != jobID || len(record.RawTx) < 4 || !bytes.Equal(record.RawTx[:4], evm.WithdrawBatchWithHandoversMethodID()) {
		return false, fmt.Errorf("permanent handover withdrawal artifact scope is invalid")
	}
	migrationParent, err := artifact.ResolveAccountMigrationHead(sv.Get, chainName, vaultID, epoch)
	if err != nil {
		return false, err
	}
	if migrationParent == "" || len(record.Metadata.ParentIDs) != 1 || record.Metadata.ParentIDs[0] != migrationParent {
		return false, fmt.Errorf("handover withdrawal artifact is not linked to the current migration proof")
	}
	return true, nil
}

                                                                                         
func setTransitionStateSV(sv StateView, transitionKey string, ts *pb.VaultTransitionState) error {
	data, err := MarshalProto(ts)
	if err != nil {
		return err
	}
	sv.Set(transitionKey, data)
	activeKey := keys.KeyFrostVaultTransitionActive(ts.Chain, ts.VaultId, ts.EpochId)
	sv.Set(activeKey, []byte(transitionKey))
	return nil
}

func dkgAttemptCanClose(status string) bool {
	switch status {
	case DKGStatusNotStarted, DKGStatusCommitting, DKGStatusSharing, DKGStatusResolving:
		return true
	default:
		return false
	}
}

func missingDkgCommitmentDealers(sv StateView, ts *pb.VaultTransitionState) (map[string]bool, error) {
	missing := make(map[string]bool)
	for _, dealer := range ts.NewCommitteeMembers {
		key := keys.KeyFrostVaultDkgCommit(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, dealer)
		data, exists, err := sv.Get(key)
		if err != nil {
			return nil, err
		}
		if !exists || len(data) == 0 {
			missing[dealer] = true
		}
	}
	return missing, nil
}

func missingDkgShareDealers(sv StateView, ts *pb.VaultTransitionState) (map[string]bool, error) {
	missing := make(map[string]bool)
	for _, dealer := range ts.NewCommitteeMembers {
		for _, receiver := range ts.NewCommitteeMembers {
			                                                                      
			                                                            
			if dealer == receiver {
				continue
			}
			key := keys.KeyFrostVaultDkgShare(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, dealer, receiver)
			data, exists, err := sv.Get(key)
			if err != nil {
				return nil, err
			}
			if !exists || len(data) == 0 {
				missing[dealer] = true
				break
			}
		}
	}
	return missing, nil
}

                                                                              
                                                                              
                                                                               
                                                                          
func missingDkgValidationMembers(sv StateView, ts *pb.VaultTransitionState) (map[string]bool, error) {
	missing := make(map[string]bool)
	for _, member := range ts.NewCommitteeMembers {
		key := keys.KeyFrostVaultDkgValidationAck(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, member)
		data, exists, err := sv.Get(key)
		if err != nil {
			return nil, err
		}
		if !exists || len(data) == 0 {
			missing[member] = true
			continue
		}
		if len(ts.ValidationMsgHash) == 0 || !bytes.Equal(data, ts.ValidationMsgHash) {
			return nil, fmt.Errorf("validation ack mismatch for member %s", member)
		}
	}
	return missing, nil
}

func sortedDkgMemberSet(members map[string]bool) []string {
	result := make([]string, 0, len(members))
	for member := range members {
		result = append(result, member)
	}
	sort.Strings(result)
	return result
}

func clearDkgAttemptOutputs(ts *pb.VaultTransitionState) {
	ts.NewGroupPubkey = nil
	ts.ValidationMsgHash = nil
	ts.MigrationJobId = ""
}

func closeDkgAttemptForCommitteeChange(
	sv StateView,
	transitionKey string,
	ts *pb.VaultTransitionState,
	remove map[string]bool,
	height uint64,
	note string,
) error {
	if ts == nil || len(remove) == 0 {
		return nil
	}

	removed := make([]string, 0, len(remove))
	for _, member := range ts.NewCommitteeMembers {
		if remove[member] {
			removed = append(removed, member)
		}
	}
	if len(removed) == 0 {
		return nil
	}
	sort.Strings(removed)
	disqualified := make(map[string]bool, len(ts.DkgDisqualifiedMembers)+len(removed))
	for _, member := range ts.DkgDisqualifiedMembers {
		disqualified[member] = true
	}
	for _, member := range removed {
		disqualified[member] = true
	}
	ts.DkgDisqualifiedMembers = ts.DkgDisqualifiedMembers[:0]
	for member := range disqualified {
		ts.DkgDisqualifiedMembers = append(ts.DkgDisqualifiedMembers, member)
	}
	sort.Strings(ts.DkgDisqualifiedMembers)

	oldSessionID := ts.DkgSessionId
	if _, err := cleanupFrostDkgSessionTempState(sv, ts.Chain, ts.VaultId, ts.EpochId, oldSessionID); err != nil {
		return err
	}

	clearDkgAttemptOutputs(ts)
	plannedSize, sizeErr := committeeSizeForFrozenThreshold(ts.DkgThresholdT)
	if sizeErr != nil {
		                                                                        
		                                                                        
		                                                                     
		legacyMembers := make([]string, 0, len(ts.NewCommitteeMembers)-len(removed))
		for _, member := range ts.NewCommitteeMembers {
			if !remove[member] {
				legacyMembers = append(legacyMembers, member)
			}
		}
		ts.NewCommitteeMembers = legacyMembers
		ts.DkgN = uint32(len(legacyMembers))
		if ts.DkgN < ts.DkgThresholdT || !canRestartDkg(ts) {
			markDkgTransitionDraining(ts)
		} else {
			resetDkgTransitionForRetry(ts, height+1)
		}
		if err := setTransitionStateWithIndex(sv, transitionKey, ts); err != nil {
			return err
		}
		for _, member := range removed {
			recordUserActivity(sv, member, &UserActivityRecord{
				Kind:   ActKindDKGDisqualified,
				Height: height,
				Ref:    fmt.Sprintf("%s/%d/epoch=%d", ts.Chain, ts.VaultId, ts.EpochId),
				Note:   note,
			})
		}
		return nil
	}
	if sizeErr == nil && canRestartDkg(ts) {
		if topData, exists, readErr := sv.Get(keys.KeyFrostActiveMiners()); readErr == nil && exists && len(topData) > 0 {
			var top pb.FrostActiveMiners
			if unmarshalProtoCompat(topData, &top) == nil {
				selectionCfg := &pb.FrostVaultConfig{CommitteeSize: uint32(plannedSize)}
				if replacement, selectErr := expectedTransitionCommitteeExcluding(&top, selectionCfg, disqualified); selectErr == nil {
					ts.NewCommitteeMembers = replacement
					resetDkgTransitionForRetry(ts, height+1)
				}
			}
		}
	}
	if ts.DkgStatus != DKGStatusNotStarted || len(ts.NewCommitteeMembers) != plannedSize || ts.DkgN != uint32(plannedSize) {
		                                                                      
		                                                                       
		                                                                
		if sizeErr == nil {
			ts.DkgN = uint32(plannedSize)
		}
		markDkgTransitionDraining(ts)
	}
	if err := setTransitionStateWithIndex(sv, transitionKey, ts); err != nil {
		return err
	}

	for _, member := range removed {
		recordUserActivity(sv, member, &UserActivityRecord{
			Kind:   ActKindDKGDisqualified,
			Height: height,
			Ref:    fmt.Sprintf("%s/%d/epoch=%d", ts.Chain, ts.VaultId, ts.EpochId),
			Note:   note,
		})
	}
	return nil
}

type dkgComplaintAtDeadline struct {
	key       string
	complaint *pb.FrostVaultDkgComplaint
}

type dkgTimeoutSlashPlan struct {
	dealers        []string
	ownerBalances  map[string]*pb.TokenBalance
	accountUpdates map[string][]byte
	poolBalance    *pb.TokenBalance
	total          *big.Int
}

func prepareDkgTimeoutSlashPlan(sv StateView, pending []dkgComplaintAtDeadline) (*dkgTimeoutSlashPlan, error) {
	dealerSet := make(map[string]bool)
	for _, item := range pending {
		if item.complaint.DealerId != "" {
			dealerSet[item.complaint.DealerId] = true
		}
	}
	dealers := sortedDkgMemberSet(dealerSet)
	plan := &dkgTimeoutSlashPlan{
		dealers:        dealers,
		ownerBalances:  make(map[string]*pb.TokenBalance),
		accountUpdates: make(map[string][]byte),
		total:          new(big.Int),
	}

	for _, dealer := range dealers {
		if _, planned := plan.ownerBalances[dealer]; !planned {
			balance := GetBalance(sv, dealer, DkgComplaintBondToken)
			locked, err := parseBalanceStrict("timed-out dkg dealer locked stake", balance.MinerLockedBalance)
			if err != nil {
				return nil, err
			}
			if locked.Sign() > 0 {
				plan.total.Add(plan.total, locked)
				balance.MinerLockedBalance = "0"
				plan.ownerBalances[dealer] = balance
			}
		}

		accountKey := keys.KeyAccount(dealer)
		accountData, exists, err := sv.Get(accountKey)
		if err != nil {
			return nil, err
		}
		if !exists || len(accountData) == 0 {
			return nil, fmt.Errorf("timed-out dealer account not found: %s", dealer)
		}
		var account pb.Account
		if err := unmarshalProtoCompat(accountData, &account); err != nil {
			return nil, fmt.Errorf("parse timed-out dealer account %s: %w", dealer, err)
		}
		if !account.IsMiner {
			continue
		}
		account.IsMiner = false
		updated, err := MarshalProto(&account)
		if err != nil {
			return nil, err
		}
		plan.accountUpdates[accountKey] = updated
	}

	poolBalance := GetBalance(sv, DkgSlashPoolAccount, DkgComplaintBondToken)
	poolAvailable, err := parseBalanceStrict("dkg slash pool balance", poolBalance.Balance)
	if err != nil {
		return nil, err
	}
	nextPool, err := SafeAdd(poolAvailable, plan.total)
	if err != nil {
		return nil, err
	}
	poolBalance.Balance = nextPool.String()
	plan.poolBalance = poolBalance
	return plan, nil
}

func applyDkgTimeoutSlashPlan(sv StateView, plan *dkgTimeoutSlashPlan, height uint64) error {
	if plan == nil {
		return nil
	}
	snapshot := sv.Snapshot()
	owners := make([]string, 0, len(plan.ownerBalances))
	for owner := range plan.ownerBalances {
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	for _, owner := range owners {
		SetBalance(sv, owner, DkgComplaintBondToken, plan.ownerBalances[owner])
	}
	if plan.total.Sign() > 0 {
		SetBalance(sv, DkgSlashPoolAccount, DkgComplaintBondToken, plan.poolBalance)
	}
	accountKeys := make([]string, 0, len(plan.accountUpdates))
	for key := range plan.accountUpdates {
		accountKeys = append(accountKeys, key)
	}
	sort.Strings(accountKeys)
	for _, key := range accountKeys {
		sv.Set(key, plan.accountUpdates[key])
	}
	for _, dealer := range plan.dealers {
		if err := ensureMinerExitRecord(sv, dealer, height); err != nil {
			if revertErr := sv.Revert(snapshot); revertErr != nil {
				return fmt.Errorf("persist timed-out dealer exit for %s: %v (rollback failed: %w)", dealer, err, revertErr)
			}
			return fmt.Errorf("persist timed-out dealer exit for %s: %w", dealer, err)
		}
	}
	return nil
}

func refundPendingDkgComplaintBonds(sv StateView, pending []dkgComplaintAtDeadline) error {
	refunds := make(map[string]*big.Int)
	total := new(big.Int)
	for _, item := range pending {
		bond := strings.TrimSpace(item.complaint.Bond)
		if bond == "" || bond == "0" {                                       
			continue
		}
		amount, err := parsePositiveBalanceStrict("pending dkg complaint bond", bond)
		if err != nil {
			return fmt.Errorf("complaint %s: %w", item.key, err)
		}
		if refunds[item.complaint.ReceiverId] == nil {
			refunds[item.complaint.ReceiverId] = new(big.Int)
		}
		refunds[item.complaint.ReceiverId].Add(refunds[item.complaint.ReceiverId], amount)
		total.Add(total, amount)
	}
	if total.Sign() == 0 {
		return nil
	}

	poolBalance := GetBalance(sv, DkgComplaintBondPoolAccount, DkgComplaintBondToken)
	poolAvailable, err := parseBalanceStrict("dkg complaint bond pool balance", poolBalance.Balance)
	if err != nil {
		return err
	}
	if poolAvailable.Cmp(total) < 0 {
		return fmt.Errorf("insufficient dkg complaint bond pool: have %s need %s", poolAvailable, total)
	}
	nextPool, err := SafeSub(poolAvailable, total)
	if err != nil {
		return err
	}

	receivers := make([]string, 0, len(refunds))
	nextReceiverBalances := make(map[string]*pb.TokenBalance, len(refunds))
	for receiver, amount := range refunds {
		receivers = append(receivers, receiver)
		balance := GetBalance(sv, receiver, DkgComplaintBondToken)
		available, err := parseBalanceStrict("dkg complaint refund destination balance", balance.Balance)
		if err != nil {
			return err
		}
		next, err := SafeAdd(available, amount)
		if err != nil {
			return err
		}
		balance.Balance = next.String()
		nextReceiverBalances[receiver] = balance
	}

	poolBalance.Balance = nextPool.String()
	SetBalance(sv, DkgComplaintBondPoolAccount, DkgComplaintBondToken, poolBalance)
	sort.Strings(receivers)
	for _, receiver := range receivers {
		SetBalance(sv, receiver, DkgComplaintBondToken, nextReceiverBalances[receiver])
	}
	return nil
}

                                                                              
                                                                              
                                                                         
func (x *Executor) processFrostDkgTimeouts(b *pb.Block, sv StateView) {
	if b.Header == nil {
		return
	}

	activePrefix := keys.KeyFrostVaultTransitionActivePrefix()
	activeKV, err := sv.Scan(activePrefix)
	if err != nil || len(activeKV) == 0 {
		return
	}

	activeKeys := make([]string, 0, len(activeKV))
	for activeKey := range activeKV {
		activeKeys = append(activeKeys, activeKey)
	}
	sort.Strings(activeKeys)

	for _, activeKey := range activeKeys {
		transitionKeyBytes := activeKV[activeKey]
		transitionKey := string(transitionKeyBytes)
		if transitionKey == "" {
			continue
		}

		data, ok, err := sv.Get(transitionKey)
		if err != nil || !ok || len(data) == 0 {
			continue
		}
		var ts pb.VaultTransitionState
		if err := unmarshalProtoCompat(data, &ts); err != nil {
			continue
		}
		if !dkgAttemptCanClose(ts.DkgStatus) {
			continue
		}

		                                                                    
		                                                                       
		                                        
		if ts.DkgCommitDeadline > 0 && b.Header.Height == ts.DkgCommitDeadline {
			missing, scanErr := missingDkgCommitmentDealers(sv, &ts)
			if scanErr != nil {
				logs.Warn("[VM][DKGDeadline] commitment completeness scan failed for %s/%d epoch=%d: %v",
					ts.Chain, ts.VaultId, ts.EpochId, scanErr)
				continue
			}
			if len(missing) > 0 {
				logs.Warn("[VM][DKGDeadline] missing commitments for %s/%d epoch=%d session=%s dealers=%v",
					ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, sortedDkgMemberSet(missing))
				if err := closeDkgAttemptForCommitteeChange(sv, transitionKey, &ts, missing, b.Header.Height, "missing commitment"); err != nil {
					logs.Warn("[VM][DKGDeadline] failed to close missing-commit attempt for %s/%d epoch=%d: %v",
						ts.Chain, ts.VaultId, ts.EpochId, err)
				}
				continue
			}
		}

		                                                                        
		                                                                        
		                                      
		if ts.DkgSharingDeadline > 0 && b.Header.Height == ts.DkgSharingDeadline {
			missing, scanErr := missingDkgShareDealers(sv, &ts)
			if scanErr != nil {
				logs.Warn("[VM][DKGDeadline] share completeness scan failed for %s/%d epoch=%d: %v",
					ts.Chain, ts.VaultId, ts.EpochId, scanErr)
				continue
			}
			missingCommitments, scanErr := missingDkgCommitmentDealers(sv, &ts)
			if scanErr != nil {
				logs.Warn("[VM][DKGDeadline] defensive commitment scan failed at sharing deadline for %s/%d epoch=%d: %v",
					ts.Chain, ts.VaultId, ts.EpochId, scanErr)
				continue
			}
			for dealer := range missingCommitments {
				missing[dealer] = true
			}
			if len(missing) > 0 {
				logs.Warn("[VM][DKGDeadline] incomplete sharing transcript for %s/%d epoch=%d session=%s dealers=%v",
					ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, sortedDkgMemberSet(missing))
				if err := closeDkgAttemptForCommitteeChange(sv, transitionKey, &ts, missing, b.Header.Height, "missing share"); err != nil {
					logs.Warn("[VM][DKGDeadline] failed to close missing-share attempt for %s/%d epoch=%d: %v",
						ts.Chain, ts.VaultId, ts.EpochId, err)
				}
				continue
			}
		}

		if ts.DkgDisputeDeadline == 0 || b.Header.Height < ts.DkgDisputeDeadline {
			continue
		}

		logs.Info("[VM][DKGTimeout] Vault %s/%d epoch %d reached dispute deadline at height %d. Current status: %s",
			ts.Chain, ts.VaultId, ts.EpochId, b.Header.Height, ts.DkgStatus)

		complaintPrefix := keys.KeyFrostVaultDkgComplaintPrefix(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId)
		kv, scanErr := sv.Scan(complaintPrefix)
		if scanErr != nil {
			logs.Warn("[VM][DKGTimeout] complaint scan failed for %s/%d epoch=%d: %v",
				ts.Chain, ts.VaultId, ts.EpochId, scanErr)
			continue
		}
		complaintKeys := make([]string, 0, len(kv))
		for ck := range kv {
			complaintKeys = append(complaintKeys, ck)
		}
		sort.Strings(complaintKeys)

		removeMembers := make(map[string]bool)
		pending := make([]dkgComplaintAtDeadline, 0)
		complaintsValid := true
		for _, ck := range complaintKeys {
			var c pb.FrostVaultDkgComplaint
			if err := unmarshalProtoCompat(kv[ck], &c); err != nil {
				logs.Warn("[VM][DKGTimeout] malformed complaint %s: %v", ck, err)
				complaintsValid = false
				break
			}
			item := dkgComplaintAtDeadline{key: ck, complaint: &c}
			switch c.Status {
			case ComplaintStatusResolved:
				removeMembers[c.ReceiverId] = true
			case ComplaintStatusDisqualified:
				removeMembers[c.DealerId] = true
			case ComplaintStatusPending:
				removeMembers[c.DealerId] = true
				pending = append(pending, item)
			default:
				logs.Warn("[VM][DKGTimeout] complaint %s has unknown status %q", ck, c.Status)
				complaintsValid = false
			}
			if !complaintsValid {
				break
			}
		}
		if !complaintsValid {
			continue
		}
		pendingUpdates := make(map[string][]byte, len(pending))
		for _, item := range pending {
			item.complaint.Status = ComplaintStatusDisqualified
			newData, err := MarshalProto(item.complaint)
			if err != nil {
				logs.Warn("[VM][DKGTimeout] failed to marshal complaint %s: %v", item.key, err)
				complaintsValid = false
				break
			}
			pendingUpdates[item.key] = newData
		}
		if !complaintsValid {
			continue
		}
		var timeoutSlashPlan *dkgTimeoutSlashPlan
		if len(pending) > 0 {
			timeoutSlashPlan, scanErr = prepareDkgTimeoutSlashPlan(sv, pending)
			if scanErr != nil {
				logs.Warn("[VM][DKGTimeout] pending dealer slash preflight failed for %s/%d epoch=%d session=%s: %v",
					ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, scanErr)
				continue
			}
		}
		                                                                     
		                                                                        
		if err := refundPendingDkgComplaintBonds(sv, pending); err != nil {
			logs.Warn("[VM][DKGTimeout] pending complaint bond refund failed for %s/%d epoch=%d session=%s: %v",
				ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, err)
			continue
		}
		if err := applyDkgTimeoutSlashPlan(sv, timeoutSlashPlan, b.Header.Height); err != nil {
			logs.Warn("[VM][DKGTimeout] failed to persist slash exits for %s/%d epoch=%d: %v", ts.Chain, ts.VaultId, ts.EpochId, err)
			continue
		}
		for _, item := range pending {
			sv.Set(item.key, pendingUpdates[item.key])
		}

		if len(removeMembers) > 0 {
			logs.Warn("[VM][DKGTimeout] closing adjudicated attempt for %s/%d epoch=%d session=%s removed=%v",
				ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, sortedDkgMemberSet(removeMembers))
			if err := closeDkgAttemptForCommitteeChange(sv, transitionKey, &ts, removeMembers, b.Header.Height, "dkg complaint adjudication"); err != nil {
				logs.Warn("[VM][DKGTimeout] failed to close adjudicated attempt for %s/%d epoch=%d: %v",
					ts.Chain, ts.VaultId, ts.EpochId, err)
			}
			continue
		}

		if b.Header.Height > ts.DkgDisputeDeadline+dkgTimeoutRetryGraceBlocks {
			missingReady, readyErr := missingDkgValidationMembers(sv, &ts)
			if readyErr != nil {
				logs.Warn("[VM][DKGTimeout] validation readiness scan failed for %s/%d epoch=%d session=%s: %v",
					ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, readyErr)
				continue
			}
			if len(missingReady) > 0 {
				logs.Warn("[VM][DKGTimeout] members missing durable validation ack for %s/%d epoch=%d session=%s members=%v",
					ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, sortedDkgMemberSet(missingReady))
				if err := closeDkgAttemptForCommitteeChange(sv, transitionKey, &ts, missingReady, b.Header.Height, "missing durable validation ack"); err != nil {
					logs.Warn("[VM][DKGTimeout] failed to close validation-incomplete attempt for %s/%d epoch=%d: %v",
						ts.Chain, ts.VaultId, ts.EpochId, err)
				}
				continue
			}

			cleanupSessionID := ts.DkgSessionId
			willRestart := canRestartDkg(&ts)
			if willRestart {
				resetDkgTransitionForRetry(&ts, b.Header.Height+1)
			} else {
				markDkgTransitionDraining(&ts)
			}
			if n, err := cleanupFrostDkgSessionTempState(sv, ts.Chain, ts.VaultId, ts.EpochId, cleanupSessionID); err != nil {
				logs.Warn("[VM][DKGTimeout] cleanup DKG session failed: chain=%s vault=%d epoch=%d session=%s: %v",
					ts.Chain, ts.VaultId, ts.EpochId, cleanupSessionID, err)
				continue
			} else if n > 0 {
				logs.Info("[VM][DKGTimeout] cleaned %d temp keys for DKG session %s/%d epoch=%d session=%s",
					n, ts.Chain, ts.VaultId, ts.EpochId, cleanupSessionID)
			}
			if err := setTransitionStateWithIndex(sv, transitionKey, &ts); err != nil {
				logs.Warn("[VM][DKGTimeout] failed to persist transition state for %s/%d epoch %d: %v",
					ts.Chain, ts.VaultId, ts.EpochId, err)
				continue
			}
			if willRestart {
				logs.Warn("[VM][DKGTimeout] Vault %s/%d epoch %d retrying DKG after deadline: height=%d dispute_deadline=%d next_attempt=%d",
					ts.Chain, ts.VaultId, ts.EpochId, b.Header.Height, ts.DkgDisputeDeadline, ts.DkgAttempt)
			} else {
				logs.Warn("[VM][DKGTimeout] Vault %s/%d epoch %d reached DKG restart limit and is draining: height=%d attempt=%d max_restarts=%d",
					ts.Chain, ts.VaultId, ts.EpochId, b.Header.Height, ts.DkgAttempt, maxDkgRestartsPerEpoch)
			}
		}
	}
}

                                                                                
                                                                             
                     
const MinerExitFreezeBlocks = liveness.DefaultFreezeBlocks

func (x *Executor) minerExitFreezeBlocks() uint64 {
	if provider, ok := x.LivenessTracker.(interface{ FreezeBlocks() uint64 }); ok {
		if blocks := provider.FreezeBlocks(); blocks > 0 {
			return blocks
		}
	}
	return MinerExitFreezeBlocks
}

type minerExitUnlockPlan struct {
	exitKey     string
	hot         string
	reward      string
	exitHeight  uint64
	amount      *big.Int
	activitySeq uint32
	activityRaw []byte
}

type minerExitBalanceWrite struct {
	key   string
	value []byte
	del   bool
}

func uniqueSortedStateKeys(stateKeys []string) []string {
	if len(stateKeys) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(stateKeys))
	for _, stateKey := range stateKeys {
		if stateKey != "" {
			set[stateKey] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for stateKey := range set {
		out = append(out, stateKey)
	}
	sort.Strings(out)
	return out
}

func batchValue(primary, secondary map[string][]byte, key string) ([]byte, bool) {
	if value, ok := primary[key]; ok {
		return value, true
	}
	value, ok := secondary[key]
	return value, ok
}

func decodeMinerExitAccount(raw []byte, exists bool, address string) (*pb.Account, bool, error) {
	if !exists {
		return nil, false, nil
	}
	if len(raw) == 0 {
		return nil, false, fmt.Errorf("decode account %s: empty record", address)
	}
	var account pb.Account
	if err := unmarshalProtoCompat(raw, &account); err != nil {
		return nil, false, fmt.Errorf("decode account %s: %w", address, err)
	}
	return &account, true, nil
}

func decodeMinerExitBalance(raw []byte, exists bool, address string) (*pb.TokenBalance, error) {
	if !exists {
		return zeroTokenBalance(), nil
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("decode balance %s/FB: empty record", address)
	}
	var record pb.TokenBalanceRecord
	if err := unmarshalProtoCompat(raw, &record); err != nil {
		return nil, fmt.Errorf("decode balance %s/FB: %w", address, err)
	}
	if record.Balance == nil {
		return nil, fmt.Errorf("decode balance %s/FB: missing balance payload", address)
	}
	return &pb.TokenBalance{
		Balance:               record.Balance.Balance,
		MinerLockedBalance:    record.Balance.MinerLockedBalance,
		LiquidLockedBalance:   record.Balance.LiquidLockedBalance,
		WitnessLockedBalance:  record.Balance.WitnessLockedBalance,
		LeverageLockedBalance: record.Balance.LeverageLockedBalance,
		OrderFrozenBalance:    record.Balance.OrderFrozenBalance,
	}, nil
}

                                                                             
                                                                               
                                                                  
func pendingHandoverMembers(sv StateView) (map[string]struct{}, error) {
	active, err := sv.Scan(keys.KeyFrostVaultTransitionActivePrefix())
	if err != nil {
		return nil, fmt.Errorf("scan active vault transitions: %w", err)
	}
	protected := make(map[string]struct{})
	if len(active) == 0 {
		return protected, nil
	}
	activeKeys := make([]string, 0, len(active))
	transitionKeys := make([]string, 0, len(active))
	activeByTransition := make(map[string][]string, len(active))
	for activeKey := range active {
		activeKeys = append(activeKeys, activeKey)
	}
	sort.Strings(activeKeys)
	for _, activeKey := range activeKeys {
		transitionKey := strings.TrimSpace(string(active[activeKey]))
		if transitionKey == "" {
			return nil, fmt.Errorf("active vault transition %s has empty target", activeKey)
		}
		transitionKeys = append(transitionKeys, transitionKey)
		activeByTransition[transitionKey] = append(activeByTransition[transitionKey], activeKey)
	}
	transitionKeys = uniqueSortedStateKeys(transitionKeys)
	states, err := getStateMany(sv, transitionKeys)
	if err != nil {
		return nil, fmt.Errorf("read active vault transitions: %w", err)
	}
	for _, transitionKey := range transitionKeys {
		data, exists := states[transitionKey]
		if !exists || len(data) == 0 {
			return nil, fmt.Errorf("active vault transition target %s is missing", transitionKey)
		}
		var transition pb.VaultTransitionState
		if err := unmarshalProtoCompat(data, &transition); err != nil {
			return nil, fmt.Errorf("decode active vault transition %s: %w", transitionKey, err)
		}
		canonicalTransitionKey := keys.KeyFrostVaultTransition(transition.Chain, transition.VaultId, transition.EpochId)
		if transitionKey != canonicalTransitionKey {
			return nil, fmt.Errorf("active vault transition target mismatch: got=%s canonical=%s", transitionKey, canonicalTransitionKey)
		}
		canonicalActiveKey := keys.KeyFrostVaultTransitionActive(transition.Chain, transition.VaultId, transition.EpochId)
		for _, activeKey := range activeByTransition[transitionKey] {
			if activeKey != canonicalActiveKey {
				return nil, fmt.Errorf("active vault transition index mismatch: got=%s canonical=%s target=%s", activeKey, canonicalActiveKey, transitionKey)
			}
		}
		if transition.Lifecycle == "ACTIVE" {
			continue
		}
		for _, member := range transition.OldCommitteeMembers {
			if member != "" {
				protected[member] = struct{}{}
			}
		}
		if transition.DkgStatus == DKGStatusKeyReady {
			for _, member := range transition.NewCommitteeMembers {
				if member != "" {
					protected[member] = struct{}{}
				}
			}
		}
	}
	return protected, nil
}

                                                                         
                                                                               
                                                                    
func (x *Executor) processMinerExitUnlocks(b *pb.Block, sv StateView) error {
	if b.Header == nil {
		return nil
	}

	kvs, err := sv.Scan(keys.KeyMinerExitPrefix())
	if err != nil || len(kvs) == 0 {
		return err
	}

	height := b.Header.Height
	freezeBlocks := x.minerExitFreezeBlocks()
	exitKeys := make([]string, 0, len(kvs))
	for exitKey := range kvs {
		exitKeys = append(exitKeys, exitKey)
	}
	sort.Strings(exitKeys)

	type candidate struct {
		record        *pb.MinerExitRecord
		exitKey       string
		hotAccount    *pb.Account
		hotAccountSet bool
		reward        string
	}
	candidates := make([]candidate, 0, len(exitKeys))
	for _, exitKey := range exitKeys {
		var record pb.MinerExitRecord
		if err := unmarshalProtoCompat(kvs[exitKey], &record); err != nil {
			return fmt.Errorf("decode miner exit %s: %w", exitKey, err)
		}
		if record.MinerAddress == "" || exitKey != keys.KeyMinerExitRecord(record.MinerAddress) {
			return fmt.Errorf("invalid miner exit record key=%s miner=%q", exitKey, record.MinerAddress)
		}
		if height < record.ExitHeight || height-record.ExitHeight < freezeBlocks {
			continue
		}
		candidates = append(candidates, candidate{record: &record, exitKey: exitKey})
	}
	if len(candidates) == 0 {
		return nil
	}

	protected, err := pendingHandoverMembers(sv)
	if err != nil {
		return err
	}
	eligible := candidates[:0]
	for _, item := range candidates {
		if _, blocked := protected[item.record.MinerAddress]; !blocked {
			eligible = append(eligible, item)
		}
	}
	candidates = eligible
	if len(candidates) == 0 {
		return nil
	}

	firstKeys := make([]string, 0, len(candidates)*2)
	for _, item := range candidates {
		firstKeys = append(firstKeys, keys.KeyAccount(item.record.MinerAddress), keys.KeyBalance(item.record.MinerAddress, "FB"))
	}
	firstValues, err := getStateMany(sv, uniqueSortedStateKeys(firstKeys))
	if err != nil {
		return fmt.Errorf("prefetch miner exit hot state: %w", err)
	}

	secondKeys := make([]string, 0, len(candidates)*3)
	for i := range candidates {
		item := &candidates[i]
		hot := item.record.MinerAddress
		accountKey := keys.KeyAccount(hot)
		accountRaw, accountFound := firstValues[accountKey]
		account, exists, err := decodeMinerExitAccount(accountRaw, accountFound, hot)
		if err != nil {
			return err
		}
		item.hotAccount, item.hotAccountSet = account, exists
		item.reward = item.record.RewardAddress
		if item.reward == "" {
			item.reward = hot
			if exists && account.RewardAddress != "" {
				item.reward = account.RewardAddress
			}
		}
		if !utils.IsDexAddress(item.reward) {
			logs.Warn("[VM][MinerExit] invalid reward address for miner=%s", hot)
			continue
		}
		secondKeys = append(secondKeys, keys.KeyAccount(item.reward), keys.KeyBalance(item.reward, "FB"))
		if exists {
			secondKeys = append(secondKeys, keys.KeyIndexToAccount(account.Index))
		}
	}

	valid := candidates[:0]
	for _, item := range candidates {
		if utils.IsDexAddress(item.reward) {
			valid = append(valid, item)
		}
	}
	candidates = valid
	if len(candidates) == 0 {
		return nil
	}
	secondValues, err := getStateMany(sv, uniqueSortedStateKeys(secondKeys))
	if err != nil {
		return fmt.Errorf("prefetch miner exit reward state: %w", err)
	}

	balances := make(map[string]*pb.TokenBalance, len(candidates)*2)
	missingRewardAccounts := make(map[string]struct{})
	allAddresses := make([]string, 0, len(candidates)*2)
	for _, item := range candidates {
		allAddresses = append(allAddresses, item.record.MinerAddress, item.reward)
	}
	for _, address := range uniqueSortedStateKeys(allAddresses) {
		balanceKey := keys.KeyBalance(address, "FB")
		raw, exists := batchValue(secondValues, firstValues, balanceKey)
		balance, err := decodeMinerExitBalance(raw, exists, address)
		if err != nil {
			return err
		}
		balances[address] = balance
	}
	for _, item := range candidates {
		accountKey := keys.KeyAccount(item.reward)
		raw, exists := batchValue(secondValues, firstValues, accountKey)
		if _, exists, err = decodeMinerExitAccount(raw, exists, item.reward); err != nil {
			return err
		} else if !exists {
			missingRewardAccounts[item.reward] = struct{}{}
		}
	}

	plans := make([]minerExitUnlockPlan, 0, len(candidates))
	dirtyBalances := make(map[string]struct{}, len(candidates)*2)
	creditedRewards := make(map[string]struct{}, len(candidates))
	indexRemovals := make(map[uint64]struct{})
	for _, item := range candidates {
		hot := item.record.MinerAddress
		hotBalance := balances[hot]
		actual, err := parseBalanceStrict("miner exit locked balance", hotBalance.MinerLockedBalance)
		if err != nil {
			return fmt.Errorf("miner %s: %w", hot, err)
		}
		if actual.Sign() > 0 {
			rewardBalance := balances[item.reward]
			available, err := parseBalanceStrict("miner exit destination balance", rewardBalance.Balance)
			if err != nil {
				return fmt.Errorf("miner %s reward %s: %w", hot, item.reward, err)
			}
			next, err := SafeAdd(available, actual)
			if err != nil {
				return fmt.Errorf("miner %s reward %s balance: %w", hot, item.reward, err)
			}
			rewardBalance.Balance = next.String()
			hotBalance.MinerLockedBalance = "0"
			dirtyBalances[hot] = struct{}{}
			dirtyBalances[item.reward] = struct{}{}
			creditedRewards[item.reward] = struct{}{}
		}
		if item.hotAccountSet {
			mappingKey := keys.KeyIndexToAccount(item.hotAccount.Index)
			if mapping, exists := secondValues[mappingKey]; exists && string(mapping) == keys.KeyAccount(hot) {
				indexRemovals[item.hotAccount.Index] = struct{}{}
			}
		}
		activityRaw, err := json.Marshal(&UserActivityRecord{
			Kind: ActKindMinerExitUnlock, Height: height,
			Amount: actual.String(), Token: "FB",
			Ref: fmt.Sprintf("miner=%s exit_height=%d", hot, item.record.ExitHeight),
		})
		if err != nil {
			return fmt.Errorf("encode miner exit activity %s: %w", hot, err)
		}
		plans = append(plans, minerExitUnlockPlan{
			exitKey: item.exitKey, hot: hot, reward: item.reward,
			exitHeight: item.record.ExitHeight, amount: actual, activityRaw: activityRaw,
		})
	}

	balanceWrites := make([]minerExitBalanceWrite, 0, len(dirtyBalances))
	for _, address := range uniqueSortedStateKeys(mapKeys(dirtyBalances)) {
		balance := balances[address]
		write := minerExitBalanceWrite{key: keys.KeyBalance(address, "FB")}
		if rent.TokenBalanceTotal(balance).Sign() == 0 {
			write.del = true
		} else {
			encoded, err := MarshalProto(&pb.TokenBalanceRecord{Address: address, Token: "FB", Balance: balance})
			if err != nil {
				return fmt.Errorf("encode miner exit balance %s: %w", address, err)
			}
			write.value = encoded
		}
		balanceWrites = append(balanceWrites, write)
	}

	accountsToCreate := make(map[string][]byte, len(missingRewardAccounts))
	for address := range creditedRewards {
		if _, missing := missingRewardAccounts[address]; !missing {
			continue
		}
		encoded, err := MarshalProto(&pb.Account{Address: address})
		if err != nil {
			return fmt.Errorf("encode miner exit reward account %s: %w", address, err)
		}
		accountsToCreate[address] = encoded
	}
	accountAddresses := make([]string, 0, len(accountsToCreate))
	for address := range accountsToCreate {
		accountAddresses = append(accountAddresses, address)
	}
	sort.Strings(accountAddresses)
	indices := make([]uint64, 0, len(indexRemovals))
	for index := range indexRemovals {
		indices = append(indices, index)
	}
	sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })

	snap := sv.Snapshot()
	rollback := func(cause error) error {
		if revertErr := sv.Revert(snap); revertErr != nil {
			return fmt.Errorf("%v; revert miner exit batch: %w", cause, revertErr)
		}
		return cause
	}
	                                                                            
	                                                                             
	                                                      
	if _, hasAllocator := sv.(userActivitySeqAllocator); hasAllocator {
		for i := range plans {
			seq, err := nextUserActivitySeq(sv, plans[i].reward, height)
			if err != nil {
				return rollback(fmt.Errorf("allocate miner exit activity for %s: %w", plans[i].reward, err))
			}
			plans[i].activitySeq = seq
		}
	} else {
		                                                                         
		                                                                        
		                                                                     
		nextByReward := make(map[string]uint32)
		for i := range plans {
			next, exists := nextByReward[plans[i].reward]
			if !exists {
				var err error
				next, err = nextUserActivitySeq(sv, plans[i].reward, height)
				if err != nil {
					return rollback(fmt.Errorf("allocate miner exit activity for %s: %w", plans[i].reward, err))
				}
			}
			plans[i].activitySeq = next
			if next == ^uint32(0) {
				return rollback(fmt.Errorf("allocate miner exit activity for %s: sequence overflow", plans[i].reward))
			}
			nextByReward[plans[i].reward] = next + 1
		}
	}
	                                                                          
	                                                                           
	                                                                          
	                        
	if x.LivenessTracker != nil {
		hotAddresses := make([]string, 0, len(plans))
		for _, plan := range plans {
			hotAddresses = append(hotAddresses, plan.hot)
		}
		if err := x.LivenessTracker.UnregisterMinersBatch(sv, hotAddresses); err != nil {
			return rollback(fmt.Errorf("unregister miner exit batch: %w", err))
		}
	}
	for _, address := range accountAddresses {
		sv.Set(keys.KeyAccount(address), accountsToCreate[address])
		rent.MarkOwnerInitialized(sv, address)
	}
	for _, write := range balanceWrites {
		if write.del {
			sv.Del(write.key)
		} else {
			sv.Set(write.key, write.value)
		}
	}
	for _, index := range indices {
		sv.Del(keys.KeyIndexToAccount(index))
	}
	for _, plan := range plans {
		sv.Del(plan.exitKey)
		writeUserActivity(sv, plan.reward, height, plan.activitySeq, plan.activityRaw)
	}
	if len(indices) > 0 {
		if dbm, ok := x.DB.(*dbpkg.Manager); ok && dbm != nil {
			if dbm.IndexMgr != nil {
				for _, index := range indices {
					dbm.IndexMgr.Remove(index)
				}
			}
			dbm.InvalidateMinerCache()
		}
	}
	for _, plan := range plans {
		logs.Info("[VM][MinerExit] height=%d released actual frozen stake %s for miner=%s reward_address=%s (exit_height=%d)", height, plan.amount.String(), plan.hot, plan.reward, plan.exitHeight)
	}
	return nil
}

func mapKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	return out
}

                                                                                              
                                                                                                     
                                                                                                     
                                                               
func hasPendingHandover(sv StateView, addr string) bool {
	if addr == "" {
		return false
	}
	protected, err := pendingHandoverMembers(sv)
	if err != nil {
		logs.Warn("[VM][MinerExit] handover protection read failed for %s: %v", addr, err)
		return true
	}
	_, exists := protected[addr]
	return exists
}
