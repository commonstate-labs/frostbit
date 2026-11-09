package vm

import (
	"reflect"
	"strings"
	"testing"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"

	"google.golang.org/protobuf/proto"
)

func btcIsolationParentForTest(id string) *FrostWithdrawRequest {
	return &FrostWithdrawRequest{WithdrawID: id, Chain: "btc", Asset: "BTC", Amount: "20000", BroadcastFee: "0", Seq: 1,
		To: testBTCTaprootAddress, Status: WithdrawStatusQueued, BtcProtocolVersion: 3,
		BtcUnallocatedAmount: "20000", BtcClaimedAmount: "0", BtcSignedAmount: "0", BtcRefundedAmount: "0",
		BtcMaxTotalFee: "10000", BtcCommittedFee: "0", BtcMaxParts: 4, BtcNextPartSeq: 1}
}

func enableBTCIsolationForTest(t *testing.T, sv StateView) {
	t.Helper()
	sv.(interface{ BindNetworkChainID(uint64) }).BindNetworkChainID(51025)
	sv.Set(keys.KeyProtocolVersion(), []byte("3"))
	sv.Set(keys.KeyProtocolUpgradeReceipt(3), []byte(protocol.UpgradeReceiptValue(protocol.Upgrade{Version: 3, ActivationHeight: 10, ID: protocol.UpgradeBTCIsolationV3})))
}

func btcIsolationSignedForTest(t *testing.T, f *btcWithdrawValidationFixture, c *pb.FrostWithdrawClaimTx) *pb.AnyTx {
	t.Helper()
	hashes, err := f.template.ComputeTaprootSighash([][]byte{f.scriptPubKey}, btc.SighashDefault)
	if err != nil {
		t.Fatal(err)
	}
	op, guard := btcDualTestKeys()
	a, err := schnorr.Sign(op, hashes[0])
	if err != nil {
		t.Fatal(err)
	}
	b, err := schnorr.Sign(guard, hashes[0])
	if err != nil {
		t.Fatal(err)
	}
	return &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
		Base:  &pb.BaseMessage{TxId: "v3-signed", FromAddress: c.Base.FromAddress, ExecutedHeight: 102},
		JobId: c.JobId, Chain: "btc", Asset: c.Asset, VaultId: c.VaultId, KeyEpoch: c.KeyEpoch,
		WithdrawIds: c.WithdrawIds, TemplateHash: c.TemplateHash, TemplateData: c.TemplateData,
		InputSigs: [][]byte{a.Serialize()}, GuardianSigs: [][]byte{b.Serialize()}, ScriptPubkeys: [][]byte{f.scriptPubKey}, SignedPackageBytes: a.Serialize(),
	}}}
}

