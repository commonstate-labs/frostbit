                        
                                
package vm

import (
	"bytes"
	"crypto/sha256"
	bridgeasset "dex/frost/asset"
	"dex/frost/chain"
	"dex/frost/custodyevidence"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/protocol"
	"dex/utils"
	"dex/witness"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
)

const DefaultVaultCount = 100

                                                                                                 
                                                                                                   
func allocateVaultID(requestID string, vaultCount uint32) uint32 {
	hash := sha256.Sum256([]byte(requestID))
	                                     
	n := binary.BigEndian.Uint32(hash[:4])
	return n % vaultCount
}

                                                                                          
                                                                                                                
func allocateVaultIDWithLifecycleCheck(sv StateView, chain, requestID string, vaultCount uint32) (uint32, error) {
	if vaultCount == 0 {
		vaultCount = DefaultVaultCount
	}

	initialVaultID := allocateVaultID(requestID, vaultCount)
	                                                   
	vaultStateKey := keys.KeyFrostVaultState(chain, initialVaultID)
	vaultStateData, exists, _ := sv.Get(vaultStateKey)
	if exists && len(vaultStateData) > 0 {
		var vaultState pb.FrostVaultState
		if err := unmarshalProtoCompat(vaultStateData, &vaultState); err == nil {
			                                                                           
			                                                                             
			if vaultState.Status == VaultLifecycleDraining {
				                                                                 
				for offset := uint32(1); offset < vaultCount; offset++ {
					candidateID := (initialVaultID + offset) % vaultCount
					candidateKey := keys.KeyFrostVaultState(chain, candidateID)
					candidateData, candidateExists, _ := sv.Get(candidateKey)
					if candidateExists && len(candidateData) > 0 {
						var candidateState pb.FrostVaultState
						if err := unmarshalProtoCompat(candidateData, &candidateState); err == nil {
							if candidateState.Status == "ACTIVE" {
								return candidateID, nil
							}
						}
					} else {
						                                                                                
						return candidateID, nil
					}
				}
				                                                                              
				return 0, fmt.Errorf("no ACTIVE vault available for chain %s", chain)
			}
		}
	}

	                                                                                
	return initialVaultID, nil
}

                                                                           
                                                                                         
