package indexdb

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"dex/keys"
	"dex/pb"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"
)

const (
	defaultFuturePositionHistoryLimit = 100
	maxFuturePositionHistoryLimit     = 500
	maxFuturePositionCursorLength     = 2_048
)

var (
	ErrInvalidFuturePositionCursor      = errors.New("invalid future position cursor")
	ErrInvalidFuturePositionQuery       = errors.New("invalid future position history query")
	ErrFuturePositionHistoryUnavailable = errors.New("future position history read model unavailable")
)

                                                                         
                                                                          
type FuturePositionHistoryPage struct {
	Positions  []*pb.FuturePositionState `json:"positions"`
	HasMore    bool                      `json:"has_more"`
	NextCursor string                    `json:"next_cursor,omitempty"`
	Limit      int                       `json:"limit"`
}

type futurePositionHistoryRef struct {
	closeHeight uint64
	positionID  string
	orderSuffix string
	status      string
}

var futurePositionClosedStatusKeys = []string{
	"closed",
	"liquidated",
	"adl_closed",
	"insured_deleveraged",
}

                                                                            
                                                                              
                                                                              
                                                                              
                                                                    
func (idb *IndexDB) GetFuturePositionHistoryPage(owner, pair, status, cursor string, limit int) (*FuturePositionHistoryPage, error) {
	if idb == nil || idb.db == nil {
		return nil, ErrFuturePositionHistoryUnavailable
	}
	owner = strings.TrimSpace(owner)
	pair = strings.TrimSpace(pair)
	if owner != "" && pair != "" {
		return nil, fmt.Errorf("%w: owner and pair are mutually exclusive", ErrInvalidFuturePositionQuery)
	}
	statuses, err := normalizedFuturePositionHistoryStatuses(status)
	if err != nil {
		return nil, err
	}
	cursorSuffix, err := decodeFuturePositionHistoryCursor(cursor)
	if err != nil {
		return nil, err
	}
	limit = boundedFuturePositionHistoryLimit(limit)

	coverage, err := idb.GetReadModelCoverage()
	if err != nil {
		return nil, err
	}
	if coverage == nil {
		return nil, ErrFuturePositionHistoryUnavailable
	}

	snapshot := idb.db.NewSnapshot()
	defer snapshot.Close()

	scanLimit := limit + 1
	refs := make([]futurePositionHistoryRef, 0, len(statuses)*scanLimit)
	for _, statusKey := range statuses {
		prefix := futurePositionHistoryIndexPrefix(owner, pair, statusKey)
		rows, scanErr := scanSnapshotPrefixAfter(snapshot, prefix, cursorSuffix, scanLimit)
		if scanErr != nil {
			return nil, scanErr
		}
		for _, row := range rows {
			orderSuffix := strings.TrimPrefix(row.Key, prefix)
			closeHeight, positionID, parseErr := parseFuturePositionHistoryOrderSuffix(orderSuffix)
			if parseErr != nil || positionID != row.Value {
				return nil, fmt.Errorf("corrupt future position history index %q", row.Key)
			}
			refs = append(refs, futurePositionHistoryRef{
				closeHeight: closeHeight,
				positionID:  positionID,
				orderSuffix: orderSuffix,
				status:      statusKey,
			})
		}
	}

	sort.Slice(refs, func(i, j int) bool {
		if refs[i].closeHeight != refs[j].closeHeight {
			return refs[i].closeHeight > refs[j].closeHeight
		}
		return refs[i].positionID < refs[j].positionID
	})
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if _, exists := seen[ref.positionID]; exists {
			return nil, fmt.Errorf("duplicate future position history index for %q", ref.positionID)
		}
		seen[ref.positionID] = struct{}{}
	}

	pageSize := len(refs)
	if pageSize > limit+1 {
		pageSize = limit + 1
	}
	positions := make([]*pb.FuturePositionState, 0, pageSize)
	for _, ref := range refs[:pageSize] {
		position, hydrateErr := hydrateFuturePositionHistory(snapshot, ref, owner, pair)
		if hydrateErr != nil {
			return nil, hydrateErr
		}
		positions = append(positions, position)
	}

	page := &FuturePositionHistoryPage{
		Positions: positions,
		Limit:     limit,
	}
	if len(positions) > limit {
		page.Positions = positions[:limit]
		page.HasMore = true
		page.NextCursor = encodeFuturePositionHistoryCursor(refs[limit-1].orderSuffix)
	}
	return page, nil
}

func normalizedFuturePositionHistoryStatuses(status string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "closed":
		return append([]string(nil), futurePositionClosedStatusKeys...), nil
	case "liquidated":
		return []string{"liquidated"}, nil
	case "adl_closed":
		return []string{"adl_closed"}, nil
	case "insured_deleveraged":
		return []string{"insured_deleveraged"}, nil
	default:
		return nil, fmt.Errorf("%w: status must select terminal positions", ErrInvalidFuturePositionQuery)
	}
}