func TestBTCIsolationClaimCancelSignedAndExactReplay(t *testing.T) {
	f, c, parent := seedBTCIsolationPrepareFixture(t)
	enableBTCIsolationForTest(t, f.sv)
	parent.Requester = "v3-owner"
	data, err := marshalWithdrawRequest(parent)
	if err != nil {
		t.Fatal(err)
	}
	f.sv.Set(keys.KeyFrostWithdraw(parent.WithdrawID), data)
	uRaw, _, _ := f.sv.Get(keys.KeyFrostBtcUtxo(f.vaultID, f.txID, 0))
	f.sv.Set(keys.KeyFrostBtcUtxoAmount(f.vaultID, f.template.Inputs[0].Amount, f.txID, 0), uRaw)
	f.sv.Set(keys.KeyToken(parent.Asset), mustMarshalProto(t, &pb.Token{Address: parent.Asset, NativeChain: "btc", Type: pb.TokenType_TOKEN_BRIDGED, TotalSupply: "10000"}))
	SetBalance(f.sv, parent.Requester, parent.Asset, &pb.TokenBalance{Balance: "0"})
	claim := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: c}}
	if ops, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(claim, f.sv); err != nil || receipt.Status != "SUCCEED" || len(ops) != 0 {
		t.Fatalf("claim: %v %+v", err, receipt)
	}
	cancel := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawCancelTx{FrostWithdrawCancelTx: &pb.FrostWithdrawCancelTx{Base: &pb.BaseMessage{TxId: "cancel-u", FromAddress: parent.Requester, ExecutedHeight: 101}, WithdrawId: parent.WithdrawID}}}
	if ops, receipt, err := (&FrostWithdrawCancelTxHandler{}).DryRun(cancel, f.sv); err != nil || receipt.Status != "SUCCEED" || len(ops) != 0 {
		t.Fatalf("cancel: %v %+v", err, receipt)
	}
	afterCancel, err := readBTCIsolationParent(f.sv, parent.WithdrawID)
	if err != nil || afterCancel.BtcClaimedAmount != "10000" || afterCancel.BtcRefundedAmount != "10000" || afterCancel.Status != WithdrawStatusSigning {
		t.Fatalf("partial cancel: %+v %v", afterCancel, err)
	}
	if balance := GetBalance(f.sv, parent.Requester, parent.Asset); balance.Balance != "10000" {
		t.Fatalf("refund %s", balance.Balance)
	}
	signed := btcIsolationSignedForTest(t, f, c)
	if ops, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(signed, f.sv); err != nil || receipt.Status != "SUCCEED" || len(ops) != 0 {
		t.Fatalf("signed: %v %+v", err, receipt)
	}
	final, err := readBTCIsolationParent(f.sv, parent.WithdrawID)
	if err != nil || final.Status != WithdrawStatusPartiallyCancelled || final.BtcSignedAmount != "10000" || final.BtcClaimedAmount != "0" {
		t.Fatalf("final: %+v %v", final, err)
	}
	if _, found, _ := f.sv.Get(keys.KeyFrostBtcUtxo(f.vaultID, f.txID, 0)); found {
		t.Fatal("consumed input retained")
	}
	changeTxID, _ := f.template.TxID()
	changeRaw, found, _ := f.sv.Get(keys.KeyFrostBtcUtxo(f.vaultID, changeTxID, 1))
	var change pb.FrostUtxo
	if !found || proto.Unmarshal(changeRaw, &change) != nil || !reflect.DeepEqual(change.OriginId, []byte(strings.Repeat("o", 32))) {
		t.Fatalf("change origin: %+v", &change)
	}
	before := f.sv.Diff()
	for _, tx := range []*pb.AnyTx{signed, cancel, claim} {
		var r *Receipt
		var err error
		switch tx.GetContent().(type) {
		case *pb.AnyTx_FrostWithdrawSignedTx:
			_, r, err = (&FrostWithdrawSignedTxHandler{}).DryRun(tx, f.sv)
		case *pb.AnyTx_FrostWithdrawCancelTx:
			_, r, err = (&FrostWithdrawCancelTxHandler{}).DryRun(tx, f.sv)
		default:
			_, r, err = (&FrostWithdrawClaimTxHandler{}).DryRun(tx, f.sv)
		}
		if err != nil || r.Status != "SUCCEED" {
			t.Fatalf("replay: %v %+v", err, r)
		}
	}
	if !reflect.DeepEqual(before, f.sv.Diff()) {
		t.Fatal("replay changed ledger")
	}
}

func TestBTCIsolationRequestExplicitBudgetsAndSingleBurn(t *testing.T) {
	for _, tc := range []struct {
		name, fee      string
		version, parts uint32
		valid          bool
	}{
		{"valid", "10000", 3, 4, true}, {"missing version", "10000", 0, 4, false},
		{"missing fee", "", 3, 4, false}, {"zero fee", "0", 3, 4, false},
		{"fee consumes gross", "100000", 3, 4, false}, {"missing parts", "10000", 3, 0, false},
		{"too many parts", "10000", 3, protocol.BTCIsolationMaxParts + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBTCWithdrawAdmissionFixture(t, "100000")
			enableBTCIsolationForTest(t, f.sv)
			tx := f.tx()
			req := tx.GetFrostWithdrawRequestTx()
			req.BtcProtocolVersion, req.BtcMaxTotalFee, req.BtcMaxParts = tc.version, tc.fee, tc.parts
			before := f.sv.Diff()
			ops, receipt, err := (&FrostWithdrawRequestTxHandler{}).DryRun(tx, f.sv)
			if !tc.valid {
				if err == nil || receipt.Status != "FAILED" || !reflect.DeepEqual(before, f.sv.Diff()) {
					t.Fatalf("invalid consent: %v %+v", err, receipt)
				}
				return
			}
			if err != nil || receipt.Status != "SUCCEED" || len(ops) != 0 {
				t.Fatalf("request: %v %+v", err, receipt)
			}
			ref, _, _ := f.sv.Get(keys.KeyFrostWithdrawTxRef(req.Base.TxId))
			parent, err := readBTCIsolationParent(f.sv, string(ref))
			if err != nil || parent.BtcUnallocatedAmount != "100000" || parent.BtcMaxTotalFee != "10000" || parent.BtcNextPartSeq != 1 {
				t.Fatalf("parent: %+v %v", parent, err)
			}
			if _, found, _ := f.sv.Get(keys.KeyFrostBtcWithdrawReady(parent.Asset, parent.Seq, parent.WithdrawID)); !found {
				t.Fatal("missing ready parent")
			}
			if _, found, _ := f.sv.Get(keys.KeyFrostWithdrawFIFOIndex("btc", parent.Asset, parent.Seq)); found {
				t.Fatal("v3 parent polluted legacy FIFO")
			}
			after := f.sv.Diff()
			if _, receipt, err := (&FrostWithdrawRequestTxHandler{}).DryRun(tx, f.sv); err != nil || receipt.Status != "SUCCEED" {
				t.Fatalf("request retry: %v %+v", err, receipt)
			}
			if !reflect.DeepEqual(after, f.sv.Diff()) {
				t.Fatal("request retry burned or enqueued twice")
			}
		})
	}
}

