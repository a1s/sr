package build

import (
	"github.com/a1s/sr/internal/geom"
	"github.com/a1s/sr/internal/tmpl"
	"github.com/a1s/sr/printout"
)

// Balancing a columns block, per doc/layout.md#balanced-columns.
//
// A frame fills its columns one after another, so the content that does
// not reach the bottom of the last one leaves it short: twenty rows on
// the left and two on the right. Balancing spreads that last run of bands
// over the columns it reached, so they end at similar heights.
//
// It happens after the fact rather than by predicting the run's extent.
// The engine is a single pass over records whose bands are measured where
// they land, so the extent is not known until the bands are placed; but by
// then every band's height is known, its marks are in hand, and the columns
// are the same width, so moving a band to another column is a translation
// and nothing has to be measured again. Nothing is re-evaluated either,
// which is the whole of the cost: an expression that read its own position
// was answered before the move. The fragment rules below refuse the cases
// where that shows.

// placedBand is one band committed inside a balanced frame.
type placedBand struct {
	marks  []printout.Mark
	column int
	// top is the band's position on the page, and height its extent.
	top, height float64
}

// fragment is what a balanced frame has been given since the page opened.
//
// blocked records that something in it cannot survive being moved,
// in which case the fragment is left exactly as it was placed.
type fragment struct {
	bands   []placedBand
	blocked bool
}

// balanced returns the frame itself or the nearest ancestor that balances,
// and whether a band placed here can be moved by it.
//
// A columns block between the two fills side by side rather than one band after
// another, so its bands reach the balanced frame as a single run that is not
// in the order the page reads them. Nothing there can be repacked by height.
func (fr *frame) balanced() (host *frame, movable bool) {
	movable = true
	for node := fr; node != nil; node = node.parent {
		if node.balance {
			return node, movable
		}
		if node.columnCount > 1 {
			movable = false
		}
	}
	return nil, movable
}

// recording reports whether a band placed here is one a balanced frame
// may move afterwards, and so whether its marks have to be kept.
func (fr *frame) recording() bool {
	host, movable := fr.balanced()
	return host != nil && movable
}

// blockBalance refuses to balance the fragment this frame prints into.
//
// The fragment is started if it has not been already, because what blocks it
// is often the reason its first band went where it did. A page break starts
// a fragment over, so a block reaches no further than the page it was made on.
func (fr *frame) blockBalance() {
	host, _ := fr.balanced()
	if host == nil {
		return
	}
	if host.fragment == nil {
		host.fragment = &fragment{}
	}
	host.fragment.blocked = true
}

// blockEnclosed refuses to balance every started fragment below this frame,
// because a band placed here interleaves with the columns underneath it and
// would be left behind by anything that moved.
func (fr *frame) blockEnclosed() {
	for _, child := range fr.children {
		if child.fragment != nil {
			child.fragment.blocked = true
		}
		child.blockEnclosed()
	}
}

// record adds a committed band to the fragment of the frame it prints into,
// or blocks the fragments underneath when it prints outside them.
func (fr *frame) record(sec *tmpl.Section, marks []printout.Mark, top, height float64) {
	// A header and a footer belong to the frame rather than to its fill:
	// both are placed against a column edge and stay where that column is.
	if sec.Kind == tmpl.BandHeader || sec.Kind == tmpl.BandFooter {
		return
	}
	host, movable := fr.balanced()
	if host == nil {
		fr.blockEnclosed()
		return
	}
	if host.fragment == nil {
		host.fragment = &fragment{}
	}
	if !movable {
		host.fragment.blocked = true
		return
	}
	host.fragment.bands = append(host.fragment.bands, placedBand{
		marks:  marks,
		column: host.column,
		top:    top,
		height: height,
	})
}

// balanceFrames spreads every balanced frame's last fragment.
//
// It runs once the frames have all the content they are going to get
// and before anything is placed below them, so that what follows
// starts at the balanced bottom rather than at the ragged one.
func (eng *engine) balanceFrames() {
	eng.frames.walk(func(fr *frame) {
		if fr.balance {
			fr.balanceFragment()
		}
	})
}

