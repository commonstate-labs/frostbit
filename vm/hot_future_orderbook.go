package vm

import (
	"dex/pb"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
)

type hotFutureOrderbookIndex struct {
	prefix string
	pair   string
	side   int32
	levels []hotFuturePriceLevel
	count  int
}

type hotFuturePriceLevel struct {
	priceKey string
	orders   []hotFutureOrderSnapshot
}

type hotFutureOrderSnapshot struct {
	indexOrderID    string
	stateOrderID    string
	owner           string
	ownerAddress    string
	amount          string
	filledBase      string
	filledQuote     string
	leverage        uint64
	isClose         bool
	closePositionID string
	status          pb.FutureOrderStatus
	createHeight    uint64
	side            int32
	raw             []byte
}

type futureOrderbookEntry struct {
	priceKey string
	orderID  string
	snapshot hotFutureOrderSnapshot
}

func (e futureOrderbookEntry) indexKey(prefix string) string {
	return prefix + "price:" + e.priceKey + "|order_id:" + e.orderID
}

func newHotFutureOrderbookIndex(prefix string, kvs map[string][]byte) *hotFutureOrderbookIndex {
	pair, side, _ := parseFutureOrderbookPrefix(prefix)
	idx := &hotFutureOrderbookIndex{prefix: prefix, pair: pair, side: side}
	for k, v := range kvs {
		pfx, priceKey, orderID, ok := parseFutureOrderbookKey(k)
		if !ok || pfx != prefix {
			continue
		}
		idx.upsert(priceKey, compactFutureOrderSnapshot(orderID, side, v))
	}
	return idx
}

func (idx *hotFutureOrderbookIndex) toMap() map[string][]byte {
	out := make(map[string][]byte, idx.count)
	for li := range idx.levels {
		level := &idx.levels[li]
		for oi := range level.orders {
			out[idx.keyFor(level.priceKey, level.orders[oi].indexOrderID)] = level.orders[oi].stateData(idx.pair, level.priceKey)
		}
	}
	return out
}

func (idx *hotFutureOrderbookIndex) scanSnapshotsAfter(startAfter string, limit int, reverse bool) []futureOrderbookEntry {
	if idx == nil || idx.count == 0 {
		return nil
	}
	out := make([]futureOrderbookEntry, 0, scanOutputCap(idx.count, limit))
	idx.forEachAfter(startAfter, reverse, func(level *hotFuturePriceLevel, order *hotFutureOrderSnapshot) bool {
		out = append(out, futureOrderbookEntry{
			priceKey: level.priceKey,
			orderID:  order.indexOrderID,
			snapshot: *order,
		})
		return limit <= 0 || len(out) < limit
	})
	return out
}

func (idx *hotFutureOrderbookIndex) visitSnapshotsAfter(startAfter string, limit int, reverse bool, visit FutureOrderbookVisitFn) {
	if idx == nil || idx.count == 0 || visit == nil {
		return
	}
	count := 0
	idx.forEachAfter(startAfter, reverse, func(level *hotFuturePriceLevel, order *hotFutureOrderSnapshot) bool {
		count++
		if !visit(futureOrderbookEntry{
			priceKey: level.priceKey,
			orderID:  order.indexOrderID,
			snapshot: *order,
		}) {
			return false
		}
		return limit <= 0 || count < limit
	})
}

func (idx *hotFutureOrderbookIndex) scanOrderedAfter(startAfter string, limit int, reverse bool) []KVPair {
	if idx == nil || idx.count == 0 {
		return nil
	}
	out := make([]KVPair, 0, scanOutputCap(idx.count, limit))
	idx.forEachAfter(startAfter, reverse, func(level *hotFuturePriceLevel, order *hotFutureOrderSnapshot) bool {
		out = append(out, KVPair{
			Key:   idx.keyFor(level.priceKey, order.indexOrderID),
			Value: order.stateData(idx.pair, level.priceKey),
		})
		return limit <= 0 || len(out) < limit
	})
	return out
}

func (idx *hotFutureOrderbookIndex) scanKeysOrderedAfter(startAfter string, limit int, reverse bool) []string {
	if idx == nil || idx.count == 0 {
		return nil
	}
	out := make([]string, 0, scanOutputCap(idx.count, limit))
	idx.forEachAfter(startAfter, reverse, func(level *hotFuturePriceLevel, order *hotFutureOrderSnapshot) bool {
		out = append(out, idx.keyFor(level.priceKey, order.indexOrderID))
		return limit <= 0 || len(out) < limit
	})
	return out
}

