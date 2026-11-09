package indexdb

import (
	"encoding/json"
	"strings"
	"testing"

	"dex/pb"
)

                                                                                                
                                              
                                                              
                                     
                                     
                                    

                                                      

func TestKeyAddressTx_RoundTrip(t *testing.T) {
	addr := "alice"
	cases := []struct {
		height uint64
		idx    int
	}{
		{0, 0}, {1, 0}, {1_000_000, 5}, {^uint64(0), 9999},
	}
	for _, c := range cases {
		k := KeyAddressTx(addr, c.height, c.idx)
		gotH, gotIdx, ok := ParseAddressTxKey(k, addr)
		if !ok {
			t.Fatalf("parse failed for h=%d idx=%d key=%s", c.height, c.idx, k)
		}
		if gotH != c.height || gotIdx != c.idx {
			t.Fatalf("roundtrip mismatch: got h=%d idx=%d, want h=%d idx=%d", gotH, gotIdx, c.height, c.idx)
		}
	}
}

                                                                                                 
func TestKeyAddressTx_NewerHeightSortsFirst(t *testing.T) {
	a := KeyAddressTx("alice", 100, 0)
	b := KeyAddressTx("alice", 200, 0)
	if !(b < a) {
		t.Fatalf("higher height key must sort BEFORE lower (inverted): a=%s b=%s", a, b)
	}
}

func TestParseAddressTxKey_RejectsBadInputs(t *testing.T) {
	cases := []string{
		"",
		"random",
		"addr_tx_alice_notnum_0",
		"addr_tx_alice_00000000000000000001_notnum",
		"addr_tx_alice_00000000000000000001",                
		"addr_tx_other_00000000000000000001_0",                         
	}
	for _, k := range cases {
		if _, _, ok := ParseAddressTxKey(k, "alice"); ok {
			t.Errorf("must reject malformed key: %q", k)
		}
	}
}

func TestKeyBlockTx_PrefixIsKeyPrefix(t *testing.T) {
	pfx := KeyBlockTxPrefix(42)
	full := KeyBlockTx(42, 7)
	if !strings.HasPrefix(full, pfx) {
		t.Fatalf("KeyBlockTx must start with prefix; got=%s prefix=%s", full, pfx)
	}
}

func TestKeyTxTypeTx_RoundTripAndSortsRecentFirst(t *testing.T) {
	oldKey := KeyTxTypeTx("WitnessVote", 100, 1)
	newKey := KeyTxTypeTx("WitnessVote", 200, 0)
	if !(newKey < oldKey) {
		t.Fatalf("higher height type index must sort first: old=%s new=%s", oldKey, newKey)
	}
	lowIndex := KeyTxTypeTx("WitnessVote", 200, 1)
	highIndex := KeyTxTypeTx("WitnessVote", 200, 9)
	if !(highIndex < lowIndex) {
		t.Fatalf("higher tx index in same block must sort first: low=%s high=%s", lowIndex, highIndex)
	}
	h, idx, ok := ParseTxTypeTxKey(highIndex, "WitnessVote")
	if !ok || h != 200 || idx != 9 {
		t.Fatalf("type index roundtrip got h=%d idx=%d ok=%v", h, idx, ok)
	}
}

func TestKeyAddressTypeTx_RoundTripAndSortsRecentFirst(t *testing.T) {
	oldKey := KeyAddressTypeTx("receiver", "Transaction", 100, 1)
	newKey := KeyAddressTypeTx("receiver", "Transaction", 200, 0)
	if !(newKey < oldKey) {
		t.Fatalf("higher height address type index must sort first: old=%s new=%s", oldKey, newKey)
	}
	lowIndex := KeyAddressTypeTx("receiver", "Transaction", 200, 1)
	highIndex := KeyAddressTypeTx("receiver", "Transaction", 200, 9)
	if !(highIndex < lowIndex) {
		t.Fatalf("higher tx index in same block must sort first: low=%s high=%s", lowIndex, highIndex)
	}
	h, idx, ok := ParseAddressTypeTxKey(highIndex, "receiver", "Transaction")
	if !ok || h != 200 || idx != 9 {
		t.Fatalf("address type index roundtrip got h=%d idx=%d ok=%v", h, idx, ok)
	}
}

