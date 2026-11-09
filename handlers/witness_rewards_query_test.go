package handlers

import (
	"dex/interfaces"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

const rewardQueryOwner = "fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8"

type spyRechargeRewardStore struct {
	values       map[string][]byte
	scanCalls    int
	getManyCalls int
	scanLimit    int
	scanError    error
	getError     error
}

func (s *spyRechargeRewardStore) ScanKVWithLimitOrderedAfter(prefix, after string, limit int, reverse bool) ([]interfaces.KVPair, error) {
	s.scanCalls++
	s.scanLimit = limit
	if s.scanError != nil {
		return nil, s.scanError
	}
	if reverse {
		panic("reward query must scan forward")
	}
	keysFound := make([]string, 0)
	for key := range s.values {
		if strings.HasPrefix(key, prefix) && (after == "" || key > after) {
			keysFound = append(keysFound, key)
		}
	}
	sort.Strings(keysFound)
	rows := make([]interfaces.KVPair, 0, min(limit, len(keysFound)))
	for _, key := range keysFound[:min(limit, len(keysFound))] {
		rows = append(rows, interfaces.KVPair{Key: key, Value: s.values[key]})
	}
	return rows, nil
}

func (s *spyRechargeRewardStore) GetKVs(requested []string) (map[string][]byte, error) {
	s.getManyCalls++
	if s.getError != nil {
		return nil, s.getError
	}
	out := make(map[string][]byte)
	for _, key := range requested {
		if raw, ok := s.values[key]; ok {
			out[key] = raw
		}
	}
	return out, nil
}

func rewardValue(t *testing.T, id string, receiver, requester, witness string) []byte {
	t.Helper()
	raw, err := proto.Marshal(&pb.RechargeRewardAttribution{
		RequestId: id, TokenAddress: "BTC", ReceiverNetAmount: receiver,
		RequesterRewardAmount: requester, WitnessRewardAmount: witness, FinalizeHeight: 55,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRechargeRewardQueryUsesOneBoundedColdScanAcrossPageEdges(t *testing.T) {
	for _, pageSize := range []int{20, 100} {
		for _, count := range []int{pageSize - 1, pageSize, pageSize + 1} {
			store := &spyRechargeRewardStore{values: map[string][]byte{}}
			for i := 0; i < count; i++ {
				id := fmt.Sprintf("request-%03d", i)
				store.values[keys.KeyRechargeRewardOwner(rewardQueryOwner, id)] = rewardValue(t, id, "99", "1", "0")
			}
			store.values[keys.KeyRechargeRewardOwner("other-owner", "z")] = rewardValue(t, "z", "1", "", "")
			first, err := queryRechargeRewards(store, rewardQueryOwner, nil, "", pageSize)
			if err != nil || len(first.Items) != min(count, pageSize) || store.scanCalls != 1 || store.scanLimit != pageSize+1 || store.getManyCalls != 0 {
				t.Fatalf("page=%d count=%d first=%+v calls=%d limit=%d err=%v", pageSize, count, first, store.scanCalls, store.scanLimit, err)
			}
			if count == pageSize+1 {
				if first.NextAfter != fmt.Sprintf("request-%03d", pageSize-1) {
					t.Fatal(first.NextAfter)
				}
				last, err := queryRechargeRewards(store, rewardQueryOwner, nil, first.NextAfter, pageSize)
				if err != nil || len(last.Items) != 1 || last.Items[0].RequestID != fmt.Sprintf("request-%03d", pageSize) || last.NextAfter != "" || store.scanCalls != 2 {
					t.Fatalf("next=%+v err=%v", last, err)
				}
			} else if first.NextAfter != "" {
				t.Fatal(first.NextAfter)
			}
		}
	}
}

func TestRechargeRewardBatchUsesOneGetManyAndKeepsRoleAmounts(t *testing.T) {
	store := &spyRechargeRewardStore{values: map[string][]byte{
		keys.KeyRechargeRewardOwner(rewardQueryOwner, "a"): rewardValue(t, "a", "98", "1", "1"),
	}}
	resp, err := queryRechargeRewards(store, rewardQueryOwner, []string{"a", "missing"}, "", 20)
	if err != nil || store.scanCalls != 0 || store.getManyCalls != 1 || len(resp.Items) != 1 || len(resp.MissingRequestIDs) != 1 {
		t.Fatalf("response=%+v scan=%d batch=%d err=%v", resp, store.scanCalls, store.getManyCalls, err)
	}
	if row := resp.Items[0]; row.ReceiverNetAmount != "98" || row.RequesterRewardAmount != "1" || row.WitnessRewardAmount != "1" {
		t.Fatal(row)
	}
	zero := rewardValue(t, "tiny", "9", "0", "")
	if _, err := decodeRechargeReward(keys.KeyRechargeRewardOwner(rewardQueryOwner, "tiny"), rewardQueryOwner, zero); err != nil {
		t.Fatalf("explicit zero requester share with positive receiver credit is valid: %v", err)
	}
	zeroOnly := rewardValue(t, "zero", "", "0", "")
	if _, err := decodeRechargeReward(keys.KeyRechargeRewardOwner(rewardQueryOwner, "zero"), rewardQueryOwner, zeroOnly); err == nil {
		t.Fatal("all-zero role set was accepted")
	}
	empty := rewardValue(t, "empty", "", "", "")
	if _, err := decodeRechargeReward(keys.KeyRechargeRewardOwner(rewardQueryOwner, "empty"), rewardQueryOwner, empty); err == nil {
		t.Fatal("empty role set was accepted")
	}
	store.getError = errors.New("cold KV unavailable")
	if _, err := queryRechargeRewards(store, rewardQueryOwner, []string{"a"}, "", 20); err == nil {
		t.Fatal("cold KV failure was suppressed")
	}
}

func TestRechargeRewardRouteValidatesAndSeparatesOwners(t *testing.T) {
	hm, db := newFrostQueryTestHandler(t)
	defer db.Close()
	key := keys.KeyRechargeRewardOwner(rewardQueryOwner, "a")
	db.EnqueueSet(key, string(rewardValue(t, "a", "98", "1", "1")))
	if err := db.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	get := func(query string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		hm.HandleWitnessRewards(rec, httptest.NewRequest(http.MethodGet, "/witness/rewards?"+query, nil))
		return rec
	}
	ownerQuery := "owner=" + rewardQueryOwner
	rec := get(ownerQuery + "&request_id=a&request_id=a&request_id=missing")
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var result rechargeRewardResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || len(result.MissingRequestIDs) != 1 || result.MissingRequestIDs[0] != "missing" {
		t.Fatal(result)
	}
	for _, query := range []string{"owner=not-an-address", ownerQuery + "&request_id=a&after=a", ownerQuery + "&request_id=%ZZ", ownerQuery + "&after=%2F", ownerQuery + "&limit=101", ownerQuery + "&request_id=" + strings.Repeat("a", 4096)} {
		if got := get(query); got.Code != 400 {
			t.Fatalf("%s: %d %s", query, got.Code, got.Body.String())
		}
	}
	otherKey, err := utils.ParseSecp256k1PrivateKey(strings.Repeat("0", 63) + "2")
	if err != nil {
		t.Fatal(err)
	}
	other, err := utils.DeriveDexAddress(otherKey)
	if err != nil {
		t.Fatal(err)
	}
	if got := get("owner=" + other + "&request_id=a"); got.Code != 200 || strings.Contains(got.Body.String(), `"token_address":"BTC"`) {
		t.Fatalf("cross-owner leak: %d %s", got.Code, got.Body.String())
	}
	db.EnqueueSet(key, string(rewardValue(t, "wrong", "1", "", "")))
	if err := db.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	if got := get(ownerQuery + "&request_id=a"); got.Code != 500 {
		t.Fatalf("corrupt key-value pair was accepted: %d %s", got.Code, got.Body.String())
	}
}
