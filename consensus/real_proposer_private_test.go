package consensus

import (
	"dex/config"
	"dex/db"
	"dex/interfaces"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/protocol"
	"dex/txpool"
	"dex/txverify"
	"dex/types"
	"dex/utils"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

type privateTxProviderProbe struct {
	sender          string
	hasPending      bool
	limits          interfaces.PrivateProposalLimits
	batch           *interfaces.PrivateTxBatch
	prepareErr      error
	validateErr     error
	hasCalls        int
	limitsCalls     int
	prepareCalls    int
	validateCalls   int
	lastContext     interfaces.ProposalContext
	lastPublic      []*pb.AnyTx
	lastMarket      *interfaces.ProposalSpotMarket
	preparedBatch   *interfaces.PrivateTxBatch
	lastResult      *interfaces.ProposalSimulationResult
	validatedIDs    []string
	callOrder       *[]string
	mutatePublic    bool
	mutateValidated bool
}

type privateTxCandidateProviderProbe struct {
	*privateTxProviderProbe
	candidates         []*interfaces.PrivateTxBatch
	candidateErr       error
	candidateCalls     int
	preparedCandidates []*interfaces.PrivateTxBatch
}

func (p *privateTxProviderProbe) SenderAddress() string {
	if p.sender != "" {
		return p.sender
	}
	if p.batch != nil && len(p.batch.Transactions) > 0 && p.batch.Transactions[0] != nil && p.batch.Transactions[0].GetBase() != nil {
		return p.batch.Transactions[0].GetBase().FromAddress
	}
	return ""
}

func (p *privateTxProviderProbe) Limits() interfaces.PrivateProposalLimits {
	p.limitsCalls++
	if p.callOrder != nil {
		*p.callOrder = append(*p.callOrder, "limits")
	}
	return p.limits
}

func (p *privateTxProviderProbe) HasPending(ctx interfaces.ProposalContext) bool {
	p.hasCalls++
	p.lastContext = ctx
	return p.hasPending
}

func (p *privateTxProviderProbe) Prepare(ctx interfaces.ProposalContext, orderedPublic []*pb.AnyTx, market *interfaces.ProposalSpotMarket) (*interfaces.PrivateTxBatch, error) {
	p.prepareCalls++
	p.lastContext = ctx
	p.lastPublic = orderedPublic
	p.lastMarket = market
	if p.callOrder != nil {
		*p.callOrder = append(*p.callOrder, "prepare")
	}
	if p.batch == nil || p.prepareErr != nil {
		return p.batch, p.prepareErr
	}
	if p.mutatePublic {
		for _, tx := range orderedPublic {
			if base := tx.GetBase(); base != nil {
				base.ExecutedHeight = ctx.Height
			}
		}
	}
	prepared := *p.batch
	prepared.Transactions = append([]*pb.AnyTx(nil), p.batch.Transactions...)
	prepared.InsertAfterPublic = append([]int(nil), p.batch.InsertAfterPublic...)
	if prepared.PublicPrefixHash == "" && prepared.PublicPrefixCount == 0 {
		prepared.PublicPrefixHash = ctx.PublicPrefixHash
		prepared.PublicPrefixCount = ctx.PublicPrefixCount
	}
	p.preparedBatch = &prepared
	return &prepared, nil
}

func (p *privateTxProviderProbe) ValidateCandidate(ctx interfaces.ProposalContext, batch *interfaces.PrivateTxBatch, result *interfaces.ProposalSimulationResult) error {
	p.validateCalls++
	p.lastContext = ctx
	p.lastResult = result
	if batch != nil {
		p.validatedIDs = append(p.validatedIDs, batch.ID)
		if p.mutateValidated && len(batch.Transactions) > 0 && batch.Transactions[0] != nil && batch.Transactions[0].GetBase() != nil {
			batch.Transactions[0].GetBase().ExecutedHeight = ctx.Height + 1000
		}
	}
	if p.mutateValidated && p.batch != nil && len(p.batch.Transactions) > 0 && p.batch.Transactions[0] != nil && p.batch.Transactions[0].GetBase() != nil {
		                                                                         
		                                                       
		p.batch.Transactions[0].GetBase().ExecutedHeight = ctx.Height + 2000
	}
	return p.validateErr
}

func (p *privateTxCandidateProviderProbe) PrepareCandidates(ctx interfaces.ProposalContext, orderedPublic []*pb.AnyTx, market *interfaces.ProposalSpotMarket) ([]*interfaces.PrivateTxBatch, error) {
	p.candidateCalls++
	p.lastContext = ctx
	p.lastPublic = orderedPublic
	p.lastMarket = market
	if p.callOrder != nil {
		*p.callOrder = append(*p.callOrder, "prepareCandidates")
	}
	if p.mutatePublic {
		for _, tx := range orderedPublic {
			if base := tx.GetBase(); base != nil {
				base.ExecutedHeight = ctx.Height
			}
		}
	}
	if p.candidateErr != nil {
		return nil, p.candidateErr
	}
	prepared := make([]*interfaces.PrivateTxBatch, 0, len(p.candidates))
	for _, batch := range p.candidates {
		if batch == nil {
			prepared = append(prepared, nil)
			continue
		}
		copyBatch := *batch
		copyBatch.Transactions = append([]*pb.AnyTx(nil), batch.Transactions...)
		copyBatch.InsertAfterPublic = append([]int(nil), batch.InsertAfterPublic...)
		if copyBatch.PublicPrefixHash == "" && copyBatch.PublicPrefixCount == 0 {
			copyBatch.PublicPrefixHash = ctx.PublicPrefixHash
			copyBatch.PublicPrefixCount = ctx.PublicPrefixCount
		}
		prepared = append(prepared, &copyBatch)
	}
	p.preparedCandidates = prepared
	return prepared, nil
}

type proposalPreflightProbe struct {
	market           *interfaces.ProposalSpotMarket
	projectErr       error
	projectSeen      []*pb.AnyTx
	projectContext   interfaces.ProposalContext
	projectMaxOrders int
	projectTaker     string
	projectCalls     int
	mutateProjection bool
	result           *interfaces.ProposalSimulationResult
	results          []*interfaces.ProposalSimulationResult
	err              error
	errs             []error
	seen             *pb.Block
	seenCandidates   []*pb.Block
	calls            int
	mutate           bool
	callOrder        *[]string
}

func (p *proposalPreflightProbe) ProjectSpotPrefix(ctx interfaces.ProposalContext, orderedPublic []*pb.AnyTx, maxOrdersPerBook int, takerAddress string) (*interfaces.ProposalSpotMarket, error) {
	p.projectCalls++
	p.projectContext = ctx
	p.projectSeen = orderedPublic
	p.projectMaxOrders = maxOrdersPerBook
	p.projectTaker = takerAddress
	if p.callOrder != nil {
		*p.callOrder = append(*p.callOrder, "project")
	}
	if p.mutateProjection {
		for _, tx := range orderedPublic {
			if base := tx.GetBase(); base != nil {
				base.ExecutedHeight = ctx.Height
			}
		}
	}
	if p.market != nil || p.projectErr != nil {
		return p.market, p.projectErr
	}
	return &interfaces.ProposalSpotMarket{
		PublicPrefixHash:  ctx.PublicPrefixHash,
		PublicPrefixCount: ctx.PublicPrefixCount,
	}, nil
}

func (p *proposalPreflightProbe) Simulate(block *pb.Block) (*interfaces.ProposalSimulationResult, error) {
	callIndex := p.calls
	p.calls++
	p.seen = block
	p.seenCandidates = append(p.seenCandidates, block)
	if p.callOrder != nil {
		*p.callOrder = append(*p.callOrder, "simulate")
	}
	if p.mutate && block != nil && block.Header != nil {
		for _, tx := range block.Body {
			if base := tx.GetBase(); base != nil {
				base.ExecutedHeight = block.Header.Height
			}
		}
	}
	result, err := p.result, p.err
	if callIndex < len(p.results) {
		result = p.results[callIndex]
	}
	if callIndex < len(p.errs) {
		err = p.errs[callIndex]
	}
	return result, err
}

func privateTestOrder(id, sender string, nonce, height uint64, proposer string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
		Base: &pb.BaseMessage{
			TxId:         id,
			FromAddress:  sender,
			Nonce:        nonce,
			Fee:          fmt.Sprint(protocol.MinRuntimeFeeRaw),
			TargetHeight: height,
			OnlyProposer: proposer,
		},
		BaseToken:  "BTC",
		QuoteToken: "USDT",
		Op:         pb.OrderOp_ADD,
		Amount:     "1",
		Price:      "100",
	}}}
}

