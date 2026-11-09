package vm

import (
	"dex/pb"
	"strings"
)

func hasEmptyOrDuplicateMembers(members []string) bool {
	if len(members) == 0 {
		return true
	}
	seen := make(map[string]struct{}, len(members))
	for _, m := range members {
		member := strings.TrimSpace(m)
		if member == "" {
			return true
		}
		if _, ok := seen[member]; ok {
			return true
		}
		seen[member] = struct{}{}
	}
	return false
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func collectNonEmptyMinerAddresses(top *pb.FrostActiveMiners) []string {
	if top == nil || len(top.Addresses) == 0 {
		return nil
	}
	out := make([]string, 0, len(top.Addresses))
	for _, addr := range top.Addresses {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		out = append(out, addr)
	}
	return out
}
