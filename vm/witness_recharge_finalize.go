package vm

import (
	"bytes"
	frostchain "dex/frost/chain"
	"dex/frost/custodyevidence"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/rent"
	"dex/utils"
	"dex/witness"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
)

var errBTCInternalRecharge = errors.New("custody change or sweep is not a deposit")

func abortRechargeFinalized(sv StateView, snap int, format string, args ...interface{}) error {
	reason := fmt.Sprintf(format, args...)
	if err := sv.Revert(snap); err != nil {
		return fmt.Errorf("%s (revert failed: %v)", reason, err)
	}
	return fmt.Errorf("%s", reason)
}

func addVaultCreditedFundsLedger(sv StateView, chain, asset string, vaultID uint32, amount *big.Int) error {
	if amount == nil || amount.Sign() <= 0 {
		return fmt.Errorf("credited funds ledger amount must be positive")
	}
	ledgerKey := keys.KeyFrostVaultFundsLedger(chain, asset, vaultID)
	data, exists, err := sv.Get(ledgerKey)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "not found") {
		return err
	}
	current := big.NewInt(0)
	if exists && len(data) > 0 {
		current, err = ParseBalance(string(data))
		if err != nil || current.Sign() < 0 {
			return fmt.Errorf("invalid credited funds ledger value: %q", string(data))
		}
	}
	next, err := SafeAdd(current, amount)
	if err != nil {
		return err
	}
	sv.Set(ledgerKey, []byte(next.String()))
	return nil
}

func verifyBTCRechargeAssignedVault(sv StateView, chainName string, vaultCount uint32, stored *pb.RechargeRequest) ([]byte, error) {
	if stored == nil {
		return nil, fmt.Errorf("nil BTC recharge request")
	}
	if stored.VaultId >= vaultCount {
		return nil, fmt.Errorf("assigned BTC vault out of range: vault=%d count=%d", stored.VaultId, vaultCount)
	}
	vaultData, exists, err := sv.Get(keys.KeyFrostVaultState(chainName, stored.VaultId))
	if err != nil {
		return nil, err
	}
	if !exists || len(vaultData) == 0 {
		return nil, fmt.Errorf("assigned BTC vault state not found: chain=%s vault=%d", chainName, stored.VaultId)
	}
	var vault pb.FrostVaultState
	if err := unmarshalProtoCompat(vaultData, &vault); err != nil {
		return nil, fmt.Errorf("parse assigned BTC vault state: %w", err)
	}
	baseXOnly, err := utils.NormalizeSecp256k1XOnlyPubKey(vault.GroupPubkey)
	if err != nil {
		return nil, fmt.Errorf("normalize assigned BTC vault pubkey: %w", err)
	}
	_, err = utils.ParseTaprootScriptPubKeyXOnly(stored.NativeScript)
	if err != nil {
		return nil, fmt.Errorf("parse assigned BTC script_pubkey: %w", err)
	}
	tweak := utils.ComputeUserTweak(baseXOnly, stored.ReceiverAddress)
	expectedXOnly, err := utils.ComputeTweakedXOnlyPubkey(vault.GroupPubkey, tweak)
	if err != nil {
		return nil, fmt.Errorf("derive assigned BTC vault script_pubkey: %w", err)
	}
	cfg, err := loadFrostVaultConfig(sv, chainName)
	if err != nil || len(cfg.GuardianPubkey) != 32 {
		return nil, fmt.Errorf("BTC guardian configuration is missing")
	}
	expectedScript, _, _, err := utils.BuildDualCustodyTaproot(expectedXOnly, cfg.GuardianPubkey)
	if err != nil || !bytes.Equal(stored.NativeScript, expectedScript) {
		return nil, fmt.Errorf("BTC script_pubkey does not match assigned vault=%d", stored.VaultId)
	}
	return tweak, nil
}

