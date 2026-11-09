package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"dex/config"
	"dex/consensus"
	"dex/keys"

	"github.com/cockroachdb/pebble"
)

const frostLateJoinSyncMarkerVersion uint32 = 1

                                                                             
                                                                             
                                                                         
type frostLateJoinSyncMarker struct {
	Version            uint32 `json:"version"`
	NetworkChainID     uint64 `json:"network_chain_id"`
	PeerManifestDigest string `json:"peer_manifest_digest"`
	NodeID             string `json:"node_id"`
	VerifiedHeight     uint64 `json:"verified_height"`
	VerifiedBlockID    string `json:"verified_block_id"`
}

type frostLateJoinSyncMarkerDB interface {
	Get(key string) ([]byte, error)
	EnqueueSet(key, value string)
	ForceFlushHot() error
}

type frostLateJoinConsensus interface {
	GetLastAccepted() (string, uint64)
	GetFinalizedBlockID(height uint64) (string, bool)
	SetVerifiedSyncObserver(observer func(consensus.VerifiedSyncPoint) error)
}

                                                                            
                                                                              
                                                                            
                               
func configureFrostLateJoinSyncGate(node *NodeInstance, mode BootstrapMode, frostCfg config.FrostConfig) (bool, error) {
	if mode != LateJoin {
		return false, nil
	}
	if node == nil || node.DBManager == nil || node.ConsensusManager == nil {
		return false, errors.New("late-join FROST sync marker dependencies are unavailable")
	}
	if frostCfg.Enabled {
		if err := validateFrostLateJoinSyncIdentity(node); err != nil {
			return false, err
		}
	}

	present := false
	if frostCfg.Enabled {
		_, varPresent, err := loadFrostLateJoinSyncMarker(node.DBManager, node.ConsensusManager, node)
		if err != nil {
			return false, err
		}
		present = varPresent
	}
	node.ConsensusManager.SetVerifiedSyncObserver(func(point consensus.VerifiedSyncPoint) error {
		if _, err := validatePersistedFrostGenesisMarker(node.DBManager, frostCfg); err != nil {
			reason := fmt.Sprintf("FROST configured-genesis mismatch after authenticated sync: %v", err)
			if node.Emergency != nil {
				node.Emergency.Stop(reason)
			}
			node.ConsensusManager.SetPaused(true)
			node.ConsensusManager.Stop()
			return errors.New(reason)
		}
		if !frostCfg.Enabled {
			return nil
		}
		return persistFrostLateJoinSyncMarker(node.DBManager, node.ConsensusManager, node, point)
	})
	return frostCfg.Enabled && !present, nil
}

func validateFrostLateJoinSyncIdentity(node *NodeInstance) error {
	if node.NetworkChainID == 0 || len(node.FrostPeerManifestDigest) != 32 || node.Address == "" {
		return errors.New("late-join sync marker requires a chain ID, 32-byte peer-manifest digest, and node identity")
	}
	return nil
}

func expectedFrostLateJoinSyncMarker(node *NodeInstance, point consensus.VerifiedSyncPoint) frostLateJoinSyncMarker {
	return frostLateJoinSyncMarker{
		Version:            frostLateJoinSyncMarkerVersion,
		NetworkChainID:     node.NetworkChainID,
		PeerManifestDigest: hex.EncodeToString(node.FrostPeerManifestDigest),
		NodeID:             node.Address,
		VerifiedHeight:     point.Height,
		VerifiedBlockID:    point.BlockID,
	}
}

func loadFrostLateJoinSyncMarker(
	store frostLateJoinSyncMarkerDB,
	chain frostLateJoinConsensus,
	node *NodeInstance,
) (frostLateJoinSyncMarker, bool, error) {
	var marker frostLateJoinSyncMarker
	raw, err := store.Get(keys.KeyFrostLateJoinSyncMarker())
	if errors.Is(err, pebble.ErrNotFound) {
		return marker, false, nil
	}
	if err != nil {
		return marker, false, fmt.Errorf("read late-join sync marker: %w", err)
	}
	if len(raw) == 0 {
		return marker, false, errors.New("late-join sync marker is empty")
	}
	if err := json.Unmarshal(raw, &marker); err != nil {
		return marker, false, fmt.Errorf("decode late-join sync marker: %w", err)
	}
	canonical, err := json.Marshal(marker)
	if err != nil || !bytes.Equal(raw, canonical) {
		return marker, false, errors.New("late-join sync marker is not canonical JSON")
	}
	expectedDigest := hex.EncodeToString(node.FrostPeerManifestDigest)
	if marker.Version != frostLateJoinSyncMarkerVersion || marker.NetworkChainID != node.NetworkChainID ||
		marker.PeerManifestDigest != expectedDigest || marker.NodeID != node.Address {
		return marker, false, errors.New("late-join sync marker does not match this network genesis or node identity")
	}
	if marker.VerifiedHeight == 0 || marker.VerifiedBlockID == "" {
		return marker, false, errors.New("late-join sync marker has no verified target")
	}
	acceptedID, acceptedHeight := chain.GetLastAccepted()
	if acceptedHeight < marker.VerifiedHeight {
		return marker, false, fmt.Errorf("late-join sync marker height %d is ahead of local finalized height %d", marker.VerifiedHeight, acceptedHeight)
	}
	if acceptedHeight == marker.VerifiedHeight && acceptedID != marker.VerifiedBlockID {
		return marker, false, errors.New("late-join sync marker conflicts with the local finalized tip")
	}
	return marker, true, nil
}

func persistFrostLateJoinSyncMarker(
	store frostLateJoinSyncMarkerDB,
	chain frostLateJoinConsensus,
	node *NodeInstance,
	point consensus.VerifiedSyncPoint,
) error {
	if store == nil || chain == nil || node == nil {
		return errors.New("late-join sync marker persistence dependencies are unavailable")
	}
	if err := validateFrostLateJoinSyncIdentity(node); err != nil {
		return err
	}
	if point.Height == 0 || point.BlockID == "" {
		return errors.New("cannot persist an empty verified sync target")
	}
	_, acceptedHeight := chain.GetLastAccepted()
	if acceptedHeight < point.Height {
		return errors.New("verified sync target is not installed locally")
	}
	blockID, ok := chain.GetFinalizedBlockID(point.Height)
	if !ok || blockID != point.BlockID {
		return errors.New("verified sync target block does not match local finalized state")
	}

	current, present, err := loadFrostLateJoinSyncMarker(store, chain, node)
	if err != nil {
		return err
	}
	if present {
		if current.VerifiedHeight > point.Height {
			return nil
		}
		if current.VerifiedHeight == point.Height {
			if current.VerifiedBlockID != point.BlockID {
				return errors.New("verified sync target conflicts with the durable marker")
			}
			return nil
		}
	}

	marker := expectedFrostLateJoinSyncMarker(node, point)
	encoded, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("encode late-join sync marker: %w", err)
	}
	store.EnqueueSet(keys.KeyFrostLateJoinSyncMarker(), string(encoded))
	if err := store.ForceFlushHot(); err != nil {
		return fmt.Errorf("flush late-join sync marker: %w", err)
	}
	written, err := store.Get(keys.KeyFrostLateJoinSyncMarker())
	if err != nil || !bytes.Equal(written, encoded) {
		return errors.New("late-join sync marker durability verification failed")
	}
	return nil
}

func frostCatchupReady(status consensus.SyncStatus, localHeight uint64) bool {
	return status.VerifiedSyncCompletions > 0 && status.VerifiedSyncHeight > 0 &&
		!status.IsSyncing && localHeight >= status.VerifiedSyncHeight
}
