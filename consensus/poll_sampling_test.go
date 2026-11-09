package consensus

import (
	"reflect"
	"testing"
)

func TestBuildReplacementSampleDeterministicAndAggregated(t *testing.T) {
	population := []int{0, 1, 2}
	first := BuildReplacementSample(population, 15, []byte("seed-a"))
	second := BuildReplacementSample(population, 15, []byte("seed-a"))
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same inputs produced different samples: %v vs %v", first, second)
	}

	total := 0
	seen := make(map[int]struct{}, len(first))
	for _, sample := range first {
		if sample.Weight <= 0 {
			t.Fatalf("non-positive sample weight: %+v", sample)
		}
		if _, duplicate := seen[sample.Value]; duplicate {
			t.Fatalf("population member returned more than once: %v", first)
		}
		seen[sample.Value] = struct{}{}
		total += sample.Weight
	}
	if total != 15 {
		t.Fatalf("aggregated slot total = %d, want 15", total)
	}
}

func TestBuildReplacementSampleCanRepeatOneMember(t *testing.T) {
	got := BuildReplacementSample([]string{"only"}, 15, []byte("seed"))
	want := []WeightedSample[string]{{Value: "only", Weight: 15}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("single-member replacement sample = %v, want %v", got, want)
	}
}

func TestBuildReplacementSampleEmptyInputs(t *testing.T) {
	if got := BuildReplacementSample([]int{1}, 0, []byte("seed")); got != nil {
		t.Fatalf("zero slots returned %v", got)
	}
	if got := BuildReplacementSample[int](nil, 15, []byte("seed")); got != nil {
		t.Fatalf("empty population returned %v", got)
	}
}
