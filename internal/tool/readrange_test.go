package tool

import (
	"math/rand"
	"slices"
	"testing"
)

func TestNormalizeReadRanges(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in, want []ReadRange
	}{
		{"empty dropped", []ReadRange{{5, 5}, {7, 3}}, nil},
		{"adjacent merge", []ReadRange{{0, 5}, {5, 9}}, []ReadRange{{0, 9}}},
		{"overlap merge", []ReadRange{{0, 6}, {4, 9}}, []ReadRange{{0, 9}}},
		{"disjoint kept", []ReadRange{{5, 7}, {0, 2}}, []ReadRange{{0, 2}, {5, 7}}},
		{"contained dropped", []ReadRange{{0, 10}, {3, 5}}, []ReadRange{{0, 10}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := slices.Clone(tc.in)
			if got := NormalizeReadRanges(tc.in); !slices.Equal(got, tc.want) {
				t.Fatalf("NormalizeReadRanges(%+v) = %+v, want %+v", tc.in, got, tc.want)
			}
			if !slices.Equal(original, tc.in) {
				t.Fatal("normalization mutated input")
			}
		})
	}
}

func TestNormalizeReadRangesPreservesCoveredLines(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for range 500 {
		var input []ReadRange
		var covered [64]bool
		for range 1 + rng.Intn(6) {
			start := rng.Intn(40)
			r := ReadRange{Start: start, End: start + rng.Intn(12)}
			input = append(input, r)
			for line := r.Start; line < r.End; line++ {
				covered[line] = true
			}
		}
		output := NormalizeReadRanges(input)
		var actual [64]bool
		for i, r := range output {
			if i > 0 && output[i-1].End >= r.Start {
				t.Fatalf("unmerged ranges: %+v", output)
			}
			for line := r.Start; line < r.End; line++ {
				actual[line] = true
			}
		}
		if covered != actual {
			t.Fatalf("normalization changed coverage: %+v -> %+v", input, output)
		}
		rng.Shuffle(len(input), func(i, j int) { input[i], input[j] = input[j], input[i] })
		if !slices.Equal(NormalizeReadRanges(input), output) {
			t.Fatal("normalization depends on input order")
		}
		if !slices.Equal(NormalizeReadRanges(output), output) {
			t.Fatal("normalization is not idempotent")
		}
	}
}
