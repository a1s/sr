package build

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/a1s/sr/internal/barcodes"
	"github.com/a1s/sr/internal/expr"
	"github.com/a1s/sr/internal/fontres"
	"github.com/a1s/sr/internal/geom"
	"github.com/a1s/sr/internal/tmpl"
	"github.com/a1s/sr/printout"
)

// fieldText produces a field's text: the expression's result
// put through format, or the literal text, or a data blob.
//
// For a deferred field it produces the placeholder,
// which is what the band is measured from.
func (eng *engine) fieldText(field *tmpl.Field, slot *elementSlot) (string, error) {
	if field.Deferred() {
		return eng.placeholderText(&field.Content)
	}
	return eng.contentText(&field.Content)
}

func (eng *engine) placeholderText(content *tmpl.Content) (string, error) {
	switch {
	case content.HasText:
		return content.Text, nil
	case content.Data != "":
		return eng.blobText(content.Data)
	}
	return "", nil
}

func (eng *engine) contentText(content *tmpl.Content) (string, error) {
	switch {
	case content.Expr != nil:
		value, err := eng.ctx.eval(content.Expr)
		if err != nil {
			return "", err
		}
		return expr.Format(content.Format, expr.FormatArgs(value))
	case content.HasText:
		return content.Text, nil
	case content.Data != "":
		return eng.blobText(content.Data)
	}
	return "", nil
}

// emit turns a resolved element into marks, in document order.
func (eng *engine) emit(slot *elementSlot, measured *measurement) error {
	switch el := slot.el.(type) {
	case *tmpl.Field:
		return eng.emitField(el, slot, measured)
	case *tmpl.Line:
		return eng.emitLine(el, slot, measured)
	case *tmpl.Rectangle:
		return eng.emitRectangle(el, slot, measured)
	case *tmpl.Image:
		return eng.emitImage(el, slot, measured)
	case *tmpl.Barcode:
		return eng.emitBarcode(el, slot, measured)
	case *tmpl.Xref:
		return eng.emitXref(el, slot, measured)
	}
	return fmt.Errorf("unknown element %T", slot.el)
}

func (eng *engine) emitField(field *tmpl.Field, slot *elementSlot, measured *measurement) error {
	if slot.face == nil {
		return fmt.Errorf("%s: no font is in scope; give the layout a style with a font",
			field.Node.Path())
	}
	text, err := eng.fieldText(field, slot)
	if err != nil {
		return err
	}
	lines := fontres.Wrap(slot.face, text, slot.width)
	leading := slot.face.Leading()
	// A stretch field grows to its text unless maxheight capped that growth --
	// then the box is the limit again.
	if !field.Stretch || field.Vert.Max.Set {
		lines = trimToBox(lines, leading, slot.height)
	}
	box, _ := alignedText(slot, lines, slot.el.Base().VAlign)

	mark := printout.NewText()
	mark.Box = printout.Box{Left: box.Left, Top: box.Top, Width: box.Width, Height: box.Height}
	mark.Font = eng.fontName(slot.style.Font)
	mark.Color = slot.style.Color.Hex()
	mark.Align = effectiveAlign(field, slot.el.Base().HAlign).String()
	mark.Leading = leading
	mark.Lines = lines

	dft := &draft{
		mark:    mark,
		top:     box.Top,
		bottom:  geom.Round(box.Top + box.Height),
		leading: leading,
		text:    mark,
	}
	if field.Stretch && len(lines) > 1 {
		// A stretch field permits cuts between its lines.
		for index := 1; index < len(lines); index++ {
			dft.lineTops = append(dft.lineTops, geom.Round(box.Top+float64(index)*leading))
		}
	}
	slot.drafts = append(slot.drafts, dft)
	eng.noteMissingGlyphs(slot.face, field.Node.Path())

	if field.Deferred() {
		// The engine evaluates nothing here: it snapshots the value
		// of every name the expression references except FINAL,
		// which it does not bind yet.
		snap, err := eng.ctx.snapshot(field.Expr)
		if err != nil {
			return err
		}
		slot.defers = append(slot.defers, &deferral{
			scope:    field.EvalTime,
			program:  field.Expr,
			snapshot: snap,
			format:   field.Format,
			node:     field.Node.Path(),
			text:     mark,
			face:     slot.face,
			boxWidth: slot.width,
			reserved: box.Height,
			stretch:  field.Stretch,
			kind:     "field",
			draft:    dft,
			halign:   slot.el.Base().HAlign,
			valign:   slot.el.Base().VAlign,
		})
	}
	return nil
}

