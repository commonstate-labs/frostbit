package btc

import (
	"bytes"
	"testing"

	"dex/frost/chain"
)

                                             
const (
	txid1                    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	txid2                    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	txid3                    = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	testTaprootOutputAddress = "tb1pdpd76eytdcdwnm62uuxve403srq0dtcls0uj488wjjy4z4lfm25sdwatf0"
)

func testTaprootPrevOutScript(fill byte) []byte {
	return append([]byte{0x51, 0x20}, bytes.Repeat([]byte{fill}, 32)...)
}

                                                                                 
func TestTemplateHash_DifferentInputOrder(t *testing.T) {
	                                                                
	                          
	params1 := chain.WithdrawTemplateParams{
		Chain:       "btc",
		Asset:       "native",
		VaultID:     1,
		KeyEpoch:    100,
		WithdrawIDs: []string{"w1", "w2"},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w1", To: "bc1qaddr1", Amount: "10000"},
			{WithdrawID: "w2", To: "bc1qaddr2", Amount: "20000"},
		},
		Inputs: []chain.UTXO{
			{TxID: txid1, Vout: 0, Amount: 50000, ConfirmHeight: 100},
			{TxID: txid2, Vout: 1, Amount: 30000, ConfirmHeight: 101},
		},
		ChangeAddress: "bc1qchange",
		Fee:           1000,
		ChangeAmount:  49000,
	}

	                                                   
	params2 := chain.WithdrawTemplateParams{
		Chain:       "btc",
		Asset:       "native",
		VaultID:     1,
		KeyEpoch:    100,
		WithdrawIDs: []string{"w1", "w2"},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w1", To: "bc1qaddr1", Amount: "10000"},
			{WithdrawID: "w2", To: "bc1qaddr2", Amount: "20000"},
		},
		Inputs: []chain.UTXO{
			{TxID: txid2, Vout: 1, Amount: 30000, ConfirmHeight: 101},                
			{TxID: txid1, Vout: 0, Amount: 50000, ConfirmHeight: 100},
		},
		ChangeAddress: "bc1qchange",
		Fee:           1000,
		ChangeAmount:  49000,
	}

	               
	template1, err := newTestBTCTemplate(params1)
	if err != nil {
		t.Fatalf("NewBTCTemplate params1 failed: %v", err)
	}

	template2, err := newTestBTCTemplate(params2)
	if err != nil {
		t.Fatalf("NewBTCTemplate params2 failed: %v", err)
	}

	              
	hash1 := template1.TemplateHash()
	hash2 := template2.TemplateHash()

	                     
	if !bytes.Equal(hash1, hash2) {
		t.Errorf("TemplateHash mismatch for different input order:\nhash1: %x\nhash2: %x", hash1, hash2)
	}

	                        
	if template1.Inputs[0].TxID != txid1 {
		t.Errorf("Expected first input TxID=%s, got %s", txid1, template1.Inputs[0].TxID)
	}
}

                                                        
func TestTemplateHash_MultipleInputs(t *testing.T) {
	                        
	                                                                  
	params := chain.WithdrawTemplateParams{
		Chain:       "btc",
		Asset:       "native",
		VaultID:     2,
		KeyEpoch:    200,
		WithdrawIDs: []string{"w1"},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w1", To: "bc1qbigwithdraw", Amount: "100000"},
		},
		Inputs: []chain.UTXO{
			{TxID: txid3, Vout: 0, Amount: 50000, ConfirmHeight: 100},
			{TxID: txid1, Vout: 2, Amount: 40000, ConfirmHeight: 98},
			{TxID: txid2, Vout: 1, Amount: 60000, ConfirmHeight: 99},
		},
		ChangeAddress: "bc1qchange",
		Fee:           2000,
		ChangeAmount:  48000,
	}

	template, err := newTestBTCTemplate(params)
	if err != nil {
		t.Fatalf("NewBTCTemplate failed: %v", err)
	}

	                                             
	expectedOrder := []string{txid1, txid2, txid3}
	for i, expected := range expectedOrder {
		if template.Inputs[i].TxID != expected {
			t.Errorf("Input[%d]: expected TxID=%s, got %s", i, expected, template.Inputs[i].TxID)
		}
	}

	                              
	hash1 := template.TemplateHash()
	hash2 := template.TemplateHash()
	if !bytes.Equal(hash1, hash2) {
		t.Error("TemplateHash should be deterministic")
	}
}

                                                                                 
func TestTemplateHash_SameTxidDifferentVout(t *testing.T) {
	                                                                
	params1 := chain.WithdrawTemplateParams{
		Chain:       "btc",
		VaultID:     1,
		KeyEpoch:    1,
		WithdrawIDs: []string{"w1"},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w1", To: "bc1qaddr", Amount: "50000"},
		},
		Inputs: []chain.UTXO{
			{TxID: txid1, Vout: 2, Amount: 30000},
			{TxID: txid1, Vout: 0, Amount: 30000},
			{TxID: txid1, Vout: 1, Amount: 30000},
		},
		ChangeAddress: "bc1qchange",
		Fee:           1000,
		ChangeAmount:  39000,
	}

	params2 := chain.WithdrawTemplateParams{
		Chain:       "btc",
		VaultID:     1,
		KeyEpoch:    1,
		WithdrawIDs: []string{"w1"},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w1", To: "bc1qaddr", Amount: "50000"},
		},
		Inputs: []chain.UTXO{
			{TxID: txid1, Vout: 1, Amount: 30000},                
			{TxID: txid1, Vout: 2, Amount: 30000},
			{TxID: txid1, Vout: 0, Amount: 30000},
		},
		ChangeAddress: "bc1qchange",
		Fee:           1000,
		ChangeAmount:  39000,
	}

	template1, _ := newTestBTCTemplate(params1)
	template2, _ := newTestBTCTemplate(params2)

	                     
	if template1.Inputs[0].Vout != 0 || template1.Inputs[1].Vout != 1 || template1.Inputs[2].Vout != 2 {
		t.Errorf("Inputs not sorted by vout: %+v", template1.Inputs)
	}

	                     
	if !bytes.Equal(template1.TemplateHash(), template2.TemplateHash()) {
		t.Error("TemplateHash should be same for different input order with same txid")
	}
}

                                                                                   
func TestTemplateHash_JSONRoundTrip(t *testing.T) {
	                                                                 
	params := chain.WithdrawTemplateParams{
		Chain:       "btc",
		VaultID:     5,
		KeyEpoch:    999,
		WithdrawIDs: []string{"w1", "w2", "w3"},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w1", To: "bc1qaddr1", Amount: "10000"},
			{WithdrawID: "w2", To: "bc1qaddr2", Amount: "20000"},
			{WithdrawID: "w3", To: "bc1qaddr3", Amount: "30000"},
		},
		Inputs: []chain.UTXO{
			{TxID: txid2, Vout: 0, Amount: 100000},
		},
		ChangeAddress: "bc1qchange",
		Fee:           2000,
		ChangeAmount:  38000,
	}

	template1, err := newTestBTCTemplate(params)
	if err != nil {
		t.Fatalf("NewBTCTemplate failed: %v", err)
	}

	hash1 := template1.TemplateHash()

	                    
	jsonData, err := template1.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}

	                        
	template2, err := FromJSON(jsonData)
	if err != nil {
		t.Fatalf("FromJSON failed: %v", err)
	}

	hash2 := template2.TemplateHash()

	                     
	if !bytes.Equal(hash1, hash2) {
		t.Errorf("TemplateHash mismatch after JSON roundtrip:\nhash1: %x\nhash2: %x", hash1, hash2)
	}
}

                                                                           
func TestTemplateHash_DifferentVaultID(t *testing.T) {
	                                                                
	baseParams := chain.WithdrawTemplateParams{
		Chain:       "btc",
		KeyEpoch:    1,
		WithdrawIDs: []string{"w1"},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w1", To: "bc1qaddr", Amount: "10000"},
		},
		Inputs: []chain.UTXO{
			{TxID: txid1, Vout: 0, Amount: 50000},
		},
		ChangeAddress: "bc1qchange",
		Fee:           1000,
		ChangeAmount:  39000,
	}

	              
	params1 := baseParams
	params1.VaultID = 1
	template1, _ := newTestBTCTemplate(params1)

	              
	params2 := baseParams
	params2.VaultID = 2
	template2, _ := newTestBTCTemplate(params2)

	                     
	if bytes.Equal(template1.TemplateHash(), template2.TemplateHash()) {
		t.Error("TemplateHash should differ for different VaultID")
	}
}

                                                                             
func TestTemplateHash_DifferentKeyEpoch(t *testing.T) {
	                                                                
	baseParams := chain.WithdrawTemplateParams{
		Chain:       "btc",
		VaultID:     1,
		WithdrawIDs: []string{"w1"},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w1", To: "bc1qaddr", Amount: "10000"},
		},
		Inputs: []chain.UTXO{
			{TxID: txid1, Vout: 0, Amount: 50000},
		},
		ChangeAddress: "bc1qchange",
		Fee:           1000,
		ChangeAmount:  39000,
	}

	               
	params1 := baseParams
	params1.KeyEpoch = 1
	template1, _ := newTestBTCTemplate(params1)

	               
	params2 := baseParams
	params2.KeyEpoch = 2
	template2, _ := newTestBTCTemplate(params2)

	                     
	if bytes.Equal(template1.TemplateHash(), template2.TemplateHash()) {
		t.Error("TemplateHash should differ for different KeyEpoch")
	}
}

                                                                           
func TestNewBTCTemplate_InputOutputMismatch(t *testing.T) {
	                                                                            
	params := chain.WithdrawTemplateParams{
		Chain:       "btc",
		VaultID:     1,
		KeyEpoch:    1,
		WithdrawIDs: []string{"w1"},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w1", To: "bc1qaddr", Amount: "100000"},                 
		},
		Inputs: []chain.UTXO{
			{TxID: txid1, Vout: 0, Amount: 10000},                
		},
		Fee:          1000,
		ChangeAmount: 0,
	}

	_, err := newTestBTCTemplate(params)
	if err == nil {
		t.Error("Expected input/output mismatch error")
	}
}

                                                    
func TestNewBTCTemplate_NoInputs(t *testing.T) {
	params := chain.WithdrawTemplateParams{
		Chain:       "btc",
		VaultID:     1,
		KeyEpoch:    1,
		WithdrawIDs: []string{"w1"},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w1", To: "bc1qaddr", Amount: "10000"},
		},
		Inputs:       []chain.UTXO{},             
		Fee:          1000,
		ChangeAmount: 0,
	}

	_, err := newTestBTCTemplate(params)
	if err == nil {
		t.Error("Expected no inputs error")
	}
}

                                                                  
func TestNewBTCTemplate_OutputOrderMismatch(t *testing.T) {
	                                                                
	params := chain.WithdrawTemplateParams{
		Chain:       "btc",
		VaultID:     1,
		KeyEpoch:    1,
		WithdrawIDs: []string{"w1", "w2"},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w2", To: "bc1qaddr2", Amount: "10000"},                
			{WithdrawID: "w1", To: "bc1qaddr1", Amount: "10000"},
		},
		Inputs: []chain.UTXO{
			{TxID: txid1, Vout: 0, Amount: 50000},
		},
		ChangeAddress: "bc1qchange",
		Fee:           1000,
		ChangeAmount:  29000,
	}

	_, err := newTestBTCTemplate(params)
	if err == nil {
		t.Error("Expected withdraw output order mismatch error")
	}
}

                                                                   

                                                                  
func TestTaprootSighash_Deterministic(t *testing.T) {
	               
	params := chain.WithdrawTemplateParams{
		Chain:       "btc",
		VaultID:     1,
		KeyEpoch:    100,
		WithdrawIDs: []string{"w1"},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w1", To: testTaprootOutputAddress, Amount: "10000"},
		},
		Inputs: []chain.UTXO{
			{TxID: txid1, Vout: 0, Amount: 50000},
		},
		ChangeAddress: testTaprootOutputAddress,
		Fee:           1000,
		ChangeAmount:  39000,
	}

	template, err := newTestBTCTemplate(params)
	if err != nil {
		t.Fatalf("NewBTCTemplate failed: %v", err)
	}

	scriptPubKeys := testTemplatePrevScripts(template)

	                       
	sighashes1, err := template.ComputeTaprootSighash(scriptPubKeys, SighashDefault)
	if err != nil {
		t.Fatalf("ComputeTaprootSighash failed: %v", err)
	}

	sighashes2, err := template.ComputeTaprootSighash(scriptPubKeys, SighashDefault)
	if err != nil {
		t.Fatalf("ComputeTaprootSighash failed: %v", err)
	}

	                  
	if !bytes.Equal(sighashes1[0], sighashes2[0]) {
		t.Errorf("Sighash not deterministic:\n  first:  %x\n  second: %x", sighashes1[0], sighashes2[0])
	}

	                            
	if len(sighashes1[0]) != 32 {
		t.Errorf("Expected sighash length 32, got %d", len(sighashes1[0]))
	}

	t.Logf("Sighash: %x", sighashes1[0])
}

                                                                            
func TestTaprootSighash_DifferentInputs(t *testing.T) {
	          
	params1 := chain.WithdrawTemplateParams{
		Chain:       "btc",
		VaultID:     1,
		KeyEpoch:    100,
		WithdrawIDs: []string{"w1"},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w1", To: testTaprootOutputAddress, Amount: "10000"},
		},
		Inputs: []chain.UTXO{
			{TxID: txid1, Vout: 0, Amount: 50000},
		},
		ChangeAddress: testTaprootOutputAddress,
		Fee:           1000,
		ChangeAmount:  39000,
	}

	                                    
	params2 := chain.WithdrawTemplateParams{
		Chain:       "btc",
		VaultID:     1,
		KeyEpoch:    100,
		WithdrawIDs: []string{"w1"},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w1", To: testTaprootOutputAddress, Amount: "10000"},
		},
		Inputs: []chain.UTXO{
			{TxID: txid2, Vout: 0, Amount: 50000},                  
		},
		ChangeAddress: testTaprootOutputAddress,
		Fee:           1000,
		ChangeAmount:  39000,
	}

	template1, _ := newTestBTCTemplate(params1)
	template2, _ := newTestBTCTemplate(params2)

	scriptPubKeys := testTemplatePrevScripts(template1)

	sighashes1, err := template1.ComputeTaprootSighash(scriptPubKeys, SighashDefault)
	if err != nil {
		t.Fatalf("template1 sighash: %v", err)
	}
	sighashes2, err := template2.ComputeTaprootSighash(testTemplatePrevScripts(template2), SighashDefault)
	if err != nil {
		t.Fatalf("template2 sighash: %v", err)
	}

	if bytes.Equal(sighashes1[0], sighashes2[0]) {
		t.Error("Different inputs should produce different sighash")
	}
}

                                                          
func TestTaprootSighash_MultipleInputs(t *testing.T) {
	params := chain.WithdrawTemplateParams{
		Chain:       "btc",
		VaultID:     1,
		KeyEpoch:    100,
		WithdrawIDs: []string{"w1"},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w1", To: testTaprootOutputAddress, Amount: "50000"},
		},
		Inputs: []chain.UTXO{
			{TxID: txid1, Vout: 0, Amount: 30000},
			{TxID: txid2, Vout: 1, Amount: 30000},
		},
		ChangeAddress: testTaprootOutputAddress,
		Fee:           1000,
		ChangeAmount:  9000,
	}

	template, err := newTestBTCTemplate(params)
	if err != nil {
		t.Fatalf("NewBTCTemplate failed: %v", err)
	}

	scriptPubKeys := testTemplatePrevScripts(template)

	sighashes, err := template.ComputeTaprootSighash(scriptPubKeys, SighashDefault)
	if err != nil {
		t.Fatalf("ComputeTaprootSighash failed: %v", err)
	}

	                                
	if len(sighashes) != 2 {
		t.Errorf("Expected 2 sighashes, got %d", len(sighashes))
	}

	                                                             
	if bytes.Equal(sighashes[0], sighashes[1]) {
		t.Error("Different input indices should produce different sighash")
	}

	t.Logf("Sighash[0]: %x", sighashes[0])
	t.Logf("Sighash[1]: %x", sighashes[1])
}

                                                                              
func TestTaprootSighash_ScriptPubKeyMismatch(t *testing.T) {
	params := chain.WithdrawTemplateParams{
		Chain:       "btc",
		VaultID:     1,
		KeyEpoch:    100,
		WithdrawIDs: []string{"w1"},
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w1", To: testTaprootOutputAddress, Amount: "10000"},
		},
		Inputs: []chain.UTXO{
			{TxID: txid1, Vout: 0, Amount: 50000},
			{TxID: txid2, Vout: 1, Amount: 30000},
		},
		ChangeAddress: testTaprootOutputAddress,
		Fee:           1000,
		ChangeAmount:  69000,
	}

	template, _ := newTestBTCTemplate(params)

	                                                    
	scriptPubKeys := [][]byte{testTaprootPrevOutScript(0x01)}

	_, err := template.ComputeTaprootSighash(scriptPubKeys, SighashDefault)
	if err == nil {
		t.Error("Expected error for scriptPubKey count mismatch")
	}
}