func appendUniqueNonEmpty(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func resolveVaultChainAndCount(sv StateView, chainName string) (string, uint32, error) {
	raw := strings.TrimSpace(chainName)
	candidates := make([]string, 0, 3)
	candidates = appendUniqueNonEmpty(candidates, chain.NormalizeChain(raw))
	candidates = appendUniqueNonEmpty(candidates, raw)
	candidates = appendUniqueNonEmpty(candidates, strings.ToUpper(raw))

	for _, candidate := range candidates {
		cfgKey := keys.KeyFrostVaultConfig(candidate, 0)
		cfgData, exists, err := sv.Get(cfgKey)
		if err != nil {
			return "", 0, fmt.Errorf("failed to read vault config for chain %s: %w", candidate, err)
		}
		if !exists || len(cfgData) == 0 {
			continue
		}

		var cfg pb.FrostVaultConfig
		if err := unmarshalProtoCompat(cfgData, &cfg); err != nil {
			return "", 0, fmt.Errorf("failed to parse vault config for chain %s: %w", candidate, err)
		}
		if cfg.VaultCount == 0 {
			return "", 0, fmt.Errorf("invalid vault count=0 for chain %s", candidate)
		}
		return candidate, cfg.VaultCount, nil
	}

	return "", 0, fmt.Errorf("vault config not found for chain %s", raw)
}

func resolveWitnessRequestAsset(sv StateView, chainName string, request *pb.WitnessRequestTx) (string, error) {
	if request == nil {
		return "", fmt.Errorf("nil witness request")
	}
	identity, err := bridgeasset.Resolve(chainName, request.NativeContract)
	if err != nil {
		return "", err
	}
	request.NativeChain = identity.Chain
	request.NativeContract = identity.Contract

	idxKey := keys.KeyNativeTokenIndex(identity.Chain, identity.Contract)
	idxData, indexed, err := sv.Get(idxKey)
	if err != nil {
		return "", err
	}
	if indexed && string(idxData) != identity.ID {
		return "", fmt.Errorf("native asset index conflicts with canonical AssetID")
	}

	tokenData, exists, err := sv.Get(keys.KeyToken(identity.ID))
	if err != nil {
		return "", err
	}
	if !exists {
		if indexed {
			return "", fmt.Errorf("native asset index points to missing token")
		}
		return identity.ID, nil
	}
	if !indexed {
		return "", fmt.Errorf("registered external asset is missing its native index")
	}
	var token pb.Token
	if err := unmarshalProtoCompat(tokenData, &token); err != nil {
		return "", fmt.Errorf("failed to parse registered asset: %w", err)
	}
	if token.Type != pb.TokenType_TOKEN_BRIDGED || token.Address != identity.ID {
		return "", fmt.Errorf("canonical AssetID is occupied by a non-bridged asset")
	}
	registered, err := bridgeasset.Resolve(token.NativeChain, token.NativeContract)
	if err != nil || registered != identity {
		return "", fmt.Errorf("registered asset identity mismatch")
	}
	return identity.ID, nil
}

func isAllocatableVaultStatus(status string) bool {
	return status == VaultStatusKeyReady || status == VaultStatusActive
}

func isBTCChainName(chainName string) bool {
	c := strings.ToLower(strings.TrimSpace(chainName))
	return c == "btc" || strings.HasPrefix(c, "btc_") || strings.HasPrefix(c, "btc-")
}

                                                                                 
                                                                                
                                            
func rechargeNativeLockIdentity(chainName, txHash string, vout uint32) (string, string, error) {
	if !isBTCChainName(chainName) {
		canonical, err := protocol.CanonicalExternalTxHash(chainName, txHash)
		if err != nil {
			return "", "", err
		}
		return canonical, keys.KeyRechargeRequestByNativeTx(chainName, canonical), nil
	}
	if txHash != strings.TrimSpace(txHash) || len(txHash) != 64 {
		return "", "", fmt.Errorf("BTC txid must be exactly 64 lowercase hex characters")
	}
	raw, err := hex.DecodeString(txHash)
	if err != nil || len(raw) != 32 {
		return "", "", fmt.Errorf("BTC txid must be exactly 64 lowercase hex characters")
	}
	canonical := hex.EncodeToString(raw)
	if txHash != canonical {
		return "", "", fmt.Errorf("BTC txid must be lowercase canonical hex")
	}
	outpoint := canonical + ":" + strconv.FormatUint(uint64(vout), 10)
	return canonical, keys.KeyRechargeRequestByNativeTx(chainName, outpoint), nil
}

func lookupVaultIDByScriptPubKey(sv StateView, chainName string, vaultCount uint32, scriptPubKey []byte, receiverAddress string) (uint32, error) {
	if vaultCount == 0 {
		return 0, fmt.Errorf("invalid vault count=0 for chain %s", chainName)
	}

	_, err := utils.ParseTaprootScriptPubKeyXOnly(scriptPubKey)
	if err != nil {
		return 0, fmt.Errorf("parse taproot script_pubkey failed: %w", err)
	}

	cfg, err := loadFrostVaultConfig(sv, chainName)
	if err != nil || len(cfg.GuardianPubkey) != 32 {
		return 0, fmt.Errorf("BTC guardian configuration is missing")
	}
	matches := make([]uint32, 0, 1)
	nonAllocatableMatches := make([]uint32, 0, 1)

	for candidateID := uint32(0); candidateID < vaultCount; candidateID++ {
		candidateKey := keys.KeyFrostVaultState(chainName, candidateID)
		candidateData, candidateExists, err := sv.Get(candidateKey)
		if err != nil {
			return 0, fmt.Errorf("failed to read vault state for chain %s vault %d: %w", chainName, candidateID, err)
		}
		if !candidateExists || len(candidateData) == 0 {
			continue
		}

		var candidateState pb.FrostVaultState
		if err := unmarshalProtoCompat(candidateData, &candidateState); err != nil {
			return 0, fmt.Errorf("failed to parse vault state for chain %s vault %d: %w", chainName, candidateID, err)
		}

		candidateXOnly, err := utils.NormalizeSecp256k1XOnlyPubKey(candidateState.GroupPubkey)
		if err != nil {
			continue
		}

		                                                                        
		tweak := utils.ComputeUserTweak(candidateXOnly, receiverAddress)
		derivedOperator, err := utils.ComputeTweakedXOnlyPubkey(candidateState.GroupPubkey, tweak)
		if err != nil {
			continue
		}

		expectedScript, _, _, err := utils.BuildDualCustodyTaproot(derivedOperator, cfg.GuardianPubkey)
		if err != nil || !bytes.Equal(expectedScript, scriptPubKey) {
			continue
		}

		if isAllocatableVaultStatus(candidateState.Status) {
			matches = append(matches, candidateID)
		} else {
			nonAllocatableMatches = append(nonAllocatableMatches, candidateID)
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		if len(nonAllocatableMatches) > 0 {
			return 0, fmt.Errorf(
				"script_pubkey=%s maps only to non-allocatable vault(s) %v on chain %s",
				hex.EncodeToString(scriptPubKey),
				nonAllocatableMatches,
				chainName,
			)
		}
		return 0, fmt.Errorf(
			"no allocatable vault matches script_pubkey=%s on chain %s (receiver=%s)",
			hex.EncodeToString(scriptPubKey),
			chainName,
			receiverAddress,
		)
	default:
		return 0, fmt.Errorf(
			"ambiguous script_pubkey=%s maps to multiple allocatable vaults %v on chain %s",
			hex.EncodeToString(scriptPubKey),
			matches,
			chainName,
		)
	}
}

func allocateVaultIDWithStateCheck(sv StateView, chainName, requestID string, vaultCount uint32) (uint32, error) {
	if vaultCount == 0 {
		return 0, fmt.Errorf("invalid vault count=0 for chain %s", chainName)
	}

	initialVaultID := allocateVaultID(requestID, vaultCount)
	for offset := uint32(0); offset < vaultCount; offset++ {
		candidateID := (initialVaultID + offset) % vaultCount
		candidateKey := keys.KeyFrostVaultState(chainName, candidateID)
		candidateData, candidateExists, err := sv.Get(candidateKey)
		if err != nil {
			return 0, fmt.Errorf("failed to read vault state for chain %s vault %d: %w", chainName, candidateID, err)
		}
		if !candidateExists || len(candidateData) == 0 {
			continue
		}

		var candidateState pb.FrostVaultState
		if err := unmarshalProtoCompat(candidateData, &candidateState); err != nil {
			return 0, fmt.Errorf("failed to parse vault state for chain %s vault %d: %w", chainName, candidateID, err)
		}
		if isAllocatableVaultStatus(candidateState.Status) {
			return candidateID, nil
		}
	}

	return 0, fmt.Errorf("no allocatable vault (KEY_READY/ACTIVE) for chain %s", chainName)
}

type WitnessServiceAware interface {
	SetWitnessService(svc *witness.Service)
}

                                                                  

                                                                  
type WitnessStakeTxHandler struct {
	witnessSvc *witness.Service
}

                                                    
func (h *WitnessStakeTxHandler) SetWitnessService(svc *witness.Service) {
	h.witnessSvc = svc
}

func (h *WitnessStakeTxHandler) Kind() string {
	return "witness_stake"
}

func (h *WitnessStakeTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	stakeTx, ok := tx.GetContent().(*pb.AnyTx_WitnessStakeTx)
	if !ok {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "not a witness stake transaction"}, fmt.Errorf("not a witness stake transaction")
	}

	stake := stakeTx.WitnessStakeTx
	if stake == nil || stake.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid witness stake transaction"}, fmt.Errorf("invalid witness stake transaction")
	}

	address := stake.Base.FromAddress
	if stake.Op != pb.OrderOp_ADD && stake.Op != pb.OrderOp_REMOVE {
		return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "invalid witness stake operation"}, nil
	}

	accountKey := keys.KeyAccount(address)
	accountData, accountExists, err := sv.Get(accountKey)
	if err != nil {
		return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "failed to read account"}, err
	}

	var account pb.Account
	if accountExists {
		if err := unmarshalProtoCompat(accountData, &account); err != nil {
			return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "failed to parse account"}, err
		}
	} else {
		return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "account not found"}, fmt.Errorf("account not found")
	}

	                                                            
	witnessKey := keys.KeyWitnessInfo(address)
	witnessData, witnessExists, err := sv.Get(witnessKey)
	if err != nil {
		return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "failed to read witness info"}, err
	}

	var witnessInfo pb.WitnessInfo
	if witnessExists {
		if err := unmarshalProtoCompat(witnessData, &witnessInfo); err != nil {
			return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "failed to parse witness info"}, err
		}
	} else {
		witnessInfo = pb.WitnessInfo{Address: address, StakeAmount: "0", Status: pb.WitnessStatus_WITNESS_CANDIDATE}
	}

	amount, err := parseWitnessCollateralAmount("witness stake amount", stake.Amount)
	if err != nil || amount.Sign() <= 0 {
		return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "invalid amount"}, fmt.Errorf("invalid amount")
	}

	fbBalance, err := getBalanceStrict(sv, address, "FB")
	if err != nil {
		return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "failed to read witness balance"}, err
	}
	if stake.Op == pb.OrderOp_ADD {
		if h.witnessSvc != nil {
			minStake, err := parseWitnessCollateralAmount("minimum witness stake", h.witnessSvc.ConfigSnapshot().MinStakeAmount)
			if err != nil {
				return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "invalid minimum stake config"}, err
			}
			if amount.Cmp(minStake) < 0 {
				return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: witness.ErrInsufficientStake.Error()}, witness.ErrInsufficientStake
			}
		}

		balance, err := parseWitnessCollateralAmount("balance", fbBalance.Balance)
		if err != nil {
			return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "invalid balance state"}, err
		}
		if balance.Cmp(amount) < 0 {
			return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "insufficient balance"}, fmt.Errorf("insufficient balance")
		}
		_, err = SafeSub(balance, amount)
		if err != nil {
			return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "balance underflow"}, fmt.Errorf("balance underflow: %w", err)
		}
		lockedBalance, err := parseWitnessCollateralAmount("witness locked balance", fbBalance.WitnessLockedBalance)
		if err != nil {
			return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "invalid locked balance state"}, err
		}
		                                                                 
		_, err = SafeAdd(lockedBalance, amount)
		if err != nil {
			return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "locked balance overflow"}, fmt.Errorf("locked balance overflow: %w", err)
		}
		currentStake, err := parseWitnessCollateralAmount("witness stake amount", witnessInfo.StakeAmount)
		if err != nil {
			return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "invalid witness stake state"}, err
		}
		reserved, err := parseWitnessCollateralAmount("reserved witness stake", witnessInfo.ReservedStakeAmount)
		if err != nil || reserved.Cmp(currentStake) > 0 || lockedBalance.Cmp(currentStake) != 0 {
			return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "inconsistent witness collateral state"}, fmt.Errorf("inconsistent witness collateral state")
		}
		                                                                            
		newStake, err := SafeAdd(currentStake, amount)
		if err != nil {
			return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "stake amount overflow"}, fmt.Errorf("stake amount overflow: %w", err)
		}
		witnessInfo.StakeAmount = newStake.String()
		                                                                                             
		if witnessInfo.Status != pb.WitnessStatus_WITNESS_UNSTAKING {
			witnessInfo.Status = pb.WitnessStatus_WITNESS_ACTIVE
		}
	} else {
		if witnessInfo.Status != pb.WitnessStatus_WITNESS_ACTIVE {
			return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "witness is not active"}, fmt.Errorf("witness is not active")
		}
		witnessInfo.Status = pb.WitnessStatus_WITNESS_UNSTAKING
		witnessInfo.UnstakeHeight = stake.Base.ExecutedHeight
	}

	witnessInfoData, err := proto.Marshal(&witnessInfo)
	if err != nil {
		return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "failed to marshal witness info"}, err
	}
	if stake.Op == pb.OrderOp_ADD {
		snapshot := sv.Snapshot()
		if err := NewLedger(sv).Move(MoveRequest{
			Account: address, Token: FeeToken, Amount: amount, From: BucketAvailable, To: BucketWitnessLocked,
			Reason: "witness_stake", Height: stake.Base.ExecutedHeight, TxID: stake.Base.TxId,
		}); err != nil {
			if revertErr := sv.Revert(snapshot); revertErr != nil {
				return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: "witness stake rollback failed"}, revertErr
			}
			return nil, &Receipt{TxID: stake.Base.TxId, Status: "FAILED", Error: err.Error()}, err
		}
	}
	sv.Set(witnessKey, witnessInfoData)

	                                                                 
	if h.witnessSvc != nil {
		h.witnessSvc.LoadWitness(&witnessInfo)
	}

	return nil, &Receipt{TxID: stake.Base.TxId, Status: "SUCCEED"}, nil
}

