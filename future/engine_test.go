package future

import (
	"fmt"
	"math/big"
	"runtime"
	"sort"
	"sync"
	"testing"
)

                                    

type mockPositionStore struct {
	data        map[string]*Position
	getAllErr   error
	idSequences map[string]struct {
		height  uint64
		ordinal uint64
	}
}

func newMockPositionStore() *mockPositionStore {
	return &mockPositionStore{
		data: make(map[string]*Position),
		idSequences: make(map[string]struct {
			height  uint64
			ordinal uint64
		}),
	}
}

func (m *mockPositionStore) AllocatePositionID(owner, pair string, side Side, height uint64) (string, error) {
	key := fmt.Sprintf("%s\x00%s\x00%d", owner, pair, side)
	sequence := m.idSequences[key]
	if sequence.height != height {
		sequence.height = height
		sequence.ordinal = 1
	} else {
		sequence.ordinal++
	}
	m.idSequences[key] = sequence
	base := fmt.Sprintf("%s_%s_%d_%d", owner, pair, side, height)
	if sequence.ordinal == 1 {
		return base, nil
	}
	return fmt.Sprintf("%s_%d", base, sequence.ordinal), nil
}

func (m *mockPositionStore) GetPosition(id string) (*Position, error) {
	p, ok := m.data[id]
	if !ok {
		return nil, nil
	}
	return p, nil
}
func (m *mockPositionStore) SetPosition(pos *Position) error { m.data[pos.ID] = pos; return nil }
func (m *mockPositionStore) DeletePosition(id string) error  { delete(m.data, id); return nil }
func (m *mockPositionStore) GetPositionsByOwner(owner string) ([]*Position, error) {
	var result []*Position
	for _, p := range m.data {
		if p.Owner == owner {
			result = append(result, p)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}
func (m *mockPositionStore) GetPositionByOwnerAndPairSide(owner, pair string, side Side) (*Position, error) {
	for _, p := range m.data {
		if p.Owner == owner && p.Pair == pair && p.Side == side && p.Status == StatusOpen {
			return p, nil
		}
	}
	return nil, nil
}
func (m *mockPositionStore) GetAllOpenPositions(pair string) ([]*Position, error) {
	if m.getAllErr != nil {
		return nil, m.getAllErr
	}
	var result []*Position
	for _, p := range m.data {
		if p.Pair == pair && p.Status == StatusOpen {
			result = append(result, p)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

type mockInsuranceFund struct {
	balances   map[string]*big.Int
	shortfalls map[string]*big.Int
}

func newMockInsuranceFund() *mockInsuranceFund {
	return &mockInsuranceFund{balances: make(map[string]*big.Int), shortfalls: make(map[string]*big.Int)}
}
func (m *mockInsuranceFund) GetBalance(pair string) (*big.Int, error) {
	b, ok := m.balances[pair]
	if !ok {
		return new(big.Int), nil
	}
	return new(big.Int).Set(b), nil
}
func (m *mockInsuranceFund) SetBalance(pair string, amount *big.Int) error {
	m.balances[pair] = new(big.Int).Set(amount)
	return nil
}
func (m *mockInsuranceFund) GetShortfall(pair string) (*big.Int, error) {
	b, ok := m.shortfalls[pair]
	if !ok {
		return new(big.Int), nil
	}
	return new(big.Int).Set(b), nil
}
func (m *mockInsuranceFund) AddShortfall(pair string, amount *big.Int) error {
	if amount == nil || amount.Sign() <= 0 {
		return nil
	}
	cur, ok := m.shortfalls[pair]
	if !ok {
		cur = new(big.Int)
	}
	m.shortfalls[pair] = new(big.Int).Add(cur, amount)
	return nil
}

type mockBalanceAccessor struct {
	balances map[string]*big.Int                            
}

func newMockBalanceAccessor() *mockBalanceAccessor {
	return &mockBalanceAccessor{balances: make(map[string]*big.Int)}
}
func balKey(owner, token string) string { return owner + ":" + token }
func (m *mockBalanceAccessor) GetAvailableBalance(owner, token string) (*big.Int, error) {
	b, ok := m.balances[balKey(owner, token)]
	if !ok {
		return new(big.Int), nil
	}
	return new(big.Int).Set(b), nil
}
func (m *mockBalanceAccessor) DeductBalance(owner, token string, amount *big.Int) error {
	k := balKey(owner, token)
	current := m.balances[k]
	if current == nil || current.Cmp(amount) < 0 {
		return fmt.Errorf("insufficient balance")
	}
	m.balances[k] = new(big.Int).Sub(current, amount)
	return nil
}
func (m *mockBalanceAccessor) AddBalance(owner, token string, amount *big.Int) error {
	k := balKey(owner, token)
	current := m.balances[k]
	if current == nil {
		current = new(big.Int)
	}
	m.balances[k] = new(big.Int).Add(current, amount)
	return nil
}

type mockLiquidationIndex struct {
	rows map[string]struct {
		pair  string
		side  Side
		price *big.Int
	}
	removeCalls      map[string]int
	lastRemovedPrice map[string]*big.Int
	findLongErr      error
	findShortErr     error
}

func newMockLiquidationIndex() *mockLiquidationIndex {
	return &mockLiquidationIndex{
		rows: make(map[string]struct {
			pair  string
			side  Side
			price *big.Int
		}),
		removeCalls:      make(map[string]int),
		lastRemovedPrice: make(map[string]*big.Int),
	}
}

func (m *mockLiquidationIndex) Upsert(positionID, pair string, side Side, liqPrice *big.Int) error {
	m.rows[positionID] = struct {
		pair  string
		side  Side
		price *big.Int
	}{pair: pair, side: side, price: new(big.Int).Set(liqPrice)}
	return nil
}

func (m *mockLiquidationIndex) Remove(positionID, pair string, side Side, oldLiqPrice *big.Int) error {
	m.removeCalls[positionID]++
	if row, ok := m.rows[positionID]; ok && row.price != nil {
		m.lastRemovedPrice[positionID] = new(big.Int).Set(row.price)
	}
	delete(m.rows, positionID)
	return nil
}

func (m *mockLiquidationIndex) FindLongsAtRisk(pair string, markPrice *big.Int) ([]string, error) {
	if m.findLongErr != nil {
		return nil, m.findLongErr
	}
	var ids []string
	for id, row := range m.rows {
		if row.pair == pair && row.side == Long && row.price.Cmp(markPrice) >= 0 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (m *mockLiquidationIndex) FindShortsAtRisk(pair string, markPrice *big.Int) ([]string, error) {
	if m.findShortErr != nil {
		return nil, m.findShortErr
	}
	var ids []string
	for id, row := range m.rows {
		if row.pair == pair && row.side == Short && row.price.Cmp(markPrice) <= 0 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

type mockMarkPrice struct {
	prices  map[string]*big.Int
	settles map[string]*big.Int
}

func newMockMarkPrice() *mockMarkPrice {
	return &mockMarkPrice{prices: make(map[string]*big.Int), settles: make(map[string]*big.Int)}
}
func (m *mockMarkPrice) GetMarkPrice(pair string) (*big.Int, error) {
	p, ok := m.prices[pair]
	if !ok {
		return nil, fmt.Errorf("no mark price for %s", pair)
	}
	return new(big.Int).Set(p), nil
}

                                                                                                           
func (m *mockMarkPrice) GetSettleMarkPrice(pair string) (*big.Int, error) {
	if p, ok := m.settles[pair]; ok {
		return new(big.Int).Set(p), nil
	}
	return m.GetMarkPrice(pair)
}

                               

type mockCumulativeFunding struct {
	mu            sync.Mutex
	data          map[string][2]*big.Int                               
	prev          map[string][2]*big.Int                                 
	getErr        error
	failGetOnCall int
	getCalls      int
}

func newMockCumulativeFunding() *mockCumulativeFunding {
	return &mockCumulativeFunding{
		data: make(map[string][2]*big.Int),
		prev: make(map[string][2]*big.Int),
	}
}
func (m *mockCumulativeFunding) GetCumulativeFunding(pair string) (*big.Int, *big.Int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getCalls++
	if m.getErr != nil && (m.failGetOnCall == 0 || m.getCalls == m.failGetOnCall) {
		return nil, nil, m.getErr
	}
	if d, ok := m.data[pair]; ok {
		return new(big.Int).Set(d[0]), new(big.Int).Set(d[1]), nil
	}
	return new(big.Int), new(big.Int), nil
}
func (m *mockCumulativeFunding) SetCumulativeFunding(pair string, cumLong, cumShort *big.Int) error {
	m.data[pair] = [2]*big.Int{new(big.Int).Set(cumLong), new(big.Int).Set(cumShort)}
	return nil
}
func (m *mockCumulativeFunding) GetPrevCumulativeFunding(pair string) (*big.Int, *big.Int, error) {
	if d, ok := m.prev[pair]; ok {
		return new(big.Int).Set(d[0]), new(big.Int).Set(d[1]), nil
	}
	return new(big.Int), new(big.Int), nil
}
func (m *mockCumulativeFunding) SetPrevCumulativeFunding(pair string, prevLong, prevShort *big.Int) error {
	m.prev[pair] = [2]*big.Int{new(big.Int).Set(prevLong), new(big.Int).Set(prevShort)}
	return nil
}

func newTestEngine() (*Engine, *mockPositionStore, *mockInsuranceFund, *mockBalanceAccessor, *mockMarkPrice) {
	ps := newMockPositionStore()
	ins := newMockInsuranceFund()
	ba := newMockBalanceAccessor()
	mp := newMockMarkPrice()
	cf := newMockCumulativeFunding()
	e := NewEngine(ps, ins, ba, mp, cf, nil)
	return e, ps, ins, ba, mp
}

                                                                                                                        
func newTestEngineFull() (*Engine, *mockPositionStore, *mockInsuranceFund, *mockBalanceAccessor, *mockMarkPrice, *mockCumulativeFunding) {
	ps := newMockPositionStore()
	ins := newMockInsuranceFund()
	ba := newMockBalanceAccessor()
	mp := newMockMarkPrice()
	cf := newMockCumulativeFunding()
	e := NewEngine(ps, ins, ba, mp, cf, nil)
	return e, ps, ins, ba, mp, cf
}

                               

func TestPreviewPendingFundingByPositionParallelMatchesSerial(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)

	e, _, _, _, mp, cf := newTestEngineFull()
	pair := "BTC_USDT"
	mp.settles[pair] = big.NewInt(100)
	cf.data[pair] = [2]*big.Int{big.NewInt(10), big.NewInt(10)}

	positions := make([]*Position, 0, parallelFundingPreviewMinPositions+64)
	for i := 0; i < parallelFundingPreviewMinPositions+64; i++ {
		side := Long
		if i%2 == 1 {
			side = Short
		}
		positions = append(positions, &Position{
			ID:                    fmt.Sprintf("pos_%05d", i),
			Owner:                 fmt.Sprintf("owner_%05d", i),
			Pair:                  pair,
			Side:                  side,
			EntryPrice:            big.NewInt(100),
			Quantity:              big.NewInt(int64(i + 1)),
			Margin:                big.NewInt(1000000),
			Status:                StatusOpen,
			LastCumulativeFunding: big.NewInt(0),
		})
	}

	serial, serialHasPayer, err := e.previewPendingFundingByPosition(positions)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(4)
	parallel, parallelHasPayer, err := e.previewPendingFundingByPosition(positions)
	if err != nil {
		t.Fatal(err)
	}
	if serialHasPayer != parallelHasPayer || len(serial) != len(parallel) {
		t.Fatalf("preview mismatch: serialHasPayer=%v parallelHasPayer=%v serial=%d parallel=%d", serialHasPayer, parallelHasPayer, len(serial), len(parallel))
	}
	for id, want := range serial {
		got := parallel[id]
		if got == nil || got.Cmp(want) != 0 {
			t.Fatalf("pending mismatch for %s: got=%v want=%v", id, got, want)
		}
	}
}

func TestEngine_OpenNewPosition(t *testing.T) {
	e, ps, _, _, _ := newTestEngine()
	err := e.ProcessTradeForPosition("alice", "BTC_USDT", Long, bi(1), bi(50000), bi(500), 100)
	if err != nil {
		t.Fatal(err)
	}
	positions, _ := ps.GetAllOpenPositions("BTC_USDT")
	if len(positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(positions))
	}
	pos := positions[0]
	if pos.Side != Long || pos.EntryPrice.Cmp(bi(50000)) != 0 || pos.Quantity.Cmp(bi(1)) != 0 || pos.Margin.Cmp(bi(500)) != 0 {
		t.Fatalf("unexpected position: %+v", pos)
	}
}

func TestEngine_OpenNewPositionFailsClosedOnFundingAnchorRead(t *testing.T) {
	ps := newMockPositionStore()
	cf := newMockCumulativeFunding()
	cf.getErr = fmt.Errorf("injected cumulative funding read failure")
	e := NewEngine(ps, newMockInsuranceFund(), newMockBalanceAccessor(), newMockMarkPrice(), cf, nil)

	err := e.ProcessTradeForPosition("alice", "BTC_USDT", Long, bi(1), bi(50000), bi(500), 100)
	if err == nil {
		t.Fatal("expected cumulative funding anchor read failure")
	}
	if len(ps.data) != 0 || len(ps.idSequences) != 0 {
		t.Fatalf("failed anchor read mutated position state: positions=%d sequences=%d", len(ps.data), len(ps.idSequences))
	}
}

func TestEngine_AddToPosition(t *testing.T) {
	e, ps, _, _, _ := newTestEngine()
	                           
	_ = e.ProcessTradeForPosition("alice", "BTC_USDT", Long, bi(1), bi(50000), bi(500), 100)
	                                    
	_ = e.ProcessTradeForPosition("alice", "BTC_USDT", Long, bi(1), bi(52000), bi(520), 100)

	positions, _ := ps.GetAllOpenPositions("BTC_USDT")
	if len(positions) != 1 {
		t.Fatalf("should still be 1 position after adding, got %d", len(positions))
	}
	pos := positions[0]
	                                         
	if pos.EntryPrice.Cmp(bi(51000)) != 0 {
		t.Fatalf("weighted entry = %s, want 51000", pos.EntryPrice)
	}
	if pos.Quantity.Cmp(bi(2)) != 0 {
		t.Fatalf("quantity = %s, want 2", pos.Quantity)
	}
	if pos.Margin.Cmp(bi(1020)) != 0 {
		t.Fatalf("margin = %s, want 1020", pos.Margin)
	}
}

func TestEngine_ReverseOpenIndexesNewPosition(t *testing.T) {
	ps := newMockPositionStore()
	ins := newMockInsuranceFund()
	ba := newMockBalanceAccessor()
	mp := newMockMarkPrice()
	cf := newMockCumulativeFunding()
	liq := newMockLiquidationIndex()
	e := NewEngine(ps, ins, ba, mp, cf, liq)
	pair := "BTC_USDT"
	mp.prices[pair] = sc(100)
	if err := e.ProcessTradeForPosition("alice", pair, Short, sc(1), sc(100), sc(10), 1); err != nil {
		t.Fatalf("open short: %v", err)
	}
	if err := e.ProcessTradeForPosition("alice", pair, Long, sc(2), sc(90), sc(20), 2); err != nil {
		t.Fatalf("reverse open: %v", err)
	}
	pos, err := ps.GetPositionByOwnerAndPairSide("alice", pair, Long)
	if err != nil || pos == nil || pos.Status != StatusOpen {
		t.Fatalf("expected new long position, pos=%+v err=%v", pos, err)
	}
	row, ok := liq.rows[pos.ID]
	if !ok {
		t.Fatalf("new reverse-open position missing liquidation index: %s", pos.ID)
	}
	if row.side != Long || row.pair != pair {
		t.Fatalf("unexpected liq index row: %+v", row)
	}
}

func TestEngine_ReverseOpenReadsFundingAnchorBeforeClosingOldLeg(t *testing.T) {
	ps := newMockPositionStore()
	cf := newMockCumulativeFunding()
	e := NewEngine(ps, newMockInsuranceFund(), newMockBalanceAccessor(), newMockMarkPrice(), cf, nil)
	pair := "BTC_USDT"
	if err := e.ProcessTradeForPosition("alice", pair, Short, sc(1), sc(100), sc(10), 1); err != nil {
		t.Fatalf("open short: %v", err)
	}
	shortBefore, err := ps.GetPositionByOwnerAndPairSide("alice", pair, Short)
	if err != nil || shortBefore == nil {
		t.Fatalf("load original short: pos=%v err=%v", shortBefore, err)
	}
	shortID := shortBefore.ID

	cf.getErr = fmt.Errorf("injected reverse anchor read failure")
	                                                                            
	cf.failGetOnCall = cf.getCalls + 2
	err = e.ProcessTradeForPosition("alice", pair, Long, sc(2), sc(90), sc(20), 2)
	if err == nil {
		t.Fatal("expected reverse cumulative funding anchor read failure")
	}
	shortAfter, err := ps.GetPosition(shortID)
	if err != nil || shortAfter == nil || shortAfter.Status != StatusOpen || shortAfter.Quantity.Cmp(sc(1)) != 0 {
		t.Fatalf("failed reverse anchor read mutated old leg: pos=%+v err=%v", shortAfter, err)
	}
	if long, err := ps.GetPositionByOwnerAndPairSide("alice", pair, Long); err != nil || long != nil {
		t.Fatalf("failed reverse anchor read created new leg: pos=%+v err=%v", long, err)
	}
}

func TestEngine_AddMargin(t *testing.T) {
	e, ps, _, ba, mp := newTestEngine()
	_ = e.ProcessTradeForPosition("alice", "BTC_USDT", Long, bi(1), bi(50000), bi(500), 100)
	ba.balances[balKey("alice", "USDT")] = bi(10000)
	mp.prices["BTC_USDT"] = bi(50000)

	positions, _ := ps.GetAllOpenPositions("BTC_USDT")
	posID := positions[0].ID

	err := e.AddMargin(posID, bi(300))
	if err != nil {
		t.Fatal(err)
	}
	pos, _ := ps.GetPosition(posID)
	if pos.Margin.Cmp(bi(800)) != 0 {
		t.Fatalf("margin after add = %s, want 800", pos.Margin)
	}
	                      
	bal, _ := ba.GetAvailableBalance("alice", "USDT")
	if bal.Cmp(bi(9700)) != 0 {
		t.Fatalf("balance after add margin = %s, want 9700", bal)
	}
}

func TestEngine_Liquidation_NoADL(t *testing.T) {
	e, ps, ins, _, mp := newTestEngine()
	fundingLiquidationCallbacks := 0
	e.OnFundingLiquidated = func(_ string, _ *LiquidationResult, _ *ADLResult) {
		fundingLiquidationCallbacks++
	}
	                                                                            
	_ = e.ProcessTradeForPosition("alice", "BTC_USDT", Long, sc(1), sc(50000), sc(500), 100)
	                          
	mp.prices["BTC_USDT"] = sc(49700)
	ins.balances["BTC_USDT"] = sc(10000)                         

	liqResults, adlResults, err := e.CheckAndProcessLiquidations("BTC_USDT")
	if err != nil {
		t.Fatal(err)
	}
	if len(liqResults) != 1 {
		t.Fatalf("expected 1 liquidation, got %d", len(liqResults))
	}
	if len(adlResults) != 0 {
		t.Fatalf("expected 0 ADL, got %d", len(adlResults))
	}
	if fundingLiquidationCallbacks != 0 {
		t.Fatalf("explicit liquidation fired funding-only callback %d times", fundingLiquidationCallbacks)
	}
	if liqResults[0].NeedsADL {
		t.Fatal("should not need ADL")
	}
	                                 
	if liqResults[0].InsuranceDelta.Sign() <= 0 {
		t.Fatal("insurance delta should be positive (no breach)")
	}
	insBal, _ := ins.GetBalance("BTC_USDT")
	if insBal.Cmp(sc(10500)) != 0 {
		t.Fatalf("insurance should receive lost margin, got %s want %s", insBal, sc(10500))
	}
	                                   
	positions, _ := ps.GetAllOpenPositions("BTC_USDT")
	if len(positions) != 0 {
		t.Fatal("liquidated position should not be open")
	}
}

func TestCheckAndProcessLiquidations_FailsClosedOnEitherIndexScanError(t *testing.T) {
	tests := []struct {
		name      string
		side      Side
		markPrice *big.Int
		inject    func(*mockLiquidationIndex)
	}{
		{
			name:      "long scan fails before short candidate can be processed",
			side:      Short,
			markPrice: sc(50300),
			inject: func(idx *mockLiquidationIndex) {
				idx.findLongErr = fmt.Errorf("injected long index scan failure")
			},
		},
		{
			name:      "short scan fails before long candidate can be processed",
			side:      Long,
			markPrice: sc(49700),
			inject: func(idx *mockLiquidationIndex) {
				idx.findShortErr = fmt.Errorf("injected short index scan failure")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, ps, ins, _, mp, _ := newTestEngineFull()
			idx := newMockLiquidationIndex()
			e.liqIndex = idx
			const pair = "BTC_USDT"
			if err := e.ProcessTradeForPosition("alice", pair, tc.side, sc(1), sc(50000), sc(500), 100); err != nil {
				t.Fatal(err)
			}
			positions, err := ps.GetAllOpenPositions(pair)
			if err != nil || len(positions) != 1 {
				t.Fatalf("seed open position: len=%d err=%v", len(positions), err)
			}
			posID := positions[0].ID
			originalQty := new(big.Int).Set(positions[0].Quantity)
			originalMargin := new(big.Int).Set(positions[0].Margin)
			mp.prices[pair] = tc.markPrice
			tc.inject(idx)

			liqResults, adlResults, err := e.CheckAndProcessLiquidations(pair)
			if err == nil {
				t.Fatal("expected liquidation index scan error")
			}
			if len(liqResults) != 0 || len(adlResults) != 0 {
				t.Fatalf("scan failure returned partial results: liq=%d adl=%d", len(liqResults), len(adlResults))
			}
			got, getErr := ps.GetPosition(posID)
			if getErr != nil || got == nil || got.Status != StatusOpen || got.Quantity.Cmp(originalQty) != 0 || got.Margin.Cmp(originalMargin) != 0 {
				t.Fatalf("scan failure mutated position: got=%+v err=%v", got, getErr)
			}
			insurance, getErr := ins.GetBalance(pair)
			if getErr != nil || insurance.Sign() != 0 {
				t.Fatalf("scan failure mutated insurance fund: balance=%v err=%v", insurance, getErr)
			}
		})
	}
}

func TestCheckAndProcessLiquidations_PropagatesFallbackPositionScanError(t *testing.T) {
	e, ps, _, _, mp, _ := newTestEngineFull()
	const pair = "BTC_USDT"
	mp.prices[pair] = sc(50000)
	ps.getAllErr = fmt.Errorf("injected open-position scan failure")

	liqResults, adlResults, err := e.CheckAndProcessLiquidations(pair)
	if err == nil {
		t.Fatal("expected open-position scan error")
	}
	if len(liqResults) != 0 || len(adlResults) != 0 {
		t.Fatalf("fallback scan failure returned partial results: liq=%d adl=%d", len(liqResults), len(adlResults))
	}
}

func TestEngine_Liquidation_WithADL(t *testing.T) {
	e, _, ins, ba, mp := newTestEngine()
	                                         
	_ = e.ProcessTradeForPosition("alice", "BTC_USDT", Long, sc(1), sc(50000), sc(500), 100)
	                                              
	_ = e.ProcessTradeForPosition("bob", "BTC_USDT", Short, sc(5), sc(50000), sc(25000), 100)
	ba.balances[balKey("bob", "USDT")] = sc(0)

	                                                  
	mp.prices["BTC_USDT"] = sc(49300)
	ins.balances["BTC_USDT"] = sc(50)                      

	liqResults, adlResults, err := e.CheckAndProcessLiquidations("BTC_USDT")
	if err != nil {
		t.Fatal(err)
	}
	if len(liqResults) != 1 {
		t.Fatalf("expected 1 liquidation, got %d", len(liqResults))
	}
	if !liqResults[0].NeedsADL {
		t.Fatal("should need ADL (insurance insufficient)")
	}
	if len(adlResults) == 0 {
		t.Fatal("expected ADL to cover liquidation shortfall")
	}
	                                                                                                      
	insBal, _ := ins.GetBalance("BTC_USDT")
	paid := new(big.Int)
	withheld := new(big.Int)
	for _, reduced := range adlResults[0].Reduced {
		paid.Add(paid, reduced.PaidProfit)
		withheld.Add(withheld, reduced.WithheldProfit)
	}
	if new(big.Int).Add(insBal, paid).Cmp(sc(550)) != 0 {
		t.Fatalf("insurance payout is not conserved: balance=%s paid=%s start+margin=%s", insBal, paid, sc(550))
	}
	if withheld.Sign() != 0 {
		t.Fatalf("insurance was sufficient, but ADL still withheld user profit=%s", withheld)
	}
}

func TestLiquidatePartial_DepositsLostMarginToInsurance(t *testing.T) {
	e, ps, ins, _, _ := newTestEngine()
	pair := "BTC_USDT"
	_ = ins.SetBalance(pair, sc(100))
	_ = ps.SetPosition(&Position{
		ID: "long", Owner: "alice", Pair: pair, Side: Long,
		EntryPrice: sc(100), Quantity: sc(2), Margin: sc(10), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int),
	})
	pos, _ := ps.GetPosition("long")
	cfg, _ := e.GetMarketConfig(pair)

	res, err := e.LiquidatePartial(pos, sc(95), sc(1), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.InsuranceDelta.Cmp(sc(5)) != 0 {
		t.Fatalf("insurance delta = %s, want %s", res.InsuranceDelta, sc(5))
	}
	insBal, _ := ins.GetBalance(pair)
	if insBal.Cmp(sc(105)) != 0 {
		t.Fatalf("insurance = %s, want %s", insBal, sc(105))
	}
	back, _ := ps.GetPosition("long")
	if back.Margin.Cmp(sc(5)) != 0 || back.Quantity.Cmp(sc(1)) != 0 {
		t.Fatalf("remaining position margin=%s qty=%s, want margin=%s qty=%s", back.Margin, back.Quantity, sc(5), sc(1))
	}
}

func TestExecuteADL_CoversWithWithheldPnLAndRecordsResidual(t *testing.T) {
	e, ps, ins, ba, mp := newTestEngine()
	pair := "BTC_USDT"
	_ = e.ProcessTradeForPosition("bob", pair, Short, sc(1), sc(50000), sc(500), 100)
	mp.prices[pair] = sc(49900)
	_ = ins.SetBalance(pair, sc(20))

	result, err := e.ExecuteADL(pair, Short, sc(150))
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.TotalCovered.Cmp(sc(100)) != 0 {
		t.Fatalf("ADL covered = %v, want %s", result, sc(100))
	}
	shortfall, _ := ins.GetShortfall(pair)
	if shortfall.Cmp(sc(50)) != 0 {
		t.Fatalf("residual shortfall = %s, want %s", shortfall, sc(50))
	}
	bal, _ := ba.GetAvailableBalance("bob", "USDT")
	if bal.Cmp(sc(520)) != 0 {
		t.Fatalf("bob should retain 20%% profit plus margin, got %s want %s", bal, sc(520))
	}
	pos, _ := ps.GetPosition("bob_BTC_USDT_2_100")
	if pos == nil || pos.Status != StatusADLClosed || pos.Quantity.Sign() != 0 || pos.Margin.Sign() != 0 {
		t.Fatalf("bob position not fully ADL closed: %+v", pos)
	}
	if pos.RealizedPnL.Cmp(sc(20)) != 0 || pos.ADLWithheldPnL.Cmp(sc(80)) != 0 {
		t.Fatalf("bob paid/withheld ADL profit=%s/%s, want %s/%s", pos.RealizedPnL, pos.ADLWithheldPnL, sc(20), sc(80))
	}
	if got := result.Reduced[0]; got.GrossProfit.Cmp(sc(100)) != 0 || got.PaidProfit.Cmp(sc(20)) != 0 || got.WithheldProfit.Cmp(sc(80)) != 0 {
		t.Fatalf("ADL result gross/paid/withheld=%s/%s/%s", got.GrossProfit, got.PaidProfit, got.WithheldProfit)
	}
}

func TestExecuteADL_ReservesProtectedProfitAcrossCandidates(t *testing.T) {
	e, ps, ins, ba, mp := newTestEngine()
	const pair = "BTC_USDT"
	mp.prices[pair] = sc(100)
	_ = ins.SetBalance(pair, sc(4))
	for _, owner := range []string{"bob", "carol"} {
		if err := ps.SetPosition(&Position{
			ID: owner + "-short", Owner: owner, Pair: pair, Side: Short,
			EntryPrice: sc(110), Quantity: sc(1), Margin: sc(10), Status: StatusOpen,
		}); err != nil {
			t.Fatal(err)
		}
	}

	result, err := e.ExecuteADL(pair, Short, sc(20))
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.TotalCovered.Cmp(sc(20)) != 0 || len(result.Reduced) != 2 {
		t.Fatalf("ADL result=%+v, want two candidates covering %s", result, sc(20))
	}
	for _, owner := range []string{"bob", "carol"} {
		pos, _ := ps.GetPosition(owner + "-short")
		if pos.Status != StatusADLClosed || pos.RealizedPnL.Cmp(sc(2)) != 0 || pos.ADLWithheldPnL.Cmp(sc(8)) != 0 {
			t.Fatalf("%s ADL state=%+v, want paid/withheld %s/%s", owner, pos, sc(2), sc(8))
		}
		balance, _ := ba.GetAvailableBalance(owner, "USDT")
		if balance.Cmp(sc(12)) != 0 {
			t.Fatalf("%s balance=%s, want margin 10 + protected profit 2", owner, balance)
		}
	}
	insurance, _ := ins.GetBalance(pair)
	shortfall, _ := ins.GetShortfall(pair)
	if insurance.Sign() != 0 || shortfall.Sign() != 0 {
		t.Fatalf("insurance/shortfall=%s/%s, want 0/0", insurance, shortfall)
	}
}

func TestExecuteADL_FullyInsuredUsesSeparateCloseStatus(t *testing.T) {
	e, ps, ins, ba, mp := newTestEngine()
	const pair = "BTC_USDT"
	pos := &Position{
		ID: "bob-short", Owner: "bob", Pair: pair, Side: Short,
		EntryPrice: sc(110), Quantity: sc(1), Margin: sc(10), Status: StatusOpen,
	}
	if err := ps.SetPosition(pos); err != nil {
		t.Fatal(err)
	}
	mp.prices[pair] = sc(100)
	if err := ins.SetBalance(pair, sc(10)); err != nil {
		t.Fatal(err)
	}

	result, err := e.ExecuteADL(pair, Short, sc(10))
	if err != nil {
		t.Fatal(err)
	}
	back, _ := ps.GetPosition(pos.ID)
	if result == nil || len(result.Reduced) != 1 || result.Reduced[0].WithheldProfit.Sign() != 0 {
		t.Fatalf("fully insured reduction result=%+v", result)
	}
	if back == nil || back.Status != StatusInsuredDeleveraged || back.RealizedPnL.Cmp(sc(10)) != 0 || back.ADLWithheldPnL.Sign() != 0 {
		t.Fatalf("fully insured reduction was mislabeled as ADL: %+v", back)
	}
	balance, _ := ba.GetAvailableBalance("bob", "USDT")
	if balance.Cmp(sc(20)) != 0 {
		t.Fatalf("fully insured balance=%s, want released margin + full profit %s", balance, sc(20))
	}
}

func TestExecuteADL_DoesNotBreakProfitFloorWithoutInsurance(t *testing.T) {
	e, ps, ins, ba, mp := newTestEngine()
	const pair = "BTC_USDT"
	mp.prices[pair] = sc(100)
	pos := &Position{
		ID: "bob-short", Owner: "bob", Pair: pair, Side: Short,
		EntryPrice: sc(110), Quantity: sc(1), Margin: sc(10), Status: StatusOpen,
	}
	if err := ps.SetPosition(pos); err != nil {
		t.Fatal(err)
	}

	result, err := e.ExecuteADL(pair, Short, sc(10))
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.TotalCovered.Sign() != 0 || len(result.Reduced) != 0 {
		t.Fatalf("ADL bypassed the profit floor without insurance: %+v", result)
	}
	back, _ := ps.GetPosition(pos.ID)
	balance, _ := ba.GetAvailableBalance("bob", "USDT")
	shortfall, _ := ins.GetShortfall(pair)
	if back.Status != StatusOpen || back.Quantity.Cmp(sc(1)) != 0 || balance.Sign() != 0 || shortfall.Cmp(sc(10)) != 0 {
		t.Fatalf("protected ADL state position=%+v balance=%s shortfall=%s", back, balance, shortfall)
	}
}

func TestExecuteADL_RoundingOvershootDoesNotOverWithhold(t *testing.T) {
	e, ps, ins, ba, mp := newTestEngine()
	pair := "BTC_USDT"
	entry := new(big.Int).Mul(big.NewInt(100), PriceScale)
	mark := new(big.Int).Mul(big.NewInt(975), new(big.Int).Div(PriceScale, big.NewInt(10)))
	pos := &Position{
		ID: "bob_adl_rounding", Owner: "bob", Pair: pair, Side: Short,
		EntryPrice: entry, Quantity: big.NewInt(10), Margin: big.NewInt(1000), Status: StatusOpen,
	}
	if err := ps.SetPosition(pos); err != nil {
		t.Fatal(err)
	}
	mp.prices[pair] = mark
	_ = ins.SetBalance(pair, big.NewInt(1))

	result, err := e.ExecuteADL(pair, Short, big.NewInt(1))
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.TotalCovered.Sign() != 0 || len(result.Reduced) != 0 {
		t.Fatalf("ADL must not reduce a position by 2 PnL to cover 1: %+v", result)
	}
	bal, _ := ba.GetAvailableBalance("bob", "USDT")
	back, _ := ps.GetPosition(pos.ID)
	if bal.Sign() != 0 || back.Quantity.Cmp(big.NewInt(10)) != 0 || back.Margin.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("rounding-only ADL mutated user funds/position: balance=%s qty=%s margin=%s", bal, back.Quantity, back.Margin)
	}
	shortfall, _ := ins.GetShortfall(pair)
	if shortfall.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("uncovered indivisible dust=%s, want 1", shortfall)
	}
	if back.RealizedPnL != nil && back.RealizedPnL.Sign() != 0 {
		t.Fatalf("skipped ADL changed realized PnL: %s", back.RealizedPnL)
	}
}

func TestExecuteADL_RoundingUsesLargestNonOvershootingQuantity(t *testing.T) {
	e, ps, ins, ba, mp := newTestEngine()
	pair := "BTC_USDT"
	entry := new(big.Int).Mul(big.NewInt(100), PriceScale)
	mark := new(big.Int).Mul(big.NewInt(975), new(big.Int).Div(PriceScale, big.NewInt(10)))
	pos := &Position{
		ID: "bob_adl_rounding_partial", Owner: "bob", Pair: pair, Side: Short,
		EntryPrice: entry, Quantity: big.NewInt(10), Margin: big.NewInt(1000), Status: StatusOpen,
	}
	if err := ps.SetPosition(pos); err != nil {
		t.Fatal(err)
	}
	mp.prices[pair] = mark
	_ = ins.SetBalance(pair, big.NewInt(1))

	result, err := e.ExecuteADL(pair, Short, big.NewInt(3))
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.TotalCovered.Cmp(big.NewInt(2)) != 0 || len(result.Reduced) != 1 || result.Reduced[0].ReducedQuantity.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("ADL reduction = %+v, want qty=1 covering exactly 2", result)
	}
	back, _ := ps.GetPosition(pos.ID)
	bal, _ := ba.GetAvailableBalance("bob", "USDT")
	if back.Quantity.Cmp(big.NewInt(9)) != 0 || back.Margin.Cmp(big.NewInt(900)) != 0 || bal.Cmp(big.NewInt(101)) != 0 {
		t.Fatalf("partial ADL state qty/margin/balance=%s/%s/%s, want 9/900/101", back.Quantity, back.Margin, bal)
	}
	shortfall, _ := ins.GetShortfall(pair)
	if shortfall.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("residual shortfall=%s, want 1", shortfall)
	}
}

func TestExecuteADL_MaintainsLiquidationIndex(t *testing.T) {
	const pair = "BTC_USDT"

	setup := func(t *testing.T) (*Engine, *mockPositionStore, *mockLiquidationIndex, *Position, *big.Int) {
		t.Helper()

		ps := newMockPositionStore()
		liq := newMockLiquidationIndex()
		mp := newMockMarkPrice()
		ins := newMockInsuranceFund()
		_ = ins.SetBalance(pair, big.NewInt(10))
		e := NewEngine(ps, ins, newMockBalanceAccessor(), mp, newMockCumulativeFunding(), liq)
		pos := &Position{
			ID: "bob_adl_index", Owner: "bob", Pair: pair, Side: Short,
			EntryPrice:        new(big.Int).Mul(big.NewInt(2), PriceScale),
			Quantity:          big.NewInt(3),
			Margin:            big.NewInt(2),
			Status:            StatusOpen,
			ClosePnLRemainder: big.NewInt(17),
		}
		if err := ps.SetPosition(pos); err != nil {
			t.Fatal(err)
		}
		mp.prices[pair] = new(big.Int).Set(PriceScale)
		cfg, err := e.GetMarketConfig(pair)
		if err != nil {
			t.Fatal(err)
		}
		e.updateLiqIndex(pos, cfg)
		row, ok := liq.rows[pos.ID]
		if !ok {
			t.Fatal("precondition: liquidation index was not seeded")
		}
		return e, ps, liq, pos, new(big.Int).Set(row.price)
	}

	t.Run("full reduction removes stale entry", func(t *testing.T) {
		e, ps, liq, pos, _ := setup(t)

		if _, err := e.ExecuteADL(pair, Short, big.NewInt(3)); err != nil {
			t.Fatal(err)
		}
		back, _ := ps.GetPosition(pos.ID)
		if back == nil || back.Status != StatusInsuredDeleveraged {
			t.Fatalf("fully insured position should use insured-deleverage status, got %+v", back)
		}
		if back.ClosePnLRemainder == nil || back.ClosePnLRemainder.Sign() != 0 {
			t.Fatalf("full ADL should discard normal-close remainder, got %v", back.ClosePnLRemainder)
		}
		if _, exists := liq.rows[pos.ID]; exists {
			t.Fatal("fully reduced ADL position left a stale liquidation index entry")
		}
	})

	t.Run("partial reduction replaces stale price", func(t *testing.T) {
		e, ps, liq, pos, oldPrice := setup(t)

		if _, err := e.ExecuteADL(pair, Short, big.NewInt(1)); err != nil {
			t.Fatal(err)
		}
		back, _ := ps.GetPosition(pos.ID)
		if back == nil || back.Status != StatusOpen || back.Quantity.Cmp(big.NewInt(2)) != 0 {
			t.Fatalf("position should remain partially open, got %+v", back)
		}
		if back.ClosePnLRemainder.Cmp(big.NewInt(17)) != 0 {
			t.Fatalf("partial ADL changed prior normal-close remainder: %s", back.ClosePnLRemainder)
		}
		cfg, _ := e.GetMarketConfig(pair)
		wantPrice := positionConservativeLiquidationPrice(back, back.Margin, cfg.MaintenanceMarginRateOrDefault())
		if oldPrice.Cmp(wantPrice) == 0 {
			t.Fatalf("precondition: liquidation price did not change: %s", oldPrice)
		}
		row, exists := liq.rows[pos.ID]
		if !exists {
			t.Fatal("partially reduced ADL position is missing from liquidation index")
		}
		if row.price.Cmp(wantPrice) != 0 {
			t.Fatalf("stale liquidation price after partial ADL: got %s want %s (old %s)", row.price, wantPrice, oldPrice)
		}
		if len(liq.rows) != 1 {
			t.Fatalf("partial ADL left duplicate liquidation references: %d rows", len(liq.rows))
		}
	})

	t.Run("partial long reduction removes prior entry when new price is zero", func(t *testing.T) {
		ps := newMockPositionStore()
		liq := newMockLiquidationIndex()
		mp := newMockMarkPrice()
		ins := newMockInsuranceFund()
		_ = ins.SetBalance(pair, big.NewInt(10))
		e := NewEngine(ps, ins, newMockBalanceAccessor(), mp, newMockCumulativeFunding(), liq)
		entryPrice := new(big.Int).Quo(new(big.Int).Mul(big.NewInt(14), PriceScale), big.NewInt(10))
		markPrice := new(big.Int).Quo(new(big.Int).Mul(big.NewInt(24), PriceScale), big.NewInt(10))
		pos := &Position{
			ID: "alice_adl_zero_liq", Owner: "alice", Pair: pair, Side: Long,
			EntryPrice: entryPrice,
			Quantity:   big.NewInt(3),
			Margin:     big.NewInt(5),
			Status:     StatusOpen,
		}
		if err := ps.SetPosition(pos); err != nil {
			t.Fatal(err)
		}
		mp.prices[pair] = markPrice
		cfg, err := e.GetMarketConfig(pair)
		if err != nil {
			t.Fatal(err)
		}
		e.updateLiqIndex(pos, cfg)
		oldRow, ok := liq.rows[pos.ID]
		if !ok || oldRow.price.Sign() <= 0 {
			t.Fatalf("precondition: positive prior liquidation entry missing: %+v", oldRow)
		}
		oldPrice := new(big.Int).Set(oldRow.price)

		if _, err := e.ExecuteADL(pair, Long, big.NewInt(1)); err != nil {
			t.Fatal(err)
		}
		back, _ := ps.GetPosition(pos.ID)
		if back == nil || back.Status != StatusOpen || back.Quantity.Cmp(big.NewInt(2)) != 0 || back.Margin.Cmp(big.NewInt(4)) != 0 {
			t.Fatalf("position should remain open at qty=2 margin=4, got %+v", back)
		}
		newPrice := positionConservativeLiquidationPrice(back, back.Margin, cfg.MaintenanceMarginRateOrDefault())
		if newPrice.Sign() != 0 {
			t.Fatalf("precondition: recalculated liquidation price = %s, want zero", newPrice)
		}
		if _, exists := liq.rows[pos.ID]; exists {
			t.Fatal("non-positive recalculated price left the prior liquidation entry behind")
		}
		if liq.removeCalls[pos.ID] != 1 {
			t.Fatalf("Remove calls = %d, want 1", liq.removeCalls[pos.ID])
		}
		removedPrice := liq.lastRemovedPrice[pos.ID]
		if removedPrice == nil || removedPrice.Cmp(oldPrice) != 0 {
			t.Fatalf("removed price = %v, want prior price %s", removedPrice, oldPrice)
		}
	})
}

func TestEngine_ValidateOpenOrder(t *testing.T) {
	e, _, _, _, _ := newTestEngine()
	if err := e.ValidateOpenOrder("BTC_USDT", sc(1), sc(5000), 0); err == nil {
		t.Fatal("zero leverage should be rejected")
	}
	if err := e.ValidateOpenOrder("BTC_USDT", big.NewInt(1), big.NewInt(1), 1); err == nil {
		t.Fatal("zero notional should be rejected")
	}
	if err := e.ValidateOpenOrder("BTC_USDT", big.NewInt(1), big.NewInt(100_000_000), 100); err != nil {
		t.Fatalf("ceil margin should make the smallest positive notional valid: %v", err)
	}
	                                                                              
	if err := e.ValidateOpenOrder("BTC_USDT", sc(1), sc(5000), 100); err != nil {
		t.Fatalf("small position 100x should be valid: %v", err)
	}
	if err := e.ValidateOpenOrder("BTC_USDT", sc(10), sc(50000), 100); err != nil {
		t.Fatalf("large position 100x should be valid under global max leverage: %v", err)
	}
	if err := e.ValidateOpenOrder("BTC_USDT", sc(1), sc(5000), 101); err == nil {
		t.Fatal("leverage above global max should be rejected")
	}
}

func TestADL_RankDeterminism(t *testing.T) {
	positions := []*Position{
		{ID: "c", Owner: "c", Pair: "BTC_USDT", Side: Short, EntryPrice: sc(50000), Quantity: sc(1), Margin: sc(500), Status: StatusOpen},
		{ID: "a", Owner: "a", Pair: "BTC_USDT", Side: Short, EntryPrice: sc(50000), Quantity: sc(1), Margin: sc(500), Status: StatusOpen},
		{ID: "b", Owner: "b", Pair: "BTC_USDT", Side: Short, EntryPrice: sc(50000), Quantity: sc(1), Margin: sc(500), Status: StatusOpen},
	}
	markPrice := sc(49000)                            

	cands := RankADLCandidates(positions, markPrice, Short)
	if len(cands) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(cands))
	}
	                           
	if cands[0].PositionID != "a" || cands[1].PositionID != "b" || cands[2].PositionID != "c" {
		t.Fatalf("deterministic order broken: %s, %s, %s", cands[0].PositionID, cands[1].PositionID, cands[2].PositionID)
	}
}

                                                                
  
                                                                                                      
                                                
  
            
  
                                                             
                                                                                                
                                                                               
func TestApplyPendingFunding_BadDebtCollectedToInsurance(t *testing.T) {
	e, ps, ins, ba, mp, cf := newTestEngineFull()
	pair := "BTC_USDT"
	owner := "alice"
	callbackCount := 0
	e.OnFundingLiquidated = func(gotPair string, liq *LiquidationResult, adl *ADLResult) {
		callbackCount++
		if gotPair != pair || liq == nil || liq.PositionID != "pos1" || adl != nil {
			t.Errorf("unexpected direct funding liquidation callback: pair=%q liq=%+v adl=%+v", gotPair, liq, adl)
		}
	}
	oneUsdt := big.NewInt(100_000_000)                                    

	mp.prices[pair] = oneUsdt                                                                        

	                           
	ba.balances[balKey(owner, "USDT")] = new(big.Int).Mul(big.NewInt(50), oneUsdt)
	                                                                
	_ = ins.SetBalance(pair, new(big.Int).Mul(big.NewInt(1000), oneUsdt))

	                                                               
	                                                                                 
	pos := &Position{
		ID: "pos1", Owner: owner, Pair: pair, Side: Long,
		EntryPrice:            new(big.Int).Set(oneUsdt),
		Quantity:              new(big.Int).Mul(big.NewInt(1000), oneUsdt),
		Margin:                new(big.Int).Mul(big.NewInt(100), oneUsdt),
		Status:                StatusOpen,
		OpenHeight:            1,
		LastCumulativeFunding: big.NewInt(0),
	}
	_ = ps.SetPosition(pos)

	                                                                                                                              
	_ = cf.SetCumulativeFunding(pair, big.NewInt(2000), big.NewInt(0))

	cfg, _ := e.GetMarketConfig(pair)
	if err := e.ApplyPendingFunding(pos, cfg); err != nil {
		t.Fatal(err)
	}
	if callbackCount != 1 {
		t.Fatalf("direct funding liquidation callback count=%d, want exactly 1", callbackCount)
	}

	                           
	posBack, _ := ps.GetPosition("pos1")
	if posBack.Margin.Sign() != 0 {
		t.Fatalf("expect Margin=0 after bad debt, got %s", posBack.Margin)
	}
	                  
	balBack := ba.balances[balKey(owner, "USDT")]
	if balBack.Sign() != 0 {
		t.Fatalf("expect balance=0 (全部被吃), got %s", balBack)
	}
	                                                                                                  
	insBack, _ := ins.GetBalance(pair)
	expectedIns := new(big.Int).Mul(big.NewInt(1150), oneUsdt)                                  
	if insBack.Cmp(expectedIns) != 0 {
		t.Fatalf("expect insurance=%s (1000+150 collected), got %s", expectedIns, insBack)
	}
	                                      
	if posBack.Status != StatusLiquidated {
		t.Fatalf("expect position Liquidated, got status=%v", posBack.Status)
	}
}

                                                                             
                                                                               
func TestApplyPendingFunding_PayerDepositsInsurance(t *testing.T) {
	e, ps, ins, ba, mp, cf := newTestEngineFull()
	pair := "BTC_USDT"
	owner := "alice"
	oneUsdt := big.NewInt(100_000_000)

	mp.prices[pair] = oneUsdt
	ba.balances[balKey(owner, "USDT")] = new(big.Int).Mul(big.NewInt(50), oneUsdt)
	_ = ins.SetBalance(pair, new(big.Int).Mul(big.NewInt(1000), oneUsdt))

	pos := &Position{
		ID: "pos1", Owner: owner, Pair: pair, Side: Long,
		EntryPrice:            new(big.Int).Set(oneUsdt),
		Quantity:              new(big.Int).Mul(big.NewInt(1000), oneUsdt),
		Margin:                new(big.Int).Mul(big.NewInt(500), oneUsdt),                 
		Status:                StatusOpen,
		OpenHeight:            1,
		LastCumulativeFunding: big.NewInt(0),
	}
	_ = ps.SetPosition(pos)

	                                                                                      
	_ = cf.SetCumulativeFunding(pair, big.NewInt(200), big.NewInt(0))

	cfg, _ := e.GetMarketConfig(pair)
	if err := e.ApplyPendingFunding(pos, cfg); err != nil {
		t.Fatal(err)
	}

	posBack, _ := ps.GetPosition("pos1")
	expectMargin := new(big.Int).Mul(big.NewInt(480), oneUsdt)            
	if posBack.Margin.Cmp(expectMargin) != 0 {
		t.Fatalf("expect margin=%s, got %s", expectMargin, posBack.Margin)
	}
	                                                      
	insBack, _ := ins.GetBalance(pair)
	expectedIns := new(big.Int).Mul(big.NewInt(1020), oneUsdt)
	if insBack.Cmp(expectedIns) != 0 {
		t.Fatalf("insurance = %s, want %s", insBack, expectedIns)
	}
	               
	balBack := ba.balances[balKey(owner, "USDT")]
	expectBal := new(big.Int).Mul(big.NewInt(50), oneUsdt)
	if balBack.Cmp(expectBal) != 0 {
		t.Fatalf("balance should be unchanged, got %s", balBack)
	}
	                 
	if posBack.Status != StatusOpen {
		t.Fatalf("expect Open, got %v", posBack.Status)
	}
}

func TestApplyPendingFunding_ZeroRoundedAmountAdvancesAnchorBeforeSameSideIncrease(t *testing.T) {
	e, ps, ins, _, mp, cf := newTestEngineFull()
	pair := "BTC_USDT"
	mark := new(big.Int).Set(PriceScale)
	mp.prices[pair] = new(big.Int).Set(mark)

	pos := &Position{
		ID:                    "zero-rounded-anchor",
		Owner:                 "alice",
		Pair:                  pair,
		Side:                  Long,
		EntryPrice:            new(big.Int).Set(mark),
		Quantity:              big.NewInt(1),
		Margin:                big.NewInt(1000),
		Status:                StatusOpen,
		LastCumulativeFunding: new(big.Int),
		SettledFunding:        big.NewInt(13),
		LastSettledFunding:    big.NewInt(7),
	}
	if err := ps.SetPosition(pos); err != nil {
		t.Fatal(err)
	}
	if err := cf.SetCumulativeFunding(pair, big.NewInt(1), new(big.Int)); err != nil {
		t.Fatal(err)
	}
	cfg, err := e.GetMarketConfig(pair)
	if err != nil {
		t.Fatal(err)
	}

	                                                                     
	                                                                             
	if err := e.ApplyPendingFunding(pos, cfg); err != nil {
		t.Fatal(err)
	}
	if pos.LastCumulativeFunding.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("zero-rounded settlement did not advance anchor: got %s want 1", pos.LastCumulativeFunding)
	}
	if pos.LastSettledFunding == nil || pos.LastSettledFunding.Sign() != 0 {
		t.Fatalf("last settled funding should describe this zero-amount round, got %v", pos.LastSettledFunding)
	}
	if pos.SettledFunding.Cmp(big.NewInt(13)) != 0 || pos.Margin.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("zero-rounded settlement changed money: settled=%s margin=%s", pos.SettledFunding, pos.Margin)
	}

	                                                                               
	                                                                                    
	if err := e.ProcessTradeForPosition("alice", pair, Long, big.NewInt(9999), mark, new(big.Int), 2); err != nil {
		t.Fatal(err)
	}
	back, err := ps.GetPosition(pos.ID)
	if err != nil {
		t.Fatal(err)
	}
	marginBefore := new(big.Int).Set(back.Margin)
	if err := e.ApplyPendingFunding(back, cfg); err != nil {
		t.Fatal(err)
	}
	if back.Margin.Cmp(marginBefore) != 0 || back.SettledFunding.Cmp(big.NewInt(13)) != 0 {
		t.Fatalf("old zero-rounded funding window was charged after increase: margin=%s want=%s settled=%s", back.Margin, marginBefore, back.SettledFunding)
	}
	insurance, err := ins.GetBalance(pair)
	if err != nil {
		t.Fatal(err)
	}
	if insurance.Sign() != 0 {
		t.Fatalf("zero-rounded funding unexpectedly moved insurance money: %s", insurance)
	}
}