func registerBTCRechargeUTXO(sv StateView, stored *pb.RechargeRequest, finalizeHeight uint64) (bool, error) {
	chainName, vaultCount, err := resolveVaultChainAndCount(sv, stored.NativeChain)
	if err != nil {
		return false, fmt.Errorf("resolve vault config for btc recharge failed: %w", err)
	}
	stored.NativeChain = chainName
	txid, nativeLockKey, err := rechargeNativeLockIdentity(chainName, stored.NativeTxHash, stored.NativeVout)
	if err != nil {
		return false, err
	}
	outputs, err := custodyevidence.InternalBTCOutputs(sv.Get, txid)
	if err != nil {
		return false, err
	}
	for _, vout := range outputs.InternalVouts {
		if vout == stored.NativeVout {
			return false, errBTCInternalRecharge
		}
	}
	lockOwner, lockExists, err := sv.Get(nativeLockKey)
	if err != nil {
		return false, err
	}
	if !lockExists || string(lockOwner) != stored.RequestId {
		return false, fmt.Errorf("btc outpoint lock owner mismatch: request=%s", stored.RequestId)
	}
	stored.NativeTxHash = txid
	tweakBytes, err := verifyBTCRechargeAssignedVault(sv, chainName, vaultCount, stored)
	if err != nil {
		return false, err
	}

	vout := stored.NativeVout

	utxo := &pb.FrostUtxo{
		Txid:           txid,
		Vout:           vout,
		Amount:         stored.Amount,
		VaultId:        stored.VaultId,
		FinalizeHeight: finalizeHeight,
		RequestId:      stored.RequestId,
		ScriptPubkey:   stored.NativeScript,
		Tweak:          tweakBytes,
	}
	active, err := frostchain.BTCIsolationActive(sv.Get)
	if err != nil {
		return false, err
	}
	if active {
		if err := ensureBTCUTXOOrigin(sv, utxo, stored.BtcProtocolVersion != 3); err != nil {
			return false, err
		}
	}
	utxoKey := keys.KeyFrostBtcUtxo(stored.VaultId, txid, vout)
	existingData, utxoExists, err := sv.Get(utxoKey)
	if err != nil {
		return false, err
	}
	var utxoData []byte
	if utxoExists {
		var existing pb.FrostUtxo
		if err := unmarshalProtoCompat(existingData, &existing); err != nil {
			return false, err
		}
		return false, fmt.Errorf("btc recharge UTXO exists without credited marker: request=%s existing_request=%s", stored.RequestId, existing.RequestId)
	} else {
		utxoData, err = proto.Marshal(utxo)
		if err != nil {
			return false, err
		}
		sv.Set(utxoKey, utxoData)
	}
	if !utxoExists {
		utxoSeqKey := keys.KeyFrostBtcUtxoFIFOSeq(stored.VaultId)
		utxoSeq := readUintSeq(sv, utxoSeqKey) + 1
		sv.Set(keys.KeyFrostBtcUtxoFIFOIndex(stored.VaultId, utxoSeq), utxoData)
		if err := setBTCSpendableUTXOIndex(sv, stored.VaultId, utxoSeq, utxo, utxoData); err != nil {
			return false, err
		}
		sv.Set(utxoSeqKey, []byte(strconv.FormatUint(utxoSeq, 10)))
	}
	if _, headExists, _ := sv.Get(keys.KeyFrostBtcUtxoFIFOHead(stored.VaultId)); !headExists {
		sv.Set(keys.KeyFrostBtcUtxoFIFOHead(stored.VaultId), []byte("1"))
	}
	return !utxoExists, nil
}

