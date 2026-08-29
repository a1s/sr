package build

import (
	"fmt"

	"github.com/a1s/sr/internal/expr"
	"github.com/a1s/sr/internal/fontres"
	"github.com/a1s/sr/internal/geom"
	"github.com/a1s/sr/internal/segment"
	"github.com/a1s/sr/internal/tmpl"
	"github.com/a1s/sr/printout"
	"go.starlark.net/starlark"
)

// measurement is a band laid out into a scratch context: resolved, wrapped,
// floated, and sized, with nothing mutated and nothing emitted.
type measurement struct {
	section *tmpl.Section
	// printed is false when the band's printwhen suppressed it,
	// in which case it produces no marks and contributes no height.
	printed bool
	height  float64
	drafts  []*draft
	outline *printout.Outline
	defers  []*deferral
}

// draft is one mark at band-relative coordinates, with what splitting needs.
type draft struct {
	mark printout.Mark
	// top and bottom are the span of what is drawn -- the content box,
	// not the resolved box. What cannot be cut is what is drawn.
	top, bottom float64
	// lineTops are the band-relative offsets a stretch field may be cut at.
	lineTops []float64
	leading  float64
	// text is the field's mark, when this draft is a splittable stretch field.
	text *printout.Text
}

// deferral is a field or barcode whose expr is evaluated when a scope ends.
type deferral struct {
	scope    string
	program  *expr.Program
	snapshot map[string]starlark.Value
	format   string
	node     string

	// what to patch, and what to re-measure against
	text     *printout.Text
	barcode  *printout.Barcode
	face     *fontres.Face
	boxWidth float64
	reserved float64
	stretch  bool
	vertical bool
	grow     bool
	module   float64
	kind     string
	draft    *draft
}

// elementSlot is one element's intermediate state while a band is measured.
type elementSlot struct {
	el      tmpl.Element
	dropped bool

	style resolvedStyle
	face  *fontres.Face

	left, width float64

	// declaredTop is the element's top as the template declares it,
	// which is what the floating partial order is built from.
	declaredTop float64
	// ownHeight is the height the element brings itself:
	// a declared height, or a content height.
	ownHeight float64
	hasOwn    bool
	// anchored means the element's vertical extent comes from the band's bottom
	// edge, so it is resolved after the band's height is settled.
	anchored bool

	top    float64
	height float64

	drafts []*draft
	defers []*deferral
}

// measureSection lays a band out without mutating anything.
func (eng *engine) measureSection(
	sec *tmpl.Section,
	scopes styleScopes,
	fr *frame,
	available float64,
) (*measurement, error) {
	return eng.measureDecided(sec, scopes, fr, available, nil)
}

