package main

import (
	"dex/config"
	"dex/db"
	"dex/logs"
	"dex/pb"
	"dex/utils"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"
)

type sideStats struct {
	Count       int    `json:"count"`
	Old100      int    `json:"old_gt_100_blocks"`
	Old500      int    `json:"old_gt_500_blocks"`
	Far10Pct    int    `json:"far_gt_10pct"`
	Far25Pct    int    `json:"far_gt_25pct"`
	MinPrice    string `json:"min_price,omitempty"`
	MaxPrice    string `json:"max_price,omitempty"`
	TotalRemain string `json:"total_remain"`
}

type pairStats struct {
	Pair       string    `json:"pair"`
	BestBid    string    `json:"best_bid,omitempty"`
	BestAsk    string    `json:"best_ask,omitempty"`
	Bid        sideStats `json:"bid"`
	Ask        sideStats `json:"ask"`
	OpenByAge  ageStats  `json:"open_by_age"`
	PriceRange string    `json:"simulator_price_range,omitempty"`
}

type ageStats struct {
	Blocks0To10    int `json:"blocks_0_10"`
	Blocks11To100  int `json:"blocks_11_100"`
	Blocks101To500 int `json:"blocks_101_500"`
	BlocksOver500  int `json:"blocks_over_500"`
}

type statusCounts struct {
	Open      int `json:"open"`
	Filled    int `json:"filled"`
	Cancelled int `json:"cancelled"`
	Closed    int `json:"closed"`
	Other     int `json:"other"`
}

type blockStats struct {
	Height             uint64 `json:"height"`
	FutureOrderTx      int    `json:"future_order_tx"`
	FutureOrderSucceed int    `json:"future_order_succeed"`
	FutureOrderFailed  int    `json:"future_order_failed"`
	FutureCancelTx     int    `json:"future_cancel_tx"`
	CancelSucceed      int    `json:"cancel_succeed"`
	CancelFailed       int    `json:"cancel_failed"`
	OpenCreatedNow     int    `json:"open_created_now_current_survivors"`
	FilledDeletesEst   int    `json:"filled_delete_est"`
	NetOpenEst         int    `json:"net_open_est"`
}

type orderInfo struct {
	Pair         string
	Side         int32
	Price        *big.Int
	Amount       *big.Int
	Filled       *big.Int
	Status       pb.FutureOrderStatus
	CreateHeight uint64
}

type tradeInfo struct {
	Height uint64
	Amount *big.Int
}

type report struct {
	NodeDataDir         string               `json:"node_data_dir"`
	LatestHeight        uint64               `json:"latest_height"`
	CurrentOpenTotal    int                  `json:"current_open_total"`
	HistoryStatusCounts statusCounts         `json:"history_status_counts"`
	PairStats           []pairStats          `json:"pair_stats"`
	RecentBlocks        []blockStats         `json:"recent_blocks"`
	SimulatorRanges     map[string][2]string `json:"simulator_ranges"`
	Inspect             *inspectReport       `json:"inspect,omitempty"`
	Notes               []string             `json:"notes"`
}

type inspectReport struct {
	Pair    string      `json:"pair"`
	Crossed bool        `json:"crossed"`
	Bid     inspectSide `json:"bid"`
	Ask     inspectSide `json:"ask"`
}

type inspectSide struct {
	Side         string        `json:"side"`
	Sampled      int           `json:"sampled"`
	UniqueOwners int           `json:"unique_owners"`
	MinPrice     string        `json:"min_price,omitempty"`
	MaxPrice     string        `json:"max_price,omitempty"`
	TopOwners    []ownerCount  `json:"top_owners"`
	SampleOrders []orderSample `json:"sample_orders,omitempty"`
}

type ownerCount struct {
	Owner string `json:"owner"`
	Count int    `json:"count"`
}

type orderSample struct {
	OrderID string `json:"order_id"`
	Owner   string `json:"owner"`
	Price   string `json:"price"`
	Remain  string `json:"remain"`
}

