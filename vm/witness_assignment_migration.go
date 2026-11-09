package vm

import (
	"dex/keys"
	"sort"
)

                                                                             
                                                                             
                                                                             
func migrateLegacyWitnessAssignments(sv StateView) error {
	if _, exists, err := sv.Get(keys.KeyWitnessAssignmentQueryMigrated()); err != nil || exists {
		return err
	}
	rows, err := sv.Scan(keys.KeyLegacyWitnessAssignedPrefix())
	if err != nil {
		return err
	}
	ordered := make([]string, 0, len(rows))
	for key := range rows {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		sv.Del(key)
	}
	sv.Set(keys.KeyWitnessAssignmentQueryMigrated(), []byte{1})
	return nil
}