func privateTestPublicOrder(id, sender string, side pb.OrderSide) *pb.AnyTx {
	tx := privateTestOrder(id, sender, 0, 0, "")
	tx.GetOrderTx().Side = side
	return tx
}

func privateTestTransfer(id, sender string, nonce uint64) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base:         &pb.BaseMessage{TxId: id, FromAddress: sender, Nonce: nonce, Fee: fmt.Sprint(protocol.MinRuntimeFeeRaw)},
		To:           "receiver-" + sender,
		Amount:       "1",
		TokenAddress: "FB",
	}}}
}

func privateTestCert(id, sender string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_CertSignTx{CertSignTx: &pb.CertSignTx{
		Base:             &pb.BaseMessage{TxId: id, FromAddress: sender, Fee: fmt.Sprint(protocol.MinRuntimeFeeRaw)},
		CheckpointHeight: 1,
	}}}
}

func newPrivateProposerHarness(t *testing.T, limit int) (*RealBlockProposer, *txpool.TxPool) {
	t.Helper()
	previousChainID := pb.GetDefaultChainID()
	pb.SetDefaultChainID(0)
	t.Cleanup(func() { pb.SetDefaultChainID(previousChainID) })
	logger := logs.NewNodeLogger("private-proposer-test", 100)
	dbm, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logger)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	dbm.InitWriteQueue(100, time.Millisecond)
	t.Cleanup(func() { dbm.Close() })
	pool, err := txpool.NewTxPool(dbm, &mockTxValidator{}, "private-proposer-test", logger)
	if err != nil {
		t.Fatalf("NewTxPool: %v", err)
	}
	keyMgr := utils.NewKeyManager()
	if err := keyMgr.InitKeyRandom(); err != nil {
		t.Fatalf("InitKeyRandom: %v", err)
	}
	return &RealBlockProposer{
		maxBlocksPerHeight:  2,
		targetTxsPerBlock:   limit,
		maxTxsPerBlock:      limit,
		maxTxsLimitPerBlock: limit,
		pool:                pool,
		dbManager:           dbm,
		vrfProvider:         utils.NewVRFProvider(),
		windowConfig:        config.WindowConfig{Enabled: false},
		livenessTracker:     verifiedParticipantTrackerForTest(),
		keyMgr:              keyMgr,
		selfAddress:         keyMgr.GetAddress(),
	}, pool
}

func TestValidatePrivateBatchAuthenticityMatchesBlockAdmission(t *testing.T) {
	proposer, _ := newPrivateProposerHarness(t, 4)
	const (
		chainID    = uint64(77001)
		height     = uint64(21)
		proposerID = "miner-private-auth"
	)
	pb.SetDefaultChainID(chainID)

	fundingKey := utils.NewKeyManager()
	if err := fundingKey.InitKeyRandom(); err != nil {
		t.Fatalf("InitKeyRandom funding key: %v", err)
	}
	makeSigned := func(from string) *pb.AnyTx {
		t.Helper()
		tx := privateTestOrder("", from, 0, height, proposerID)
		tx.GetBase().ChainId = chainID
		if _, err := txverify.SignWithKeyManager(tx, fundingKey); err != nil {
			t.Fatalf("SignWithKeyManager: %v", err)
		}
		return tx
	}
	cloneTx := func(tx *pb.AnyTx) *pb.AnyTx {
		t.Helper()
		cloned, ok := proto.Clone(tx).(*pb.AnyTx)
		if !ok || cloned == nil {
			t.Fatal("clone private transaction")
		}
		return cloned
	}
	ctx := interfaces.ProposalContext{
		Height:            height,
		Proposer:          types.NodeID(proposerID),
		PrivateTxLimit:    1,
		PublicPrefixCount: 1,
		PublicPrefixHash:  "auth-prefix",
	}
	validate := func(tx *pb.AnyTx) (string, error) {
		return proposer.validatePrivateBatch(ctx, &interfaces.PrivateTxBatch{
			ID:                "auth-batch",
			Transactions:      []*pb.AnyTx{tx},
			PublicPrefixCount: ctx.PublicPrefixCount,
			PublicPrefixHash:  ctx.PublicPrefixHash,
		})
	}

	valid := makeSigned(fundingKey.GetAddress())
	if sender, err := validate(valid); err != nil {
		t.Fatalf("canonical signed transaction rejected: %v", err)
	} else if sender != fundingKey.GetAddress() {
		t.Fatalf("validated sender = %q, want %q", sender, fundingKey.GetAddress())
	}

	tamperedPayload := cloneTx(valid)
	tamperedPayload.GetOrderTx().Price = "101"
	wrongChain := cloneTx(valid)
	wrongChain.GetBase().ChainId = chainID + 1
	badSignature := cloneTx(valid)
	badSignature.GetBase().Signature[0] ^= 0xff
	wrongSender := makeSigned("forged-funding-account")

	for _, tc := range []struct {
		name string
		tx   *pb.AnyTx
		want string
	}{
		{name: "canonical tx id", tx: tamperedPayload, want: "tx_id mismatch"},
		{name: "chain id", tx: wrongChain, want: "InvalidChainID"},
		{name: "ecdsa signature", tx: badSignature, want: "invalid ECDSA signature"},
		{name: "from address binding", tx: wrongSender, want: "public key does not match from_address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validate(tc.tx); err == nil {
				t.Fatalf("unauthenticated private transaction was accepted")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validation error = %q, want substring %q", err, tc.want)
			}
		})
	}
}

func storePrivateTestTx(t *testing.T, pool *txpool.TxPool, dbm *db.Manager, tx *pb.AnyTx) {
	t.Helper()
	                                                                           
	                                                                           
	owner := tx.GetBase().FromAddress
	balance, err := proto.Marshal(&pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: "1000000000"}})
	if err != nil {
		t.Fatal(err)
	}
	dbm.EnqueueSet(keys.KeyBalance(owner, "FB"), string(balance))
	if err := dbm.ForceFlushHot(); err != nil {
		t.Fatal(err)
	}
	if err := pool.StoreAnyTx(tx); err != nil {
		t.Fatalf("StoreAnyTx(%s): %v", tx.GetTxId(), err)
	}
}

func txIDs(txs []*pb.AnyTx) []string {
	ids := make([]string, 0, len(txs))
	for _, tx := range txs {
		ids = append(ids, tx.GetTxId())
	}
	return ids
}

func successfulPrivateResult(txs ...*pb.AnyTx) *interfaces.ProposalSimulationResult {
	receipts := make([]interfaces.ProposalSimulationReceipt, 0, len(txs))
	for _, tx := range txs {
		receipts = append(receipts, interfaces.ProposalSimulationReceipt{TxID: tx.GetTxId(), Status: "SUCCEED"})
	}
	return &interfaces.ProposalSimulationResult{Valid: true, Receipts: receipts}
}

func TestClampPrivateProposalLimits(t *testing.T) {
	privateTxs, ordersPerBook, err := clampPrivateProposalLimits(interfaces.PrivateProposalLimits{
		MaxTransactions:  100,
		MaxOrdersPerBook: 9000,
	})
	if err != nil {
		t.Fatalf("clampPrivateProposalLimits: %v", err)
	}
	if privateTxs != 100 || ordersPerBook != maxProjectedOrdersPerBook {
		t.Fatalf("validated limits = %d/%d, want %d/%d", privateTxs, ordersPerBook, 100, maxProjectedOrdersPerBook)
	}
	for _, limits := range []interfaces.PrivateProposalLimits{
		{MaxTransactions: 1, MaxOrdersPerBook: 64},
		{MaxTransactions: 2, MaxOrdersPerBook: 0},
	} {
		if _, _, err := clampPrivateProposalLimits(limits); err == nil {
			t.Fatalf("invalid private limits %+v were accepted", limits)
		}
	}
}

