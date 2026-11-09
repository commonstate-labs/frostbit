package btc

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
)

const testFeeRateSatsPerVByte = uint64(25)

func testP2WPKHScript(fill byte) []byte {
	return append([]byte{0x00, 0x14}, bytes.Repeat([]byte{fill}, 20)...)
}

func TestExpectedUserPaidWithdrawAmountsSingle(t *testing.T) {
	gross := uint64(100_000_000)
	recipientScript := testP2WPKHScript(0x11)
	fee, err := EstimateWithdrawFee(1, [][]byte{recipientScript}, testFeeRateSatsPerVByte)
	if err != nil {
		t.Fatal(err)
	}
	net, gotFee, change, err := ExpectedUserPaidWithdrawAmounts(gross, []uint64{gross}, 1, [][]byte{recipientScript}, nil, testFeeRateSatsPerVByte)
	if err != nil {
		t.Fatal(err)
	}
	if gotFee != fee || change != 0 || !reflect.DeepEqual(net, []uint64{gross - fee}) {
		t.Fatalf("net=%v fee=%d change=%d want net=%v fee=%d change=0", net, gotFee, change, []uint64{gross - fee}, fee)
	}
}

func TestExpectedUserPaidWithdrawAmountsSplitsFeeInFIFOOrder(t *testing.T) {
	gross := []uint64{10_000, 20_000, 30_000}
	totalInput := uint64(65_000)
	recipientScripts := [][]byte{testP2WPKHScript(0x11), testP2WPKHScript(0x22), testP2WPKHScript(0x33)}
	changeScript := testP2WPKHScript(0x44)
	fee, err := EstimateWithdrawFee(1, append(append([][]byte{}, recipientScripts...), changeScript), testFeeRateSatsPerVByte)
	if err != nil {
		t.Fatal(err)
	}
	base, remainder := fee/uint64(len(gross)), fee%uint64(len(gross))
	want := make([]uint64, len(gross))
	for i, amount := range gross {
		share := base
		if uint64(i) < remainder {
			share++
		}
		want[i] = amount - share
	}

	net, gotFee, change, err := ExpectedUserPaidWithdrawAmounts(totalInput, gross, 1, recipientScripts, changeScript, testFeeRateSatsPerVByte)
	if err != nil {
		t.Fatal(err)
	}
	if gotFee != fee || change != 5_000 || !reflect.DeepEqual(net, want) {
		t.Fatalf("net=%v fee=%d change=%d want net=%v fee=%d change=5000", net, gotFee, change, want, fee)
	}
}

func TestExpectedUserPaidWithdrawAmountsRejectsDust(t *testing.T) {
	recipientScript := testP2WPKHScript(0x11)
	changeScript := testP2WPKHScript(0x22)
	_, _, _, err := ExpectedUserPaidWithdrawAmounts(2_000, []uint64{2_000}, 1, [][]byte{recipientScript}, changeScript, testFeeRateSatsPerVByte)
	if err == nil || !strings.Contains(err.Error(), "below dust") {
		t.Fatalf("expected net dust rejection, got %v", err)
	}

	_, _, _, err = ExpectedUserPaidWithdrawAmounts(10_100, []uint64{10_000}, 1, [][]byte{recipientScript}, changeScript, testFeeRateSatsPerVByte)
	if err == nil || !strings.Contains(err.Error(), "change below dust") {
		t.Fatalf("expected change dust rejection, got %v", err)
	}
}

func TestEstimateWithdrawFeeUsesConfiguredRate(t *testing.T) {
	scripts := [][]byte{testP2WPKHScript(0x11), testP2WPKHScript(0x22)}
	fee25, err := EstimateWithdrawFee(1, scripts, 25)
	if err != nil {
		t.Fatal(err)
	}
	fee50, err := EstimateWithdrawFee(1, scripts, 50)
	if err != nil {
		t.Fatal(err)
	}
	if fee50 != fee25*2 {
		t.Fatalf("fee did not follow configured rate: fee25=%d fee50=%d", fee25, fee50)
	}
	if _, err := EstimateWithdrawFee(1, scripts, 0); err == nil {
		t.Fatal("zero fee rate was accepted")
	}
}

func TestEstimateWithdrawFeeMatchesFinalTaprootTransaction(t *testing.T) {
	address, err := btcutil.NewAddressWitnessPubKeyHash(bytes.Repeat([]byte{0x42}, 20), &chaincfg.MainNetParams)
	if err != nil {
		t.Fatal(err)
	}
	outputScript, err := AddressScript(address.EncodeAddress())
	if err != nil {
		t.Fatal(err)
	}

	vsize, err := ExactTaprootKeySpendVSize(2, [][]byte{outputScript})
	if err != nil {
		t.Fatal(err)
	}
	if vsize != 241 {
		t.Fatalf("exact vsize=%d want=241", vsize)
	}
	fee, err := EstimateWithdrawFee(2, [][]byte{outputScript}, testFeeRateSatsPerVByte)
	if err != nil {
		t.Fatal(err)
	}
	if fee != 6_025 {
		t.Fatalf("exact fee=%d want=6025", fee)
	}
	template := &BTCTemplate{
		Version: 2,
		Inputs: []TxInput{
			{TxID: strings.Repeat("11", 32), Vout: 0, Amount: 50_000, Sequence: DefaultSequence},
			{TxID: strings.Repeat("22", 32), Vout: 1, Amount: 50_000, Sequence: DefaultSequence},
		},
		Outputs: []TxOutput{{Address: address.EncodeAddress(), Amount: 100_000 - fee}},
	}
	decorateTestTemplate(template)
	raw, err := template.BuildSignedRawTx([][]byte{
		make([]byte, SchnorrSigSize),
		make([]byte, SchnorrSigSize),
	}, [][]byte{make([]byte, SchnorrSigSize), make([]byte, SchnorrSigSize)})
	if err != nil {
		t.Fatal(err)
	}
	var signed wire.MsgTx
	if err := signed.Deserialize(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	weight := uint64(signed.SerializeSizeStripped())*4 + uint64(signed.SerializeSize()-signed.SerializeSizeStripped())
	actualVSize := (weight + 3) / 4
	if actualVSize != vsize {
		t.Fatalf("estimated vsize=%d final signed vsize=%d", vsize, actualVSize)
	}
}

func TestExpectedUserPaidWithdrawAmountsUsesFinalScripts(t *testing.T) {
	recipientScript := append([]byte{0x00, 0x14}, bytes.Repeat([]byte{0x11}, 20)...)
	net, fee, change, err := ExpectedUserPaidWithdrawAmounts(
		100_000,
		[]uint64{100_000},
		2,
		[][]byte{recipientScript},
		nil,
		testFeeRateSatsPerVByte,
	)
	if err != nil {
		t.Fatal(err)
	}
	if fee != 6_025 || change != 0 || !reflect.DeepEqual(net, []uint64{93_975}) {
		t.Fatalf("net=%v fee=%d change=%d want net=[93975] fee=6025 change=0", net, fee, change)
	}
}
