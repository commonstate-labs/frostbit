package withdrawresult

import (
	"bytes"
	"context"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"

	"dex/frost/artifact"
	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"github.com/btcsuite/btcd/wire"
)

func TestBTCParentResultReturnsConstantSizePartialSummary(t *testing.T) {
	rows := records{}
	parent := &pb.FrostWithdrawState{WithdrawId: "parent", TxId: "request", Chain: "btc", Asset: "BTC", Requester: "owner", Amount: "100000", GrossAmount: "100000", FeeAmount: "0", Status: "QUEUED", BtcProtocolVersion: 3, BtcUnallocatedAmount: "30000", BtcClaimedAmount: "20000", BtcSignedAmount: "40000", BtcRefundedAmount: "10000", BtcMaxTotalFee: "10000", BtcCommittedFee: "300", BtcMaxParts: 100, BtcNextPartSeq: 4}
	rows.put(keys.KeyFrostWithdraw("parent"), parent)
	reads := 0
	get := func(k string) ([]byte, bool, error) { reads++; return rows.get(k) }
	result, e := Resolve(context.Background(), get, "parent", "")
	if e != nil || reads != 2 || result.UnallocatedAmount != "30000" || result.SignedAmount != "40000" || result.RefundedAmount != "10000" || result.RawTxHex != "" || result.JobID != "" {
		t.Fatalf("partial summary=%+v reads=%d err=%v", result, reads, e)
	}
	parent.BtcSignedAmount = "40001"
	rows.put(keys.KeyFrostWithdraw("parent"), parent)
	_, e = Resolve(context.Background(), get, "parent", "")
	errorStatus(t, e, http.StatusConflict)
}

func TestBTCPartResultDeliversOnlyItsArtifactFromPermanentState(t *testing.T) {
	rows := records{}
	address := "bcrt1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqdku202"
	script, e := btc.AddressScriptForNetwork(address, "regtest")
	if e != nil {
		t.Fatal(e)
	}
	template := &btc.BTCTemplate{Version: 2, Inputs: []btc.TxInput{{TxID: strings.Repeat("00", 32), Amount: 3080, Sequence: 3}}, Outputs: []btc.TxOutput{{Address: address, Amount: 1000}}, KeyEpoch: 2, WithdrawIDs: []string{"parent"}}
	data, _ := template.ToJSON()
	origin := bytes.Repeat([]byte{7}, 32)
	jobID := chain.BTCWithdrawPartJobID(chain.BTCWithdrawPartIntent{Network: 51025, BTCNetwork: "regtest", ParentID: "parent", PartSeq: 1, VaultID: 0, KeyEpoch: 2, Gross: 3080, Fee: 2080, OriginID: origin, TemplateHash: template.TemplateHash()})
	rows.put(keys.KeyFrostPermanentWithdraw("parent"), &pb.FrostWithdrawState{WithdrawId: "parent", TxId: "request", Chain: "btc", Asset: "BTC", To: address, Amount: "10000", Status: "QUEUED", BtcProtocolVersion: 3})
	rows.put(keys.KeyFrostPermanentWithdrawJob(jobID), &pb.FrostWithdrawJobState{JobId: jobID, Chain: "btc", Asset: "BTC", Status: "SIGNED", KeyEpoch: 2, WithdrawIds: []string{"parent"}, TemplateHash: template.TemplateHash(), TemplateData: data, BtcProtocolVersion: 3, BtcPartSeq: 1, BtcGrossAmount: "3080", BtcFeeAmount: "2080", BtcOriginId: origin})
	rows[keys.KeyFrostBtcWithdrawPart("parent", 1)] = []byte(jobID)
	rows.put(keys.KeyFrostVaultConfig("btc", 0), &pb.FrostVaultConfig{Chain: "btc", VaultCount: 1, BtcNetwork: "regtest", DepositDexChainId: 51025})
	tx := wire.NewMsgTx(2)
	in := wire.NewTxIn(&wire.OutPoint{}, nil, wire.TxWitness{bytes.Repeat([]byte{1}, 64)})
	in.Sequence = 3
	tx.AddTxIn(in)
	tx.AddTxOut(wire.NewTxOut(1000, script))
	var raw bytes.Buffer
	tx.Serialize(&raw)
	record := rows.artifact(t, artifact.Metadata{ArtifactID: tx.TxHash().String(), Kind: artifact.KindWithdraw, Chain: "BTC", VaultID: 0, Epoch: 2, SourceID: jobID}, raw.Bytes())
	rows[keys.KeyFrostPermanentWithdrawArtifact(jobID)] = []byte(record.Metadata.ArtifactID)
	rows = permanentRows(rows)
	result, e := ResolveBTCPart(context.Background(), stateOnlyGet(t, rows), jobID)
	if e != nil || result.RawTxHex != hex.EncodeToString(raw.Bytes()) || result.WithdrawID != "parent" || result.JobID != jobID || result.PartSeq != "1" || result.GrossAmount != "3080" || result.ReceivedAmount != "1000" {
		t.Fatalf("child result=%+v err=%v", result, e)
	}
	rows[keys.KeyFrostPermanentWithdrawJob(jobID)] = []byte("corrupt")
	_, e = ResolveBTCPart(context.Background(), rows.get, jobID)
	errorStatus(t, e, http.StatusConflict)
}