type fundingFailSetPositionStore struct {
	PositionStore
}

func (s *fundingFailSetPositionStore) SetPosition(*Position) error {
	return fmt.Errorf("injected funding SetPosition failure")
}

func TestApplyPendingFunding_ZeroRoundedAnchorWriteFailureDoesNotMutateInput(t *testing.T) {
	base := newMockPositionStore()
	store := &fundingFailSetPositionStore{PositionStore: base}
	insurance := newMockInsuranceFund()
	balance := newMockBalanceAccessor()
	markPrice := newMockMarkPrice()
	funding := newMockCumulativeFunding()
	e := NewEngine(store, insurance, balance, markPrice, funding, nil)
	pair := "BTC_USDT"
	markPrice.prices[pair] = new(big.Int).Set(PriceScale)
	if err := funding.SetCumulativeFunding(pair, big.NewInt(1), new(big.Int)); err != nil {
		t.Fatal(err)
	}
	pos := &Position{
		ID:                 "zero-rounded-write-failure",
		Owner:              "alice",
		Pair:               pair,
		Side:               Long,
		EntryPrice:         new(big.Int).Set(PriceScale),
		Quantity:           big.NewInt(1),
		Margin:             big.NewInt(1000),
		Status:             StatusOpen,
		SettledFunding:     big.NewInt(13),
		LastSettledFunding: big.NewInt(7),
	}
	if err := base.SetPosition(pos); err != nil {
		t.Fatal(err)
	}
	cfg, err := e.GetMarketConfig(pair)
	if err != nil {
		t.Fatal(err)
	}

	err = e.ApplyPendingFunding(pos, cfg)
	if err == nil {
		t.Fatal("expected anchor persistence failure")
	}
	if pos.LastCumulativeFunding != nil {
		t.Fatalf("failed anchor write mutated input last cumulative funding: %s", pos.LastCumulativeFunding)
	}
	if pos.LastSettledFunding.Cmp(big.NewInt(7)) != 0 || pos.SettledFunding.Cmp(big.NewInt(13)) != 0 || pos.Margin.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("failed anchor write mutated input: last=%s total=%s margin=%s", pos.LastSettledFunding, pos.SettledFunding, pos.Margin)
	}
	stored, err := base.GetPosition(pos.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored != pos || stored.LastCumulativeFunding != nil {
		t.Fatalf("failed anchor write replaced or mutated stored position: %+v", stored)
	}
}