func TestBTCIsolationSignedRejectsParentAndSignatureMutation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*pb.FrostWithdrawSignedTx)
	}{
		{"different parent", func(s *pb.FrostWithdrawSignedTx) { s.WithdrawIds[0] = "different-parent" }},
		{"different signature", func(s *pb.FrostWithdrawSignedTx) { s.InputSigs[0][0] ^= 1 }},
		{"different guardian", func(s *pb.FrostWithdrawSignedTx) { s.GuardianSigs[0][0] ^= 1 }},
		{"account field", func(s *pb.FrostWithdrawSignedTx) { s.IncludesHandover = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c, parent := seedBTCIsolationPrepareFixture(t)
			enableBTCIsolationForTest(t, f.sv)
			data, _ := marshalWithdrawRequest(parent)
			f.sv.Set(keys.KeyFrostWithdraw(parent.WithdrawID), data)
			uRaw, _, _ := f.sv.Get(keys.KeyFrostBtcUtxo(f.vaultID, f.txID, 0))
			f.sv.Set(keys.KeyFrostBtcUtxoAmount(f.vaultID, f.template.Inputs[0].Amount, f.txID, 0), uRaw)
			if _, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: c}}, f.sv); err != nil || receipt.Status != "SUCCEED" {
				t.Fatalf("claim: %v %+v", err, receipt)
			}
			tx := btcIsolationSignedForTest(t, f, c)
			tc.mutate(tx.GetFrostWithdrawSignedTx())
			before := f.sv.Diff()
			if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(tx, f.sv); err == nil || receipt.Status != "FAILED" {
				t.Fatal("accepted mutated signed intent")
			}
			if !reflect.DeepEqual(before, f.sv.Diff()) {
				t.Fatal("rejected Signed published partial artifact or parent amount")
			}
		})
	}
}

