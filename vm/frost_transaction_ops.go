package vm

                                                                             
                                                                             
                                                                               
                                                                             
                                                                            
                                                                              
func applyFrostWithdrawTransactionOps(sv StateView, ops []WriteOp) {
	for i := range ops {
		op := &ops[i]
		if op.Key == "" {
			continue
		}
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, op.Value)
		}
	}
}
