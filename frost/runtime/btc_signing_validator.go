package runtime

import (
	"bytes"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/frost/runtime/planning"
	"dex/frost/runtime/roast"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

func isBTCSigningChain(chainName string) bool {
	return chain.NormalizeChain(chainName) == chain.ChainBTC
}

func isEVMSigningChain(chainName string) bool {
	switch chain.NormalizeChain(chainName) {
	case chain.ChainETH, chain.ChainBNB, chain.ChainBASE, chain.ChainARB, chain.ChainTRX:
		return true
	default:
		return false
	}
}

func sameSigningMatrix(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

type signingBulkReader interface {
	GetMany(keys []string) (map[string][]byte, error)
}

func readSigningState(reader ChainStateReader, stateKeys []string) (map[string][]byte, error) {
	if bulk, ok := reader.(signingBulkReader); ok {
		return bulk.GetMany(stateKeys)
	}
	values := make(map[string][]byte, len(stateKeys))
	for _, key := range stateKeys {
		value, exists, err := reader.Get(key)
		if err != nil {
			return nil, err
		}
		if exists {
			values[key] = value
		}
	}
	return values, nil
}

func newSigningRequestValidator(reader ChainStateReader) roast.SigningRequestValidator {
	return func(env *roast.Envelope, messages [][]byte) error {
		if env == nil {
			return fmt.Errorf("nil signing request")
		}
		chainName := chain.NormalizeChain(env.Chain)
		if !isBTCSigningChain(chainName) && !isEVMSigningChain(chainName) {
			return fmt.Errorf("signing intent validation unsupported for chain: %s", chainName)
		}
		if reader == nil {
			return fmt.Errorf("signing state reader unavailable")
		}
		jobData, exists, err := reader.Get(keys.KeyFrostWithdrawJob(env.SessionID))
		if err != nil {
			return fmt.Errorf("%w: read signing intent: %v", roast.ErrSigningIntentNotReady, err)
		}
		if !exists || len(jobData) == 0 {
			if strings.HasPrefix(env.SessionID, "migration_") {
				return validateFinalizedMigrationSigningRequest(reader, env, messages)
			}
			return fmt.Errorf("%w: signing job %s", roast.ErrSigningIntentNotReady, env.SessionID)
		}
		var job pb.FrostWithdrawJobState
		if err := proto.Unmarshal(jobData, &job); err != nil {
			return fmt.Errorf("invalid signing intent: %w", err)
		}
		if isEVMSigningChain(chainName) {
			return validateEVMWithdrawSigningRequest(reader, env, messages, &job)
		}
		return validateBTCWithdrawSigningRequest(reader, env, messages, &job)
	}
}

func validateBTCWithdrawSigningRequest(reader ChainStateReader, env *roast.Envelope, messages [][]byte, job *pb.FrostWithdrawJobState) error {
	if job == nil {
		return fmt.Errorf("invalid BTC signing intent")
	}
	if job.Status != "CLAIMED" || job.JobId != env.SessionID || chain.NormalizeChain(job.Chain) != chain.ChainBTC ||
		job.VaultId != env.VaultID || job.KeyEpoch != env.Epoch || env.SignAlgo != pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340 {
		return fmt.Errorf("BTC signing intent context mismatch")
	}
	template, err := btc.FromJSON(job.TemplateData)
	if err != nil || len(template.Inputs) == 0 || template.VaultID != env.VaultID || template.KeyEpoch != env.Epoch || !bytes.Equal(template.TemplateHash(), job.TemplateHash) {
		return fmt.Errorf("invalid BTC signing template")
	}

	scriptPubKeys := make([][]byte, len(template.Inputs))
	tweaks := make([][]byte, len(template.Inputs))
	stateKeys := make([]string, 0, len(template.Inputs)*2+2)
	cfgKey, vaultKey := keys.KeyFrostVaultConfig(chain.ChainBTC, 0), keys.KeyFrostVaultState(chain.ChainBTC, env.VaultID)
	stateKeys = append(stateKeys, cfgKey, vaultKey)
	for _, input := range template.Inputs {
		stateKeys = append(stateKeys, keys.KeyFrostBtcUtxo(env.VaultID, input.TxID, input.Vout), keys.KeyFrostBtcLockedUtxo(env.VaultID, input.TxID, input.Vout))
	}
	if job.BtcProtocolVersion == 3 {
		if len(job.WithdrawIds) != 1 || len(template.Inputs) != 1 {
			return fmt.Errorf("BTC isolated child must have one parent and input")
		}
		stateKeys = append(stateKeys, keys.KeyFrostWithdraw(job.WithdrawIds[0]), keys.KeyFrostBtcWithdrawPart(job.WithdrawIds[0], job.BtcPartSeq))
	}
	state, err := readSigningState(reader, stateKeys)
	if err != nil {
		return fmt.Errorf("%w: read BTC signing inputs: %v", roast.ErrSigningIntentNotReady, err)
	}
	var cfg pb.FrostVaultConfig
	var vault pb.FrostVaultState
	if len(state[cfgKey]) == 0 || proto.Unmarshal(state[cfgKey], &cfg) != nil || len(cfg.GuardianPubkey) != 32 || len(state[vaultKey]) == 0 || proto.Unmarshal(state[vaultKey], &vault) != nil || vault.KeyEpoch != env.Epoch {
		return fmt.Errorf("BTC custody configuration mismatch")
	}
	if job.BtcProtocolVersion == 3 {
		if err := validateBTCIsolatedSigningIntent(job, template, &cfg, &vault, state); err != nil {
			return err
		}
	}
	for i, input := range template.Inputs {
		utxoData := state[keys.KeyFrostBtcUtxo(env.VaultID, input.TxID, input.Vout)]
		if len(utxoData) == 0 {
			return fmt.Errorf("BTC signing input not found: %s:%d", input.TxID, input.Vout)
		}
		var utxo pb.FrostUtxo
		if err := proto.Unmarshal(utxoData, &utxo); err != nil {
			return fmt.Errorf("invalid BTC signing input")
		}
		amount, err := strconv.ParseUint(strings.TrimSpace(utxo.Amount), 10, 64)
		if err != nil || amount != input.Amount || utxo.VaultId != env.VaultID || utxo.Txid != input.TxID || utxo.Vout != input.Vout || len(utxo.ScriptPubkey) == 0 || len(utxo.Tweak) != 0 && len(utxo.Tweak) != 32 {
			return fmt.Errorf("BTC signing input state mismatch: %s:%d", input.TxID, input.Vout)
		}
		lock := state[keys.KeyFrostBtcLockedUtxo(env.VaultID, input.TxID, input.Vout)]
		if string(lock) != env.SessionID {
			return fmt.Errorf("BTC signing input is not reserved by job")
		}
		operator, err := utils.NormalizeSecp256k1XOnlyPubKey(vault.GroupPubkey)
		if len(utxo.Tweak) == 32 {
			operator, err = utils.ComputeTweakedXOnlyPubkey(vault.GroupPubkey, utxo.Tweak)
		}
		if err != nil {
			return err
		}
		if err := template.ValidateInputPolicy(i, operator, cfg.GuardianPubkey, utxo.ScriptPubkey); err != nil {
			return err
		}
		scriptPubKeys[i] = append([]byte(nil), utxo.ScriptPubkey...)
		tweaks[i] = append([]byte(nil), utxo.Tweak...)
	}
	expectedMessages, err := template.ComputeTaprootSighash(scriptPubKeys, btc.SighashDefault)
	if err != nil || !sameSigningMatrix(expectedMessages, messages) || !sameSigningMatrix(tweaks, env.Tweaks) {
		return fmt.Errorf("BTC signing messages/tweaks differ from finalized intent")
	}
	return nil
}

func validateEVMWithdrawSigningRequest(reader ChainStateReader, env *roast.Envelope, messages [][]byte, job *pb.FrostWithdrawJobState) error {
	chainName := chain.NormalizeChain(env.Chain)
	if job == nil || job.Status != "CLAIMED" || job.JobId != env.SessionID || chain.NormalizeChain(job.Chain) != chainName ||
		job.Asset != "" || job.VaultId != env.VaultID || job.KeyEpoch != env.Epoch || env.SignAlgo != pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128 || len(env.Tweaks) != 0 {
		return fmt.Errorf("EVM signing intent context mismatch")
	}

	vaultData, exists, err := reader.Get(keys.KeyFrostVaultState(chainName, env.VaultID))
	if err != nil {
		return fmt.Errorf("%w: read EVM signing vault state: %v", roast.ErrSigningIntentNotReady, err)
	}
	if !exists || len(vaultData) == 0 {
		return fmt.Errorf("%w: EVM vault state", roast.ErrSigningIntentNotReady)
	}
	var vault pb.FrostVaultState
	if err := proto.Unmarshal(vaultData, &vault); err != nil {
		return fmt.Errorf("invalid EVM signing vault state: %w", err)
	}
	if chain.NormalizeChain(vault.Chain) != chainName || vault.VaultId != env.VaultID || vault.KeyEpoch != env.Epoch ||
		vault.Status != "ACTIVE" || vault.SignAlgo != pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128 || len(vault.GroupPubkey) != 64 || len(vault.CommitteeMembers) == 0 {
		return fmt.Errorf("EVM signing vault context mismatch")
	}

	active, activeExists, err := reader.Get(keys.KeyFrostWithdrawActiveVaultJob(chainName, env.VaultID))
	if err != nil || !activeExists || string(active) != job.JobId {
		return fmt.Errorf("EVM signing physical Vault active job mismatch")
	}
	params, firstSeq, err := planning.BuildAccountTemplateParams(reader, chainName, "", env.VaultID, env.Epoch, job.WithdrawIds, nil)
	if err != nil {
		return fmt.Errorf("rebuild EVM signing intent: %w", err)
	}
	if err := validateEVMWithdrawReservation(reader, chainName, job, params); err != nil {
		return err
	}
	chainID, err := readEVMChainID(reader, chainName)
	if err != nil {
		return err
	}
	canonical, err := evm.NewEVMAdapter(chainName, chainID).BuildWithdrawTemplate(params)
	if err != nil {
		return fmt.Errorf("build EVM signing intent: %w", err)
	}
	expectedJobID := chain.ComputeWithdrawJobID(chainName, "", env.VaultID, firstSeq, canonical.TemplateHash, env.Epoch)
	if job.FirstSeq != firstSeq || job.JobId != expectedJobID || !bytes.Equal(job.TemplateHash, canonical.TemplateHash) ||
		job.BatchSeq != params.BatchSeq || !bytes.Equal(job.TemplateData, canonical.TemplateData) || !sameSigningMatrix(canonical.SigHashes, messages) {
		return fmt.Errorf("EVM signing messages differ from finalized canonical intent")
	}
	return nil
}

func validateEVMWithdrawReservation(reader ChainStateReader, chainName string, job *pb.FrostWithdrawJobState, params chain.WithdrawTemplateParams) error {
	if job == nil || len(job.WithdrawIds) == 0 || len(params.Outputs) != len(job.WithdrawIds) {
		return fmt.Errorf("EVM signing reservation is missing")
	}
	stateKeys := make([]string, len(job.WithdrawIds))
	for i, id := range job.WithdrawIds {
		stateKeys[i] = keys.KeyFrostWithdraw(id)
	}
	states, err := signingGetMany(reader, stateKeys)
	if err != nil {
		return err
	}
	totals := make(map[string]*big.Int)
	for i, id := range job.WithdrawIds {
		var state pb.FrostWithdrawState
		if data := states[stateKeys[i]]; len(data) == 0 || proto.Unmarshal(data, &state) != nil || state.WithdrawId != id || state.JobId != job.JobId || state.Status != "SIGNING" || state.VaultId != job.VaultId {
			return fmt.Errorf("EVM signing withdrawal reservation scope mismatch")
		}
		amount, err := chain.ParseWithdrawAmount(state.GrossAmount)
		if err != nil || params.Outputs[i].Amount != amount.String() {
			return fmt.Errorf("EVM signing output amount is invalid")
		}
		if totals[state.Asset] == nil {
			totals[state.Asset] = new(big.Int)
		}
		totals[state.Asset].Add(totals[state.Asset], amount)
		if totals[state.Asset].BitLen() > 256 {
			return fmt.Errorf("EVM signing output amount overflows uint256")
		}
	}
	assets := make([]string, 0, len(totals))
	for asset := range totals {
		assets = append(assets, asset)
	}
	sort.Strings(assets)
	reservationKeys := make([]string, len(assets))
	for i, asset := range assets {
		reservationKeys[i] = keys.KeyFrostVaultReservedBalance(chainName, asset, job.VaultId)
	}
	reservations, err := signingGetMany(reader, reservationKeys)
	if err != nil {
		return fmt.Errorf("read EVM signing vault reservation: %w", err)
	}
	for i, asset := range assets {
		aggregate, ok := new(big.Int).SetString(strings.TrimSpace(string(reservations[reservationKeys[i]])), 10)
		if !ok || aggregate.Sign() <= 0 || aggregate.BitLen() > 256 || aggregate.Cmp(totals[asset]) < 0 {
			return fmt.Errorf("EVM signing vault reservation is insufficient for %s", asset)
		}
	}
	return nil
}

func signingGetMany(reader ChainStateReader, stateKeys []string) (map[string][]byte, error) {
	if batch, ok := reader.(interface {
		GetMany([]string) (map[string][]byte, error)
	}); ok {
		return batch.GetMany(stateKeys)
	}
	out := make(map[string][]byte, len(stateKeys))
	for _, key := range stateKeys {
		data, found, err := reader.Get(key)
		if err != nil {
			return nil, err
		}
		if found {
			out[key] = data
		}
	}
	return out, nil
}

func readEVMChainID(reader ChainStateReader, chainName string) (uint64, error) {
	data, exists, err := reader.Get(keys.KeyFrostVaultConfig(chainName, 0))
	if err != nil {
		return 0, fmt.Errorf("read EVM signing vault config: %w", err)
	}
	if !exists || len(data) == 0 {
		return 0, fmt.Errorf("EVM signing vault config not found")
	}
	var cfg pb.FrostVaultConfig
	if err := proto.Unmarshal(data, &cfg); err != nil {
		return 0, fmt.Errorf("invalid EVM signing vault config: %w", err)
	}
	if cfg.EvmChainId != 0 {
		return cfg.EvmChainId, nil
	}
	chainID := evm.DefaultChainID(chainName)
	if chainID == 0 {
		return 0, fmt.Errorf("EVM signing chain_id unavailable")
	}
	return chainID, nil
}
