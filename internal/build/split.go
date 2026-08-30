package build

import (
	"sort"

	"github.com/a1s/sr/internal/barcodes"
	"github.com/a1s/sr/internal/fontres"
	"github.com/a1s/sr/internal/geom"
	"github.com/a1s/sr/internal/tmpl"
	"github.com/a1s/sr/printout"
)

// cutPoints lists the band-relative offsets a band may be cut at.
//
// A cut point is an offset no mark's span falls through, except that
// a stretch field may be cut on one of its line boundaries.
// An element's vertical span is the span of the marks it produced --
// its content box, not its resolved box. What cannot be cut is what is drawn.
func cutPoints(measured *measurement) []float64 {
	candidates := map[float64]bool{}
	for _, dft := range measured.drafts {
		candidates[dft.top] = true
		candidates[dft.bottom] = true
		for _, top := range dft.lineTops {
			candidates[top] = true
		}
	}
	candidates[0] = true
	candidates[measured.height] = true

	var out []float64
	for cut := range candidates {
		if cut <= 0 || cut >= measured.height {
			continue
		}
		if blocked(measured, cut) {
			continue
		}
		out = append(out, cut)
	}
	sort.Float64s(out)
	return out
}

// blocked reports whether the cut falls through a mark.
func blocked(measured *measurement, cut float64) bool {
	for _, dft := range measured.drafts {
		if cut <= dft.top+geom.Tolerance || cut >= dft.bottom-geom.Tolerance {
			continue
		}
		// Strictly inside the mark's span. A stretch field permits a cut
		// on a line boundary; everything else blocks.
		ok := false
		for _, line := range dft.lineTops {
			if abs(line-cut) <= geom.Tolerance {
				ok = true
				break
			}
		}
		if !ok {
			return true
		}
		// A deferred value is written into one mark when its scope ends,
		// so a field carrying one cannot be divided between two marks.
		if len(defersOf(measured, dft)) > 0 {
			return true
		}
	}
	return false
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

// dividesContent reports whether the cut leaves marks on both sides.
//
// A cut with all the band's marks on one side of it moves whitespace
// to another frame and nothing else, and ejecting the band whole
// is better in every case of that shape.
func dividesContent(measured *measurement, cut float64) bool {
	above, below := false, false
	for _, dft := range measured.drafts {
		if dft.bottom <= cut+geom.Tolerance {
			above = true
			continue
		}
		if dft.top >= cut-geom.Tolerance {
			below = true
			continue
		}
		// A split field straddles the cut, so both sides carry marks.
		above, below = true, true
	}
	return above && below
}

// orphansWidowsHold reports whether the cut leaves enough lines
// on each side of every split field.
//
// A field with fewer than orphans+widows lines permits no internal cut
// and blocks like an unsplittable element.
func orphansWidowsHold(measured *measurement, sec *tmpl.Section, cut float64) bool {
	for _, dft := range measured.drafts {
		if len(dft.lineTops) == 0 || dft.text == nil {
			continue
		}
		if cut <= dft.top+geom.Tolerance || cut >= dft.bottom-geom.Tolerance {
			continue
		}
		above := 0
		for _, line := range dft.lineTops {
			if line <= cut+geom.Tolerance {
				above++
			}
		}
		below := len(dft.text.Lines) - above
		if above < sec.Orphans || below < sec.Widows {
			return false
		}
	}
	return true
}

// legalSplit returns the greatest legal split point
// not exceeding the space available.
func legalSplit(measured *measurement, sec *tmpl.Section, available float64) (float64, bool) {
	best, found := 0.0, false
	for _, cut := range cutPoints(measured) {
		if !geom.Fits(cut, available) {
			break
		}
		if !dividesContent(measured, cut) {
			continue
		}
		if !orphansWidowsHold(measured, sec, cut) {
			continue
		}
		best, found = cut, true
	}
	return best, found
}

// lastCutPoint returns the greatest cut point that fits,
// giving up every split preference. It is the last resort
// for a band too tall for any frame: a blank tail beats not making progress.
func lastCutPoint(measured *measurement, available float64) (float64, bool) {
	best, found := 0.0, false
	for _, cut := range cutPoints(measured) {
		if !geom.Fits(cut, available) {
			break
		}
		best, found = cut, true
	}
	return best, found
}

// splitAt divides a measured band at the cut.
//
// The head takes elements wholly above the cut as they are,
// and split fields their leading lines with lastLineJustified set
// so the renderer does not treat a continued line as a paragraph end.
// The tail carries the remaining lines and its elements are re-placed
// from the tail's top.
func splitAt(measured *measurement, cut float64) (head, tail *measurement) {
	head = &measurement{
		section: measured.section,
		printed: true,
		height:  cut,
		outline: measured.outline,
		left:    measured.left,
	}
	tail = &measurement{
		section: measured.section,
		printed: true,
		left:    measured.left,
	}

	tailBottom := 0.0
	for _, dft := range measured.drafts {
		switch {
		case dft.bottom <= cut+geom.Tolerance:
			head.drafts = append(head.drafts, dft)
			head.defers = append(head.defers, defersOf(measured, dft)...)
		case dft.top >= cut-geom.Tolerance:
			shifted, clones := shiftDraft(dft, -cut)
			tail.drafts = append(tail.drafts, shifted)
			tail.defers = append(tail.defers, shiftDefers(measured, dft, shifted, clones)...)
			if shifted.bottom > tailBottom {
				tailBottom = shifted.bottom
			}
		default:
			// A stretch field cut on a line boundary.
			above := 0
			for _, line := range dft.lineTops {
				if line <= cut+geom.Tolerance {
					above++
				}
			}
			headText := *dft.text
			headText.Lines = append([]string(nil), dft.text.Lines[:above]...)
			headText.Box.Height = geom.Round(float64(above) * dft.leading)
			headText.LastLineJustified = true
			head.drafts = append(head.drafts, &draft{
				mark:    &headText,
				top:     dft.top,
				bottom:  geom.Round(dft.top + headText.Box.Height),
				leading: dft.leading,
				text:    &headText,
			})

			tailText := *dft.text
			tailText.Lines = append([]string(nil), dft.text.Lines[above:]...)
			tailText.Box.Top = 0
			tailText.Box.Height = geom.Round(float64(len(tailText.Lines)) * dft.leading)
			tailText.LastLineJustified = false
			tailDraft := &draft{
				mark: &tailText, top: 0, bottom: tailText.Box.Height,
				leading: dft.leading, text: &tailText,
			}
			for index := 1; index < len(tailText.Lines); index++ {
				tailDraft.lineTops = append(
					tailDraft.lineTops, geom.Round(float64(index)*dft.leading))
			}
			tail.drafts = append(tail.drafts, tailDraft)
			if tailDraft.bottom > tailBottom {
				tailBottom = tailDraft.bottom
			}
		}
	}
	tail.height = geom.Round(tailBottom)
	if tail.section.Height.Set && tail.section.Height.Value > tail.height {
		tail.height = tail.section.Height.Value
	}
	return head, tail
}

// defersOf lists the deferrals that patch a mark this draft carries.
//
// Membership is by mark, not by draft identity. An element inside an xref
// has its deferrals lifted into the parent slot while its mark stays nested
// in the xref's, so its draft is never one of the band's -- matching on the
// draft found nothing, and the deferral was dropped from whichever side
// of the cut it belonged to, head as well as tail.
func defersOf(measured *measurement, dft *draft) []*deferral {
	carried := map[printout.Mark]bool{}
	for _, mark := range marksOf(dft.mark) {
		carried[mark] = true
	}
	var out []*deferral
	for _, def := range measured.defers {
		if target := def.patches(); target != nil && carried[target] {
			out = append(out, def)
		}
	}
	return out
}

// marksOf lists a mark and everything nested inside it.
func marksOf(mark printout.Mark) []printout.Mark {
	out := []printout.Mark{mark}
	if xref, ok := mark.(*printout.Xref); ok {
		for _, inner := range xref.Marks {
			out = append(out, marksOf(inner)...)
		}
	}
	return out
}

// patches is the mark this deferral writes its resolved value into.
func (def *deferral) patches() printout.Mark {
	switch {
	case def.text != nil:
		return def.text
	case def.barcode != nil:
		return def.barcode
	}
	return nil
}

// shiftDefers re-points a draft's deferrals at the copy that moved into the tail.
//
// A deferral holds the mark it will patch. shiftDraft clones the mark, so the
// tail commits the clone while the original is discarded -- and a deferral
// left pointing at the original would write the resolved value into a mark
// the printout no longer carries, leaving the placeholder on the page forever.
func shiftDefers(
	measured *measurement,
	dft, shifted *draft,
	clones map[printout.Mark]printout.Mark,
) []*deferral {
	var out []*deferral
	for _, def := range defersOf(measured, dft) {
		moved := *def
		moved.draft = shifted
		if moved.text != nil {
			if text, ok := clones[moved.text].(*printout.Text); ok {
				moved.text = text
			}
		}
		if moved.barcode != nil {
			if barcode, ok := clones[moved.barcode].(*printout.Barcode); ok {
				moved.barcode = barcode
			}
		}
		out = append(out, &moved)
	}
	return out
}

// shiftDraft copies a draft down by dy, and reports which clone replaced which
// original so that a deferral can be re-pointed at the copy the tail commits.
func shiftDraft(dft *draft, dy float64) (*draft, map[printout.Mark]printout.Mark) {
	out := *dft
	out.top = geom.Round(dft.top + dy)
	out.bottom = geom.Round(dft.bottom + dy)
	out.lineTops = nil
	for _, line := range dft.lineTops {
		out.lineTops = append(out.lineTops, geom.Round(line+dy))
	}
	clones := map[printout.Mark]printout.Mark{}
	out.mark = cloneShifted(dft.mark, dy, clones)
	if text, ok := out.mark.(*printout.Text); ok {
		out.text = text
	}
	return &out, clones
}

func cloneShifted(
	mark printout.Mark,
	dy float64,
	clones map[printout.Mark]printout.Mark,
) printout.Mark {
	switch typed := mark.(type) {
	case *printout.Text:
		text := *typed
		text.Box.Top = geom.Round(text.Box.Top + dy)
		clones[mark] = &text
		return &text
	case *printout.Line:
		line := *typed
		line.Box.Top = geom.Round(line.Box.Top + dy)
		clones[mark] = &line
		return &line
	case *printout.Rectangle:
		rect := *typed
		rect.Box.Top = geom.Round(rect.Box.Top + dy)
		clones[mark] = &rect
		return &rect
	case *printout.Image:
		image := *typed
		image.Box.Top = geom.Round(image.Box.Top + dy)
		clones[mark] = &image
		return &image
	case *printout.Barcode:
		barcode := *typed
		barcode.Box.Top = geom.Round(barcode.Box.Top + dy)
		clones[mark] = &barcode
		return &barcode
	case *printout.Xref:
		xref := *typed
		xref.Box.Top = geom.Round(xref.Box.Top + dy)
		xref.Marks = nil
		for _, inner := range typed.Marks {
			xref.Marks = append(xref.Marks, cloneShifted(inner, dy, clones))
		}
		clones[mark] = &xref
		return &xref
	}
	return mark
}

// fontres_Wrap is the wrapper the deferral path uses.
//
// Named so that the import stays visible where the function is read.
func fontres_Wrap(face *fontres.Face, text string, width float64) []string {
	return fontres.Wrap(face, text, width)
}

// encodeDeferredBarcode re-encodes a deferred barcode once its value is known.
func encodeDeferredBarcode(
	deferred *deferral,
	text string,
) (*barcodes.Symbol, barcodes.Metrics, error) {
	sym, err := barcodes.Encode(deferred.barcode.Type, text)
	if err != nil {
		return nil, barcodes.Metrics{}, err
	}
	boxLength, boxCross := deferred.barcode.Box.Width, deferred.barcode.Box.Height
	if deferred.vertical {
		boxLength, boxCross = deferred.barcode.Box.Height, deferred.barcode.Box.Width
	}
	return sym, barcodes.Measure(sym, deferred.module, deferred.grow, boxLength, boxCross), nil
}