func TestKeyBalanceSnapshot_NewerHeightSortsFirst(t *testing.T) {
	a := KeyBalanceSnapshot("alice", 100)
	b := KeyBalanceSnapshot("alice", 200)
	if !(b < a) {
		t.Fatalf("balance snapshot at higher height must sort first; a=%s b=%s", a, b)
	}
	pfx := KeyBalanceSnapshotPrefix("alice")
	if !strings.HasPrefix(a, pfx) {
		t.Fatalf("snapshot key must start with prefix")
	}
}

                                                         

func TestExtractTxType_AllOneofBranches(t *testing.T) {
	cases := []struct {
		name string
		tx   *pb.AnyTx
		want string
	}{
		{"nil", nil, ""},
		{"empty", &pb.AnyTx{}, "Unknown"},
		{"Transaction", &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{}}}, "Transaction"},
		{"OrderTx", &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{}}}, "Order"},
		{"WitnessRequestTx", &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{}}}, "WitnessRequest"},
		{"FrostWithdrawRequestTx", &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawRequestTx{FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{}}}, "FrostWithdrawRequest"},
		{"FrostWithdrawCancelTx", &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawCancelTx{FrostWithdrawCancelTx: &pb.FrostWithdrawCancelTx{}}}, "FrostWithdrawCancel"},
		{"FrostWithdrawClaimTx", &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: &pb.FrostWithdrawClaimTx{}}}, "FrostWithdrawClaim"},
		{"FrostWithdrawSignedTx", &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{}}}, "FrostWithdrawSigned"},
		{"FutureOrderTx", &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{}}}, "FutureOrder"},
		{"CertSignTx", &pb.AnyTx{Content: &pb.AnyTx_CertSignTx{CertSignTx: &pb.CertSignTx{}}}, "CertSign"},
		{"SetReferrerTx", &pb.AnyTx{Content: &pb.AnyTx_SetReferrerTx{SetReferrerTx: &pb.SetReferrerTx{}}}, "SetReferrer"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExtractTxType(c.tx); got != c.want {
				t.Errorf("got=%q want=%q", got, c.want)
			}
		})
	}
}

func TestExtractToAndValue_Variants(t *testing.T) {
	tx := &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{To: "bob", Amount: "100"}}}
	to, val := ExtractToAndValue(tx)
	if to != "bob" || val != "100" {
		t.Fatalf("Transaction: to=%q val=%q", to, val)
	}

	order := &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{Price: "5", Amount: "1"}}}
	to, val = ExtractToAndValue(order)
	if to != "" || val != "1 @ 5" {
		t.Fatalf("Order with price+amount: to=%q val=%q", to, val)
	}

	orderNoPrice := &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{Amount: "1"}}}
	to, val = ExtractToAndValue(orderNoPrice)
	if to != "" || val != "1" {
		t.Fatalf("Order no price: to=%q val=%q", to, val)
	}

	witness := &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{ReceiverAddress: "receiver", Amount: "42"}}}
	to, val = ExtractToAndValue(witness)
	if to != "receiver" || val != "42" {
		t.Fatalf("WitnessRequest: to=%q val=%q", to, val)
	}

	withdraw := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawRequestTx{FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{To: "external", Amount: "7"}}}
	to, val = ExtractToAndValue(withdraw)
	if to != "external" || val != "7" {
		t.Fatalf("FrostWithdrawRequest: to=%q val=%q", to, val)
	}

	cancel := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawCancelTx{FrostWithdrawCancelTx: &pb.FrostWithdrawCancelTx{WithdrawId: "withdraw-1"}}}
	to, val = ExtractToAndValue(cancel)
	if to != "withdraw-1" || val != "" {
		t.Fatalf("FrostWithdrawCancel: to=%q val=%q", to, val)
	}

	if to, val := ExtractToAndValue(nil); to != "" || val != "" {
		t.Fatalf("nil tx must return empty, got to=%q val=%q", to, val)
	}
}

