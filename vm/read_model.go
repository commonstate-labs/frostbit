package vm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"dex/future"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                                                             
                                                                            
                                                                            
                                                                               
func buildBlockReadModel(block *pb.Block, finalDiff []WriteOp) (*pb.BlockReadModel, error) {
	if block == nil || block.Header == nil || block.BlockHash == "" {
		return nil, fmt.Errorf("cannot build read model for incomplete block")
	}

	mutations := make(map[string]*pb.ReadModelMutation)
	put := func(key string, value []byte, retention pb.ReadModelRetention) {
		mutations[key] = &pb.ReadModelMutation{
			Key:       key,
			Value:     append([]byte(nil), value...),
			Op:        pb.ReadModelMutationOp_READ_MODEL_MUTATION_PUT,
			Retention: retention,
		}
	}
	del := func(key string, retention pb.ReadModelRetention) {
		mutations[key] = &pb.ReadModelMutation{
			Key:       key,
			Op:        pb.ReadModelMutationOp_READ_MODEL_MUTATION_DELETE,
			Retention: retention,
		}
	}

	for i := range finalDiff {
		op := &finalDiff[i]
		family := readModelFamily(op.Key)
		if family == "" {
			continue
		}
		retention := readModelRetentionForKey(op.Key)
		if op.Del {
			del(op.Key, retention)
		} else {
			put(op.Key, op.Value, retention)
		}

		switch family {
		case "activity":
			if err := deriveActivityKindMutation(op, put, del); err != nil {
				return nil, err
			}
		case "spot_order":
			if err := deriveSpotRecentMutation(op, put, del); err != nil {
				return nil, err
			}
		case "future_order":
			if err := deriveFutureRecentMutation(op, put, del); err != nil {
				return nil, err
			}
		case "future_position_history":
			if err := deriveFuturePositionHistoryMutations(op, put); err != nil {
				return nil, err
			}
		}
	}

	orderedKeys := make([]string, 0, len(mutations))
	for key := range mutations {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)
	ordered := make([]*pb.ReadModelMutation, 0, len(orderedKeys))
	for i, key := range orderedKeys {
		mutation := mutations[key]
		mutation.Ordinal = uint32(i)
		ordered = append(ordered, mutation)
	}

	model := &pb.BlockReadModel{
		SchemaVersion: pb.BlockReadModelSchemaVersion,
		Height:        block.Header.Height,
		BlockId:       block.BlockHash,
		ParentId:      block.Header.PrevBlockHash,
		Mutations:     ordered,
	}
	if err := pb.SealBlockReadModel(model); err != nil {
		return nil, err
	}
	return model, nil
}

func readModelFamily(key string) string {
	switch {
	case strings.HasPrefix(key, keys.KeyUserActivityFamilyPrefix()):
		return "activity"
	case strings.HasPrefix(key, keys.KeySpotAccountOrderFamilyPrefix()):
		return "spot_order"
	case strings.HasPrefix(key, keys.KeyFutureAccountOrderFamilyPrefix()):
		return "future_order"
	case strings.HasPrefix(key, keys.KeyFuturePositionHistoryPrefix()):
		return "future_position_history"
	default:
		return ""
	}
}

                                                                            
                                                                           
                                                                               
                           
func conservationAuditDiff(diff []WriteOp) []WriteOp {
	filtered := make([]WriteOp, 0, len(diff))
	for i := range diff {
		if keys.StorageClassForKey(diff[i].Key) == keys.StorageQuery {
			continue
		}
		filtered = append(filtered, diff[i])
	}
	return filtered
}

                                                                             
                                                                         
                                                                      
                                                                       
                                                    
func finalizedNodeDiff(diff []WriteOp) []WriteOp {
	filtered := make([]WriteOp, 0, len(diff))
	activityPrefix := keys.KeyUserActivityFamilyPrefix()
	spotOrderPrefix := keys.KeySpotAccountOrderFamilyPrefix()
	futureRecentPrefix := keys.KeyFutureAccountOrderFamilyPrefix() + "recent|"
	futurePositionHistoryPrefix := keys.KeyFuturePositionHistoryPrefix()
	for i := range diff {
		key := diff[i].Key
		if strings.HasPrefix(key, activityPrefix) ||
			strings.HasPrefix(key, spotOrderPrefix) ||
			strings.HasPrefix(key, futureRecentPrefix) ||
			strings.HasPrefix(key, futurePositionHistoryPrefix) {
			continue
		}
		filtered = append(filtered, diff[i])
	}
	return filtered
}