// balanceFragment redistributes one frame's fragment, or leaves it alone.
func (fr *frame) balanceFragment() {
	frag := fr.fragment
	if frag == nil || frag.blocked || len(frag.bands) < 2 {
		return
	}
	starts, reached, first, ok := fr.columnStarts(frag.bands)
	if !ok || len(starts) < 2 {
		return
	}

	// What the frame did is reproduced by filling the same columns
	// to the same bottom. If it is not, something other than the room left
	// decided where a band went -- an eject node, a keep-together lookahead --
	// and packing by height alone would undo it.
	if !sameColumns(packColumns(frag.bands, starts[:reached], fr.bottom), frag.bands, first) {
		return
	}

	// The shallowest bottom the same bands still reach in these columns.
	// Feasibility only improves as the bottom drops away, so it bisects.
	// The unit is the third decimal place, which is what geom.Round keeps.
	low, high := int64(lowest(starts)*1000), int64(geom.Round(fr.bottom*1000))
	for low < high {
		mid := low + (high-low)/2
		if packColumns(frag.bands, starts, float64(mid)/1000) != nil {
			high = mid
			continue
		}
		low = mid + 1
	}
	assigned := packColumns(frag.bands, starts, float64(high)/1000)
	if assigned == nil {
		return
	}
	if used := assigned[len(assigned)-1] + 1; used < reached {
		// A column the frame already opened would be left empty,
		// and its header is printed in it. Leaving the fragment alone
		// is the honest answer.
		return
	}
	fr.applyBalance(assigned, starts, first)
}

// columnStarts is where each column open to the fragment begins.
//
// The columns the fragment reached begin where its first band in each of them
// was placed. The ones it never reached are open to it only when the frame
// has no header and no footer: both are placed as a column opens, against the
// context of the moment, and balancing has no way to place them afterwards.
//
// The bands are in the order they were placed, so the columns they name run
// from the first upwards without a gap; anything else -- a column skipped
// by an eject, a band placed out of order -- is reported as unbalanceable.
func (fr *frame) columnStarts(bands []placedBand) (
	starts []float64, reached, first int, ok bool,
) {
	first = bands[0].column
	current := first
	starts = []float64{bands[0].top}
	for _, band := range bands {
		switch band.column {
		case current:
		case current + 1:
			current++
			starts = append(starts, band.top)
		default:
			return nil, 0, 0, false
		}
	}
	reached = len(starts)
	if fr.header == nil && fr.footer == nil {
		for column := first + reached; column < fr.columnCount; column++ {
			starts = append(starts, fr.top)
		}
	}
	return starts, reached, first, true
}

// lowest is the smallest of the column starts.
func lowest(starts []float64) float64 {
	out := starts[0]
	for _, start := range starts[1:] {
		if start < out {
			out = start
		}
	}
	return out
}

// packColumns fills each column until the next band would pass bottom.
//
// It answers the column index of every band, relative to the first,
// or nil when the bands need more columns than they started in.
func packColumns(bands []placedBand, starts []float64, bottom float64) []int {
	out := make([]int, len(bands))
	column, fill := 0, starts[0]
	for index, band := range bands {
		if !geom.Fits(band.height, geom.Round(bottom-fill)) {
			column++
			if column >= len(starts) {
				return nil
			}
			fill = starts[column]
			if !geom.Fits(band.height, geom.Round(bottom-fill)) {
				return nil
			}
		}
		out[index] = column
		fill = geom.Round(fill + band.height)
	}
	return out
}

// sameColumns reports whether an assignment puts every band where it went.
func sameColumns(assigned []int, bands []placedBand, first int) bool {
	if assigned == nil {
		return false
	}
	for index, band := range bands {
		if assigned[index]+first != band.column {
			return false
		}
	}
	return true
}

// applyBalance moves the fragment's bands to the columns they were assigned
// and leaves the frame filled to the deepest of them.
func (fr *frame) applyBalance(assigned []int, starts []float64, first int) {
	fill := make([]float64, len(starts))
	copy(fill, starts)
	deepest := starts[0]
	for index, band := range fr.fragment.bands {
		column := assigned[index]
		top := fill[column]
		across := (fr.width + fr.columnGap) * float64(column+first-band.column)
		down := geom.Round(top - band.top)
		if across != 0 || down != 0 {
			for _, mark := range band.marks {
				translate(mark, geom.Round(across), down)
			}
		}
		fill[column] = geom.Round(top + band.height)
		if fill[column] > deepest {
			deepest = fill[column]
		}
	}
	if last := first + assigned[len(assigned)-1]; last != fr.column {
		// The fragment spread into a column the frame had not opened,
		// which is where the frame now stands.
		fr.setColumn(last)
	}
	fr.refill(deepest)
}

// refill puts the fill position back to y, here and everywhere it was carried.
//
// Only a balanced fragment calls this, and a fragment that shares a page
// with a band outside it is blocked, so every position this lowers was set
// by the fragment that just moved.
func (fr *frame) refill(y float64) {
	fr.fillY = y
	for parent := fr.parent; parent != nil; parent = parent.parent {
		parent.fillY = y
	}
	var down func(*frame)
	down = func(node *frame) {
		for _, child := range node.children {
			child.fillY = y
			down(child)
		}
	}
	down(fr)
}
