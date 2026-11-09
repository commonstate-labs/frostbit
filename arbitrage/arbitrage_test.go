package arbitrage

import (
	"dex/interfaces"
	"dex/keys"
	"dex/pb"
	"dex/types"
	"encoding/json"
	"math"
	"math/big"
	"reflect"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"
)

const testArbitrageAccount = "arbitrage-account"

type memoryState struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newMemoryState() *memoryState { return &memoryState{data: make(map[string][]byte)} }

func (s *memoryState) Get(key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]byte(nil), s.data[key]...), nil
}

func (s *memoryState) putProto(t *testing.T, key string, message proto.Message) {
	t.Helper()
	data, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.data[key] = data
	s.mu.Unlock()
}

func seedAccount(t *testing.T, state *memoryState, address string, nonce uint64) {
	t.Helper()
	state.putProto(t, keys.KeyAccount(address), &pb.Account{Address: address, Nonce: nonce})
}

func seedBalance(t *testing.T, state *memoryState, address, token, available string) {
	t.Helper()
	state.putProto(t, keys.KeyBalance(address, token), &pb.TokenBalanceRecord{
		Address: address,
		Token:   token,
		Balance: &pb.TokenBalance{Balance: available},
	})
}

func testSigner(counter *int) SignFunc {
	return func(tx *pb.AnyTx) (string, error) {
		(*counter)++
		txID := tx.GenerateTxID()
		tx.GetBase().TxId = txID
		tx.GetBase().Signature = []byte("local-test-signature")
		return txID, nil
	}
}

func proposalContext(hash string, prefixCount, privateLimit int) interfaces.ProposalContext {
	return interfaces.ProposalContext{
		ParentID:          "parent",
		Height:            11,
		Proposer:          types.NodeID("miner"),
		Window:            1,
		MaxTxs:            8,
		PrivateTxLimit:    privateLimit,
		PublicPrefixCount: prefixCount,
		PublicPrefixHash:  hash,
	}
}

func spotLevel(base, quote string, side pb.OrderSide, price, orderID, owner, remaining string) interfaces.ProposalSpotLevel {
	return interfaces.ProposalSpotLevel{
		BaseToken:  base,
		QuoteToken: quote,
		TakerSide:  side,
		Price:      price,
		Makers: []interfaces.ProposalSpotMaker{{
			OrderID: orderID, Owner: owner, Remaining: remaining,
		}},
	}
}

func proposalMarket(ctx interfaces.ProposalContext, levels ...interfaces.ProposalSpotLevel) *interfaces.ProposalSpotMarket {
	return &interfaces.ProposalSpotMarket{
		PublicPrefixHash:  ctx.PublicPrefixHash,
		PublicPrefixCount: ctx.PublicPrefixCount,
		Levels:            levels,
	}
}

func spotMatch(orderID, quantity, price string) interfaces.ProposalSpotMatch {
	return interfaces.ProposalSpotMatch{MakerOrderID: orderID, Quantity: quantity, Price: price}
}

func spotExecution(
	position int,
	orderID, owner, base, quote string,
	side pb.OrderSide,
	price, amount, remaining string,
	matches []interfaces.ProposalSpotMatch,
	levels ...interfaces.ProposalSpotLevel,
) interfaces.ProposalSpotExecution {
	return interfaces.ProposalSpotExecution{
		PublicPosition: position,
		OrderID:        orderID,
		Owner:          owner,
		BaseToken:      base,
		QuoteToken:     quote,
		Side:           side,
		Price:          price,
		Amount:         amount,
		Remaining:      remaining,
		Matches:        matches,
		Levels:         levels,
	}
}

func crossedMarket(ctx interfaces.ProposalContext) *interfaces.ProposalSpotMarket {
	return proposalMarket(ctx,
		spotLevel("A", "FB", pb.OrderSide_BUY, scaledPrice(1), "ask", "alice", "10"),
		spotLevel("A", "FB", pb.OrderSide_SELL, scaledPrice(1000), "bid", "bob", "10"),
	)
}

func enabledMachine(t *testing.T, state *memoryState, cfg EngineConfig, signCount *int) *Machine {
	t.Helper()
	cfg.Enabled = true
	if cfg.ChainID == 0 {
		cfg.ChainID = 99
	}
	machine, err := NewMachine(cfg, state, testArbitrageAccount, testSigner(signCount), nil)
	if err != nil {
		t.Fatal(err)
	}
	machine.allowUntracedCross = true
	return machine
}

