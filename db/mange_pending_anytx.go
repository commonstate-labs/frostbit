package db

import (
	"dex/keys"
	"dex/pb"
	"fmt"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"
)

                                                         
func (mgr *Manager) SavePendingAnyTx(tx *pb.AnyTx) error {
	txID := tx.GetTxId()
	if txID == "" {
		return fmt.Errorf("SavePendingAnyTx: empty txID")
	}
	data, err := ProtoMarshal(tx)
	if err != nil {
		return err
	}
	mgr.EnqueueSet(keys.KeyPendingAnyTx(txID), string(data))
	return nil
}

                                        
func (mgr *Manager) DeletePendingAnyTx(txID string) error {
	if txID == "" {
		return fmt.Errorf("DeletePendingAnyTx: empty txID")
	}
	mgr.EnqueueDelete(keys.KeyPendingAnyTx(txID))
	return nil
}

                                      
func (mgr *Manager) LoadPendingAnyTx() ([]*pb.AnyTx, error) {
	db, err := mgr.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return nil, err
	}
	var result []*pb.AnyTx
	prefix := []byte(keys.KeyPendingAnyTx(""))
	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixUpperBound(prefix)})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	for iter.SeekGE(prefix); iter.Valid(); iter.Next() {
		val := make([]byte, len(iter.Value()))
		copy(val, iter.Value())
		var anyTx pb.AnyTx
		if uerr := proto.Unmarshal(val, &anyTx); uerr != nil {
			continue
		}
		result = append(result, &anyTx)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return result, nil
}