func TestMergePrivateTransactionsPreservesProtocolPriorityAndAllOrNothing(t *testing.T) {
	protocolA := privateTestCert("cert-a", "miner")
	protocolB := privateTestCert("cert-b", "miner")
	normalA := privateTestTransfer("normal-a", "alice", 0)
	normalB := privateTestTransfer("normal-b", "bob", 0)
	privateA := privateTestOrder("private-a", "miner", 0, 8, "miner")
	privateB := privateTestOrder("private-b", "miner", 1, 8, "miner")

	merged, ok := mergePrivateTransactionsAtPrefix(
		[]*pb.AnyTx{protocolA, normalA, normalB},
		[]*pb.AnyTx{privateA, privateB},
		2,
		5,
	)
	if !ok {
		t.Fatal("private batch should fit")
	}
	if got, want := txIDs(merged), []string{"cert-a", "normal-a", "private-a", "private-b", "normal-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("merged order = %v, want %v", got, want)
	}

	original := []*pb.AnyTx{protocolA, protocolB, normalA}
	got, ok := mergePrivateTransactionsAtPrefix(original, []*pb.AnyTx{privateA, privateB}, 1, 3)
	if ok {
		t.Fatal("batch must be omitted rather than displace protocol transactions")
	}
	if !reflect.DeepEqual(txIDs(got), txIDs(original)) {
		t.Fatalf("all-or-nothing failure changed normal body: %v", txIDs(got))
	}
}

func TestMergePrivateTransactionBatchInterleavesAndDropsOnlyPublicTail(t *testing.T) {
	publicSell := privateTestPublicOrder("public-sell", "a-public-seller", pb.OrderSide_SELL)
	publicBuy := privateTestPublicOrder("public-buy", "b-public-buyer", pb.OrderSide_BUY)
	publicTail := privateTestTransfer("public-tail", "z-public-tail", 0)
	publicDropped := privateTestTransfer("public-dropped", "zz-public-dropped", 0)
	privateBuy := privateTestOrder("private-buy", "capital", 0, 8, "miner")
	privateSell := privateTestOrder("private-sell", "capital", 1, 8, "miner")

	batch := &interfaces.PrivateTxBatch{
		Transactions:      []*pb.AnyTx{privateBuy, privateSell},
		InsertAfterPublic: []int{1, 2},
	}
	merged, ok := mergePrivateTransactionBatch(
		[]*pb.AnyTx{publicSell, publicBuy, publicTail, publicDropped},
		batch,
		2,
		5,
	)
	if !ok {
		t.Fatal("interleaved private batch should fit")
	}
	if got, want := txIDs(merged), []string{"public-sell", "private-buy", "public-buy", "private-sell", "public-tail"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("interleaved order = %v, want %v", got, want)
	}
	if got := txIDs([]*pb.AnyTx{publicSell, publicBuy, publicTail, publicDropped}); !reflect.DeepEqual(got, []string{"public-sell", "public-buy", "public-tail", "public-dropped"}) {
		t.Fatalf("merge changed public input order: %v", got)
	}
}

func TestMergePrivateTransactionBatchRetainsInsertionFrontierFromFullPublicBody(t *testing.T) {
	public := []*pb.AnyTx{
		privateTestTransfer("public-a-full-scan", "a-full-scan", 0),
		privateTestTransfer("public-b-full-scan", "b-full-scan", 0),
		privateTestTransfer("public-c-full-scan", "c-full-scan", 0),
		privateTestTransfer("public-d-target", "d-full-scan", 0),
		privateTestTransfer("public-e-tail", "e-full-scan", 0),
		privateTestTransfer("public-f-tail", "f-full-scan", 0),
	}
	privateA := privateTestOrder("private-a-full-scan", "capital-full-scan", 0, 8, "miner")
	privateB := privateTestOrder("private-b-full-scan", "capital-full-scan", 1, 8, "miner")
	batch := &interfaces.PrivateTxBatch{
		Transactions:      []*pb.AnyTx{privateA, privateB},
		InsertAfterPublic: []int{1, 4},
	}

	merged, ok := mergePrivateTransactionBatch(public, batch, len(public), 6)
	if !ok {
		t.Fatal("full-public interleaving should fit by trimming only the suffix")
	}
	want := []string{
		"public-a-full-scan", "private-a-full-scan",
		"public-b-full-scan", "public-c-full-scan", "public-d-target", "private-b-full-scan",
	}
	if got := txIDs(merged); !reflect.DeepEqual(got, want) {
		t.Fatalf("full-public merge = %v, want %v", got, want)
	}

	tooLate := &interfaces.PrivateTxBatch{
		Transactions:      []*pb.AnyTx{privateA, privateB},
		InsertAfterPublic: []int{1, 5},
	}
	if unchanged, ok := mergePrivateTransactionBatch(public, tooLate, len(public), 6); ok {
		t.Fatal("candidate whose target frontier is in the capacity-trimmed suffix was accepted")
	} else if got := txIDs(unchanged); !reflect.DeepEqual(got, txIDs(public)) {
		t.Fatalf("rejected candidate changed public body: %v", got)
	}
}

func TestMergePrivateTransactionBatchRejectsProtocolCrossing(t *testing.T) {
	protocol := privateTestCert("cert-priority", "miner")
	public := privateTestTransfer("public", "alice", 0)
	privateBuy := privateTestOrder("private-buy", "capital", 0, 8, "miner")
	privateSell := privateTestOrder("private-sell", "capital", 1, 8, "miner")
	original := []*pb.AnyTx{protocol, public}

	merged, ok := mergePrivateTransactionBatch(original, &interfaces.PrivateTxBatch{
		Transactions:      []*pb.AnyTx{privateBuy, privateSell},
		InsertAfterPublic: []int{0, 2},
	}, 2, 4)
	if ok {
		t.Fatal("private transaction was allowed to precede a protocol-priority transaction")
	}
	if got, want := txIDs(merged), txIDs(original); !reflect.DeepEqual(got, want) {
		t.Fatalf("invalid insertion schedule changed normal body: got %v want %v", got, want)
	}
}

func TestRealBlockProposerPrivateBatchOrderAndClonePreflight(t *testing.T) {
	proposer, pool := newPrivateProposerHarness(t, 4)
	const (
		height        = uint64(11)
		proposerID    = "miner-private-success"
		privateSender = "capital-account"
	)
	protocol := privateTestCert("cert-priority-success", privateSender)
	normal := privateTestTransfer("normal-success", "alice", 0)
	storePrivateTestTx(t, pool, proposer.dbManager, normal)
	storePrivateTestTx(t, pool, proposer.dbManager, protocol)

	buy := privateTestOrder("private-buy-success", privateSender, 0, height, proposerID)
	sell := privateTestOrder("private-sell-success", privateSender, 1, height, proposerID)
	callOrder := make([]string, 0, 4)
	provider := &privateTxProviderProbe{
		limits:    interfaces.PrivateProposalLimits{MaxTransactions: 2, MaxOrdersPerBook: 9000},
		batch:     &interfaces.PrivateTxBatch{ID: "arb-success", Transactions: []*pb.AnyTx{buy, sell}},
		callOrder: &callOrder,
	}
	preflight := &proposalPreflightProbe{
		result:           successfulPrivateResult(buy, sell),
		mutate:           true,
		mutateProjection: true,
		callOrder:        &callOrder,
	}
	proposer.SetPrivateTxProvider(provider)
	proposer.SetProposalPreflight(preflight)

	block, err := proposer.ProposeBlock("parent-private-success", height, types.NodeID(proposerID), 0)
	if err != nil {
		t.Fatalf("ProposeBlock: %v", err)
	}
	t.Cleanup(func() { RemoveCachedBlock(block.ID) })
	cached, ok := GetCachedBlock(block.ID)
	if !ok || cached == nil {
		t.Fatal("final candidate was not cached")
	}
	if len(block.MinerSignature) == 0 || len(cached.MinerSignature) == 0 {
		t.Fatal("private candidate is missing proposer signature")
	}
	if got, want := txIDs(cached.Body), []string{protocol.GetTxId(), normal.GetTxId(), buy.GetTxId(), sell.GetTxId()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate body = %v, want %v", got, want)
	}
	if got, want := callOrder, []string{"limits", "project", "prepare", "simulate"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("private proposal call order = %v, want %v", got, want)
	}
	prefixHash := txpool.ComputeTxsHash([]*pb.AnyTx{protocol, normal})
	if provider.lastContext.PrivateTxLimit != 2 || provider.lastContext.PublicPrefixCount != 2 || provider.lastContext.PublicPrefixHash != prefixHash {
		t.Fatalf("private proposal context = %+v, want limit=2 prefix=2/%s", provider.lastContext, prefixHash)
	}
	if preflight.projectCalls != 1 || preflight.projectMaxOrders != maxProjectedOrdersPerBook {
		t.Fatalf("prefix projection calls/maxOrders = %d/%d, want 1/%d", preflight.projectCalls, preflight.projectMaxOrders, maxProjectedOrdersPerBook)
	}
	if preflight.projectTaker != privateSender {
		t.Fatalf("prefix projection taker = %q, want independent sender %q", preflight.projectTaker, privateSender)
	}
	if got, want := txIDs(preflight.projectSeen), []string{protocol.GetTxId(), normal.GetTxId()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("projected prefix = %v, want %v", got, want)
	}
	if len(preflight.projectSeen) != 2 || preflight.projectSeen[0] == protocol || preflight.projectSeen[1] == normal {
		t.Fatal("prefix projection did not receive detached transaction clones")
	}
	if provider.lastMarket == nil || provider.lastMarket.PublicPrefixCount != 2 || provider.lastMarket.PublicPrefixHash != prefixHash {
		t.Fatalf("Prepare market binding = %+v, want 2/%s", provider.lastMarket, prefixHash)
	}
	if provider.preparedBatch == nil || provider.preparedBatch.PublicPrefixCount != 2 || provider.preparedBatch.PublicPrefixHash != prefixHash {
		t.Fatalf("private batch binding = %+v, want 2/%s", provider.preparedBatch, prefixHash)
	}
	if preflight.calls != 1 || preflight.seen == nil || preflight.seen.BlockHash != block.ID {
		t.Fatalf("preflight did not receive final committed candidate: calls=%d seen=%v", preflight.calls, preflight.seen)
	}
	if provider.validateCalls != 1 {
		t.Fatalf("ValidateCandidate calls = %d, want 1", provider.validateCalls)
	}
	if protocol.GetBase().ExecutedHeight != 0 || normal.GetBase().ExecutedHeight != 0 || buy.GetBase().ExecutedHeight != 0 ||
		cached.Body[0].GetBase().ExecutedHeight != 0 || cached.Body[1].GetBase().ExecutedHeight != 0 || cached.Body[2].GetBase().ExecutedHeight != 0 {
		t.Fatal("prefix projection or full preflight mutated source transactions or the cached payload")
	}
	if err := ValidateBlockCommitment(cached); err != nil {
		t.Fatalf("final candidate commitment: %v", err)
	}
}