func TestApplyPendingFundingForPair_SweepsReceiversAndPayers(t *testing.T) {
	e, ps, ins, ba, mp, cf := newTestEngineFull()
	pair := "BTC_USDT"
	mp.prices[pair] = sc(50000)
	_ = ps.SetPosition(&Position{
		ID: "long", Owner: "alice", Pair: pair, Side: Long,
		EntryPrice: sc(50000), Quantity: sc(1), Margin: sc(1000), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int),
	})
	_ = ps.SetPosition(&Position{
		ID: "short", Owner: "bob", Pair: pair, Side: Short,
		EntryPrice: sc(50000), Quantity: sc(1), Margin: sc(1000), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int),
	})
	_ = cf.SetCumulativeFunding(pair, big.NewInt(10), big.NewInt(10))

	if err := e.ApplyPendingFundingForPair(pair); err != nil {
		t.Fatal(err)
	}

	longBack, _ := ps.GetPosition("long")
	if longBack.Margin.Cmp(sc(950)) != 0 || longBack.LastCumulativeFunding.Cmp(big.NewInt(10)) != 0 {
		t.Fatalf("long funding not settled: margin=%s lastCum=%s", longBack.Margin, longBack.LastCumulativeFunding)
	}
	if longBack.SettledFunding.Cmp(sc(-50)) != 0 || longBack.LastSettledFunding.Cmp(sc(-50)) != 0 {
		t.Fatalf("long settled funding not tracked: total=%s last=%s", longBack.SettledFunding, longBack.LastSettledFunding)
	}
	bobBal, _ := ba.GetAvailableBalance("bob", "USDT")
	if bobBal.Cmp(sc(50)) != 0 {
		t.Fatalf("short receiver balance = %s, want %s", bobBal, sc(50))
	}
	shortBack, _ := ps.GetPosition("short")
	if shortBack.LastCumulativeFunding.Cmp(big.NewInt(10)) != 0 {
		t.Fatalf("short lastCum = %s, want 10", shortBack.LastCumulativeFunding)
	}
	if shortBack.SettledFunding.Cmp(sc(50)) != 0 || shortBack.LastSettledFunding.Cmp(sc(50)) != 0 {
		t.Fatalf("short settled funding not tracked: total=%s last=%s", shortBack.SettledFunding, shortBack.LastSettledFunding)
	}
	insBack, _ := ins.GetBalance(pair)
	if insBack.Sign() != 0 {
		t.Fatalf("equal notional funding should clear insurance back to 0, got %s", insBack)
	}
}

