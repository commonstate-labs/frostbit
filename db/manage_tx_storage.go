package db

import (
	"dex/keys"
	"dex/pb"
	"fmt"
)

func (mgr *Manager) GetAnyTxById(txID string) (*pb.AnyTx, error) {
	rawKey := keys.KeyTxRaw(txID)
	rawData, err := mgr.Read(rawKey)
	if err != nil {
		return nil, fmt.Errorf("failed to read txraw key %s: %v", rawKey, err)
	}
	if rawData == "" {
		return nil, fmt.Errorf("empty txraw data for txID %s", txID)
	}
	anyTx := &pb.AnyTx{}
	if err := ProtoUnmarshal([]byte(rawData), anyTx); err != nil {
		return nil, fmt.Errorf("failed to unmarshal txraw data: %v", err)
	}
	return anyTx, nil
}

func (mgr *Manager) GetTxReceipt(txID string) (*pb.Receipt, error) {
	statusKey := keys.KeyVMAppliedTx(txID)
	status, err := mgr.Read(statusKey)
	if err != nil || status == "" {
		return nil, fmt.Errorf("transaction not applied or not found")
	}

	errorKey := keys.KeyVMTxError(txID)
	errMsg, _ := mgr.Read(errorKey)

	heightKey := keys.KeyVMTxHeight(txID)
	heightStr, _ := mgr.Read(heightKey)
	var height uint64
	if heightStr != "" {
		fmt.Sscanf(heightStr, "%d", &height)
	}

	feeKey := keys.KeyVMTxFee(txID)
	fee, _ := mgr.Read(feeKey)

	return &pb.Receipt{
		TxId:        txID,
		Status:      status,
		Error:       errMsg,
		BlockHeight: height,
		Fee:         fee,
	}, nil
}