func TestRealBlockProposerValidationCannotMutateCommittedCandidate(t *testing.T) {
	proposer, pool := newPrivateProposerHarness(t, 4)
	const (
		height        = uint64(35)
		proposerID    = "miner-validation-clone"
		privateSender = "capital-validation-clone"
	)
	normal := privateTestTransfer("normal-validation-clone", "alice-validation-clone", 0)
	storePrivateTestTx(t, pool, proposer.dbManager, normal)
	buy := privateTestOrder("private-buy-validation-clone", privateSender, 0, height, proposerID)
	sell := privateTestOrder("private-sell-validation-clone", privateSender, 1, height, proposerID)
	provider := &privateTxProviderProbe{
		limits:          interfaces.PrivateProposalLimits{MaxTransactions: 2, MaxOrdersPerBook: 64},
		batch:           &interfaces.PrivateTxBatch{ID: "candidate-validation-clone", Transactions: []*pb.AnyTx{buy, sell}},
		mutateValidated: true,
	}
	preflight := &proposalPreflightProbe{result: successfulPrivateResult(buy, sell)}
	proposer.SetPrivateTxProvider(provider)
	proposer.SetProposalPreflight(preflight)

	block, err := proposer.ProposeBlock("parent-validation-clone", height, types.NodeID(proposerID), 0)
	if err != nil {
		t.Fatalf("ProposeBlock: %v", err)
	}
	t.Cleanup(func() { RemoveCachedBlock(block.ID) })
	cached, ok := GetCachedBlock(block.ID)
	if !ok || cached == nil {
		t.Fatal("validated candidate was not cached")
	}
	if got, want := txIDs(cached.Body), []string{normal.GetTxId(), buy.GetTxId(), sell.GetTxId()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("validated candidate body=%v, want=%v", got, want)
	}
	if cached.Body[1].GetBase().ExecutedHeight != 0 {
		t.Fatal("provider validation mutation escaped its detached batch clone")
	}
	if buy.GetBase().ExecutedHeight == 0 {
		t.Fatal("validation probe did not mutate its retained prepared transaction")
	}
	if err := ValidateBlockCommitment(cached); err != nil {
		t.Fatalf("candidate commitment after provider validation: %v", err)
	}
}

func TestRealBlockProposerInterleavesPrivateTransactionsBetweenPublicOrders(t *testing.T) {
	proposer, pool := newPrivateProposerHarness(t, 4)
	const (
		height        = uint64(31)
		proposerID    = "miner-private-interleaved"
		privateSender = "capital-interleaved"
	)
	publicSell := privateTestPublicOrder("public-sell-interleaved", "a-public-seller", pb.OrderSide_SELL)
	publicBuy := privateTestPublicOrder("public-buy-interleaved", "b-public-buyer", pb.OrderSide_BUY)
	storePrivateTestTx(t, pool, proposer.dbManager, publicBuy)
	storePrivateTestTx(t, pool, proposer.dbManager, publicSell)

	privateBuy := privateTestOrder("private-buy-interleaved", privateSender, 0, height, proposerID)
	privateBuy.GetOrderTx().Side = pb.OrderSide_BUY
	privateSell := privateTestOrder("private-sell-interleaved", privateSender, 1, height, proposerID)
	privateSell.GetOrderTx().Side = pb.OrderSide_SELL
	provider := &privateTxProviderProbe{
		limits: interfaces.PrivateProposalLimits{MaxTransactions: 2, MaxOrdersPerBook: 64},
		batch: &interfaces.PrivateTxBatch{
			ID:                "arb-interleaved",
			Transactions:      []*pb.AnyTx{privateBuy, privateSell},
			InsertAfterPublic: []int{1, 2},
		},
		mutatePublic: true,
	}
	preflight := &proposalPreflightProbe{
		result:           successfulPrivateResult(privateBuy, privateSell),
		mutateProjection: true,
	}
	proposer.SetPrivateTxProvider(provider)
	proposer.SetProposalPreflight(preflight)

	block, err := proposer.ProposeBlock("parent-private-interleaved", height, types.NodeID(proposerID), 0)
	if err != nil {
		t.Fatalf("ProposeBlock: %v", err)
	}
	t.Cleanup(func() { RemoveCachedBlock(block.ID) })
	cached, ok := GetCachedBlock(block.ID)
	if !ok || cached == nil {
		t.Fatal("interleaved candidate was not cached")
	}
	want := []string{
		publicSell.GetTxId(),
		privateBuy.GetTxId(),
		publicBuy.GetTxId(),
		privateSell.GetTxId(),
	}
	if got := txIDs(cached.Body); !reflect.DeepEqual(got, want) {
		t.Fatalf("interleaved candidate body = %v, want %v", got, want)
	}
	if got := txIDs(provider.lastPublic); !reflect.DeepEqual(got, []string{publicSell.GetTxId(), publicBuy.GetTxId()}) {
		t.Fatalf("provider ordered public = %v", got)
	}
	if len(provider.lastPublic) != 2 || provider.lastPublic[0] == publicSell || provider.lastPublic[1] == publicBuy {
		t.Fatal("provider did not receive detached public transaction clones")
	}
	if len(preflight.projectSeen) != 2 || provider.lastPublic[0] == preflight.projectSeen[0] || provider.lastPublic[1] == preflight.projectSeen[1] {
		t.Fatal("provider and VM projection shared mutable public transaction clones")
	}
	if publicSell.GetBase().ExecutedHeight != 0 || publicBuy.GetBase().ExecutedHeight != 0 ||
		cached.Body[0].GetBase().ExecutedHeight != 0 || cached.Body[2].GetBase().ExecutedHeight != 0 {
		t.Fatal("public inspection mutated source transactions or cached candidate")
	}
	if preflight.calls != 1 || provider.validateCalls != 1 {
		t.Fatalf("interleaved validation calls: simulate=%d validate=%d", preflight.calls, provider.validateCalls)
	}
}