func boundedFuturePositionHistoryLimit(limit int) int {
	if limit <= 0 {
		return defaultFuturePositionHistoryLimit
	}
	if limit > maxFuturePositionHistoryLimit {
		return maxFuturePositionHistoryLimit
	}
	return limit
}

func futurePositionHistoryIndexPrefix(owner, pair, status string) string {
	switch {
	case owner != "":
		return keys.KeyFuturePositionHistoryOwnerIndexPrefix(owner, status)
	case pair != "":
		return keys.KeyFuturePositionHistoryPairIndexPrefix(pair, status)
	default:
		return keys.KeyFuturePositionHistoryRecentIndexPrefix(status)
	}
}

func scanSnapshotPrefixAfter(snapshot *pebble.Snapshot, prefix, cursorSuffix string, limit int) ([]KV, error) {
	prefixBytes := []byte(prefix)
	upper := prefixUpperBound(prefixBytes)
	iter, err := snapshot.NewIter(&pebble.IterOptions{LowerBound: prefixBytes, UpperBound: upper})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	seek := prefixBytes
	if cursorSuffix != "" {
		seek = []byte(prefix + cursorSuffix)
	}
	valid := iter.SeekGE(seek)
	if cursorSuffix != "" && valid && bytes.Equal(iter.Key(), seek) {
		valid = iter.Next()
	}

	rows := make([]KV, 0, limit)
	for ; valid; valid = iter.Next() {
		if limit > 0 && len(rows) >= limit {
			break
		}
		rows = append(rows, KV{Key: string(iter.Key()), Value: string(iter.Value())})
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return rows, nil
}

func hydrateFuturePositionHistory(snapshot *pebble.Snapshot, ref futurePositionHistoryRef, owner, pair string) (*pb.FuturePositionState, error) {
	canonicalKey := keys.KeyFuturePositionHistory(ref.positionID)
	raw, closer, err := snapshot.Get([]byte(canonicalKey))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, fmt.Errorf("missing future position history %q", ref.positionID)
		}
		return nil, err
	}
	data := append([]byte(nil), raw...)
	if closeErr := closer.Close(); closeErr != nil {
		return nil, closeErr
	}

	var position pb.FuturePositionState
	if err := proto.Unmarshal(data, &position); err != nil {
		return nil, fmt.Errorf("decode future position history %q: %w", ref.positionID, err)
	}
	if position.PositionId != ref.positionID || position.CloseHeight != ref.closeHeight ||
		keys.FuturePositionStatusKey(position.Status) != ref.status ||
		(owner != "" && position.Owner != owner) ||
		(pair != "" && position.Pair != pair) ||
		!isTerminalFuturePositionStatus(position.Status) {
		return nil, fmt.Errorf("future position history index mismatch for %q", ref.positionID)
	}
	return &position, nil
}

func isTerminalFuturePositionStatus(status int32) bool {
	switch keys.FuturePositionStatusKey(status) {
	case "closed", "liquidated", "adl_closed", "insured_deleveraged":
		return true
	default:
		return false
	}
}

func futurePositionHistoryOrderSuffix(closeHeight uint64, positionID string) string {
	return fmt.Sprintf("height:%020d|position_id:%s", ^closeHeight, positionID)
}

func encodeFuturePositionHistoryCursor(orderSuffix string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(orderSuffix))
}

func decodeFuturePositionHistoryCursor(cursor string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	if len(cursor) > maxFuturePositionCursorLength {
		return "", fmt.Errorf("%w: cursor is too long", ErrInvalidFuturePositionCursor)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidFuturePositionCursor, err)
	}
	suffix := string(decoded)
	if _, _, err := parseFuturePositionHistoryOrderSuffix(suffix); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidFuturePositionCursor, err)
	}
	return suffix, nil
}

func parseFuturePositionHistoryOrderSuffix(suffix string) (uint64, string, error) {
	const (
		heightPrefix     = "height:"
		positionIDMarker = "|position_id:"
	)
	if !strings.HasPrefix(suffix, heightPrefix) {
		return 0, "", fmt.Errorf("missing height prefix")
	}
	rest := strings.TrimPrefix(suffix, heightPrefix)
	marker := strings.Index(rest, positionIDMarker)
	if marker != 20 {
		return 0, "", fmt.Errorf("invalid inverted height width")
	}
	invertedHeight, err := strconv.ParseUint(rest[:marker], 10, 64)
	if err != nil {
		return 0, "", err
	}
	positionID := rest[marker+len(positionIDMarker):]
	if positionID == "" || futurePositionHistoryOrderSuffix(^invertedHeight, positionID) != suffix {
		return 0, "", fmt.Errorf("invalid position id")
	}
	return ^invertedHeight, positionID, nil
}
