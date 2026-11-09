package vm

import "sort"

type orderedStateScanner interface {
	ScanWithLimitOrdered(prefix string, limit int, reverse bool) ([]KVPair, error)
}

type orderedStateScannerAfter interface {
	ScanWithLimitOrderedAfter(prefix, startAfter string, limit int, reverse bool) ([]KVPair, error)
}

type orderedStateKeyScanner interface {
	ScanKeysWithLimitOrdered(prefix string, limit int, reverse bool) ([]string, error)
}

type orderedStateKeyScannerAfter interface {
	ScanKeysWithLimitOrderedAfter(prefix, startAfter string, limit int, reverse bool) ([]string, error)
}

type futureOrderbookSnapshotScanner interface {
	ScanFutureOrderbookSnapshots(prefix, startAfter string, limit int, reverse bool) ([]futureOrderbookEntry, bool, error)
}

type futureOrderbookSnapshotVisitor interface {
	VisitFutureOrderbookSnapshots(prefix, startAfter string, limit int, reverse bool, visit FutureOrderbookVisitFn) (bool, error)
}

func scanStateViewOrdered(sv StateView, prefix string, limit int, reverse bool) ([]KVPair, error) {
	return scanStateViewOrderedAfter(sv, prefix, "", limit, reverse)
}

func scanStateViewOrderedKeysAfter(sv StateView, prefix, startAfter string, limit int, reverse bool) ([]string, error) {
	                                                                          
	                                                                        
	                                                                        
	                         
	if orderedAfter, ok := sv.(orderedStateKeyScannerAfter); ok {
		return orderedAfter.ScanKeysWithLimitOrderedAfter(prefix, startAfter, limit, reverse)
	}
	if startAfter == "" {
		if ordered, ok := sv.(orderedStateKeyScanner); ok {
			return ordered.ScanKeysWithLimitOrdered(prefix, limit, reverse)
		}
	}
	pairs, err := scanStateViewOrderedAfter(sv, prefix, startAfter, limit, reverse)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, p.Key)
	}
	return out, nil
}

func scanStateViewOrderedAfter(sv StateView, prefix, startAfter string, limit int, reverse bool) ([]KVPair, error) {
	                                                                         
	                                                                            
	                                                
	if orderedAfter, ok := sv.(orderedStateScannerAfter); ok {
		return orderedAfter.ScanWithLimitOrderedAfter(prefix, startAfter, limit, reverse)
	}
	if startAfter == "" {
		if ordered, ok := sv.(orderedStateScanner); ok {
			return ordered.ScanWithLimitOrdered(prefix, limit, reverse)
		}
	}

	var (
		kvs map[string][]byte
		err error
	)
	if startAfter != "" {
		kvs, err = sv.Scan(prefix)
	} else if reverse {
		kvs, err = sv.ScanWithLimitReverse(prefix, limit)
	} else {
		kvs, err = sv.ScanWithLimit(prefix, limit)
	}
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(kvs))
	for k := range kvs {
		keys = append(keys, k)
	}
	if reverse {
		sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	} else {
		sort.Strings(keys)
	}

	out := make([]KVPair, 0, len(keys))
	for _, k := range keys {
		if !orderedAfter(k, startAfter, reverse) {
			continue
		}
		val := make([]byte, len(kvs[k]))
		copy(val, kvs[k])
		out = append(out, KVPair{Key: k, Value: val})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}
