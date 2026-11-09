package arbitrage

import (
	"dex/interfaces"
	"dex/pb"
	"fmt"
)

var _ interfaces.PrivateTxProvider = (*Machine)(nil)
var _ interfaces.PrivateTxCandidateProvider = (*Machine)(nil)

                                                                            
                                                                      
func (m *Machine) SenderAddress() string {
	if m == nil || !m.config.Enabled {
		return ""
	}
	return m.account
}

func (m *Machine) Limits() interfaces.PrivateProposalLimits {
	if m == nil || !m.config.Enabled {
		return interfaces.PrivateProposalLimits{}
	}
	                                                                            
	                                                                          
	                                                                          
	maxTransactions := m.config.MaxCycleEvaluations
	if maxTransactions > int(^uint(0)>>1)/2 {
		maxTransactions = int(^uint(0) >> 1)
	} else {
		maxTransactions *= 2
	}
	if maxTransactions < maxClosedRouteLegs {
		maxTransactions = maxClosedRouteLegs
	}
	return interfaces.PrivateProposalLimits{
		MaxTransactions:  maxTransactions,
		MaxOrdersPerBook: m.config.MaxOrdersPerBook,
	}
}

                                                                            
                                                                              
                                                 
func (m *Machine) HasPending(ctx interfaces.ProposalContext) bool {
	return m.HasPendingContext(fromProposalContext(ctx))
}

                                                                             
                                                                          
                               
func (m *Machine) Prepare(ctx interfaces.ProposalContext, orderedPublic []*pb.AnyTx, market *interfaces.ProposalSpotMarket) (*interfaces.PrivateTxBatch, error) {
	candidates, err := m.PrepareCandidates(ctx, orderedPublic, market)
	if err != nil || len(candidates) == 0 {
		return nil, err
	}
	return clonePrivateTxBatch(candidates[0]), nil
}

                                                                               
                                                                             
                                                                              
func (m *Machine) PrepareCandidates(ctx interfaces.ProposalContext, orderedPublic []*pb.AnyTx, market *interfaces.ProposalSpotMarket) ([]*interfaces.PrivateTxBatch, error) {
	batches, err := m.PrepareCandidatesContext(fromProposalContext(ctx), orderedPublic, fromProposalSpotMarket(market))
	if err != nil || len(batches) == 0 {
		return nil, err
	}
	converted := make([]*interfaces.PrivateTxBatch, 0, len(batches))
	for _, batch := range batches {
		if batch == nil {
			continue
		}
		converted = append(converted, &interfaces.PrivateTxBatch{
			ID:                batch.ID,
			Transactions:      cloneBatch(batch).Txs,
			InsertAfterPublic: append([]int(nil), batch.InsertAfterPublic...),
			PublicPrefixHash:  batch.PublicPrefixHash,
			PublicPrefixCount: batch.PublicPrefixCount,
		})
	}
	return converted, nil
}

func clonePrivateTxBatch(batch *interfaces.PrivateTxBatch) *interfaces.PrivateTxBatch {
	if batch == nil {
		return nil
	}
	internal := cloneBatch(&CandidateBatch{
		ID:                batch.ID,
		Txs:               batch.Transactions,
		InsertAfterPublic: batch.InsertAfterPublic,
		PublicPrefixHash:  batch.PublicPrefixHash,
		PublicPrefixCount: batch.PublicPrefixCount,
	})
	return &interfaces.PrivateTxBatch{
		ID:                internal.ID,
		Transactions:      internal.Txs,
		InsertAfterPublic: internal.InsertAfterPublic,
		PublicPrefixHash:  internal.PublicPrefixHash,
		PublicPrefixCount: internal.PublicPrefixCount,
	}
}

                                                                            
                                                                             
