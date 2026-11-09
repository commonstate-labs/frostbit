package main_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"testing"

	"dex/frost/core/curve"
	"dex/frost/core/dkg"
	"dex/frost/core/frost"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

func Test_CompareWithBtcecSchnorr(t *testing.T) {
	grp := curve.NewSecp256k1Group()
	               
	privScalar := dkg.RandomScalar(grp.Order())
	privKey, _ := btcec.PrivKeyFromBytes(privScalar.Bytes())

	               
	msg := []byte("consistency check")
	                                         
	digest := sha256.Sum256(msg)

	                                      
	Rx, Ry, z, _ := frost.SchnorrSign(grp, privScalar, digest[:])

	                                   
	sig, err := schnorr.Sign(privKey, digest[:])
	if err != nil {
		t.Fatalf("外部 schnorr.Sign 失败: %v", err)
	}
	sigBytes := sig.Serialize()             
	if len(sigBytes) != 64 {
		t.Fatalf("外部签名长度不对: got %d, want 64", len(sigBytes))
	}

	                      
	RxExt := new(big.Int).SetBytes(sigBytes[:32])
	sExt := new(big.Int).SetBytes(sigBytes[32:])

	               
	t.Logf(`
=== 内部 SchnorrSign ===
  R = (%s, %s)
  z = %s
=== 外部 schnorr.Sign ===
  R.x = %s
  s   = %s
`, Rx.Text(16), Ry.Text(16), z.Text(16), RxExt.Text(16), sExt.Text(16))

	               
	if Rx.Cmp(RxExt) != 0 {
		t.Errorf("R.x 不一致：内部 %s，外部 %s", Rx.Text(16), RxExt.Text(16))
	}
	if z.Cmp(sExt) != 0 {
		t.Errorf("s/z 不一致：内部 %s，外部 %s", z.Text(16), sExt.Text(16))
	}
}