func TestApplyPendingFundingForPair_ImbalancedOpenInterestClearsViaInsurance(t *testing.T) {
	e, ps, ins, ba, mp, cf := newTestEngineFull()
	pair := "BTC_USDT"
	mp.prices[pair] = sc(50000)
	_ = ins.SetBalance(pair, sc(1000))
	_ = ps.SetPosition(&Position{
		ID: "long", Owner: "alice", Pair: pair, Side: Long,
		EntryPrice: sc(50000), Quantity: sc(2), Margin: sc(1000), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int),
	})
	_ = ps.SetPosition(&Position{
		ID: "short", Owner: "bob", Pair: pair, Side: Short,
		EntryPrice: sc(50000), Quantity: sc(1), Margin: sc(1000), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int),
	})
	_ = cf.SetCumulativeFunding(pair, big.NewInt(10), big.NewInt(10))

	if err := e.ApplyPendingFundingForPair(pair); err != nil {
		t.Fatal(err)
	}

	longBack, _ := ps.GetPosition("long")
	if longBack.Margin.Cmp(sc(900)) != 0 || longBack.SettledFunding.Cmp(sc(-100)) != 0 {
		t.Fatalf("long funding not debited: margin=%s settled=%s", longBack.Margin, longBack.SettledFunding)
	}
	bobBal, _ := ba.GetAvailableBalance("bob", "USDT")
	if bobBal.Cmp(sc(50)) != 0 {
		t.Fatalf("short receiver balance = %s, want %s", bobBal, sc(50))
	}
	insBack, _ := ins.GetBalance(pair)
	if insBack.Cmp(sc(1050)) != 0 {
		t.Fatalf("imbalanced funding remainder should stay in insurance, got %s want %s", insBack, sc(1050))
	}
}