func TestBTCIsolationFullBalanceRequestThenCancelRestoresBalanceAndSupply(t *testing.T) {
	f := newBTCWithdrawAdmissionFixture(t, "100000")
	enableBTCIsolationForTest(t, f.sv)
	SetBalance(f.sv, f.user, f.tokenAddr, &pb.TokenBalance{Balance: "100000"})
	raw, _, _ := f.sv.Get(keys.KeyToken(f.tokenAddr))
	var token pb.Token
	if err := proto.Unmarshal(raw, &token); err != nil {
		t.Fatal(err)
	}
	token.TotalSupply = "100000"
	f.sv.Set(keys.KeyToken(f.tokenAddr), mustMarshalProto(t, &token))
	tx := f.tx()
	req := tx.GetFrostWithdrawRequestTx()
	req.BtcProtocolVersion, req.BtcMaxTotalFee, req.BtcMaxParts = 3, "10000", 4
	if _, r, err := (&FrostWithdrawRequestTxHandler{}).DryRun(tx, f.sv); err != nil || r.Status != "SUCCEED" {
		t.Fatalf("request: %v %+v", err, r)
	}
	if data, exists, _ := f.sv.Get(keys.KeyBalance(f.user, f.tokenAddr)); exists || len(data) != 0 {
		t.Fatalf("zero balance became an empty record: %x", data)
	}
	ref, _, _ := f.sv.Get(keys.KeyFrostWithdrawTxRef(req.Base.TxId))
	cancel := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawCancelTx{FrostWithdrawCancelTx: &pb.FrostWithdrawCancelTx{
		Base: &pb.BaseMessage{TxId: "cancel-full-balance", FromAddress: f.user}, WithdrawId: string(ref),
	}}}
	if _, r, err := (&FrostWithdrawCancelTxHandler{}).DryRun(cancel, f.sv); err != nil || r.Status != "SUCCEED" {
		t.Fatalf("cancel: %v %+v", err, r)
	}
	if balance := GetBalance(f.sv, f.user, f.tokenAddr); balance.Balance != "100000" {
		t.Fatalf("restored balance=%s", balance.Balance)
	}
	raw, _, _ = f.sv.Get(keys.KeyToken(f.tokenAddr))
	if proto.Unmarshal(raw, &token) != nil || token.TotalSupply != "100000" {
		t.Fatalf("restored supply=%s", token.TotalSupply)
	}
}

func TestBTCIsolationParentSequenceDoesNotCreateLegacyFIFOExceptions(t *testing.T) {
	f := newBTCWithdrawAdmissionFixture(t, "100000")
	f.sv.Set(keys.KeyFrostWithdrawFIFOSeq("btc", f.tokenAddr), []byte("0"))
	f.sv.Set(keys.KeyFrostWithdrawFIFOHead("btc", f.tokenAddr), []byte("1"))
	oldTx := f.tx()
	if _, r, err := (&FrostWithdrawRequestTxHandler{}).DryRun(oldTx, f.sv); err != nil || r.Status != "SUCCEED" {
		t.Fatalf("old request: %v %+v", err, r)
	}
	oldRef, _, _ := f.sv.Get(keys.KeyFrostWithdrawTxRef(oldTx.GetFrostWithdrawRequestTx().Base.TxId))
	enableBTCIsolationForTest(t, f.sv)
	newTx := f.tx()
	req := newTx.GetFrostWithdrawRequestTx()
	req.Base.TxId = "v3-after-legacy"
	req.BtcProtocolVersion, req.BtcMaxTotalFee, req.BtcMaxParts = 3, "10000", 4
	if _, r, err := (&FrostWithdrawRequestTxHandler{}).DryRun(newTx, f.sv); err != nil || r.Status != "SUCCEED" {
		t.Fatalf("new request: %v %+v", err, r)
	}
	if tail, _, _ := f.sv.Get(keys.KeyFrostWithdrawFIFOSeq("btc", f.tokenAddr)); string(tail) != "1" {
		t.Fatalf("legacy tail advanced: %s", tail)
	}
	if seq, _, _ := f.sv.Get(keys.KeyFrostBtcWithdrawParentSeq(f.tokenAddr)); string(seq) != "2" {
		t.Fatalf("new parent sequence: %s", seq)
	}
	cancel := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawCancelTx{FrostWithdrawCancelTx: &pb.FrostWithdrawCancelTx{
		Base: &pb.BaseMessage{TxId: "cancel-old-with-new-parent", FromAddress: f.user}, WithdrawId: string(oldRef),
	}}}
	if _, r, err := (&FrostWithdrawCancelTxHandler{}).DryRun(cancel, f.sv); err != nil || r.Status != "SUCCEED" {
		t.Fatalf("legacy cancel hit a v3 FIFO gap: %v %+v", err, r)
	}
}

