package build

import (
	"fmt"

	"github.com/a1s/sr/internal/geom"
	"github.com/a1s/sr/internal/tmpl"
	"github.com/a1s/sr/printout"
)

// A mark outside the page's printable area is an overflow, and the printout's
// seventh invariant says so. It is judged here, when the document ends,
// rather than when a band is placed, because what a mark's coordinates
// finally are is not settled until then: balancing moves a band between
// columns, and a split band's tail is re-placed. A deferred value set later
// stays inside the room its placeholder took, so nothing after that moves
// a mark outward.
//
// See doc/layout.md#errors.

// bandSite is one band as it was committed, for the margin check to report.
type bandSite struct {
	page   *printout.Page
	node   string
	record int
	marks  []printout.Mark
}

// recordBand notes a committed band so the margin check can report it.
//
// The band's own list of marks is shared rather than copied:
// nothing appends to it once the band is committed, and where
// a balanced frame moves the band it moves these same marks,
// which is what the check has to read.
func (eng *engine) recordBand(node string, marks []printout.Mark) {
	if len(marks) == 0 {
		return
	}
	eng.doc.bands = append(eng.doc.bands, bandSite{
		page:   eng.doc.page,
		node:   node,
		record: eng.recordIndex(),
		marks:  marks,
	})
}

// checkMargins raises an overflow warning for each band with a mark outside
// its page's printable area, or fails the build where overflow is not allowed.
//
// A band is reported once, for the first of its marks in paint order
// that lies outside. A band already reported for fitting no frame
// is not reported again: it was placed knowing that it runs past its frame,
// and its warning says so.
func (eng *engine) checkMargins() error {
	for _, band := range eng.doc.bands {
		if band.page == nil {
			continue
		}
		if eng.doc.overflowed[overflowKey(band.node, band.record)] {
			continue
		}
		geometry := band.page.Geometry(eng.out.Header.Page)
		if message, ok := firstOutside(band.marks, geometry); ok {
			if err := eng.overflowAt(band.node, band.record, message); err != nil {
				return err
			}
		}
	}
	return nil
}

// firstOutside finds the first mark, in paint order,
// that leaves the printable area, and describes how.
//
// An xref's nested marks lie there too, and are reported against themselves
// rather than against the xref, since each came from a child element.
func firstOutside(
	marks []printout.Mark, geometry printout.PageGeometry,
) (string, bool) {
	for _, mark := range marks {
		if xref, ok := mark.(*printout.Xref); ok {
			if message, found := firstOutside(xref.Marks, geometry); found {
				return message, true
			}
		}
		box, ok := markBox(mark)
		if !ok {
			continue
		}
		if message, found := outsidePrintable(box, geometry); found {
			return fmt.Sprintf("the %s's %s", mark.MarkKind(), message), true
		}
	}
	return "", false
}

// markBox is the area a mark covers, which for an outline entry is its point.
func markBox(mark printout.Mark) (printout.Box, bool) {
	switch typed := mark.(type) {
	case *printout.Outline:
		return printout.Box{Left: typed.Left, Top: typed.Top}, true
	case *printout.Text:
		return typed.Box, true
	case *printout.Line:
		return typed.Box, true
	case *printout.Rectangle:
		return typed.Box, true
	case *printout.Image:
		return typed.Box, true
	case *printout.Barcode:
		return typed.Box, true
	case *printout.Xref:
		return typed.Box, true
	}
	return printout.Box{}, false
}

// outsidePrintable reports how a box leaves the printable area.
//
// Each edge is compared as a frame boundary is, with the tolerance added in
// binary64: a left edge x lies inside a left margin m where m <= x + 0.001.
func outsidePrintable(
	box printout.Box, geometry printout.PageGeometry,
) (string, bool) {
	left := geometry.LeftMargin
	top := geometry.TopMargin
	right := geom.Round(geometry.Width - geometry.RightMargin)
	bottom := geom.Round(geometry.Height - geometry.BottomMargin)
	boxRight := geom.Round(box.Left + box.Width)
	boxBottom := geom.Round(box.Top + box.Height)
	switch {
	case !geom.Fits(left, box.Left):
		return fmt.Sprintf("left edge is at %g pt, past the left margin at %g pt",
			box.Left, left), true
	case !geom.Fits(top, box.Top):
		return fmt.Sprintf("top edge is at %g pt, past the top margin at %g pt",
			box.Top, top), true
	case !geom.Fits(boxRight, right):
		return fmt.Sprintf("right edge is at %g pt, past the right margin at %g pt",
			boxRight, right), true
	case !geom.Fits(boxBottom, bottom):
		return fmt.Sprintf("bottom edge is at %g pt, past the bottom margin at %g pt",
			boxBottom, bottom), true
	}
	return "", false
}

// overflowKey identifies a band's placement, so that one already reported
// for fitting no frame is not reported again for running past the margin.
func overflowKey(node string, record int) string {
	return fmt.Sprintf("%s\x00%d", node, record)
}

// sectionNode is a band's node path, for a diagnostic to name.
func sectionNode(section *tmpl.Section) string {
	if section == nil || section.Node == nil {
		return "<band>"
	}
	return section.Node.Path()
}
