package vm

import (
	"dex/keys"
	"dex/pb"
	"fmt"
)

func validateTxNonce(sv StateView, base *pb.BaseMessage) error {
	if base == nil || base.FromAddress == "" {
		return nil
	}
	current, err := readAccountNonce(sv, base.FromAddress)
	if err != nil {
		return err
	}
	if base.Nonce != current {
		return fmt.Errorf("account %s got=%d expected=%d", base.FromAddress, base.Nonce, current)
	}
	return nil
}

func txRequiresAccountNonce(tx *pb.AnyTx) bool { return pb.TxRequiresAccountNonce(tx) }

func advanceAccountNonce(sv StateView, base *pb.BaseMessage) error {
	if base == nil || base.FromAddress == "" {
		return nil
	}
	accountKey := keys.KeyAccount(base.FromAddress)
	accountData, exists, err := sv.Get(accountKey)
	if err != nil {
		return err
	}
	var account pb.Account
	if exists && len(accountData) > 0 {
		if err := unmarshalProtoCompat(accountData, &account); err != nil {
			return err
		}
	} else {
		account.Address = base.FromAddress
	}
	next := base.Nonce + 1
	if next == 0 {
		return fmt.Errorf("account %s nonce overflow", base.FromAddress)
	}
	account.Nonce = next
	updatedData, err := MarshalProto(&account)
	if err != nil {
		return err
	}
	sv.Set(accountKey, updatedData)
	return nil
}

func readAccountNonce(sv StateView, addr string) (uint64, error) {
	accountData, exists, err := sv.Get(keys.KeyAccount(addr))
	if err != nil || !exists || len(accountData) == 0 {
		return 0, err
	}
	var account pb.Account
	if err := unmarshalProtoCompat(accountData, &account); err != nil {
		return 0, err
	}
	return account.Nonce, nil
}
