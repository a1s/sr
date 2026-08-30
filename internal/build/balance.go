package build

import (
	"math"

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

// block refuses to balance this frame's fragment.
//
// The fragment is started if it has not been already, because what blocks it
// is often the reason its first band went where it did. A page break starts
// a fragment over, so a block reaches no further than the page it was made on.
func (fr *frame) block() {
	if fr.fragment == nil {
		fr.fragment = &fragment{}
	}
	fr.fragment.blocked = true
}

// blockBalance refuses every balanced frame this one prints inside.
func (fr *frame) blockBalance() {
	for node := fr; node != nil; node = node.parent {
		if node.balance {
			node.block()
		}
	}
}

// blockAbove refuses every balanced frame outside the one that holds a band.
//
// The outer frame sees only what is placed between the inner one's runs,
// which is a sequence with holes where the inner content sits. Packing it by
// height would close those holes over content the outer frame never recorded.
func blockAbove(host *frame) {
	for node := host.parent; node != nil; node = node.parent {
		if node.balance {
			node.block()
		}
	}
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
// or blocks the fragments it cannot be moved by.
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
	blockAbove(host)
	if !movable {
		host.block()
		return
	}
	if host.fragment == nil {
		host.fragment = &fragment{}
	}
	host.fragment.bands = append(host.fragment.bands, placedBand{
		marks:  marks,
		column: host.column,
		top:    top,
		height: height,
	})
}

// balanceOwn spreads the last fragment of every balanced frame this engine
// built, which for an inline subreport leaves the host's frames alone:
// the host is still filling them, and ends them itself.
//
// It runs once the frames have all the content they are going to get and
// before anything is placed below them, so that what follows starts at
// the balanced bottom rather than at the ragged one.
func (eng *engine) balanceOwn() { balanceFrames(eng.frames.walkOwned) }

// balancePage spreads the last fragment of every balanced frame
// printing on the page that is ending, whichever engine built it.
func (eng *engine) balancePage() { balanceFrames(eng.frames.walk) }

func balanceFrames(walk func(func(*frame))) {
	walk(func(fr *frame) {
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

	// What the frame did is reproduced by filling the same columns to the
	// same bottom. If it is not -- a different column, or the same one at
	// a different height -- then something other than the room left decided
	// where a band went, and packing by height alone would undo it.
	if !reproduces(packColumns(frag.bands, starts[:reached], fr.bottom), frag.bands, first) {
		return
	}

	// The shallowest bottom the same bands still reach in these columns.
	// Feasibility only improves as the bottom drops away, so it bisects.
	// The unit is the third decimal place, which is what geom.Round keeps.
	low, high := millipoints(lowest(starts)), millipoints(fr.bottom)
	for low < high {
		mid := low + (high-low)/2
		if packColumns(frag.bands, starts, float64(mid)/1000) != nil {
			high = mid
			continue
		}
		low = mid + 1
	}
	slots := packColumns(frag.bands, starts, float64(high)/1000)
	if slots == nil {
		return
	}
	fr.applyBalance(slots, first)
}

// millipoints is a length as the whole thousandths geom.Round keeps it to.
func millipoints(value float64) int64 { return int64(math.Round(value * 1000)) }

// columnStarts is where each column open to the fragment begins.
//
// The columns the fragment reached begin where its first band in each of them
// was placed. The ones it never reached are open to it only when neither the
// frame nor anything under it places a header or a footer as a column opens:
// those are measured against the context of that moment, and balancing has
// no such moment to place one in afterwards.
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
	if !fr.hasFurniture() {
		for column := first + reached; column < fr.columnCount; column++ {
			starts = append(starts, fr.top)
		}
	}
	return starts, reached, first, true
}

// hasFurniture reports whether this frame or any under it
// places a header or a footer as a column opens.
func (fr *frame) hasFurniture() bool {
	if fr.header != nil || fr.footer != nil {
		return true
	}
	for _, child := range fr.children {
		if child.hasFurniture() {
			return true
		}
	}
	return false
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

// slot is where packing puts a band: a column counted from the first one the
// fragment used, and the position it takes in it.
type slot struct {
	column int
	top    float64
}

// packColumns fills each column until the next band would pass bottom.
//
// It answers a slot for every band, or nil when the bands need more columns
// than they have.
func packColumns(bands []placedBand, starts []float64, bottom float64) []slot {
	out := make([]slot, len(bands))
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
		out[index] = slot{column: column, top: fill}
		fill = geom.Round(fill + band.height)
	}
	return out
}

// reproduces reports whether a packing puts every band exactly where it went.
func reproduces(slots []slot, bands []placedBand, first int) bool {
	if slots == nil {
		return false
	}
	for index, band := range bands {
		if slots[index].column+first != band.column || slots[index].top != band.top {
			return false
		}
	}
	return true
}

// applyBalance moves the fragment's bands to the slots they were given
// and leaves the frame filled to the deepest of them.
//
// The fragment is rewritten as it moves, so that what it holds is where the
// bands now are: a page that ends twice -- balanced, and then again when a
// summary that would not fit ejects it -- must not move them a second time.
func (fr *frame) applyBalance(slots []slot, first int) {
	deepest := slots[0].top
	for index := range fr.fragment.bands {
		band := &fr.fragment.bands[index]
		column := slots[index].column + first
		across := (fr.width + fr.columnGap) * float64(column-band.column)
		down := geom.Round(slots[index].top - band.top)
		if across != 0 || down != 0 {
			for _, mark := range band.marks {
				translate(mark, geom.Round(across), down)
			}
		}
		band.column, band.top = column, slots[index].top
		if bottom := geom.Round(band.top + band.height); bottom > deepest {
			deepest = bottom
		}
	}
	if last := fr.fragment.bands[len(fr.fragment.bands)-1].column; last != fr.column {
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
