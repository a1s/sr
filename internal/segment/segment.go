// Package segment solves one-dimensional stretchable segment layout.
//
// Build a partial order of "segment A lies wholly before segment B",
// then propagate positions in topological order using actual measured
// extents rather than declared ones.
//
// The engine uses it vertically, for floating elements: a floating element
// sits below whatever lies above it, and how far below is its declared gap
// to the nearest predecessor.
package segment

import "sort"

// Segment is one participant, identified by its index in the caller's slice.
//
// Start and Extent are the declared geometry, which is what the partial order
// and the gaps are computed from. The measured extent is supplied separately,
// so the same order holds for every record.
type Segment struct {
	Start  float64
	Extent float64
}

// Layout is a solved partial order, reusable across measurements.
type Layout struct {
	order    []int     // topological order, dependencies first
	deps     [][]int   // per segment, the segments it follows
	gaps     []float64 // per segment, distance to its nearest declared predecessor
	declared []float64 // per segment, its declared near edge
	count    int
}

// Precedes reports whether earlier lies wholly before seg:
// earlier's far edge is at or before seg's near edge.
func Precedes(earlier, seg Segment) bool { return earlier.Start+earlier.Extent <= seg.Start }

// New builds the layout.
//
// deps maps each segment to the segments it depends on -- those that must
// be placed first. It must be acyclic and hold an entry for every segment.
func New(segments []Segment, deps [][]int) *Layout {
	layout := &Layout{count: len(segments), deps: deps, gaps: leadingGaps(segments, 0)}
	layout.declared = make([]float64, len(segments))
	for index, seg := range segments {
		layout.declared[index] = seg.Start
	}
	layout.order = toposort(deps)
	return layout
}

// Solve returns each segment's actual start, given its actual extent.
//
// A segment with no dependencies keeps its declared start.
// One with dependencies begins after the last of them ends,
// plus its gap -- and the gap applies only when the segment has
// an extent of its own, so a collapsed segment neither occupies space
// nor reserves any.
func (layout *Layout) Solve(extents []float64) []float64 {
	starts := make([]float64, layout.count)
	ends := make([]float64, layout.count)
	for _, index := range layout.order {
		extent := extents[index]
		start := 0.0
		if len(layout.deps[index]) == 0 {
			start = layout.declaredStart(index)
		} else {
			first := true
			for _, dep := range layout.deps[index] {
				if first || ends[dep] > start {
					start, first = ends[dep], false
				}
			}
			if extent > 0 {
				start += layout.gaps[index]
			}
		}
		starts[index] = start
		ends[index] = start + extent
	}
	return starts
}

func (layout *Layout) declaredStart(index int) float64 { return layout.declared[index] }

// leadingGaps returns, for each segment, the distance from its near edge
// to the far edge of the nearest segment lying wholly before it.
//
// A bound segment sitting just before origin stands in for "nothing before
// this", so a segment with no real predecessor measures its gap from there.
func leadingGaps(segments []Segment, origin float64) []float64 {
	gaps := make([]float64, len(segments))
	if len(segments) == 0 {
		return gaps
	}
	low := origin
	for _, seg := range segments {
		if seg.Start < low {
			low = seg.Start
		}
	}
	bound := Segment{Start: low - 1, Extent: 1}
	for index, seg := range segments {
		nearest := bound.Start + bound.Extent
		for other, earlier := range segments {
			if index == other || !Precedes(earlier, seg) {
				continue
			}
			if end := earlier.Start + earlier.Extent; end > nearest {
				nearest = end
			}
		}
		gaps[index] = seg.Start - nearest
	}
	return gaps
}

// toposort orders vertices so that every vertex follows the ones it depends on.
func toposort(deps [][]int) []int {
	order := make([]int, 0, len(deps))
	visited := make([]bool, len(deps))
	var walk func(int)
	walk = func(node int) {
		if visited[node] {
			return
		}
		visited[node] = true
		for _, dep := range deps[node] {
			walk(dep)
		}
		order = append(order, node)
	}
	for node := range deps {
		walk(node)
	}
	return order
}

// Reduce removes edges that transitivity already implies, leaving the minimal
// partial order: if C follows both A and B and B follows A, C follows B alone.
func Reduce(deps [][]int) [][]int {
	reach := make([]map[int]bool, len(deps))
	order := toposort(deps)
	for _, node := range order {
		reachable := map[int]bool{}
		for _, dep := range deps[node] {
			reachable[dep] = true
			for beyond := range reach[dep] {
				reachable[beyond] = true
			}
		}
		reach[node] = reachable
	}
	out := make([][]int, len(deps))
	for node := range deps {
		indirect := map[int]bool{}
		for _, dep := range deps[node] {
			for beyond := range reach[dep] {
				indirect[beyond] = true
			}
		}
		var kept []int
		for _, dep := range deps[node] {
			if !indirect[dep] {
				kept = append(kept, dep)
			}
		}
		sort.Ints(kept)
		out[node] = kept
	}
	return out
}
