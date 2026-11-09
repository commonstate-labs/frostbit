package vm

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"

	"dex/keys"
	"dex/pb"
	"dex/utils"

	"github.com/btcsuite/btcd/btcec/v2"
)

const minerRegistrationPoPDomain = "dex/miner-registration/v1"

func writeMinerRegistrationField(h interface{ Write([]byte) (int, error) }, value []byte) {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(value)
}

func canonicalMinerSecpKey(base *pb.BaseMessage) ([]byte, error) {
	if base == nil || base.PublicKeys == nil {
		return nil, fmt.Errorf("missing miner secp256k1 public key")
	}
	raw := base.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)]
	parsed, err := btcec.ParsePubKey(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid miner secp256k1 public key: %w", err)
	}
	canonical := parsed.SerializeCompressed()
	if !bytes.Equal(raw, canonical) {
		return nil, fmt.Errorf("miner secp256k1 public key must use compressed canonical encoding")
	}
	derived, err := utils.AddressFromSecp256k1PubKey(canonical)
	if err != nil || derived != base.FromAddress {
		return nil, fmt.Errorf("miner secp256k1 public key does not match from_address")
	}
	return canonical, nil
}

func canonicalMinerBLSKey(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing miner BLS public key")
	}
	point, err := utils.DeserializePublicKey(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid miner BLS public key: %w", err)
	}
	canonical, err := utils.SerializePublicKey(point)
	if err != nil {
		return nil, fmt.Errorf("serialize miner BLS public key: %w", err)
	}
	if !bytes.Equal(raw, canonical) {
		return nil, fmt.Errorf("miner BLS public key must use canonical encoding")
	}
	return canonical, nil
}

func canonicalMinerRewardAddress(hot, configured string) (string, error) {
	reward := strings.TrimSpace(configured)
	if reward == "" {
		reward = hot
	}
	if !utils.IsDexAddress(reward) {
		return "", fmt.Errorf("reward_address must be a canonical DEX address")
	}
	return reward, nil
}

                                                                             
                                                                              
                                                                                
                                                                            
func MinerRegistrationPoPDigest(tx *pb.MinerTx) ([]byte, error) {
	if tx == nil || tx.Base == nil || tx.Op != pb.OrderOp_ADD {
		return nil, fmt.Errorf("invalid miner registration transaction")
	}
	hot := ResolveMinerHot(tx)
	if hot == "" || hot != tx.Base.FromAddress {
		return nil, fmt.Errorf("miner_address must equal the signing hot address")
	}
	secpKey, err := canonicalMinerSecpKey(tx.Base)
	if err != nil {
		return nil, err
	}
	blsKey, err := canonicalMinerBLSKey(tx.BlsPublicKey)
	if err != nil {
		return nil, err
	}
	reward, err := canonicalMinerRewardAddress(hot, tx.RewardAddress)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimSpace(tx.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint is required for miner registration")
	}
	if err := validateMinerEndpoint(endpoint); err != nil {
		return nil, err
	}
	amount, err := parsePositiveBalanceStrict("mining amount", tx.Amount)
	if err != nil {
		return nil, err
	}

	h := sha256.New()
	_, _ = h.Write([]byte(minerRegistrationPoPDomain))
	var chainID [8]byte
	binary.BigEndian.PutUint64(chainID[:], tx.Base.ChainId)
	_, _ = h.Write(chainID[:])
	writeMinerRegistrationField(h, []byte(hot))
	writeMinerRegistrationField(h, []byte(reward))
	writeMinerRegistrationField(h, []byte(endpoint))
	writeMinerRegistrationField(h, []byte(amount.String()))
	writeMinerRegistrationField(h, secpKey)
	writeMinerRegistrationField(h, blsKey)
	return h.Sum(nil), nil
}

func verifyMinerRegistrationPoP(tx *pb.MinerTx, blsKey []byte) error {
	if len(tx.BlsProof) == 0 {
		return fmt.Errorf("missing miner BLS proof-of-possession")
	}
	digest, err := MinerRegistrationPoPDigest(tx)
	if err != nil {
		return err
	}
	point, err := utils.DeserializePublicKey(blsKey)
	if err != nil {
		return fmt.Errorf("invalid miner BLS public key: %w", err)
	}
	if err := utils.BLSVerifySignature(point, string(digest), tx.BlsProof); err != nil {
		return fmt.Errorf("invalid miner BLS proof-of-possession: %w", err)
	}
	return nil
}

