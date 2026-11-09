                                                               
                                                                                                  
                                                                           

package roast

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"dex/frost/core/curve"
)

                                                    
type ChangePubTestVectors struct {
	KeyA           BN128KeyInfo         `json:"key_a"`
	KeyB           BN128KeyInfo         `json:"key_b"`
	KeyC           BN128KeyInfo         `json:"key_c"`
	ChangePubAB    BN128SignatureVector `json:"changepub_a_to_b"`                          
	ChangePubBC    BN128SignatureVector `json:"changepub_b_to_c"`                          
	WrongChangePub BN128SignatureVector `json:"wrong_changepub"`                                          
	WithdrawByA    BN128WithdrawVector  `json:"withdraw_by_a"`                                                       
	WithdrawByB    BN128WithdrawVector  `json:"withdraw_by_b"`                                                             
	WithdrawByC    BN128WithdrawVector  `json:"withdraw_by_c"`                                                             
}

type BN128KeyInfo struct {
	Px string `json:"px"`
	Py string `json:"py"`
}

type BN128SignatureVector struct {
	Px    string `json:"px"`                   
	Py    string `json:"py"`
	Rx    string `json:"rx"`
	Ry    string `json:"ry"`
	S     string `json:"s"`
	NewPx string `json:"new_px,omitempty"`                    
	NewPy string `json:"new_py,omitempty"`
}