func (idx *hotFutureOrderbookIndex) apply(op WriteOp) {
	prefix, priceKey, orderID, ok := parseFutureOrderbookKey(op.Key)
	if !ok || prefix != idx.prefix {
		return
	}
	if op.Del {
		idx.remove(priceKey, orderID)
		return
	}
	idx.upsert(priceKey, compactFutureOrderSnapshot(orderID, idx.side, op.Value))
}

func (idx *hotFutureOrderbookIndex) forEachAfter(startAfter string, reverse bool, visit func(*hotFuturePriceLevel, *hotFutureOrderSnapshot) bool) {
	if reverse {
		li, oi := idx.reverseStart(startAfter)
		for ; li >= 0; li-- {
			level := &idx.levels[li]
			if oi >= len(level.orders) {
				oi = len(level.orders) - 1
			}
			for ; oi >= 0; oi-- {
				if !visit(level, &level.orders[oi]) {
					return
				}
			}
			oi = int(^uint(0) >> 1)
		}
		return
	}
	li, oi := idx.forwardStart(startAfter)
	for ; li < len(idx.levels); li++ {
		level := &idx.levels[li]
		for ; oi < len(level.orders); oi++ {
			if !visit(level, &level.orders[oi]) {
				return
			}
		}
		oi = 0
	}
}

func (idx *hotFutureOrderbookIndex) forwardStart(startAfter string) (int, int) {
	if startAfter == "" {
		return 0, 0
	}
	prefix, priceKey, orderID, ok := parseFutureOrderbookKey(startAfter)
	if !ok || prefix != idx.prefix {
		return 0, 0
	}
	li := sort.Search(len(idx.levels), func(i int) bool { return idx.levels[i].priceKey >= priceKey })
	if li >= len(idx.levels) || idx.levels[li].priceKey != priceKey {
		return li, 0
	}
	orders := idx.levels[li].orders
	oi := sort.Search(len(orders), func(i int) bool { return orders[i].indexOrderID > orderID })
	if oi >= len(orders) {
		return li + 1, 0
	}
	return li, oi
}

func (idx *hotFutureOrderbookIndex) reverseStart(startAfter string) (int, int) {
	if startAfter == "" {
		li := len(idx.levels) - 1
		return li, len(idx.levels[li].orders) - 1
	}
	prefix, priceKey, orderID, ok := parseFutureOrderbookKey(startAfter)
	if !ok || prefix != idx.prefix {
		li := len(idx.levels) - 1
		return li, len(idx.levels[li].orders) - 1
	}
	li := sort.Search(len(idx.levels), func(i int) bool { return idx.levels[i].priceKey >= priceKey })
	if li >= len(idx.levels) {
		li = len(idx.levels) - 1
		return li, len(idx.levels[li].orders) - 1
	}
	if idx.levels[li].priceKey > priceKey {
		li--
		if li < 0 {
			return -1, -1
		}
		return li, len(idx.levels[li].orders) - 1
	}
	orders := idx.levels[li].orders
	oi := sort.Search(len(orders), func(i int) bool { return orders[i].indexOrderID >= orderID }) - 1
	if oi >= 0 {
		return li, oi
	}
	li--
	if li < 0 {
		return -1, -1
	}
	return li, len(idx.levels[li].orders) - 1
}

func (idx *hotFutureOrderbookIndex) upsert(priceKey string, order hotFutureOrderSnapshot) {
	if priceKey == "" || order.indexOrderID == "" {
		return
	}
	li := sort.Search(len(idx.levels), func(i int) bool { return idx.levels[i].priceKey >= priceKey })
	if li >= len(idx.levels) || idx.levels[li].priceKey != priceKey {
		idx.levels = append(idx.levels, hotFuturePriceLevel{})
		copy(idx.levels[li+1:], idx.levels[li:])
		idx.levels[li] = hotFuturePriceLevel{priceKey: priceKey}
	}
	level := &idx.levels[li]
	oi := sort.Search(len(level.orders), func(i int) bool { return level.orders[i].indexOrderID >= order.indexOrderID })
	if oi < len(level.orders) && level.orders[oi].indexOrderID == order.indexOrderID {
		level.orders[oi] = order
		return
	}
	level.orders = append(level.orders, hotFutureOrderSnapshot{})
	copy(level.orders[oi+1:], level.orders[oi:])
	level.orders[oi] = order
	idx.count++
}