func (eng *engine) emitLine(line *tmpl.Line, slot *elementSlot, measured *measurement) error {
	mark := printout.NewLine()
	mark.Box = printout.Box{Left: slot.left, Top: slot.top, Width: slot.width, Height: slot.height}
	mark.Width = line.PenWidth
	mark.Dash = line.Dash.String()
	mark.Color = slot.style.Color.Hex()
	mark.Backslant = line.Backslant
	slot.drafts = append(slot.drafts, &draft{
		mark: mark, top: slot.top, bottom: geom.Round(slot.top + slot.height),
	})
	return nil
}

func (eng *engine) emitRectangle(rect *tmpl.Rectangle, slot *elementSlot, measured *measurement) error {
	mark := printout.NewRectangle()
	mark.Box = printout.Box{Left: slot.left, Top: slot.top, Width: slot.width, Height: slot.height}
	mark.Width = rect.PenWidth
	mark.Dash = rect.Dash.String()
	mark.Radius = rect.Radius
	// The two halves switch off independently: stroke=#false suppresses the
	// outline, opaque=#false the fill.
	if rect.Stroke && slot.style.HasColor {
		stroke := slot.style.Color.Hex()
		mark.Stroke = &stroke
	}
	if rect.Opaque && slot.style.HasBg {
		fill := slot.style.BgColor.Hex()
		mark.Fill = &fill
	}
	slot.drafts = append(slot.drafts, &draft{
		mark: mark, top: slot.top, bottom: geom.Round(slot.top + slot.height),
	})
	return nil
}

type decodedImage struct {
	data          []byte
	format        string
	width, height float64
	file          string
}

func (eng *engine) emitImage(im *tmpl.Image, slot *elementSlot, measured *measurement) error {
	img, err := eng.image(im)
	if err != nil {
		return err
	}
	mark := printout.NewImage()
	mark.Type = img.format

	boxX, boxY := slot.left, slot.top
	boxW, boxH := slot.width, slot.height

	switch im.Scale {
	case tmpl.ScaleFill:
		width, height := boxW, boxH
		if im.Proportional && img.width > 0 && img.height > 0 {
			ratio := img.width / img.height
			if boxW/boxH > ratio {
				width = geom.Round(boxH * ratio)
			} else {
				height = geom.Round(boxW / ratio)
			}
		}
		left := geom.AlignH(boxX, boxW, width, slot.el.Base().HAlign)
		top := geom.AlignV(boxY, boxH, height, slot.el.Base().VAlign)
		mark.Box = printout.Box{Left: left, Top: top, Width: width, Height: height}
	case tmpl.ScaleGrow:
		// The box expands wherever the image exceeds it, so the image is drawn
		// at natural size, neither scaled nor clipped.
		left := geom.AlignH(boxX, maxf(boxW, img.width), img.width, slot.el.Base().HAlign)
		mark.Box = printout.Box{Left: left, Top: boxY, Width: img.width, Height: img.height}
	default: // cut
		// Drawn at natural size and clipped to the box; the retained region
		// becomes the mark's crop.
		width, height := img.width, img.height
		cropW, cropH := width, height
		if width > boxW {
			cropW = boxW
			width = boxW
		}
		if height > boxH {
			cropH = boxH
			height = boxH
		}
		left := geom.AlignH(boxX, boxW, width, slot.el.Base().HAlign)
		top := geom.AlignV(boxY, boxH, height, slot.el.Base().VAlign)
		mark.Box = printout.Box{Left: left, Top: top, Width: width, Height: height}
		if cropW < img.width || cropH < img.height {
			mark.Crop = &printout.Box{Left: 0, Top: 0, Width: cropW, Height: cropH}
		}
	}

	switch {
	case !im.Embed:
		mark.SetFile(img.file)
	case im.Data != "":
		// An image reading a template data node keeps that node's name,
		// unless another template in the document has taken it already.
		published, err := eng.publishBlob(im.Data)
		if err != nil {
			return err
		}
		mark.Data = published
	default:
		mark.Data = eng.addBlob(img)
	}
	slot.drafts = append(slot.drafts, &draft{
		mark: mark, top: mark.Box.Top, bottom: geom.Round(mark.Box.Top + mark.Box.Height),
	})
	return nil
}

