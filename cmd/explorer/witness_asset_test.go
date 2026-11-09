package main

import (
	"testing"

	"dex/pb"
)

func TestWitnessRequestAssetRepairsMissingCanonicalAssetID(t *testing.T) {
	const baseUSDC = "br_718d932d6adfb5e61bfbbbb7b266e13884b6604908067bbe8d5bad1a7159159c"

	tests := []struct {
		name string
		req  *pb.RechargeRequest
		want string
	}{
		{name: "explicit asset wins", req: &pb.RechargeRequest{TokenAddress: "custom_asset", NativeChain: "base", NativeContract: "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"}, want: "custom_asset"},
		{name: "base usdc legacy request", req: &pb.RechargeRequest{NativeChain: "base", NativeContract: "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"}, want: baseUSDC},
		{name: "unknown identity", req: &pb.RechargeRequest{NativeChain: "unknown", NativeContract: "0x1"}, want: ""},
		{name: "nil request", req: nil, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := witnessRequestAsset(tt.req); got != tt.want {
				t.Fatalf("witnessRequestAsset()=%q, want %q", got, tt.want)
			}
		})
	}
}
