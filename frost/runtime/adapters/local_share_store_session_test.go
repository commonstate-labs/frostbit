package adapters

import (
	"bytes"
	"encoding/json"
	"testing"

	"dex/keys"
)

func TestDBLocalShareStoreSessionScopedRevealMaterial(t *testing.T) {
	w := newFakeWriter()
	store := NewDBLocalShareStore(w)

	if err := store.SaveDkgRevealMaterial("btc", 1, 7, "session-a", 2, []byte{0x01}, []byte{0x11}); err != nil {
		t.Fatalf("save session-a reveal: %v", err)
	}
	if err := store.SaveDkgRevealMaterial("btc", 1, 7, "session-b", 2, []byte{0x02}, []byte{0x22}); err != nil {
		t.Fatalf("save session-b reveal: %v", err)
	}

	shareA, randA, err := store.LoadDkgRevealMaterial("btc", 1, 7, "session-a", 2)
	if err != nil {
		t.Fatalf("load session-a reveal: %v", err)
	}
	shareB, randB, err := store.LoadDkgRevealMaterial("btc", 1, 7, "session-b", 2)
	if err != nil {
		t.Fatalf("load session-b reveal: %v", err)
	}
	if !bytes.Equal(shareA, []byte{0x01}) || !bytes.Equal(randA, []byte{0x11}) ||
		!bytes.Equal(shareB, []byte{0x02}) || !bytes.Equal(randB, []byte{0x22}) {
		t.Fatalf("session-scoped reveal material collided: a=(%x,%x) b=(%x,%x)", shareA, randA, shareB, randB)
	}
	if err := store.SaveDkgRevealMaterial("btc", 1, 7, "session-a", 2, []byte{0xff}, []byte{0x11}); err == nil {
		t.Fatal("same session/receiver must reject conflicting reveal material")
	}
}

func TestDBLocalShareStorePendingPromotionSurvivesRestartWindow(t *testing.T) {
	w := newFakeWriter()
	store := NewDBLocalShareStore(w)
	share := []byte{0x10, 0x20, 0x30}

	if err := store.SaveDkgRevealMaterial("btc", 1, 7, "session-a", 2, []byte{0x01}, []byte{0x02}); err != nil {
		t.Fatalf("save reveal: %v", err)
	}
	if err := store.SavePendingLocalShare("btc", 1, 7, "session-a", share); err != nil {
		t.Fatalf("save pending share: %v", err)
	}
	if canonical, err := store.LoadLocalShare("btc", 1, 7); err != nil || canonical != nil {
		t.Fatalf("pending share leaked into canonical slot: share=%x err=%v", canonical, err)
	}

	                                                           
	restarted := NewDBLocalShareStore(w)
	refs, err := restarted.ListDkgLocalSessions()
	if err != nil || len(refs) != 1 || refs[0].SessionID != "session-a" {
		t.Fatalf("restart did not rediscover pending DKG session: refs=%+v err=%v", refs, err)
	}
	if err := restarted.PromotePendingLocalShare("btc", 1, 7, "session-a"); err != nil {
		t.Fatalf("promote pending share: %v", err)
	}

	                                                                        
	                                                                   
	restartedAgain := NewDBLocalShareStore(w)
	canonical, err := restartedAgain.LoadLocalShare("btc", 1, 7)
	if err != nil || !bytes.Equal(canonical, share) {
		t.Fatalf("canonical share missing after promotion restart: share=%x err=%v", canonical, err)
	}
	pending, err := restartedAgain.LoadPendingLocalShare("btc", 1, 7, "session-a")
	if err != nil || pending != nil {
		t.Fatalf("pending share survived promotion: share=%x err=%v", pending, err)
	}
	if has, err := restartedAgain.HasDkgLocalSession("btc", 1, 7, "session-a"); err != nil || !has {
		t.Fatalf("recovery marker must remain until planning completes: has=%v err=%v", has, err)
	}
	if err := restartedAgain.PromotePendingLocalShare("btc", 1, 7, "session-a"); err != nil {
		t.Fatalf("promotion must be idempotent after crash: %v", err)
	}
	if reveal, _, err := restartedAgain.LoadDkgRevealMaterial("btc", 1, 7, "session-a", 2); err != nil || reveal != nil {
		t.Fatalf("reveal material should be removed by promotion: reveal=%x err=%v", reveal, err)
	}
	if err := restartedAgain.DeleteDkgSessionArtifacts("btc", 1, 7, "session-a"); err != nil {
		t.Fatalf("delete completed recovery marker: %v", err)
	}
	if has, err := restartedAgain.HasDkgLocalSession("btc", 1, 7, "session-a"); err != nil || has {
		t.Fatalf("completed recovery marker remained: has=%v err=%v", has, err)
	}
}