func TestRealBlockProposerReservesPrivateCapacityAndTrimsOnlySuffix(t *testing.T) {
	proposer, pool := newPrivateProposerHarness(t, 6)
	const (
		height        = uint64(41)
		proposerID    = "miner-private-full-public-scan"
		privateSender = "capital-private-full-public-scan"
	)
	public := []*pb.AnyTx{
		privateTestTransfer("public-a-proposal-scan", "a-proposal-scan", 0),
		privateTestTransfer("public-b-proposal-scan", "b-proposal-scan", 0),
		privateTestTransfer("public-c-proposal-scan", "c-proposal-scan", 0),
		privateTestTransfer("public-d-proposal-target", "d-proposal-scan", 0),
		privateTestTransfer("public-e-proposal-tail", "e-proposal-scan", 0),
		privateTestTransfer("public-f-proposal-tail", "f-proposal-scan", 0),
	}
	for _, tx := range public {
		storePrivateTestTx(t, pool, proposer.dbManager, tx)
	}
	privateA := privateTestOrder("private-a-proposal-scan", privateSender, 0, height, proposerID)
	privateB := privateTestOrder("private-b-proposal-scan", privateSender, 1, height, proposerID)
	provider := &privateTxProviderProbe{
		sender: privateSender,
		limits: interfaces.PrivateProposalLimits{MaxTransactions: 2, MaxOrdersPerBook: 64},
		batch: &interfaces.PrivateTxBatch{
			ID:                "full-public-scan",
			Transactions:      []*pb.AnyTx{privateA, privateB},
			InsertAfterPublic: []int{1, 4},
		},
	}
	preflight := &proposalPreflightProbe{result: successfulPrivateResult(privateA, privateB)}
	proposer.SetPrivateTxProvider(provider)
	proposer.SetProposalPreflight(preflight)

	block, err := proposer.ProposeBlock("parent-private-full-public-scan", height, types.NodeID(proposerID), 0)
	if err != nil {
		t.Fatalf("ProposeBlock: %v", err)
	}
	t.Cleanup(func() { RemoveCachedBlock(block.ID) })
	cached, ok := GetCachedBlock(block.ID)
	if !ok || cached == nil {
		t.Fatal("full-public-scan candidate was not cached")
	}
	wantPrefixCount := len(public) - 2
	if provider.lastContext.PublicPrefixCount != wantPrefixCount || len(provider.lastPublic) != wantPrefixCount || len(preflight.projectSeen) != wantPrefixCount {
		t.Fatalf(
			"reserved public prefix: ctx=%d prepare=%d project=%d want=%d",
			provider.lastContext.PublicPrefixCount, len(provider.lastPublic), len(preflight.projectSeen), wantPrefixCount,
		)
	}
	want := []string{
		"public-a-proposal-scan", "private-a-proposal-scan",
		"public-b-proposal-scan", "public-c-proposal-scan", "public-d-proposal-target", "private-b-proposal-scan",
	}
	if got := txIDs(cached.Body); !reflect.DeepEqual(got, want) {
		t.Fatalf("full-public-scan body = %v, want %v", got, want)
	}
	if preflight.calls != 1 || provider.validateCalls != 1 {
		t.Fatalf("full-public-scan validation calls: simulate=%d validate=%d", preflight.calls, provider.validateCalls)
	}
}

func TestRealBlockProposerReservesOneHundredPrivateSlots(t *testing.T) {
	const (
		blockLimit    = 105
		height        = uint64(43)
		proposerID    = "miner-private-reserve-one-hundred"
		privateSender = "capital-private-reserve-one-hundred"
	)
	proposer, pool := newPrivateProposerHarness(t, blockLimit)
	for i := 0; i < blockLimit; i++ {
		storePrivateTestTx(t, pool, proposer.dbManager, privateTestTransfer(
			fmt.Sprintf("public-reserve-%03d", i),
			fmt.Sprintf("public-reserve-sender-%03d", i),
			0,
		))
	}
	privateA := privateTestOrder("private-reserve-a", privateSender, 0, height, proposerID)
	privateB := privateTestOrder("private-reserve-b", privateSender, 1, height, proposerID)
	provider := &privateTxProviderProbe{
		sender: privateSender,
		limits: interfaces.PrivateProposalLimits{MaxTransactions: 4096, MaxOrdersPerBook: 64},
		batch: &interfaces.PrivateTxBatch{
			ID:           "reserve-one-hundred",
			Transactions: []*pb.AnyTx{privateA, privateB},
		},
	}
	preflight := &proposalPreflightProbe{result: successfulPrivateResult(privateA, privateB)}
	proposer.SetPrivateTxProvider(provider)
	proposer.SetProposalPreflight(preflight)

	block, err := proposer.ProposeBlock("parent-private-reserve-one-hundred", height, types.NodeID(proposerID), 0)
	if err != nil {
		t.Fatalf("ProposeBlock: %v", err)
	}
	t.Cleanup(func() { RemoveCachedBlock(block.ID) })
	if provider.lastContext.PrivateTxLimit != reservedPrivateTransactionSlots {
		t.Fatalf("private limit=%d, want reserved %d", provider.lastContext.PrivateTxLimit, reservedPrivateTransactionSlots)
	}
	wantPrefix := blockLimit - reservedPrivateTransactionSlots
	if provider.lastContext.PublicPrefixCount != wantPrefix || len(provider.lastPublic) != wantPrefix || len(preflight.projectSeen) != wantPrefix {
		t.Fatalf(
			"reserved prefix: ctx=%d prepare=%d project=%d want=%d",
			provider.lastContext.PublicPrefixCount, len(provider.lastPublic), len(preflight.projectSeen), wantPrefix,
		)
	}
	cached, ok := GetCachedBlock(block.ID)
	if !ok || cached == nil {
		t.Fatal("reserved private candidate was not cached")
	}
	if len(cached.Body) != blockLimit {
		t.Fatalf("final body size=%d, want full block size %d", len(cached.Body), blockLimit)
	}
}

func TestRealBlockProposerAcceptsPrivateCandidateLargerThanFormerFixedCap(t *testing.T) {
	const privateCount = 18
	proposer, _ := newPrivateProposerHarness(t, privateCount)
	const (
		height        = uint64(42)
		proposerID    = "miner-private-large-candidate"
		privateSender = "capital-private-large-candidate"
	)
	privateTxs := make([]*pb.AnyTx, 0, privateCount)
	insertions := make([]int, 0, privateCount)
	for i := 0; i < privateCount; i++ {
		privateTxs = append(privateTxs, privateTestOrder(
			fmt.Sprintf("private-large-%02d", i), privateSender, uint64(i), height, proposerID,
		))
		insertions = append(insertions, 0)
	}
	provider := &privateTxProviderProbe{
		sender: privateSender,
		limits: interfaces.PrivateProposalLimits{MaxTransactions: privateCount, MaxOrdersPerBook: 64},
		batch: &interfaces.PrivateTxBatch{
			ID:                "large-private-candidate",
			Transactions:      privateTxs,
			InsertAfterPublic: insertions,
		},
	}
	preflight := &proposalPreflightProbe{result: successfulPrivateResult(privateTxs...)}
	proposer.SetPrivateTxProvider(provider)
	proposer.SetProposalPreflight(preflight)

	block, err := proposer.ProposeBlock("parent-private-large-candidate", height, types.NodeID(proposerID), 0)
	if err != nil {
		t.Fatalf("ProposeBlock: %v", err)
	}
	t.Cleanup(func() { RemoveCachedBlock(block.ID) })
	cached, ok := GetCachedBlock(block.ID)
	if !ok || cached == nil {
		t.Fatal("large private candidate was not cached")
	}
	if provider.lastContext.PrivateTxLimit != privateCount {
		t.Fatalf("private candidate limit=%d, want %d", provider.lastContext.PrivateTxLimit, privateCount)
	}
	if got := txIDs(cached.Body); !reflect.DeepEqual(got, txIDs(privateTxs)) {
		t.Fatalf("large private candidate body has %d transactions, want %d", len(got), privateCount)
	}
	if preflight.calls != 1 || provider.validateCalls != 1 {
		t.Fatalf("large candidate validation calls: simulate=%d validate=%d", preflight.calls, provider.validateCalls)
	}
}

