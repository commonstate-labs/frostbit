package btc

import (
	"fmt"
	"math"

	"github.com/btcsuite/btcd/wire"
)

                                                                      
                                                                             
                                                                              
                                                            
const MinimumWithdrawAmountSats = uint64(10_000)

                                                                          
                                                                            
                                                                              
                                                                  
func ExactTaprootKeySpendVSize(inputCount int, outputScripts [][]byte) (uint64, error) {
	if inputCount <= 0 {
		return 0, fmt.Errorf("BTC transaction needs at least one input")
	}
	if len(outputScripts) == 0 {
		return 0, fmt.Errorf("BTC transaction needs at least one output")
	}

	tx := wire.NewMsgTx(2)
	for i := 0; i < inputCount; i++ {
		tx.AddTxIn(&wire.TxIn{
			Sequence: DefaultSequence,
			Witness:  wire.TxWitness{make([]byte, SchnorrSigSize), make([]byte, SchnorrSigSize), make([]byte, 68), make([]byte, 33)},
		})
	}
	for i, script := range outputScripts {
		if len(script) == 0 {
			return 0, fmt.Errorf("BTC output[%d] script is empty", i)
		}
		tx.AddTxOut(wire.NewTxOut(0, append([]byte(nil), script...)))
	}

	strippedSize := tx.SerializeSizeStripped()
	totalSize := tx.SerializeSize()
	if strippedSize <= 0 || totalSize < strippedSize {
		return 0, fmt.Errorf("invalid BTC transaction size")
	}
	weight := uint64(strippedSize)*4 + uint64(totalSize-strippedSize)
	return (weight + 3) / 4, nil
}

                                                                        
                                                                             
                                                                            
func EstimateWithdrawFee(inputCount int, outputScripts [][]byte, feeRateSatsPerVByte uint64) (uint64, error) {
	if feeRateSatsPerVByte == 0 {
		return 0, fmt.Errorf("BTC fee rate is not configured")
	}
	vsize, err := ExactTaprootKeySpendVSize(inputCount, outputScripts)
	if err != nil {
		return 0, err
	}
	if vsize > math.MaxUint64/feeRateSatsPerVByte {
		return 0, fmt.Errorf("BTC fee overflow")
	}
	return vsize * feeRateSatsPerVByte, nil
}

                                                                                      
func ExpectedWithdrawFeeAndChange(totalInput, withdrawTotal uint64, inputCount int, withdrawScripts [][]byte, changeScript []byte, feeRateSatsPerVByte uint64) (uint64, uint64, error) {
	if inputCount <= 0 || len(withdrawScripts) == 0 || totalInput < withdrawTotal {
		return 0, 0, fmt.Errorf("invalid BTC withdraw totals")
	}
	remaining := totalInput - withdrawTotal
	feeWithoutChange, err := EstimateWithdrawFee(inputCount, withdrawScripts, feeRateSatsPerVByte)
	if err != nil {
		return 0, 0, err
	}
	if remaining < feeWithoutChange {
		return 0, 0, fmt.Errorf("insufficient BTC input for fee")
	}
	if remaining-feeWithoutChange < DustLimit {
		return remaining, 0, nil
	}
	if len(changeScript) == 0 {
		return 0, 0, fmt.Errorf("BTC change script is empty")
	}
	outputScripts := append(append(make([][]byte, 0, len(withdrawScripts)+1), withdrawScripts...), changeScript)
	feeWithChange, err := EstimateWithdrawFee(inputCount, outputScripts, feeRateSatsPerVByte)
	if err != nil {
		return 0, 0, err
	}
	if remaining < feeWithChange || remaining-feeWithChange < DustLimit {
		return remaining, 0, nil
	}
	return feeWithChange, remaining - feeWithChange, nil
}

                                                                            
                                                                            
                                                                          
                                                                         
  
                                                                            
                                                                    
func ExpectedUserPaidWithdrawAmounts(
	totalInput uint64,
	grossAmounts []uint64,
	inputCount int,
	recipientScripts [][]byte,
	changeScript []byte,
	feeRateSatsPerVByte uint64,
) (netAmounts []uint64, fee, change uint64, err error) {
	if inputCount <= 0 {
		return nil, 0, 0, fmt.Errorf("invalid BTC user-paid withdraw totals")
	}
	_, change, err = userPaidWithdrawTotals(totalInput, grossAmounts)
	if err != nil {
		return nil, 0, 0, err
	}
	if len(recipientScripts) != len(grossAmounts) {
		return nil, 0, 0, fmt.Errorf("BTC recipient script count mismatch")
	}

	outputScripts := make([][]byte, 0, len(recipientScripts)+1)
	for i, script := range recipientScripts {
		if len(script) == 0 {
			return nil, 0, 0, fmt.Errorf("BTC recipient script[%d] is empty", i)
		}
		outputScripts = append(outputScripts, script)
	}
	if change > 0 {
		if len(changeScript) == 0 {
			return nil, 0, 0, fmt.Errorf("BTC change script is empty")
		}
		outputScripts = append(outputScripts, changeScript)
	}

	fee, err = EstimateWithdrawFee(inputCount, outputScripts, feeRateSatsPerVByte)
	if err != nil {
		return nil, 0, 0, err
	}
	return allocateUserPaidWithdrawFee(grossAmounts, fee, change)
}

func userPaidWithdrawTotals(totalInput uint64, grossAmounts []uint64) (grossTotal, change uint64, err error) {
	if len(grossAmounts) == 0 {
		return 0, 0, fmt.Errorf("invalid BTC user-paid withdraw totals")
	}

	for _, amount := range grossAmounts {
		if amount == 0 || math.MaxUint64-grossTotal < amount {
			return 0, 0, fmt.Errorf("invalid BTC user-paid withdraw amount")
		}
		grossTotal += amount
	}
	if totalInput < grossTotal {
		return 0, 0, fmt.Errorf("insufficient BTC input for gross withdrawals")
	}

	change = totalInput - grossTotal
	if change > 0 && change < DustLimit {
		return 0, 0, fmt.Errorf("BTC change below dust limit")
	}
	return grossTotal, change, nil
}

func allocateUserPaidWithdrawFee(grossAmounts []uint64, fee, change uint64) (netAmounts []uint64, gotFee, gotChange uint64, err error) {
	count := uint64(len(grossAmounts))
	baseShare, remainder := fee/count, fee%count
	netAmounts = make([]uint64, len(grossAmounts))
	for i, gross := range grossAmounts {
		share := baseShare
		if uint64(i) < remainder {
			share++
		}
		if gross <= share || gross-share < DustLimit {
			return nil, 0, 0, fmt.Errorf("BTC withdrawal output below dust limit after fee")
		}
		netAmounts[i] = gross - share
	}
	return netAmounts, fee, change, nil
}
