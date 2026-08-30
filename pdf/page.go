package pdf

import (
	"fmt"
	"strings"

	"github.com/a1s/sr/internal/pdfw"
	"github.com/a1s/sr/printout"
)

// page draws one page, returning its content stream
// and the Annots entry for its links.
func (ren *renderer) page(index int, page *printout.Page) ([]byte, string, error) {
	con := pdfw.NewContent(ren.pageHeight(page))
	var annots []string
	if err := ren.drawMarks(con, page.Marks, &annots); err != nil {
		return nil, "", err
	}
	entry := ""
	if len(annots) > 0 {
		entry = " /Annots [" + strings.Join(annots, " ") + "]"
	}
	return con.Bytes(), entry, nil
}

// drawMarks paints marks in the order they are given, which is the paint
// order the printout carries: a later mark draws over an earlier one.
func (ren *renderer) drawMarks(
	con *pdfw.Content, marks []printout.Mark, annots *[]string) error {
	for _, mark := range marks {
		var err error
		switch typed := mark.(type) {
		case *printout.Text:
			err = ren.drawText(con, typed)
		case *printout.Line:
			ren.drawLine(con, typed)
		case *printout.Rectangle:
			ren.drawRectangle(con, typed)
		case *printout.Image:
			err = ren.drawImage(con, typed)
		case *printout.Barcode:
			ren.drawBarcode(con, typed)
		case *printout.Outline:
			// An outline entry names a position rather than drawing
			// anything; it was collected in the first pass.
		case *printout.Xref:
			// The nested marks carry page coordinates,
			// so they are drawn exactly as top-level marks are,
			// and the box is only a hit region.
			if err = ren.drawMarks(con, typed.Marks, annots); err == nil {
				err = ren.annotate(con, typed, annots)
			}
		default:
			err = fmt.Errorf("unknown mark %T", mark)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// annotate adds a link annotation for an xref mark.
func (ren *renderer) annotate(
	con *pdfw.Content, mark *printout.Xref, annots *[]string) error {
	box := mark.Box
	rect := fmt.Sprintf("[%s %s %s %s]",
		pdfw.Num(box.Left), pdfw.Num(con.Height-(box.Top+box.Height)),
		pdfw.Num(box.Left+box.Width), pdfw.Num(con.Height-box.Top))
	caption := ""
	if mark.Caption != "" {
		caption = " /Contents " + pdfw.TextString(mark.Caption)
	}

	var action string
	switch mark.Type {
	case "url":
		action = " /A <</S /URI /URI " + pdfw.ASCIIString(mark.Target) + ">>"
	case "outline":
		index, ok := ren.named[mark.Target]
		if !ok {
			return fmt.Errorf(
				"an xref points at outline %q, which no outline entry claims",
				mark.Target)
		}
		node := ren.outlines[index]
		action = " /Dest " + ren.destination(node)
	default:
		return fmt.Errorf("unknown xref type %q", mark.Type)
	}

	*annots = append(*annots, fmt.Sprintf(
		"<</Type /Annot /Subtype /Link /Rect %s /Border [0 0 0]%s%s>>",
		rect, caption, action))
	return nil
}

// destination is the position an outline entry or an internal link jumps to:
// the page, and a point on it in that page's own coordinates.
func (ren *renderer) destination(node *outlineNode) string {
	height := ren.pageHeight(ren.src.Pages[node.page])
	return fmt.Sprintf("[%s /XYZ %s %s null]",
		pdfw.Ref(ren.pageObjects[node.page]),
		pdfw.Num(node.left), pdfw.Num(height-node.top))
}

// drawText sets a text mark's lines.
//
// The printout fixes the box, the leading, and the wrapped lines.
// What is left to the renderer is where the baseline sits
// inside a line's leading, and how a justified line spends its slack.
func (ren *renderer) drawText(con *pdfw.Content, mark *printout.Text) error {
	font, ok := ren.fonts[mark.Font]
	if !ok {
		return fmt.Errorf(
			"a text mark names font %q, which the header does not carry",
			mark.Font)
	}
	if len(mark.Lines) == 0 {
		return nil
	}
	size := float64(font.entry.Size)
	scale := size / font.face.Upem()
	ascender, descender := font.face.VerticalMetrics()
	ascent, descent := ascender*scale, descender*scale
	// Centre the face's own em extent in the leading the printout
	// reserved, then measure the baseline down from the top of it.
	// The leading is a constant multiple of the size and the face's
	// extent is not, so a face whose extent exceeds the leading
	// overhangs its slot evenly rather than at one end.
	first := mark.Box.Top + (mark.Leading-(ascent+descent))/2 + ascent

	red, green, blue := parseColor(mark.Color)
	con.FillColor(red, green, blue)

	for index, line := range mark.Lines {
		baseline := first + float64(index)*mark.Leading
		last := index == len(mark.Lines)-1
		justify := mark.Align == "justified" &&
			(!last || mark.LastLineJustified)
		ren.drawLineOfText(con, font, mark, line, baseline, justify)
	}
	return nil
}

// drawLineOfText places one line and, when the font asks for it,
// its underline.
func (ren *renderer) drawLineOfText(con *pdfw.Content, font *renderFont,
	mark *printout.Text, line string, baseline float64, justify bool) {
	size := float64(font.entry.Size)
	width := font.face.Width(line)

	left := mark.Box.Left
	drawn := width
	var parts []string
	var widths []float64
	share := 0.0
	if justify {
		parts = segments(line)
		// The slack is measured against the sum of the segments
		// rather than against the whole line: the segments are what
		// is drawn, each is measured and rounded on its own, and
		// their sum is therefore what has to reach the box's far edge.
		widths = make([]float64, len(parts))
		total := 0.0
		for index, part := range parts {
			widths[index] = font.face.Width(part)
			total += widths[index]
		}
		if extra := mark.Box.Width - total; extra > 0 && len(parts) > 1 {
			share = extra / float64(len(parts)-1)
			drawn = mark.Box.Width
		} else {
			parts = nil
		}
	}
	if parts == nil {
		switch mark.Align {
		case "center":
			left = mark.Box.Left + (mark.Box.Width-width)/2
		case "right":
			left = mark.Box.Left + mark.Box.Width - width
		}
	}

	con.BeginText()
	con.Font(font.resource, size)
	con.TextStart(left, baseline)
	if parts == nil {
		con.ShowGlyphs(font.glyphs(line))
	} else {
		// Each segment is placed by an exact displacement
		// from the one before, so a segment's position does
		// not depend on the glyph advances that precede it.
		for index, part := range parts {
			con.ShowGlyphs(font.glyphs(part))
			if index < len(parts)-1 {
				con.TextOffset(widths[index]+share, 0)
			}
		}
	}
	con.EndText()

	if font.entry.Underline && strings.TrimSpace(line) != "" {
		scale := size / font.face.Upem()
		offset, thickness := font.face.UnderlineMetrics()
		con.Rect(left, baseline+offset*scale, drawn, thickness*scale)
		con.Fill()
	}
}

// segments splits a justified line at its internal runs of whitespace.
//
// A segment carries its own trailing spaces, so the spaces are drawn
// and the text comes back out of the file with its words separated;
// the slack is added as a jump on top of them. Leading whitespace stays
// attached to the first segment rather than becoming a gap of its own,
// so an indent does not stretch.
func segments(line string) []string {
	runes := []rune(line)
	start := 0
	for start < len(runes) && isSpace(runes[start]) {
		start++
	}
	if start >= len(runes) {
		return []string{line}
	}

	var out []string
	segment := string(runes[:start])
	at := start
	for at < len(runes) {
		for at < len(runes) && !isSpace(runes[at]) {
			segment += string(runes[at])
			at++
		}
		if at >= len(runes) {
			break
		}
		for at < len(runes) && isSpace(runes[at]) {
			segment += string(runes[at])
			at++
		}
		out = append(out, segment)
		segment = ""
	}
	if segment != "" {
		out = append(out, segment)
	}
	return out
}

func isSpace(char rune) bool { return char == ' ' || char == '\t' }

// dashPatterns are the dash enumerations, in points.
//
// They are absolute rather than a multiple of the stroke width,
// so a hairline and a two-point rule dash on the same rhythm,
// and a dotted line looks dotted at every width.
var dashPatterns = map[string][]float64{
	"solid":   nil,
	"dot":     {1, 2},
	"dash":    {3, 2},
	"dashdot": {3, 2, 1, 2},
}

func (ren *renderer) drawLine(con *pdfw.Content, mark *printout.Line) {
	red, green, blue := parseColor(mark.Color)
	con.Save()
	con.StrokeColor(red, green, blue)
	con.LineWidth(mark.Width)
	con.Dash(dashPatterns[mark.Dash])
	if mark.Backslant {
		con.MoveTo(mark.Box.Left, mark.Box.Top+mark.Box.Height)
		con.LineTo(mark.Box.Left+mark.Box.Width, mark.Box.Top)
	} else {
		con.MoveTo(mark.Box.Left, mark.Box.Top)
		con.LineTo(mark.Box.Left+mark.Box.Width, mark.Box.Top+mark.Box.Height)
	}
	con.Stroke()
	con.Restore()
}

func (ren *renderer) drawRectangle(con *pdfw.Content, mark *printout.Rectangle) {
	if mark.Stroke == nil && mark.Fill == nil {
		return
	}
	con.Save()
	if mark.Fill != nil {
		red, green, blue := parseColor(*mark.Fill)
		con.FillColor(red, green, blue)
	}
	if mark.Stroke != nil {
		red, green, blue := parseColor(*mark.Stroke)
		con.StrokeColor(red, green, blue)
		con.LineWidth(mark.Width)
		con.Dash(dashPatterns[mark.Dash])
	}
	con.RoundedRect(mark.Box.Left, mark.Box.Top,
		mark.Box.Width, mark.Box.Height, mark.Radius)
	switch {
	case mark.Fill != nil && mark.Stroke != nil:
		con.FillStroke()
	case mark.Fill != nil:
		con.Fill()
	default:
		con.Stroke()
	}
	con.Restore()
}

// drawImage places an image, scaling whatever maps its crop onto its box.
//
// The printout has already resolved the template's scale and
// proportional settings: the box is the drawn rectangle and the crop,
// if there is one, names the source pixels that fill it. So there is
// no fitting to do here, only a clip when part of the image falls outside.
func (ren *renderer) drawImage(con *pdfw.Content, mark *printout.Image) error {
	pic, ok := ren.images[imageKey(mark)]
	if !ok {
		return fmt.Errorf("an image was not gathered: %s", imageKey(mark))
	}
	box := mark.Box
	if box.Width <= 0 || box.Height <= 0 {
		return nil
	}

	con.Save()
	width, height := box.Width, box.Height
	left, top := box.Left, box.Top
	if mark.Crop != nil && mark.Crop.Width > 0 && mark.Crop.Height > 0 {
		perPixelX := box.Width / mark.Crop.Width
		perPixelY := box.Height / mark.Crop.Height
		width = float64(pic.pic.Width) * perPixelX
		height = float64(pic.pic.Height) * perPixelY
		left = box.Left - mark.Crop.Left*perPixelX
		top = box.Top - mark.Crop.Top*perPixelY
		con.Rect(box.Left, box.Top, box.Width, box.Height)
		con.Clip()
	}
	con.Matrix(width, height, left, top)
	con.XObject(pic.resource)
	con.Restore()
	return nil
}

// drawBarcode paints a symbol as filled rectangles from the
// stripe geometry the printout carries. No encoding happens here.
//
// The paper goes down first and covers the whole box, quiet zones
// included, so the margin a scanner needs is the colour the template
// asked for rather than whatever the band left underneath. A printout
// with no paper leaves that showing, which is the older behaviour.
func (ren *renderer) drawBarcode(con *pdfw.Content, mark *printout.Barcode) {
	if mark.Module <= 0 {
		return
	}
	con.Save()
	if mark.Paper != nil {
		red, green, blue := parseColor(*mark.Paper)
		con.FillColor(red, green, blue)
		con.Rect(mark.Box.Left, mark.Box.Top, mark.Box.Width, mark.Box.Height)
		con.Fill()
	}
	red, green, blue := parseColor(mark.Ink)
	con.FillColor(red, green, blue)
	before := con.Len()
	if len(mark.Rows) > 0 {
		ren.drawMatrix(con, mark)
	} else {
		ren.drawStripes(con, mark)
	}
	// The bars are one path, painted once. A symbol with nothing dark
	// in it leaves the path empty, and an empty path is not painted.
	if con.Len() > before {
		con.Fill()
	}
	con.Restore()
}

// drawStripes paints a one-dimensional symbol.
//
// The symbol is a sequence of alternating space and bar widths
// along the coding direction, starting with the leading quiet zone,
// spanning the whole extent across it.
func (ren *renderer) drawStripes(con *pdfw.Content, mark *printout.Barcode) {
	along := mark.Box.Left
	if mark.Vertical {
		along = mark.Box.Top
	}
	dark := false
	for _, stripe := range mark.Stripes {
		extent := float64(stripe) * mark.Module
		if dark && extent > 0 {
			if mark.Vertical {
				con.Rect(mark.Box.Left, along, mark.Box.Width, extent)
			} else {
				con.Rect(along, mark.Box.Top, extent, mark.Box.Height)
			}
		}
		along += extent
		dark = !dark
	}
}

// drawMatrix paints a two-dimensional symbol, one row of runs at a time.
//
// With vertical set the symbol is turned a quarter turn clockwise:
// the coding direction runs down the page and the rows advance leftward
// from the box's right edge.
func (ren *renderer) drawMatrix(con *pdfw.Content, mark *printout.Barcode) {
	for index, row := range mark.Rows {
		cross := float64(index) * mark.Module
		along := 0.0
		dark := false
		for _, run := range row {
			extent := float64(run) * mark.Module
			if dark && extent > 0 {
				if mark.Vertical {
					con.Rect(
						mark.Box.Left+mark.Box.Width-cross-mark.Module,
						mark.Box.Top+along, mark.Module, extent)
				} else {
					con.Rect(mark.Box.Left+along, mark.Box.Top+cross,
						extent, mark.Module)
				}
			}
			along += extent
			dark = !dark
		}
	}
}

// parseColor reads a #RRGGBB string into components in 0..1.
//
// An unreadable colour is black, which is what a printout's own
// validation makes unreachable.
func parseColor(text string) (red, green, blue float64) {
	if len(text) != 7 || text[0] != '#' {
		return 0, 0, 0
	}
	value := 0
	for _, char := range text[1:] {
		digit := 0
		switch {
		case char >= '0' && char <= '9':
			digit = int(char - '0')
		case char >= 'a' && char <= 'f':
			digit = int(char-'a') + 10
		case char >= 'A' && char <= 'F':
			digit = int(char-'A') + 10
		default:
			return 0, 0, 0
		}
		value = value<<4 | digit
	}
	return float64((value>>16)&0xFF) / 255,
		float64((value>>8)&0xFF) / 255,
		float64(value&0xFF) / 255
}
