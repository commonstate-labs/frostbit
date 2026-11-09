package vm

import (
	"dex/keys"
	"dex/liveness"
	"dex/pb"
	"dex/protocol"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

                                                               
var MinMinerStake = new(big.Int).SetUint64(protocol.MinMinerStakeRaw)

                                                                               
                                                                               
                           
type MinerTxHandler struct{}

func (h *MinerTxHandler) Kind() string { return "miner" }

func (h *MinerTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	wrapper, ok := tx.GetContent().(*pb.AnyTx_MinerTx)
	if !ok {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "not a miner transaction"}, fmt.Errorf("not a miner transaction")
	}
	minerTx := wrapper.MinerTx
	if minerTx == nil || minerTx.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid miner transaction"}, fmt.Errorf("invalid miner transaction")
	}
	switch minerTx.Op {
	case pb.OrderOp_ADD:
		return h.handleStartMining(minerTx, sv)
	case pb.OrderOp_REMOVE:
		return h.handleStopMining(minerTx, sv)
	default:
		return nil, &Receipt{TxID: minerTx.Base.TxId, Status: "FAILED", Error: "unknown miner operation"}, fmt.Errorf("unknown miner operation: %v", minerTx.Op)
	}
}

                                                                               
                                                                             
                                                                          
func (h *MinerTxHandler) handleStartMining(minerTx *pb.MinerTx, sv StateView) ([]WriteOp, *Receipt, error) {
	txID := minerTx.Base.TxId
	fail := func(msg string) ([]WriteOp, *Receipt, error) {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: msg}, errors.New(msg)
	}
	amount, err := parsePositiveBalanceStrict("mining amount", minerTx.Amount)
	if err != nil {
		return fail("invalid mining amount")
	}
	hot := ResolveMinerHot(minerTx)
	if hot == "" || hot != minerTx.Base.FromAddress {
		return fail("miner_address must equal the signing hot address")
	}
	accountKey := keys.KeyAccount(hot)
	accountData, accountExists, err := sv.Get(accountKey)
	if err != nil || !accountExists {
		return fail("miner account not found")
	}
	var account pb.Account
	if err := unmarshalProtoCompat(accountData, &account); err != nil {
		return fail("failed to parse miner account")
	}
	if account.Address != hot {
		return fail("miner account identity mismatch")
	}
	if _, exists, err := sv.Get(keys.KeyMinerExitRecord(hot)); err != nil {
		return fail("failed to read miner exit state")
	} else if exists {
		return fail("miner exit is still frozen")
	}
	if err := validateMinerRejoinState(sv, &account, hot); err != nil {
		return fail(err.Error())
	}
	rewardAddress, identityBinding, err := prepareMinerIdentity(minerTx, &account, sv)
	if err != nil {
		return fail(err.Error())
	}
	account.RewardAddress = rewardAddress

	const nativeToken = "FB"
	minerBalance := GetBalance(sv, hot, nativeToken)
	available, err := parseBalanceStrict("balance", minerBalance.Balance)
	if err != nil {
		return fail("invalid balance state")
	}
	if available.Cmp(amount) < 0 {
		return fail("insufficient balance for mining")
	}
	nextAvailable, err := SafeSub(available, amount)
	if err != nil {
		return fail("balance underflow")
	}
	locked, err := parseBalanceStrict("miner locked balance", minerBalance.MinerLockedBalance)
	if err != nil {
		return fail("invalid locked balance state")
	}
	nextLocked, err := SafeAdd(locked, amount)
	if err != nil {
		return fail("locked balance overflow")
	}
	if nextLocked.Cmp(MinMinerStake) < 0 {
		return fail(fmt.Sprintf("total stake %s below minimum %s", nextLocked, MinMinerStake))
	}
	if nextLocked.BitLen() > 64 {
		return fail("total stake exceeds the uint64 consensus-weight limit")
	}
	minerBalance.Balance = nextAvailable.String()
	minerBalance.MinerLockedBalance = nextLocked.String()
	SetBalance(sv, hot, nativeToken, minerBalance)

	account.IsMiner = true
	updatedAccount, err := MarshalProto(&account)
	if err != nil {
		return fail("failed to marshal miner account")
	}
	ws := []WriteOp{balanceWriteOp(sv, hot, nativeToken)}
	if identityBinding != nil {
		ws = append(ws, *identityBinding)
	}
	ws = append(ws, WriteOp{Key: accountKey, Value: updatedAccount, Category: "account"})
	return ws, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: len(ws)}, nil
}

