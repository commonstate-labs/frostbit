package certchain

import (
	"bytes"
	"fmt"

	"dex/logs"
	"dex/pb"
	"dex/utils"

	"go.dedis.ch/kyber/v3"
)

                                                                          
                                                                       
                                                                
func VerifyCertChain(genesisMinerSet map[uint32]*MinerSnapshot, certs []*pb.CheckpointCertificate, quorum float64, interval uint64) (lastVerifiedHeight uint64, lastStateRoot []byte, err error) {
	if len(genesisMinerSet) == 0 {
		return 0, nil, fmt.Errorf("empty genesis miner set")
	}
	if len(certs) == 0 {
		return 0, nil, fmt.Errorf("empty certificate chain")
	}
	if err := validateMinerSet(genesisMinerSet); err != nil {
		return 0, nil, fmt.Errorf("invalid genesis miner set: %w", err)
	}
	currentMiners, _ := cloneMinerSet(genesisMinerSet)
	chainID := pb.GetDefaultChainID()
	var prevHeight uint64

	for i, cert := range certs {
		if cert == nil {
			return 0, nil, fmt.Errorf("cert %d: nil certificate", i)
		}
		if cert.Height == 0 {
			return 0, nil, fmt.Errorf("cert %d: zero height", i)
		}
		if interval > 0 && cert.Height%interval != 0 {
			return 0, nil, fmt.Errorf("cert %d (height %d): not aligned to interval %d", i, cert.Height, interval)
		}
		if i == 0 {
			if cert.PrevCheckpoint != 0 {
				return 0, nil, fmt.Errorf("cert %d (height %d): first prev_checkpoint %d != 0", i, cert.Height, cert.PrevCheckpoint)
			}
			if interval > 0 && cert.Height != interval {
				return 0, nil, fmt.Errorf("cert %d (height %d): first height must equal interval %d", i, cert.Height, interval)
			}
		} else {
			if cert.Height <= prevHeight {
				return 0, nil, fmt.Errorf("cert %d (height %d): not strictly increasing after %d", i, cert.Height, prevHeight)
			}
			if interval > 0 && cert.Height-prevHeight != interval {
				return 0, nil, fmt.Errorf("cert %d (height %d): gap after %d", i, cert.Height, prevHeight)
			}
			if cert.PrevCheckpoint != prevHeight {
				return 0, nil, fmt.Errorf("cert %d (height %d): prev_checkpoint %d != %d", i, cert.Height, cert.PrevCheckpoint, prevHeight)
			}
		}

		if cert.ChainId != chainID {
			return 0, nil, fmt.Errorf("cert %d (height %d): chain_id %d != %d", i, cert.Height, cert.ChainId, chainID)
		}
		if cert.CommitteeVersion != CommitteeVersionTopStakeV1 {
			return 0, nil, fmt.Errorf("cert %d (height %d): unsupported committee version %d", i, cert.Height, cert.CommitteeVersion)
		}
		if cert.MinerCount != uint32(len(currentMiners)) {
			return 0, nil, fmt.Errorf("cert %d (height %d): miner_count %d != Active set size %d", i, cert.Height, cert.MinerCount, len(currentMiners))
		}
		committee, _, selectErr := SelectTopStakeCommittee(currentMiners)
		if selectErr != nil {
			return 0, nil, fmt.Errorf("cert %d (height %d): derive committee: %w", i, cert.Height, selectErr)
		}
		if len(committee) == 0 {
			return 0, nil, fmt.Errorf("cert %d (height %d): derived empty committee", i, cert.Height)
		}
		if cert.CommitteeSize != uint32(len(committee)) {
			return 0, nil, fmt.Errorf("cert %d (height %d): committee_size %d != derived %d", i, cert.Height, cert.CommitteeSize, len(committee))
		}
		threshold := quorumThresholdForMinerCount(len(committee), quorum)
		signerIndexes := bitmapToIndexes(cert.Bitmap)
		if len(signerIndexes) < threshold {
			return 0, nil, fmt.Errorf("cert %d (height %d): signers %d < threshold %d", i, cert.Height, len(signerIndexes), threshold)
		}
		if int(cert.SignerCount) != len(signerIndexes) {
			return 0, nil, fmt.Errorf("cert %d (height %d): signer_count %d != bitmap count %d", i, cert.Height, cert.SignerCount, len(signerIndexes))
		}

		diffs, parseErr := minerDiffsFromPB(cert.Diffs)
		if parseErr != nil {
			return 0, nil, fmt.Errorf("cert %d (height %d): %w", i, cert.Height, parseErr)
		}
		digest := CertDigestFromCert(cert)
		pubKeys := make([]kyber.Point, 0, len(signerIndexes))
		for _, idx := range signerIndexes {
			miner := committee[idx]
			if miner == nil {
				return 0, nil, fmt.Errorf("cert %d (height %d): signer %d is not in derived committee", i, cert.Height, idx)
			}
			pk, keyErr := utils.DeserializePublicKey(miner.PubKey)
			if keyErr != nil {
				return 0, nil, fmt.Errorf("cert %d (height %d): bad pubkey for signer %d: %w", i, cert.Height, idx, keyErr)
			}
			pubKeys = append(pubKeys, pk)
		}
		if err := utils.BLSAggregateVerify(pubKeys, digest, cert.AggSig); err != nil {
			return 0, nil, fmt.Errorf("cert %d (height %d): BLS verify failed: %w", i, cert.Height, err)
		}

		nextMiners, _ := cloneMinerSet(currentMiners)
		if err := applyMinerDiffs(nextMiners, diffs); err != nil {
			return 0, nil, fmt.Errorf("cert %d (height %d): invalid Active-set diff: %w", i, cert.Height, err)
		}
		if len(nextMiners) == 0 {
			return 0, nil, fmt.Errorf("cert %d (height %d): diff produces empty Active set", i, cert.Height)
		}
		nextRoot, rootErr := ValidatorSetRoot(chainID, nextMiners)
		if rootErr != nil {
			return 0, nil, fmt.Errorf("cert %d (height %d): compute next Active-set root: %w", i, cert.Height, rootErr)
		}
		if len(cert.NextValidatorSetRoot) != len(nextRoot) || !bytes.Equal(cert.NextValidatorSetRoot, nextRoot) {
			return 0, nil, fmt.Errorf("cert %d (height %d): next_validator_set_root mismatch", i, cert.Height)
		}

		currentMiners = nextMiners
		prevHeight = cert.Height
		lastVerifiedHeight = cert.Height
		lastStateRoot = append([]byte(nil), cert.StateRoot...)
		logs.Debug("[CertChain] verified cert height=%d signers=%d/%d Active=%d", cert.Height, cert.SignerCount, len(committee), len(currentMiners))
	}
	return lastVerifiedHeight, lastStateRoot, nil
}

func bitmapToIndexes(bitmap []byte) []uint32 {
	var indexes []uint32
	for byteIdx, b := range bitmap {
		for bit := uint32(0); bit < 8; bit++ {
			if b&(1<<bit) != 0 {
				indexes = append(indexes, uint32(byteIdx)*8+bit)
			}
		}
	}
	return indexes
}