func (h *WitnessStakeTxHandler) Apply(tx *pb.AnyTx) error {
	return nil
}

                                                                    

                                                                                                
type WitnessRequestTxHandler struct {
	witnessSvc *witness.Service
	VaultCount uint32
}

                                                    
func (h *WitnessRequestTxHandler) SetWitnessService(svc *witness.Service) {
	h.witnessSvc = svc
}

func (h *WitnessRequestTxHandler) Kind() string {
	return "witness_request"
}

func (h *WitnessRequestTxHandler) DryRun(tx *pb.AnyTx, sv StateView) (ops []WriteOp, receipt *Receipt, resultErr error) {
	requestTx, ok := tx.GetContent().(*pb.AnyTx_WitnessRequestTx)
	if !ok {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "not a witness request transaction"}, fmt.Errorf("not a witness request transaction")
	}

	request := requestTx.WitnessRequestTx
	if request == nil || request.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid witness request transaction"}, fmt.Errorf("invalid witness request transaction")
	}
	clonedRequest, ok := proto.Clone(request).(*pb.WitnessRequestTx)
	if !ok || clonedRequest == nil || clonedRequest.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "clone witness request transaction failed"}, fmt.Errorf("clone witness request transaction failed")
	}
	request = clonedRequest
	if trustedChainID := networkChainIDFromView(sv); trustedChainID != 0 && request.Base.ChainId != trustedChainID {
		err := fmt.Errorf("witness request chain %d does not match executor network %d", request.Base.ChainId, trustedChainID)
		return nil, &Receipt{TxID: request.Base.TxId, Status: "FAILED", Error: err.Error()}, err
	}
	economicsActive := protocol.WitnessRechargeEconomicsActive(request.Base.ChainId)
	if economicsActive {
		if err := validateWitnessRechargeMinFee(request.RechargeFee); err != nil {
			return nil, &Receipt{TxID: request.Base.TxId, Status: "FAILED", Error: err.Error()}, err
		}
	}
	if strings.TrimSpace(request.ReceiverAddress) == "" {
		return nil, &Receipt{TxID: request.Base.TxId, Status: "FAILED", Error: "receiver address is required"}, fmt.Errorf("receiver address is required")
	}
	amount, err := ParseBalance(request.Amount)
	if err != nil || amount.Sign() <= 0 {
		if err == nil {
			err = fmt.Errorf("recharge amount must be positive")
		}
		return nil, &Receipt{TxID: request.Base.TxId, Status: "FAILED", Error: "invalid recharge amount"}, err
	}

	ws := make([]WriteOp, 0)
	requestID := request.Base.TxId

	existingKey := keys.KeyRechargeRequest(requestID)
	_, exists, err := sv.Get(existingKey)
	if err != nil {
		return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: err.Error()}, err
	}
	if exists {
		                                                                   
		return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: "request already exists"}, nil
	}

	chainName, vaultCount, err := resolveVaultChainAndCount(sv, request.NativeChain)
	if err != nil {
		return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: err.Error()}, err
	}
	if isBTCChainName(chainName) && !amount.IsUint64() {
		return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: "BTC recharge amount exceeds uint64"}, fmt.Errorf("BTC recharge amount exceeds uint64")
	}
	btcPolicyVersion := uint32(0)
	if isBTCChainName(chainName) {
		active, err := chain.BTCIsolationActive(sv.Get)
		if err != nil {
			return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: err.Error()}, err
		}
		if active {
			if amount.Uint64() < protocol.MinBTCRechargeSats {
				err := fmt.Errorf("BTC recharge minimum is %d sat (0.001 BTC) per outpoint", protocol.MinBTCRechargeSats)
				return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: err.Error()}, err
			}
			btcPolicyVersion = protocol.BTCIsolationVersion
			request.Amount = amount.String()
		}
	}

	canonicalNativeHash, nativeTxKey, err := rechargeNativeLockIdentity(chainName, request.NativeTxHash, request.NativeVout)
	if err != nil {
		return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: err.Error()}, err
	}
	request.NativeTxHash = canonicalNativeHash
	if isBTCChainName(chainName) {
		outputs, err := custodyevidence.InternalBTCOutputs(sv.Get, canonicalNativeHash)
		if err != nil {
			return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: err.Error()}, err
		}
		for _, vout := range outputs.InternalVouts {
			if vout == request.NativeVout {
				return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: "custody change or sweep is not a deposit"}, nil
			}
		}
	}
	if !isBTCChainName(chainName) {
		cfg, err := loadFrostVaultConfig(sv, chainName)
		if err != nil {
			return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: err.Error()}, err
		}
		if err := validateAccountChainRecharge(cfg, request); err != nil {
			return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: err.Error()}, err
		}
		if err := ensureLegacyRechargeIndex(sv); err != nil {
			return nil, nil, err
		}
		for _, key := range []string{keys.KeyRechargeLegacyNative(chainName, canonicalNativeHash), keys.KeyRechargeEvent(chainName, canonicalNativeHash, request.DepositEventIndex)} {
			if _, used, err := sv.Get(key); err != nil {
				return nil, nil, err
			} else if used {
				return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: "native deposit already used or reserved by legacy recharge"}, nil
			}
		}
		nativeTxKey = keys.KeyRechargeEventPending(chainName, canonicalNativeHash, request.DepositEventIndex)
	} else if request.DepositProtocol != 0 || request.DepositEventIndex != 0 || request.VaultId != 0 {
		return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: "BTC does not use account-chain deposit fields"}, fmt.Errorf("BTC does not use account-chain deposit fields")
	}
	_, nativeExists, _ := sv.Get(nativeTxKey)
	if nativeExists {
		return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: "native tx already used"}, nil
	}

	assetID, err := resolveWitnessRequestAsset(sv, chainName, request)
	if err != nil {
		return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: err.Error()}, err
	}

	                
	                                                         
	                                                                                                     
	var vaultID uint32
	if isBTCChainName(chainName) {
		vaultID, err = lookupVaultIDByScriptPubKey(sv, chainName, vaultCount, request.NativeScript, request.ReceiverAddress)
	} else {
		vaultID = request.VaultId
		data, found, readErr := sv.Get(keys.KeyFrostVaultState(chainName, vaultID))
		if readErr != nil {
			err = readErr
		} else if !found {
			err = fmt.Errorf("deposit vault state not found")
		} else {
			var state pb.FrostVaultState
			if decodeErr := unmarshalProtoCompat(data, &state); decodeErr != nil {
				err = decodeErr
			} else if !isAllocatableVaultStatus(state.Status) {
				err = fmt.Errorf("deposit vault is not active")
			}
		}
	}
	if err != nil {
		return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: err.Error()}, err
	}

	selectionDelay := uint64(6)
	if btcPolicyVersion == protocol.BTCIsolationVersion {
		if err := rejectBTCRechargeDuringFrozenMigration(sv, vaultID); err != nil {
			return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: err.Error()}, err
		}
	}
	if h.witnessSvc != nil {
		selectionDelay = h.witnessSvc.SelectionBeaconDepth()
	}
	rechargeRequest := &pb.RechargeRequest{
		RequestId: requestID, NativeChain: request.NativeChain, NativeTxHash: request.NativeTxHash,
		NativeVout: request.NativeVout, NativeScript: request.NativeScript, TokenAddress: assetID,
		Amount: request.Amount, ReceiverAddress: request.ReceiverAddress, RequesterAddress: request.Base.FromAddress,
		Status: pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION, CreateHeight: request.Base.ExecutedHeight,
		RechargeFee: request.RechargeFee, NativeContract: request.NativeContract,
		DepositEventIndex: request.DepositEventIndex, DepositProtocol: request.DepositProtocol,
		SelectionHeight:    addRechargeHeightSaturating(request.Base.ExecutedHeight, selectionDelay),
		BtcProtocolVersion: btcPolicyVersion,
	}
	rechargeRequest.EconomicsPolicy = protocol.RechargeEconomicsLegacy
	if economicsActive {
		rechargeRequest.EconomicsPolicy = protocol.RechargeEconomicsLaunchV1
	}
	rechargeRequest.TokenAddress = assetID

	rechargeRequest.NativeChain = chainName
	rechargeRequest.VaultId = vaultID
	snap := sv.Snapshot()
	defer func() {
		if receipt == nil || receipt.Status != "SUCCEED" {
			_ = sv.Revert(snap)
		}
	}()
	mix := uint64(3)
	if h.witnessSvc != nil {
		mix = h.witnessSvc.BeaconMixBlocks()
	}
	if err := setRechargeRequestWithIndexes(sv, rechargeRequest, mix); err != nil {
		return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: err.Error()}, err
	}
	                                                                                        
	                                                                                   
	                                                                                          
	if err := collectWitnessServiceFee(sv, rechargeRequest, economicsActive); err != nil {
		return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: err.Error()}, err
	}
	if economicsActive {
		if err := collectRequesterBond(sv, rechargeRequest); err != nil {
			return nil, &Receipt{TxID: requestID, Status: "FAILED", Error: err.Error()}, err
		}
	}
	for _, op := range ws {
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, op.Value)
		}
	}
	sv.Set(nativeTxKey, []byte(requestID))
	return nil, &Receipt{TxID: requestID, Status: "SUCCEED"}, nil
}