func validateMinerRejoinState(sv StateView, account *pb.Account, hot string) error {
	locked, err := parseBalanceStrict("miner locked balance", GetBalance(sv, hot, "FB").MinerLockedBalance)
	if err != nil {
		return fmt.Errorf("invalid locked balance state")
	}
	if !account.IsMiner && locked.Sign() > 0 {
		return fmt.Errorf("existing locked stake must complete exit before rejoining")
	}
	if account.Index > uint64(^uint32(0)) {
		return fmt.Errorf("invalid miner slot")
	}
	raw, exists, err := sv.Get(keys.KeyLivenessMiner(uint32(account.Index)))
	if err != nil || !exists || len(raw) == 0 {
		return nil
	}
	var info liveness.MinerInfo
	if json.Unmarshal(raw, &info) != nil || info.Address != hot {
		return nil
	}
	if info.State == liveness.StateFrozen {
		return fmt.Errorf("miner is still registered as frozen; complete protocol cleanup before rejoining")
	}
	if !account.IsMiner {
		return fmt.Errorf("miner account and liveness state are inconsistent")
	}
	return nil
}

                                                                                
                                                                             
func (h *MinerTxHandler) handleStopMining(minerTx *pb.MinerTx, sv StateView) ([]WriteOp, *Receipt, error) {
	txID := minerTx.Base.TxId
	fail := func(msg string) ([]WriteOp, *Receipt, error) {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: msg}, errors.New(msg)
	}
	hot := ResolveMinerHot(minerTx)
	if hot == "" || hot != minerTx.Base.FromAddress {
		return fail("exit must be signed by the hot miner address")
	}
	if minerTx.RewardAddress != "" || len(minerTx.BlsPublicKey) > 0 || len(minerTx.BlsProof) > 0 || minerTx.Endpoint != "" {
		return fail("REMOVE must not override registered miner identity fields")
	}
	accountKey := keys.KeyAccount(hot)
	accountData, accountExists, err := sv.Get(accountKey)
	if err != nil || !accountExists {
		return fail("miner account not found")
	}
	var account pb.Account
	if err := unmarshalProtoCompat(accountData, &account); err != nil {
		return fail("failed to parse miner account")
	}
	if account.Address != hot {
		return fail("miner account identity mismatch")
	}
	if _, exists, err := sv.Get(keys.KeyMinerExitRecord(hot)); err != nil {
		return fail("failed to read miner exit state")
	} else if exists {
		return fail("miner exit is already pending")
	}

	transitionKvs, _ := sv.Scan(keys.KeyFrostVaultTransitionActivePrefix())
	for _, transitionIndexValue := range transitionKvs {
		transitionKey := string(transitionIndexValue)
		if transitionKey == "" {
			continue
		}
		transitionData, exists, readErr := sv.Get(transitionKey)
		if readErr != nil || !exists || len(transitionData) == 0 {
			continue
		}
		var transition pb.VaultTransitionState
		if unmarshalProtoCompat(transitionData, &transition) != nil {
			continue
		}
		if transition.DkgStatus == DKGStatusNotStarted || transition.DkgStatus == DKGStatusCommitting ||
			transition.DkgStatus == DKGStatusSharing || transition.DkgStatus == DKGStatusResolving ||
			transition.DkgStatus == DKGStatusKeyReady {
			for _, member := range transition.NewCommitteeMembers {
				if member == hot {
					return fail(fmt.Sprintf("miner is in active DKG session (chain=%s vault=%d epoch=%d)", transition.Chain, transition.VaultId, transition.EpochId))
				}
			}
		}
		if transition.Lifecycle != VaultLifecycleActive {
			for _, member := range transition.OldCommitteeMembers {
				if member == hot {
					return fail(fmt.Sprintf("miner is in pending vault handover (chain=%s vault=%d epoch=%d lifecycle=%s)", transition.Chain, transition.VaultId, transition.EpochId, transition.Lifecycle))
				}
			}
		}
	}

	locked, err := parseBalanceStrict("miner locked balance", GetBalance(sv, hot, "FB").MinerLockedBalance)
	if err != nil {
		return fail("invalid locked balance state")
	}
	if locked.Sign() <= 0 {
		return fail("no locked balance to release")
	}
	rewardAddress, err := canonicalMinerRewardAddress(hot, account.RewardAddress)
	if err != nil {
		return fail(err.Error())
	}
	exitData, err := MarshalProto(&pb.MinerExitRecord{MinerAddress: hot, ExitHeight: minerTx.Base.ExecutedHeight, RewardAddress: rewardAddress})
	if err != nil {
		return fail("failed to marshal miner exit record")
	}
	account.IsMiner = false
	account.RewardAddress = rewardAddress
	updatedAccount, err := MarshalProto(&account)
	if err != nil {
		return fail("failed to marshal miner account")
	}
	ws := []WriteOp{
		{Key: keys.KeyMinerExitRecord(hot), Value: exitData, Category: "miner_exit"},
		{Key: accountKey, Value: updatedAccount, Category: "account"},
	}
	return ws, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: len(ws)}, nil
}

func (h *MinerTxHandler) Apply(tx *pb.AnyTx) error { return ErrNotImplemented }