func completeRegisteredMinerIdentity(account *pb.Account, hot string, signingSecp []byte) ([]byte, error) {
	if account == nil || account.Address != hot || account.PublicKeys == nil {
		return nil, fmt.Errorf("miner identity is incomplete")
	}
	registeredSecp := account.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)]
	if !bytes.Equal(registeredSecp, signingSecp) {
		return nil, fmt.Errorf("registered miner secp256k1 key does not match signer")
	}
	registeredBLS, err := canonicalMinerBLSKey(account.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_BLS_BN256_G2)])
	if err != nil {
		return nil, fmt.Errorf("miner identity is incomplete: %w", err)
	}
	if strings.TrimSpace(account.Ip) == "" {
		return nil, fmt.Errorf("miner identity is incomplete: endpoint is missing")
	}
	if strings.TrimSpace(account.RewardAddress) == "" {
		return nil, fmt.Errorf("miner identity is incomplete: reward_address is missing")
	}
	if _, err := canonicalMinerRewardAddress(hot, account.RewardAddress); err != nil {
		return nil, fmt.Errorf("miner identity is incomplete: %w", err)
	}
	return registeredBLS, nil
}

                                                                            
                                                                               
                            
func prepareMinerIdentity(tx *pb.MinerTx, account *pb.Account, sv StateView) (string, *WriteOp, error) {
	hot := ResolveMinerHot(tx)
	secpKey, err := canonicalMinerSecpKey(tx.Base)
	if err != nil {
		return "", nil, err
	}
	if account == nil || account.Address != hot {
		return "", nil, fmt.Errorf("miner account identity mismatch")
	}
	if account.IsMiner && strings.TrimSpace(account.RewardAddress) == "" {
		return "", nil, fmt.Errorf("existing miner has no fixed reward_address; migrate genesis state before accepting ADD")
	}

	rewardInput := tx.RewardAddress
	if account.RewardAddress != "" {
		if rewardInput != "" && strings.TrimSpace(rewardInput) != account.RewardAddress {
			return "", nil, fmt.Errorf("reward_address is immutable once bound")
		}
		rewardInput = account.RewardAddress
	}
	reward, err := canonicalMinerRewardAddress(hot, rewardInput)
	if err != nil {
		return "", nil, err
	}

	registeredBLS, completeErr := completeRegisteredMinerIdentity(account, hot, secpKey)
	if completeErr == nil {
		if len(tx.BlsPublicKey) > 0 && !bytes.Equal(tx.BlsPublicKey, registeredBLS) {
			return "", nil, fmt.Errorf("registered miner BLS key is immutable")
		}
		if endpoint := strings.TrimSpace(tx.Endpoint); endpoint != "" && endpoint != account.Ip {
			return "", nil, fmt.Errorf("registered endpoint must be updated with MinerInfoTx")
		}
		account.RewardAddress = reward
		return bindMinerBLSKey(sv, hot, registeredBLS, reward)
	}

	                                                                              
	                                                                            
	                                                            
	blsKey, err := canonicalMinerBLSKey(tx.BlsPublicKey)
	if err != nil {
		return "", nil, fmt.Errorf("incomplete miner identity requires registration fields: %w", err)
	}
	if err := verifyMinerRegistrationPoP(tx, blsKey); err != nil {
		return "", nil, err
	}
	if account.PublicKeys == nil {
		account.PublicKeys = &pb.PublicKeys{Keys: make(map[int32][]byte)}
	}
	if account.PublicKeys.Keys == nil {
		account.PublicKeys.Keys = make(map[int32][]byte)
	}
	if existing := account.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)]; len(existing) > 0 && !bytes.Equal(existing, secpKey) {
		return "", nil, fmt.Errorf("registered miner secp256k1 key is immutable")
	}
	if existing := account.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_BLS_BN256_G2)]; len(existing) > 0 && !bytes.Equal(existing, blsKey) {
		return "", nil, fmt.Errorf("registered miner BLS key is immutable")
	}
	account.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)] = append([]byte(nil), secpKey...)
	account.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_BLS_BN256_G2)] = append([]byte(nil), blsKey...)
	account.Ip = strings.TrimSpace(tx.Endpoint)
	account.RewardAddress = reward
	return bindMinerBLSKey(sv, hot, blsKey, reward)
}

func bindMinerBLSKey(sv StateView, hot string, blsKey []byte, reward string) (string, *WriteOp, error) {
	key := keys.KeyMinerBLSPublicKeyOwner(blsKey)
	value, exists, err := sv.Get(key)
	if err != nil {
		return "", nil, fmt.Errorf("read miner BLS key binding: %w", err)
	}
	if exists && string(value) != hot {
		return "", nil, fmt.Errorf("miner BLS public key is already bound to another hot address")
	}
	if exists {
		return reward, nil, nil
	}
	op := &WriteOp{Key: key, Value: []byte(hot), Category: "miner_identity"}
	return reward, op, nil
}