func TestRealBlockProposerInvalidPrivateInsertionScheduleFallsBack(t *testing.T) {
	proposer, pool := newPrivateProposerHarness(t, 4)
	const (
		height     = uint64(32)
		proposerID = "miner-invalid-private-schedule"
	)
	publicSell := privateTestPublicOrder("public-sell-invalid-schedule", "a-public-seller-invalid", pb.OrderSide_SELL)
	publicBuy := privateTestPublicOrder("public-buy-invalid-schedule", "b-public-buyer-invalid", pb.OrderSide_BUY)
	storePrivateTestTx(t, pool, proposer.dbManager, publicSell)
	storePrivateTestTx(t, pool, proposer.dbManager, publicBuy)
	privateBuy := privateTestOrder("private-buy-invalid-schedule", "capital-invalid-schedule", 0, height, proposerID)
	privateSell := privateTestOrder("private-sell-invalid-schedule", "capital-invalid-schedule", 1, height, proposerID)
	provider := &privateTxProviderProbe{
		limits: interfaces.PrivateProposalLimits{MaxTransactions: 2, MaxOrdersPerBook: 64},
		batch: &interfaces.PrivateTxBatch{
			ID:                "arb-invalid-schedule",
			Transactions:      []*pb.AnyTx{privateBuy, privateSell},
			InsertAfterPublic: []int{2, 1},
		},
	}
	preflight := &proposalPreflightProbe{result: successfulPrivateResult(privateBuy, privateSell)}
	proposer.SetPrivateTxProvider(provider)
	proposer.SetProposalPreflight(preflight)

	block, err := proposer.ProposeBlock("parent-invalid-private-schedule", height, types.NodeID(proposerID), 0)
	if err != nil {
		t.Fatalf("ProposeBlock: %v", err)
	}
	t.Cleanup(func() { RemoveCachedBlock(block.ID) })
	cached, ok := GetCachedBlock(block.ID)
	if !ok || cached == nil {
		t.Fatal("ordinary fallback candidate was not cached")
	}
	if got, want := txIDs(cached.Body), []string{publicSell.GetTxId(), publicBuy.GetTxId()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("invalid schedule fallback body = %v, want %v", got, want)
	}
	if preflight.calls != 0 || provider.validateCalls != 0 {
		t.Fatalf("invalid schedule reached candidate validation: simulate=%d validate=%d", preflight.calls, provider.validateCalls)
	}
}

func TestRealBlockProposerFallsBackAfterSinglePrivatePreflightFailure(t *testing.T) {
	proposer, pool := newPrivateProposerHarness(t, 4)
	const (
		height        = uint64(33)
		proposerID    = "miner-private-candidate-fallback"
		privateSender = "capital-private-candidate-fallback"
	)
	publicSell := privateTestPublicOrder("public-sell-candidate-fallback", "a-public-seller-candidate", pb.OrderSide_SELL)
	publicBuy := privateTestPublicOrder("public-buy-candidate-fallback", "b-public-buyer-candidate", pb.OrderSide_BUY)
	storePrivateTestTx(t, pool, proposer.dbManager, publicSell)
	storePrivateTestTx(t, pool, proposer.dbManager, publicBuy)

	firstBuy := privateTestOrder("private-first-buy", privateSender, 0, height, proposerID)
	firstSell := privateTestOrder("private-first-sell", privateSender, 1, height, proposerID)
	secondBuy := privateTestOrder("private-second-buy", privateSender, 0, height, proposerID)
	secondSell := privateTestOrder("private-second-sell", privateSender, 1, height, proposerID)
	callOrder := make([]string, 0, 6)
	baseProvider := &privateTxProviderProbe{
		sender:    privateSender,
		limits:    interfaces.PrivateProposalLimits{MaxTransactions: 2, MaxOrdersPerBook: 64},
		callOrder: &callOrder,
	}
	provider := &privateTxCandidateProviderProbe{
		privateTxProviderProbe: baseProvider,
		candidates: []*interfaces.PrivateTxBatch{
			{
				ID:                "candidate-first",
				Transactions:      []*pb.AnyTx{firstBuy, firstSell},
				InsertAfterPublic: []int{1, 2},
			},
			{
				ID:                "candidate-second",
				Transactions:      []*pb.AnyTx{secondBuy, secondSell},
				InsertAfterPublic: []int{1, 2},
			},
		},
	}
	preflight := &proposalPreflightProbe{
		results: []*interfaces.ProposalSimulationResult{
			{
				Valid: true,
				Receipts: []interfaces.ProposalSimulationReceipt{
					{TxID: firstBuy.GetTxId(), Status: "SUCCEED"},
					{TxID: firstSell.GetTxId(), Status: "FAILED", Error: "maker disappeared"},
				},
			},
			successfulPrivateResult(secondBuy, secondSell),
		},
		callOrder: &callOrder,
	}
	proposer.SetPrivateTxProvider(provider)
	proposer.SetProposalPreflight(preflight)

	block, err := proposer.ProposeBlock("parent-private-candidate-fallback", height, types.NodeID(proposerID), 0)
	if err != nil {
		t.Fatalf("ProposeBlock: %v", err)
	}
	t.Cleanup(func() { RemoveCachedBlock(block.ID) })
	cached, ok := GetCachedBlock(block.ID)
	if !ok || cached == nil {
		t.Fatal("ordinary fallback candidate was not cached")
	}
	want := []string{publicSell.GetTxId(), publicBuy.GetTxId()}
	if got := txIDs(cached.Body); !reflect.DeepEqual(got, want) {
		t.Fatalf("single-preflight fallback body = %v, want %v", got, want)
	}
	if provider.candidateCalls != 1 || provider.prepareCalls != 0 {
		t.Fatalf("candidate/legacy prepare calls = %d/%d, want 1/0", provider.candidateCalls, provider.prepareCalls)
	}
	if preflight.calls != 1 || len(preflight.seenCandidates) != 1 {
		t.Fatalf("candidate preflight calls = %d/%d, want 1/1", preflight.calls, len(preflight.seenCandidates))
	}
	if len(provider.validatedIDs) != 0 {
		t.Fatalf("failed strategy candidate unexpectedly validated: %v", provider.validatedIDs)
	}
	if got, want := callOrder, []string{"limits", "project", "prepareCandidates", "simulate"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate call order = %v, want %v", got, want)
	}
	if _, exists := GetCachedBlock(preflight.seenCandidates[0].BlockHash); exists {
		t.Fatal("failed first candidate leaked into the block cache")
	}
	if err := ValidateBlockCommitment(cached); err != nil {
		t.Fatalf("successful alternative commitment: %v", err)
	}
}

func TestRealBlockProposerSkipsInvalidPrivateCandidateAndTriesNext(t *testing.T) {
	proposer, pool := newPrivateProposerHarness(t, 4)
	const (
		height        = uint64(34)
		proposerID    = "miner-private-invalid-candidate"
		privateSender = "capital-private-invalid-candidate"
	)
	publicSell := privateTestPublicOrder("public-sell-invalid-candidate", "a-public-seller-invalid-candidate", pb.OrderSide_SELL)
	publicBuy := privateTestPublicOrder("public-buy-invalid-candidate", "b-public-buyer-invalid-candidate", pb.OrderSide_BUY)
	storePrivateTestTx(t, pool, proposer.dbManager, publicSell)
	storePrivateTestTx(t, pool, proposer.dbManager, publicBuy)
	invalidBuy := privateTestOrder("private-invalid-buy-candidate", privateSender, 0, height, proposerID)
	invalidSell := privateTestOrder("private-invalid-sell-candidate", privateSender, 1, height, proposerID)
	validBuy := privateTestOrder("private-valid-buy-candidate", privateSender, 0, height, proposerID)
	validSell := privateTestOrder("private-valid-sell-candidate", privateSender, 1, height, proposerID)
	baseProvider := &privateTxProviderProbe{
		sender: privateSender,
		limits: interfaces.PrivateProposalLimits{MaxTransactions: 2, MaxOrdersPerBook: 64},
	}
	provider := &privateTxCandidateProviderProbe{
		privateTxProviderProbe: baseProvider,
		candidates: []*interfaces.PrivateTxBatch{
			{
				ID:                "candidate-invalid",
				Transactions:      []*pb.AnyTx{invalidBuy, invalidSell},
				InsertAfterPublic: []int{2, 1},
			},
			{
				ID:                "candidate-valid",
				Transactions:      []*pb.AnyTx{validBuy, validSell},
				InsertAfterPublic: []int{1, 2},
			},
		},
	}
	preflight := &proposalPreflightProbe{result: successfulPrivateResult(validBuy, validSell)}
	proposer.SetPrivateTxProvider(provider)
	proposer.SetProposalPreflight(preflight)

	block, err := proposer.ProposeBlock("parent-private-invalid-candidate", height, types.NodeID(proposerID), 0)
	if err != nil {
		t.Fatalf("ProposeBlock: %v", err)
	}
	t.Cleanup(func() { RemoveCachedBlock(block.ID) })
	cached, ok := GetCachedBlock(block.ID)
	if !ok || cached == nil {
		t.Fatal("candidate after invalid alternative was not cached")
	}
	want := []string{publicSell.GetTxId(), validBuy.GetTxId(), publicBuy.GetTxId(), validSell.GetTxId()}
	if got := txIDs(cached.Body); !reflect.DeepEqual(got, want) {
		t.Fatalf("invalid candidate skip body = %v, want %v", got, want)
	}
	if preflight.calls != 1 || provider.validateCalls != 1 {
		t.Fatalf("invalid candidate consumed preflight work: simulate=%d validate=%d", preflight.calls, provider.validateCalls)
	}
	if got, want := provider.validatedIDs, []string{"candidate-valid"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("validated candidates = %v, want %v", got, want)
	}
}