func TestValidateConfigHasNoRouteEnumeration(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.TxFee = "999"
	if err := ValidateConfig(cfg); err == nil || !strings.Contains(err.Error(), "at least 1000") {
		t.Fatalf("expected minimum fee error, got %v", err)
	}
	cfg.TxFee = "1000"
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("enabled dynamic config rejected: %v", err)
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"chainId"`, `"txFee"`, `"capitalBps"`, `"minProfitBps"`, `"maxOrdersPerBook"`, `"maxCycleEvaluations"`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("JSON missing %s: %s", field, encoded)
		}
	}
	if strings.Contains(string(encoded), `"routes"`) {
		t.Fatalf("dynamic config unexpectedly contains routes: %s", encoded)
	}
}

func TestEngineConfigRejectsSecretLikeAndUnknownJSONFields(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{name: "camel private key", input: `{"privateKey":"do-not-store"}`, wantErr: "secret field"},
		{name: "snake private key", input: `{"private_key":"do-not-store"}`, wantErr: "secret field"},
		{name: "seed phrase", input: `{"seed_phrase":"do-not-store"}`, wantErr: "secret field"},
		{name: "unknown policy", input: `{"routeScanLimit":1}`, wantErr: "unknown arbitrage config field"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := DefaultConfig()
			err := json.Unmarshal([]byte(test.input), &cfg)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("json.Unmarshal error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestEngineConfigJSONOverlayRetainsDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(`{"enabled":true}`), &cfg); err != nil {
		t.Fatal(err)
	}
	defaults := DefaultConfig()
	if !cfg.Enabled || cfg.TxFee != defaults.TxFee || cfg.CapitalBPS != defaults.CapitalBPS ||
		cfg.MaxOrdersPerBook != defaults.MaxOrdersPerBook || cfg.MaxCycleEvaluations != defaults.MaxCycleEvaluations {
		t.Fatalf("partial JSON did not overlay defaults: %+v", cfg)
	}
}

func TestEnabledMachineRequiresIndependentFundingAccount(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	if _, err := NewMachine(cfg, newMemoryState(), "", testSigner(new(int)), nil); err == nil || !strings.Contains(err.Error(), "funding account") {
		t.Fatalf("missing funding account error = %v", err)
	}
}

func TestQuoteBuyUsesGlobalFreezeRoundingAcrossMakers(t *testing.T) {
	price := big.NewInt(60_000_000)
	level := &topLevel{price: price, orders: []bookOrder{
		{orderID: "a", remain: big.NewInt(3)},
		{orderID: "b", remain: big.NewInt(3)},
	}}
	quoted := quoteBuy(level, big.NewInt(2))
	if quoted.baseAmount.String() != "3" || quoted.inputSpent.String() != "1" || quoted.output.String() != "3" {
		t.Fatalf("unexpected rounded BUY quote: %+v", quoted)
	}
	quoted = quoteBuy(level, big.NewInt(3))
	if quoted.baseAmount.String() != "6" || quoted.inputSpent.String() != "3" {
		t.Fatalf("full two-maker quote mismatch: %+v", quoted)
	}
}

func TestQuoteSellKeepsExecutablePrefixBeforeRoundedPartialMaker(t *testing.T) {
	level := &topLevel{price: big.NewInt(60_000_000), orders: []bookOrder{
		{orderID: "first", remain: big.NewInt(3)},
		{orderID: "partial-dust", remain: big.NewInt(3)},
	}}
	quoted := quoteSell(level, big.NewInt(4))
	if quoted.overflow || quoted.baseAmount.String() != "3" || quoted.inputSpent.String() != "3" || quoted.output.String() != "1" {
		t.Fatalf("SELL executable-prefix quote mismatch: %+v", quoted)
	}
}

func TestMachineDynamicallyFindsTwoLegRouteAndCachesByPrefix(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 7)
	seedBalance(t, state, testArbitrageAccount, "FB", "5000")
	cfg := DefaultConfig()
	signCount := 0
	machine := enabledMachine(t, state, cfg, &signCount)
	ctx := proposalContext("prefix-a", 2, 3)
	if machine.HasPending(ctx) {
		t.Fatal("dynamic provider claimed an opportunity without a VM projection")
	}
	batch, err := machine.Prepare(ctx, nil, crossedMarket(ctx))
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || len(batch.Transactions) != 2 || !strings.HasPrefix(batch.ID, "arb-") {
		t.Fatalf("unexpected private batch: %+v", batch)
	}
	if batch.PublicPrefixHash != "prefix-a" || batch.PublicPrefixCount != 2 {
		t.Fatalf("batch lost public-prefix binding: %+v", batch)
	}
	if !reflect.DeepEqual(batch.InsertAfterPublic, []int{2, 2}) {
		t.Fatalf("dynamic fallback insertion schedule=%v, want [2 2]", batch.InsertAfterPublic)
	}
	for i, tx := range batch.Transactions {
		base := tx.GetBase()
		order := tx.GetOrderTx()
		if base.FromAddress != testArbitrageAccount || base.Nonce != 7+uint64(i) || base.TargetHeight != 11 || base.OnlyProposer != "miner" || base.ChainId != 99 {
			t.Fatalf("leg %d base mismatch: %+v", i, base)
		}
		if order.IsMarket || order.MaxSlippageBps != 0 || order.Op != pb.OrderOp_ADD || order.Amount != "10" {
			t.Fatalf("leg %d is not expected ordinary limit ADD: %+v", i, order)
		}
	}
	if signCount != 2 {
		t.Fatalf("sign count=%d, want 2", signCount)
	}

	batch.Transactions[0].GetOrderTx().Amount = "999"
	retry, err := machine.Prepare(ctx, nil, crossedMarket(ctx))
	if err != nil || retry.Transactions[0].GetOrderTx().Amount != "10" || retry.ID != batch.ID || signCount != 2 {
		t.Fatalf("same-prefix retry not idempotent: retry=%+v err=%v signs=%d", retry, err, signCount)
	}
	ctx.Window++
	windowRetry, err := machine.Prepare(ctx, nil, crossedMarket(ctx))
	if err != nil || windowRetry.ID != retry.ID || signCount != 2 {
		t.Fatalf("window retry changed batch: batch=%+v err=%v signs=%d", windowRetry, err, signCount)
	}
	ctx.PublicPrefixHash = "prefix-b"
	prefixRetry, err := machine.Prepare(ctx, nil, crossedMarket(ctx))
	if err != nil || prefixRetry == nil || prefixRetry.ID == retry.ID || signCount != 4 {
		t.Fatalf("new txpool prefix did not invalidate cache: batch=%+v err=%v signs=%d", prefixRetry, err, signCount)
	}
}

func TestMachineSizesOnlyFromIndependentArbitrageAccount(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 4)
	seedBalance(t, state, testArbitrageAccount, "FB", "0")
	seedAccount(t, state, "miner", 91)
	seedBalance(t, state, "miner", "FB", "999999999")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))

	ctx := proposalContext("miner-only-capital", 2, 2)
	batch, err := machine.Prepare(ctx, nil, crossedMarket(ctx))
	if err != nil || batch != nil {
		t.Fatalf("miner balance was used as arbitrage capital: batch=%+v err=%v", batch, err)
	}

	seedBalance(t, state, testArbitrageAccount, "FB", "5000")
	ctx.PublicPrefixHash = "arbitrage-capital"
	batch, err = machine.Prepare(ctx, nil, crossedMarket(ctx))
	if err != nil || batch == nil || len(batch.Transactions) != 2 {
		t.Fatalf("funded arbitrage account did not produce route: batch=%+v err=%v", batch, err)
	}
	for i, tx := range batch.Transactions {
		base := tx.GetBase()
		if base.FromAddress != testArbitrageAccount || base.Nonce != 4+uint64(i) || base.OnlyProposer != "miner" {
			t.Fatalf("leg %d identity split mismatch: %+v", i, base)
		}
	}
}

func TestMachineSelfMakerUsesArbitrageAccountNotMiner(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "5000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))

	ctx := proposalContext("miner-maker", 2, 2)
	minerMaker := proposalMarket(ctx,
		spotLevel("A", "FB", pb.OrderSide_BUY, scaledPrice(1), "ask", "miner", "10"),
		spotLevel("A", "FB", pb.OrderSide_SELL, scaledPrice(1000), "bid", "bob", "10"),
	)
	batch, err := machine.Prepare(ctx, nil, minerMaker)
	if err != nil || batch == nil {
		t.Fatalf("miner maker was incorrectly treated as arbitrage self maker: batch=%+v err=%v", batch, err)
	}

	ctx.PublicPrefixHash = "arbitrage-self-maker"
	selfMaker := proposalMarket(ctx,
		spotLevel("A", "FB", pb.OrderSide_BUY, scaledPrice(1), "ask", testArbitrageAccount, "10"),
		spotLevel("A", "FB", pb.OrderSide_SELL, scaledPrice(1000), "bid", "bob", "10"),
	)
	batch, err = machine.Prepare(ctx, nil, selfMaker)
	if err != nil || batch != nil {
		t.Fatalf("arbitrage self maker was treated as external liquidity: batch=%+v err=%v", batch, err)
	}
}

func TestMachineFindsThreeLegRouteWithDynamicTokenNames(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 2)
	seedBalance(t, state, testArbitrageAccount, "FB", "100000")
	cfg := DefaultConfig()
	signCount := 0
	machine := enabledMachine(t, state, cfg, &signCount)
	ctx := proposalContext("triangle-prefix", 3, 3)
	market := proposalMarket(ctx,
		spotLevel("A.B", "FB", pb.OrderSide_BUY, scaledPrice(2), "a-fb-ask", "alice", "10"),
		spotLevel("A.B", "B_C", pb.OrderSide_SELL, scaledPrice(3), "a-b-bid", "bob", "10"),
		spotLevel("B_C", "FB", pb.OrderSide_SELL, scaledPrice(1000), "b-fb-bid", "carol", "30"),
	)
	batch, err := machine.Prepare(ctx, nil, market)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || len(batch.Transactions) != 3 {
		t.Fatalf("triangle not prepared: %+v", batch)
	}
	wants := []struct {
		base, quote string
		side        pb.OrderSide
		amount      string
	}{
		{"A.B", "FB", pb.OrderSide_BUY, "10"},
		{"A.B", "B_C", pb.OrderSide_SELL, "10"},
		{"B_C", "FB", pb.OrderSide_SELL, "30"},
	}
	for i, want := range wants {
		order := batch.Transactions[i].GetOrderTx()
		if order.BaseToken != want.base || order.QuoteToken != want.quote || order.Side != want.side || order.Amount != want.amount {
			t.Fatalf("triangle leg %d=%+v, want %+v", i, order, want)
		}
	}
}

func TestMachineRequiresClosedProjectedCycle(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "5000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	ctx := proposalContext("open-path", 1, 3)
	market := proposalMarket(ctx, spotLevel("A", "FB", pb.OrderSide_BUY, scaledPrice(1), "ask", "alice", "10"))
	batch, err := machine.Prepare(ctx, nil, market)
	if err != nil || batch != nil {
		t.Fatalf("open path produced arbitrage: batch=%+v err=%v", batch, err)
	}
}

func TestMachineUsesIndependentAccountsExistingNonFBCapital(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "5000")
	seedBalance(t, state, testArbitrageAccount, "Q", "100")
	seedBalance(t, state, "miner", "Q", "999999999")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	ctx := proposalContext("non-fb-cycle", 2, 2)
	market := proposalMarket(ctx,
		spotLevel("A", "Q", pb.OrderSide_BUY, scaledPrice(1), "ask", "alice", "10"),
		spotLevel("A", "Q", pb.OrderSide_SELL, scaledPrice(1000000), "bid", "bob", "10"),
	)
	batch, err := machine.Prepare(ctx, nil, market)
	if err != nil || batch == nil || len(batch.Transactions) != 2 {
		t.Fatalf("funded non-FB route was not prepared: batch=%+v err=%v", batch, err)
	}
	if got := batch.Transactions[0].GetOrderTx().Amount; got != "10" {
		t.Fatalf("route size used the wrong account/token balance: amount=%s want=10", got)
	}
	for _, tx := range batch.Transactions {
		if tx.GetBase().FromAddress != testArbitrageAccount {
			t.Fatalf("non-FB route sender=%q, want %q", tx.GetBase().FromAddress, testArbitrageAccount)
		}
	}
	result := &interfaces.ProposalSimulationResult{Valid: true}
	makers := []string{"ask", "bid"}
	prices := []string{scaledPrice(1), scaledPrice(1000000)}
	for i, tx := range batch.Transactions {
		result.Receipts = append(result.Receipts, interfaces.ProposalSimulationReceipt{
			TxID: tx.GetTxId(), Status: "SUCCEED",
			Events: []interfaces.ProposalSimulationEvent{{Kind: "ORDER_MATCHED", Payload: map[string]string{
				"maker_order_id": makers[i], "quantity": tx.GetOrderTx().Amount, "price": prices[i],
			}}},
		})
	}
	if err := machine.ValidateCandidate(ctx, batch, result); err != nil {
		t.Fatalf("profitable non-FB execution rejected: %v", err)
	}
}

func TestMachineNonFBCapitalStillRequiresFBFees(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "1999")
	seedBalance(t, state, testArbitrageAccount, "Q", "100")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	ctx := proposalContext("non-fb-no-fees", 2, 2)
	market := proposalMarket(ctx,
		spotLevel("A", "Q", pb.OrderSide_BUY, scaledPrice(1), "ask", "alice", "10"),
		spotLevel("A", "Q", pb.OrderSide_SELL, scaledPrice(1000000), "bid", "bob", "10"),
	)
	batch, err := machine.Prepare(ctx, nil, market)
	if err != nil || batch != nil {
		t.Fatalf("non-FB route ignored its separate FB fee requirement: batch=%+v err=%v", batch, err)
	}
}

func TestMachineRejectsNonceThatVMCannotAdvance(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, math.MaxUint64-1)
	seedBalance(t, state, testArbitrageAccount, "FB", "5000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	ctx := proposalContext("max-nonce", 2, 2)
	_, err := machine.Prepare(ctx, nil, crossedMarket(ctx))
	if err == nil || !strings.Contains(err.Error(), "nonce overflow") {
		t.Fatalf("unadvanceable nonce was accepted: %v", err)
	}
}

func TestValidateCandidateUsesActualMatchesAndRequiresProjectedMakers(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "5000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	ctx := proposalContext("validate", 2, 2)
	batch, err := machine.Prepare(ctx, nil, crossedMarket(ctx))
	if err != nil || batch == nil {
		t.Fatalf("prepare: batch=%v err=%v", batch, err)
	}
	result := &interfaces.ProposalSimulationResult{Valid: true}
	makers := []string{"ask", "bid"}
	prices := []string{scaledPrice(1), scaledPrice(1000)}
	for i, tx := range batch.Transactions {
		result.Receipts = append(result.Receipts, interfaces.ProposalSimulationReceipt{
			TxID: tx.GetTxId(), Status: "SUCCEED",
			Events: []interfaces.ProposalSimulationEvent{{Kind: "ORDER_MATCHED", Payload: map[string]string{
				"maker_order_id": makers[i], "quantity": tx.GetOrderTx().Amount, "price": prices[i],
			}}},
		})
	}
	if err := machine.ValidateCandidate(ctx, batch, result); err != nil {
		t.Fatalf("profitable complete execution rejected: %v", err)
	}

	result.Receipts[0].Events[0].Payload["quantity"] = "9"
	if err := machine.ValidateCandidate(ctx, batch, result); err == nil {
		t.Fatal("partial fill accepted")
	} else {
		for _, want := range []string{
			"partially filled",
			"leg=0",
			"validated_prior_legs=0",
			"total_legs=2",
			"group=0",
			"market=spot",
			`pair="A/FB"`,
			"side=BUY",
			`expected_makers="ask"`,
			`actual_matches="ask:9@` + scaledPrice(1) + `"`,
			"actual_match_count=1",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("partial-fill diagnostic missing %q: %v", want, err)
			}
		}
	}
	result.Receipts[0].Events[0].Payload["quantity"] = "10"
	result.Receipts[0].Events[0].Payload["maker_order_id"] = "unprojected"
	if err := machine.ValidateCandidate(ctx, batch, result); err == nil {
		t.Fatal("unprojected maker accepted")
	} else {
		for _, want := range []string{
			"unprojected maker",
			`expected_makers="ask"`,
			`actual_matches="unprojected:10@` + scaledPrice(1) + `"`,
		} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("unprojected-maker diagnostic missing %q: %v", want, err)
			}
		}
	}
}

func TestValidateCandidateRejectsChangedFundingAccount(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "5000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	ctx := proposalContext("sender-validation", 2, 2)
	batch, err := machine.Prepare(ctx, nil, crossedMarket(ctx))
	if err != nil || batch == nil {
		t.Fatalf("prepare: batch=%+v err=%v", batch, err)
	}
	batch.Transactions[0].GetBase().FromAddress = "miner"
	if err := machine.ValidateCandidate(ctx, batch, &interfaces.ProposalSimulationResult{Valid: true}); err == nil || !strings.Contains(err.Error(), "funding account") {
		t.Fatalf("changed arbitrage sender accepted: %v", err)
	}
}

func TestMachineReservesAllFeesWhenRouteStartsInFB(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "2005")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	ctx := proposalContext("fb-prefix", 1, 2)
	market := proposalMarket(ctx,
		spotLevel("A", "FB", pb.OrderSide_BUY, scaledPrice(1), "ask", "alice", "100"),
		spotLevel("A", "FB", pb.OrderSide_SELL, scaledPrice(1000), "bid", "bob", "100"),
	)
	batch, err := machine.Prepare(ctx, nil, market)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Transactions[0].GetOrderTx().Amount != "5" {
		t.Fatalf("fees were not reserved before capital sizing: %+v", batch)
	}
}

func scaledPrice(multiplier int64) string {
	return new(big.Int).Mul(priceScale, big.NewInt(multiplier)).String()
}
