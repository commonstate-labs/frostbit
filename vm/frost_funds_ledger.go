                           
                                                          
package vm

import (
	"dex/keys"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

                                        
type FundsLotHead struct {
	Chain   string
	Asset   string
	VaultID uint32
	Height  uint64
	Seq     uint64
}

const frostFundsLotSeqBackfillLimit = 512

type fundsLotSeqOrderedScanner interface {
	ScanWithLimitOrderedAfter(prefix, startAfter string, limit int, reverse bool) ([]KVPair, error)
}

func fundsLotRemainingAmount(sv StateView, chain, asset string, vaultID uint32, height, seq uint64, original *big.Int) (*big.Int, bool, error) {
	data, exists, err := sv.Get(keys.KeyFrostFundsLotRemaining(chain, asset, vaultID, height, seq))
	if err != nil {
		return nil, false, err
	}
	if !exists || len(data) == 0 {
		return new(big.Int).Set(original), false, nil
	}
	remaining, err := ParseBalance(string(data))
	if err != nil || remaining.Sign() <= 0 || remaining.Cmp(original) > 0 {
		return nil, true, fmt.Errorf("invalid funds lot remaining amount at %d/%d", height, seq)
	}
	return remaining, true, nil
}

                                                        
                                                                           
func GetFundsLotHead(sv StateView, chain, asset string, vaultID uint32) *FundsLotHead {
	headKey := keys.KeyFrostFundsLotHead(chain, asset, vaultID)
	data, exists, err := sv.Get(headKey)
	if err != nil || !exists || len(data) == 0 {
		                                             
		return &FundsLotHead{
			Chain:   chain,
			Asset:   asset,
			VaultID: vaultID,
			Height:  0,
			Seq:     0,
		}
	}

	                               
	height, seq := parseFundsLotHeadValue(string(data))
	return &FundsLotHead{
		Chain:   chain,
		Asset:   asset,
		VaultID: vaultID,
		Height:  height,
		Seq:     seq,
	}
}

                                                        
func SetFundsLotHead(sv StateView, head *FundsLotHead) {
	headKey := keys.KeyFrostFundsLotHead(head.Chain, head.Asset, head.VaultID)
	value := formatFundsLotHeadValue(head.Height, head.Seq)
	sv.Set(headKey, []byte(value))
}

func includeFundsLotInHead(sv StateView, chain, asset string, vaultID uint32, height, seq uint64) {
	if height == 0 || seq == 0 {
		return
	}
	head := GetFundsLotHead(sv, chain, asset, vaultID)
	if head.Height == 0 || head.Seq == 0 || fundsLotPositionLess(height, seq, head.Height, head.Seq) {
		SetFundsLotHead(sv, &FundsLotHead{Chain: chain, Asset: asset, VaultID: vaultID, Height: height, Seq: seq})
	}
}

func fundsLotPositionLess(leftHeight, leftSeq, rightHeight, rightSeq uint64) bool {
	return leftHeight < rightHeight || (leftHeight == rightHeight && leftSeq < rightSeq)
}

                                                                           
                                                                         
                                                                            
                                                                              
                                           
func findFundsLotAtOrAfter(sv StateView, chain, asset string, vaultID uint32, height, seq uint64, strict bool) (*FundsLotHead, string, bool, error) {
	prefix := keys.KeyFrostFundsLotPrefix(chain, asset, vaultID)
	cursor := ""
	if height != 0 && seq != 0 {
		                                                                      
		                                                            
		cursor = keys.KeyFrostFundsLotIndex(chain, asset, vaultID, height, seq)
	}
	_ = strict                                                               

	const pageSize = 32
	for {
		page, err := scanStateViewOrderedAfter(sv, prefix, cursor, pageSize, false)
		if err != nil {
			return nil, "", false, err
		}
		if len(page) == 0 {
			return nil, "", false, nil
		}
		for _, pair := range page {
			cursor = pair.Key
			candidateHeight, candidateSeq, ok := parseVaultFundsLotIndexSuffix(pair.Key, prefix)
			if !ok || len(pair.Value) == 0 {
				continue
			}
			return &FundsLotHead{Chain: chain, Asset: asset, VaultID: vaultID, Height: candidateHeight, Seq: candidateSeq}, string(pair.Value), true, nil
		}
		if len(page) < pageSize {
			return nil, "", false, nil
		}
	}
}

func getFundsLotAtHeadStrict(sv StateView, chain, asset string, vaultID uint32) (string, bool, error) {
	head := GetFundsLotHead(sv, chain, asset, vaultID)
	if head.Height != 0 && head.Seq != 0 {
		data, exists, err := sv.Get(keys.KeyFrostFundsLotIndex(chain, asset, vaultID, head.Height, head.Seq))
		if err != nil {
			return "", false, err
		}
		if exists && len(data) > 0 {
			return string(data), true, nil
		}
	}

	startHeight, startSeq := head.Height, head.Seq
	if startHeight == 0 || startSeq == 0 {
		startHeight, startSeq = 0, 0
	}
	next, requestID, found, err := findFundsLotAtOrAfter(sv, chain, asset, vaultID, startHeight, startSeq, false)
	if err != nil || !found {
		return "", false, err
	}
	SetFundsLotHead(sv, next)
	return requestID, true, nil
}

func advanceFundsLotHeadStrict(sv StateView, chain, asset string, vaultID uint32) (*FundsLotHead, error) {
	head := GetFundsLotHead(sv, chain, asset, vaultID)
	if head.Height == 0 || head.Seq == 0 {
		return head, nil
	}

	next, _, found, err := findFundsLotAtOrAfter(sv, chain, asset, vaultID, head.Height, head.Seq, true)
	if err != nil {
		return nil, err
	}
	sv.Del(keys.KeyFrostFundsLotIndex(chain, asset, vaultID, head.Height, head.Seq))
	sv.Del(keys.KeyFrostFundsLotRemaining(chain, asset, vaultID, head.Height, head.Seq))
	if !found {
		next = &FundsLotHead{Chain: chain, Asset: asset, VaultID: vaultID}
	}
	SetFundsLotHead(sv, next)
	return next, nil
}

                                                              
                                                     
func AdvanceFundsLotHead(sv StateView, chain, asset string, vaultID uint32) *FundsLotHead {
	head, err := advanceFundsLotHeadStrict(sv, chain, asset, vaultID)
	if err != nil {
		return GetFundsLotHead(sv, chain, asset, vaultID)
	}
	return head
}

                                                                              
func GetFundsLotSeq(sv StateView, chain, asset string, vaultID uint32, height uint64) uint64 {
	seqKey := keys.KeyFrostFundsLotSeq(chain, asset, vaultID, height)
	data, exists, err := sv.Get(seqKey)
	if err != nil || !exists || len(data) == 0 {
		return 0
	}
	n, err := strconv.ParseUint(string(data), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

                                                   
func IncrFundsLotSeq(sv StateView, chain, asset string, vaultID uint32, height uint64) uint64 {
	seqKey := keys.KeyFrostFundsLotSeq(chain, asset, vaultID, height)
	currentSeq := GetFundsLotSeq(sv, chain, asset, vaultID, height)
	newSeq := currentSeq + 1
	sv.Set(seqKey, []byte(strconv.FormatUint(newSeq, 10)))
	trackFrostFundsLotSeqForBlockCleanup(sv, height, seqKey)
	return newSeq
}

func trackFrostFundsLotSeqForBlockCleanup(sv StateView, height uint64, sequenceKey string) {
	if sv == nil || height == 0 || sequenceKey == "" {
		return
	}
	sv.Set(keys.KeyFrostFundsLotSeqCleanupMarker(height), []byte("1"))
	sv.Set(keys.KeyFrostFundsLotSeqCleanup(height, sequenceKey), []byte(sequenceKey))
}

                                                                         
                                                                               
                                                                           
func cleanupFrostFundsLotSequencesForHeight(sv StateView, height uint64) error {
	if sv == nil || height == 0 {
		return nil
	}
	markerKey := keys.KeyFrostFundsLotSeqCleanupMarker(height)
	if _, exists, err := sv.Get(markerKey); err != nil || !exists {
		return err
	}
	prefix := keys.KeyFrostFundsLotSeqCleanupPrefix(height)
	rows, err := sv.Scan(prefix)
	if err != nil {
		return err
	}
	markerKeys := make([]string, 0, len(rows))
	for key := range rows {
		markerKeys = append(markerKeys, key)
	}
	sort.Strings(markerKeys)
	for _, key := range markerKeys {
		encoded := strings.TrimPrefix(key, prefix)
		decoded, decodeErr := hex.DecodeString(encoded)
		if decodeErr != nil || len(decoded) == 0 {
			return fmt.Errorf("invalid FROST funds-lot sequence cleanup key %q", key)
		}
		sequenceKey := string(decoded)
		if string(rows[key]) != sequenceKey || !isFrostFundsLotSequenceKey(sequenceKey) {
			return fmt.Errorf("invalid FROST funds-lot sequence cleanup target %q", sequenceKey)
		}
		sv.Del(sequenceKey)
		sv.Del(key)
	}
	sv.Del(markerKey)
	return nil
}

func isFrostFundsLotSequenceKey(key string) bool {
	prefix := keys.KeyFrostFundsLotSeqPrefix()
	if !strings.HasPrefix(key, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(key, prefix)
	if strings.HasPrefix(suffix, "cleanup_") {
		return false
	}
	lastSeparator := strings.LastIndexByte(suffix, '_')
	if lastSeparator < 0 || lastSeparator == len(suffix)-1 {
		return false
	}
	_, err := strconv.ParseUint(suffix[lastSeparator+1:], 10, 64)
	return err == nil
}

                                                                         
                                                                              
                                                                              
func ensureFrostFundsLotSeqCleanupMigration(sv StateView, height uint64) error {
	if sv == nil {
		return nil
	}
	doneKey := keys.KeyFrostFundsLotSeqCleanupBackfillDone()
	if _, exists, err := sv.Get(doneKey); err != nil || exists {
		return err
	}
	cursorKey := keys.KeyFrostFundsLotSeqCleanupBackfillCursor()
	cursorRaw, _, err := sv.Get(cursorKey)
	if err != nil {
		return err
	}
	cursor := string(cursorRaw)
	rows, err := scanFrostFundsLotSeqBackfill(sv, cursor, frostFundsLotSeqBackfillLimit)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if isFrostFundsLotSequenceKey(row.Key) {
			sv.Del(row.Key)
		}
	}
	if len(rows) == frostFundsLotSeqBackfillLimit {
		sv.Set(cursorKey, []byte(rows[len(rows)-1].Key))
		return nil
	}
	sv.Del(cursorKey)
	sv.Set(doneKey, []byte(strconv.FormatUint(height, 10)))
	return nil
}

func scanFrostFundsLotSeqBackfill(sv StateView, cursor string, limit int) ([]KVPair, error) {
	prefix := keys.KeyFrostFundsLotSeqPrefix()
	if scanner, ok := sv.(fundsLotSeqOrderedScanner); ok {
		return scanner.ScanWithLimitOrderedAfter(prefix, cursor, limit, false)
	}
	                                                                            
	                                                                            
	                                                  
	rows, err := sv.Scan(prefix)
	if err != nil {
		return nil, err
	}
	ordered := make([]string, 0, len(rows))
	for key := range rows {
		if key > cursor {
			ordered = append(ordered, key)
		}
	}
	sort.Strings(ordered)
	if len(ordered) > limit {
		ordered = ordered[:limit]
	}
	out := make([]KVPair, 0, len(ordered))
	for _, key := range ordered {
		out = append(out, KVPair{Key: key, Value: rows[key]})
	}
	return out, nil
}

                                                                     
func GetFundsLotAtHead(sv StateView, chain, asset string, vaultID uint32) (string, bool) {
	requestID, ok, err := getFundsLotAtHeadStrict(sv, chain, asset, vaultID)
	return requestID, ok && err == nil
}

                                            
                                                
func ConsumeFundsLot(sv StateView, chain, asset string, vaultID uint32) (string, bool) {
	requestID, ok, err := getFundsLotAtHeadStrict(sv, chain, asset, vaultID)
	if err != nil || !ok {
		return "", false
	}

	                  
	if _, err := advanceFundsLotHeadStrict(sv, chain, asset, vaultID); err != nil {
		return "", false
	}
	return requestID, true
}

                                                
func formatFundsLotHeadValue(height, seq uint64) string {
	return strconv.FormatUint(height, 10) + "|" + strconv.FormatUint(seq, 10)
}

                                            
func parseFundsLotHeadValue(value string) (height, seq uint64) {
	for i := 0; i < len(value); i++ {
		if value[i] == '|' {
			height, _ = strconv.ParseUint(value[:i], 10, 64)
			seq, _ = strconv.ParseUint(value[i+1:], 10, 64)
			return
		}
	}
	height, _ = strconv.ParseUint(value, 10, 64)
	return height, 0
}