func TestApplyPendingFundingForPair_UnmatchedPayerDepositsInsurance(t *testing.T) {
	e, ps, ins, ba, mp, cf := newTestEngineFull()
	pair := "BTC_USDT"
	mp.prices[pair] = sc(50000)
	_ = ins.SetBalance(pair, sc(1000))
	_ = ps.SetPosition(&Position{
		ID: "long", Owner: "alice", Pair: pair, Side: Long,
		EntryPrice: sc(50000), Quantity: sc(1), Margin: sc(1000), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int),
	})
	_ = cf.SetCumulativeFunding(pair, big.NewInt(10), big.NewInt(10))

	if err := e.ApplyPendingFundingForPair(pair); err != nil {
		t.Fatal(err)
	}

	longBack, _ := ps.GetPosition("long")
	if longBack.Margin.Cmp(sc(950)) != 0 || longBack.SettledFunding.Cmp(sc(-50)) != 0 {
		t.Fatalf("long unmatched funding not debited: margin=%s settled=%s", longBack.Margin, longBack.SettledFunding)
	}
	insBack, _ := ins.GetBalance(pair)
	if insBack.Cmp(sc(1050)) != 0 {
		t.Fatalf("insurance = %s, want %s", insBack, sc(1050))
	}
	aliceBal, _ := ba.GetAvailableBalance("alice", "USDT")
	if aliceBal.Sign() != 0 {
		t.Fatalf("alice balance changed unexpectedly: %s", aliceBal)
	}
}

