package interfaces

                                                                     
                                                                               
                                                                       
type SyncPressureState struct {
	Syncing      bool
	LocalHeight  uint64
	TargetHeight uint64
	Lag          uint64
	CatchUp      bool
	Reason       string
}

type SyncPressureReader interface {
	SyncPressureState() SyncPressureState
}
