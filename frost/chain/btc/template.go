package btc

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"

	"dex/frost/chain"
	"dex/utils"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

               
const (
	                                                  
	DefaultSequence = 0xffffffff
	                                     
	DefaultLockTime = 0
	                                      
	DustLimit = 546
)

                                            
type TxInput struct {
	TxID         string `json:"txid"`                                         
	Vout         uint32 `json:"vout"`                    
	Amount       uint64 `json:"amount"`                              
	Sequence     uint32 `json:"sequence"`             
	SpendScript  []byte `json:"spend_script"`
	ControlBlock []byte `json:"control_block"`
}

                                             
type TxOutput struct {
	Address string `json:"address"`                
	Amount  uint64 `json:"amount"`                        
}

                                                    
                                                   
type BTCTemplate struct {
	Version     int32      `json:"version"`                     
	LockTime    uint32     `json:"lock_time"`                   
	Inputs      []TxInput  `json:"inputs"`                                     
	Outputs     []TxOutput `json:"outputs"`                                    
	VaultID     uint32     `json:"vault_id"`                
	KeyEpoch    uint64     `json:"key_epoch"`                   
	WithdrawIDs []string   `json:"withdraw_ids"`                    
}

                                            
                                                                                                                  
func NewBTCTemplate(params chain.WithdrawTemplateParams) (*BTCTemplate, error) {
	if len(params.Inputs) == 0 {
		return nil, errors.New("no inputs provided")
	}
	if len(params.Outputs) == 0 {
		return nil, errors.New("no outputs provided")
	}
	if len(params.GuardianPubkey) != 32 || len(params.BTCInputPubkeys) != len(params.Inputs) {
		return nil, errors.New("BTC dual custody keys are required for every input")
	}
	                                                                          
	inputs := make([]TxInput, len(params.Inputs))
	for i, utxo := range params.Inputs {
		script, leaf, control, err := utils.BuildDualCustodyTaproot(params.BTCInputPubkeys[i], params.GuardianPubkey)
		if err != nil {
			return nil, fmt.Errorf("input[%d] policy: %w", i, err)
		}
		if !bytes.Equal(script, utxo.ScriptPubKey) {
			return nil, fmt.Errorf("input[%d] does not match dual custody policy", i)
		}
		inputs[i] = TxInput{
			TxID:        utxo.TxID,
			Vout:        utxo.Vout,
			Amount:      utxo.Amount,
			Sequence:    DefaultSequence,
			SpendScript: leaf, ControlBlock: control,
		}
	}
	sortInputs(inputs)

	                                                                        
	                                                                            
	outputs := make([]TxOutput, 0, len(params.Outputs)+1)
	for _, out := range params.Outputs {
		amount, err := chain.ParseWithdrawAmount(out.Amount)
		if err != nil || !amount.IsUint64() || amount.Uint64() > btcutil.MaxSatoshi {
			return nil, fmt.Errorf("invalid bitcoin withdraw amount for %s", out.WithdrawID)
		}
		outputs = append(outputs, TxOutput{
			Address: out.To,
			Amount:  amount.Uint64(),
		})
	}
	if len(params.WithdrawIDs) != len(params.Outputs) {
		return nil, errors.New("withdraw_ids and outputs length mismatch")
	}
	for i, out := range params.Outputs {
		if out.WithdrawID != params.WithdrawIDs[i] {
			return nil, errors.New("withdraw output order mismatch")
		}
	}

	                                                                              
	var totalIn, totalOut uint64
	for _, in := range params.Inputs {
		if math.MaxUint64-totalIn < in.Amount {
			return nil, errors.New("input amount overflow")
		}
		totalIn += in.Amount
	}
	for _, out := range outputs[:len(params.Outputs)] {
		if math.MaxUint64-totalOut < out.Amount {
			return nil, errors.New("output amount overflow")
		}
		totalOut += out.Amount
	}

	if math.MaxUint64-totalOut < params.Fee || math.MaxUint64-(totalOut+params.Fee) < params.ChangeAmount {
		return nil, errors.New("fee/change amount overflow")
	}
	expectedTotal := totalOut + params.Fee + params.ChangeAmount
	if totalIn != expectedTotal {
		return nil, errors.New("input/output mismatch: totalIn != totalOut + fee + change")
	}

	                                    
	if params.ChangeAmount > 0 && params.ChangeAddress != "" {
		outputs = append(outputs, TxOutput{
			Address: params.ChangeAddress,
			Amount:  params.ChangeAmount,
		})
	}

	                                                                         
	withdrawIDs := make([]string, len(params.WithdrawIDs))
	copy(withdrawIDs, params.WithdrawIDs)

	return &BTCTemplate{
		Version:     2,
		LockTime:    DefaultLockTime,
		Inputs:      inputs,
		Outputs:     outputs,
		VaultID:     params.VaultID,
		KeyEpoch:    params.KeyEpoch,
		WithdrawIDs: withdrawIDs,
	}, nil
}

                                                          
func sortInputs(inputs []TxInput) {
	sort.Slice(inputs, func(i, j int) bool {
		if inputs[i].TxID != inputs[j].TxID {
			return inputs[i].TxID < inputs[j].TxID
		}
		return inputs[i].Vout < inputs[j].Vout
	})
}

                                                                
                                                                                 