func TestApplyPendingFundingForPair_UnmatchedReceiverDoesNotMint(t *testing.T) {
	e, ps, ins, ba, mp, cf := newTestEngineFull()
	pair := "BTC_USDT"
	mp.prices[pair] = sc(50000)
	_ = ins.SetBalance(pair, sc(1000))
	_ = ps.SetPosition(&Position{
		ID: "short", Owner: "bob", Pair: pair, Side: Short,
		EntryPrice: sc(50000), Quantity: sc(1), Margin: sc(1000), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int),
	})
	_ = cf.SetCumulativeFunding(pair, big.NewInt(10), big.NewInt(10))

	if err := e.ApplyPendingFundingForPair(pair); err != nil {
		t.Fatal(err)
	}

	shortBack, _ := ps.GetPosition("short")
	if shortBack.Margin.Cmp(sc(1000)) != 0 || shortBack.SettledFunding.Sign() != 0 || shortBack.LastCumulativeFunding.Cmp(big.NewInt(10)) != 0 {
		t.Fatalf("short unmatched receiver should only advance cum: margin=%s settled=%s lastCum=%s", shortBack.Margin, shortBack.SettledFunding, shortBack.LastCumulativeFunding)
	}
	bobBal, _ := ba.GetAvailableBalance("bob", "USDT")
	if bobBal.Sign() != 0 {
		t.Fatalf("bob should not receive minted funding, got %s", bobBal)
	}
	insBack, _ := ins.GetBalance(pair)
	if insBack.Cmp(sc(1000)) != 0 {
		t.Fatalf("insurance should be unchanged, got %s", insBack)
	}
}

func TestApplyPendingFundingForPair_LiquidatesAfterFundingSweep(t *testing.T) {
	e, ps, ins, _, mp, cf := newTestEngineFull()
	pair := "BTC_USDT"
	mp.prices[pair] = sc(95)
	_ = ins.SetBalance(pair, sc(1000))
	_ = ps.SetPosition(&Position{
		ID: "long", Owner: "alice", Pair: pair, Side: Long,
		EntryPrice: sc(100), Quantity: sc(1), Margin: sc(6), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int),
	})
	_ = cf.SetCumulativeFunding(pair, big.NewInt(200), new(big.Int))

	if err := e.ApplyPendingFundingForPair(pair); err != nil {
		t.Fatal(err)
	}

	back, _ := ps.GetPosition("long")
	if back.Status != StatusLiquidated {
		t.Fatalf("position should be liquidated after funding lowers margin into risk zone: status=%v margin=%s", back.Status, back.Margin)
	}
	if back.SettledFunding.Cmp(sc(-1)) >= 0 {
		t.Fatalf("funding should be debited before liquidation, got %s", back.SettledFunding)
	}
}