func main() {
	dataDir := flag.String("data-dir", "data/node", "node data dir")
	configDir := flag.String("config-dir", "config/env/local20", "config dir")
	recent := flag.Int("recent", 120, "recent blocks to inspect")
	statePebbleDir := flag.String("state-pebble-dir", "", "direct StateDB live/checkpoint pebble dir; scans orderbook state only")
	latestOverride := flag.Uint64("latest-height", 0, "height used for age buckets in state-pebble-dir mode")
	stateShardWidth := flag.Int("state-shard-width", 1, "StateDB shard hex width in state-pebble-dir mode")
	decodeValues := flag.Bool("decode-values", false, "decode StateDB orderbook values in state-pebble-dir mode; slower but includes age/remain")
	inspectPair := flag.String("inspect-pair", "", "pair to inspect top-of-book owner distribution in state-pebble-dir mode")
	inspectLimit := flag.Int("inspect-limit", 2000, "top orders per side to decode for inspect-pair")
	flag.Parse()

	genesis := loadGenesis(*configDir)
	ranges := simulatorRanges(genesis)
	if strings.TrimSpace(*statePebbleDir) != "" {
		latest := *latestOverride
		if latest == 0 {
			latest = inferHeightFromPath(*statePebbleDir)
		}
		var pairStats []pairStats
		if *decodeValues {
			openIndex, err := scanStateOrderbookPebble(*statePebbleDir, *stateShardWidth)
			if err != nil {
				fmt.Fprintf(os.Stderr, "scan state pebble: %v\n", err)
				os.Exit(1)
			}
			pairStats = collectOpenIndexStats(openIndex, latest, ranges)
		} else {
			var err error
			pairStats, err = scanStateOrderbookKeyStats(*statePebbleDir, *stateShardWidth, ranges)
			if err != nil {
				fmt.Fprintf(os.Stderr, "scan state pebble keys: %v\n", err)
				os.Exit(1)
			}
		}
		ageNote := "state-pebble-dir fast mode scans keys only; open_by_age and total_remain are omitted because decoding every protobuf value is much slower."
		if *decodeValues {
			ageNote = "state-pebble-dir decode-values mode includes protobuf-derived open_by_age and total_remain."
		}
		var inspect *inspectReport
		if strings.TrimSpace(*inspectPair) != "" {
			var err error
			inspect, err = inspectStateOrderbookPair(*statePebbleDir, *stateShardWidth, strings.TrimSpace(*inspectPair), *inspectLimit)
			if err != nil {
				fmt.Fprintf(os.Stderr, "inspect state orderbook pair: %v\n", err)
				os.Exit(1)
			}
		}
		writeReport(report{
			NodeDataDir:      *statePebbleDir,
			LatestHeight:     latest,
			CurrentOpenTotal: totalOpen(pairStats),
			PairStats:        pairStats,
			SimulatorRanges:  ranges,
			Inspect:          inspect,
			Notes: []string{
				"state-pebble-dir mode scans only current open maker snapshots from StateDB checkpoint/live pebble.",
				ageNote,
				"history_status_counts and recent_blocks are omitted in this mode to avoid opening locked live runtime/query/archive DBs.",
				"far_gt_10pct/25pct compares bid orders below best ask and ask orders above best bid.",
			},
		})
		return
	}

	cfg, err := config.LoadFromDir(*configDir)
	if err != nil {
		cfg = config.DefaultConfig()
	}
	mgr, err := db.NewReadOnlyManagerWithConfig(*dataDir, logs.NewNodeLogger("diag-orderbook", 0), cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open readonly db: %v\n", err)
		os.Exit(1)
	}
	defer mgr.Close()

	latest, _ := mgr.GetLatestBlockHeight()

	orders, status, pairStats := collectOrders(mgr, latest, ranges)
	blocks := collectRecentBlocks(mgr, latest, *recent, orders)

	out := report{
		NodeDataDir:         *dataDir,
		LatestHeight:        latest,
		CurrentOpenTotal:    totalOpen(pairStats),
		HistoryStatusCounts: status,
		PairStats:           pairStats,
		RecentBlocks:        blocks,
		SimulatorRanges:     ranges,
		Notes: []string{
			"filled_delete_est is estimated from final filled order history plus the last maker trade height.",
			"open_created_now_current_survivors counts orders created at that height that are still open now.",
			"far_gt_10pct/25pct compares bid orders below best ask and ask orders above best bid.",
		},
	}
	writeReport(out)
}

