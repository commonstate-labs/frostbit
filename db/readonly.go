package db

import (
	"dex/config"
	"dex/logs"
	statedb "dex/stateDB"
	"fmt"
	"sync"
)

                                                            
                                                                                     
func NewReadOnlyManager(path string, logger logs.Logger) (*Manager, error) {
	return NewReadOnlyManagerWithConfig(path, logger, nil)
}

func NewReadOnlyManagerWithConfig(path string, logger logs.Logger, cfg *config.Config) (*Manager, error) {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	runtimeDB, err := openPebbleStore(resolveDBSubdir(path, cfg.Database.RuntimeDBDir, "runtime"), cfg, true, 100)
	if err != nil {
		return nil, fmt.Errorf("failed to open runtime pebble db for explorer: %w", err)
	}
	queryDB, err := openPebbleStore(resolveDBSubdir(path, cfg.Database.QueryDBDir, "query"), cfg, true, 100)
	if err != nil {
		_ = runtimeDB.Close()
		return nil, fmt.Errorf("failed to open query pebble db for explorer: %w", err)
	}
	archiveDB, err := openPebbleStore(resolveDBSubdir(path, cfg.Database.ArchiveDBDir, "archive"), cfg, true, 100)
	if err != nil {
		_ = queryDB.Close()
		_ = runtimeDB.Close()
		return nil, fmt.Errorf("failed to open archive pebble db for explorer: %w", err)
	}
	stateCfg := cfg.Database.StateDB
	tableCacheShards := stateCfg.TableCacheShards
	if tableCacheShards <= 0 {
		tableCacheShards = cfg.Database.TableCacheShards
	}
	stateStore, err := statedb.New(statedb.Config{
		Backend:          stateCfg.Backend,
		DataDir:          resolveStateDBDir(path, stateCfg),
		ShardHexWidth:    stateCfg.ShardHexWidth,
		PageSize:         stateCfg.PageSize,
		TableCacheShards: tableCacheShards,
		BlockCacheSize:   cfg.Database.BlockCacheSizeDB,
		CheckpointKeep:   stateCfg.CheckpointKeep,
		CheckpointEvery:  stateCfg.CheckpointEvery,
		ReadOnly:         true,
	})
	if err != nil {
		_ = archiveDB.Close()
		_ = queryDB.Close()
		_ = runtimeDB.Close()
		return nil, fmt.Errorf("failed to open stateDB for explorer: %w", err)
	}
	return &Manager{
		Db:        runtimeDB,
		runtimeDB: runtimeDB,
		queryDB:   queryDB,
		archiveDB: archiveDB,
		mu:        sync.RWMutex{},
		Logger:    logger,
		cfg:       cfg,
		stateDB:   stateStore,
	}, nil
}
