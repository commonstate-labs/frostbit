package vm

import (
	"bytes"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"fmt"

	"google.golang.org/protobuf/proto"
)

func resolveReceiverECIESPubKey(sv StateView, receiverID string) []byte {
	if pub := loadActiveMinerPubKey(sv, receiverID); len(pub) > 0 {
		return pub
	}
	if pub := loadAccountPubKey(sv, receiverID, int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)); len(pub) > 0 {
		return pub
	}
	if acctKeys := loadAccountPubKeys(sv, receiverID); len(acctKeys) > 0 {
		return pickDeterministicPubKey(acctKeys)
	}
	return nil
}

func loadActiveMinerPubKey(sv StateView, minerID string) []byte {
	if minerID == "" {
		return nil
	}
	data, exists, err := sv.Get(keys.KeyFrostActiveMiners())
	if err != nil || !exists || len(data) == 0 {
		return nil
	}

	var top pb.FrostActiveMiners
	if err := proto.Unmarshal(data, &top); err != nil {
		return nil
	}
	for i, addr := range top.Addresses {
		if addr != minerID || i >= len(top.PublicKeys) || len(top.PublicKeys[i]) == 0 {
			continue
		}
		return append([]byte(nil), top.PublicKeys[i]...)
	}
	return nil
}

func loadAccountPubKey(sv StateView, address string, algo int32) []byte {
	keysByAlgo := loadAccountPubKeys(sv, address)
	if len(keysByAlgo) == 0 {
		return nil
	}
	if pub, ok := keysByAlgo[algo]; ok && len(pub) > 0 {
		return append([]byte(nil), pub...)
	}
	return nil
}

func loadAccountPubKeys(sv StateView, address string) map[int32][]byte {
	if address == "" {
		return nil
	}
	accountData, exists, err := sv.Get(keys.KeyAccount(address))
	if err != nil || !exists || len(accountData) == 0 {
		return nil
	}
	var acc pb.Account
	if err := proto.Unmarshal(accountData, &acc); err != nil || acc.PublicKeys == nil {
		return nil
	}
	return acc.PublicKeys.Keys
}

func resolveValidationAckPubKey(sv StateView, sender string, base *pb.BaseMessage) ([]byte, error) {
	if base != nil && base.PublicKeys != nil {
		if pub, ok := base.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)]; ok && len(pub) > 0 {
			derivedAddr, err := utils.AddressFromSecp256k1PubKey(pub)
			if err == nil && derivedAddr == sender {
				return append([]byte(nil), pub...), nil
			}
		}
	}

	pub := loadAccountPubKey(sv, sender, int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX))
	if len(pub) == 0 {
		return nil, fmt.Errorf("validation signer pubkey not found for %s", sender)
	}
	return pub, nil
}

func verifyValidationAckSignature(sv StateView, sender string, base *pb.BaseMessage, msgHash, sig []byte) error {
	if len(sig) == 0 {
		return fmt.Errorf("empty validation signature")
	}
	pub, err := resolveValidationAckPubKey(sv, sender, base)
	if err != nil {
		return err
	}
	if !utils.VerifyECDSASignature(pub, msgHash, sig) {
		return fmt.Errorf("invalid validation signature")
	}
	return nil
}

func hasPendingDkgComplaints(sv StateView, chain string, vaultID uint32, epochID uint64, sessionID string) (bool, error) {
	kvs, err := sv.Scan(keys.KeyFrostVaultDkgComplaintPrefix(chain, vaultID, epochID, sessionID))
	if err != nil {
		return false, err
	}
	for _, key := range sortedBytesMapKeys(kvs) {
		var complaint pb.FrostVaultDkgComplaint
		if err := unmarshalProtoCompat(kvs[key], &complaint); err != nil {
			return false, fmt.Errorf("decode complaint %s: %w", key, err)
		}
		if complaint.Status == ComplaintStatusPending {
			return true, nil
		}
	}
	return false, nil
}

func countDkgValidationAcks(sv StateView, chain string, vaultID uint32, epochID uint64, sessionID string) (int, error) {
	kvs, err := sv.Scan(keys.KeyFrostVaultDkgValidationAckPrefix(chain, vaultID, epochID, sessionID))
	if err != nil {
		return 0, err
	}
	return len(kvs), nil
}

func validationAckAlreadyRecorded(existing []byte, msgHash []byte) bool {
	return len(existing) > 0 && bytes.Equal(existing, msgHash)
}
