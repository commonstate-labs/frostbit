package pb

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestSanitize_NormalAnyTx(t *testing.T) {
	tx := &AnyTx{Content: &AnyTx_Transaction{Transaction: &Transaction{
		Base: &BaseMessage{TxId: "abc123", FromAddress: "fbc1qxyz", Fee: "1000"},
		To:   "fbc1qdest", TokenAddress: "FB", Amount: "100",
	}}}
	if err := Sanitize(tx); err != nil {
		t.Fatalf("normal tx should pass: %v", err)
	}
}

func TestSanitize_StringTooLong(t *testing.T) {
	tx := &AnyTx{Content: &AnyTx_Transaction{Transaction: &Transaction{
		Base: &BaseMessage{TxId: "abc", StatusText: strings.Repeat("x", MaxStringLen+1)},
	}}}
	err := Sanitize(tx)
	if err == nil {
		t.Fatal("expected error for oversized description")
	}
	if !errors.Is(err, ErrDataBloat) {
		t.Fatalf("expected ErrDataBloat, got: %v", err)
	}
}

func TestSanitize_BytesTooLong(t *testing.T) {
	tx := &AnyTx{Content: &AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: &FrostWithdrawSignedTx{
		Base:               &BaseMessage{TxId: "abc"},
		SignedPackageBytes: make([]byte, 600*1024),                       
	}}}
	err := Sanitize(tx)
	if err == nil {
		t.Fatal("expected error for oversized bytes")
	}
	if !errors.Is(err, ErrDataBloat) {
		t.Fatalf("expected ErrDataBloat, got: %v", err)
	}
}

func TestSanitize_WithdrawCancelProofBounded(t *testing.T) {
	tx := &AnyTx{Content: &AnyTx_FrostWithdrawCancelTx{FrostWithdrawCancelTx: &FrostWithdrawCancelTx{
		Base: &BaseMessage{TxId: "cancel"}, WithdrawId: "withdraw", OriginalRequestTx: make([]byte, 16*1024+1),
	}}}
	err := Sanitize(tx)
	if err == nil || !errors.Is(err, ErrDataBloat) {
		t.Fatalf("expected oversized original request proof to be rejected, got %v", err)
	}
}

func TestSanitize_RepeatedTooMany(t *testing.T) {
	ids := make([]string, 6000)                     
	for i := range ids {
		ids[i] = "id"
	}
	tx := &AnyTx{Content: &AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: &FrostWithdrawSignedTx{
		Base:        &BaseMessage{TxId: "abc"},
		WithdrawIds: ids,
	}}}
	err := Sanitize(tx)
	if err == nil {
		t.Fatal("expected error for oversized repeated")
	}
	if !errors.Is(err, ErrDataBloat) {
		t.Fatalf("expected ErrDataBloat, got: %v", err)
	}
}

func TestSanitizeFrostActiveMinersProtocolCap(t *testing.T) {
	valid := &FrostActiveMiners{
		Addresses:          make([]string, 175),
		Indices:            make([]uint64, 175),
		PublicKeys:         make([][]byte, 175),
		MaturedStakes:      make([]uint64, 175),
		ActiveSinceHeights: make([]uint64, 175),
	}
	if err := Sanitize(valid); err != nil {
		t.Fatalf("175-candidate snapshot rejected: %v", err)
	}
	tooManyIdentities := proto.Clone(valid).(*FrostActiveMiners)
	tooManyIdentities.Addresses = append(tooManyIdentities.Addresses, "overflow")
	if err := Sanitize(tooManyIdentities); !errors.Is(err, ErrDataBloat) {
		t.Fatalf("176 identities were not rejected: %v", err)
	}
	tooManyRankingValues := proto.Clone(valid).(*FrostActiveMiners)
	tooManyRankingValues.MaturedStakes = append(tooManyRankingValues.MaturedStakes, 1)
	if err := Sanitize(tooManyRankingValues); !errors.Is(err, ErrDataBloat) {
		t.Fatalf("176 ranking values were not rejected: %v", err)
	}
}

func TestSanitize_NormalBlock(t *testing.T) {
	block := &Block{
		BlockHash: "hash123",
		Header:    &BlockHeader{Height: 100, Miner: "fbc1q"},
		Body: []*AnyTx{
			{Content: &AnyTx_Transaction{Transaction: &Transaction{
				Base: &BaseMessage{TxId: "tx1"}, Amount: "100",
			}}},
		},
	}
	if err := Sanitize(block); err != nil {
		t.Fatalf("normal block should pass: %v", err)
	}
}