func applyRechargeFinalized(sv StateView, req *pb.RechargeRequest, fallbackHeight, mix uint64, frozenFeeRecipients ...[]string) error {
	if req == nil || req.RequestId == "" {
		return nil
	}
	requestKey := keys.KeyRechargeRequest(req.RequestId)
	requestData, exists, err := sv.Get(requestKey)
	if err != nil {
		return err
	}
	if !exists {
		if isBTCChainName(req.NativeChain) {
			return fmt.Errorf("canonical BTC recharge request not found: request=%s", req.RequestId)
		}
		return nil
	}
	var stored pb.RechargeRequest
	if err := unmarshalProtoCompat(requestData, &stored); err != nil {
		return err
	}
	if err := validateRechargeEconomicsPolicy(sv, &stored); err != nil {
		return err
	}
	creditedKey := keys.KeyRechargeCredited(req.RequestId)
	if _, credited, err := sv.Get(creditedKey); err != nil {
		return err
	} else if credited {
		if stored.Status != pb.RechargeRequestStatus_RECHARGE_FINALIZED {
			return fmt.Errorf("recharge credited marker conflicts with non-finalized request: request=%s status=%s", stored.RequestId, stored.Status)
		}
		return nil
	}
	if stored.Status == pb.RechargeRequestStatus_RECHARGE_REJECTED {
		return nil
	}
	if !isBTCChainName(stored.NativeChain) {
		valid, err := checkAccountChainRechargeFinalization(sv, &stored)
		if err != nil {
			return err
		}
		if !valid {
			return rejectUncreditableRecharge(sv, &stored, fallbackHeight, mix)
		}
	}
	if err := applyRechargeFinalizedLoaded(sv, req, &stored, requestKey, creditedKey, fallbackHeight, mix, frozenFeeRecipients...); err != nil {
		if errors.Is(err, errBTCInternalRecharge) {
			canonical := loadRechargeRequestAtKey(sv, requestKey)
			if canonical == nil {
				return err
			}
			return rejectUncreditableRecharge(sv, canonical, fallbackHeight, mix)
		}
		return err
	}
	if isBTCChainName(stored.NativeChain) {
		if _, credited, err := sv.Get(creditedKey); err != nil {
			return err
		} else if !credited {
			return fmt.Errorf("btc recharge finalization did not commit credited marker: request=%s", stored.RequestId)
		}
	}
	return nil
}