func (h *WitnessRequestTxHandler) Apply(tx *pb.AnyTx) error {
	return nil
}

                                                                 

                                                                                                      
type WitnessVoteTxHandler struct {
	witnessSvc *witness.Service
}

                                                    
func (h *WitnessVoteTxHandler) SetWitnessService(svc *witness.Service) {
	h.witnessSvc = svc
}

func (h *WitnessVoteTxHandler) Kind() string {
	return "witness_vote"
}

func (h *WitnessVoteTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	voteTx, ok := tx.GetContent().(*pb.AnyTx_WitnessVoteTx)
	if !ok {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "not a witness vote transaction"}, fmt.Errorf("not a witness vote transaction")
	}

	vote := voteTx.WitnessVoteTx
	if vote == nil || vote.Base == nil || vote.Vote == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid witness vote transaction"}, fmt.Errorf("invalid witness vote transaction")
	}
	vote = proto.Clone(vote).(*pb.WitnessVoteTx)
	if !witness.IsValidVoteType(vote.Vote.VoteType) {
		return nil, &Receipt{TxID: vote.Base.TxId, Status: "FAILED", Error: "invalid vote type"}, nil
	}

	ws := make([]WriteOp, 0, 4)
	requestID := vote.Vote.RequestId
	witnessAddr := vote.Vote.WitnessAddress
	fromAddr := vote.Base.FromAddress

	if fromAddr == "" || witnessAddr == "" || fromAddr != witnessAddr {
		logs.Warn(
			"[VM][WitnessVoteReject] signer_mismatch tx=%s request=%s from=%s vote_witness=%s",
			vote.Base.TxId, requestID, fromAddr, witnessAddr,
		)
		return nil, &Receipt{TxID: vote.Base.TxId, Status: "FAILED", Error: "vote signer does not match witness address"}, nil
	}

	requestKey := keys.KeyRechargeRequest(requestID)
	requestData, requestExists, err := sv.Get(requestKey)
	if err != nil || !requestExists {
		                                                                         
		                                                                                                                                                    
		logs.Warn(
			"[VM][WitnessVoteReject] request_not_found tx=%s request=%s from=%s vote_witness=%s sv_get_err=%v",
			vote.Base.TxId, requestID, fromAddr, witnessAddr, err,
		)
		return nil, &Receipt{TxID: vote.Base.TxId, Status: "FAILED", Error: "request not found"}, nil
	}

	var request pb.RechargeRequest
	if err := unmarshalProtoCompat(requestData, &request); err != nil {
		return nil, &Receipt{TxID: vote.Base.TxId, Status: "FAILED", Error: "failed to parse request"}, err
	}
	if !rechargeRequestAcceptsWitnessVote(&request, vote.Base.ExecutedHeight) {
		return nil, &Receipt{TxID: vote.Base.TxId, Status: "FAILED", Error: "request is not accepting votes"}, nil
	}
	selected := false
	for _, member := range request.SelectedWitnesses {
		if member == witnessAddr {
			selected = true
			break
		}
	}
	if !selected {
		return nil, &Receipt{TxID: vote.Base.TxId, Status: "FAILED", Error: "witness not selected"}, nil
	}
	if vote.Vote.Round != request.Round {
		return nil, &Receipt{TxID: vote.Base.TxId, Status: "FAILED", Error: witness.ErrWitnessVoteRoundMismatch.Error()}, nil
	}

	voteKey := keys.KeyWitnessVote(requestID, witnessAddr)
	previousVoteData, voteExists, _ := sv.Get(voteKey)
	if voteExists {
		var previousVote pb.WitnessVote
		if err := unmarshalProtoCompat(previousVoteData, &previousVote); err != nil || previousVote.Round == request.Round {
			logs.Info(
				"[VM][WitnessVoteSkip] duplicate_vote tx=%s request=%s round=%d from=%s vote_witness=%s vote_key=%s",
				vote.Base.TxId, requestID, request.Round, fromAddr, witnessAddr, voteKey,
			)
			return nil, &Receipt{TxID: vote.Base.TxId, Status: "FAILED", Error: "duplicate vote"}, nil
		}
	}

	                                                                          
	                                                                          
	                                                                     
	witnessKey := keys.KeyWitnessInfo(witnessAddr)
	witnessData, witnessExists, err := sv.Get(witnessKey)
	if err != nil {
		return nil, &Receipt{TxID: vote.Base.TxId, Status: "FAILED", Error: "failed to read witness info"}, err
	}
	var witnessInfo *pb.WitnessInfo
	if witnessExists {
		witnessInfo = &pb.WitnessInfo{}
		if err := unmarshalProtoCompat(witnessData, witnessInfo); err != nil {
			return nil, &Receipt{TxID: vote.Base.TxId, Status: "FAILED", Error: "failed to parse witness info"}, err
		}
		if h.witnessSvc != nil {
			h.witnessSvc.LoadWitness(proto.Clone(witnessInfo).(*pb.WitnessInfo))
		}
	}

	                                                                                                 
	                                                      
	var finalRequest *pb.RechargeRequest
	if h.witnessSvc != nil {
		                                                                       
		                                                                      
		                                                     
		h.witnessSvc.LoadRequest(proto.Clone(&request).(*pb.RechargeRequest))
		vote.Vote.TxId = vote.Base.TxId
		updatedRequest, err := h.witnessSvc.ProcessVoteAtHeight(vote.Vote, vote.Base.ExecutedHeight)
		if err != nil {
			memStatus := "N/A"
			memRound := uint32(0)
			memSelected := []string(nil)
			memVotes := -1
			memErrStr := ""
			if memReq, memErr := h.witnessSvc.GetRequest(requestID); memErr != nil {
				memErrStr = memErr.Error()
			} else if memReq != nil {
				memStatus = memReq.Status.String()
				memRound = memReq.Round
				memSelected = append(memSelected, memReq.SelectedWitnesses...)
				memVotes = len(memReq.Votes)
			}
			logs.Warn(
				"[VM][WitnessVoteReject] service_reject tx=%s request=%s from=%s vote_witness=%s vote_type=%s svc_err=%v sv_status=%s sv_round=%d sv_selected=%v sv_votes=%d mem_status=%s mem_round=%d mem_selected=%v mem_votes=%d mem_err=%s",
				vote.Base.TxId,
				requestID,
				fromAddr,
				witnessAddr,
				vote.Vote.VoteType.String(),
				err,
				request.Status.String(),
				request.Round,
				request.SelectedWitnesses,
				len(request.Votes),
				memStatus,
				memRound,
				memSelected,
				memVotes,
				memErrStr,
			)
			                                                                                                   
			return nil, &Receipt{TxID: vote.Base.TxId, Status: "FAILED", Error: err.Error()}, nil
		}
		logs.Info(
			"[VM][WitnessVoteAccept] tx=%s request=%s from=%s vote_witness=%s vote_type=%s prev_status=%s prev_round=%d next_status=%s next_round=%d",
			vote.Base.TxId,
			requestID,
			fromAddr,
			witnessAddr,
			vote.Vote.VoteType.String(),
			request.Status.String(),
			request.Round,
			updatedRequest.Status.String(),
			updatedRequest.Round,
		)
		finalRequest = updatedRequest
	} else {
		logs.Warn(
			"[VM][WitnessVote] witness_service_nil tx=%s request=%s from=%s vote_witness=%s",
			vote.Base.TxId, requestID, fromAddr, witnessAddr,
		)

		request.Votes = append(request.Votes, vote.Vote)
		switch vote.Vote.VoteType {
		case pb.WitnessVoteType_VOTE_PASS:
			request.PassCount++
		case pb.WitnessVoteType_VOTE_FAIL:
			request.FailCount++
		case pb.WitnessVoteType_VOTE_ABSTAIN:
			request.AbstainCount++
		}
		finalRequest = &request
	}

	voteData, err := proto.Marshal(vote.Vote)
	if err != nil {
		return nil, &Receipt{TxID: vote.Base.TxId, Status: "FAILED", Error: "failed to marshal vote"}, err
	}
	ws = append(ws, WriteOp{Key: voteKey, Value: voteData, Del: false, Category: "witness_vote"})

	finalRequest.OriginalPositions = witness.LastDirectionalPositions(finalRequest.OriginalPositions, vote.Vote)
	updatedRequestData, err := proto.Marshal(finalRequest)
	if err != nil {
		return nil, &Receipt{TxID: vote.Base.TxId, Status: "FAILED", Error: "failed to marshal request"}, err
	}

	if witnessInfo != nil {
		witnessInfo.TotalWitnessCount++
		switch vote.Vote.VoteType {
		case pb.WitnessVoteType_VOTE_PASS:
			witnessInfo.PassCount++
		case pb.WitnessVoteType_VOTE_FAIL:
			witnessInfo.FailCount++
		case pb.WitnessVoteType_VOTE_ABSTAIN:
			witnessInfo.AbstainCount++
		}
		updatedWitnessData, err := proto.Marshal(witnessInfo)
		if err != nil {
			return nil, &Receipt{TxID: vote.Base.TxId, Status: "FAILED", Error: "failed to marshal witness info"}, err
		}
		ws = append(ws, WriteOp{Key: witnessKey, Value: updatedWitnessData, Category: "witness"})
	}

	                                                                                              
	ws = append(ws, WriteOp{Key: requestKey, Value: updatedRequestData, Del: false, Category: "witness_request"})

	                                                                                                                  
	                                                                                
	if h.witnessSvc != nil && finalRequest.Status == pb.RechargeRequestStatus_RECHARGE_CHALLENGED && finalRequest.ChallengeId != "" {
		challengeKey := keys.KeyChallengeRecord(finalRequest.ChallengeId)
		if _, exists, _ := sv.Get(challengeKey); !exists {
			if rec, gerr := h.witnessSvc.GetChallenge(finalRequest.ChallengeId); gerr == nil && rec != nil {
				if recData, merr := proto.Marshal(rec); merr == nil {
					ws = append(ws, WriteOp{Key: challengeKey, Value: recData, Del: false, Category: "challenge"})
				}
			}
		}
	}

	return ws, &Receipt{TxID: vote.Base.TxId, Status: "SUCCEED", WriteCount: len(ws)}, nil
}

