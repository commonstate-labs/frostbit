package db

import (
	"bytes"
	"dex/interfaces"
	"dex/keys"
	"dex/pb"
	"fmt"
	"strings"
)

func extractPriceKeyFromIndexKey(indexKey string) (string, bool) {
	priceMarker := "|price:"
	orderIDMarker := "|order_id:"
	priceStart := strings.Index(indexKey, priceMarker)
	if priceStart < 0 {
		return "", false
	}
	priceStart += len(priceMarker)
	rest := indexKey[priceStart:]
	priceEndOffset := strings.Index(rest, orderIDMarker)
	if priceEndOffset < 0 {
		return "", false
	}
	return rest[:priceEndOffset], true
}

func extractOrderIDFromIndexKeyBytes(indexKey []byte) (string, bool) {
	const marker = "|order_id:"
	idx := bytes.Index(indexKey, []byte(marker))
	if idx < 0 {
		return "", false
	}
	start := idx + len(marker)
	if start >= len(indexKey) {
		return "", false
	}
	return string(indexKey[start:]), true
}

func (manager *Manager) ScanOrderPriceIndexRange(
	pair string, side pb.OrderSide, isFilled bool,
	minPriceKey67 string, maxPriceKey67 string, limit int, reverse bool,
) (map[string][]byte, error) {
	result := make(map[string][]byte)
	if minPriceKey67 == "" || maxPriceKey67 == "" || minPriceKey67 > maxPriceKey67 {
		return result, nil
	}

	prefix := keys.KeyOrderPriceIndexPrefix(pair, side, isFilled)
	prefixBytes := []byte(prefix)
	lowSeek := []byte(fmt.Sprintf("%sprice:%s|order_id:", prefix, minPriceKey67))
	highSeek := []byte(fmt.Sprintf("%sprice:%s|order_id:%c", prefix, maxPriceKey67, 0xFF))

	pairs, err := manager.ScanKVWithLimitOrdered(prefix, 0, reverse)
	if err != nil {
		return nil, err
	}

	count := 0
	if reverse {
		for _, pair := range pairs {
			if limit > 0 && count >= limit {
				break
			}
			k := pair.Key
			priceKey, ok := extractPriceKeyFromIndexKey(k)
			if !ok {
				continue
			}
			if priceKey < minPriceKey67 {
				break
			}
			if priceKey > maxPriceKey67 {
				continue
			}
			result[k] = append([]byte(nil), pair.Value...)
			count++
		}
	} else {
		for _, pair := range pairs {
			if limit > 0 && count >= limit {
				break
			}
			k := pair.Key
			priceKey, ok := extractPriceKeyFromIndexKey(k)
			if !ok {
				continue
			}
			if priceKey < minPriceKey67 {
				continue
			}
			if priceKey > maxPriceKey67 {
				break
			}
			result[k] = append([]byte(nil), pair.Value...)
			count++
		}
	}
	_ = lowSeek
	_ = highSeek
	_ = prefixBytes
	return result, nil
}

                                                                                                           
func (manager *Manager) ScanOrderPriceIndexRangeOrdered(
	pair string, side pb.OrderSide, isFilled bool,
	minPriceKey67 string, maxPriceKey67 string, limit int, reverse bool,
) ([]interfaces.OrderIndexEntry, error) {
	result := make([]interfaces.OrderIndexEntry, 0, limit)
	if minPriceKey67 == "" || maxPriceKey67 == "" || minPriceKey67 > maxPriceKey67 {
		return result, nil
	}

	prefix := keys.KeyOrderPriceIndexPrefix(pair, side, isFilled)
	prefixBytes := []byte(prefix)
	lowSeek := []byte(fmt.Sprintf("%sprice:%s|order_id:", prefix, minPriceKey67))
	highSeek := []byte(fmt.Sprintf("%sprice:%s|order_id:%c", prefix, maxPriceKey67, 0xFF))

	pairs, err := manager.ScanKVWithLimitOrdered(prefix, 0, reverse)
	if err != nil {
		return nil, err
	}

	count := 0
	if reverse {
		for _, pair := range pairs {
			if limit > 0 && count >= limit {
				break
			}
			keyBytes := []byte(pair.Key)
			if bytes.Compare(keyBytes, highSeek) > 0 {
				continue
			}
			if bytes.Compare(keyBytes, lowSeek) < 0 {
				break
			}
			orderID, ok := extractOrderIDFromIndexKeyBytes(keyBytes)
			if !ok {
				continue
			}
			result = append(result, interfaces.OrderIndexEntry{OrderID: orderID, IndexData: append([]byte(nil), pair.Value...)})
			count++
		}
	} else {
		for _, pair := range pairs {
			if limit > 0 && count >= limit {
				break
			}
			keyBytes := []byte(pair.Key)
			if bytes.Compare(keyBytes, lowSeek) < 0 {
				continue
			}
			if bytes.Compare(keyBytes, highSeek) > 0 {
				break
			}
			orderID, ok := extractOrderIDFromIndexKeyBytes(keyBytes)
			if !ok {
				continue
			}
			result = append(result, interfaces.OrderIndexEntry{OrderID: orderID, IndexData: append([]byte(nil), pair.Value...)})
			count++
		}
	}
	_ = prefixBytes
	return result, nil
}

                                                                     
func (manager *Manager) ScanOrdersByPairs(pairs []string) (map[string]map[string][]byte, error) {
	result := make(map[string]map[string][]byte)
	unfilledMarker := "|is_filled:false|"

	for _, pair := range pairs {
		prefix := keys.KeyOrderPriceIndexGeneralPrefix(pair, false)
		pairMap := make(map[string][]byte)
		kvs, err := manager.ScanKVWithLimitOrdered(prefix, 0, false)
		if err != nil {
			return nil, err
		}
		for _, kv := range kvs {
			if !strings.Contains(kv.Key, unfilledMarker) {
				continue
			}
			pairMap[kv.Key] = append([]byte(nil), kv.Value...)
		}
		result[pair] = pairMap
	}
	return result, nil
}
