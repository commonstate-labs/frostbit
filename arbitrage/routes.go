package arbitrage

import (
	"crypto/sha256"
	"dex/pb"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

type routeEdge struct {
	leg   Leg
	level *topLevel
}

                                                                             
                                                                           
                                                                          
func discoverClosedRoutes(snapshot *MarketSnapshot, maxLegs, traversalLimit int, traderAddress string) ([]discoveredRoute, error) {
	if snapshot == nil || maxLegs < 2 || len(snapshot.Levels) == 0 {
		return nil, nil
	}
	if maxLegs > maxClosedRouteLegs {
		maxLegs = maxClosedRouteLegs
	}
	if traversalLimit <= 0 {
		return nil, nil
	}

	edges := make([]routeEdge, 0, len(snapshot.Levels))
	seenBooks := make(map[spotBookID]struct{}, len(snapshot.Levels))
	for i, projected := range snapshot.Levels {
		leg := projected.Leg
		if strings.TrimSpace(leg.BaseToken) == "" || strings.TrimSpace(leg.QuoteToken) == "" || leg.BaseToken == leg.QuoteToken {
			return nil, fmt.Errorf("projected spot level %d has invalid semantic pair %q/%q", i, leg.BaseToken, leg.QuoteToken)
		}
		if leg.Side != pb.OrderSide_BUY && leg.Side != pb.OrderSide_SELL {
			return nil, fmt.Errorf("projected spot level %d has invalid taker side %d", i, leg.Side)
		}
		bookID := legBookID(leg)
		if _, exists := seenBooks[bookID]; exists {
			return nil, fmt.Errorf("duplicate projected spot level for %s/%s side=%d", leg.BaseToken, leg.QuoteToken, leg.Side)
		}
		seenBooks[bookID] = struct{}{}

		price, err := parseUint256("projected price", projected.Price)
		if err != nil {
			return nil, fmt.Errorf("projected spot level %d has invalid price: %w", i, err)
		}
		if price.Sign() <= 0 {
			return nil, fmt.Errorf("projected spot level %d has non-positive price", i)
		}
		level := &topLevel{price: price}
		seenOrders := make(map[string]struct{}, len(projected.Makers))
		for _, maker := range projected.Makers {
			if strings.TrimSpace(maker.OrderID) == "" {
				return nil, fmt.Errorf("projected spot level %d has empty maker order ID", i)
			}
			if _, exists := seenOrders[maker.OrderID]; exists {
				return nil, fmt.Errorf("projected spot level %d repeats maker %s", i, maker.OrderID)
			}
			seenOrders[maker.OrderID] = struct{}{}
			                                                                      
			                                                                  
			if maker.Owner == traderAddress {
				break
			}
			remain, parseErr := parseUint256("projected maker remaining", maker.Remaining)
			if parseErr != nil {
				return nil, fmt.Errorf("projected maker %s has invalid remaining amount: %w", maker.OrderID, parseErr)
			}
			if remain.Sign() <= 0 {
				return nil, fmt.Errorf("projected maker %s has non-positive remaining amount", maker.OrderID)
			}
			if !basePriceProductFits(remain, price) || quoteFromBase(remain, price).Sign() == 0 {
				continue
			}
			level.orders = append(level.orders, bookOrder{orderID: maker.OrderID, owner: maker.Owner, remain: remain})
		}
		if len(level.orders) > 0 {
			edges = append(edges, routeEdge{leg: leg, level: level})
		}
	}

	sort.Slice(edges, func(i, j int) bool {
		left, right := edges[i].leg, edges[j].leg
		if inL, inR := legInputToken(left), legInputToken(right); inL != inR {
			return inL < inR
		}
		if outL, outR := legOutputToken(left), legOutputToken(right); outL != outR {
			return outL < outR
		}
		if left.BaseToken != right.BaseToken {
			return left.BaseToken < right.BaseToken
		}
		if left.QuoteToken != right.QuoteToken {
			return left.QuoteToken < right.QuoteToken
		}
		return left.Side < right.Side
	})
	byInput := make(map[string][]routeEdge)
	for _, edge := range edges {
		byInput[legInputToken(edge.leg)] = append(byInput[legInputToken(edge.leg)], edge)
	}

	routes := make([]discoveredRoute, 0)
	remainingTraversal := traversalLimit
	appendRoute := func(path []routeEdge) {
		legs := make([]Leg, len(path))
		levels := make([]*topLevel, len(path))
		for i, edge := range path {
			legs[i] = edge.leg
			levels[i] = cloneTopLevel(edge.level)
		}
		routes = append(routes, discoveredRoute{
			name:       dynamicRouteName(legs),
			startToken: legInputToken(legs[0]),
			legs:       legs,
			levels:     levels,
		})
	}

	for _, first := range edges {
		start := legInputToken(first.leg)
		for _, second := range byInput[legOutputToken(first.leg)] {
			remainingTraversal--
			if remainingTraversal < 0 {
				return routes, nil
			}
			if legBookID(first.leg) == legBookID(second.leg) {
				continue
			}
			secondOut := legOutputToken(second.leg)
			if secondOut == start {
				appendRoute([]routeEdge{first, second})
				continue
			}
			if maxLegs < 3 || secondOut == legOutputToken(first.leg) {
				continue
			}
			for _, third := range byInput[secondOut] {
				remainingTraversal--
				if remainingTraversal < 0 {
					return routes, nil
				}
				thirdBook := legBookID(third.leg)
				if thirdBook == legBookID(first.leg) || thirdBook == legBookID(second.leg) {
					continue
				}
				if legOutputToken(third.leg) == start {
					appendRoute([]routeEdge{first, second, third})
				}
			}
		}
	}
	return routes, nil
}

func dynamicRouteName(legs []Leg) string {
	h := sha256.New()
	var size [4]byte
	writeField := func(value string) {
		binary.BigEndian.PutUint32(size[:], uint32(len(value)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(value))
	}
	for _, leg := range legs {
		writeField(leg.BaseToken)
		writeField(leg.QuoteToken)
		binary.BigEndian.PutUint32(size[:], uint32(leg.Side))
		_, _ = h.Write(size[:])
	}
	return "dynamic-" + hex.EncodeToString(h.Sum(nil)[:12])
}

func cloneTopLevel(level *topLevel) *topLevel {
	if level == nil {
		return nil
	}
	cloned := &topLevel{orders: make([]bookOrder, len(level.orders))}
	if level.price != nil {
		cloned.price = new(big.Int).Set(level.price)
	}
	for i, order := range level.orders {
		cloned.orders[i] = order
		cloned.orders[i].remain = new(big.Int).Set(order.remain)
	}
	return cloned
}