func TestSanitize_PushQuery_LargeContainer(t *testing.T) {
	                                                               
	pq := &PushQuery{
		RequestId: 1,
		Address:   "fbc1q",
		Container: make([]byte, 1024*1024),
	}
	if err := Sanitize(pq); err != nil {
		t.Fatalf("1MB container should pass: %v", err)
	}
	for _, size := range []int{1700 * 1024, MaxTotalSize} {
		pq.Container = make([]byte, size)
		pq.Signature = make([]byte, 128)
		pq.Nonce = make([]byte, 32)
		if err := Sanitize(pq); err != nil {
			t.Fatalf("legal %d-byte container rejected: %v", size, err)
		}
	}
	                                                          
	pq2 := &PushQuery{
		RequestId: 1,
		Address:   "fbc1q",
		Container: make([]byte, 2*1024*1024+1),
	}
	if err := Sanitize(pq2); err == nil {
		t.Fatal("2MB+ container should be rejected")
	}
}

func TestPushQueryCarriesMaximumBlockThroughBothDecoders(t *testing.T) {
	tx := &AnyTx{Content: &AnyTx_Transaction{Transaction: &Transaction{
		Base: &BaseMessage{TxId: strings.Repeat("a", 64), StatusText: strings.Repeat("x", 260)},
		To:   "receiver", Amount: "1", TokenAddress: "FB",
	}}}
	block := &Block{BlockHash: "block-1", Header: &BlockHeader{Height: 1}, Body: make([]*AnyTx, 5000)}
	for i := range block.Body {
		block.Body[i] = tx
	}
	if size := proto.Size(block); size <= 1536*1024 || size >= MaxTotalSize {
		t.Fatalf("fixture size=%d, want between old container limit and block limit", size)
	}
	                                                                                     
	tag := protowire.AppendTag(nil, 1000, protowire.BytesType)
	padding := MaxTotalSize - proto.Size(block) - len(tag) - 3
	block.ProtoReflect().SetUnknown(protowire.AppendBytes(tag, make([]byte, padding)))
	if size := proto.Size(block); size != MaxTotalSize {
		t.Fatalf("maximum block fixture size=%d", size)
	}
	if err := Sanitize(block); err != nil {
		t.Fatal(err)
	}
	container, err := proto.Marshal(block)
	if err != nil {
		t.Fatal(err)
	}
	query := &PushQuery{
		ContainerIsBlock: true, Container: container,
		Address: strings.Repeat("a", MaxStringLen), Responder: strings.Repeat("b", MaxStringLen),
		BlockId: strings.Repeat("c", MaxStringLen), Signature: make([]byte, 128), Nonce: make([]byte, 32), VrfSeed: make([]byte, 64),
	}
	wire, err := proto.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PushQuery
	if err := UnmarshalSanitize(wire, &decoded); err != nil {
		t.Fatalf("outer query rejected: %v", err)
	}
	if err := UnmarshalSanitize(decoded.Container, &Block{}); err != nil {
		t.Fatalf("inner block rejected: %v", err)
	}
	if err := UnmarshalSanitize(make([]byte, MaxPushQuerySize+1), &PushQuery{}); !errors.Is(err, ErrDataBloat) {
		t.Fatalf("oversize wire was not rejected before decoding: %v", err)
	}
	if err := UnmarshalSanitize(make([]byte, MaxTotalSize+1), &Block{}); !errors.Is(err, ErrDataBloat) {
		t.Fatalf("block limit was widened: %v", err)
	}
}

func TestSanitize_FrostEnvelope(t *testing.T) {
	                 
	env := &FrostEnvelope{From: "a", To: "b", Payload: make([]byte, 100*1024)}
	if err := Sanitize(env); err != nil {
		t.Fatalf("normal envelope should pass: %v", err)
	}
	                 
	env2 := &FrostEnvelope{From: "a", Payload: make([]byte, 300*1024)}
	if err := Sanitize(env2); err == nil {
		t.Fatal("oversized payload should be rejected")
	}
}

func TestSanitize_DefaultStringLimit(t *testing.T) {
	                                                                   
	tx := &AnyTx{Content: &AnyTx_Transaction{Transaction: &Transaction{
		Base: &BaseMessage{TxId: "abc", StatusText: strings.Repeat("x", 1100)},
	}}}
	err := Sanitize(tx)
	if err == nil {
		t.Fatal("expected error for oversized status_text")
	}
}
