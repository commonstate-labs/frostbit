package db

import (
	"dex/keys"
	"dex/pb"
	"time"

	"google.golang.org/protobuf/proto"
)

                                     
func (mgr *Manager) GetAllNodeInfos() ([]*pb.NodeInfo, error) {
	var nodes []*pb.NodeInfo
	items, err := mgr.ScanKV(keys.KeyNode())
	if err != nil {
		return nil, err
	}
	for _, val := range items {
		var node pb.NodeInfo
		if err := proto.Unmarshal(val, &node); err != nil {
			return nil, err
		}
		nodes = append(nodes, &node)
	}
	return nodes, nil
}

                                                                             
                                               
func (mgr *Manager) getCachedDiscoveryIPs(maxAge time.Duration) []string {
	mgr.discoveryNodesMu.RLock()
	if time.Since(mgr.discoveryNodesCacheAt) < maxAge && mgr.discoveryNodesCache != nil {
		out := make([]string, len(mgr.discoveryNodesCache))
		copy(out, mgr.discoveryNodesCache)
		mgr.discoveryNodesMu.RUnlock()
		return out
	}
	mgr.discoveryNodesMu.RUnlock()

	nodes, err := mgr.GetAllNodeInfos()
	if err != nil {
		return nil
	}
	ips := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n != nil && n.Ip != "" {
			ips = append(ips, n.Ip)
		}
	}

	mgr.discoveryNodesMu.Lock()
	mgr.discoveryNodesCache = ips
	mgr.discoveryNodesCacheAt = time.Now()
	mgr.discoveryNodesMu.Unlock()

	out := make([]string, len(ips))
	copy(out, ips)
	return out
}

                                  
func (mgr *Manager) SaveNodeInfo(node *pb.NodeInfo) error {
	data, err := ProtoMarshal(node)
	if err != nil {
		return err
	}
	var nodeKey string
	if node.PublicKeys != nil && len(node.PublicKeys.Keys) > 0 {
		if pk, ok := node.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)]; ok {
			nodeKey = string(pk)
		}
	}
	if nodeKey == "" {
		nodeKey = node.Ip
	}
	key := keys.KeyNode() + nodeKey
	mgr.EnqueueSet(key, string(data))
	return nil
}