func (t *BTCTemplate) Serialize() []byte {
	var buf bytes.Buffer

	                                           
	binary.Write(&buf, binary.LittleEndian, t.Version)

	                                              
	binary.Write(&buf, binary.LittleEndian, t.LockTime)

	                                         
	binary.Write(&buf, binary.LittleEndian, t.VaultID)

	                                          
	binary.Write(&buf, binary.LittleEndian, t.KeyEpoch)

	                              
	writeVarInt(&buf, uint64(len(t.Inputs)))

	                                 
	for _, in := range t.Inputs {
		                                                
		txidBytes, _ := hex.DecodeString(in.TxID)
		reverseBytes(txidBytes)
		buf.Write(txidBytes)
		                    
		binary.Write(&buf, binary.LittleEndian, in.Vout)
		                      
		binary.Write(&buf, binary.LittleEndian, in.Amount)
		                        
		binary.Write(&buf, binary.LittleEndian, in.Sequence)
		writeVarInt(&buf, uint64(len(in.SpendScript)))
		buf.Write(in.SpendScript)
		writeVarInt(&buf, uint64(len(in.ControlBlock)))
		buf.Write(in.ControlBlock)
	}

	                              
	writeVarInt(&buf, uint64(len(t.Outputs)))

	                  
	for _, out := range t.Outputs {
		                      
		binary.Write(&buf, binary.LittleEndian, out.Amount)
		                                            
		addrBytes := []byte(out.Address)
		writeVarInt(&buf, uint64(len(addrBytes)))
		buf.Write(addrBytes)
	}

	                                    
	writeVarInt(&buf, uint64(len(t.WithdrawIDs)))

	                         
	for _, wid := range t.WithdrawIDs {
		widBytes := []byte(wid)
		writeVarInt(&buf, uint64(len(widBytes)))
		buf.Write(widBytes)
	}

	return buf.Bytes()
}

                                                                    
func (t *BTCTemplate) TemplateHash() []byte {
	serialized := t.Serialize()
	hash := sha256.Sum256(serialized)
	return hash[:]
}

                                                    
func (t *BTCTemplate) ToJSON() ([]byte, error) {
	return json.Marshal(t)
}

                                 
func FromJSON(data []byte) (*BTCTemplate, error) {
	var t BTCTemplate
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

                                                                                                  
                                                                                         
                                               
func (t *BTCTemplate) BuildSignedRawTx(inputSigs, guardianSigs [][]byte) ([]byte, error) {
	if len(guardianSigs) != len(t.Inputs) {
		return nil, errors.New("guardian signature count mismatch")
	}
	if len(inputSigs) != len(t.Inputs) {
		return nil, fmt.Errorf("input_sigs count %d != inputs count %d", len(inputSigs), len(t.Inputs))
	}

	tx, err := t.buildUnsignedMsgTx()
	if err != nil {
		return nil, err
	}

	                                                                   
	for i, sig := range inputSigs {
		if len(sig) != 64 {
			return nil, fmt.Errorf("input[%d] signature length %d != 64", i, len(sig))
		}
		if len(guardianSigs[i]) != 64 || len(t.Inputs[i].SpendScript) != 68 || len(t.Inputs[i].ControlBlock) != 33 {
			return nil, fmt.Errorf("input[%d] invalid dual custody witness", i)
		}
		tx.TxIn[i].Witness = wire.TxWitness{guardianSigs[i], sig, t.Inputs[i].SpendScript, t.Inputs[i].ControlBlock}
	}

	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		return nil, fmt.Errorf("serialize failed: %w", err)
	}
	return buf.Bytes(), nil
}

                                                                              
                                                                             
                                                                         
                                                              