func Test_3_4_tweak(t *testing.T) {
	grp := curve.NewSecp256k1Group()
	N := grp.Order()
	thr, nPart := 3, 5                

	                                            
	parts := make([]*dkg.Polynomial, nPart)
	for i := range parts {
		parts[i] = dkg.NewPolynomial(thr, grp)
	}

	ids := make([]*big.Int, nPart)        
	sj := make([]*big.Int, nPart)
	for j := 0; j < nPart; j++ {
		ids[j] = big.NewInt(int64(j + 1))
		sum := big.NewInt(0)
		for _, p := range parts {
			sum.Add(sum, p.Evaluate(ids[j], grp))
		}
		sj[j] = sum.Mod(sum, N)
	}

	                                                                
	Q0 := big.NewInt(0)
	for _, p := range parts {
		Q0.Add(Q0, p.Coefficients[0])
	}
	Q0.Mod(Q0, N)
	Qx, Qy := grp.ScalarBaseMult(Q0).XY()
	if Qy.Bit(0) == 1 {
		for j := range sj {
			sj[j].Sub(N, sj[j])
		}
		Q0.Sub(N, Q0)
		Qy.Sub(grp.Modulus(), Qy)
	}

	                                    
	tweak := curve.TapTweakScalar(Qx, "TapTweak")
	Q0t := new(big.Int).Add(Q0, tweak)
	Q0t.Mod(Q0t, N)
	for j := range sj {
		sj[j].Add(sj[j], tweak)
		sj[j].Mod(sj[j], N)
	}

	Qtx, Qty := grp.ScalarBaseMult(Q0t).XY()
	if Qty.Bit(0) == 1 {
		Q0t.Sub(N, Q0t)
		Qty.Sub(grp.Modulus(), Qty)
		for j := range sj {
			sj[j].Sub(N, sj[j])
		}
	}
	                                                           
	{
		                                             
		fmt.Printf("\n[tweak] Q0' (私钥): %s\n", Q0t.Text(16))

		                                  
		fmt.Printf("[tweak] Q'   (公钥): (%s, %s)\n", Qtx.Text(16), Qty.Text(16))

		                                                 
		privTweaked, _ := btcec.PrivKeyFromBytes(Q0t.FillBytes(make([]byte, 32)))
		wifT, _ := btcutil.NewWIF(privTweaked, &chaincfg.TestNet3Params, true)
		fmt.Println("[tweak] 私钥 (WIF):", wifT.String())

		                                             
		addrPriv, _ := btcutil.NewAddressTaproot(
			schnorr.SerializePubKey(privTweaked.PubKey()),
			&chaincfg.TestNet3Params,
		)
		fmt.Println("[tweak] 私钥→地址：", addrPriv.EncodeAddress())

		                                             
		pubTweaked := btcec.NewPublicKey(
			curve.BigIntToFieldVal(Qtx),
			curve.BigIntToFieldVal(Qty),
		)
		addrPub, _ := btcutil.NewAddressTaproot(
			schnorr.SerializePubKey(pubTweaked),
			&chaincfg.TestNet3Params,
		)
		fmt.Println("[tweak] 公钥→地址：", addrPub.EncodeAddress())

		                                                         
		if addrPub.EncodeAddress() != addrPriv.EncodeAddress() {
			t.Fatalf("公钥/私钥 tweak 地址不一致")
		}
		                                                            
		fmt.Println("\n[tweak] 各参与者私钥份额 s_j' (已加 tweak 并偶‑Y):")
		for i, share := range sj {
			fmt.Printf("  参与者 %d : %s\n", i+1, share.Text(16))
		}
	}
	                                              
	msg := sha256.Sum256([]byte("Hello, threshold sig"))
	RxDir, RyDir, zDir, _ := frost.SchnorrSign(grp, Q0t, msg[:])

	                                                          
	choose := []int{1, 2, 4}

	idsSel := make([]*big.Int, thr)
	kSel := make([]*big.Int, thr)
	RxSel := make([]*big.Int, thr)
	RySel := make([]*big.Int, thr)
	sSel := make([]*big.Int, thr)

	for i, idx := range choose {
		k, Rx, Ry := frost.ThresholdSchnorrPartialSign(grp, sj[idx], Qtx, Qty, msg[:])
		idsSel[i], kSel[i] = ids[idx], k
		RxSel[i], RySel[i] = Rx, Ry
		sSel[i] = sj[idx]
	}

	                                                                       
	RxSum, RySum := RxSel[0], RySel[0]
	for i := 1; i < thr; i++ {
		RSum := curve.Point{X: RxSum, Y: RySum}
		RSel := curve.Point{X: RxSel[i], Y: RySel[i]}
		RxSum, RySum = grp.Add(RSum, RSel).XY()
	}

	if RySum.Bit(0) == 1 {                                      
		for i := 0; i < thr; i++ {
			kSel[i].Sub(N, kSel[i])                               
			RySel[i].Sub(grp.Modulus(), RySel[i])                
		}
		RySum.Sub(grp.Modulus(), RySum)                  
	}

	                                               
	λ := dkg.ComputeLagrangeCoefficients(idsSel, N)
	                               
	e := frost.BIP340Challenge(RxSum, Qtx, msg[:], grp)

	zSel := make([]*big.Int, thr)
	for i := 0; i < thr; i++ {
		zSel[i] = frost.ThresholdSchnorrFinalize(grp,
			kSel[i], λ[i], e, sSel[i])
	}

	                                                    
	Rax, Ray, zAgg := frost.AggregateThresholdSignature(
		grp, idsSel, RxSel, RySel, zSel,
		Qtx, msg[:],
	)

	                                  
	if !frost.SchnorrVerify(grp, Qtx, Qty, Rax, Ray, zAgg, msg[:]) {
		t.Fatalf("阈值聚合签名验证失败")
	}
	if !frost.SchnorrVerify(grp, Qtx, Qty, RxDir, RyDir, zDir, msg[:]) {
		t.Fatalf("完整私钥签名验证失败")
	}
}

                                                                                       
                                                                     
