package certchain

const DefaultCheckpointInterval uint64 = 100

                                    
type Config struct {
	CheckpointInterval uint64                                  
	Quorum             float64                                                              
}

func DefaultConfig() Config {
	return Config{
		CheckpointInterval: DefaultCheckpointInterval,
		Quorum:             0.667,                    
	}
}