func (t *BTCTemplate) buildUnsignedMsgTx() (*wire.MsgTx, error) {
	if len(t.Inputs) == 0 {
		return nil, errors.New("cannot build bitcoin transaction without inputs")
	}
	if len(t.Outputs) == 0 {
		return nil, errors.New("cannot build bitcoin transaction without outputs")
	}

	tx := wire.NewMsgTx(t.Version)
	tx.LockTime = t.LockTime
	seenOutPoints := make(map[wire.OutPoint]struct{}, len(t.Inputs))

	for i, in := range t.Inputs {
		txHash, err := chainhash.NewHashFromStr(in.TxID)
		if err != nil {
			return nil, fmt.Errorf("invalid input[%d] txid %s: %w", i, in.TxID, err)
		}
		if in.Amount > uint64(btcutil.MaxSatoshi) {
			return nil, fmt.Errorf("input[%d] amount exceeds bitcoin maximum", i)
		}

		outPoint := *wire.NewOutPoint(txHash, in.Vout)
		if _, exists := seenOutPoints[outPoint]; exists {
			return nil, fmt.Errorf("input[%d] duplicates outpoint %s:%d", i, in.TxID, in.Vout)
		}
		seenOutPoints[outPoint] = struct{}{}
		tx.AddTxIn(&wire.TxIn{
			PreviousOutPoint: outPoint,
			Sequence:         in.Sequence,
		})
	}

	for i, out := range t.Outputs {
		if out.Amount > uint64(btcutil.MaxSatoshi) {
			return nil, fmt.Errorf("output[%d] amount exceeds bitcoin maximum", i)
		}
		pkScript, err := AddressScript(out.Address)
		if err != nil {
			return nil, fmt.Errorf("failed to convert output[%d] address %s: %w", i, out.Address, err)
		}
		tx.AddTxOut(wire.NewTxOut(int64(out.Amount), pkScript))
	}

	return tx, nil
}

                                                            
func writeVarInt(buf *bytes.Buffer, n uint64) {
	switch {
	case n < 0xfd:
		buf.WriteByte(byte(n))
	case n <= 0xffff:
		buf.WriteByte(0xfd)
		binary.Write(buf, binary.LittleEndian, uint16(n))
	case n <= 0xffffffff:
		buf.WriteByte(0xfe)
		binary.Write(buf, binary.LittleEndian, uint32(n))
	default:
		buf.WriteByte(0xff)
		binary.Write(buf, binary.LittleEndian, n)
	}
}

                                                                   
func reverseBytes(b []byte) {
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
}

                                                       

                             
type SighashType byte

const (
	                                                         
	SighashDefault SighashType = 0x00
)

                                                            
                                                
                                                                       
func (t *BTCTemplate) ComputeTaprootSighash(scriptPubKeys [][]byte, sighashType SighashType) ([][]byte, error) {
	if len(scriptPubKeys) != len(t.Inputs) {
		return nil, errors.New("scriptPubKeys count mismatch with inputs")
	}

	if sighashType != SighashDefault {
		return nil, fmt.Errorf("unsupported taproot sighash type 0x%02x", byte(sighashType))
	}

	tx, err := t.buildUnsignedMsgTx()
	if err != nil {
		return nil, err
	}

	prevOutFetcher := txscript.NewMultiPrevOutFetcher(nil)
	for i, txIn := range tx.TxIn {
		if len(scriptPubKeys[i]) == 0 {
			return nil, fmt.Errorf("input[%d] scriptPubKey is empty", i)
		}
		if !txscript.IsPayToTaproot(scriptPubKeys[i]) {
			return nil, fmt.Errorf("input[%d] scriptPubKey is not pay-to-taproot", i)
		}
		prevOutFetcher.AddPrevOut(
			txIn.PreviousOutPoint,
			wire.NewTxOut(int64(t.Inputs[i].Amount), append([]byte(nil), scriptPubKeys[i]...)),
		)
	}

	sigHashes := txscript.NewTxSigHashes(tx, prevOutFetcher)

	sighashes := make([][]byte, len(t.Inputs))
	for i := range t.Inputs {
		if err := t.ValidateInputPolicy(i, nil, nil, scriptPubKeys[i]); err != nil {
			return nil, err
		}
		sighash, err := txscript.CalcTapscriptSignaturehash(
			sigHashes,
			txscript.SigHashDefault,
			tx,
			i,
			prevOutFetcher,
			txscript.NewBaseTapLeaf(t.Inputs[i].SpendScript),
		)
		if err != nil {
			return nil, fmt.Errorf("compute taproot sighash for input[%d]: %w", i, err)
		}
		sighashes[i] = append([]byte(nil), sighash...)
	}

	return sighashes, nil
}

                                                                           
                                                                                 
                                                                           
func (t *BTCTemplate) ValidateInputPolicy(i int, operator, guardian, previousScript []byte) error {
	if i < 0 || i >= len(t.Inputs) {
		return fmt.Errorf("invalid input policy index")
	}
	in := t.Inputs[i]
	if len(in.SpendScript) != 68 || len(in.ControlBlock) != 33 || in.SpendScript[0] != 32 || in.SpendScript[33] != txscript.OP_CHECKSIGVERIFY || in.SpendScript[34] != 32 || in.SpendScript[67] != txscript.OP_CHECKSIG {
		return fmt.Errorf("input[%d] invalid dual custody script", i)
	}
	if operator == nil && guardian == nil {
		operator, guardian = in.SpendScript[1:33], in.SpendScript[35:67]
	}
	script, leaf, control, err := utils.BuildDualCustodyTaproot(operator, guardian)
	if err != nil {
		return err
	}
	if !bytes.Equal(script, previousScript) || !bytes.Equal(leaf, in.SpendScript) || !bytes.Equal(control, in.ControlBlock) {
		return fmt.Errorf("input[%d] dual custody commitment mismatch", i)
	}
	return nil
}

                                                                              
func (t *BTCTemplate) TxID() (string, error) {
	tx, err := t.buildUnsignedMsgTx()
	if err != nil {
		return "", err
	}
	return tx.TxHash().String(), nil
}