func Test_GenerateThresholdKeys(t *testing.T) {
	grp := curve.NewSecp256k1Group()
	N := grp.Order()
	thr, nPart := 3, 5                

	                                            
	parts := make([]*dkg.Polynomial, nPart)
	for i := range parts {
		parts[i] = dkg.NewPolynomial(thr, grp)
	}

	ids := make([]*big.Int, nPart)        
	sj := make([]*big.Int, nPart)
	for j := 0; j < nPart; j++ {
		ids[j] = big.NewInt(int64(j + 1))
		sum := big.NewInt(0)
		for _, p := range parts {
			sum.Add(sum, p.Evaluate(ids[j], grp))
		}
		sj[j] = sum.Mod(sum, N)
	}

	                                                                
	Q0 := big.NewInt(0)
	for _, p := range parts {
		Q0.Add(Q0, p.Coefficients[0])
	}
	Q0.Mod(Q0, N)
	Qx, Qy := grp.ScalarBaseMult(Q0).XY()
	if Qy.Bit(0) == 1 {
		for j := range sj {
			sj[j].Sub(N, sj[j])
		}
		Q0.Sub(N, Q0)
		Qy.Sub(grp.Modulus(), Qy)
	}

	                                    
	tweak := curve.TapTweakScalar(Qx, "TapTweak")
	Q0t := new(big.Int).Add(Q0, tweak)
	Q0t.Mod(Q0t, N)
	for j := range sj {
		sj[j].Add(sj[j], tweak)
		sj[j].Mod(sj[j], N)
	}

	Qtx, Qty := grp.ScalarBaseMult(Q0t).XY()
	if Qty.Bit(0) == 1 {
		Q0t.Sub(N, Q0t)
		Qty.Sub(grp.Modulus(), Qty)
		for j := range sj {
			sj[j].Sub(N, sj[j])
		}
	}

	                                                           
	fmt.Println("\n========== 门限密钥生成结果 ==========")

	                                             
	fmt.Printf("\n[tweak] Q0' (聚合私钥): %s\n", Q0t.Text(16))

	                                  
	fmt.Printf("[tweak] Q'   (聚合公钥): (%s, %s)\n", Qtx.Text(16), Qty.Text(16))

	                                                 
	privTweaked, _ := btcec.PrivKeyFromBytes(Q0t.FillBytes(make([]byte, 32)))
	wifT, _ := btcutil.NewWIF(privTweaked, &chaincfg.TestNet3Params, true)
	fmt.Println("[tweak] 聚合私钥 (WIF):", wifT.String())

	                                             
	addrPriv, _ := btcutil.NewAddressTaproot(
		schnorr.SerializePubKey(privTweaked.PubKey()),
		&chaincfg.TestNet3Params,
	)
	fmt.Println("[tweak] Taproot 地址：", addrPriv.EncodeAddress())

	                                                         
	pubTweaked := btcec.NewPublicKey(
		curve.BigIntToFieldVal(Qtx),
		curve.BigIntToFieldVal(Qty),
	)
	addrPub, _ := btcutil.NewAddressTaproot(
		schnorr.SerializePubKey(pubTweaked),
		&chaincfg.TestNet3Params,
	)

	                                                         
	if addrPub.EncodeAddress() != addrPriv.EncodeAddress() {
		t.Fatalf("公钥/私钥 tweak 地址不一致")
	}

	                                                            
	fmt.Println("\n[tweak] 各参与者私钥份额 s_j' (已加 tweak 并偶‑Y):")
	for i, share := range sj {
		fmt.Printf("  参与者 %d : %s\n", i+1, share.Text(16))
	}

	                                                         
	fmt.Printf("\n[tweak] 聚合公钥 X (用于签名): %s\n", Qtx.Text(16))

	fmt.Println("\n========================================")
	fmt.Println("请将上述私钥份额和公钥 X 坐标复制到 Test_ThresholdSignRawTX 中使用")
	fmt.Println("========================================")

	t.Logf("门限密钥生成成功: %d-of-%d, 地址: %s", thr, nPart, addrPriv.EncodeAddress())
}

                                                                                        
                                                          