func (h *WitnessVoteTxHandler) Apply(tx *pb.AnyTx) error {
	return nil
}

                                                                      

                                                                                                     
type WitnessChallengeTxHandler struct {
	witnessSvc *witness.Service
}

                                                    
func (h *WitnessChallengeTxHandler) SetWitnessService(svc *witness.Service) {
	h.witnessSvc = svc
}

func (h *WitnessChallengeTxHandler) Kind() string {
	return "witness_challenge"
}

func (h *WitnessChallengeTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	challengeTx, ok := tx.GetContent().(*pb.AnyTx_WitnessChallengeTx)
	if !ok {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "not a witness challenge transaction"}, fmt.Errorf("not a witness challenge transaction")
	}

	challenge := challengeTx.WitnessChallengeTx
	if challenge == nil || challenge.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid witness challenge transaction"}, fmt.Errorf("invalid witness challenge transaction")
	}

	return h.fundChallenge(challenge, sv)
}

func (h *WitnessChallengeTxHandler) Apply(tx *pb.AnyTx) error {
	return nil
}

func canonicalChallengeContribution(challenge *pb.WitnessChallengeTx) *pb.WitnessChallengeTx {
	if challenge == nil {
		return nil
	}
	cp, ok := proto.Clone(challenge).(*pb.WitnessChallengeTx)
	if !ok || cp == nil {
		return challenge
	}
	if cp.Base != nil {
		cp.Base.Status = pb.Status_PENDING
		cp.Base.ExecutedHeight = 0
		cp.Base.StatusText = ""
	}
	return cp
}

                                                                     

                                                                  
type ArbitrationVoteTxHandler struct {
	witnessSvc *witness.Service
}

                                          
func (h *ArbitrationVoteTxHandler) SetWitnessService(svc *witness.Service) {
	h.witnessSvc = svc
}

