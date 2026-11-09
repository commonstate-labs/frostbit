                                    
                           

package planning

import (
	"fmt"
	"strconv"

	chainpkg "dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

type btcWithdrawBatch struct {
	grossTotal   uint64
	grossAmounts []uint64
	recipients   []string
	withdrawIDs  []string
	firstSeq     uint64
}

                                                                              
                                                                                 
func (p *JobWindowPlanner) prepareBTCWithdrawBatch(withdraws []*ScanResult) (*btcWithdrawBatch, error) {
	if len(withdraws) > chainpkg.MaxBTCWithdrawalsPerBatch {
		return nil, fmt.Errorf("BTC withdraw batch exceeds %d items", chainpkg.MaxBTCWithdrawalsPerBatch)
	}
	stateKeys := make([]string, 0, len(withdraws))
	for _, wd := range withdraws {
		if wd.state == nil {
			stateKeys = append(stateKeys, keys.KeyFrostWithdraw(wd.WithdrawID))
		}
	}
	if len(stateKeys) > 0 {
		states, err := getMany(p.stateReader, stateKeys)
		if err != nil {
			return nil, err
		}
		for _, wd := range withdraws {
			if wd.state != nil {
				continue
			}
			data := states[keys.KeyFrostWithdraw(wd.WithdrawID)]
			if len(data) == 0 {
				return nil, fmt.Errorf("withdraw not found: %s", wd.WithdrawID)
			}
			state := &pb.FrostWithdrawState{}
			if err := proto.Unmarshal(data, state); err != nil {
				return nil, fmt.Errorf("decode withdraw %s: %w", wd.WithdrawID, err)
			}
			wd.state = state
		}
	}
	batch := &btcWithdrawBatch{
		grossAmounts: make([]uint64, 0, len(withdraws)),
		recipients:   make([]string, 0, len(withdraws)),
		withdrawIDs:  make([]string, 0, len(withdraws)),
	}
	for i, wd := range withdraws {
		amount, err := parseBTCAmount(wd.state.Amount)
		if err != nil {
			return nil, fmt.Errorf("withdraw %s: %w", wd.WithdrawID, err)
		}
		batch.grossTotal, err = addUint64Checked(batch.grossTotal, amount)
		if err != nil {
			return nil, fmt.Errorf("estimate BTC need: %w", err)
		}
		if i == 0 {
			batch.firstSeq = wd.Seq
		}
		batch.grossAmounts = append(batch.grossAmounts, amount)
		batch.recipients = append(batch.recipients, wd.state.To)
		batch.withdrawIDs = append(batch.withdrawIDs, wd.WithdrawID)
	}
	return batch, nil
}

func (p *JobWindowPlanner) estimateBTCNeed(withdraws []*ScanResult) (uint64, error) {
	batch, err := p.prepareBTCWithdrawBatch(withdraws)
	if err != nil {
		return 0, err
	}
	return batch.grossTotal, nil
}

                                                                               
                                                                               
func (p *JobWindowPlanner) planBTCJob(chain, asset string, vaultID uint32, keyEpoch uint64, withdraws []*ScanResult) (*Job, error) {
	batch, err := p.prepareBTCWithdrawBatch(withdraws)
	if err != nil {
		return nil, err
	}
	return p.planBTCJobFromBatch(chain, asset, vaultID, keyEpoch, batch)
}

func (p *JobWindowPlanner) planBTCJobFromBatch(chain, asset string, vaultID uint32, keyEpoch uint64, batch *btcWithdrawBatch) (*Job, error) {
	feeRate, err := p.btcFeeRateForChain(chain)
	if err != nil {
		return nil, err
	}
	grossTotal := batch.grossTotal
	utxos, err := p.selectBTCUTXOsMeetingTarget(chain, vaultID, func(inputCount int) uint64 {
		return grossTotal
	})
	if err != nil {
		return nil, err
	}
	if len(utxos) == 0 {
		return nil, fmt.Errorf("no available UTXOs for vault %d", vaultID)
	}

	totalInput := sumUTXOAmount(utxos)
	if totalInput > grossTotal && totalInput-grossTotal < btc.DustLimit {
		minimumWithChange, err := addUint64Checked(grossTotal, btc.DustLimit)
		if err != nil {
			return nil, fmt.Errorf("BTC gross withdrawal total: %w", err)
		}
		utxos, err = p.selectBTCUTXOsMeetingTarget(chain, vaultID, func(inputCount int) uint64 {
			return minimumWithChange
		})
		if err != nil {
			return nil, err
		}
		totalInput = sumUTXOAmount(utxos)
	}

	grossAmounts, recipients, withdrawIDs := batch.grossAmounts, batch.recipients, batch.withdrawIDs
	firstSeq := batch.firstSeq

	changeSource := btcTemplateFirstInputUTXO(utxos)
	recipientScripts := make([][]byte, len(recipients))
	for i, recipient := range recipients {
		recipientScripts[i], err = btc.AddressScript(recipient)
		if err != nil {
			return nil, fmt.Errorf("invalid BTC recipient for withdraw %s: %w", withdrawIDs[i], err)
		}
	}
	netAmounts, fee, changeAmount, err := btc.ExpectedUserPaidWithdrawAmounts(
		totalInput,
		grossAmounts,
		len(utxos),
		recipientScripts,
		changeSource.ScriptPubKey,
		feeRate,
	)
	if err != nil {
		return nil, err
	}
	outputs := make([]chainpkg.WithdrawOutput, len(withdrawIDs))
	for i := range withdrawIDs {
		outputs[i] = chainpkg.WithdrawOutput{
			WithdrawID: withdrawIDs[i],
			To:         recipients[i],
			Amount:     strconv.FormatUint(netAmounts[i], 10),
		}
	}

	changeAddress := ""
	if changeAmount > 0 {
		changeAddress = p.btcAddressFromScriptPubKey(changeSource.ScriptPubKey, chain)
		if changeAddress == "" {
			return nil, fmt.Errorf("cannot derive BTC change address for vault %d", vaultID)
		}
	}

	adapter, err := p.adapterFactory.Adapter(chain)
	if err != nil {
		return nil, err
	}
	guardian, inputKeys, err := p.btcCustodyKeys(chain, vaultID, utxos)
	if err != nil {
		return nil, err
	}
	params := chainpkg.WithdrawTemplateParams{
		Chain: chain, Asset: asset, VaultID: vaultID, KeyEpoch: keyEpoch,
		WithdrawIDs: withdrawIDs, Outputs: outputs, Inputs: utxos,
		GuardianPubkey: guardian, BTCInputPubkeys: inputKeys,
		Fee: fee, ChangeAmount: changeAmount, ChangeAddress: changeAddress,
	}
	result, err := adapter.BuildWithdrawTemplate(params)
	if err != nil {
		return nil, err
	}

	jobID := generateJobID(chain, asset, vaultID, firstSeq, result.TemplateHash, keyEpoch)
	return &Job{
		JobID: jobID, Chain: chain, Asset: asset, VaultID: vaultID, KeyEpoch: keyEpoch,
		WithdrawIDs: withdrawIDs, TemplateHash: result.TemplateHash, TemplateData: result.TemplateData, FirstSeq: firstSeq,
	}, nil
}

func btcTemplateFirstInputUTXO(utxos []chainpkg.UTXO) chainpkg.UTXO {
	if len(utxos) == 0 {
		return chainpkg.UTXO{}
	}
	first := utxos[0]
	for _, utxo := range utxos[1:] {
		if utxo.TxID < first.TxID || (utxo.TxID == first.TxID && utxo.Vout < first.Vout) {
			first = utxo
		}
	}
	return first
}

func (p *JobWindowPlanner) btcAddressFromScriptPubKey(scriptPubKey []byte, chain string) string {
	xOnly, err := utils.ParseTaprootScriptPubKeyXOnly(scriptPubKey)
	if err != nil {
		return ""
	}
	addr, err := utils.TaprootAddressFromXOnly(xOnly, p.btcNetParamsForChain(chain))
	if err != nil {
		return ""
	}
	return addr
}

                                                                   
func (p *JobWindowPlanner) btcCustodyKeys(chainName string, vaultID uint32, inputs []chainpkg.UTXO) ([]byte, [][]byte, error) {
	cfgKey, vaultKey := keys.KeyFrostVaultConfig(chainName, 0), keys.KeyFrostVaultState(chainName, vaultID)
	stateKeys := make([]string, 0, len(inputs)+2)
	stateKeys = append(stateKeys, cfgKey, vaultKey)
	for _, in := range inputs {
		stateKeys = append(stateKeys, keys.KeyFrostBtcUtxo(vaultID, in.TxID, in.Vout))
	}
	state, err := getMany(p.stateReader, stateKeys)
	if err != nil {
		return nil, nil, err
	}
	var cfg pb.FrostVaultConfig
	var vault pb.FrostVaultState
	if proto.Unmarshal(state[cfgKey], &cfg) != nil || len(cfg.GuardianPubkey) != 32 || proto.Unmarshal(state[vaultKey], &vault) != nil || len(vault.GroupPubkey) == 0 {
		return nil, nil, fmt.Errorf("BTC custody configuration is unavailable")
	}
	inputKeys := make([][]byte, len(inputs))
	for i, in := range inputs {
		var u pb.FrostUtxo
		if len(state[keys.KeyFrostBtcUtxo(vaultID, in.TxID, in.Vout)]) == 0 || proto.Unmarshal(state[keys.KeyFrostBtcUtxo(vaultID, in.TxID, in.Vout)], &u) != nil {
			return nil, nil, fmt.Errorf("BTC selected input state unavailable")
		}
		if len(u.Tweak) == 0 {
			inputKeys[i], err = utils.NormalizeSecp256k1XOnlyPubKey(vault.GroupPubkey)
		} else if len(u.Tweak) == 32 {
			inputKeys[i], err = utils.ComputeTweakedXOnlyPubkey(vault.GroupPubkey, u.Tweak)
		} else {
			return nil, nil, fmt.Errorf("invalid BTC selected input tweak")
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return cfg.GuardianPubkey, inputKeys, nil
}