func writeReport(out report) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "encode report: %v\n", err)
		os.Exit(1)
	}
}

func collectOrders(mgr *db.Manager, latest uint64, ranges map[string][2]string) (map[string]orderInfo, statusCounts, []pairStats) {
	history := make(map[string]orderInfo)
	var counts statusCounts
	accountOrders, err := mgr.Scan("v1_future_acc_order_")
	if err == nil {
		for _, raw := range accountOrders {
			var st pb.FutureOrderState
			if proto.Unmarshal(raw, &st) != nil || st.OrderId == "" {
				continue
			}
			info := orderInfo{
				Pair: st.Pair, Side: st.Side, Price: parseInt(st.Price), Amount: parseInt(st.Amount),
				Filled: parseInt(st.FilledBase), Status: st.Status, CreateHeight: st.CreateHeight,
			}
			history[st.OrderId] = info
			switch st.Status {
			case pb.FutureOrderStatus_FUTURE_ORDER_OPEN:
				counts.Open++
			case pb.FutureOrderStatus_FUTURE_ORDER_FILLED:
				counts.Filled++
			case pb.FutureOrderStatus_FUTURE_ORDER_CANCELLED:
				counts.Cancelled++
			case pb.FutureOrderStatus_FUTURE_ORDER_CLOSED:
				counts.Closed++
			default:
				counts.Other++
			}
		}
	}

	openIndex, err := mgr.Scan("v1_future_pair:")
	if err != nil {
		return history, counts, nil
	}
	return history, counts, collectOpenIndexStats(openIndex, latest, ranges)
}

func collectOpenIndexStats(openIndex map[string][]byte, latest uint64, ranges map[string][2]string) []pairStats {
	byPair := make(map[string]*pairStats)
	for key, raw := range openIndex {
		pair, keySide, ok := parseFutureIndexKey(key)
		if !ok {
			continue
		}
		var st pb.FutureOrderState
		if proto.Unmarshal(raw, &st) != nil || st.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN {
			continue
		}
		ps := byPair[pair]
		if ps == nil {
			ps = &pairStats{Pair: pair}
			if r, ok := ranges[pair]; ok {
				ps.PriceRange = r[0] + "-" + r[1]
			}
			byPair[pair] = ps
		}
		side := &ps.Bid
		if keySide == int(pb.OrderSide_SELL) {
			side = &ps.Ask
		}
		price := parseInt(st.Price)
		remain := new(big.Int).Sub(parseInt(st.Amount), parseInt(st.FilledBase))
		if remain.Sign() < 0 {
			remain.SetInt64(0)
		}
		side.Count++
		side.TotalRemain = addStringInts(side.TotalRemain, remain)
		updateMinMax(side, price)
		age := uint64(0)
		if latest >= st.CreateHeight {
			age = latest - st.CreateHeight
		}
		switch {
		case age <= 10:
			ps.OpenByAge.Blocks0To10++
		case age <= 100:
			ps.OpenByAge.Blocks11To100++
		case age <= 500:
			ps.OpenByAge.Blocks101To500++
			side.Old100++
		default:
			ps.OpenByAge.BlocksOver500++
			side.Old100++
			side.Old500++
		}
	}
	for _, ps := range byPair {
		if ps.Bid.MaxPrice != "" {
			ps.BestBid = ps.Bid.MaxPrice
		}
		if ps.Ask.MinPrice != "" {
			ps.BestAsk = ps.Ask.MinPrice
		}
	}
	for key, raw := range openIndex {
		pair, keySide, ok := parseFutureIndexKey(key)
		if !ok {
			continue
		}
		ps := byPair[pair]
		if ps == nil {
			continue
		}
		var st pb.FutureOrderState
		if proto.Unmarshal(raw, &st) != nil || st.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN {
			continue
		}
		price := parseInt(st.Price)
		if keySide == int(pb.OrderSide_BUY) {
			if bestAsk := parseInt(ps.BestAsk); bestAsk.Sign() > 0 {
				markFar(&ps.Bid, price, bestAsk, false)
			}
		} else {
			if bestBid := parseInt(ps.BestBid); bestBid.Sign() > 0 {
				markFar(&ps.Ask, price, bestBid, true)
			}
		}
	}

	out := make([]pairStats, 0, len(byPair))
	for _, ps := range byPair {
		out = append(out, *ps)
	}
	sort.Slice(out, func(i, j int) bool {
		totalI := out[i].Bid.Count + out[i].Ask.Count
		totalJ := out[j].Bid.Count + out[j].Ask.Count
		if totalI != totalJ {
			return totalI > totalJ
		}
		return out[i].Pair < out[j].Pair
	})
	return out
}

