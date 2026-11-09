package handlers

import (
	"dex/keys"
	"dex/pb"
	"io"
	"net/http"
	"strings"

	"google.golang.org/protobuf/proto"
)

                                                             
                                                                     
func (hm *HandlerManager) HandleGetAccountBalances(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleGetAccountBalances")

	                  
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		resp := &pb.GetAccountBalancesResponse{Error: "failed to read request body"}
		data, _ := proto.Marshal(resp)
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.Write(data)
		return
	}

	var req pb.GetAccountBalancesRequest
	if err := pb.UnmarshalSanitize(bodyBytes, &req); err != nil {
		resp := &pb.GetAccountBalancesResponse{Error: "invalid request proto"}
		data, _ := proto.Marshal(resp)
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.Write(data)
		return
	}

	if req.Address == "" {
		resp := &pb.GetAccountBalancesResponse{Error: "address is required"}
		data, _ := proto.Marshal(resp)
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.Write(data)
		return
	}

	                                 
	balances := hm.scanAccountBalances(req.Address)

	resp := &pb.GetAccountBalancesResponse{Balances: balances}
	data, _ := proto.Marshal(resp)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(data)
}

                                                                    
                                                                                         
func (hm *HandlerManager) scanAccountBalances(address string) map[string]*pb.TokenBalance {
	balances := make(map[string]*pb.TokenBalance)
	prefix := keys.KeyBalancePrefix(address)

	                                                                       
	kvResults, err := hm.dbManager.ScanByPrefix(prefix, 100)
	if err != nil {
		return balances
	}
	for key, val := range kvResults {
		token := extractTokenFromBalanceKey(key, address)
		if token == "" {
			continue
		}
		var record pb.TokenBalanceRecord
		if err := proto.Unmarshal([]byte(val), &record); err == nil && record.Balance != nil {
			balances[token] = record.Balance
		}
	}
	return balances
}

                                                                  
                                           
func extractTokenFromBalanceKey(key, address string) string {
	                                   
	prefix := keys.KeyBalancePrefix(address)
	if !strings.HasPrefix(key, prefix) {
		return ""
	}
	return key[len(prefix):]
}