func TestBTCIsolationRollbackPreservesEarlierSuccessfulTransaction(t *testing.T) {
	f := newBTCWithdrawAdmissionFixture(t, "100000")
	enableBTCIsolationForTest(t, f.sv)
	SetBalance(f.sv, f.user, f.tokenAddr, &pb.TokenBalance{Balance: "200000"})
	raw, _, _ := f.sv.Get(keys.KeyToken(f.tokenAddr))
	var token pb.Token
	if err := proto.Unmarshal(raw, &token); err != nil {
		t.Fatal(err)
	}
	token.TotalSupply = "200000"
	f.sv.Set(keys.KeyToken(f.tokenAddr), mustMarshalProto(t, &token))
	f.base["rollback-existing-delete"] = []byte("old")
	f.sv.Del("rollback-existing-delete")
	f.sv.Set("rollback-existing-overlay", []byte("earlier block write"))
	first := f.tx()
	firstReq := first.GetFrostWithdrawRequestTx()
	firstReq.BtcProtocolVersion, firstReq.BtcMaxTotalFee, firstReq.BtcMaxParts = 3, "10000", 4
	if _, r, err := (&FrostWithdrawRequestTxHandler{}).DryRun(first, f.sv); err != nil || r.Status != "SUCCEED" {
		t.Fatalf("first request: %v %+v", err, r)
	}
	                                                                          
	                                                                            
	releaseStateViewHistory(f.sv)
	before, err := f.sv.Scan("")
	if err != nil {
		t.Fatal(err)
	}
	snap := f.sv.Snapshot()
	second := proto.Clone(first).(*pb.AnyTx)
	second.GetFrostWithdrawRequestTx().Base.TxId = "second-request-rolls-back"
	if _, r, err := (&FrostWithdrawRequestTxHandler{}).DryRun(second, f.sv); err != nil || r.Status != "SUCCEED" {
		t.Fatalf("second request: %v %+v", err, r)
	}
	if err := f.sv.Revert(snap); err != nil {
		t.Fatal(err)
	}
	after, err := f.sv.Scan("")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("second transaction rollback leaked business writes or undid the first transaction")
	}
	if balance := GetBalance(f.sv, f.user, f.tokenAddr); balance.Balance != "100000" {
		t.Fatalf("first request balance was lost: %s", balance.Balance)
	}
}

func TestBTCIsolationParentLedgerRoundTrip(t *testing.T) {
	parent := btcIsolationParentForTest("parent")
	data, err := marshalWithdrawRequest(parent)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := unmarshalWithdrawRequest(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parent, roundTrip) {
		t.Fatalf("parent fields were lost: %+v", roundTrip)
	}
	ledger, err := readBTCIsolationLedger(roundTrip)
	if err != nil || ledger.unallocated != 20000 {
		t.Fatalf("ledger: %+v %v", ledger, err)
	}
	for _, mutate := range []func(*FrostWithdrawRequest){
		func(p *FrostWithdrawRequest) { p.BtcUnallocatedAmount = "19999" },
		func(p *FrostWithdrawRequest) { p.BtcClaimedAmount = "00" },
		func(p *FrostWithdrawRequest) { p.BtcCommittedFee = "10001" },
		func(p *FrostWithdrawRequest) { p.BtcNextPartSeq = 6 },
	} {
		broken := *parent
		mutate(&broken)
		if _, err := readBTCIsolationLedger(&broken); err == nil {
			t.Fatalf("accepted invalid ledger: %+v", broken)
		}
	}
}

func TestBTCIsolationParentStatusUsesAmounts(t *testing.T) {
	for _, tc := range []struct {
		u, c, s, r uint64
		status     WithdrawRequestStatus
		terminal   bool
	}{
		{20000, 0, 0, 0, WithdrawStatusQueued, false}, {10000, 10000, 0, 0, WithdrawStatusQueued, false},
		{0, 10000, 10000, 0, WithdrawStatusSigning, false}, {0, 0, 20000, 0, WithdrawStatusSigned, true},
		{0, 0, 0, 20000, WithdrawStatusCancelled, true}, {0, 0, 10000, 10000, WithdrawStatusPartiallyCancelled, true},
	} {
		p := btcIsolationParentForTest("status")
		l := &btcIsolationLedger{gross: 20000, unallocated: tc.u, claimed: tc.c, signed: tc.s, refunded: tc.r}
		if terminal := writeBTCIsolationLedger(p, l); terminal != tc.terminal || p.Status != tc.status {
			t.Fatalf("status %d/%d/%d/%d: %s terminal=%v", tc.u, tc.c, tc.s, tc.r, p.Status, terminal)
		}
	}
}

