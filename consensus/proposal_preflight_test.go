package consensus

import (
	"dex/pb"
	"dex/vm"
	"testing"
)

func TestProposalSimulationResultFromVMCopiesReceiptsAndEvents(t *testing.T) {
	payload := map[string]string{
		"amount": "25",
		"price":  "101",
	}
	result := &vm.SpecResult{
		Valid:  false,
		Reason: "candidate rejected",
		Receipts: []*vm.Receipt{
			nil,
			{
				TxID:           "private-order",
				Status:         "FAILED",
				Error:          "not fully filled",
				BlockHeight:    42,
				Fee:            "10",
				FBBalanceAfter: `{"balance":"990"}`,
				Events: []vm.Event{{
					Kind:    vm.EventOrderMatched,
					Payload: payload,
				}},
			},
		},
	}

	converted := proposalSimulationResultFromVM(result)
	if converted.Valid || converted.Reason != result.Reason {
		t.Fatalf("converted validity = %+v", converted)
	}
	if len(converted.Receipts) != 1 {
		t.Fatalf("converted receipt count = %d, want 1", len(converted.Receipts))
	}
	receipt := converted.Receipts[0]
	if receipt.TxID != "private-order" || receipt.Status != "FAILED" || receipt.Error != "not fully filled" ||
		receipt.BlockHeight != 42 || receipt.Fee != "10" || receipt.FBBalanceAfter != `{"balance":"990"}` {
		t.Fatalf("converted receipt = %+v", receipt)
	}
	if len(receipt.Events) != 1 || receipt.Events[0].Kind != string(vm.EventOrderMatched) ||
		receipt.Events[0].Payload["amount"] != "25" || receipt.Events[0].Payload["price"] != "101" {
		t.Fatalf("converted events = %+v", receipt.Events)
	}

	payload["amount"] = "mutated"
	if receipt.Events[0].Payload["amount"] != "25" {
		t.Fatal("converted event payload aliases VM receipt payload")
	}
}

func TestVMProposalPreflightRequiresExecutor(t *testing.T) {
	if _, err := NewVMProposalPreflight(nil).Simulate(nil); err == nil {
		t.Fatal("Simulate() with nil executor succeeded")
	}
}

func TestProposalSpotMarketFromVMCopiesExecutionTrace(t *testing.T) {
	projection := &vm.ProposalSpotProjection{
		Executions: []vm.ProposalSpotExecutionProjection{{
			PublicPosition: 2,
			OrderID:        "public-order",
			Owner:          "alice",
			BaseToken:      "A",
			QuoteToken:     "Q",
			Side:           pb.OrderSide_SELL,
			Price:          "100",
			Amount:         "10",
			Remaining:      "4",
			Matches: []vm.ProposalSpotMatchProjection{{
				MakerOrderID: "older", Quantity: "6", Price: "90",
			}},
			Levels: []vm.ProposalSpotLevelProjection{{
				BaseToken: "A", QuoteToken: "Q", TakerSide: pb.OrderSide_BUY, Price: "100",
				Makers: []vm.ProposalSpotMakerProjection{{OrderID: "public-order", Owner: "alice", Remaining: "4"}},
			}},
		}},
	}
	market := proposalSpotMarketFromVM(projection, "prefix", 3)
	if market.PublicPrefixHash != "prefix" || market.PublicPrefixCount != 3 || len(market.Executions) != 1 {
		t.Fatalf("market=%+v", market)
	}
	execution := market.Executions[0]
	if execution.PublicPosition != 2 || execution.OrderID != "public-order" || execution.Remaining != "4" ||
		len(execution.Matches) != 1 || execution.Matches[0].MakerOrderID != "older" ||
		len(execution.Levels) != 1 || execution.Levels[0].Makers[0].Remaining != "4" {
		t.Fatalf("execution=%+v", execution)
	}
	projection.Executions[0].Matches[0].Quantity = "mutated"
	projection.Executions[0].Levels[0].Makers[0].Remaining = "mutated"
	if execution.Matches[0].Quantity != "6" || execution.Levels[0].Makers[0].Remaining != "4" {
		t.Fatal("converted execution aliases VM projection")
	}
}
