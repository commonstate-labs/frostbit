                                 
package planning

import (
	"fmt"
	"math"

	chainpkg "dex/frost/chain"
	"dex/pb"

	"github.com/btcsuite/btcd/btcutil"
)

                                                                           
type Job struct {
	JobID              string
	Chain              string
	Asset              string
	VaultID            uint32
	KeyEpoch           uint64
	WithdrawIDs        []string
	TemplateHash       []byte
	TemplateData       []byte
	FirstSeq           uint64
	BatchSeq           string
	RewardTokens       []string
	IncludesHandover   bool
	Logs               []*pb.FrostPlanningLog
	BTCProtocolVersion uint32
	BTCPartSeq         uint64
	BTCGrossAmount     string
	BTCFeeAmount       string
	BTCOriginID        []byte
	BTCReservationKey  string                                                       
}

                                                                                           
func generateJobID(chain, asset string, vaultID uint32, firstSeq uint64, templateHash []byte, keyEpoch uint64) string {
	return chainpkg.ComputeWithdrawJobID(chain, asset, vaultID, firstSeq, templateHash, keyEpoch)
}

                                                                              
                                                                            
func parseBTCAmount(amount string) (uint64, error) {
	parsed, err := chainpkg.ParseWithdrawAmount(amount)
	if err != nil || !parsed.IsUint64() || parsed.Uint64() > uint64(btcutil.MaxSatoshi) {
		return 0, fmt.Errorf("invalid BTC amount %q", amount)
	}
	return parsed.Uint64(), nil
}

func addUint64Checked(left, right uint64) (uint64, error) {
	if right > math.MaxUint64-left {
		return 0, fmt.Errorf("uint64 amount overflow")
	}
	return left + right, nil
}
