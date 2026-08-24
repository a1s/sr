package pdfw

import (
	"bytes"
	"fmt"
	"strings"
)

// Bezier is the circle approximation constant: the control point offset,
// as a fraction of the radius, that makes a cubic segment match a quarter
// circle to within a quarter of a per mille.
const Bezier = 0.5522847498

// Content builds one page's content stream.
//
// Callers work in the printout's coordinates -- origin top left, Y down --
// and Content flips Y as it writes each number. The flip is per
// coordinate rather than a page-level matrix because a matrix
// that mirrors the page mirrors the glyphs with it.
type Content struct {
	// Height is the page height, the axis the flip turns around.
	Height float64

	buf bytes.Buffer
}

// NewContent starts a content stream for a page of a height.
func NewContent(height float64) *Content { return &Content{Height: height} }

// Bytes returns the stream written so far.
func (con *Content) Bytes() []byte { return con.buf.Bytes() }

// Len is the number of bytes written, which tells an empty page
// from one that drew something.
func (con *Content) Len() int { return con.buf.Len() }

// flip converts a top-down Y to PDF's bottom-up one.
func (con *Content) flip(top float64) float64 { return con.Height - top }

func (con *Content) op(format string, args ...any) {
	fmt.Fprintf(&con.buf, format, args...)
	con.buf.WriteByte('\n')
}

// Save pushes the graphics state.
func (con *Content) Save() { con.op("q") }

// Restore pops it.
func (con *Content) Restore() { con.op("Q") }

// LineWidth sets the stroke width.
//
// Zero is a hairline, which PDF defines as the thinnest line
// the output device can render, so it is passed through
// rather than turned into a width of our choosing.
func (con *Content) LineWidth(width float64) { con.op("%s w", Num(width)) }

// Dash sets a dash pattern in points. An empty pattern is a solid line.
func (con *Content) Dash(pattern []float64) {
	if len(pattern) == 0 {
		con.op("[] 0 d")
		return
	}
	parts := make([]string, len(pattern))
	for index, value := range pattern {
		parts[index] = Num(value)
	}
	con.op("[%s] 0 d", strings.Join(parts, " "))
}

// StrokeColor sets the stroking colour from components in 0..1.
func (con *Content) StrokeColor(red, green, blue float64) {
	con.op("%s %s %s RG", Num(red), Num(green), Num(blue))
}

// FillColor sets the non-stroking colour, which paints both fills and glyphs.
func (con *Content) FillColor(red, green, blue float64) {
	con.op("%s %s %s rg", Num(red), Num(green), Num(blue))
}

// MoveTo starts a subpath.
func (con *Content) MoveTo(left, top float64) {
	con.op("%s %s m", Num(left), Num(con.flip(top)))
}

// LineTo extends it.
func (con *Content) LineTo(left, top float64) {
	con.op("%s %s l", Num(left), Num(con.flip(top)))
}

// CurveTo appends a cubic segment.
func (con *Content) CurveTo(left1, top1, left2, top2, left3, top3 float64) {
	con.op("%s %s %s %s %s %s c",
		Num(left1), Num(con.flip(top1)),
		Num(left2), Num(con.flip(top2)),
		Num(left3), Num(con.flip(top3)))
}

// Rect appends a rectangle subpath, given its top-left corner.
func (con *Content) Rect(left, top, width, height float64) {
	con.op("%s %s %s %s re",
		Num(left), Num(con.flip(top+height)), Num(width), Num(height))
}

// RoundedRect appends a rectangle with rounded corners. A radius larger
// than half the shorter side is clamped, so a very round small box stays
// a box rather than turning inside out.
func (con *Content) RoundedRect(left, top, width, height, radius float64) {
	limit := width / 2
	if height/2 < limit {
		limit = height / 2
	}
	if radius > limit {
		radius = limit
	}
	if radius <= 0 {
		con.Rect(left, top, width, height)
		return
	}
	right, bottom := left+width, top+height
	pull := radius * Bezier

	con.MoveTo(left+radius, top)
	con.LineTo(right-radius, top)
	con.CurveTo(right-radius+pull, top, right, top+radius-pull, right, top+radius)
	con.LineTo(right, bottom-radius)
	con.CurveTo(right, bottom-radius+pull, right-radius+pull, bottom, right-radius, bottom)
	con.LineTo(left+radius, bottom)
	con.CurveTo(left+radius-pull, bottom, left, bottom-radius+pull, left, bottom-radius)
	con.LineTo(left, top+radius)
	con.CurveTo(left, top+radius-pull, left+radius-pull, top, left+radius, top)
	con.op("h")
}

// Stroke paints the current path's outline.
func (con *Content) Stroke() { con.op("S") }

// Fill paints its interior.
func (con *Content) Fill() { con.op("f") }

// FillStroke paints both, the fill first.
func (con *Content) FillStroke() { con.op("B") }

// Clip intersects the clipping path with the current path and ends it.
func (con *Content) Clip() { con.op("W n") }

// Matrix concatenates a transformation matrix, its translation given
// in top-down coordinates: the matrix maps the unit square onto a box
// whose top-left corner is at left, top.
func (con *Content) Matrix(scaleX, scaleY, left, top float64) {
	con.op("%s 0 0 %s %s %s cm",
		Num(scaleX), Num(scaleY), Num(left), Num(con.flip(top+scaleY)))
}

// XObject draws a named external object, an image here.
func (con *Content) XObject(name string) { con.op("/%s Do", name) }

// BeginText opens a text object.
func (con *Content) BeginText() { con.op("BT") }

// EndText closes it.
func (con *Content) EndText() { con.op("ET") }

// Font selects a font resource at a size.
func (con *Content) Font(name string, size float64) {
	con.op("/%s %s Tf", name, Num(size))
}

// TextStart places the first line of a text object by its baseline.
func (con *Content) TextStart(left, baseline float64) {
	con.op("%s %s Td", Num(left), Num(con.flip(baseline)))
}

// TextOffset moves the line start by a displacement.
//
// This is how the segments of a justified line are placed: each offset is exact,
// so a segment's position does not depend on the glyph advances before it.
func (con *Content) TextOffset(dx, dy float64) {
	con.op("%s %s Td", Num(dx), Num(-dy))
}

// ShowGlyphs draws a run of glyphs, given as glyph indices in the
// embedded subset, which Identity-H encoding writes as two bytes each.
func (con *Content) ShowGlyphs(glyphs []uint16) {
	if len(glyphs) == 0 {
		return
	}
	var hex strings.Builder
	for _, glyph := range glyphs {
		fmt.Fprintf(&hex, "%04X", glyph)
	}
	con.op("<%s> Tj", hex.String())
}
