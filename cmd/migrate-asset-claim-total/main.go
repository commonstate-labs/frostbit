package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cockroachdb/pebble"
)

const (
	statePrefix       = "v1_sdb_state_"
	claimPrefix       = "v1_asset_claim_"
	claimTotalPrefix  = "v1_asset_claim_total_"
	claimMarkerPrefix = "v1_asset_claim_marker_"
)

type stats struct {
	StateDir         string `json:"state_dir"`
	LiveDir          string `json:"live_dir"`
	DryRun           bool   `json:"dry_run"`
	ShardWidth       int    `json:"shard_width"`
	ClaimDetails     int    `json:"claim_details"`
	OwnerTokenTotals int    `json:"owner_token_totals"`
	ExistingTotals   int    `json:"existing_totals"`
	WrittenTotals    int    `json:"written_totals"`
	DeletedTotals    int    `json:"deleted_totals"`
	UnchangedTotals  int    `json:"unchanged_totals"`
	InvalidClaims    int    `json:"invalid_claims"`
}

func main() {
	stateDir := flag.String("state-dir", "", "StateDB root directory containing live/")
	liveDir := flag.String("live-dir", "", "StateDB live Pebble directory")
	dryRun := flag.Bool("dry-run", false, "scan and report without writing")
	shardWidth := flag.Int("shard-width", 0, "StateDB shard width; 0 infers from live keys")
	flag.Parse()

	if strings.TrimSpace(*liveDir) == "" {
		if strings.TrimSpace(*stateDir) == "" {
			fatalf("either -state-dir or -live-dir is required")
		}
		*liveDir = filepath.Join(*stateDir, "live")
	}
	if st, err := os.Stat(*liveDir); err != nil {
		fatalf("stat live dir: %v", err)
	} else if !st.IsDir() {
		fatalf("live path is not a directory: %s", *liveDir)
	}

	db, err := pebble.Open(*liveDir, &pebble.Options{ReadOnly: *dryRun, MaxOpenFiles: 500})
	if err != nil {
		fatalf("open pebble live dir %s: %v", *liveDir, err)
	}
	defer db.Close()

	result, err := migrate(db, *stateDir, *liveDir, *dryRun, *shardWidth)
	if err != nil {
		fatalf("%v", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fatalf("encode result: %v", err)
	}
}

func migrate(db *pebble.DB, stateDir, liveDir string, dryRun bool, forcedShardWidth int) (stats, error) {
	result := stats{StateDir: stateDir, LiveDir: liveDir, DryRun: dryRun, ShardWidth: forcedShardWidth}
	totals := map[string]*big.Int{}
	existingTotals := map[string][]byte{}

	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: []byte(statePrefix), UpperBound: prefixUpperBound([]byte(statePrefix))})
	if err != nil {
		return result, err
	}
	defer iter.Close()

	for ok := iter.SeekGE([]byte(statePrefix)); ok; ok = iter.Next() {
		bizKey, width, ok := businessKeyFromStateKey(string(iter.Key()))
		if !ok {
			continue
		}
		if result.ShardWidth == 0 {
			result.ShardWidth = width
		}
		switch {
		case strings.HasPrefix(bizKey, claimTotalPrefix):
			existingTotals[bizKey] = append([]byte(nil), iter.Value()...)
		case strings.HasPrefix(bizKey, claimMarkerPrefix):
			continue
		case strings.HasPrefix(bizKey, claimPrefix):
			owner, token, ok := parseClaimOwnerToken(bizKey)
			if !ok {
				result.InvalidClaims++
				continue
			}
			amt, ok := parseNonNegativeInt(iter.Value())
			if !ok {
				result.InvalidClaims++
				continue
			}
			result.ClaimDetails++
			totalKey := claimTotalPrefix + owner + "_" + token
			if totals[totalKey] == nil {
				totals[totalKey] = new(big.Int)
			}
			totals[totalKey].Add(totals[totalKey], amt)
		}
	}
	if err := iter.Error(); err != nil {
		return result, err
	}
	if result.ShardWidth == 0 {
		result.ShardWidth = 1
	}
	if result.ShardWidth != 1 && result.ShardWidth != 2 {
		return result, fmt.Errorf("unsupported shard width %d", result.ShardWidth)
	}

	result.OwnerTokenTotals = len(totals)
	result.ExistingTotals = len(existingTotals)

	keys := make([]string, 0, len(totals))
	for k := range totals {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	stale := make([]string, 0)
	for k := range existingTotals {
		if _, ok := totals[k]; !ok {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)

	for _, k := range keys {
		next := []byte(totals[k].String())
		if string(existingTotals[k]) == string(next) {
			result.UnchangedTotals++
			continue
		}
		result.WrittenTotals++
	}
	result.DeletedTotals = len(stale)
	if dryRun || (result.WrittenTotals == 0 && result.DeletedTotals == 0) {
		return result, nil
	}

	batch := db.NewBatch()
	defer batch.Close()
	for _, k := range keys {
		next := []byte(totals[k].String())
		if string(existingTotals[k]) == string(next) {
			continue
		}
		if err := batch.Set([]byte(stateKeyForBusinessKey(k, result.ShardWidth)), next, nil); err != nil {
			return result, err
		}
	}
	for _, k := range stale {
		if err := batch.Delete([]byte(stateKeyForBusinessKey(k, result.ShardWidth)), nil); err != nil && err != pebble.ErrNotFound {
			return result, err
		}
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return result, err
	}
	return result, nil
}

func businessKeyFromStateKey(key string) (string, int, bool) {
	if !strings.HasPrefix(key, statePrefix) {
		return "", 0, false
	}
	rest := strings.TrimPrefix(key, statePrefix)
	idx := strings.IndexByte(rest, '_')
	if idx <= 0 || idx+1 >= len(rest) {
		return "", 0, false
	}
	return rest[idx+1:], idx, true
}

func parseClaimOwnerToken(key string) (string, string, bool) {
	rest := strings.TrimPrefix(key, claimPrefix)
	first := strings.IndexByte(rest, '_')
	if first <= 0 {
		return "", "", false
	}
	owner := rest[:first]
	rest = rest[first+1:]
	second := strings.IndexByte(rest, '_')
	if second <= 0 {
		return "", "", false
	}
	token := rest[:second]
	return owner, token, owner != "" && token != ""
}

func parseNonNegativeInt(raw []byte) (*big.Int, bool) {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return new(big.Int), true
	}
	out, ok := new(big.Int).SetString(s, 10)
	return out, ok && out.Sign() >= 0
}

func stateKeyForBusinessKey(bizKey string, width int) string {
	return fmt.Sprintf("%s%s_%s", statePrefix, shardOf(bizKey, width), bizKey)
}

func shardOf(key string, width int) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:width]
}

func prefixUpperBound(prefix []byte) []byte {
	upper := append([]byte(nil), prefix...)
	for i := len(upper) - 1; i >= 0; i-- {
		if upper[i] != 0xff {
			upper[i]++
			return upper[:i+1]
		}
	}
	return nil
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