func TestIndexBlock_WitnessRequestIsIndexedByReceiver(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idb.Close()

	tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{
		Base:            &pb.BaseMessage{TxId: "witness_tx_1", FromAddress: "requester", Status: pb.Status_SUCCEED},
		ReceiverAddress: "receiver",
		Amount:          "42",
	}}}
	block := &pb.Block{Header: &pb.BlockHeader{Height: 9}, Body: []*pb.AnyTx{tx}}
	if err := idb.IndexBlock(block); err != nil {
		t.Fatalf("IndexBlock: %v", err)
	}

	typeSet := map[string]struct{}{"WitnessRequest": {}}
	txs, err := idb.GetAddressTxHistory("receiver", typeSet, 10)
	if err != nil {
		t.Fatalf("GetAddressTxHistory: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("receiver history len=%d want 1", len(txs))
	}
	if got := txs[0]; got.TxID != "witness_tx_1" || got.ToAddress != "receiver" || got.Value != "42" {
		t.Fatalf("unexpected history record: %+v", got)
	}
}

func TestIndexBlock_DuplicateTxStillIndexesBlockSlot(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idb.Close()

	tx := &pb.AnyTx{Content: &pb.AnyTx_MinerTx{MinerTx: &pb.MinerTx{
		Base:   &pb.BaseMessage{TxId: "miner_tx_1", FromAddress: "miner", Status: pb.Status_SUCCEED},
		Op:     pb.OrderOp_ADD,
		Amount: "3200000000",
	}}}
	if err := idb.IndexBlock(&pb.Block{Header: &pb.BlockHeader{Height: 1}, Body: []*pb.AnyTx{tx}}); err != nil {
		t.Fatalf("IndexBlock first: %v", err)
	}
	if err := idb.IndexBlock(&pb.Block{Header: &pb.BlockHeader{Height: 2}, Body: []*pb.AnyTx{tx}}); err != nil {
		t.Fatalf("IndexBlock second: %v", err)
	}

	txs, err := idb.GetBlockTxs(2)
	if err != nil {
		t.Fatalf("GetBlockTxs: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("height 2 tx count=%d want 1", len(txs))
	}
	if txs[0].TxID != "miner_tx_1" || txs[0].Height != 2 || txs[0].TxIndex != 0 {
		t.Fatalf("unexpected normalized record: %+v", txs[0])
	}
}

func TestIndexBlock_StoresBlockHash(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idb.Close()

	block := &pb.Block{BlockHash: "block-hash-7", Header: &pb.BlockHeader{Height: 7}}
	if err := idb.IndexBlock(block); err != nil {
		t.Fatalf("IndexBlock: %v", err)
	}
	got, err := idb.GetBlockHash(7)
	if err != nil {
		t.Fatalf("GetBlockHash: %v", err)
	}
	if got != "block-hash-7" {
		t.Fatalf("block hash=%q want block-hash-7", got)
	}
}

func TestGetRecentTxs_ByTypeUsesTypeIndex(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idb.Close()

	tx1 := testTransferTx("tx_transfer_old", "alice")
	tx2 := testOrderTx("tx_order", "maker")
	tx3 := testWitnessVoteTx("tx_witness_vote", "witness")
	tx4 := testTransferTx("tx_transfer_new", "bob")
	if err := idb.IndexBlock(&pb.Block{Header: &pb.BlockHeader{Height: 1}, Body: []*pb.AnyTx{tx1, tx2}}); err != nil {
		t.Fatalf("IndexBlock 1: %v", err)
	}
	if err := idb.IndexBlock(&pb.Block{Header: &pb.BlockHeader{Height: 2}, Body: []*pb.AnyTx{tx3, tx4}}); err != nil {
		t.Fatalf("IndexBlock 2: %v", err)
	}

	txs, err := idb.GetRecentTxs("Transaction", 10)
	if err != nil {
		t.Fatalf("GetRecentTxs Transaction: %v", err)
	}
	if len(txs) != 2 {
		t.Fatalf("transaction count=%d want 2", len(txs))
	}
	if txs[0].TxID != "tx_transfer_new" || txs[0].Height != 2 || txs[0].TxIndex != 1 {
		t.Fatalf("new transaction not first: %+v", txs[0])
	}
	if txs[1].TxID != "tx_transfer_old" || txs[1].Height != 1 || txs[1].TxIndex != 0 {
		t.Fatalf("old transaction mismatch: %+v", txs[1])
	}

	witnessTxs, err := idb.GetRecentTxs("WitnessVote", 1)
	if err != nil {
		t.Fatalf("GetRecentTxs WitnessVote: %v", err)
	}
	if len(witnessTxs) != 1 || witnessTxs[0].TxID != "tx_witness_vote" {
		t.Fatalf("unexpected witness txs: %+v", witnessTxs)
	}
}

func TestGetRecentTxsAllBoundsSparseBlockScan(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idb.Close()

	if err := idb.IndexBlock(&pb.Block{Header: &pb.BlockHeader{Height: 400}, Body: []*pb.AnyTx{testTransferTx("too-old-for-recent-scan", "alice")}}); err != nil {
		t.Fatalf("IndexBlock: %v", err)
	}
	if err := idb.SetSyncHeight(1000); err != nil {
		t.Fatalf("SetSyncHeight: %v", err)
	}

	txs, err := idb.GetRecentTxs("all", 1)
	if err != nil {
		t.Fatalf("GetRecentTxs: %v", err)
	}
	if len(txs) != 0 {
		t.Fatalf("sparse scan reached a record beyond %d blocks: %+v", maxRecentAllTypeBlockScan, txs)
	}
}

func TestGetAddressTxHistory_ByTypeUsesAddressTypeIndexAndPaginates(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idb.Close()

	if err := idb.IndexBlock(&pb.Block{Header: &pb.BlockHeader{Height: 1}, Body: []*pb.AnyTx{testTransferTx("tx_transfer_old", "alice")}}); err != nil {
		t.Fatalf("IndexBlock 1: %v", err)
	}
	if err := idb.IndexBlock(&pb.Block{Header: &pb.BlockHeader{Height: 2}, Body: []*pb.AnyTx{testWitnessRequestTx("tx_witness_request", "requester")}}); err != nil {
		t.Fatalf("IndexBlock 2: %v", err)
	}
	if err := idb.IndexBlock(&pb.Block{Header: &pb.BlockHeader{Height: 3}, Body: []*pb.AnyTx{testTransferTx("tx_transfer_new", "bob")}}); err != nil {
		t.Fatalf("IndexBlock 3: %v", err)
	}

	typeSet := map[string]struct{}{"Transaction": {}}
	txs, err := idb.GetAddressTxHistoryPage("receiver", typeSet, 1, 0)
	if err != nil {
		t.Fatalf("GetAddressTxHistoryPage type first page: %v", err)
	}
	if len(txs) != 1 || txs[0].TxID != "tx_transfer_new" {
		t.Fatalf("unexpected first typed page: %+v", txs)
	}
	txs, err = idb.GetAddressTxHistoryPage("receiver", typeSet, 1, 1)
	if err != nil {
		t.Fatalf("GetAddressTxHistoryPage type second page: %v", err)
	}
	if len(txs) != 1 || txs[0].TxID != "tx_transfer_old" {
		t.Fatalf("unexpected second typed page: %+v", txs)
	}

	txs, err = idb.GetAddressTxHistoryPage("receiver", nil, 1, 1)
	if err != nil {
		t.Fatalf("GetAddressTxHistoryPage untyped offset: %v", err)
	}
	if len(txs) != 1 || txs[0].TxID != "tx_witness_request" {
		t.Fatalf("unexpected untyped offset page: %+v", txs)
	}
}

func TestGetAddressTxHistoryPageHydratesOnlySelectedPage(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idb.Close()

	                                                                           
	                                                                         
	                                      
	if err := idb.Set(KeyAddressTx("receiver", 3, 0), "newest"); err != nil {
		t.Fatalf("set newest index: %v", err)
	}
	if err := idb.Set(KeyTxDetail("newest"), "not-json"); err != nil {
		t.Fatalf("set newest detail: %v", err)
	}
	if err := idb.Set(KeyAddressTx("receiver", 2, 0), "middle"); err != nil {
		t.Fatalf("set middle index: %v", err)
	}
	if err := idb.Set(KeyTxDetail("middle"), "not-json"); err != nil {
		t.Fatalf("set middle detail: %v", err)
	}
	record := &TxRecord{TxID: "oldest", TxType: "Transaction"}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	if err := idb.Set(KeyAddressTx("receiver", 1, 0), "oldest"); err != nil {
		t.Fatalf("set oldest index: %v", err)
	}
	if err := idb.Set(KeyTxDetail("oldest"), string(encoded)); err != nil {
		t.Fatalf("set oldest detail: %v", err)
	}

	txs, err := idb.GetAddressTxHistoryPage("receiver", nil, 1, 2)
	if err != nil {
		t.Fatalf("GetAddressTxHistoryPage: %v", err)
	}
	if len(txs) != 1 || txs[0].TxID != "oldest" || txs[0].Height != 1 {
		t.Fatalf("unexpected selected page: %+v", txs)
	}
}

func testTransferTx(txID, from string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base:   &pb.BaseMessage{TxId: txID, FromAddress: from, Status: pb.Status_SUCCEED},
		To:     "receiver",
		Amount: "1",
	}}}
}

