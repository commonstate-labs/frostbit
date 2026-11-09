package runtime

import (
	"bytes"
	"fmt"
	"strconv"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

                                                                             
                                                                            
func validateBTCIsolatedSigningIntent(job *pb.FrostWithdrawJobState, template *btc.BTCTemplate, cfg *pb.FrostVaultConfig, vault *pb.FrostVaultState, state map[string][]byte) error {
	if len(job.WithdrawIds) != 1 || len(template.Inputs) != 1 || len(template.WithdrawIDs) != 1 || template.WithdrawIDs[0] != job.WithdrawIds[0] || len(template.Outputs) < 1 || len(template.Outputs) > 2 || len(job.BtcOriginId) != 32 || job.BtcPartSeq == 0 || cfg.DepositDexChainId == 0 {
		return fmt.Errorf("invalid BTC isolated signing child")
	}
	var parent pb.FrostWithdrawState
	if proto.Unmarshal(state[keys.KeyFrostWithdraw(job.WithdrawIds[0])], &parent) != nil || parent.BtcProtocolVersion != 3 || parent.WithdrawId != job.WithdrawIds[0] || parent.Chain != chain.ChainBTC || parent.Asset != job.Asset || parent.Seq != job.FirstSeq || job.LastSeq != job.FirstSeq || parent.BtcNextPartSeq <= job.BtcPartSeq || job.BtcPartSeq > uint64(parent.BtcMaxParts) || string(state[keys.KeyFrostBtcWithdrawPart(parent.WithdrawId, job.BtcPartSeq)]) != job.JobId {
		return fmt.Errorf("BTC isolated signing parent/part scope mismatch")
	}
	gross, e := strconv.ParseUint(job.BtcGrossAmount, 10, 64)
	if e != nil || gross == 0 || strconv.FormatUint(gross, 10) != job.BtcGrossAmount {
		return fmt.Errorf("invalid BTC child gross")
	}
	fee, e := strconv.ParseUint(job.BtcFeeAmount, 10, 64)
	if e != nil || strconv.FormatUint(fee, 10) != job.BtcFeeAmount {
		return fmt.Errorf("invalid BTC child fee")
	}
	in := template.Inputs[0]
	var u pb.FrostUtxo
	if proto.Unmarshal(state[keys.KeyFrostBtcUtxo(job.VaultId, in.TxID, in.Vout)], &u) != nil || !bytes.Equal(u.OriginId, job.BtcOriginId) {
		return fmt.Errorf("BTC child origin mismatch")
	}
	expectedID := chain.BTCWithdrawPartJobID(chain.BTCWithdrawPartIntent{Network: cfg.DepositDexChainId, BTCNetwork: cfg.BtcNetwork, ParentID: parent.WithdrawId, PartSeq: job.BtcPartSeq, VaultID: job.VaultId, KeyEpoch: job.KeyEpoch, Gross: gross, Fee: fee, OriginID: u.OriginId, TemplateHash: job.TemplateHash})
	if expectedID != job.JobId {
		return fmt.Errorf("BTC isolated signing ID does not bind complete intent")
	}
	script, err := btc.AddressScriptForNetwork(parent.To, cfg.BtcNetwork)
	if err != nil {
		return err
	}
	net, expectedFee, change, err := btc.ExpectedUserPaidWithdrawAmounts(in.Amount, []uint64{gross}, 1, [][]byte{script}, u.ScriptPubkey, cfg.BtcFeeSatsPerVbyte)
	if err != nil || fee != expectedFee || template.Outputs[0].Address != parent.To || template.Outputs[0].Amount != net[0] || len(template.Outputs) != (1+boolInt(change > 0)) {
		return fmt.Errorf("BTC isolated signing payment differs from parent allocation")
	}
	if change > 0 {
		out := template.Outputs[1]
		actual, e := btc.AddressScriptForNetwork(out.Address, cfg.BtcNetwork)
		if e != nil || out.Amount != change || !bytes.Equal(actual, u.ScriptPubkey) {
			return fmt.Errorf("BTC isolated change differs from reserved input")
		}
	}
	return nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