func (idx *hotFutureOrderbookIndex) remove(priceKey, orderID string) {
	li := sort.Search(len(idx.levels), func(i int) bool { return idx.levels[i].priceKey >= priceKey })
	if li >= len(idx.levels) || idx.levels[li].priceKey != priceKey {
		return
	}
	level := &idx.levels[li]
	oi := sort.Search(len(level.orders), func(i int) bool { return level.orders[i].indexOrderID >= orderID })
	if oi >= len(level.orders) || level.orders[oi].indexOrderID != orderID {
		return
	}
	copy(level.orders[oi:], level.orders[oi+1:])
	level.orders[len(level.orders)-1] = hotFutureOrderSnapshot{}
	level.orders = level.orders[:len(level.orders)-1]
	idx.count--
	if len(level.orders) > 0 {
		return
	}
	copy(idx.levels[li:], idx.levels[li+1:])
	idx.levels[len(idx.levels)-1] = hotFuturePriceLevel{}
	idx.levels = idx.levels[:len(idx.levels)-1]
}

func (idx *hotFutureOrderbookIndex) keyFor(priceKey, orderID string) string {
	return idx.prefix + "price:" + priceKey + "|order_id:" + orderID
}

func compactFutureOrderSnapshot(indexOrderID string, fallbackSide int32, data []byte) hotFutureOrderSnapshot {
	out := hotFutureOrderSnapshot{indexOrderID: indexOrderID, side: fallbackSide}
	if len(data) == 0 {
		return out
	}
	var st pb.FutureOrderState
	if err := proto.Unmarshal(data, &st); err != nil {
		out.raw = cloneBytes(data)
		return out
	}
	out.stateOrderID = st.OrderId
	if out.stateOrderID == "" {
		out.stateOrderID = indexOrderID
	}
	out.owner = st.Owner
	out.ownerAddress = st.OwnerAddress
	out.amount = st.Amount
	out.filledBase = st.FilledBase
	out.filledQuote = st.FilledQuote
	out.leverage = st.Leverage
	out.isClose = st.IsClose
	out.closePositionID = st.ClosePositionId
	out.status = st.Status
	out.createHeight = st.CreateHeight
	if st.Side != 0 {
		out.side = st.Side
	}
	return out
}

func (o *hotFutureOrderSnapshot) stateData(pair, priceKey string) []byte {
	if o.raw != nil {
		return cloneBytes(o.raw)
	}
	if o.stateOrderID == "" && o.owner == "" && o.amount == "" {
		return nil
	}
	price := ""
	if p := priceIntFromKey128(priceKey); p != nil {
		price = p.String()
	}
	st := &pb.FutureOrderState{
		OrderId:         o.stateOrderID,
		Owner:           o.owner,
		Pair:            pair,
		Side:            o.side,
		Price:           price,
		Amount:          o.amount,
		FilledBase:      o.filledBase,
		FilledQuote:     o.filledQuote,
		Leverage:        o.leverage,
		IsClose:         o.isClose,
		ClosePositionId: o.closePositionID,
		Status:          o.status,
		OwnerAddress:    o.ownerAddress,
		CreateHeight:    o.createHeight,
	}
	data, err := proto.Marshal(st)
	if err != nil {
		return nil
	}
	return data
}

func (o *hotFutureOrderSnapshot) futureState(pair, priceKey string) *pb.FutureOrderState {
	if o == nil {
		return nil
	}
	orderID := o.stateOrderID
	if orderID == "" {
		orderID = o.indexOrderID
	}
	price := ""
	if p := priceIntFromKey128(priceKey); p != nil {
		price = p.String()
	}
	return &pb.FutureOrderState{
		OrderId:         orderID,
		Owner:           o.owner,
		Pair:            pair,
		Side:            o.side,
		Price:           price,
		Amount:          o.amount,
		FilledBase:      o.filledBase,
		FilledQuote:     o.filledQuote,
		Leverage:        o.leverage,
		IsClose:         o.isClose,
		ClosePositionId: o.closePositionID,
		Status:          o.status,
		OwnerAddress:    o.ownerAddress,
		CreateHeight:    o.createHeight,
	}
}

