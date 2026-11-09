package btc

import (
	"fmt"
	"math"
	"strconv"

	"dex/frost/chain"
	"dex/utils"
)

                                                                            
                                                           
func MigrationFee(inputCount int, outputScript []byte, feeRateSatsPerVByte uint64) (uint64, error) {
	if inputCount <= 0 {
		return 0, fmt.Errorf("migration needs at least one input")
	}
	return EstimateWithdrawFee(inputCount, [][]byte{outputScript}, feeRateSatsPerVByte)
}

                                                                        
                                                                        
func BuildMigrationTemplate(chainName string, vaultID uint32, oldEpoch, newEpoch uint64, utxos []chain.UTXO, newGroupPubkey []byte, network string, feeRateSatsPerVByte uint64, guardianPubkey []byte, inputPubkeys [][]byte) (*chain.TemplateResult, *BTCTemplate, error) {
	if len(utxos) == 0 {
		return nil, nil, fmt.Errorf("migration needs at least one input")
	}
	params, err := NetworkParams(network)
	if err != nil {
		return nil, nil, err
	}
	newAddress, err := utils.DualCustodyTaprootAddress(newGroupPubkey, guardianPubkey, params)
	if err != nil {
		return nil, nil, fmt.Errorf("derive migration address: %w", err)
	}
	outputScript, err := AddressScriptForNetwork(newAddress, network)
	if err != nil {
		return nil, nil, fmt.Errorf("derive migration output script: %w", err)
	}
	var total uint64
	for _, utxo := range utxos {
		if utxo.Amount == 0 || math.MaxUint64-total < utxo.Amount {
			return nil, nil, fmt.Errorf("invalid migration input amount")
		}
		total += utxo.Amount
	}
	fee, err := MigrationFee(len(utxos), outputScript, feeRateSatsPerVByte)
	if err != nil {
		return nil, nil, err
	}
	if total <= fee || total-fee < DustLimit {
		return nil, nil, fmt.Errorf("migration sweep amount is below fee/dust: total=%d fee=%d", total, fee)
	}
	migrationID := chain.MigrationID(chainName, vaultID, newEpoch)
	result, err := NewBTCAdapter(network).BuildWithdrawTemplate(chain.WithdrawTemplateParams{
		Chain:          chain.NormalizeChain(chainName),
		Asset:          "BTC",
		VaultID:        vaultID,
		KeyEpoch:       oldEpoch,
		WithdrawIDs:    []string{migrationID},
		Inputs:         utxos,
		GuardianPubkey: guardianPubkey, BTCInputPubkeys: inputPubkeys,
		Outputs: []chain.WithdrawOutput{{
			WithdrawID: migrationID,
			To:         newAddress,
			Amount:     strconv.FormatUint(total-fee, 10),
		}},
		Fee: fee,
	})
	if err != nil {
		return nil, nil, err
	}
	template, err := FromJSON(result.TemplateData)
	if err != nil {
		return nil, nil, err
	}
	return result, template, nil
}