func TestApplyPendingFundingForPair_SettlesReceiverBeforePayerADL(t *testing.T) {
	e, ps, ins, ba, mp, cf := newTestEngineFull()
	pair := "BTC_USDT"
	mp.settles[pair] = sc(100)
	mp.prices[pair] = sc(90)
	ba.balances[balKey("alice", "USDT")] = sc(5)
	_ = ins.SetBalance(pair, sc(2))
	if err := ps.SetPosition(&Position{
		ID: "alice-long", Owner: "alice", Pair: pair, Side: Long,
		EntryPrice: sc(100), Quantity: sc(1), Margin: sc(5), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int), SettledFunding: new(big.Int),
	}); err != nil {
		t.Fatal(err)
	}
	if err := ps.SetPosition(&Position{
		ID: "bob-short", Owner: "bob", Pair: pair, Side: Short,
		EntryPrice: sc(110), Quantity: sc(1), Margin: sc(20), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int), SettledFunding: new(big.Int),
	}); err != nil {
		t.Fatal(err)
	}
	if err := cf.SetCumulativeFunding(pair, big.NewInt(1000), big.NewInt(1000)); err != nil {
		t.Fatal(err)
	}

	if err := e.ApplyPendingFundingForPair(pair); err != nil {
		t.Fatal(err)
	}

	bob, err := ps.GetPosition("bob-short")
	if err != nil {
		t.Fatal(err)
	}
	half := new(big.Int).Quo(sc(1), big.NewInt(2))
	if bob == nil || bob.Status != StatusOpen || bob.Quantity.Cmp(half) != 0 {
		t.Fatalf("Bob should be ADL-reduced only after funding settlement: %+v", bob)
	}
	if bob.SettledFunding.Cmp(sc(10)) != 0 || bob.LastCumulativeFunding.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("Bob lost funding on the ADL-reduced quantity: settled=%s lastCum=%s", bob.SettledFunding, bob.LastCumulativeFunding)
	}
	bobBalance, err := ba.GetAvailableBalance("bob", "USDT")
	if err != nil {
		t.Fatal(err)
	}
	                                                                           
	                                                              
	if bobBalance.Cmp(sc(22)) != 0 {
		t.Fatalf("Bob balance=%s, want funding 10 + ADL margin 10 + protected profit 2", bobBalance)
	}
	insurance, err := ins.GetBalance(pair)
	if err != nil {
		t.Fatal(err)
	}
	if insurance.Sign() != 0 {
		t.Fatalf("funding remainder stranded after ADL: %s", insurance)
	}
}

func TestReducePosition_FragmentedCloseCannotEraseLossDust(t *testing.T) {
	type closeResult struct {
		balance   *big.Int
		insurance *big.Int
		realized  *big.Int
	}
	runClose := func(parts ...*big.Int) closeResult {
		e, ps, ins, ba, mp, _ := newTestEngineFull()
		pair := "BTC_USDT"
		entryPrice := new(big.Int).Add(new(big.Int).Set(PriceScale), big.NewInt(1))
		exitPrice := new(big.Int).Set(PriceScale)
		mp.prices[pair] = new(big.Int).Set(exitPrice)
		if err := ps.SetPosition(&Position{
			ID: "long", Owner: "alice", Pair: pair, Side: Long,
			EntryPrice: entryPrice, Quantity: new(big.Int).Set(PriceScale),
			Margin: big.NewInt(1000), Status: StatusOpen,
			LastCumulativeFunding: new(big.Int), RealizedPnL: new(big.Int),
			ClosedQty: new(big.Int), ExitNotional: new(big.Int),
		}); err != nil {
			t.Fatal(err)
		}

		for i, qty := range parts {
			if err := e.ProcessTradeForPosition(
				"alice", pair, Short, new(big.Int).Set(qty), exitPrice,
				new(big.Int), uint64(i+1),
			); err != nil {
				t.Fatalf("close part %d: %v", i, err)
			}
		}

		back, err := ps.GetPosition("long")
		if err != nil || back == nil || back.Status != StatusClosed {
			t.Fatalf("closed position: pos=%+v err=%v", back, err)
		}
		balance, _ := ba.GetAvailableBalance("alice", "USDT")
		insurance, _ := ins.GetBalance(pair)
		return closeResult{
			balance: balance, insurance: insurance,
			realized: new(big.Int).Set(back.RealizedPnL),
		}
	}

	full := runClose(new(big.Int).Set(PriceScale))
	half := new(big.Int).Quo(new(big.Int).Set(PriceScale), big.NewInt(2))
	fragmented := runClose(half, half)

	if full.balance.Cmp(big.NewInt(999)) != 0 ||
		full.insurance.Cmp(big.NewInt(1)) != 0 ||
		full.realized.Cmp(big.NewInt(-1)) != 0 {
		t.Fatalf("one-shot close balance=%s insurance=%s realized=%s, want 999/1/-1",
			full.balance, full.insurance, full.realized)
	}
	if fragmented.balance.Cmp(full.balance) != 0 ||
		fragmented.insurance.Cmp(full.insurance) != 0 ||
		fragmented.realized.Cmp(full.realized) != 0 {
		t.Fatalf("fragmented close erased loss dust: full=%s/%s/%s fragmented=%s/%s/%s",
			full.balance, full.insurance, full.realized,
			fragmented.balance, fragmented.insurance, fragmented.realized)
	}
}

func TestReducePosition_FragmentedProfitMatchesOneShot(t *testing.T) {
	type closeResult struct {
		balance   *big.Int
		insurance *big.Int
		realized  *big.Int
	}
	runClose := func(parts ...*big.Int) closeResult {
		e, ps, ins, ba, mp, _ := newTestEngineFull()
		pair := "BTC_USDT"
		entryPrice := new(big.Int).Set(PriceScale)
		exitPrice := new(big.Int).Add(new(big.Int).Set(PriceScale), big.NewInt(1))
		mp.prices[pair] = new(big.Int).Set(exitPrice)
		if err := ins.SetBalance(pair, big.NewInt(10)); err != nil {
			t.Fatal(err)
		}
		if err := ps.SetPosition(&Position{
			ID: "long", Owner: "alice", Pair: pair, Side: Long,
			EntryPrice: entryPrice, Quantity: new(big.Int).Set(PriceScale),
			Margin: big.NewInt(1000), Status: StatusOpen,
			LastCumulativeFunding: new(big.Int), RealizedPnL: new(big.Int),
			ClosedQty: new(big.Int), ExitNotional: new(big.Int), ClosePnLRemainder: new(big.Int),
		}); err != nil {
			t.Fatal(err)
		}
		for i, qty := range parts {
			if err := e.ProcessTradeForPosition("alice", pair, Short, new(big.Int).Set(qty), exitPrice, new(big.Int), uint64(i+1)); err != nil {
				t.Fatalf("close part %d: %v", i, err)
			}
		}
		closed, _ := ps.GetPosition("long")
		balance, _ := ba.GetAvailableBalance("alice", "USDT")
		insurance, _ := ins.GetBalance(pair)
		return closeResult{balance: balance, insurance: insurance, realized: new(big.Int).Set(closed.RealizedPnL)}
	}

	full := runClose(new(big.Int).Set(PriceScale))
	half := new(big.Int).Quo(new(big.Int).Set(PriceScale), big.NewInt(2))
	fragmented := runClose(half, half)
	if full.balance.Cmp(big.NewInt(1001)) != 0 || full.insurance.Cmp(big.NewInt(9)) != 0 || full.realized.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("one-shot profit close=%s/%s/%s, want 1001/9/1", full.balance, full.insurance, full.realized)
	}
	if fragmented.balance.Cmp(full.balance) != 0 || fragmented.insurance.Cmp(full.insurance) != 0 || fragmented.realized.Cmp(full.realized) != 0 {
		t.Fatalf("fragmented profit differs: full=%s/%s/%s fragmented=%s/%s/%s",
			full.balance, full.insurance, full.realized,
			fragmented.balance, fragmented.insurance, fragmented.realized)
	}
}

func TestReducePosition_VariablePricesCarrySignedNumerator(t *testing.T) {
	e, ps, ins, ba, mp, _ := newTestEngineFull()
	pair := "BTC_USDT"
	entryPrice := new(big.Int).Set(PriceScale)
	qty := new(big.Int).Set(PriceScale)
	half := new(big.Int).Quo(new(big.Int).Set(qty), big.NewInt(2))
	if err := ps.SetPosition(&Position{
		ID: "long", Owner: "alice", Pair: pair, Side: Long,
		EntryPrice: entryPrice, Quantity: qty, Margin: big.NewInt(1000), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int), RealizedPnL: new(big.Int),
		ClosedQty: new(big.Int), ExitNotional: new(big.Int),
	}); err != nil {
		t.Fatal(err)
	}

	lossPrice := new(big.Int).Sub(new(big.Int).Set(PriceScale), big.NewInt(1))
	mp.prices[pair] = new(big.Int).Set(lossPrice)
	if err := e.ProcessTradeForPosition("alice", pair, Short, half, lossPrice, new(big.Int), 1); err != nil {
		t.Fatal(err)
	}
	afterLoss, _ := ps.GetPosition("long")
	afterLossBalance, _ := ba.GetAvailableBalance("alice", "USDT")
	afterLossInsurance, _ := ins.GetBalance(pair)
	wantRemainder := new(big.Int).Neg(new(big.Int).Set(half))
	if afterLoss.RealizedPnL.Sign() != 0 ||
		afterLossBalance.Cmp(big.NewInt(500)) != 0 ||
		afterLossInsurance.Sign() != 0 ||
		afterLoss.ClosePnLRemainder.Cmp(wantRemainder) != 0 {
		t.Fatalf("first exit realized=%s balance=%s insurance=%s remainder=%s, want 0/500/0/%s",
			afterLoss.RealizedPnL, afterLossBalance, afterLossInsurance, afterLoss.ClosePnLRemainder, wantRemainder)
	}

	profitPrice := new(big.Int).Add(new(big.Int).Set(PriceScale), big.NewInt(1))
	mp.prices[pair] = new(big.Int).Set(profitPrice)
	if err := e.ProcessTradeForPosition("alice", pair, Short, half, profitPrice, new(big.Int), 2); err != nil {
		t.Fatal(err)
	}
	closed, _ := ps.GetPosition("long")
	finalBalance, _ := ba.GetAvailableBalance("alice", "USDT")
	finalInsurance, _ := ins.GetBalance(pair)
	wantExitNotional := new(big.Int).Add(CalcNotional(half, lossPrice), CalcNotional(half, profitPrice))
	if closed.RealizedPnL.Sign() != 0 || finalBalance.Cmp(big.NewInt(1000)) != 0 || finalInsurance.Sign() != 0 ||
		closed.ExitNotional.Cmp(wantExitNotional) != 0 {
		t.Fatalf("different-price exits realized=%s balance=%s insurance=%s exitNotional=%s, want 0/1000/0/%s",
			closed.RealizedPnL, finalBalance, finalInsurance, closed.ExitNotional, wantExitNotional)
	}
}