// measureDecided is measureSection with the band's printwhen already answered.
//
// Placing a band asks its printwhen once, at bandPrints, and passes the answer
// here. It has to be one answer: the question is asked before the band's
// negative-seq subreports run, those may eject, and a printwhen reading
// VERTICAL_SPACE or PAGE_NUMBER would then answer differently by the time
// the band itself is measured -- which is how a suppressed row's subreport
// came to print without it. A nil answer means ask, which is what a header, a
// footer and the keep-together lookahead do, each being a measurement of its own.
func (eng *engine) measureDecided(
	sec *tmpl.Section,
	scopes styleScopes,
	fr *frame,
	available float64,
	prints *bool,
) (*measurement, error) {
	measured := &measurement{section: sec}
	if sec == nil {
		return measured, nil
	}

	eng.ctx.verticalPosition = geom.Round(fr.fillY - fr.top)
	eng.ctx.verticalSpace = geom.Round(available)

	// 1. The band's printwhen. A suppressed band is dropped whole.
	var ok bool
	if prints != nil {
		ok = *prints
	} else {
		var err error
		if ok, err = eng.ctx.truth(sec.PrintWhen); err != nil {
			return nil, err
		}
	}
	if !ok {
		return measured, nil
	}
	measured.printed = true

	// 2. The band's style.
	saved := eng.currentScopes
	eng.currentScopes = scopes
	defer func() { eng.currentScopes = saved }()
	bandStyle, err := scopes.resolve(eng.ctx)
	if err != nil {
		return nil, err
	}

	// 3. The band's geometry against the frame. Its width is the frame's;
	// an explicit height is a minimum, settled with the content at step 6.
	box := geom.Box{Left: fr.left, Top: 0, Width: fr.width}

	// 4. For each element, resolve its content.
	slots := make([]*elementSlot, len(sec.Elements))
	for index, el := range sec.Elements {
		slot, err := eng.measureElement(el, scopes, bandStyle, box, measured)
		if err != nil {
			return nil, err
		}
		slots[index] = slot
	}

	// 5. Floating elements.
	if err := placeFloats(slots); err != nil {
		return nil, err
	}

	// 6. The band's height: the greater of the declared minimum
	// and the lowest bottom edge any participating element produced.
	height := 0.0
	if sec.Height.Set {
		height = sec.Height.Value
	}
	for _, slot := range slots {
		if slot.dropped || slot.anchored {
			continue
		}
		if bottom := geom.Round(slot.top + slot.height); bottom > height {
			height = bottom
		}
	}
	measured.height = geom.Round(height)

	// Anchored elements are resolved against the height the others produced.
	for _, slot := range slots {
		if slot.dropped || !slot.anchored {
			continue
		}
		top, extent, err := slot.el.Base().Vert.Resolve(0, measured.height)
		if err != nil {
			return nil, eng.nodeError(slot.el.Base().Node, err)
		}
		slot.top, slot.height = top, extent
	}

	// 7. Emit in document order, which is paint order.
	for _, slot := range slots {
		if slot.dropped {
			continue
		}
		if err := eng.emit(slot, measured); err != nil {
			return nil, err
		}
		// An element that produced marks below the band's declared height
		// grows it -- a floating stretch field, say, whose text ran long.
		for _, dft := range slot.drafts {
			if dft.bottom > measured.height {
				measured.height = geom.Round(dft.bottom)
			}
		}
		measured.drafts = append(measured.drafts, slot.drafts...)
	}

	// The band's outline entry, first-win: a band emits at most one
	// each time it prints.
	for _, outline := range sec.Outlines {
		ok, err := eng.ctx.truth(outline.When)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		title, err := eng.ctx.eval(outline.Title)
		if err != nil {
			return nil, err
		}
		mark := printout.NewOutline()
		mark.Title = expr.Str(title)
		mark.Level = outline.Level
		mark.Closed = outline.Closed
		mark.Left = fr.left
		if outline.Name != nil {
			name, err := eng.ctx.eval(outline.Name)
			if err != nil {
				return nil, err
			}
			mark.Name = expr.Str(name)
		}
		measured.outline = mark
		break
	}

	for _, slot := range slots {
		measured.defers = append(measured.defers, slot.defers...)
	}
	return measured, nil
}

