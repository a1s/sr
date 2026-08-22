// Package geom implements the geometry model of doc/template.md#geometry:
// dimensions with units, the three-decimal rounding rule, boxes,
// the any-two-of-three edge resolution, and content alignment.
package geom

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Tolerance is the slack allowed when comparing a length
// against a frame boundary, per doc/layout.md#coordinates-and-rounding.
// A band whose height matches the remaining space exactly fits
// rather than ejecting.
const Tolerance = 0.001

// Round rounds a length to three decimal places.
//
// The rule is normative rather than cosmetic: it decides whether a band fits,
// so an implementation that rounds only at output time disagrees about page
// breaks. Every computed coordinate and extent passes through here immediately
// after the computation that produces it.
func Round(value float64) float64 {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return value
	}
	return math.Round(value*1000) / 1000
}

// Fits reports whether want fits in have, within Tolerance.
func Fits(want, have float64) bool { return want <= have+Tolerance }

// Units accepted as a suffix on a quoted dimension. Bare numbers are points.
var units = []struct {
	suffix string
	perPt  float64
}{
	{"pt", 1},
	{"mil", 72.0 / 1000.0},
	{"mm", 72.0 / 25.4},
	{"cm", 72.0 / 2.54},
	{"in", 72},
}

// ParseDim parses a dimension: a bare number in PostScript points,
// or a number with one of the unit suffixes pt, mil, mm, cm, in.
func ParseDim(spec string) (float64, error) {
	text := strings.TrimSpace(spec)
	if text == "" {
		return 0, fmt.Errorf("empty dimension")
	}
	for _, unit := range units {
		if !strings.HasSuffix(text, unit.suffix) {
			continue
		}
		number := strings.TrimSpace(strings.TrimSuffix(text, unit.suffix))
		value, err := strconv.ParseFloat(number, 64)
		if err != nil {
			return 0, fmt.Errorf("bad dimension %q", spec)
		}
		return Round(value * unit.perPt), nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, fmt.Errorf("bad dimension %q", spec)
	}
	return Round(value), nil
}

// Box is an absolute rectangle. Y grows downward from the top-left of the page.
type Box struct {
	Left, Top, Width, Height float64
}

// Right returns the box's right edge.
func (box Box) Right() float64 { return Round(box.Left + box.Width) }

// Bottom returns the box's bottom edge.
func (box Box) Bottom() float64 { return Round(box.Top + box.Height) }

// Opt is an optionally-specified length.
type Opt struct {
	Value float64
	Set   bool
}

// Val returns a set Opt.
func Val(value float64) Opt { return Opt{Value: value, Set: true} }

// Unset returns an unset Opt.
func Unset() Opt { return Opt{} }

// Extent is one axis of a declared box: the near edge (left or top),
// the far edge (right or bottom, measured inward from the container's
// far edge), and the size. Max is the maxwidth or maxheight clamp,
// which is not part of the two-of-three count.
type Extent struct {
	Near, Far, Size Opt
	Max             Opt
}

// Count returns how many of Near, Far and Size are specified.
func (ext Extent) Count() int {
	count := 0
	for _, opt := range [...]Opt{ext.Near, ext.Far, ext.Size} {
		if opt.Set {
			count++
		}
	}
	return count
}

// ErrOverSpecified reports an axis with all three of near, far and size given.
// Templates are rejected for this at load; Resolve guards against it too.
var ErrOverSpecified = fmt.Errorf("all three of near edge, far edge and size specified")

// Resolve places the extent within a container running from start for size
// units, per the fill order in doc/template.md#position-and-size-any-two-of-three:
// missing values are filled in the order near, then far, until two of the three
// are known, and the third is derived.
//
// A derived size may come out negative when the two declared edges cross;
// it is clamped to zero, since a printout box has non-negative extents.
func (ext Extent) Resolve(start, size float64) (pos, extent float64, err error) {
	near, far, own := ext.Near, ext.Far, ext.Size
	if near.Set && far.Set && own.Set {
		return 0, 0, ErrOverSpecified
	}
	// Fill defaults in a fixed order until two of the three are known.
	if !near.Set && !far.Set && !own.Set {
		near, far = Val(0), Val(0)
	} else if near.Count(far, own) == 1 {
		switch {
		case near.Set:
			far = Val(0)
		default:
			near = Val(0)
		}
	}

	switch {
	case near.Set && own.Set:
		pos, extent = start+near.Value, own.Value
	case near.Set && far.Set:
		pos, extent = start+near.Value, size-near.Value-far.Value
	default: // far and own
		extent = own.Value
		pos = start + size - far.Value - extent
	}
	pos, extent = Round(pos), Round(extent)

	if ext.Max.Set && extent > ext.Max.Value {
		clamped := Round(ext.Max.Value)
		// Keep whichever edge the author actually anchored.
		// Only the far-plus-size form has a derived near edge.
		if !near.Set {
			pos = Round(pos + extent - clamped)
		}
		extent = clamped
	}
	if extent < 0 {
		extent = 0
	}
	return pos, extent, nil
}

// Count is a helper for Resolve: how many of the receiver
// and the two arguments are set.
func (opt Opt) Count(rest ...Opt) int {
	count := 0
	if opt.Set {
		count++
	}
	for _, other := range rest {
		if other.Set {
			count++
		}
	}
	return count
}

// HAlign is horizontal alignment of content within its box.
type HAlign int

// Horizontal alignments.
const (
	HLeft HAlign = iota
	HCenter
	HRight
)

// VAlign is vertical alignment of content within its box.
type VAlign int

// Vertical alignments.
const (
	VTop VAlign = iota
	VCenter
	VBottom
)

// ParseHAlign parses a halign enumeration value.
func ParseHAlign(text string) (HAlign, error) {
	switch text {
	case "left":
		return HLeft, nil
	case "center":
		return HCenter, nil
	case "right":
		return HRight, nil
	}
	return 0, fmt.Errorf("unknown halign %q", text)
}

// ParseVAlign parses a valign enumeration value.
func ParseVAlign(text string) (VAlign, error) {
	switch text {
	case "top":
		return VTop, nil
	case "center":
		return VCenter, nil
	case "bottom":
		return VBottom, nil
	}
	return 0, fmt.Errorf("unknown valign %q", text)
}

// AlignH places content of width inner inside a box at outerStart
// of width outer, and returns the content's start coordinate.
func AlignH(outerStart, outer, inner float64, align HAlign) float64 {
	switch align {
	case HCenter:
		return Round(outerStart + (outer-inner)/2)
	case HRight:
		return Round(outerStart + outer - inner)
	}
	return Round(outerStart)
}

// AlignV places content of height inner inside a box at outerStart
// of height outer, and returns the content's start coordinate.
func AlignV(outerStart, outer, inner float64, align VAlign) float64 {
	switch align {
	case VCenter:
		return Round(outerStart + (outer-inner)/2)
	case VBottom:
		return Round(outerStart + outer - inner)
	}
	return Round(outerStart)
}