func (h *ArbitrationVoteTxHandler) Kind() string {
	return "arbitration_vote"
}

func (h *ArbitrationVoteTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	arbVoteTx, ok := tx.GetContent().(*pb.AnyTx_ArbitrationVoteTx)
	if !ok {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "not an arbitration vote transaction"}, fmt.Errorf("not an arbitration vote transaction")
	}

	arbVote := arbVoteTx.ArbitrationVoteTx
	if arbVote == nil || arbVote.Base == nil || arbVote.Vote == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid arbitration vote transaction"}, fmt.Errorf("invalid arbitration vote transaction")
	}
	if !witness.IsValidVoteType(arbVote.Vote.VoteType) {
		return nil, &Receipt{TxID: arbVote.Base.TxId, Status: "FAILED", Error: "invalid vote type"}, nil
	}

	challengeID := arbVote.ChallengeId
	arbitratorAddr := arbVote.Vote.WitnessAddress
	fromAddr := arbVote.Base.FromAddress

	if fromAddr == "" || arbitratorAddr == "" || fromAddr != arbitratorAddr {
		logs.Warn(
			"[VM][ArbitrationVoteReject] signer_mismatch tx=%s challenge=%s from=%s vote_witness=%s",
			arbVote.Base.TxId, challengeID, fromAddr, arbitratorAddr,
		)
		return nil, &Receipt{TxID: arbVote.Base.TxId, Status: "FAILED", Error: "vote signer does not match arbitrator address"}, nil
	}

	challengeKey := keys.KeyChallengeRecord(challengeID)
	rawChallenge, challengeExists, err := sv.Get(challengeKey)
	if err != nil || !challengeExists {
		return nil, &Receipt{TxID: arbVote.Base.TxId, Status: "FAILED", Error: "challenge not found"}, nil
	}
	var stateChallenge pb.ChallengeRecord
	if err := unmarshalProtoCompat(rawChallenge, &stateChallenge); err != nil {
		return nil, &Receipt{TxID: arbVote.Base.TxId, Status: "FAILED", Error: "failed to parse challenge"}, err
	}

	if h.witnessSvc == nil {
		return nil, &Receipt{TxID: arbVote.Base.TxId, Status: "FAILED", Error: "witness service not wired"}, nil
	}
	request := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(stateChallenge.RequestId))
	if request == nil {
		return nil, &Receipt{TxID: arbVote.Base.TxId, Status: "FAILED", Error: "request not found"}, nil
	}
	vote := proto.Clone(arbVote.Vote).(*pb.WitnessVote)
	vote.TxId = arbVote.Base.TxId
	if err := requireWitnessArbitrationVersion(request); err != nil {
		return nil, &Receipt{TxID: arbVote.Base.TxId, Status: "FAILED", Error: err.Error()}, nil
	}
	outcome, err := witness.ComputeArbitrationBallotOutcome(&stateChallenge, request, vote, arbVote.BallotRound, arbVote.Base.ExecutedHeight)
	if err != nil {
		return nil, &Receipt{TxID: arbVote.Base.TxId, Status: "FAILED", Error: err.Error()}, nil
	}
	if err := applyArbitrationOutcomeToState(sv, outcome, h.witnessSvc.BeaconMixBlocks(), 0, arbVote.Base.ExecutedHeight); err != nil {
		return nil, &Receipt{TxID: arbVote.Base.TxId, Status: "FAILED", Error: err.Error()}, err
	}
	if !outcome.Challenge.Finalized {
		vote.BallotRound = arbVote.BallotRound
		data, err := proto.Marshal(vote)
		if err != nil {
			return nil, nil, err
		}
		sv.Set(keys.KeyArbitrationVoteRound(challengeID, arbVote.BallotRound, arbitratorAddr), data)
	}
	return nil, &Receipt{TxID: arbVote.Base.TxId, Status: "SUCCEED"}, nil
}