func seedBTCIsolationPrepareFixture(t *testing.T) (*btcWithdrawValidationFixture, *pb.FrostWithdrawClaimTx, *FrostWithdrawRequest) {
	t.Helper()
	f := newBTCWithdrawValidationFixture(t)
	cfgRaw, _, _ := f.sv.Get(keys.KeyFrostVaultConfig("btc", 0))
	var cfg pb.FrostVaultConfig
	if err := proto.Unmarshal(cfgRaw, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.DepositDexChainId = 51025
	f.sv.Set(keys.KeyFrostVaultConfig("btc", 0), mustMarshalProto(t, &cfg))
	key := keys.KeyFrostBtcUtxo(f.vaultID, f.txID, 0)
	raw, _, _ := f.sv.Get(key)
	utxo := new(pb.FrostUtxo)
	if err := proto.Unmarshal(raw, utxo); err != nil {
		t.Fatal(err)
	}
	utxo.OriginId = []byte(strings.Repeat("o", 32))
	raw = mustMarshalProto(t, utxo)
	f.sv.Set(key, raw)
	f.sv.Set(keys.KeyFrostBtcSpendableUtxoIndex(f.vaultID, 1), raw)
	claim := f.claimTx(t, f.template).GetFrostWithdrawClaimTx()
	claim.BtcProtocolVersion, claim.BtcPartSeq, claim.BtcGrossAmount = 3, 1, "10000"
	parent := btcIsolationParentForTest(f.withdrawID)
	f.sv.(interface{ BindNetworkChainID(uint64) }).BindNetworkChainID(51025)
	fee := f.template.Inputs[0].Amount
	for _, out := range f.template.Outputs {
		fee -= out.Amount
	}
	claim.JobId = chain.BTCWithdrawPartJobID(chain.BTCWithdrawPartIntent{Network: 51025, BTCNetwork: "mainnet", ParentID: parent.WithdrawID,
		PartSeq: 1, VaultID: f.vaultID, KeyEpoch: f.keyEpoch, Gross: 10000, Fee: fee, OriginID: utxo.OriginId, TemplateHash: f.template.TemplateHash()})
	claim.Base.FromAddress, _ = chain.WithdrawCoordinator(claim.JobId, f.keyEpoch, f.committee)
	return f, claim, parent
}

func TestBTCIsolationPrepareClaimMovesOnlyChildGross(t *testing.T) {
	f, claim, parent := seedBTCIsolationPrepareFixture(t)
	before := f.sv.Diff()
	job, validated, reservation, err := prepareBTCIsolationClaim(f.sv, claim, parent)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, f.sv.Diff()) {
		t.Fatal("claim preparation wrote StateView")
	}
	if len(reservation) == 0 || len(validated.utxos) != 1 || job.BtcGrossAmount != "10000" || len(job.BtcOriginId) != 32 {
		t.Fatalf("invalid prepared child: %+v", job)
	}
	if parent.BtcUnallocatedAmount != "10000" || parent.BtcClaimedAmount != "10000" || parent.BtcNextPartSeq != 2 || parent.Status != WithdrawStatusQueued {
		t.Fatalf("parent was treated as a whole payment: %+v", parent)
	}
}

func TestBTCIsolationPrepareClaimRejectsBudgetsWithoutWrites(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*pb.FrostWithdrawClaimTx, *FrostWithdrawRequest)
	}{
		{"sequence", func(c *pb.FrostWithdrawClaimTx, p *FrostWithdrawRequest) { c.BtcPartSeq = 2 }},
		{"parts", func(c *pb.FrostWithdrawClaimTx, p *FrostWithdrawRequest) {
			p.BtcMaxParts = 1
			p.BtcNextPartSeq = 2
			c.BtcPartSeq = 2
		}},
		{"fee", func(c *pb.FrostWithdrawClaimTx, p *FrostWithdrawRequest) { p.BtcMaxTotalFee = "1" }},
		{"gross", func(c *pb.FrostWithdrawClaimTx, p *FrostWithdrawRequest) { c.BtcGrossAmount = "20001" }},
		{"multiple parents", func(c *pb.FrostWithdrawClaimTx, p *FrostWithdrawRequest) {
			c.WithdrawIds = append(c.WithdrawIds, "another")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c, p := seedBTCIsolationPrepareFixture(t)
			tc.mutate(c, p)
			before := f.sv.Diff()
			if _, _, _, err := prepareBTCIsolationClaim(f.sv, c, p); err == nil {
				t.Fatal("accepted invalid child")
			}
			if !reflect.DeepEqual(before, f.sv.Diff()) {
				t.Fatal("rejected preparation wrote state")
			}
		})
	}
}
