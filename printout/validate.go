package printout

import (
	"fmt"
	"math"
	"strings"
)

// tolerance matches the engine's three-decimal rounding.
//
// Kept private because printout deliberately imports nothing from the engine.
const tolerance = 0.001

// Validate checks the invariants of doc/printout.md#invariants.
// A valid printout satisfies all of them; the test suite asserts this
// on every printout it produces.
//
// The numbers in the comments below are that list's numbering, so the checks
// read out of order: they are placed where the walk has what they need rather
// than in the order the document states them. Invariant 4 appears twice,
// once for an image's data and once for a font's.
func (doc *Printout) Validate() error {
	chk := &checker{
		doc:           doc,
		fonts:         map[string]bool{},
		outlineNames:  map[string]bool{},
		allowOverflow: doc.HasWarning(WarnOverflow),
	}
	for _, entry := range doc.Header.Fonts {
		chk.fonts[entry.Name] = true
	}

	// 1. The format version is one the reader understands.
	if doc.Header.SR != 0 && doc.Header.SR != Version {
		chk.fail("sr = %d, which this reader does not understand", doc.Header.SR)
	}

	// 2. The number of page lines equals the header's count.
	if doc.Header.Pages != 0 && doc.Header.Pages != len(doc.Pages) {
		chk.fail("header says %d pages, and there are %d", doc.Header.Pages, len(doc.Pages))
	}

	for _, page := range doc.Pages {
		chk.walk(page, page.Marks)
	}

	// The walk collects the outline names an xref may target, so xrefs are
	// only checkable once every page has been walked.
	for _, page := range doc.Pages {
		chk.walkXrefs(page, page.Marks)
	}

	// 4. Every data a font names exists in the header.
	for _, entry := range doc.Header.Fonts {
		if entry.ResolvedData == "" {
			continue
		}
		if _, ok := doc.Header.Data[entry.ResolvedData]; !ok {
			chk.fail("font %q names data %q, which the header does not carry",
				entry.Name, entry.ResolvedData)
		}
	}

	// 10. Outline level never jumps by more than one.
	for index := 1; index < len(chk.outlineLevels); index++ {
		if chk.outlineLevels[index] > chk.outlineLevels[index-1]+1 {
			chk.fail("an outline entry at level %d follows one at level %d",
				chk.outlineLevels[index], chk.outlineLevels[index-1])
		}
	}

	if len(chk.problems) == 0 {
		return nil
	}
	return fmt.Errorf("printout invariants violated:\n  %s", strings.Join(chk.problems, "\n  "))
}

// checker accumulates what one pass over a printout finds, both the
// violations themselves and the facts a later invariant is checked against.
type checker struct {
	doc           *Printout
	problems      []string
	fonts         map[string]bool
	allowOverflow bool
	outlineNames  map[string]bool
	outlineLevels []int
}

// fail records one violation.
func (chk *checker) fail(format string, args ...any) {
	chk.problems = append(chk.problems, fmt.Sprintf(format, args...))
}

// walk checks every mark in a list, descending into xrefs.
func (chk *checker) walk(page *Page, marks []Mark) {
	for _, mark := range marks {
		chk.checkMark(page, mark)
	}
}

// checkMark checks the invariants that apply to a single mark.
func (chk *checker) checkMark(page *Page, mark Mark) {
	box, hasBox := boxOf(mark)
	if hasBox {
		// 6. Every box has non-negative width and height.
		if box.Width < -tolerance || box.Height < -tolerance {
			chk.fail("page %d: a %s box has a negative extent: %+v",
				page.Number, mark.MarkKind(), box)
		}
		// 7. Every mark lies within its page's printable area.
		if !chk.allowOverflow {
			if msg := chk.doc.outsidePrintable(page, box); msg != "" {
				chk.fail("page %d: a %s mark %s", page.Number, mark.MarkKind(), msg)
			}
		}
	}

	switch typed := mark.(type) {
	case *Text:
		chk.checkText(page, typed, box)
	case *Image:
		chk.checkImage(page, typed)
	case *Barcode:
		chk.checkBarcode(page, typed, box)
	case *Outline:
		if typed.Name != "" {
			chk.outlineNames[typed.Name] = true
		}
		chk.outlineLevels = append(chk.outlineLevels, typed.Level)
	case *Xref:
		chk.walk(page, typed.Marks)
	}
}