func (h *ArbitrationVoteTxHandler) Apply(tx *pb.AnyTx) error {
	return nil
}

                                                                       

                                                           
                                                                                                                 
                                                                                          
type WitnessClaimStakeTxHandler struct {
	witnessSvc        *witness.Service
	UnstakeLockBlocks uint64                                             
}

func (h *WitnessClaimStakeTxHandler) SetWitnessService(svc *witness.Service) {
	h.witnessSvc = svc
}

func (h *WitnessClaimStakeTxHandler) Kind() string {
	return "witness_claim_stake"
}

func (h *WitnessClaimStakeTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	claimTx, ok := tx.GetContent().(*pb.AnyTx_WitnessClaimStakeTx)
	if !ok {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "not a witness claim stake transaction"}, fmt.Errorf("not a witness claim stake transaction")
	}
	claim := claimTx.WitnessClaimStakeTx
	if claim == nil || claim.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid witness claim stake transaction"}, fmt.Errorf("invalid witness claim stake transaction")
	}

	address := claim.Base.FromAddress

	witnessKey := keys.KeyWitnessInfo(address)
	witnessData, witnessExists, err := sv.Get(witnessKey)
	if err != nil {
		return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: "failed to read witness info"}, err
	}
	if !witnessExists {
		return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: "witness not found"}, nil
	}
	var witnessInfo pb.WitnessInfo
	if err := unmarshalProtoCompat(witnessData, &witnessInfo); err != nil {
		return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: "failed to parse witness info"}, err
	}

	if witnessInfo.Status != pb.WitnessStatus_WITNESS_UNSTAKING {
		return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: "witness is not in unstaking state"}, nil
	}

	if err := validateWitnessStakeClaim(&witnessInfo); err != nil {
		return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: err.Error()}, nil
	}

	                                                                                            
	if claim.Base.ExecutedHeight < witnessInfo.UnstakeHeight || claim.Base.ExecutedHeight-witnessInfo.UnstakeHeight < h.UnstakeLockBlocks {
		return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: "unstake lock period not expired"}, nil
	}

	claimable, err := parseWitnessCollateralAmount("witness stake amount", witnessInfo.StakeAmount)
	if err != nil {
		return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: "invalid stake amount"}, err
	}
	if claimable.Sign() <= 0 {
		return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: "nothing to claim"}, nil
	}

	fbBalance, err := getBalanceStrict(sv, address, "FB")
	if err != nil {
		return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: "failed to read witness balance"}, err
	}
	lockedBalance, err := parseWitnessCollateralAmount("witness locked balance", fbBalance.WitnessLockedBalance)
	if err != nil {
		return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: "invalid locked balance state"}, err
	}
	if lockedBalance.Cmp(claimable) != 0 {
		return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: "witness locked balance does not match stake"}, fmt.Errorf("locked balance mismatch: locked=%s claim=%s", lockedBalance, claimable)
	}
	_, err = SafeSub(lockedBalance, claimable)
	if err != nil {
		return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: "locked balance underflow"}, err
	}
	balance, err := parseWitnessCollateralAmount("balance", fbBalance.Balance)
	if err != nil {
		return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: "invalid balance state"}, err
	}
	_, err = SafeAdd(balance, claimable)
	if err != nil {
		return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: "balance overflow"}, err
	}
	                                                                   
	witnessInfo.Status = pb.WitnessStatus_WITNESS_EXITED
	witnessInfo.StakeAmount = "0"

	witnessInfoData, err := proto.Marshal(&witnessInfo)
	if err != nil {
		return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: "failed to marshal witness info"}, err
	}
	snapshot := sv.Snapshot()
	if err := NewLedger(sv).Move(MoveRequest{
		Account: address, Token: FeeToken, Amount: claimable, From: BucketWitnessLocked, To: BucketAvailable,
		Reason: "witness_stake_claim", Height: claim.Base.ExecutedHeight, TxID: claim.Base.TxId,
	}); err != nil {
		if revertErr := sv.Revert(snapshot); revertErr != nil {
			return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: "witness claim rollback failed"}, revertErr
		}
		return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: err.Error()}, err
	}
	sv.Set(witnessKey, witnessInfoData)

	if h.witnessSvc != nil {
		h.witnessSvc.LoadWitness(&witnessInfo)
	}

	return nil, &Receipt{TxID: claim.Base.TxId, Status: "SUCCEED"}, nil
}

func (h *WitnessClaimStakeTxHandler) Apply(tx *pb.AnyTx) error {
	return nil
}

                                                                        

                                                                         
type WitnessClaimRewardTxHandler struct {
	witnessSvc *witness.Service
}

                                          
func (h *WitnessClaimRewardTxHandler) SetWitnessService(svc *witness.Service) {
	h.witnessSvc = svc
}

func (h *WitnessClaimRewardTxHandler) Kind() string {
	return "witness_claim_reward"
}

func (h *WitnessClaimRewardTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	claimTx, ok := tx.GetContent().(*pb.AnyTx_WitnessClaimRewardTx)
	if !ok {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "not a witness claim reward transaction"}, fmt.Errorf("not a witness claim reward transaction")
	}

	claim := claimTx.WitnessClaimRewardTx
	if claim == nil || claim.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid witness claim reward transaction"}, fmt.Errorf("invalid witness claim reward transaction")
	}

	                                                                       
	                                                                           
	return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: "witness rewards are paid automatically at case settlement; legacy unbacked rewards cannot be claimed"}, nil
}

func (h *WitnessClaimRewardTxHandler) Apply(tx *pb.AnyTx) error {
	return nil
}
