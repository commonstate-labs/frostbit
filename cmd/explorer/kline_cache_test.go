package main

import "testing"

func TestExplorerKLineCacheCountUsesBoundedTiers(t *testing.T) {
	tests := []struct {
		count int
		want  int
	}{
		{count: 1, want: 1},
		{count: 2, want: 2},
		{count: 3, want: 4},
		{count: 1000, want: 1024},
		{count: 4096, want: 4096},
		{count: 4097, want: 5000},
		{count: 5000, want: 5000},
		{count: 99999, want: 5000},
		{count: 0, want: 1024},
	}
	for _, tt := range tests {
		if got := explorerKLineCacheCount(tt.count); got != tt.want {
			t.Fatalf("cache count for %d = %d, want %d", tt.count, got, tt.want)
		}
	}
}

func TestExplorerKLineTradeBudgetScalesWithCount(t *testing.T) {
	tests := []struct {
		count int
		want  int
	}{
		{count: 1, want: explorerKLineMinTradeLimit},
		{count: 25, want: explorerKLineMinTradeLimit},
		{count: 26, want: 260},
		{count: 1024, want: 10240},
		{count: 5000, want: explorerKLineMaxTradeLimit},
		{count: 99999, want: explorerKLineMaxTradeLimit},
	}
	for _, tt := range tests {
		if got := explorerKLineTradeBudget(tt.count); got != tt.want {
			t.Fatalf("trade budget for %d = %d, want %d", tt.count, got, tt.want)
		}
	}
}
