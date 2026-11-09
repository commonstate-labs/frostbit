package sender

import (
	"dex/logs"
	"dex/pb"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
)

const maxDiscoveryEntries = 50000

func (sm *SenderManager) StartMinerIPDiscovery() {
	if sm == nil || sm.cfg == nil || sm.dbManager == nil || !sm.cfg.Discovery.Enabled {
		return
	}

	interval := sm.cfg.Discovery.SyncInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}

	sm.discoveryStartOnce.Do(func() {
		sm.discoveryWg.Add(1)
		go sm.discoveryLoop(interval)
		logs.Info("[Discovery] started: interval=%s fanout=%d", interval, sm.cfg.Discovery.Fanout)
	})
}

func (sm *SenderManager) discoveryLoop(interval time.Duration) {
	defer sm.discoveryWg.Done()

	sm.runMinerIPDiscoveryRound()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-sm.discoveryStopCh:
			return
		case <-ticker.C:
			sm.runMinerIPDiscoveryRound()
		}
	}
}

func (sm *SenderManager) runMinerIPDiscoveryRound() {
	targets := sm.discoveryTargets()
	if len(targets) == 0 {
		return
	}

	fanout := sm.cfg.Discovery.Fanout
	if fanout <= 0 {
		fanout = 1
	}
	if fanout > len(targets) {
		fanout = len(targets)
	}

	minHeight := sm.discoveryLastHeight.Load()
	start := int(sm.discoveryRound.Add(1)-1) % len(targets)
	for i := 0; i < fanout; i++ {
		target := targets[(start+i)%len(targets)]
		resp, err := sm.fetchMinerIPMap(target, minHeight)
		if err != nil {
			logs.Debug("[Discovery] fetch from %s failed: %v", target, err)
			continue
		}
		updated, err := sm.applyMinerIPMap(resp)
		if err != nil {
			logs.Warn("[Discovery] apply map from %s failed: %v", target, err)
			continue
		}
		if updated > 0 {
			logs.Info("[Discovery] synced from %s height=%d updates=%d", target, resp.GetHeight(), updated)
		}
	}
}

func (sm *SenderManager) discoveryTargets() []string {
	seen := make(map[string]struct{})
	selfAddress := strings.TrimSpace(sm.address)
	selfIP := ""
	if acc, err := sm.dbManager.GetAccount(selfAddress); err == nil && acc != nil {
		selfIP = strings.TrimSpace(acc.Ip)
	}

	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || v == selfAddress || v == selfIP {
			return
		}
		seen[v] = struct{}{}
	}

	for _, peer := range sm.cfg.Discovery.BootstrapPeers {
		add(peer)
	}

	sampleCount := sm.cfg.Discovery.Fanout * 3
	if sampleCount < 3 {
		sampleCount = 3
	}
	if miners, err := sm.getRandomMiners(sampleCount); err == nil {
		for _, miner := range miners {
			if miner == nil {
				continue
			}
			add(miner.Address)
		}
	}

	if nodes, err := sm.dbManager.GetAllNodeInfos(); err == nil {
		for _, node := range nodes {
			if node == nil {
				continue
			}
			add(node.GetIp())
		}
	}

	out := make([]string, 0, len(seen))
	for target := range seen {
		out = append(out, target)
	}
	sort.Strings(out)
	return out
}

func (sm *SenderManager) fetchMinerIPMap(target string, minHeight uint64) (*pb.GetMinerIPMapResponse, error) {
	targetIP := strings.TrimSpace(target)
	if targetIP == "" {
		return nil, fmt.Errorf("empty discovery target")
	}

	if resolved, err := sm.AddressToIP(targetIP); err == nil {
		targetIP = resolved
	} else {
		if _, _, splitErr := net.SplitHostPort(targetIP); splitErr != nil {
			return nil, err
		}
	}

	reqBytes, err := proto.Marshal(&pb.GetMinerIPMapRequest{MinHeight: minHeight})
	if err != nil {
		return nil, err
	}

	body, err := postProtobufAndRead(&SendTask{Target: targetIP, NodeAddress: sm.address}, sm.httpClient,
		"miner discovery", "/discovery/miner_ips", reqBytes)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("empty discovery response")
	}

	out := &pb.GetMinerIPMapResponse{}
	if err := pb.UnmarshalSanitize(body, out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, fmt.Errorf("remote error: %s", out.Error)
	}
	return out, nil
}

func (sm *SenderManager) applyMinerIPMap(resp *pb.GetMinerIPMapResponse) (int, error) {
	if sm == nil || sm.dbManager == nil || resp == nil {
		return 0, nil
	}
	if len(resp.Entries) > maxDiscoveryEntries {
		return 0, fmt.Errorf("too many discovery entries: %d", len(resp.Entries))
	}

	prevHeight := sm.discoveryLastHeight.Load()
	if resp.Height < prevHeight {
		return 0, nil
	}

	updated := 0
	for _, entry := range resp.Entries {
		if entry == nil || entry.Address == "" {
			continue
		}
		ip := strings.TrimSpace(entry.Ip)
		if ip == "" {
			continue
		}

		account, err := sm.dbManager.GetAccount(entry.Address)
		if err != nil || account == nil || !account.IsMiner {
			continue
		}

		                                                                          
		                                                                     
		                                                                          
		                                                                    
		verifiedIP := strings.TrimSpace(account.Ip)
		if verifiedIP == "" || ip != verifiedIP {
			continue
		}

		existing := sm.LookupMinerIP(entry.Address)
		if existing == verifiedIP {
			continue
		}
		sm.SetMinerIP(entry.Address, verifiedIP)
		updated++
	}

	                                                              
	if resp.Height > prevHeight {
		sm.discoveryLastHeight.Store(resp.Height)
	}

	return updated, nil
}
