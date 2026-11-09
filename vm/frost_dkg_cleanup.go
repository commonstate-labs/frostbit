package vm

import "dex/keys"

type frostDkgCleanupPrefix struct {
	prefix   string
	category string
}

func frostDkgSessionCleanupPrefixes(chain string, vaultID uint32, epochID uint64, sessionID string) []frostDkgCleanupPrefix {
	return []frostDkgCleanupPrefix{
		{prefix: keys.KeyFrostVaultDkgCommitPrefix(chain, vaultID, epochID, sessionID), category: "frost_dkg_commit"},
		{prefix: keys.KeyFrostVaultDkgSharePrefix(chain, vaultID, epochID, sessionID), category: "frost_dkg_share"},
		{prefix: keys.KeyFrostVaultDkgComplaintPrefix(chain, vaultID, epochID, sessionID), category: "frost_dkg_complaint"},
		{prefix: keys.KeyFrostVaultDkgValidationAckPrefix(chain, vaultID, epochID, sessionID), category: "frost_dkg_validation_ack"},
	}
}

func appendFrostDkgSessionCleanupOps(sv StateView, ops []WriteOp, chain string, vaultID uint32, epochID uint64, sessionID string) ([]WriteOp, int, error) {
	if sessionID == "" {
		return ops, 0, nil
	}
	deleted := 0
	for _, item := range frostDkgSessionCleanupPrefixes(chain, vaultID, epochID, sessionID) {
		kvs, err := sv.Scan(item.prefix)
		if err != nil {
			return ops, deleted, err
		}
		for _, key := range sortedBytesMapKeys(kvs) {
			ops = append(ops, WriteOp{Key: key, Del: true, Category: item.category})
			deleted++
		}
	}
	return ops, deleted, nil
}

func cleanupFrostDkgSessionTempState(sv StateView, chain string, vaultID uint32, epochID uint64, sessionID string) (int, error) {
	ops, deleted, err := appendFrostDkgSessionCleanupOps(sv, nil, chain, vaultID, epochID, sessionID)
	if err != nil {
		return 0, err
	}
	for _, op := range ops {
		sv.Del(op.Key)
	}
	return deleted, nil
}
