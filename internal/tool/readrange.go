package tool

import "slices"

// NormalizeReadRanges drops empty ranges and merges overlaps and adjacency
// into the smallest equivalent set. The caller's slice remains unchanged.
func NormalizeReadRanges(ranges []ReadRange) []ReadRange {
	out := make([]ReadRange, 0, len(ranges))
	for _, r := range ranges {
		if !r.Empty() {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil
	}
	slices.SortFunc(out, func(a, b ReadRange) int {
		if a.Start != b.Start {
			return a.Start - b.Start
		}
		return a.End - b.End
	})
	merged := out[:1]
	for _, r := range out[1:] {
		last := &merged[len(merged)-1]
		if r.Start <= last.End {
			last.End = max(last.End, r.End)
			continue
		}
		merged = append(merged, r)
	}
	return merged
}