func readModelRetentionForKey(key string) pb.ReadModelRetention {
	if (strings.HasPrefix(key, keys.KeySpotAccountOrderFamilyPrefix()) ||
		strings.HasPrefix(key, keys.KeyFutureAccountOrderFamilyPrefix())) &&
		strings.Contains(key, "|status:open|") {
		return pb.ReadModelRetention_READ_MODEL_RETENTION_CURRENT
	}
	return pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED
}

type readModelPut func(string, []byte, pb.ReadModelRetention)
type readModelDel func(string, pb.ReadModelRetention)

func deriveActivityKindMutation(op *WriteOp, put readModelPut, del readModelDel) error {
	family := keys.KeyUserActivityFamilyPrefix()
	rest := strings.TrimPrefix(op.Key, family)
	if strings.HasPrefix(rest, "kind|") {
		return nil
	}
	addr, height, seq, ok := parseCanonicalActivityKey(rest)
	if !ok {
		return fmt.Errorf("invalid canonical user activity key %q", op.Key)
	}
	data := op.Value
	if op.Del {
		if !op.OldExists || len(op.OldValue) == 0 {
			                                                                        
			                                                                         
			                                                 
			return nil
		}
		data = op.OldValue
	}
	var record UserActivityRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return fmt.Errorf("decode user activity %q for read model: %w", op.Key, err)
	}
	if record.Kind == "" {
		return nil
	}
	if record.Height != 0 && record.Height != height {
		return fmt.Errorf("user activity height mismatch for %q: value=%d key=%d", op.Key, record.Height, height)
	}
	indexKey := keys.KeyUserActivityKindIndex(addr, record.Kind, height, record.TxID, seq)
	if op.Del {
		del(indexKey, pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED)
	} else {
		put(indexKey, []byte(op.Key), pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED)
	}
	return nil
}

func parseCanonicalActivityKey(rest string) (string, uint64, uint32, bool) {
	seqSep := strings.LastIndexByte(rest, '_')
	if seqSep <= 0 || seqSep+1 >= len(rest) {
		return "", 0, 0, false
	}
	heightSep := strings.LastIndexByte(rest[:seqSep], '_')
	if heightSep <= 0 || heightSep+1 >= seqSep {
		return "", 0, 0, false
	}
	height, err := strconv.ParseUint(rest[heightSep+1:seqSep], 10, 64)
	if err != nil {
		return "", 0, 0, false
	}
	seq64, err := strconv.ParseUint(rest[seqSep+1:], 10, 32)
	if err != nil {
		return "", 0, 0, false
	}
	return rest[:heightSep], height, uint32(seq64), true
}

func deriveSpotRecentMutation(op *WriteOp, put readModelPut, del readModelDel) error {
	family := keys.KeySpotAccountOrderFamilyPrefix()
	rest := strings.TrimPrefix(op.Key, family)
	if strings.HasPrefix(rest, "recent|") || !strings.HasPrefix(rest, "owner:") {
		return nil
	}
	if op.Del {
		recentKey, ok := accountOrderRecentKeyFromPrimary(family, rest)
		if !ok {
			return fmt.Errorf("invalid spot account-order key %q", op.Key)
		}
		del(recentKey, readModelRetentionForKey(recentKey))
		return nil
	}
	var state pb.OrderState
	if err := proto.Unmarshal(op.Value, &state); err != nil {
		return fmt.Errorf("decode spot account order %q for read model: %w", op.Key, err)
	}
	recentKey := keys.KeySpotAccountOrderRecentFromState(&state)
	if recentKey == "" {
		return fmt.Errorf("incomplete spot account order %q", op.Key)
	}
	put(recentKey, []byte(op.Key), readModelRetentionForKey(recentKey))
	return nil
}

