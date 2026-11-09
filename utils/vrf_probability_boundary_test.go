package utils

import "testing"

func TestCalculateBlockProbabilityThresholdBounds(t *testing.T) {
	maxOutput := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	if CalculateBlockProbability(maxOutput, 0) {
		t.Fatal("zero threshold accepted a proposal")
	}
	if !CalculateBlockProbability(maxOutput, 1) {
		t.Fatal("unit threshold rejected a proposal")
	}
}