func maxf(one, two float64) float64 {
	if one > two {
		return one
	}
	return two
}

func (eng *engine) emitBarcode(barcode *tmpl.Barcode, slot *elementSlot, measured *measurement) error {
	sym, metrics, err := eng.barcode(barcode, slot)
	if err != nil {
		return err
	}
	width, height := metrics.Cross, metrics.Length
	if !barcode.Vertical {
		width, height = metrics.Length, metrics.Cross
	}
	left := geom.AlignH(slot.left, slot.width, width, slot.el.Base().HAlign)
	top := geom.AlignV(slot.top, slot.height, height, slot.el.Base().VAlign)

	mark := printout.NewBarcode()
	mark.Box = printout.Box{Left: left, Top: top, Width: width, Height: height}
	mark.Type = barcode.Type
	mark.Value = sym.Value
	mark.Module = metrics.Module
	mark.Vertical = barcode.Vertical
	// The mark is born with black ink, so only a template
	// that named a colour of its own changes anything here.
	if barcode.Ink != nil {
		mark.Ink = barcode.Ink.Hex()
	}
	if barcode.Paper != nil {
		paper := barcode.Paper.Hex()
		mark.Paper = &paper
	}
	mark.Stripes = sym.Stripes
	mark.Rows = sym.Rows

	dft := &draft{mark: mark, top: top, bottom: geom.Round(top + height)}
	slot.drafts = append(slot.drafts, dft)

	if barcode.Deferred() {
		snap, err := eng.ctx.snapshot(barcode.Expr)
		if err != nil {
			return err
		}
		slot.defers = append(slot.defers, &deferral{
			scope:    barcode.EvalTime,
			program:  barcode.Expr,
			snapshot: snap,
			format:   barcode.Format,
			node:     barcode.Node.Path(),
			barcode:  mark,
			reserved: height,
			vertical: barcode.Vertical,
			grow:     barcode.Grow,
			module:   barcode.Module,
			kind:     "barcode",
			draft:    dft,
			boxWidth: slot.width,
			halign:   slot.el.Base().HAlign,
			valign:   slot.el.Base().VAlign,
		})
	}
	return nil
}

