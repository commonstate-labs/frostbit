package main

import (
	"dex/config"
	"dex/pb"
	"time"
)

                                        
func generateTransferTx(from, to, token, amount, fee string, nonce uint64) *pb.AnyTx {
	anyTx := &pb.AnyTx{
		Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
			Base: &pb.BaseMessage{
				FromAddress: from,
				Fee:         fee,
				Status:      pb.Status_PENDING,
				Nonce:       nonce,
			},
			To:           to,
			TokenAddress: token,
			Amount:       amount,
		}},
	}
	return anyTx
}

                                          
func generateIssueTokenTx(from, name, symbol, supply string, canMint bool, nonce uint64) *pb.AnyTx {
	_ = name
	_ = symbol
	anyTx := &pb.AnyTx{
		Content: &pb.AnyTx_IssueTokenTx{IssueTokenTx: &pb.IssueTokenTx{
			Base: &pb.BaseMessage{
				FromAddress: from,
				Status:      pb.Status_PENDING,
				Nonce:       nonce,
				Fee:         "1000",
			},
			TotalSupply: supply,
			CanMint:     canMint,
		}},
	}
	return anyTx
}

                                           
func generateMinerTx(from string, op pb.OrderOp, amount string, nonce uint64) *pb.AnyTx {
	anyTx := &pb.AnyTx{
		Content: &pb.AnyTx_MinerTx{MinerTx: &pb.MinerTx{
			Base: &pb.BaseMessage{
				FromAddress: from,
				Status:      pb.Status_PENDING,
				Nonce:       nonce,
				Fee:         "1000",
			},
			Op:     op,
			Amount: amount,
		}},
	}
	return anyTx
}

                                                     
func generateWitnessStakeTx(from string, op pb.OrderOp, amount string, nonce uint64) *pb.AnyTx {
	anyTx := &pb.AnyTx{
		Content: &pb.AnyTx_WitnessStakeTx{WitnessStakeTx: &pb.WitnessStakeTx{
			Base: &pb.BaseMessage{
				FromAddress: from,
				Status:      pb.Status_PENDING,
				Nonce:       nonce,
				Fee:         "1000",
			},
			Op:     op,
			Amount: amount,
		}},
	}
	return anyTx
}

                                                    
func generateWitnessRequestTx(from, chain, nativeHash string, vout uint32, script []byte, nativeContract, amount, receiver, fee string, nonce uint64) *pb.AnyTx {
	anyTx := &pb.AnyTx{
		Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{
			Base: &pb.BaseMessage{
				FromAddress: from,
				Status:      pb.Status_PENDING,
				Nonce:       nonce,
				Fee:         "1000",
			},
			NativeChain:     chain,
			NativeTxHash:    nativeHash,
			NativeVout:      vout,
			NativeScript:    script,
			Amount:          amount,
			ReceiverAddress: receiver,
			RechargeFee:     fee,
			NativeContract:  nativeContract,
		}},
	}
	return anyTx
}

                                                 
func generateWitnessVoteTx(witness, requestID string, round uint32, voteType pb.WitnessVoteType, nonce uint64) *pb.AnyTx {
	anyTx := &pb.AnyTx{
		Content: &pb.AnyTx_WitnessVoteTx{WitnessVoteTx: &pb.WitnessVoteTx{
			Base: &pb.BaseMessage{
				FromAddress: witness,
				Status:      pb.Status_PENDING,
				Nonce:       nonce,
				Fee:         "1000",
			},
			Vote: &pb.WitnessVote{
				RequestId:      requestID,
				WitnessAddress: witness,
				VoteType:       voteType,
				Timestamp:      uint64(time.Now().Unix()),
				Round:          round,
			},
		}},
	}
	return anyTx
}

                                                                           
func generateWitnessChallengeTx(from, requestID, stakeAmount, reason string, nonce uint64) *pb.AnyTx {
	anyTx := &pb.AnyTx{
		Content: &pb.AnyTx_WitnessChallengeTx{WitnessChallengeTx: &pb.WitnessChallengeTx{
			Base: &pb.BaseMessage{
				FromAddress: from,
				Status:      pb.Status_PENDING,
				Nonce:       nonce,
				Fee:         "1000",
			},
			RequestId:   requestID,
			StakeAmount: stakeAmount,
			Reason:      reason,
		}},
	}
	return anyTx
}

                                                     
func generateWithdrawRequestTx(from, chain, asset, to, amount string, nonce uint64) *pb.AnyTx {
	anyTx := &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawRequestTx{FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{
			Base: &pb.BaseMessage{
				FromAddress: from,
				Status:      pb.Status_PENDING,
				Nonce:       nonce,
				Fee:         "1000",
			},
			Chain:  chain,
			Asset:  asset,
			To:     to,
			Amount: amount,
		}},
	}
	return anyTx
}

                                     
func generateOrderTx(from, baseToken, quoteToken, amount, price string, nonce uint64, side pb.OrderSide) *pb.AnyTx {
	anyTx := &pb.AnyTx{
		Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
			Base: &pb.BaseMessage{
				FromAddress: from,
				Fee:         "1000",
				Status:      pb.Status_PENDING,
				Nonce:       nonce,
			},
			BaseToken:  baseToken,
			QuoteToken: quoteToken,
			Op:         pb.OrderOp_ADD,
			Amount:     amount,
			Price:      price,
			Side:       side,
		}},
	}
	return anyTx
}

                                                    
func generateCancelOrderTx(from, orderID string, nonce uint64) *pb.AnyTx {
	return &pb.AnyTx{
		Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
			Base: &pb.BaseMessage{
				FromAddress: from,
				Fee:         "1000",
				Status:      pb.Status_PENDING,
				Nonce:       nonce,
			},
			Op:         pb.OrderOp_REMOVE,
			OpTargetId: orderID,
		}},
	}
}

func generateTransactions(nodes []*NodeInstance, frostCfg config.FrostConfig, witnessCfg config.WitnessConfig, genesisCfg *config.GenesisConfig) *TxSimulator {
	simulator := NewTxSimulator(nodes, frostCfg, witnessCfg, genesisCfg)
	simulator.Start()
	return simulator
}

                                                       
func generateFutureOrderTx(from, pair, quantity, price string, leverage uint64, side int32, isClose bool, nonce uint64) *pb.AnyTx {
	anyTx := &pb.AnyTx{
		Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
			Base: &pb.BaseMessage{
				FromAddress: from,
				Fee:         "1000",
				Status:      pb.Status_PENDING,
				Nonce:       nonce,
			},
			Pair:     pair,
			Side:     side,
			Quantity: quantity,
			Price:    price,
			Leverage: leverage,
			IsClose:  isClose,
		}},
	}
	return anyTx
}