type BN128WithdrawVector struct {
	Px        string `json:"px"`                   
	Py        string `json:"py"`
	Rx        string `json:"rx"`
	Ry        string `json:"ry"`
	S         string `json:"s"`
	Msg       string `json:"msg"`
	MsgHash   string `json:"msg_hash"`
	Amount    string `json:"amount"`
	Recipient string `json:"recipient"`
}

                                                                                    
func TestGenerateChangePubVectorsForSolidity(t *testing.T) {
	if os.Getenv("DEX_GENERATE_SOLIDITY_VECTORS") != "1" {
		t.Skip("set DEX_GENERATE_SOLIDITY_VECTORS=1 to regenerate Solidity BN128 vectors")
	}
	group := curve.NewBN256Group()
	threshold, numSigners := 3, 5
	signerIDs := []int{1, 2, 3, 4}

	                                          
	secretA := new(big.Int).SetBytes([]byte{
		0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef,
		0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10,
	})
	secretB := new(big.Int).SetBytes([]byte{
		0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x11, 0x22,
		0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0x00,
	})
	secretC := new(big.Int).SetBytes([]byte{
		0xde, 0xad, 0xbe, 0xef, 0xca, 0xfe, 0xba, 0xbe,
		0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0,
	})

	pubA := group.ScalarBaseMult(secretA)
	pubB := group.ScalarBaseMult(secretB)
	pubC := group.ScalarBaseMult(secretC)

	sharesA, _ := generateTestShares(secretA, threshold, numSigners, group)
	sharesB, _ := generateTestShares(secretB, threshold, numSigners, group)
	sharesC, _ := generateTestShares(secretC, threshold, numSigners, group)

	t.Logf("KeyA: Px=%s Py=%s", pad32Hex(pubA.X), pad32Hex(pubA.Y))
	t.Logf("KeyB: Px=%s Py=%s", pad32Hex(pubB.X), pad32Hex(pubB.Y))
	t.Logf("KeyC: Px=%s Py=%s", pad32Hex(pubC.X), pad32Hex(pubC.Y))

	                                                       
	changePubPayloadAB := make([]byte, 64)
	pubB.X.FillBytes(changePubPayloadAB[:32])
	pubB.Y.FillBytes(changePubPayloadAB[32:])
	changePubHashAB := keccak256(changePubPayloadAB)

	RAB, sAB := generateBN128FROSTSignatureForHash(t, group, signerIDs, sharesA, pubA, changePubHashAB)
	t.Logf("✅ changePub A→B 签名生成完成")

	                                                       
	changePubPayloadBC := make([]byte, 64)
	pubC.X.FillBytes(changePubPayloadBC[:32])
	pubC.Y.FillBytes(changePubPayloadBC[32:])
	changePubHashBC := keccak256(changePubPayloadBC)

	RBC, sBC := generateBN128FROSTSignatureForHash(t, group, signerIDs, sharesB, pubB, changePubHashBC)
	t.Logf("✅ changePub B→C 签名生成完成")

	                                                                      
	RWrong, sWrong := generateBN128FROSTSignatureForHash(t, group, signerIDs, sharesB, pubB, changePubHashAB)
	t.Logf("✅ 错误 changePub 签名生成完成（keyB 签名但 authority 是 keyA）")

	                          
	amount := new(big.Int).SetUint64(1_000_000_000_000_000_000)         
	token := big.NewInt(0)
	recipient := new(big.Int).SetBytes([]byte{0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42})
	withdrawMsg := make([]byte, 96)
	amount.FillBytes(withdrawMsg[:32])
	token.FillBytes(withdrawMsg[32:64])
	recipient.FillBytes(withdrawMsg[64:96])
	withdrawHash := keccak256(withdrawMsg)

	                        
	RwA, swA := generateBN128FROSTSignatureForHash(t, group, signerIDs, sharesA, pubA, withdrawHash)
	t.Logf("✅ withdraw by keyA 签名生成完成")

	                                                                   
	recipient2 := new(big.Int).SetBytes([]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11, 0x22, 0x33, 0x44})
	withdrawMsg2 := make([]byte, 96)
	amount.FillBytes(withdrawMsg2[:32])
	token.FillBytes(withdrawMsg2[32:64])
	recipient2.FillBytes(withdrawMsg2[64:96])
	withdrawHash2 := keccak256(withdrawMsg2)

	RwB, swB := generateBN128FROSTSignatureForHash(t, group, signerIDs, sharesB, pubB, withdrawHash2)
	t.Logf("✅ withdraw by keyB 签名生成完成")

	                                                                   
	recipient3 := new(big.Int).SetBytes([]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0x00, 0xaa, 0xbb, 0xcc, 0xdd})
	withdrawMsg3 := make([]byte, 96)
	amount.FillBytes(withdrawMsg3[:32])
	token.FillBytes(withdrawMsg3[32:64])
	recipient3.FillBytes(withdrawMsg3[64:96])
	withdrawHash3 := keccak256(withdrawMsg3)

	RwC, swC := generateBN128FROSTSignatureForHash(t, group, signerIDs, sharesC, pubC, withdrawHash3)
	t.Logf("✅ withdraw by keyC 签名生成完成")

	                       
	vectors := ChangePubTestVectors{
		KeyA: BN128KeyInfo{Px: pad32Hex(pubA.X), Py: pad32Hex(pubA.Y)},
		KeyB: BN128KeyInfo{Px: pad32Hex(pubB.X), Py: pad32Hex(pubB.Y)},
		KeyC: BN128KeyInfo{Px: pad32Hex(pubC.X), Py: pad32Hex(pubC.Y)},
		ChangePubAB: BN128SignatureVector{
			Px: pad32Hex(pubA.X), Py: pad32Hex(pubA.Y),
			Rx: pad32Hex(RAB.X), Ry: pad32Hex(RAB.Y), S: pad32Hex(sAB),
			NewPx: pad32Hex(pubB.X), NewPy: pad32Hex(pubB.Y),
		},
		ChangePubBC: BN128SignatureVector{
			Px: pad32Hex(pubB.X), Py: pad32Hex(pubB.Y),
			Rx: pad32Hex(RBC.X), Ry: pad32Hex(RBC.Y), S: pad32Hex(sBC),
			NewPx: pad32Hex(pubC.X), NewPy: pad32Hex(pubC.Y),
		},
		WrongChangePub: BN128SignatureVector{
			Px: pad32Hex(pubB.X), Py: pad32Hex(pubB.Y),
			Rx: pad32Hex(RWrong.X), Ry: pad32Hex(RWrong.Y), S: pad32Hex(sWrong),
			NewPx: pad32Hex(pubB.X), NewPy: pad32Hex(pubB.Y),
		},
		WithdrawByA: BN128WithdrawVector{
			Px: pad32Hex(pubA.X), Py: pad32Hex(pubA.Y),
			Rx: pad32Hex(RwA.X), Ry: pad32Hex(RwA.Y), S: pad32Hex(swA),
			Msg: hex.EncodeToString(withdrawMsg), MsgHash: hex.EncodeToString(withdrawHash),
			Amount: amount.String(), Recipient: "0x4242424242424242424242424242424242424242",
		},
		WithdrawByB: BN128WithdrawVector{
			Px: pad32Hex(pubB.X), Py: pad32Hex(pubB.Y),
			Rx: pad32Hex(RwB.X), Ry: pad32Hex(RwB.Y), S: pad32Hex(swB),
			Msg: hex.EncodeToString(withdrawMsg2), MsgHash: hex.EncodeToString(withdrawHash2),
			Amount: amount.String(), Recipient: "0x1122334455667788" + "99aabbccddeeff00" + "11223344",
		},
		WithdrawByC: BN128WithdrawVector{
			Px: pad32Hex(pubC.X), Py: pad32Hex(pubC.Y),
			Rx: pad32Hex(RwC.X), Ry: pad32Hex(RwC.Y), S: pad32Hex(swC),
			Msg: hex.EncodeToString(withdrawMsg3), MsgHash: hex.EncodeToString(withdrawHash3),
			Amount: amount.String(), Recipient: "0xaabbccddeeff1122" + "3344556677889900" + "aabbccdd",
		},
	}

	jsonData, _ := json.MarshalIndent(vectors, "", "  ")
	_, thisFile, _, _ := runtime.Caller(0)
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	outputPath := filepath.Join(projectRoot, "solidity", "test", "changepub_vectors.json")
	os.MkdirAll(filepath.Dir(outputPath), 0755)

	if err := os.WriteFile(outputPath, jsonData, 0644); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	t.Logf("✅ changePub 测试向量已写入: %s", outputPath)
}