func (o *hotFutureOrderSnapshot) makerSnapshot(pair, priceKey string) (*makerOrderSnapshot, error) {
	if o == nil {
		return nil, fmt.Errorf("missing future order snapshot")
	}
	if o.raw != nil {
		return parseMakerOrderSnapshot(o.raw, MarketFuture)
	}
	orderID := o.stateOrderID
	if orderID == "" {
		orderID = o.indexOrderID
	}
	if orderID == "" || o.status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN {
		return nil, fmt.Errorf("future order not open")
	}
	amount, ok := parsePositiveIntString(o.amount)
	if !ok {
		return nil, fmt.Errorf("invalid amount")
	}
	filled := new(big.Int)
	if o.filledBase != "" {
		filled.SetString(o.filledBase, 10)
	}
	remain := new(big.Int).Sub(amount, filled) // safe-math-ok: compact maker 快照的剩余量，下一步拒绝非正结果
	if remain.Sign() <= 0 {
		return nil, fmt.Errorf("fully filled")
	}
	price := priceIntFromKey128(priceKey)
	if price == nil || price.Sign() <= 0 {
		return nil, fmt.Errorf("invalid price")
	}
	owner := o.owner
	if owner == "" {
		owner = o.ownerAddress
	}
	priceString := price.String()
	return &makerOrderSnapshot{
		remain: remain, price: price, owner: owner,
		side: o.side, leverage: o.leverage, future: &pb.FutureOrderState{
			OrderId:         orderID,
			Owner:           o.owner,
			Pair:            pair,
			Side:            o.side,
			Price:           priceString,
			Amount:          o.amount,
			FilledBase:      o.filledBase,
			FilledQuote:     o.filledQuote,
			Leverage:        o.leverage,
			IsClose:         o.isClose,
			ClosePositionId: o.closePositionID,
			Status:          o.status,
			OwnerAddress:    o.ownerAddress,
			CreateHeight:    o.createHeight,
		},
	}, nil
}

func isFutureOrderbookPriceIndexPrefix(prefix string) bool {
	_, _, ok := parseFutureOrderbookPrefix(prefix)
	return ok
}

func parseFutureOrderbookPrefix(prefix string) (string, int32, bool) {
	if !strings.HasPrefix(prefix, "v1_future_pair:") {
		return "", 0, false
	}
	const pairMarker = "v1_future_pair:"
	const sideMarker = "|side:"
	const filledMarker = "|is_filled:"
	sidePos := strings.Index(prefix[len(pairMarker):], sideMarker)
	if sidePos < 0 {
		return "", 0, false
	}
	sidePos += len(pairMarker)
	pair := prefix[len(pairMarker):sidePos]
	filledPos := strings.Index(prefix[sidePos+len(sideMarker):], filledMarker)
	if pair == "" || filledPos < 0 {
		return "", 0, false
	}
	filledPos += sidePos + len(sideMarker)
	sideValue := prefix[sidePos+len(sideMarker) : filledPos]
	sideNum, err := strconv.Atoi(sideValue)
	if err != nil {
		return "", 0, false
	}
	if !strings.HasSuffix(prefix, "|") {
		return "", 0, false
	}
	if sideNum == int(pb.OrderSide_SELL) {
		return pair, 2, true
	}
	return pair, 1, true
}

func parseFutureOrderbookKey(key string) (string, string, string, bool) {
	if !strings.HasPrefix(key, "v1_future_pair:") {
		return "", "", "", false
	}
	pricePos := strings.Index(key, "|price:")
	if pricePos <= 0 {
		return "", "", "", false
	}
	prefix := key[:pricePos+1]
	rest := key[pricePos+len("|price:"):]
	const orderMarker = "|order_id:"
	if len(rest) <= 67+len(orderMarker) || rest[67:67+len(orderMarker)] != orderMarker {
		return "", "", "", false
	}
	priceKey := rest[:67]
	orderID := rest[67+len(orderMarker):]
	if priceKey == "" || orderID == "" || !isFutureOrderbookPriceIndexPrefix(prefix) {
		return "", "", "", false
	}
	return prefix, priceKey, orderID, true
}