func TestReducePosition_ZeroDiffCloseCannotLendClosedQuantityDust(t *testing.T) {
	e, ps, ins, ba, _, _ := newTestEngineFull()
	pair := "BTC_USDT"
	entryPrice := new(big.Int).Quo(new(big.Int).Set(PriceScale), big.NewInt(2))
	if err := ins.SetBalance(pair, big.NewInt(1)); err != nil {
		t.Fatal(err)
	}
	if err := ps.SetPosition(&Position{
		ID: "long", Owner: "alice", Pair: pair, Side: Long,
		EntryPrice: entryPrice, Quantity: big.NewInt(3), Margin: big.NewInt(1), Status: StatusOpen,
		RealizedPnL: new(big.Int), ClosedQty: new(big.Int), ExitNotional: new(big.Int), ClosePnLRemainder: new(big.Int),
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.ProcessTradeForPosition("alice", pair, Short, big.NewInt(2), entryPrice, new(big.Int), 1); err != nil {
		t.Fatal(err)
	}
	partial, _ := ps.GetPosition("long")
	if partial.ClosedQty.Cmp(big.NewInt(2)) != 0 || partial.ClosePnLRemainder.Sign() != 0 {
		t.Fatalf("zero-diff close closedQty=%s remainder=%s, want 2/0", partial.ClosedQty, partial.ClosePnLRemainder)
	}
	lowerPrice := new(big.Int).Sub(new(big.Int).Set(entryPrice), big.NewInt(1))
	if err := e.ProcessTradeForPosition("alice", pair, Short, big.NewInt(1), lowerPrice, new(big.Int), 2); err != nil {
		t.Fatal(err)
	}
	closed, _ := ps.GetPosition("long")
	balance, _ := ba.GetAvailableBalance("alice", "USDT")
	insurance, _ := ins.GetBalance(pair)
	if closed.RealizedPnL.Sign() != 0 || balance.Cmp(big.NewInt(1)) != 0 || insurance.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("lower-price close borrowed quantity dust: realized=%s balance=%s insurance=%s, want 0/1/1",
			closed.RealizedPnL, balance, insurance)
	}
}

func TestPreviewIncrementalClosePnL_NeverFlipsSegmentDirection(t *testing.T) {
	almostScale := new(big.Int).Sub(new(big.Int).Set(PriceScale), big.NewInt(1))
	cases := []struct {
		name      string
		side      Side
		entry     *big.Int
		exit      *big.Int
		remainder *big.Int
		forbid    int
	}{
		{"long loss", Long, new(big.Int).Set(PriceScale), new(big.Int).Sub(new(big.Int).Set(PriceScale), big.NewInt(1)), almostScale, 1},
		{"long profit", Long, new(big.Int).Set(PriceScale), new(big.Int).Add(new(big.Int).Set(PriceScale), big.NewInt(1)), new(big.Int).Neg(new(big.Int).Set(almostScale)), -1},
		{"short loss", Short, new(big.Int).Set(PriceScale), new(big.Int).Add(new(big.Int).Set(PriceScale), big.NewInt(1)), almostScale, 1},
		{"short profit", Short, new(big.Int).Set(PriceScale), new(big.Int).Sub(new(big.Int).Set(PriceScale), big.NewInt(1)), new(big.Int).Neg(new(big.Int).Set(almostScale)), -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pnl, remainder := previewIncrementalClosePnL(&Position{
				Side: tc.side, EntryPrice: tc.entry, ClosePnLRemainder: tc.remainder,
			}, big.NewInt(1), tc.exit)
			if tc.forbid > 0 && pnl.Sign() > 0 || tc.forbid < 0 && pnl.Sign() < 0 {
				t.Fatalf("segment direction flipped: pnl=%s", pnl)
			}
			if new(big.Int).Abs(new(big.Int).Set(remainder)).Cmp(PriceScale) >= 0 {
				t.Fatalf("remainder out of range: %s", remainder)
			}
		})
	}
}

func TestReducePosition_FragmentedProfitDoesNotRetryPriorInsuranceShortfall(t *testing.T) {
	e, ps, ins, ba, mp, _ := newTestEngineFull()
	pair := "BTC_USDT"
	entryPrice := new(big.Int).Set(PriceScale)
	profitPrice := new(big.Int).Add(new(big.Int).Set(PriceScale), big.NewInt(2))
	qty := new(big.Int).Set(PriceScale)
	half := new(big.Int).Quo(new(big.Int).Set(qty), big.NewInt(2))
	mp.prices[pair] = new(big.Int).Set(profitPrice)
	if err := ps.SetPosition(&Position{
		ID: "long", Owner: "alice", Pair: pair, Side: Long,
		EntryPrice: entryPrice, Quantity: qty, Margin: big.NewInt(1000), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int), RealizedPnL: new(big.Int),
		ClosedQty: new(big.Int), ExitNotional: new(big.Int),
	}); err != nil {
		t.Fatal(err)
	}

	                                                                             
	                                                                           
	if err := e.ProcessTradeForPosition("alice", pair, Short, half, profitPrice, new(big.Int), 1); err != nil {
		t.Fatal(err)
	}
	afterFirst, _ := ps.GetPosition("long")
	shortfall, _ := ins.GetShortfall(pair)
	if afterFirst.RealizedPnL.Sign() != 0 || shortfall.Cmp(big.NewInt(1)) != 0 || afterFirst.ClosePnLRemainder.Sign() != 0 {
		t.Fatalf("first short-paid exit realized=%s shortfall=%s remainder=%s, want 0/1/0",
			afterFirst.RealizedPnL, shortfall, afterFirst.ClosePnLRemainder)
	}

	                                                                         
	                                                                        
	if err := ins.SetBalance(pair, big.NewInt(2)); err != nil {
		t.Fatal(err)
	}
	if err := e.ProcessTradeForPosition("alice", pair, Short, half, profitPrice, new(big.Int), 2); err != nil {
		t.Fatal(err)
	}
	closed, _ := ps.GetPosition("long")
	finalBalance, _ := ba.GetAvailableBalance("alice", "USDT")
	finalInsurance, _ := ins.GetBalance(pair)
	finalShortfall, _ := ins.GetShortfall(pair)
	if closed.RealizedPnL.Cmp(big.NewInt(1)) != 0 ||
		finalBalance.Cmp(big.NewInt(1001)) != 0 ||
		finalInsurance.Cmp(big.NewInt(1)) != 0 ||
		finalShortfall.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("second exit realized=%s balance=%s insurance=%s shortfall=%s, want 1/1001/1/1",
			closed.RealizedPnL, finalBalance, finalInsurance, finalShortfall)
	}
}

func TestReducePosition_LossDepositsInsurance(t *testing.T) {
	e, ps, ins, ba, mp, _ := newTestEngineFull()
	pair := "BTC_USDT"
	mp.prices[pair] = sc(90)
	_ = ins.SetBalance(pair, sc(1000))
	_ = ps.SetPosition(&Position{
		ID: "long", Owner: "alice", Pair: pair, Side: Long,
		EntryPrice: sc(100), Quantity: sc(1), Margin: sc(50), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int),
	})

	if err := e.ProcessTradeForPosition("alice", pair, Short, sc(1), sc(90), new(big.Int), 10); err != nil {
		t.Fatal(err)
	}

	aliceBal, _ := ba.GetAvailableBalance("alice", "USDT")
	if aliceBal.Cmp(sc(40)) != 0 {
		t.Fatalf("alice balance = %s, want %s", aliceBal, sc(40))
	}
	insBack, _ := ins.GetBalance(pair)
	if insBack.Cmp(sc(1010)) != 0 {
		t.Fatalf("insurance = %s, want %s", insBack, sc(1010))
	}
	back, _ := ps.GetPosition("long")
	if back.RealizedPnL.Cmp(sc(-10)) != 0 {
		t.Fatalf("realized pnl = %s, want %s", back.RealizedPnL, sc(-10))
	}
}

func TestReducePosition_ProfitWithdrawsInsurance(t *testing.T) {
	e, ps, ins, ba, mp, _ := newTestEngineFull()
	pair := "BTC_USDT"
	mp.prices[pair] = sc(110)
	_ = ins.SetBalance(pair, sc(1000))
	_ = ps.SetPosition(&Position{
		ID: "long", Owner: "alice", Pair: pair, Side: Long,
		EntryPrice: sc(100), Quantity: sc(1), Margin: sc(50), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int),
	})

	if err := e.ProcessTradeForPosition("alice", pair, Short, sc(1), sc(110), new(big.Int), 10); err != nil {
		t.Fatal(err)
	}

	aliceBal, _ := ba.GetAvailableBalance("alice", "USDT")
	if aliceBal.Cmp(sc(60)) != 0 {
		t.Fatalf("alice balance = %s, want %s", aliceBal, sc(60))
	}
	insBack, _ := ins.GetBalance(pair)
	if insBack.Cmp(sc(990)) != 0 {
		t.Fatalf("insurance = %s, want %s", insBack, sc(990))
	}
	back, _ := ps.GetPosition("long")
	if back.RealizedPnL.Cmp(sc(10)) != 0 {
		t.Fatalf("realized pnl = %s, want %s", back.RealizedPnL, sc(10))
	}
}

                                                                                                     
  
                                                                                             
                                                                                                       
                                                     
  
                                                                                            
                                                                                                
func TestCheckAndProcessLiquidations_HealthyPositionLastCumPreserved(t *testing.T) {
	e, ps, _, _, mp, cf := newTestEngineFull()
	pair := "BTC_USDT"
	oneUsdt := big.NewInt(100_000_000)

	                                           
	mp.prices[pair] = new(big.Int).Mul(big.NewInt(50000), oneUsdt)
	_ = cf.SetCumulativeFunding(pair, big.NewInt(30), big.NewInt(30))

	                                                                 
	pos := &Position{
		ID: "pos_healthy", Owner: "alice", Pair: pair, Side: Long,
		EntryPrice:            new(big.Int).Mul(big.NewInt(50000), oneUsdt),
		Quantity:              new(big.Int).Set(oneUsdt),         
		Margin:                new(big.Int).Mul(big.NewInt(25000), oneUsdt),
		Status:                StatusOpen,
		OpenHeight:            1,
		LastCumulativeFunding: big.NewInt(0),
	}
	_ = ps.SetPosition(pos)

	                                                                                                
	                              
	_, _, err := e.CheckAndProcessLiquidations(pair)
	if err != nil {
		t.Fatalf("CheckAndProcessLiquidations: %v", err)
	}

	back, _ := ps.GetPosition("pos_healthy")
	if back.LastCumulativeFunding == nil || back.LastCumulativeFunding.Sign() != 0 {
		t.Fatalf("healthy position lastCum polluted: want 0, got %s", back.LastCumulativeFunding)
	}
	if back.Status != StatusOpen {
		t.Fatalf("healthy position should stay Open, got %v", back.Status)
	}
}