func deriveFutureRecentMutation(op *WriteOp, put readModelPut, del readModelDel) error {
	family := keys.KeyFutureAccountOrderFamilyPrefix()
	rest := strings.TrimPrefix(op.Key, family)
	if strings.HasPrefix(rest, "recent|") || !strings.HasPrefix(rest, "owner:") {
		return nil
	}
	if op.Del {
		recentKey, ok := accountOrderRecentKeyFromPrimary(family, rest)
		if !ok {
			return fmt.Errorf("invalid future account-order key %q", op.Key)
		}
		del(recentKey, readModelRetentionForKey(recentKey))
		return nil
	}
	var state pb.FutureOrderState
	if err := proto.Unmarshal(op.Value, &state); err != nil {
		return fmt.Errorf("decode future account order %q for read model: %w", op.Key, err)
	}
	recentKey := keys.KeyFutureAccountOrderRecentFromState(&state)
	if recentKey == "" {
		return fmt.Errorf("incomplete future account order %q", op.Key)
	}
	put(recentKey, []byte(op.Key), readModelRetentionForKey(recentKey))
	return nil
}

                                                                        
                                                                           
                                                                            
                                                                              
                                                                       
func deriveFuturePositionHistoryMutations(op *WriteOp, put readModelPut) error {
	if op.Del {
		return fmt.Errorf("terminal future position history is append-only: delete %q", op.Key)
	}
	var state pb.FuturePositionState
	if err := proto.Unmarshal(op.Value, &state); err != nil {
		return fmt.Errorf("decode future position history %q for read model: %w", op.Key, err)
	}
	if state.PositionId == "" || state.Owner == "" || state.Pair == "" {
		return fmt.Errorf("incomplete future position history %q", op.Key)
	}
	if op.Key != keys.KeyFuturePositionHistory(state.PositionId) {
		return fmt.Errorf("non-canonical future position history key %q for position %q", op.Key, state.PositionId)
	}
	switch state.Status {
	case int32(future.StatusClosed),
		int32(future.StatusLiquidated),
		int32(future.StatusADLClosed),
		int32(future.StatusInsuredDeleveraged):
	default:
		return fmt.Errorf("non-terminal future position history %q has status %d", op.Key, state.Status)
	}
	retention := pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED
	positionRef := []byte(state.PositionId)
	put(keys.KeyFuturePositionHistoryOwnerIndex(state.Owner, state.Status, state.CloseHeight, state.PositionId), positionRef, retention)
	put(keys.KeyFuturePositionHistoryPairIndex(state.Pair, state.Status, state.CloseHeight, state.PositionId), positionRef, retention)
	put(keys.KeyFuturePositionHistoryRecentIndex(state.Status, state.CloseHeight, state.PositionId), positionRef, retention)
	return nil
}

                                                                               
                                                                              
                                    
func accountOrderRecentKeyFromPrimary(family, rest string) (string, bool) {
	parts := strings.Split(rest, "|")
	if len(parts) != 5 || !strings.HasPrefix(parts[0], "owner:") ||
		!strings.HasPrefix(parts[1], "pair:") || !strings.HasPrefix(parts[2], "status:") ||
		!strings.HasPrefix(parts[3], "height:") || !strings.HasPrefix(parts[4], "order_id:") {
		return "", false
	}
	owner := strings.TrimPrefix(parts[0], "owner:")
	pair := strings.TrimPrefix(parts[1], "pair:")
	status := strings.TrimPrefix(parts[2], "status:")
	height := strings.TrimPrefix(parts[3], "height:")
	orderID := strings.TrimPrefix(parts[4], "order_id:")
	if owner == "" || pair == "" || status == "" || height == "" || orderID == "" {
		return "", false
	}
	return fmt.Sprintf("%srecent|owner:%s|status:%s|height:%s|pair:%s|order_id:%s", family, owner, status, height, pair, orderID), true
}