// checkText checks the font a text mark names and the room its lines need.
func (chk *checker) checkText(page *Page, text *Text, box Box) {
	// 3. Every font a text mark names exists in the header.
	if len(chk.fonts) > 0 && !chk.fonts[text.Font] {
		chk.fail("page %d: a text mark names font %q, which the header does not carry",
			page.Number, text.Font)
	}
	// 8. A text mark has at least one line, and the lines fit the box to
	// within the rounding tolerance.
	if len(text.Lines) == 0 {
		chk.fail("page %d: a text mark has no lines", page.Number)
	}
	if used := float64(len(text.Lines)) * text.Leading; used > box.Height+tolerance {
		chk.fail("page %d: %d lines at leading %g need %g, and the box is %g high",
			page.Number, len(text.Lines), text.Leading, used, box.Height)
	}
}

// checkImage checks that an image names one source, and that it is present.
func (chk *checker) checkImage(page *Page, image *Image) {
	// 4. Every data an image names exists in the header.
	if image.Data != "" {
		if _, ok := chk.doc.Header.Data[image.Data]; !ok {
			chk.fail("page %d: an image names data %q, which the header does not carry",
				page.Number, image.Data)
		}
	}
	// 11. Every image mark carries exactly one of data and file.
	if (image.Data == "") == (image.File == "") {
		chk.fail("page %d: an image carries exactly one of data and file", page.Number)
	}
}

// checkBarcode checks a barcode's stripes against the box it was drawn in.
func (chk *checker) checkBarcode(page *Page, barcode *Barcode, box Box) {
	// 9. Stripe sums times module equal the box extent along the coding
	// direction.
	extent := box.Width
	if barcode.Vertical {
		extent = box.Height
	}
	total := 0
	switch {
	case barcode.Stripes != nil:
		for _, stripe := range barcode.Stripes {
			total += stripe
		}
	case len(barcode.Rows) > 0:
		for _, run := range barcode.Rows[0] {
			total += run
		}
	default:
		chk.fail("page %d: a barcode carries neither stripes nor rows", page.Number)
	}
	if want := float64(total) * barcode.Module; math.Abs(want-extent) > tolerance {
		chk.fail("page %d: %d modules at %g measure %g, and the box extent is %g",
			page.Number, total, barcode.Module, want, extent)
	}
}

// walkXrefs checks every xref in a list, descending into nested ones.
func (chk *checker) walkXrefs(page *Page, marks []Mark) {
	for _, mark := range marks {
		xref, ok := mark.(*Xref)
		if !ok {
			continue
		}
		// 5. Every outline xref has a target somewhere in the printout.
		if xref.Type == "outline" && !chk.outlineNames[xref.Target] {
			chk.fail("page %d: an outline xref targets %q, which no outline mark is named",
				page.Number, xref.Target)
		}
		chk.walkXrefs(page, xref.Marks)
	}
}

// outsidePrintable reports how a box leaves the page's printable area, or "".
func (doc *Printout) outsidePrintable(page *Page, box Box) string {
	geom := page.Geometry(doc.Header.Page)
	if geom.Width == 0 || geom.Height == 0 {
		return ""
	}
	left := geom.LeftMargin
	top := geom.TopMargin
	right := geom.Width - geom.RightMargin
	bottom := geom.Height - geom.BottomMargin

	switch {
	case box.Left < left-tolerance:
		return fmt.Sprintf("starts at x=%g, left of the %g pt margin", box.Left, left)
	case box.Top < top-tolerance:
		return fmt.Sprintf("starts at y=%g, above the %g pt margin", box.Top, top)
	case box.Left+box.Width > right+tolerance:
		return fmt.Sprintf("reaches x=%g, past the printable width of %g",
			box.Left+box.Width, right)
	case box.Top+box.Height > bottom+tolerance:
		return fmt.Sprintf("reaches y=%g, past the printable height of %g",
			box.Top+box.Height, bottom)
	}
	return ""
}

func boxOf(mark Mark) (Box, bool) {
	switch typed := mark.(type) {
	case *Text:
		return typed.Box, true
	case *Line:
		return typed.Box, true
	case *Rectangle:
		return typed.Box, true
	case *Image:
		return typed.Box, true
	case *Barcode:
		return typed.Box, true
	case *Xref:
		return typed.Box, true
	}
	return Box{}, false
}