func scanStateOrderbookPebble(dir string, shardWidth int) (map[string][]byte, error) {
	if shardWidth != 1 && shardWidth != 2 {
		shardWidth = 1
	}
	pdb, err := pebble.Open(dir, &pebble.Options{ReadOnly: true, MaxOpenFiles: 500})
	if err != nil {
		return nil, err
	}
	defer pdb.Close()

	out := make(map[string][]byte)
	for _, shard := range stateShards(shardWidth) {
		statePrefix := fmt.Sprintf("v1_sdb_state_%s_", shard)
		prefix := []byte(statePrefix + "v1_future_pair:")
		iter, err := pdb.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixUpper(prefix)})
		if err != nil {
			return nil, err
		}
		for ok := iter.SeekGE(prefix); ok; ok = iter.Next() {
			key := string(iter.Key())
			if !strings.HasPrefix(key, statePrefix) {
				continue
			}
			value := append([]byte(nil), iter.Value()...)
			out[key[len(statePrefix):]] = value
		}
		if err := iter.Error(); err != nil {
			_ = iter.Close()
			return nil, err
		}
		if err := iter.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func scanStateOrderbookKeyStats(dir string, shardWidth int, ranges map[string][2]string) ([]pairStats, error) {
	pdb, err := pebble.Open(dir, &pebble.Options{ReadOnly: true, MaxOpenFiles: 500})
	if err != nil {
		return nil, err
	}
	defer pdb.Close()

	byPair := make(map[string]*pairStats)
	if err := iterateStateOrderbookKeys(pdb, shardWidth, func(key string) error {
		pair, keySide, price, ok := parseFutureIndexKeyPrice(key)
		if !ok {
			return nil
		}
		ps := byPair[pair]
		if ps == nil {
			ps = &pairStats{Pair: pair}
			if r, ok := ranges[pair]; ok {
				ps.PriceRange = r[0] + "-" + r[1]
			}
			byPair[pair] = ps
		}
		side := &ps.Bid
		if keySide == int(pb.OrderSide_SELL) {
			side = &ps.Ask
		}
		side.Count++
		updateMinMax(side, price)
		return nil
	}); err != nil {
		return nil, err
	}
	for _, ps := range byPair {
		if ps.Bid.MaxPrice != "" {
			ps.BestBid = ps.Bid.MaxPrice
		}
		if ps.Ask.MinPrice != "" {
			ps.BestAsk = ps.Ask.MinPrice
		}
	}
	if err := iterateStateOrderbookKeys(pdb, shardWidth, func(key string) error {
		pair, keySide, price, ok := parseFutureIndexKeyPrice(key)
		if !ok {
			return nil
		}
		ps := byPair[pair]
		if ps == nil {
			return nil
		}
		if keySide == int(pb.OrderSide_BUY) {
			if bestAsk := parseInt(ps.BestAsk); bestAsk.Sign() > 0 {
				markFar(&ps.Bid, price, bestAsk, false)
			}
		} else if bestBid := parseInt(ps.BestBid); bestBid.Sign() > 0 {
			markFar(&ps.Ask, price, bestBid, true)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	out := make([]pairStats, 0, len(byPair))
	for _, ps := range byPair {
		out = append(out, *ps)
	}
	sort.Slice(out, func(i, j int) bool {
		totalI := out[i].Bid.Count + out[i].Ask.Count
		totalJ := out[j].Bid.Count + out[j].Ask.Count
		if totalI != totalJ {
			return totalI > totalJ
		}
		return out[i].Pair < out[j].Pair
	})
	return out, nil
}

func inspectStateOrderbookPair(dir string, shardWidth int, pair string, limit int) (*inspectReport, error) {
	if limit <= 0 {
		limit = 2000
	}
	pdb, err := pebble.Open(dir, &pebble.Options{ReadOnly: true, MaxOpenFiles: 500})
	if err != nil {
		return nil, err
	}
	defer pdb.Close()
	bid, err := inspectStateOrderbookSide(pdb, shardWidth, pair, pb.OrderSide_BUY, limit, true)
	if err != nil {
		return nil, err
	}
	ask, err := inspectStateOrderbookSide(pdb, shardWidth, pair, pb.OrderSide_SELL, limit, false)
	if err != nil {
		return nil, err
	}
	return &inspectReport{
		Pair:    pair,
		Crossed: parseInt(bid.MaxPrice).Sign() > 0 && parseInt(ask.MinPrice).Sign() > 0 && parseInt(bid.MaxPrice).Cmp(parseInt(ask.MinPrice)) >= 0,
		Bid:     bid,
		Ask:     ask,
	}, nil
}

type inspectCandidate struct {
	key   string
	price *big.Int
	value []byte
}

func inspectStateOrderbookSide(pdb *pebble.DB, shardWidth int, pair string, side pb.OrderSide, limit int, reverse bool) (inspectSide, error) {
	prefixBiz := fmt.Sprintf("v1_future_pair:%s|side:%d|is_filled:false|", pair, int(side))
	candidates := make([]inspectCandidate, 0, limit*len(stateShards(shardWidth)))
	for _, shard := range stateShards(shardWidth) {
		statePrefix := fmt.Sprintf("v1_sdb_state_%s_", shard)
		prefix := []byte(statePrefix + prefixBiz)
		upper := prefixUpper(prefix)
		iter, err := pdb.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: upper})
		if err != nil {
			return inspectSide{}, err
		}
		taken := 0
		if reverse {
			for ok := iter.SeekLT(upper); ok && taken < limit; ok = iter.Prev() {
				key := string(iter.Key())
				if !strings.HasPrefix(key, string(prefix)) {
					break
				}
				_, _, price, ok := parseFutureIndexKeyPrice(key[len(statePrefix):])
				if ok {
					candidates = append(candidates, inspectCandidate{key: key[len(statePrefix):], price: price, value: append([]byte(nil), iter.Value()...)})
					taken++
				}
			}
		} else {
			for ok := iter.SeekGE(prefix); ok && taken < limit; ok = iter.Next() {
				key := string(iter.Key())
				if !strings.HasPrefix(key, string(prefix)) {
					break
				}
				_, _, price, ok := parseFutureIndexKeyPrice(key[len(statePrefix):])
				if ok {
					candidates = append(candidates, inspectCandidate{key: key[len(statePrefix):], price: price, value: append([]byte(nil), iter.Value()...)})
					taken++
				}
			}
		}
		if err := iter.Error(); err != nil {
			_ = iter.Close()
			return inspectSide{}, err
		}
		if err := iter.Close(); err != nil {
			return inspectSide{}, err
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		cmp := candidates[i].price.Cmp(candidates[j].price)
		if cmp == 0 {
			return candidates[i].key < candidates[j].key
		}
		if reverse {
			return cmp > 0
		}
		return cmp < 0
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := inspectSide{Side: "bid"}
	if side == pb.OrderSide_SELL {
		out.Side = "ask"
	}
	owners := make(map[string]int)
	for _, c := range candidates {
		updateInspectMinMax(&out, c.price)
		var st pb.FutureOrderState
		if proto.Unmarshal(c.value, &st) != nil || st.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN {
			continue
		}
		owner := st.OwnerAddress
		if owner == "" {
			owner = st.Owner
		}
		if owner == "" {
			owner = "<empty>"
		}
		owners[owner]++
		out.Sampled++
		if len(out.SampleOrders) < 20 {
			remain := new(big.Int).Sub(parseInt(st.Amount), parseInt(st.FilledBase))
			if remain.Sign() < 0 {
				remain.SetInt64(0)
			}
			out.SampleOrders = append(out.SampleOrders, orderSample{
				OrderID: st.OrderId,
				Owner:   owner,
				Price:   st.Price,
				Remain:  remain.String(),
			})
		}
	}
	out.UniqueOwners = len(owners)
	out.TopOwners = topOwnerCounts(owners, 10)
	return out, nil
}

func updateInspectMinMax(s *inspectSide, price *big.Int) {
	if price == nil || price.Sign() <= 0 {
		return
	}
	if s.MinPrice == "" || price.Cmp(parseInt(s.MinPrice)) < 0 {
		s.MinPrice = price.String()
	}
	if s.MaxPrice == "" || price.Cmp(parseInt(s.MaxPrice)) > 0 {
		s.MaxPrice = price.String()
	}
}

func topOwnerCounts(owners map[string]int, limit int) []ownerCount {
	out := make([]ownerCount, 0, len(owners))
	for owner, count := range owners {
		out = append(out, ownerCount{Owner: owner, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Owner < out[j].Owner
	})
	if len(out) > limit {
		return out[:limit]
	}
	return out
}

func iterateStateOrderbookKeys(pdb *pebble.DB, shardWidth int, fn func(key string) error) error {
	if shardWidth != 1 && shardWidth != 2 {
		shardWidth = 1
	}
	for _, shard := range stateShards(shardWidth) {
		statePrefix := fmt.Sprintf("v1_sdb_state_%s_", shard)
		prefix := []byte(statePrefix + "v1_future_pair:")
		iter, err := pdb.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixUpper(prefix)})
		if err != nil {
			return err
		}
		for ok := iter.SeekGE(prefix); ok; ok = iter.Next() {
			key := string(iter.Key())
			if strings.HasPrefix(key, statePrefix) {
				if err := fn(key[len(statePrefix):]); err != nil {
					_ = iter.Close()
					return err
				}
			}
		}
		if err := iter.Error(); err != nil {
			_ = iter.Close()
			return err
		}
		if err := iter.Close(); err != nil {
			return err
		}
	}
	return nil
}

func stateShards(width int) []string {
	n := 16
	format := "%x"
	if width == 2 {
		n = 256
		format = "%02x"
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf(format, i))
	}
	return out
}

func prefixUpper(prefix []byte) []byte {
	upper := append([]byte(nil), prefix...)
	for i := len(upper) - 1; i >= 0; i-- {
		if upper[i] != 0xff {
			upper[i]++
			return upper[:i+1]
		}
	}
	return nil
}

func inferHeightFromPath(path string) uint64 {
	base := filepath.Base(strings.TrimRight(path, "/\\"))
	if !strings.HasPrefix(base, "h_") {
		return 0
	}
	h, _ := strconv.ParseUint(strings.TrimPrefix(base, "h_"), 10, 64)
	return h
}

func collectRecentBlocks(mgr *db.Manager, latest uint64, recent int, orders map[string]orderInfo) []blockStats {
	if recent <= 0 {
		recent = 120
	}
	from := uint64(1)
	if latest > uint64(recent) {
		from = latest - uint64(recent) + 1
	}
	byHeight := make(map[uint64]*blockStats)
	for h := from; h <= latest; h++ {
		byHeight[h] = &blockStats{Height: h}
	}
	for _, info := range orders {
		if info.Status == pb.FutureOrderStatus_FUTURE_ORDER_OPEN && info.CreateHeight >= from && info.CreateHeight <= latest {
			byHeight[info.CreateHeight].OpenCreatedNow++
		}
	}
	addFilledDeleteEstimates(mgr, from, latest, orders, byHeight)
	for h := from; h <= latest; h++ {
		blk, err := mgr.GetBlock(h)
		if err != nil || blk == nil {
			continue
		}
		bs := byHeight[h]
		for _, tx := range blk.Body {
			if tx == nil {
				continue
			}
			if fut := tx.GetFutureOrderTx(); fut != nil {
				bs.FutureOrderTx++
				switch fut.GetBase().GetStatus() {
				case pb.Status_SUCCEED:
					bs.FutureOrderSucceed++
				case pb.Status_FAILED:
					bs.FutureOrderFailed++
				}
			}
			if cancel := tx.GetFutureOrderCancelTx(); cancel != nil {
				bs.FutureCancelTx++
				switch cancel.GetBase().GetStatus() {
				case pb.Status_SUCCEED:
					bs.CancelSucceed++
				case pb.Status_FAILED:
					bs.CancelFailed++
				}
			}
		}
		bs.NetOpenEst = bs.OpenCreatedNow - bs.FilledDeletesEst - bs.CancelSucceed
	}
	out := make([]blockStats, 0, len(byHeight))
	for h := from; h <= latest; h++ {
		out = append(out, *byHeight[h])
	}
	return out
}

func addFilledDeleteEstimates(mgr *db.Manager, from, latest uint64, orders map[string]orderInfo, byHeight map[uint64]*blockStats) {
	trades, err := mgr.Scan("v1_future_trade_")
	if err != nil {
		return
	}
	lastMakerTrade := make(map[string]uint64)
	for _, raw := range trades {
		var tr pb.TradeRecord
		if proto.Unmarshal(raw, &tr) != nil || tr.MakerOrderId == "" {
			continue
		}
		if tr.Height >= from && tr.Height <= latest && tr.Height >= lastMakerTrade[tr.MakerOrderId] {
			lastMakerTrade[tr.MakerOrderId] = tr.Height
		}
	}
	for oid, h := range lastMakerTrade {
		info := orders[oid]
		if info.Status == pb.FutureOrderStatus_FUTURE_ORDER_FILLED {
			byHeight[h].FilledDeletesEst++
		}
	}
}

func parseFutureIndexKey(key string) (string, int, bool) {
	pair, side, _, ok := parseFutureIndexKeyPrice(key)
	return pair, side, ok
}

func parseFutureIndexKeyPrice(key string) (string, int, *big.Int, bool) {
	if !strings.HasPrefix(key, "v1_future_pair:") {
		return "", 0, nil, false
	}
	const pairPrefix = "v1_future_pair:"
	sideMarker := "|side:"
	filledMarker := "|is_filled:"
	priceMarker := "|price:"
	orderMarker := "|order_id:"
	sideAt := strings.Index(key, sideMarker)
	filledAt := strings.Index(key, filledMarker)
	priceAt := strings.Index(key, priceMarker)
	orderAt := strings.Index(key, orderMarker)
	if sideAt <= len(pairPrefix) || filledAt < 0 || priceAt < 0 || sideAt > filledAt {
		return "", 0, nil, false
	}
	side, err := strconv.Atoi(key[sideAt+len(sideMarker) : filledAt])
	if err != nil {
		return "", 0, nil, false
	}
	priceEnd := len(key)
	if orderAt > priceAt {
		priceEnd = orderAt
	}
	price := parseInt(key[priceAt+len(priceMarker) : priceEnd])
	return key[len(pairPrefix):sideAt], side, price, true
}

func parseInt(s string) *big.Int {
	v := new(big.Int)
	if strings.TrimSpace(s) == "" {
		return v
	}
	if _, ok := v.SetString(strings.TrimSpace(s), 10); !ok {
		return new(big.Int)
	}
	return v
}

func addStringInts(cur string, add *big.Int) string {
	sum := parseInt(cur)
	sum.Add(sum, add)
	return sum.String()
}

func updateMinMax(s *sideStats, price *big.Int) {
	if price == nil || price.Sign() <= 0 {
		return
	}
	if s.MinPrice == "" || price.Cmp(parseInt(s.MinPrice)) < 0 {
		s.MinPrice = price.String()
	}
	if s.MaxPrice == "" || price.Cmp(parseInt(s.MaxPrice)) > 0 {
		s.MaxPrice = price.String()
	}
}

func markFar(s *sideStats, price, oppositeBest *big.Int, ask bool) {
	if price == nil || oppositeBest == nil || price.Sign() <= 0 || oppositeBest.Sign() <= 0 {
		return
	}
	if ask {
		if pctDiff(price, oppositeBest) > 0.10 {
			s.Far10Pct++
		}
		if pctDiff(price, oppositeBest) > 0.25 {
			s.Far25Pct++
		}
		return
	}
	if pctDiff(oppositeBest, price) > 0.10 {
		s.Far10Pct++
	}
	if pctDiff(oppositeBest, price) > 0.25 {
		s.Far25Pct++
	}
}

func pctDiff(a, b *big.Int) float64 {
	if b == nil || b.Sign() <= 0 {
		return 0
	}
	af, _ := new(big.Float).SetInt(a).Float64()
	bf, _ := new(big.Float).SetInt(b).Float64()
	if bf == 0 || math.IsInf(af, 0) || math.IsInf(bf, 0) {
		return 0
	}
	return (af - bf) / bf
}

func totalOpen(stats []pairStats) int {
	total := 0
	for _, ps := range stats {
		total += ps.Bid.Count + ps.Ask.Count
	}
	return total
}

func loadGenesis(configDir string) *config.GenesisConfig {
	path := strings.TrimRight(configDir, "/\\") + "/genesis.json"
	cfg, err := config.LoadGenesisConfig(path)
	if err != nil {
		return config.DefaultGenesisConfig()
	}
	return cfg
}

func simulatorRanges(genesisCfg *config.GenesisConfig) map[string][2]string {
	out := make(map[string][2]string)
	for _, p := range buildSimTradingPairsLocal(genesisCfg) {
		out[p.pairKey()] = [2]string{strconv.FormatInt(p.MinPrice, 10), strconv.FormatInt(p.MaxPrice, 10)}
	}
	return out
}

type simPairLocal struct {
	BaseToken, QuoteToken string
	MinPrice, MaxPrice    int64
}

func (p simPairLocal) pairKey() string {
	return utils.GeneratePairKey(p.BaseToken, p.QuoteToken)
}

func buildSimTradingPairsLocal(genesisCfg *config.GenesisConfig) []simPairLocal {
	const simUnit = int64(100_000_000)
	if genesisCfg == nil || len(genesisCfg.Tokens) == 0 {
		genesisCfg = config.DefaultGenesisConfig()
	}
	tokenSet := make(map[string]bool, len(genesisCfg.Tokens))
	for _, token := range genesisCfg.Tokens {
		if token.Address != "" {
			tokenSet[token.Address] = true
		}
	}
	balanceSet := make(map[string]bool)
	if alloc, ok := genesisCfg.Alloc["default"]; ok {
		for token, bal := range alloc.Balances {
			if parseInt(bal.Balance).Sign() > 0 {
				balanceSet[token] = true
			}
		}
	}
	useBalanceFilter := len(balanceSet) > 0
	profiles := []struct {
		base               string
		minPrice, maxPrice int64
	}{
		{base: "FB", minPrice: 10 * simUnit, maxPrice: 20 * simUnit},
		{base: "BTC", minPrice: 60_000 * simUnit, maxPrice: 70_000 * simUnit},
		{base: "ETH", minPrice: 2_500 * simUnit, maxPrice: 3_500 * simUnit},
		{base: "TRX", minPrice: simUnit / 10, maxPrice: simUnit / 5},
	}
	quotes := []string{"ETH.USDT", "ETH.USDC", "BASE.USDC", "ARB.USDC", "BSC.USDT", "BSC.USDC", "ARB.USDT", "TRX.USDT", "ETH.DAI"}
	pairs := make([]simPairLocal, 0, len(profiles)*len(quotes))
	for _, profile := range profiles {
		if !tokenSet[profile.base] || (useBalanceFilter && !balanceSet[profile.base]) {
			continue
		}
		for _, quote := range quotes {
			if !tokenSet[quote] || (useBalanceFilter && !balanceSet[quote]) {
				continue
			}
			pairs = append(pairs, simPairLocal{
				BaseToken: profile.base, QuoteToken: quote,
				MinPrice: profile.minPrice, MaxPrice: profile.maxPrice,
			})
		}
	}
	return pairs
}