func TestRealBlockProposerCapsPrivateCandidatePreflightAttempts(t *testing.T) {
	proposer, pool := newPrivateProposerHarness(t, 4)
	const (
		height        = uint64(35)
		proposerID    = "miner-private-candidate-cap"
		privateSender = "capital-private-candidate-cap"
	)
	public := privateTestTransfer("public-private-candidate-cap", "public-owner-private-candidate-cap", 0)
	storePrivateTestTx(t, pool, proposer.dbManager, public)
	privateBuy := privateTestOrder("private-buy-candidate-cap", privateSender, 0, height, proposerID)
	privateSell := privateTestOrder("private-sell-candidate-cap", privateSender, 1, height, proposerID)
	batch := &interfaces.PrivateTxBatch{
		ID:           "candidate-cap",
		Transactions: []*pb.AnyTx{privateBuy, privateSell},
	}
	baseProvider := &privateTxProviderProbe{
		sender: privateSender,
		limits: interfaces.PrivateProposalLimits{MaxTransactions: 2, MaxOrdersPerBook: 64},
	}
	provider := &privateTxCandidateProviderProbe{
		privateTxProviderProbe: baseProvider,
		candidates: []*interfaces.PrivateTxBatch{
			batch, batch, batch, batch, batch,
		},
	}
	preflight := &proposalPreflightProbe{result: &interfaces.ProposalSimulationResult{Valid: false, Reason: "reject all"}}
	proposer.SetPrivateTxProvider(provider)
	proposer.SetProposalPreflight(preflight)

	block, err := proposer.ProposeBlock("parent-private-candidate-cap", height, types.NodeID(proposerID), 0)
	if err != nil {
		t.Fatalf("ProposeBlock: %v", err)
	}
	t.Cleanup(func() { RemoveCachedBlock(block.ID) })
	cached, ok := GetCachedBlock(block.ID)
	if !ok || cached == nil {
		t.Fatal("ordinary fallback candidate was not cached")
	}
	if got, want := txIDs(cached.Body), []string{public.GetTxId()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate cap fallback body = %v, want %v", got, want)
	}
	if preflight.calls != maxPrivatePreflightAttemptsPerProposal {
		t.Fatalf("private candidate preflight attempts = %d, want cap %d", preflight.calls, maxPrivatePreflightAttemptsPerProposal)
	}
}

func TestRealBlockProposerRejectsBatchSenderDifferentFromProviderIdentity(t *testing.T) {
	proposer, pool := newPrivateProposerHarness(t, 4)
	const (
		height     = uint64(15)
		proposerID = "miner-provider-sender"
	)
	normal := privateTestTransfer("normal-provider-sender", "alice-provider-sender", 0)
	storePrivateTestTx(t, pool, proposer.dbManager, normal)
	buy := privateTestOrder("private-buy-provider-sender", "unexpected-capital", 0, height, proposerID)
	sell := privateTestOrder("private-sell-provider-sender", "unexpected-capital", 1, height, proposerID)
	provider := &privateTxProviderProbe{
		sender: "declared-capital",
		limits: interfaces.PrivateProposalLimits{MaxTransactions: 2, MaxOrdersPerBook: 64},
		batch:  &interfaces.PrivateTxBatch{ID: "arb-provider-sender", Transactions: []*pb.AnyTx{buy, sell}},
	}
	preflight := &proposalPreflightProbe{result: successfulPrivateResult(buy, sell)}
	proposer.SetPrivateTxProvider(provider)
	proposer.SetProposalPreflight(preflight)

	block, err := proposer.ProposeBlock("parent-provider-sender", height, types.NodeID(proposerID), 0)
	if err != nil {
		t.Fatalf("ProposeBlock: %v", err)
	}
	t.Cleanup(func() { RemoveCachedBlock(block.ID) })
	cached, ok := GetCachedBlock(block.ID)
	if !ok || cached == nil {
		t.Fatal("fallback candidate was not cached")
	}
	if got, want := txIDs(cached.Body), []string{normal.GetTxId()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("provider sender mismatch body = %v, want ordinary body %v", got, want)
	}
	if preflight.projectTaker != "declared-capital" || preflight.calls != 0 || provider.validateCalls != 0 {
		t.Fatalf("provider sender mismatch calls: taker=%q simulate=%d validate=%d", preflight.projectTaker, preflight.calls, provider.validateCalls)
	}
}

func TestRealBlockProposerSkipsPrivateBatchWhenSenderHasOverlappingPublicNonceTx(t *testing.T) {
	proposer, pool := newPrivateProposerHarness(t, 4)
	const (
		height        = uint64(12)
		proposerID    = "miner-private-conflict"
		privateSender = "capital-conflict"
	)
	protocol := privateTestCert("cert-priority-conflict", privateSender)
	public := privateTestTransfer("public-capital-conflict", privateSender, 0)
	storePrivateTestTx(t, pool, proposer.dbManager, public)
	storePrivateTestTx(t, pool, proposer.dbManager, protocol)

	buy := privateTestOrder("private-buy-conflict", privateSender, 0, height, proposerID)
	sell := privateTestOrder("private-sell-conflict", privateSender, 1, height, proposerID)
	provider := &privateTxProviderProbe{
		limits: interfaces.PrivateProposalLimits{MaxTransactions: 2, MaxOrdersPerBook: 64},
		batch:  &interfaces.PrivateTxBatch{ID: "arb-conflict", Transactions: []*pb.AnyTx{buy, sell}},
	}
	preflight := &proposalPreflightProbe{result: successfulPrivateResult(buy, sell)}
	proposer.SetPrivateTxProvider(provider)
	proposer.SetProposalPreflight(preflight)

	block, err := proposer.ProposeBlock("parent-private-conflict", height, types.NodeID(proposerID), 0)
	if err != nil {
		t.Fatalf("ProposeBlock: %v", err)
	}
	t.Cleanup(func() { RemoveCachedBlock(block.ID) })
	cached, _ := GetCachedBlock(block.ID)
	if got, want := txIDs(cached.Body), []string{protocol.GetTxId(), public.GetTxId()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("conflict fallback body = %v, want untouched normal body %v", got, want)
	}
	if preflight.projectCalls != 1 || provider.prepareCalls != 1 || preflight.calls != 0 || provider.validateCalls != 0 {
		t.Fatalf("conflicting private batch calls: project=%d prepare=%d simulate=%d validate=%d", preflight.projectCalls, provider.prepareCalls, preflight.calls, provider.validateCalls)
	}
}

func TestRealBlockProposerRejectsStalePrivatePrefixBinding(t *testing.T) {
	proposer, pool := newPrivateProposerHarness(t, 4)
	const (
		height     = uint64(14)
		proposerID = "miner-private-stale-prefix"
	)
	normal := privateTestTransfer("normal-stale-prefix", "alice-stale-prefix", 0)
	storePrivateTestTx(t, pool, proposer.dbManager, normal)
	buy := privateTestOrder("private-buy-stale-prefix", "capital-stale-prefix", 0, height, proposerID)
	sell := privateTestOrder("private-sell-stale-prefix", "capital-stale-prefix", 1, height, proposerID)
	provider := &privateTxProviderProbe{
		limits: interfaces.PrivateProposalLimits{MaxTransactions: 2, MaxOrdersPerBook: 64},
		batch: &interfaces.PrivateTxBatch{
			ID:                "arb-stale-prefix",
			Transactions:      []*pb.AnyTx{buy, sell},
			PublicPrefixHash:  "stale-prefix-hash",
			PublicPrefixCount: 1,
		},
	}
	preflight := &proposalPreflightProbe{result: successfulPrivateResult(buy, sell)}
	proposer.SetPrivateTxProvider(provider)
	proposer.SetProposalPreflight(preflight)

	block, err := proposer.ProposeBlock("parent-stale-prefix", height, types.NodeID(proposerID), 0)
	if err != nil {
		t.Fatalf("ProposeBlock: %v", err)
	}
	t.Cleanup(func() { RemoveCachedBlock(block.ID) })
	cached, ok := GetCachedBlock(block.ID)
	if !ok || cached == nil {
		t.Fatal("fallback candidate was not cached")
	}
	if got, want := txIDs(cached.Body), []string{normal.GetTxId()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stale-binding fallback body = %v, want %v", got, want)
	}
	if preflight.projectCalls != 1 || provider.prepareCalls != 1 || preflight.calls != 0 || provider.validateCalls != 0 {
		t.Fatalf("stale binding calls: project=%d prepare=%d simulate=%d validate=%d", preflight.projectCalls, provider.prepareCalls, preflight.calls, provider.validateCalls)
	}
}

