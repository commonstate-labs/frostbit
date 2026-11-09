package vm

import (
	"strings"
	"testing"

	"dex/keys"
	"dex/pb"
)

func putMinerInfoAccount(t *testing.T, sv StateView, account *pb.Account) {
	t.Helper()
	raw, err := MarshalProto(account)
	if err != nil {
		t.Fatalf("marshal account: %v", err)
	}
	sv.Set(keys.KeyAccount(account.Address), raw)
}

func TestMinerInfoRejectsClearingActiveMinerEndpoint(t *testing.T) {
	sv := newMemStateView()
	putMinerInfoAccount(t, sv, &pb.Account{Address: "hot", IsMiner: true, Ip: "node.example:6000"})
	tx := &pb.AnyTx{Content: &pb.AnyTx_MinerInfoTx{MinerInfoTx: &pb.MinerInfoTx{
		Base:     &pb.BaseMessage{FromAddress: "hot", TxId: "clear-active"},
		Endpoint: "",
	}}}

	_, receipt, err := (&MinerInfoTxHandler{}).DryRun(tx, sv)
	if err == nil || !strings.Contains(err.Error(), "endpoint cannot be cleared") {
		t.Fatalf("expected active-miner clear rejection, got receipt=%+v err=%v", receipt, err)
	}
}

func TestMinerInfoAllowsExitedMinerToClearEndpoint(t *testing.T) {
	sv := newMemStateView()
	putMinerInfoAccount(t, sv, &pb.Account{Address: "hot", IsMiner: false, Ip: "node.example:6000"})
	tx := &pb.AnyTx{Content: &pb.AnyTx_MinerInfoTx{MinerInfoTx: &pb.MinerInfoTx{
		Base:     &pb.BaseMessage{FromAddress: "hot", TxId: "clear-exited"},
		Endpoint: "",
	}}}

	writes, receipt, err := (&MinerInfoTxHandler{}).DryRun(tx, sv)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" || len(writes) != 1 {
		t.Fatalf("expected exited-miner clear success, writes=%d receipt=%+v err=%v", len(writes), receipt, err)
	}
}