func (eng *engine) emitXref(xref *tmpl.Xref, slot *elementSlot, measured *measurement) error {
	target, err := eng.ctx.eval(xref.Target)
	if err != nil {
		return err
	}
	mark := printout.NewXref()
	mark.Box = printout.Box{Left: slot.left, Top: slot.top, Width: slot.width, Height: slot.height}
	mark.Type = xref.Type
	mark.Target = expr.Str(target)
	if xref.Caption != nil {
		caption, err := eng.ctx.eval(xref.Caption)
		if err != nil {
			return err
		}
		mark.Caption = expr.Str(caption)
	}

	// An xref is a container of elements and is measured as one: its children
	// resolve against its box, and their marks come out in page coordinates
	// so a renderer can flatten them in one pass.
	inner := geom.Box{Left: slot.left, Top: slot.top, Width: slot.width, Height: slot.height}
	slots := make([]*elementSlot, len(xref.Elements))
	for index, child := range xref.Elements {
		child, err := eng.measureElement(child,
			eng.currentScopes.with(xref.Base().Styles), slot.style, inner, measured)
		if err != nil {
			return err
		}
		slots[index] = child
	}
	if err := placeFloats(slots); err != nil {
		return err
	}
	bottom := slot.top
	for _, child := range slots {
		if child.dropped || child.anchored {
			continue
		}
		child.top = geom.Round(child.top + slot.top)
		if edge := geom.Round(child.top + child.height); edge > bottom {
			bottom = edge
		}
	}
	for _, child := range slots {
		if child.dropped || !child.anchored {
			continue
		}
		top, extent, err := child.el.Base().Vert.Resolve(slot.top, slot.height)
		if err != nil {
			return eng.nodeError(child.el.Base().Node, err)
		}
		child.top, child.height = top, extent
	}
	for _, child := range slots {
		if child.dropped {
			continue
		}
		if err := eng.emit(child, measured); err != nil {
			return err
		}
		for _, dft := range child.drafts {
			mark.Marks = append(mark.Marks, dft.mark)
			if dft.bottom > bottom {
				bottom = dft.bottom
			}
		}
		slot.defers = append(slot.defers, child.defers...)
	}

	slot.drafts = append(slot.drafts, &draft{
		mark: mark, top: slot.top, bottom: maxf(bottom, geom.Round(slot.top+slot.height)),
	})
	return nil
}

// barcode encodes a barcode element's content and sizes the symbol.
func (eng *engine) barcode(
	barcode *tmpl.Barcode,
	slot *elementSlot,
) (*barcodes.Symbol, barcodes.Metrics, error) {
	var text string
	var err error
	if barcode.Deferred() {
		text, err = eng.placeholderText(&barcode.Content)
	} else {
		text, err = eng.contentText(&barcode.Content)
	}
	if err != nil {
		return nil, barcodes.Metrics{}, err
	}
	sym, err := barcodes.Encode(barcode.Type, text)
	if err != nil {
		return nil, barcodes.Metrics{}, fmt.Errorf("%s: %w", barcode.Node.Path(), err)
	}
	boxLength, boxCross := slot.width, slot.height
	if barcode.Vertical {
		boxLength, boxCross = slot.height, slot.width
	}
	return sym, barcodes.Measure(sym, barcode.Module, barcode.Grow, boxLength, boxCross), nil
}

// image decodes an image element's bytes and reads its natural size.
func (eng *engine) image(im *tmpl.Image) (*decodedImage, error) {
	// Keyed by the resolved path, not the written one: two templates in one
	// document have base directories of their own, and one relative path
	// can name two different files.
	key := eng.resolvePath(im.File) + "\x00" + im.Data + "\x00" + im.Content
	if cached, ok := eng.images[key]; ok {
		return cached, nil
	}
	var raw []byte
	var file string
	switch {
	case im.Data != "":
		blob, err := eng.blobBytes(im.Data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", im.Node.Path(), err)
		}
		raw = blob
	case im.HasContent:
		blob, err := decodeBlob(im.Content, "base64", "")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", im.Node.Path(), err)
		}
		raw = blob
	default:
		path := eng.resolvePath(im.File)
		blob, err := readFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", im.Node.Path(), err)
		}
		raw, file = blob, path
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", im.Node.Path(), err)
	}
	if im.Type != "" {
		format = im.Type
	}
	out := &decodedImage{
		data: raw, format: format, file: file,
		width: float64(cfg.Width), height: float64(cfg.Height),
	}
	eng.images[key] = out
	return out, nil
}

// noteMissingGlyphs records a character the resolved font lacks.
//
// It is a warning rather than an error: the .notdef glyph is itself
// visible failure, and metrics are unaffected, so nothing silently shifts.
func (eng *engine) noteMissingGlyphs(face *fontres.Face, node string) {
	for _, char := range face.MissingRunes() {
		key := fmt.Sprintf("%s\x00%c", face.Name, char)
		if eng.glyphWarned[key] {
			continue
		}
		eng.glyphWarned[key] = true
		eng.out.AddWarning(printout.WarnGlyph, node, 0, fmt.Sprintf(
			"the font %q has no glyph for %q, so an empty box is drawn in its place",
			face.Name, char))
	}
}
