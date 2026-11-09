package vm

import (
	"sort"
	"strings"

	"github.com/google/btree"
)

const overlayKeyIndexDegree = 32

                                                                          
                                                                            
                                                                      
type overlayKeyIndex struct {
	tree        *btree.BTreeG[string]
	comparisons uint64
	rangeVisits uint64
	builds      uint64
}

func newOverlayKeyIndex(keys []string) *overlayKeyIndex {
	index := &overlayKeyIndex{}
	index.tree = btree.NewG[string](overlayKeyIndexDegree, func(a, b string) bool {
		index.comparisons++
		return a < b
	})
	sort.Strings(keys)
	for _, key := range keys {
		index.tree.ReplaceOrInsert(key)
	}
	index.builds = 1
	return index
}

func (i *overlayKeyIndex) insert(key string) {
	if i == nil {
		return
	}
	i.tree.ReplaceOrInsert(key)
}

func (i *overlayKeyIndex) remove(key string) {
	if i == nil {
		return
	}
	i.tree.Delete(key)
}

                                                                          
                                                      
                                                                             
                                                                
func (i *overlayKeyIndex) rangeKeys(prefix, startAfter, frontier string, reverse bool) []string {
	if i == nil || i.tree.Len() == 0 {
		return nil
	}
	upper, hasUpper := prefixUpperBoundString(prefix)
	out := make([]string, 0)
	visit := func(key string) bool {
		i.rangeVisits++
		if reverse {
			if startAfter != "" && key >= startAfter {
				return true
			}
			if hasUpper && key >= upper {
				return true
			}
			if key < prefix {
				return false
			}
			if !strings.HasPrefix(key, prefix) {
				return true
			}
			if frontier != "" && key < frontier {
				return false
			}
			out = append(out, key)
			return true
		}

		if key < prefix {
			return true
		}
		if hasUpper && key >= upper {
			return false
		}
		if !strings.HasPrefix(key, prefix) {
			return false
		}
		if startAfter != "" && key <= startAfter {
			return true
		}
		if frontier != "" && key > frontier {
			return false
		}
		out = append(out, key)
		return true
	}

	if reverse {
		pivot := startAfter
		if hasUpper && (pivot == "" || pivot > upper) {
			pivot = upper
		}
		if pivot != "" {
			i.tree.DescendLessOrEqual(pivot, visit)
		} else {
			i.tree.Descend(visit)
		}
		return out
	}

	pivot := prefix
	if startAfter > pivot {
		pivot = startAfter
	}
	i.tree.AscendGreaterOrEqual(pivot, visit)
	return out
}
