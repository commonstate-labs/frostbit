package db

import (
	"bytes"
	"crypto/sha256"
	"dex/keys"
	"dex/pb"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"
)

func ProtoMarshal(m proto.Message) ([]byte, error) {
	opts := proto.MarshalOptions{Deterministic: true}
	return opts.Marshal(m)
}
func ProtoUnmarshal(data []byte, m proto.Message) error { return proto.Unmarshal(data, m) }

                                  
func (manager *Manager) DeleteKey(key string) error {
	if keys.IsStatefulKey(key) {
		manager.EnqueueDelete(key)
		return nil
	}
	db, err := manager.dbForKVKey(key)
	if err != nil {
		return err
	}
	return db.Delete([]byte(key), pebble.Sync)
}

                                                                 
               
  
                                 
                                                                                
func getNewIndex(mgr *Manager) (uint64, []WriteTask, error) {
	var (
		idx        uint64
		tasks      []WriteTask
		freeIdxKey []byte
	)

	                                                       
	prefix := []byte(keys.KeyFreeIdx())
	db, err := mgr.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return 0, nil, err
	}
	iter, err := db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return 0, nil, err
	}

	if iter.SeekGE(prefix); iter.Valid() {
		freeIdxKey = make([]byte, len(iter.Key()))
		copy(freeIdxKey, iter.Key())
		idxStr := bytes.TrimPrefix(freeIdxKey, prefix)
		i, _ := strconv.ParseUint(string(idxStr), 10, 64)
		idx = i
	}
	iter.Close()

	if idx == 0 {
		idx, _ = mgr.NextIndex()
	}

	if freeIdxKey != nil {
		tasks = append(tasks, WriteTask{Key: freeIdxKey, Op: OpDelete})
	}
	return idx, tasks, nil
}

func removeIndex(idx uint64) WriteTask {
	k := []byte(fmt.Sprintf(keys.KeyFreeIdx()+"%020d", idx))
	return WriteTask{Key: k, Op: OpSet}
}

                                                                                                                    

                                                                                
                                                                                  
func HashTx(tx proto.Message) (string, error) {
	txCopy := proto.Clone(tx)
	switch t := txCopy.(type) {
	case *pb.IssueTokenTx:
		if t.Base != nil {
			t.Base.TxId = ""
			t.Base.Signature = nil
		}
	case *pb.FreezeTx:
		if t.Base != nil {
			t.Base.TxId = ""
			t.Base.Signature = nil
		}
	case *pb.Transaction:
		if t.Base != nil {
			t.Base.TxId = ""
			t.Base.Signature = nil
		}
	case *pb.OrderTx:
		if t.Base != nil {
			t.Base.TxId = ""
			t.Base.Signature = nil
		}
	case *pb.MinerTx:
		if t.Base != nil {
			t.Base.TxId = ""
			t.Base.Signature = nil
		}
	case *pb.AnyTx:
		base := t.GetBase()
		if base != nil {
			base.TxId = ""
			base.Signature = nil
		} else {
			return "", fmt.Errorf("不支持的 AnyTx 内部类型")
		}
	default:
		return "", fmt.Errorf("不支持的交易类型")
	}
	data, err := proto.Marshal(txCopy)
	if err != nil {
		return "", err
	}
	hashBytes := sha256.Sum256(data)
	return hex.EncodeToString(hashBytes[:]), nil
}

                                                                                   
func restoreSeqFromDB(db *pebble.DB) uint64 {
	raw, closer, err := db.Get([]byte("meta:seq_counter"))
	if err != nil {
		return 0
	}
	defer closer.Close()
	if len(raw) < 8 {
		return 0
	}
	return binary.BigEndian.Uint64(raw)
}
