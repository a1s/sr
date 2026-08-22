package segment

import (
	"reflect"
	"testing"
)

func TestPrecedingSegments(test *testing.T) {
	// preceding_segments: for each segment, those lying wholly before it.
	cases := []struct {
		name string
		segs []Segment
		want map[int][]int
	}{
		{"empty", nil, map[int][]int{}},
		{"one", []Segment{{0, 1}}, map[int][]int{}},
		{"identical", []Segment{{0, 1}, {0, 1}}, map[int][]int{}},
		{"nested", []Segment{{0, 1}, {0, 2}}, map[int][]int{}},
		{"nested the other way", []Segment{{0, 2}, {0, 1}}, map[int][]int{}},
		{"abutting", []Segment{{0, 1}, {1, 1}}, map[int][]int{1: {0}}},
		{"two before one", []Segment{{0, 1}, {0, 2}, {3, 1}}, map[int][]int{2: {0, 1}}},
	}
	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			got := map[int][]int{}
			for index, seg := range testCase.segs {
				var before []int
				for other, earlier := range testCase.segs {
					if index != other && Precedes(earlier, seg) {
						before = append(before, other)
					}
				}
				if len(before) > 0 {
					got[index] = before
				}
			}
			if !reflect.DeepEqual(got, testCase.want) {
				test.Errorf("got %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestLeadingGaps(test *testing.T) {
	cases := []struct {
		name   string
		segs   []Segment
		origin float64
		want   []float64
	}{
		{"empty", nil, 0, []float64{}},
		{"one zero-width at the origin", []Segment{{0, 0}}, 0, []float64{0}},
		{"origin moved back one", []Segment{{0, 0}}, -1, []float64{1}},
		{"abutting", []Segment{{0, 1}, {1, 1}}, 0, []float64{0, 0}},
		{"two zero-width", []Segment{{1, 0}, {2, 0}}, 0, []float64{1, 1}},
		{"overlapping run", []Segment{{0, 5}, {1, 3}, {2, 1}}, 0, []float64{0, 1, 2}},
		{"a zero-width between", []Segment{{0, 2}, {1, 0}, {3, 1}}, 0, []float64{0, 1, 1}},
	}
	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			got := leadingGaps(testCase.segs, testCase.origin)
			if len(got) == 0 {
				got = []float64{}
			}
			if !reflect.DeepEqual(got, testCase.want) {
				test.Errorf("got %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestToposort(test *testing.T) {
	deps := [][]int{{1, 2}, {3}, {3}, {4, 5, 6}, {6}, {6}, {}}
	got := toposort(deps)
	if len(got) != 7 {
		test.Fatalf("got %v", got)
	}
	pos := make([]int, 7)
	for index, node := range got {
		pos[node] = index
	}
	for node, ds := range deps {
		for _, dep := range ds {
			if pos[dep] >= pos[node] {
				test.Errorf("%d must come before %d in %v", dep, node, got)
			}
		}
	}
}

// solve is the SegmentLayout(...)(...) of the original: build the layout
// from declared segments and a dependency map, then solve with actual extents.
func solve(segs []Segment, deps [][]int, extents []float64) []float64 {
	return New(segs, deps).Solve(extents)
}

func declaredExtents(segs []Segment) []float64 {
	out := make([]float64, len(segs))
	for index, seg := range segs {
		out[index] = seg.Extent
	}
	return out
}

func TestSegmentLayoutDoctests(test *testing.T) {
	check := func(name string, got, want []float64) {
		test.Helper()
		if !reflect.DeepEqual(got, want) {
			test.Errorf("%s: got %v, want %v", name, got, want)
		}
	}

	// SegmentLayout({})()  ->  {}
	if got := solve(nil, nil, nil); len(got) != 0 {
		test.Errorf("empty layout: got %v", got)
	}

	// SegmentLayout({(0, 1):[]})()  ->  {(0, 1): 0}
	segs := []Segment{{0, 1}}
	check("one segment", solve(segs, [][]int{{}}, declaredExtents(segs)), []float64{0})

	// SegmentLayout({(0, 1):[], (0, 2):[]})()  ->  both at 0
	segs = []Segment{{0, 1}, {0, 2}}
	check("two independent", solve(segs, [][]int{{}, {}}, declaredExtents(segs)), []float64{0, 0})

	// [(0, 1), (1, 1)] with 1 depending on 0
	segs = []Segment{{0, 1}, {1, 1}}
	deps := [][]int{{}, {0}}
	check("abutting, declared widths", solve(segs, deps, declaredExtents(segs)), []float64{0, 1})
	check("abutting, doubled widths", solve(segs, deps, []float64{2, 2}), []float64{0, 2})

	// [(0, 1), (2, 1)] with 1 depending on 0: a gap of 1 is preserved
	segs = []Segment{{0, 1}, {2, 1}}
	check("gap of one, declared", solve(segs, deps, declaredExtents(segs)), []float64{0, 2})
	check("gap of one, doubled", solve(segs, deps, []float64{2, 2}), []float64{0, 3})

	// [(0, 1, 4), (1, 1, 1)]: the first stretches to 4
	segs = []Segment{{0, 1}, {1, 1}}
	check("first stretches", solve(segs, deps, []float64{4, 1}), []float64{0, 4})

	// [(0, 4, 1), (5, 1, 1)]: the first collapses to 1, the gap of 1 holds
	segs = []Segment{{0, 4}, {5, 1}}
	check("first collapses", solve(segs, deps, []float64{1, 1}), []float64{0, 2})

	// [(0, 2, 5), (1, 3, 1), (5, 1, 1)] with 2 depending on 0 and 1
	segs = []Segment{{0, 2}, {1, 3}, {5, 1}}
	deps = [][]int{{}, {}, {0, 1}}
	check("two leaders", solve(segs, deps, []float64{5, 1, 1}), []float64{0, 1, 6})

	// [(0, 1, 5), (1, 3, 0), (5, 1, 1)]: a leader collapsing to zero
	segs = []Segment{{0, 1}, {1, 3}, {5, 1}}
	check("a leader collapses to zero", solve(segs, deps, []float64{5, 0, 1}), []float64{0, 1, 6})
}

// A zero-extent segment reserves no gap of its own, which is what keeps a
// suppressed element from pushing its followers down.
func TestZeroExtentTakesNoGap(test *testing.T) {
	segs := []Segment{{0, 10}, {20, 5}}
	deps := [][]int{{}, {0}}
	got := solve(segs, deps, []float64{10, 0})
	if got[1] != 10 {
		test.Errorf("a collapsed follower sits at its predecessor's end: got %v", got)
	}
	got = solve(segs, deps, []float64{10, 5})
	if got[1] != 20 {
		test.Errorf("a follower with extent takes its gap: got %v", got)
	}
}

func TestReduce(test *testing.T) {
	// C follows both A and B, and B follows A, so C follows B alone.
	deps := [][]int{{}, {0}, {0, 1}}
	got := Reduce(deps)
	want := [][]int{nil, {0}, {1}}
	if !reflect.DeepEqual(got, want) {
		test.Errorf("Reduce = %v, want %v", got, want)
	}
	// Reduction does not change the answer, since a transitive predecessor
	// always ends no later than the direct one.
	segs := []Segment{{0, 10}, {12, 4}, {20, 3}}
	full := solve(segs, deps, []float64{10, 4, 3})
	reduced := solve(segs, got, []float64{10, 4, 3})
	if !reflect.DeepEqual(full, reduced) {
		test.Errorf("reduction changed the result: %v vs %v", full, reduced)
	}
}