func applyRechargeFinalizedLoaded(sv StateView, req, stored *pb.RechargeRequest, requestKey, creditedKey string, fallbackHeight, mix uint64, frozenFeeRecipients ...[]string) error {
	if err := validateRechargeEconomicsPolicy(sv, stored); err != nil {
		return err
	}
	snap := sv.Snapshot()
	oldStored := proto.Clone(stored).(*pb.RechargeRequest)

	if strings.TrimSpace(stored.ReceiverAddress) == "" {
		return abortRechargeFinalized(sv, snap, "missing recharge receiver request=%s", stored.RequestId)
	}
	if fallbackHeight > 0 {
		stored.FinalizeHeight = fallbackHeight
	} else if req.FinalizeHeight > 0 {
		stored.FinalizeHeight = req.FinalizeHeight
	}
	amount, err := ParseBalance(stored.Amount)
	if err != nil || amount.Sign() <= 0 {
		return abortRechargeFinalized(sv, snap, "invalid recharge amount request=%s amount=%q", stored.RequestId, stored.Amount)
	}
	plan, err := rechargeCreditPayouts(stored, amount, frozenFeeRecipients...)
	if err != nil {
		return abortRechargeFinalized(sv, snap, "recharge reward allocation request=%s: %v", stored.RequestId, err)
	}

	btcUTXOCreated := false
	if isBTCChainName(stored.NativeChain) {
		created, err := registerBTCRechargeUTXO(sv, stored, stored.FinalizeHeight)
		if err != nil {
			if errors.Is(err, errBTCInternalRecharge) {
				_ = sv.Revert(snap)
				return err
			}
			return abortRechargeFinalized(sv, snap, "register btc recharge UTXO request=%s: %v", stored.RequestId, err)
		}
		btcUTXOCreated = created
	}
	if err := settleWitnessRequestFeesAndCollateral(sv, stored, pb.WitnessVoteType_VOTE_PASS); err != nil {
		_ = sv.Revert(snap)
		return err
	}

	if err := resolveOrRegisterBridgedToken(sv, stored); err != nil {
		return abortRechargeFinalized(sv, snap, "resolve bridged token request=%s: %v", stored.RequestId, err)
	}
	chain := stored.NativeChain
	asset := stored.TokenAddress
	vaultID := stored.VaultId
	if !isBTCChainName(chain) {
		seqKey := keys.KeyFrostFundsLotSeq(chain, asset, vaultID, stored.FinalizeHeight)
		seq := readUintSeq(sv, seqKey) + 1
		sv.Set(keys.KeyFrostFundsLotIndex(chain, asset, vaultID, stored.FinalizeHeight, seq), []byte(stored.RequestId))
		sv.Set(seqKey, []byte(strconv.FormatUint(seq, 10)))
		trackFrostFundsLotSeqForBlockCleanup(sv, stored.FinalizeHeight, seqKey)
		includeFundsLotInHead(sv, chain, asset, vaultID, stored.FinalizeHeight, seq)
	}

	if !isBTCChainName(chain) || btcUTXOCreated {
		if err := addVaultAvailableBalance(sv, chain, asset, vaultID, amount); err != nil {
			return abortRechargeFinalized(sv, snap, "update vault available request=%s: %v", stored.RequestId, err)
		}
	}
	if err := addVaultCreditedFundsLedger(sv, chain, asset, vaultID, amount); err != nil {
		return abortRechargeFinalized(sv, snap, "update credited funds ledger request=%s: %v", stored.RequestId, err)
	}

	userAddr := stored.ReceiverAddress
	addresses := make([]string, 0, len(plan.Payouts))
	for address := range plan.Payouts {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	                                                                         
	                                                                         
	if batch, ok := sv.(interface {
		GetMany([]string) (map[string][]byte, error)
	}); ok {
		keysToRead := make([]string, 0, 2*len(addresses))
		for _, address := range addresses {
			if plan.Payouts[address].Sign() > 0 {
				keysToRead = append(keysToRead, keys.KeyAccount(address), keys.KeyBalance(address, stored.TokenAddress))
				if stored.EconomicsPolicy == protocol.RechargeEconomicsLaunchV1 {
					keysToRead = append(keysToRead, keys.KeyRechargeRewardOwner(address, stored.RequestId))
				}
			}
		}
		if _, err := batch.GetMany(keysToRead); err != nil {
			return abortRechargeFinalized(sv, snap, "prefetch credit recipients request=%s: %v", stored.RequestId, err)
		}
	}
	for _, address := range addresses {
		payout := plan.Payouts[address]
		if payout.Sign() == 0 {
			continue
		}
		userKey := keys.KeyAccount(address)
		userData, userExists, err := sv.Get(userKey)
		if err != nil {
			return abortRechargeFinalized(sv, snap, "read credit account request=%s: %v", stored.RequestId, err)
		}
		userAccount := pb.Account{Address: address}
		if userExists {
			if err := unmarshalProtoCompat(userData, &userAccount); err != nil {
				return abortRechargeFinalized(sv, snap, "parse user account request=%s: %v", stored.RequestId, err)
			}
		} else {
			rent.MarkOwnerInitialized(sv, address)
		}
		tokenBal, err := getBalanceStrict(sv, address, stored.TokenAddress)
		if err != nil {
			return abortRechargeFinalized(sv, snap, "read user balance request=%s: %v", stored.RequestId, err)
		}
		currentBalance, err := parseBalanceStrict("balance", tokenBal.Balance)
		if err != nil {
			return abortRechargeFinalized(sv, snap, "parse user balance request=%s: %v", stored.RequestId, err)
		}
		newBalance, err := SafeAdd(currentBalance, payout)
		if err != nil {
			return abortRechargeFinalized(sv, snap, "add user balance request=%s: %v", stored.RequestId, err)
		}
		tokenBal.Balance = newBalance.String()
		SetBalance(sv, address, stored.TokenAddress, tokenBal)
		updatedUserData, err := MarshalProto(&userAccount)
		if err != nil {
			return abortRechargeFinalized(sv, snap, "marshal user account request=%s: %v", stored.RequestId, err)
		}
		sv.Set(userKey, updatedUserData)
		if stored.EconomicsPolicy == protocol.RechargeEconomicsLaunchV1 {
			attribution := &pb.RechargeRewardAttribution{
				RequestId: stored.RequestId, TokenAddress: stored.TokenAddress,
				FinalizeHeight: stored.FinalizeHeight,
			}
			if address == stored.ReceiverAddress {
				attribution.ReceiverNetAmount = plan.ReceiverNet.String()
			}
			if address == stored.RequesterAddress {
				attribution.RequesterRewardAmount = plan.RequesterReward.String()
			}
			if reward := plan.WitnessCredits[address]; reward != nil && reward.Sign() > 0 {
				attribution.WitnessRewardAmount = reward.String()
			}
			attributionKey := keys.KeyRechargeRewardOwner(address, stored.RequestId)
			if _, exists, err := sv.Get(attributionKey); err != nil || exists {
				return abortRechargeFinalized(sv, snap, "recharge reward attribution conflict request=%s owner=%s err=%v", stored.RequestId, address, err)
			}
			attributionData, err := proto.Marshal(attribution)
			if err != nil {
				return abortRechargeFinalized(sv, snap, "marshal recharge reward attribution request=%s owner=%s: %v", stored.RequestId, address, err)
			}
			sv.Set(attributionKey, attributionData)
		}
	}
	if err := adjustBridgedTotalSupply(sv, stored.TokenAddress, amount); err != nil {
		return abortRechargeFinalized(sv, snap, "adjust total supply request=%s: %v", stored.RequestId, err)
	}

	if !isBTCChainName(stored.NativeChain) {
		sv.Set(keys.KeyRechargeEvent(stored.NativeChain, stored.NativeTxHash, stored.DepositEventIndex), []byte(stored.RequestId))
		sv.Del(accountChainRechargePendingKey(stored))
	} else if stored.NativeChain != "" && stored.NativeTxHash != "" {
		_, nativeLockKey, err := rechargeNativeLockIdentity(stored.NativeChain, stored.NativeTxHash, stored.NativeVout)
		if err != nil {
			return abortRechargeFinalized(sv, snap, "invalid native transaction identity request=%s: %v", stored.RequestId, err)
		}
		sv.Set(nativeLockKey, []byte(stored.RequestId))
	}
	if err := settleRuntimeFeeEscrow(sv, stored.RequestId, stored.FinalizeHeight, true); err != nil {
		return abortRechargeFinalized(sv, snap, "settle recharge runtime fee request=%s: %v", stored.RequestId, err)
	}
	if err := settleRequesterBond(sv, stored, true); err != nil {
		return abortRechargeFinalized(sv, snap, "settle requester bond request=%s: %v", stored.RequestId, err)
	}
	if stored.EconomicsPolicy == protocol.RechargeEconomicsLaunchV1 {
		stored.GrossAmount = amount.String()
		stored.ReceiverNetAmount = plan.ReceiverNet.String()
		stored.RequesterRewardAmount = plan.RequesterReward.String()
		stored.WitnessRewardAmount = plan.WitnessReward.String()
	}
	sv.Set(creditedKey, []byte("1"))
	stored.Status = pb.RechargeRequestStatus_RECHARGE_FINALIZED
	if err := cleanupTerminalRechargeHotState(sv, stored); err != nil {
		return abortRechargeFinalized(sv, snap, "clean terminal witnesses request=%s: %v", stored.RequestId, err)
	}
	updatedRequestData, err := proto.Marshal(compactRechargeRequestForHotState(stored))
	if err != nil {
		_ = sv.Revert(snap)
		return err
	}
	sv.Set(requestKey, updatedRequestData)
	syncRechargeRequestIndexes(sv, oldStored, stored, mix)
	if userAddr != "" {
		recordUserActivity(sv, userAddr, &UserActivityRecord{
			Kind: ActKindRechargeFinalized, Height: fallbackHeight,
			Amount: plan.ReceiverNet.String(), Token: stored.TokenAddress,
			Ref:  stored.RequestId,
			Note: fmt.Sprintf("chain=%s vault=%d", chain, vaultID),
		})
	}
	return nil
}

type rechargeCreditPlan struct {
	Payouts, WitnessCredits                     map[string]*big.Int
	ReceiverNet, RequesterReward, WitnessReward *big.Int
}

                                                                        
                                                                           
func rechargeCreditPayouts(req *pb.RechargeRequest, gross *big.Int, frozenFeeRecipients ...[]string) (*rechargeCreditPlan, error) {
	if req == nil || gross == nil || gross.Sign() <= 0 || req.ReceiverAddress == "" {
		return nil, fmt.Errorf("invalid recharge credit identity or amount")
	}
	plan := &rechargeCreditPlan{Payouts: map[string]*big.Int{}, WitnessCredits: map[string]*big.Int{},
		ReceiverNet: new(big.Int).Set(gross), RequesterReward: new(big.Int), WitnessReward: new(big.Int)}
	add := func(address string, amount *big.Int) error {
		if address == "" || amount == nil || amount.Sign() < 0 {
			return fmt.Errorf("invalid recharge payout recipient")
		}
		if plan.Payouts[address] == nil {
			plan.Payouts[address] = new(big.Int)
		}
		plan.Payouts[address].Add(plan.Payouts[address], amount)
		return nil
	}
	if req.EconomicsPolicy != protocol.RechargeEconomicsLaunchV1 {
		if err := add(req.ReceiverAddress, gross); err != nil {
			return nil, err
		}
		return plan, nil
	}
	if req.RequesterAddress == "" {
		return nil, fmt.Errorf("missing recharge requester")
	}
	share := new(big.Int).Quo(gross, big.NewInt(10000))
	net := new(big.Int).Sub(gross, new(big.Int).Mul(share, big.NewInt(2))) // safe-math-ok: share=floor(gross/10000)，两份奖励小于 gross，净额非负
	plan.ReceiverNet, plan.RequesterReward, plan.WitnessReward = net, new(big.Int).Set(share), new(big.Int).Set(share)
	if err := add(req.ReceiverAddress, net); err != nil {
		return nil, err
	}
	if err := add(req.RequesterAddress, share); err != nil {
		return nil, err
	}
	var recipients []string
	if len(frozenFeeRecipients) > 0 {
		recipients = frozenFeeRecipients[0]
	} else {
		if req.Status == pb.RechargeRequestStatus_RECHARGE_ARBITRATION || req.Status == pb.RechargeRequestStatus_RECHARGE_CHALLENGED {
			return nil, fmt.Errorf("challenged PASS lacks frozen arbitration eligibility")
		}
		positions := req.OriginalPositions
		if len(positions) == 0 {
			positions = req.Votes
		}
		eligibility, err := witness.ArbitrationEligibility(witness.ArbitrationRewardInput{FinalVote: pb.WitnessVoteType_VOTE_PASS, Original: positions})
		if err != nil {
			return nil, err
		}
		recipients = eligibility.FeeRecipients
	}
	unique := make(map[string]struct{}, len(recipients))
	for _, address := range recipients {
		if address == "" {
			return nil, fmt.Errorf("empty eligible witness address")
		}
		unique[address] = struct{}{}
	}
	addresses := make([]string, 0, len(unique))
	for address := range unique {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	if share.Sign() > 0 && len(addresses) == 0 {
		return nil, fmt.Errorf("positive witness reward has no eligible recipients")
	}
	if len(addresses) > 0 {
		quotient, remainder := new(big.Int).QuoRem(share, big.NewInt(int64(len(addresses))), new(big.Int))
		for i, address := range addresses {
			allocation := new(big.Int).Set(quotient)
			if int64(i) < remainder.Int64() {
				allocation.Add(allocation, big.NewInt(1))
			}
			if err := add(address, allocation); err != nil {
				return nil, err
			}
			plan.WitnessCredits[address] = allocation
		}
	}
	return plan, nil
}
