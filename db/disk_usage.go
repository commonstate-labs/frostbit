package db

import (
	"fmt"

	"dex/utils/diskspace"
)

func (manager *Manager) diskPath() string {
	if manager == nil || manager.dataDir == "" {
		return "."
	}
	return manager.dataDir
}

                                                                            
                                                                               
                                     
func (manager *Manager) DiskUsage() (diskspace.Snapshot, error) {
	if manager == nil {
		return diskspace.Snapshot{Path: "."}, fmt.Errorf("database manager is nil")
	}
	return diskspace.Usage(manager.diskPath())
}
