package consensus

import "testing"

func TestShouldRestartBlockCatchUpAsDeepSync(t *testing.T) {
	sm := &SyncManager{config: &SyncConfig{DeepLagStateSyncThreshold: 10}}

	tests := []struct {
		name          string
		session       *SyncSession
		localAccepted uint64
		maxPeer       uint64
		want          bool
	}{
		{
			name:          "block catch-up over threshold",
			session:       &SyncSession{Stage: SyncStageBlockCatchUp},
			localAccepted: 100,
			maxPeer:       111,
			want:          true,
		},
		{
			name:          "block catch-up at threshold",
			session:       &SyncSession{Stage: SyncStageBlockCatchUp},
			localAccepted: 100,
			maxPeer:       110,
			want:          false,
		},
		{
			name:          "non block catch-up",
			session:       &SyncSession{Stage: SyncStageStateSnapshot},
			localAccepted: 100,
			maxPeer:       120,
			want:          false,
		},
		{
			name:          "peer not ahead",
			session:       &SyncSession{Stage: SyncStageBlockCatchUp},
			localAccepted: 100,
			maxPeer:       100,
			want:          false,
		},
		{
			name:          "nil session",
			session:       nil,
			localAccepted: 100,
			maxPeer:       120,
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sm.shouldRestartBlockCatchUpAsDeepSync(tt.session, tt.localAccepted, tt.maxPeer)
			if got != tt.want {
				t.Fatalf("shouldRestartBlockCatchUpAsDeepSync() = %v, want %v", got, tt.want)
			}
		})
	}
}