func testWitnessRequestTx(txID, from string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{
		Base:            &pb.BaseMessage{TxId: txID, FromAddress: from, Status: pb.Status_SUCCEED},
		ReceiverAddress: "receiver",
		Amount:          "42",
	}}}
}

func testOrderTx(txID, from string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
		Base:   &pb.BaseMessage{TxId: txID, FromAddress: from, Status: pb.Status_SUCCEED},
		Amount: "1",
		Price:  "2",
	}}}
}

func testWitnessVoteTx(txID, from string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_WitnessVoteTx{WitnessVoteTx: &pb.WitnessVoteTx{
		Base: &pb.BaseMessage{TxId: txID, FromAddress: from, Status: pb.Status_SUCCEED},
		Vote: &pb.WitnessVote{RequestId: "req1", WitnessAddress: from, VoteType: pb.WitnessVoteType_VOTE_PASS},
	}}}
}

                                                                                 

func TestIndexDB_BasicSetGetDelete(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idb.Close()

	if err := idb.Set("k1", "v1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := idb.Get("k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "v1" {
		t.Fatalf("got=%q want v1", got)
	}

	                                                     
	if err := idb.Delete("k1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got, err := idb.Get("k1"); err == nil && got != "" {
		t.Fatalf("expected key gone after Delete, got=%q err=%v", got, err)
	}
}

func TestIndexDB_ResetClearsKeys(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idb.Close()

	if err := idb.Set("k1", "v1"); err != nil {
		t.Fatalf("Set k1: %v", err)
	}
	if err := idb.Set("k2", "v2"); err != nil {
		t.Fatalf("Set k2: %v", err)
	}
	if err := idb.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	for _, key := range []string{"k1", "k2"} {
		if got, err := idb.Get(key); err != nil || got != "" {
			t.Fatalf("key %s after reset got=%q err=%v", key, got, err)
		}
	}
}

func TestIndexDB_GetMissingReturnsErrorOrEmpty(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idb.Close()
	got, err := idb.Get("never-set")
	if err == nil && got != "" {
		t.Fatalf("missing key must error or return empty, got=%q err=%v", got, err)
	}
}