func TestHasPublicNonceConflictOnlyCoversPrivateNonceRange(t *testing.T) {
	privateA := privateTestOrder("private-range-a", "capital-range", 5, 12, "miner-range")
	privateB := privateTestOrder("private-range-b", "capital-range", 6, 12, "miner-range")
	batch := &interfaces.PrivateTxBatch{Transactions: []*pb.AnyTx{privateA, privateB}}

	for _, tx := range []*pb.AnyTx{
		privateTestTransfer("stale-before-range", "capital-range", 4),
		privateTestTransfer("next-after-range", "capital-range", 7),
		privateTestTransfer("far-future-range", "capital-range", 20),
		privateTestTransfer("other-sender-range", "other-capital", 5),
		privateTestCert("nonce-exempt-range", "capital-range"),
	} {
		if hasPublicNonceConflict([]*pb.AnyTx{tx}, batch) {
			t.Fatalf("non-overlapping tx %s incorrectly blocked private batch", tx.GetTxId())
		}
	}
	for _, tx := range []*pb.AnyTx{
		privateTestTransfer("overlap-first-range", "capital-range", 5),
		privateTestTransfer("overlap-last-range", "capital-range", 6),
	} {
		if !hasPublicNonceConflict([]*pb.AnyTx{tx}, batch) {
			t.Fatalf("overlapping tx %s did not block private batch", tx.GetTxId())
		}
	}
}

func TestRealBlockProposerFailedPrivatePreflightRebuildsFullNormalCandidate(t *testing.T) {
	proposer, pool := newPrivateProposerHarness(t, 4)
	const (
		height        = uint64(13)
		proposerID    = "miner-private-fallback"
		privateSender = "capital-fallback"
	)
	protocol := privateTestCert("cert-priority-fallback", privateSender)
	normalA := privateTestTransfer("normal-a-fallback", "alice", 0)
	normalB := privateTestTransfer("normal-b-fallback", "bob", 0)
	for _, tx := range []*pb.AnyTx{normalB, protocol, normalA} {
		storePrivateTestTx(t, pool, proposer.dbManager, tx)
	}

	buy := privateTestOrder("private-buy-fallback", privateSender, 0, height, proposerID)
	sell := privateTestOrder("private-sell-fallback", privateSender, 1, height, proposerID)
	provider := &privateTxProviderProbe{
		limits: interfaces.PrivateProposalLimits{MaxTransactions: 2, MaxOrdersPerBook: 64},
		batch: &interfaces.PrivateTxBatch{
			ID:                "arb-fallback",
			Transactions:      []*pb.AnyTx{buy, sell},
			InsertAfterPublic: []int{2, 2},
		},
	}
	preflight := &proposalPreflightProbe{result: &interfaces.ProposalSimulationResult{
		Valid: true,
		Receipts: []interfaces.ProposalSimulationReceipt{
			{TxID: buy.GetTxId(), Status: "SUCCEED"},
			{TxID: sell.GetTxId(), Status: "FAILED", Error: "not filled"},
		},
	}}
	proposer.SetPrivateTxProvider(provider)
	proposer.SetProposalPreflight(preflight)

	block, err := proposer.ProposeBlock("parent-private-fallback", height, types.NodeID(proposerID), 0)
	if err != nil {
		t.Fatalf("ProposeBlock: %v", err)
	}
	t.Cleanup(func() { RemoveCachedBlock(block.ID) })
	cached, _ := GetCachedBlock(block.ID)
	want := []string{protocol.GetTxId(), normalA.GetTxId(), normalB.GetTxId()}
	if got := txIDs(cached.Body); !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback body = %v, want full normal body %v", got, want)
	}
	if preflight.seen == nil || preflight.seen.BlockHash == block.ID {
		t.Fatal("fallback must recompute a different commitment after removing the private batch")
	}
	if _, exists := GetCachedBlock(preflight.seen.BlockHash); exists {
		t.Fatal("failed private candidate leaked into the block cache")
	}
	if provider.validateCalls != 0 {
		t.Fatal("strategy validation should not run after a generic private receipt failure")
	}
	if err := ValidateBlockCommitment(cached); err != nil {
		t.Fatalf("fallback commitment: %v", err)
	}
}

func TestRealBlockProposerQueriesPrivateAvailabilityOnlyAfterEligibility(t *testing.T) {
	proposer, _ := newPrivateProposerHarness(t, 4)
	provider := &privateTxProviderProbe{hasPending: true}
	proposer.SetPrivateTxProvider(provider)
	lastBlockTime := time.Now()

	if proposer.ShouldPropose("99", 0, 0, 0, 1, lastBlockTime, "genesis") {
		t.Fatal("ineligible node unexpectedly proposed")
	}
	if provider.hasCalls != 0 {
		t.Fatalf("ineligible node queried private provider %d times", provider.hasCalls)
	}
	if !proposer.ShouldPropose("1", 0, 0, 0, 1, lastBlockTime, "genesis") {
		t.Fatal("eligible node with a private opportunity should bypass early-window empty-pool return")
	}
	if provider.hasCalls != 1 {
		t.Fatalf("eligible empty-pool node queried provider %d times, want 1", provider.hasCalls)
	}
	if got := fmt.Sprintf("%s/%d", provider.lastContext.ParentID, provider.lastContext.Height); got != "genesis/1" {
		t.Fatalf("private availability context = %s", got)
	}
}

func TestRealBlockProposerFiltersExecutionGuardsWithoutCrossingNonceGap(t *testing.T) {
	proposer, pool := newPrivateProposerHarness(t, 4)
	const height = uint64(20)

	expired := privateTestTransfer("expired-guard", "expired-owner", 0)
	expired.GetBase().TargetHeight = height - 1
	storePrivateTestTx(t, pool, proposer.dbManager, expired)
	if live := proposer.pruneExpiredProposalCandidates([]*pb.AnyTx{expired}, height); len(live) != 0 {
		t.Fatalf("expired candidate survived pruning: %v", txIDs(live))
	}
	if pool.HasTransaction(expired.GetTxId()) {
		t.Fatal("expired target-height transaction remained in the public pool")
	}

	future := privateTestTransfer("future-guard", "future-owner", 0)
	future.GetBase().TargetHeight = height + 1
	futureSuccessor := privateTestTransfer("future-successor", "future-owner", 1)
	eligible := filterProposalPoolCandidates([]*pb.AnyTx{future, futureSuccessor}, height, "miner-a")
	if got := txIDs(eligible); !reflect.DeepEqual(got, []string{futureSuccessor.GetTxId()}) {
		t.Fatalf("future guard filter = %v", got)
	}
	if selected := proposer.selectNonceReadyTxs(eligible, 4); len(selected) != 0 {
		t.Fatalf("nonce successor crossed future guarded nonce: %v", txIDs(selected))
	}

	wrongProposer := privateTestTransfer("wrong-proposer-guard", "proposer-owner", 0)
	wrongProposer.GetBase().OnlyProposer = "miner-b"
	wrongSuccessor := privateTestTransfer("wrong-proposer-successor", "proposer-owner", 1)
	eligible = filterProposalPoolCandidates([]*pb.AnyTx{wrongProposer, wrongSuccessor}, height, "miner-a")
	if selected := proposer.selectNonceReadyTxs(eligible, 4); len(selected) != 0 {
		t.Fatalf("nonce successor crossed proposer-guarded nonce: %v", txIDs(selected))
	}
}