// measureElement runs steps 4.1 to 4.4 for one element.
func (eng *engine) measureElement(
	el tmpl.Element,
	scopes styleScopes,
	bandStyle resolvedStyle,
	box geom.Box,
	measured *measurement,
) (*elementSlot, error) {
	base := el.Base()
	slot := &elementSlot{el: el}

	// 4.1 printwhen, tested first so that everything else is skipped
	// for anything invisible.
	ok, err := eng.ctx.truth(base.PrintWhen)
	if err != nil {
		return nil, err
	}
	if !ok {
		slot.dropped = true
		return slot, nil
	}

	// 4.2 style, by the same outward walk starting at the element's own.
	style, err := scopes.with(base.Styles).resolve(eng.ctx)
	if err != nil {
		return nil, err
	}
	if !style.HasFont {
		style.Font, style.HasFont = bandStyle.Font, bandStyle.HasFont
	}
	if !style.HasColor {
		style.Color, style.HasColor = bandStyle.Color, bandStyle.HasColor
	}
	if !style.HasBg {
		style.BgColor, style.HasBg = bandStyle.BgColor, bandStyle.HasBg
	}
	slot.style = style
	if style.HasFont {
		face, err := eng.face(style.Font)
		if err != nil {
			return nil, err
		}
		slot.face = face
	}

	// 4.3 horizontal geometry, which never depends on the band's height.
	left, width, err := base.Horiz.Resolve(box.Left, box.Width)
	if err != nil {
		return nil, eng.nodeError(base.Node, err)
	}
	slot.left, slot.width = left, width

	// Decide whether the element's vertical extent is its own or the band's.
	slot.anchored = base.Vert.Far.Set || !base.Vert.Size.Set
	slot.declaredTop = 0
	if base.Vert.Near.Set {
		slot.declaredTop = base.Vert.Near.Value
	}
	if base.Vert.Size.Set {
		slot.ownHeight, slot.hasOwn = base.Vert.Size.Value, true
	}
	// maxheight clamps a declared height here, at step 4.3 of doc/layout.md,
	// the same way Horiz.Resolve above has already clamped maxwidth.
	if base.Vert.Max.Set && slot.ownHeight > base.Vert.Max.Value {
		slot.ownHeight = geom.Round(base.Vert.Max.Value)
	}

	// 4.4 content. An element with a content height of its own
	// stops being anchored: a stretch field has its wrapped text,
	// a barcode its symbol, a grow image its bitmap.
	if err := eng.contentHeight(slot); err != nil {
		return nil, err
	}
	if slot.hasOwn && !base.Vert.Far.Set {
		slot.anchored = false
	}
	if !slot.anchored {
		slot.top = slot.declaredTop
		slot.height = slot.ownHeight
		clampOwnHeight(slot, base)
	}
	return slot, nil
}

// clampOwnHeight applies maxheight to a height that came from content.
//
// Only a field is clamped. A barcode's box extent along the coding direction
// is its stripe count times its module, which printout invariant 9 checks,
// so shrinking the box would produce a printout that fails its own validation;
// a grow image is drawn at natural size with no crop, so a smaller box would only
// make it spill. A field has no such tie: emitField trims its lines to the box.
func clampOwnHeight(slot *elementSlot, base *tmpl.Common) {
	if !base.Vert.Max.Set {
		return
	}
	if _, ok := slot.el.(*tmpl.Field); !ok {
		return
	}
	if slot.height > base.Vert.Max.Value {
		slot.height = geom.Round(base.Vert.Max.Value)
	}
}

// contentHeight gives an element the height it brings itself, where it has one.
func (eng *engine) contentHeight(slot *elementSlot) error {
	switch el := slot.el.(type) {
	case *tmpl.Field:
		if !el.Stretch {
			return nil
		}
		if slot.face == nil {
			// The same condition emitField reports, reached earlier:
			// measuring wrapped text needs a face, and a nil one
			// panicked here before emit could give the diagnostic.
			return fmt.Errorf("%s: no font is in scope; give the layout a style with a font",
				el.Node.Path())
		}
		text, err := eng.fieldText(el, slot)
		if err != nil {
			return err
		}
		lines := fontres.Wrap(slot.face, text, slot.width)
		height := fontres.TextHeight(slot.face, len(lines))
		if height > slot.ownHeight {
			slot.ownHeight = height
		}
		slot.hasOwn = true
	case *tmpl.Barcode:
		sym, metrics, err := eng.barcode(el, slot)
		if err != nil {
			return err
		}
		_ = sym
		height := metrics.Cross
		if el.Vertical {
			height = metrics.Length
		}
		if height > slot.ownHeight {
			slot.ownHeight = height
		}
		slot.hasOwn = true
	case *tmpl.Image:
		if el.Scale != tmpl.ScaleGrow {
			return nil
		}
		img, err := eng.image(el)
		if err != nil {
			return err
		}
		if height := img.height; height > slot.ownHeight {
			slot.ownHeight = height
		}
		slot.hasOwn = true
	}
	return nil
}

