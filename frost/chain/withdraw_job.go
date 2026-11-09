package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
)

                                                               
func ComputeWithdrawJobID(chain, asset string, vaultID uint32, firstSeq uint64, templateHash []byte, keyEpoch uint64) string {
	data := chain + "|" + asset + "|" +
		strconv.FormatUint(uint64(vaultID), 10) + "|" + strconv.FormatUint(firstSeq, 10) + "|" +
		hex.EncodeToString(templateHash) + "|" + strconv.FormatUint(keyEpoch, 10)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

                                                                                                     
func WithdrawCoordinator(jobID string, keyEpoch uint64, committee []string) (string, error) {
	if len(committee) == 0 {
		return "", errors.New("empty committee")
	}
	seedData := jobID + "|" + string(rune(keyEpoch)) + "|frost_agg"
	seed := sha256.Sum256([]byte(seedData))
	ordered := append([]string(nil), committee...)
	for i := len(ordered) - 1; i > 0; i-- {
		step := sha256.Sum256(append(seed[:], byte(i)))
		j := int(step[0]) % (i + 1)
		ordered[i], ordered[j] = ordered[j], ordered[i]
	}
	return ordered[0], nil
}