func TestDBLocalShareStorePendingOnlyIsListedAndPromotedAtKeyReady(t *testing.T) {
	w := newFakeWriter()
	store := NewDBLocalShareStore(w)
	share := []byte{0x41, 0x42}
	if err := store.SavePendingLocalShare("btc", 1, 7, "session-a", share); err != nil {
		t.Fatalf("save pending share: %v", err)
	}

	                                                                            
	                                   
	w.EnqueueDel(keys.KeyFrostLocalDkgSession("btc", 1, 7, "session-a"))
	if err := w.ForceFlush(); err != nil {
		t.Fatalf("remove marker: %v", err)
	}
	restarted := NewDBLocalShareStore(w)
	refs, err := restarted.ListDkgLocalSessions()
	if err != nil || len(refs) != 1 || refs[0].SessionID != "session-a" {
		t.Fatalf("pending-only session was not recovered: refs=%+v err=%v", refs, err)
	}
	if err := restarted.PromotePendingLocalShare("btc", 1, 7, "session-a"); err != nil {
		t.Fatalf("promote pending-only KEY_READY share: %v", err)
	}
	canonical, err := restarted.LoadLocalShare("btc", 1, 7)
	if err != nil || !bytes.Equal(canonical, share) {
		t.Fatalf("pending-only share was not promoted: share=%x err=%v", canonical, err)
	}
}

func TestDBLocalShareStoreRejectsConflictingPendingShare(t *testing.T) {
	w := newFakeWriter()
	store := NewDBLocalShareStore(w)
	if err := store.SavePendingLocalShare("btc", 1, 7, "session-a", []byte{0x01}); err != nil {
		t.Fatalf("save pending share: %v", err)
	}
	if err := store.SavePendingLocalShare("btc", 1, 7, "session-a", []byte{0x02}); err == nil {
		t.Fatal("conflicting pending share must be rejected")
	}
	got, err := store.LoadPendingLocalShare("btc", 1, 7, "session-a")
	if err != nil || !bytes.Equal(got, []byte{0x01}) {
		t.Fatalf("conflict overwrote original pending share: share=%x err=%v", got, err)
	}
}

func TestDBLocalShareStoreCorruptMarkerFailsRecoveryClosed(t *testing.T) {
	w := newFakeWriter()
	store := NewDBLocalShareStore(w)
	if err := store.SaveDkgRevealMaterial("btc", 1, 7, "valid", 1, []byte{0x01}, []byte{0x02}); err != nil {
		t.Fatalf("save valid session: %v", err)
	}

	emptyKey := keys.KeyFrostLocalDkgSession("btc", 1, 7, "empty")
	w.EnqueueSet(emptyKey, "")
	w.EnqueueSet(keys.KeyFrostLocalDkgSessionPrefix()+"corrupt", "not-json")
	mismatchData, err := dkgLocalSessionMarkerData("btc", 1, 7, "other")
	if err != nil {
		t.Fatalf("encode mismatch marker: %v", err)
	}
	w.EnqueueSet(keys.KeyFrostLocalDkgSessionPrefix()+"wrong-key", string(mismatchData))

	if has, err := store.HasDkgLocalSession("btc", 1, 7, "empty"); err != nil || !has {
		t.Fatalf("even an empty existing marker must fence regeneration: has=%v err=%v", has, err)
	}
	if refs, err := store.ListDkgLocalSessions(); err == nil {
		t.Fatalf("corrupt marker must fail the whole recovery scan closed: %+v", refs)
	}
}

func TestDBLocalShareStoreCorruptOrMismatchedPendingFailsRecoveryClosed(t *testing.T) {
	t.Run("corrupt ciphertext", func(t *testing.T) {
		w := newFakeWriter()
		store := NewDBLocalShareStore(w)
		w.EnqueueSet(keys.KeyFrostPendingLocalShare("btc", 1, 7, "session-a"), "not-encrypted")
		if refs, err := store.ListDkgLocalSessions(); err == nil {
			t.Fatalf("corrupt pending ciphertext must fail recovery closed: %+v", refs)
		}
	})

	t.Run("metadata key mismatch", func(t *testing.T) {
		w := newFakeWriter()
		store := NewDBLocalShareStore(w)
		plain, err := json.Marshal(&pendingShareData{
			Chain: "btc", VaultID: 1, Epoch: 7, SessionID: "session-a", Share: []byte{0x01},
		})
		if err != nil {
			t.Fatalf("encode pending record: %v", err)
		}
		pendingKey := keys.KeyFrostPendingLocalShare("btc", 1, 7, "session-a")
		blob, err := store.cipher.encrypt(pendingKey, plain)
		if err != nil {
			t.Fatalf("encrypt pending record: %v", err)
		}
		w.EnqueueSet(keys.KeyFrostPendingLocalShare("btc", 1, 7, "wrong-session"), string(blob))
		if refs, err := store.ListDkgLocalSessions(); err == nil {
			t.Fatalf("mismatched pending metadata must fail recovery closed: %+v", refs)
		}
	})
}