func (m *Machine) ValidateCandidate(ctx interfaces.ProposalContext, batch *interfaces.PrivateTxBatch, result *interfaces.ProposalSimulationResult) error {
	if batch == nil {
		return fmt.Errorf("nil arbitrage candidate")
	}
	if result == nil {
		return fmt.Errorf("nil arbitrage simulation result")
	}
	proposer := string(ctx.Proposer)
	internal := &CandidateBatch{
		ID:                batch.ID,
		Txs:               batch.Transactions,
		InsertAfterPublic: append([]int(nil), batch.InsertAfterPublic...),
		PublicPrefixHash:  batch.PublicPrefixHash,
		PublicPrefixCount: batch.PublicPrefixCount,
	}
	if internal.PublicPrefixHash != ctx.PublicPrefixHash || internal.PublicPrefixCount != ctx.PublicPrefixCount {
		return fmt.Errorf("arbitrage candidate public prefix binding mismatch")
	}
	if len(internal.InsertAfterPublic) != len(internal.Txs) {
		return fmt.Errorf("arbitrage candidate insertion count mismatch")
	}
	for i, position := range internal.InsertAfterPublic {
		if position < 0 || position > ctx.PublicPrefixCount || (i > 0 && position < internal.InsertAfterPublic[i-1]) {
			return fmt.Errorf("invalid arbitrage insertion position %d at leg %d", position, i)
		}
	}
	for _, tx := range internal.Txs {
		if tx == nil || tx.GetBase() == nil {
			return fmt.Errorf("nil arbitrage transaction")
		}
		base := tx.GetBase()
		if base.TargetHeight != ctx.Height || base.OnlyProposer != proposer {
			return fmt.Errorf(
				"arbitrage transaction %s context mismatch: target=%d/%s proposal=%d/%s",
				tx.GetTxId(), base.TargetHeight, base.OnlyProposer, ctx.Height, proposer,
			)
		}
	}

	execution := CandidateExecution{Valid: result.Valid, Reason: result.Reason}
	execution.Receipts = make([]ExecutionReceipt, 0, len(result.Receipts))
	for _, receipt := range result.Receipts {
		converted := ExecutionReceipt{
			TxID: receipt.TxID, Status: receipt.Status, Error: receipt.Error, Fee: receipt.Fee,
			FBBalanceAfter: receipt.FBBalanceAfter,
		}
		for _, event := range receipt.Events {
			converted.Events = append(converted.Events, ExecutionEvent{
				Kind: event.Kind, Payload: cloneStringMap(event.Payload),
			})
			if event.Kind != "ORDER_MATCHED" {
				continue
			}
			converted.Matches = append(converted.Matches, ExecutionMatch{
				MakerOrderID: event.Payload["maker_order_id"],
				Quantity:     event.Payload["quantity"],
				Price:        event.Payload["price"],
			})
		}
		execution.Receipts = append(execution.Receipts, converted)
	}
	return m.ValidateExecution(internal, execution)
}

func fromProposalContext(ctx interfaces.ProposalContext) Context {
	return Context{
		ParentID:          ctx.ParentID,
		Height:            ctx.Height,
		Proposer:          string(ctx.Proposer),
		Window:            ctx.Window,
		MaxTxs:            ctx.MaxTxs,
		PrivateTxLimit:    ctx.PrivateTxLimit,
		PublicPrefixCount: ctx.PublicPrefixCount,
		PublicPrefixHash:  ctx.PublicPrefixHash,
	}
}

