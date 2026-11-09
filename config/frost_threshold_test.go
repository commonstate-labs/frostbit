package config

import "testing"

func TestComputeFrostThresholdUsesWireFloat32Ratio(t *testing.T) {
	for _, test := range []struct {
		k     int
		ratio float64
		want  int
	}{
		{4, 0.75, 3},
		{8, 0.75, 6},
		{20, 0.75, 15},
		{100, 0.75, 75},
	} {
		got, err := ComputeFrostThreshold(test.k, test.ratio)
		if err != nil || got != test.want {
			t.Fatalf("ComputeFrostThreshold(%d,%v)=(%d,%v), want %d", test.k, test.ratio, got, err, test.want)
		}
	}
}

func TestComputeFrostThresholdRejectsInvalidDimensions(t *testing.T) {
	for _, test := range []struct {
		k     int
		ratio float64
	}{{0, 0.75}, {5, 0.75}, {4, 0.67}, {4, 1}} {
		if _, err := ComputeFrostThreshold(test.k, test.ratio); err == nil {
			t.Fatalf("invalid threshold dimensions accepted: %+v", test)
		}
	}
}
