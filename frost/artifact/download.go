package artifact

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const maxBundleDownloadBytes = 64 << 20

type NodeStatus struct {
	ArchiveNode     bool   `json:"archive_node"`
	Ready           bool   `json:"ready"`
	CompleteThrough uint64 `json:"complete_through"`
	LatestHeight    uint64 `json:"latest_height"`
}

type downloadCandidate struct {
	base   string
	status NodeStatus
}

                                                                         
                                                                                
                               
func DownloadMigration(ctx context.Context, client *http.Client, nodeURLs []string, migrationID string) (*Bundle, string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	migrationID = strings.TrimSpace(migrationID)
	if migrationID == "" {
		return nil, "", fmt.Errorf("migration ID is required")
	}
	candidates := make([]downloadCandidate, 0, len(nodeURLs))
	for _, rawBase := range nodeURLs {
		base := strings.TrimRight(strings.TrimSpace(rawBase), "/")
		if base == "" {
			continue
		}
		status, err := getNodeStatus(ctx, client, base)
		if err == nil {
			candidates = append(candidates, downloadCandidate{base: base, status: status})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i].status.ArchiveNode && candidates[i].status.Ready
		right := candidates[j].status.ArchiveNode && candidates[j].status.Ready
		return left && !right
	})
	var lastErr error
	for _, candidate := range candidates {
		endpoint := candidate.base + "/frost/vault/migration/download?migration_id=" + url.QueryEscape(migrationID)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBundleDownloadBytes+1))
		_ = resp.Body.Close()
		if readErr != nil || len(body) > maxBundleDownloadBytes {
			lastErr = fmt.Errorf("download bundle from %s exceeds limit or failed", candidate.base)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("download bundle from %s: HTTP %d: %s", candidate.base, resp.StatusCode, strings.TrimSpace(string(body)))
			continue
		}
		bundle, err := UnmarshalBundle(body)
		if err != nil || bundle.MigrationID != migrationID {
			lastErr = fmt.Errorf("invalid migration bundle from %s: %v", candidate.base, err)
			continue
		}
		return bundle, candidate.base, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no node exposed FROST archive status")
	}
	return nil, "", lastErr
}

func getNodeStatus(ctx context.Context, client *http.Client, base string) (NodeStatus, error) {
	var status NodeStatus
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/frost/archive/status", nil)
	if err != nil {
		return status, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return status, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return status, fmt.Errorf("archive status HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&status); err != nil {
		return status, err
	}
	return status, nil
}