func fromProposalSpotMarket(market *interfaces.ProposalSpotMarket) *MarketSnapshot {
	if market == nil {
		return nil
	}
	converted := &MarketSnapshot{
		PublicPrefixHash:  market.PublicPrefixHash,
		PublicPrefixCount: market.PublicPrefixCount,
		Levels:            make([]MarketLevel, 0, len(market.Levels)),
		Executions:        make([]MarketExecution, 0, len(market.Executions)),
		FutureLevels:      make([]FutureMarketLevel, 0, len(market.FutureLevels)),
		FutureExecutions:  make([]FutureMarketExecution, 0, len(market.FutureExecutions)),
	}
	for _, level := range market.Levels {
		out := MarketLevel{
			Leg:    Leg{BaseToken: level.BaseToken, QuoteToken: level.QuoteToken, Side: level.TakerSide},
			Price:  level.Price,
			Makers: make([]MarketMaker, 0, len(level.Makers)),
		}
		for _, maker := range level.Makers {
			out.Makers = append(out.Makers, MarketMaker{
				OrderID: maker.OrderID, Owner: maker.Owner, Remaining: maker.Remaining,
			})
		}
		converted.Levels = append(converted.Levels, out)
	}
	for _, execution := range market.Executions {
		out := MarketExecution{
			PublicPosition: execution.PublicPosition,
			OrderID:        execution.OrderID,
			Owner:          execution.Owner,
			Leg: Leg{
				BaseToken: execution.BaseToken, QuoteToken: execution.QuoteToken, Side: execution.Side,
			},
			Price:     execution.Price,
			Amount:    execution.Amount,
			Remaining: execution.Remaining,
			Matches:   make([]MarketMatch, 0, len(execution.Matches)),
			Levels:    make([]MarketLevel, 0, len(execution.Levels)),
		}
		for _, match := range execution.Matches {
			out.Matches = append(out.Matches, MarketMatch{
				MakerOrderID: match.MakerOrderID, Quantity: match.Quantity, Price: match.Price,
			})
		}
		for _, level := range execution.Levels {
			convertedLevel := MarketLevel{
				Leg:    Leg{BaseToken: level.BaseToken, QuoteToken: level.QuoteToken, Side: level.TakerSide},
				Price:  level.Price,
				Makers: make([]MarketMaker, 0, len(level.Makers)),
			}
			for _, maker := range level.Makers {
				convertedLevel.Makers = append(convertedLevel.Makers, MarketMaker{
					OrderID: maker.OrderID, Owner: maker.Owner, Remaining: maker.Remaining,
				})
			}
			out.Levels = append(out.Levels, convertedLevel)
		}
		converted.Executions = append(converted.Executions, out)
	}
	for _, level := range market.FutureLevels {
		out := FutureMarketLevel{
			Pair: level.Pair, QuoteToken: level.QuoteToken, Side: level.TakerSide, Price: level.Price,
			Makers: make([]MarketMaker, 0, len(level.Makers)),
		}
		for _, maker := range level.Makers {
			out.Makers = append(out.Makers, MarketMaker{
				OrderID: maker.OrderID, Owner: maker.Owner, Remaining: maker.Remaining,
			})
		}
		converted.FutureLevels = append(converted.FutureLevels, out)
	}
	for _, execution := range market.FutureExecutions {
		out := FutureMarketExecution{
			PublicPosition: execution.PublicPosition,
			OrderID:        execution.OrderID,
			Owner:          execution.Owner,
			Pair:           execution.Pair,
			QuoteToken:     execution.QuoteToken,
			Side:           execution.Side,
			Price:          execution.Price,
			Quantity:       execution.Quantity,
			Remaining:      execution.Remaining,
			Leverage:       execution.Leverage,
			Matches:        make([]MarketMatch, 0, len(execution.Matches)),
			Levels:         make([]FutureMarketLevel, 0, len(execution.Levels)),
			TakerAvailable: cloneStringMap(execution.TakerAvailable),
		}
		for _, match := range execution.Matches {
			out.Matches = append(out.Matches, MarketMatch{
				MakerOrderID: match.MakerOrderID, Quantity: match.Quantity, Price: match.Price,
			})
		}
		for _, level := range execution.Levels {
			convertedLevel := FutureMarketLevel{
				Pair: level.Pair, QuoteToken: level.QuoteToken, Side: level.TakerSide, Price: level.Price,
				Makers: make([]MarketMaker, 0, len(level.Makers)),
			}
			for _, maker := range level.Makers {
				convertedLevel.Makers = append(convertedLevel.Makers, MarketMaker{
					OrderID: maker.OrderID, Owner: maker.Owner, Remaining: maker.Remaining,
				})
			}
			out.Levels = append(out.Levels, convertedLevel)
		}
		converted.FutureExecutions = append(converted.FutureExecutions, out)
	}
	if len(market.FutureAvailable) > 0 {
		converted.FutureAvailable = make(map[string]string, len(market.FutureAvailable))
		for token, amount := range market.FutureAvailable {
			converted.FutureAvailable[token] = amount
		}
	}
	return converted
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