// placeFloats resolves the vertical position of every floating element.
//
// The partial order is built from the declared boxes, so it depends
// on the template and not on the data: the same template floats things
// in the same order for every record. Measured heights propagate positions
// along it, and never change which element precedes which.
func placeFloats(slots []*elementSlot) error {
	var index []int
	for slotIndex, slot := range slots {
		if slot.dropped || slot.anchored {
			continue
		}
		if slot.height < 0 {
			continue
		}
		index = append(index, slotIndex)
	}
	anyFloat := false
	for _, slotIndex := range index {
		if slots[slotIndex].el.Base().Float {
			anyFloat = true
		}
	}
	if !anyFloat {
		return nil
	}

	// The partial order and the gaps come from the declared boxes,
	// so they depend on the template and not on the data:
	// the same template floats things in the same order for every record.
	// Measured extents propagate positions along the order,
	// and never change which element precedes which.
	segs := make([]segment.Segment, len(index))
	for order, slotIndex := range index {
		declared := 0.0
		if size := slots[slotIndex].el.Base().Vert.Size; size.Set {
			declared = size.Value
		}
		segs[order] = segment.Segment{Start: slots[slotIndex].declaredTop, Extent: declared}
	}
	deps := make([][]int, len(index))
	for order, slotIndex := range index {
		if !slots[slotIndex].el.Base().Float {
			continue
		}
		for otherOrder, other := range index {
			if otherOrder == order {
				continue
			}
			if slots[other].el.Base().Float {
				// A floating element precedes another only when
				// it starts earlier, that settles the case
				// of a zero-height floater.
				if segs[otherOrder].Start < segs[order].Start {
					deps[order] = append(deps[order], otherOrder)
				}
				continue
			}
			if segment.Precedes(segs[otherOrder], segs[order]) {
				deps[order] = append(deps[order], otherOrder)
			}
		}
	}
	deps = segment.Reduce(deps)

	extents := make([]float64, len(index))
	for order, slotIndex := range index {
		extents[order] = slots[slotIndex].height
	}
	starts := segment.New(segs, deps).Solve(extents)
	for order, slotIndex := range index {
		if slots[slotIndex].el.Base().Float {
			slots[slotIndex].top = geom.Round(starts[order])
		}
	}
	return nil
}

// nodeError names the template node a failure came from.
func (eng *engine) nodeError(node nodeLike, err error) error {
	if node == nil {
		return err
	}
	return fmt.Errorf("%s: %w", node.Path(), err)
}

type nodeLike interface{ Path() string }

// alignedText positions a run of lines inside a resolved box.
func alignedText(slot *elementSlot, lines []string, valign geom.VAlign) (geom.Box, float64) {
	leading := slot.face.Leading()
	height := fontres.TextHeight(slot.face, len(lines))
	top := geom.AlignV(slot.top, slot.height, height, valign)
	return geom.Box{Left: slot.left, Top: top, Width: slot.width, Height: height}, leading
}

// effectiveAlign combines align and halign for a field.
//
// The printout carries one box and one alignment, and the box is the field's
// resolved box -- that is what lets `align="right"` right-align a number
// in a column. So `halign`, which aligns content inside the box, has nothing
// else to act on and supplies the alignment when `align` was not written.
func effectiveAlign(field *tmpl.Field, halign geom.HAlign) tmpl.TextAlign {
	if field.HasAlign {
		return field.Align
	}
	switch halign {
	case geom.HCenter:
		return tmpl.AlignCenter
	case geom.HRight:
		return tmpl.AlignRight
	}
	return tmpl.AlignLeft
}

func trimToBox(lines []string, leading, height float64) []string {
	if leading <= 0 {
		return lines
	}
	fit := int(geom.Round(height+geom.Tolerance) / leading)
	if fit < 1 {
		fit = 1
	}
	if len(lines) > fit {
		return lines[:fit]
	}
	return lines
}
