package db

import (
	"testing"

	"dex/keys"
	"dex/logs"
)

type finalizedIdentityWriteOp struct {
	key   string
	value []byte
}

func (op finalizedIdentityWriteOp) GetKey() string   { return op.key }
func (op finalizedIdentityWriteOp) GetValue() []byte { return op.value }
func (finalizedIdentityWriteOp) IsDel() bool         { return false }

func TestFinalizedStateBlockIdentitySurvivesManagerRestart(t *testing.T) {
	root := t.TempDir()
	manager, err := NewManager(root, logs.NewNodeLogger("finalized_identity", 0))
	if err != nil {
		t.Fatal(err)
	}

	stateKey := keys.KeyAccount("finalized-identity-account")
	if _, err := manager.SyncFinalizedStateUpdatesForBlock(11, "block-11", []interface{}{
		finalizedIdentityWriteOp{key: stateKey, value: []byte("state-11")},
	}, false); err != nil {
		manager.Close()
		t.Fatal(err)
	}
	                                                                         
	                                                                          
	if _, err := manager.SyncFinalizedStateUpdatesForBlock(12, "block-12", nil, false); err != nil {
		manager.Close()
		t.Fatal(err)
	}
	manager.Close()

	restarted, err := NewManager(root, logs.NewNodeLogger("finalized_identity_restart", 0))
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	for height, want := range map[uint64]string{11: "block-11", 12: "block-12"} {
		got, exists, err := restarted.FinalizedStateBlockID(height)
		if err != nil || !exists || got != want {
			t.Fatalf("height %d identity=(%q,%t,%v) want %q", height, got, exists, err, want)
		}
	}
	if height, exists, err := restarted.GetFinalizedStateHeight(); err != nil || !exists || height != 12 {
		t.Fatalf("finalized StateDB height=(%d,%t,%v), want 12", height, exists, err)
	}
	if err := restarted.RecoverFinalizedStateCommit(12, "block-12", true); err != nil {
		t.Fatalf("recover finalized state artifacts: %v", err)
	}
	if marker, err := restarted.Get(keys.KeyVMCommitHeight(12)); err != nil || string(marker) != "block-12" {
		t.Fatalf("recovered runtime marker=%q err=%v", marker, err)
	}
	if latest, err := restarted.stateDB.LatestCheckpointHeight(); err != nil || latest < 12 {
		t.Fatalf("recovered checkpoint height=%d err=%v", latest, err)
	}
	if got, err := restarted.Get(stateKey); err != nil || string(got) != "state-11" {
		t.Fatalf("state after restart=%q err=%v", got, err)
	}
}