func Test_ThresholdSignRawTX(t *testing.T) {
	grp := curve.NewSecp256k1Group()
	thr := 3             

	                                                                                      

	                                                
	QtxHex := "19ea65f3443b5e5253326a0234803ffd2a1cd5dd2f0b0a6c10dc2fd449f05f0f"                         

	                                                      
	                                               
	participantShares := map[int]string{
		1: "406125103d66fc2f17d15c9ba1e72866dc2d4b162a506bd1246fdc61d7dd3041",             
		2: "d4c1013eb479010e89c147a58553fe623105fdbb902b240a6e18bfb04b0819ef",             
		3: "9ec069c8ecd99a4db99350c283896b10814426889757ee66d8f657cfd7f11762",             
		4: "9e5f5eaee688c7eca74777f29c876e708796a263ef1f6b2224db034d4ece69db",             
		5: "d39ddff0a18689eb52ddbd35d04e088243fd714d97819a3c51c6c228afa0115a",             
	}

	                                                        
	chosenParticipants := []int{1, 2, 4}

	              
	prevTxHashStr := "b61c72b21030e0306478b3fb8c01827ee15e108a4652a6f994ba9a382861cf9a"                      
	prevOutIndex := uint32(0)                                                                          
	prevOutAmount := int64(500000)                                                                               

	               
	outputAddr := "tb1pr84xtu6y8d09y5ejdgprfqpll54pe4wa9u9s5mqsmshagj0stu8sxha5ss"                
	outputAmount := int64(499500)                                                                            

	                                        

	                                        
	Qtx, ok := new(big.Int).SetString(QtxHex, 16)
	if !ok {
		t.Fatalf("解析聚合公钥 X 失败")
	}

	                                       
	idsSel := make([]*big.Int, thr)
	sjSel := make([]*big.Int, thr)
	for i, pid := range chosenParticipants {
		idsSel[i] = big.NewInt(int64(pid))
		shareHex, exists := participantShares[pid]
		if !exists {
			t.Fatalf("参与者 %d 的私钥份额不存在", pid)
		}
		share, ok := new(big.Int).SetString(shareHex, 16)
		if !ok {
			t.Fatalf("解析参与者 %d 的私钥份额失败", pid)
		}
		sjSel[i] = share
	}

	                                        
	tx := wire.NewMsgTx(wire.TxVersion)

	                      
	prevTxHash, err := chainhash.NewHashFromStr(prevTxHashStr)
	if err != nil {
		t.Fatalf("解析前序交易哈希失败: %v", err)
	}
	outPoint := wire.NewOutPoint(prevTxHash, prevOutIndex)
	txIn := wire.NewTxIn(outPoint, nil, nil)
	tx.AddTxIn(txIn)

	               
	addr, err := btcutil.DecodeAddress(outputAddr, &chaincfg.TestNet3Params)
	if err != nil {
		t.Fatalf("解析输出地址失败: %v", err)
	}
	pkScript, err := txscript.PayToAddrScript(addr)
	if err != nil {
		t.Fatalf("生成输出脚本失败: %v", err)
	}
	txOut := wire.NewTxOut(outputAmount, pkScript)
	tx.AddTxOut(txOut)

	                                              
	fetcher := txscript.NewCannedPrevOutputFetcher(pkScript, prevOutAmount)
	sigHashes := txscript.NewTxSigHashes(tx, fetcher)
	sigHash, err := txscript.CalcTaprootSignatureHash(
		sigHashes,
		txscript.SigHashDefault,
		tx,
		0,                   
		fetcher,
	)
	if err != nil {
		t.Fatalf("计算签名哈希失败: %v", err)
	}

	                                        
	fmt.Println("\n========== 门限签名过程 ==========")
	fmt.Printf("参与者: %v\n", chosenParticipants)
	fmt.Printf("签名哈希: %s\n", hex.EncodeToString(sigHash))

	sigBytes, err := frost.ThresholdSign(
		grp,
		idsSel,
		sjSel,
		sigHash,
		Qtx,
		frost.BIP340Challenge,                                 
	)
	if err != nil {
		t.Fatalf("门限签名失败: %v", err)
	}

	                                                  
	tx.TxIn[0].Witness = wire.TxWitness{sigBytes}

	                                        
	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		t.Fatalf("序列化交易失败: %v", err)
	}

	rawTx := hex.EncodeToString(buf.Bytes())
	fmt.Println("\n========== 门限签名生成的 Raw TX ==========")
	fmt.Printf("Raw TX: %s\n", rawTx)
	fmt.Printf("TX Size: %d bytes\n", len(buf.Bytes()))
	fmt.Printf("Signature: %s\n", hex.EncodeToString(sigBytes))
	fmt.Println("============================================")

	t.Logf("门限签名Raw TX生成成功，长度: %d bytes", len(buf.Bytes()))
}
