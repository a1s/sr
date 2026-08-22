// Package barcodes encodes a value into the stripe geometry a printout carries.
//
// See doc/printout.md#barcode.
// A renderer does not need to implement encoding of its own.
package barcodes

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/aztec"
	"github.com/boombuler/barcode/code128"
	"github.com/boombuler/barcode/code39"
	"github.com/boombuler/barcode/code93"
	"github.com/boombuler/barcode/datamatrix"
	"github.com/boombuler/barcode/qr"
	"github.com/boombuler/barcode/twooffive"
)

// QuietModules is the quiet zone added to each end of a one-dimensional
// symbol, in modules. Ten is the usual minimum for the symbologies here.
//
// Two-dimensional symbols carry whatever quiet zone their own encoding
// requires, so none is added to them.
const QuietModules = 10

// MinHeightRatio is a one-dimensional symbol's minimum bar height
// as a fraction of its length, and MinHeightPt the floor under that.
// Together they are the usual recommendation: fifteen per cent of
// the symbol length, or a quarter of an inch, whichever is greater.
const (
	MinHeightRatio = 0.15
	MinHeightPt    = 18 // 0.25 in
)

// Symbol is an encoded barcode, in modules.
type Symbol struct {
	// Type is the symbology, as the template spells it.
	Type string
	// Value is the encoded string, recorded for renderers printing
	// bar codes directly -- such as barcode printer drivers.
	Value string
	// TwoD reports whether the symbol is a matrix rather than a bar pattern.
	TwoD bool
	// Stripes for a one-dimensional symbol: alternating bar and space widths,
	// starting with a bar. A leading quiet zone therefore opens with a
	// zero-width bar.
	Stripes []int
	// Rows for a two-dimensional symbol: per row, alternating dark and light
	// run lengths, starting with dark.
	Rows [][]int
	// Modules is the symbol's extent along the coding direction,
	// in modules, quiet zones included.
	Modules int
	// CrossModules is the extent across the coding direction, in modules.
	// It is meaningful only for a two-dimensional symbol.
	CrossModules int
}

// Encode produces the symbol for a value.
//
// Content a type cannot encode is an error naming the type,
// the value, and the reason.
func Encode(kind, value string) (*Symbol, error) {
	if err := checkContent(kind, value); err != nil {
		return nil, err
	}
	code, err := encodeRaw(kind, value)
	if err != nil {
		return nil, fmt.Errorf("barcode %s: cannot encode %q: %w", kind, value, err)
	}
	bounds := code.Bounds()
	sym := &Symbol{Type: kind, Value: value}

	if bounds.Dy() <= 1 {
		bits := make([]bool, 0, bounds.Dx())
		for col := bounds.Min.X; col < bounds.Max.X; col++ {
			bits = append(bits, isDark(code.At(col, bounds.Min.Y)))
		}
		sym.Stripes = runsFromBits(bits, QuietModules)
		for _, stripe := range sym.Stripes {
			sym.Modules += stripe
		}
		sym.CrossModules = 0
		return sym, nil
	}

	sym.TwoD = true
	for row := bounds.Min.Y; row < bounds.Max.Y; row++ {
		bits := make([]bool, 0, bounds.Dx())
		for col := bounds.Min.X; col < bounds.Max.X; col++ {
			bits = append(bits, isDark(code.At(col, row)))
		}
		sym.Rows = append(sym.Rows, runsFromBits(bits, 0))
	}
	sym.Modules = bounds.Dx()
	sym.CrossModules = bounds.Dy()
	return sym, nil
}

// runsFromBits converts a bit pattern into alternating run lengths starting
// with a dark run, adding a quiet zone at each end when one is asked for.
//
// A pattern that opens light gets a zero-length dark run first, which is what
// keeps the alternation unambiguous while letting the runs sum to the whole
// extent.
func runsFromBits(bits []bool, quiet int) []int {
	var runs []int
	// dark is the polarity of the next run to emit.
	dark := true
	if quiet > 0 {
		runs = append(runs, 0, quiet)
	}
	for index := 0; index < len(bits); {
		if bits[index] != dark {
			runs = append(runs, 0)
			dark = !dark
			continue
		}
		runLen := 0
		for index < len(bits) && bits[index] == dark {
			runLen++
			index++
		}
		runs = append(runs, runLen)
		dark = !dark
	}
	if quiet > 0 {
		if dark {
			// The next run would be dark, so the last one emitted was light:
			// fold the trailing quiet zone into it.
			runs[len(runs)-1] += quiet
		} else {
			runs = append(runs, quiet)
		}
	}
	return runs
}

func isDark(pixel color.Color) bool {
	red, green, blue, alpha := pixel.RGBA()
	if alpha == 0 {
		return false
	}
	return red < 0x8000 && green < 0x8000 && blue < 0x8000
}

func encodeRaw(kind, value string) (barcode.Barcode, error) {
	switch kind {
	case "Code128":
		return code128.Encode(value)
	case "Code39":
		return code39.Encode(value, false, false)
	case "Code93":
		return code93.Encode(value, false, true)
	case "2of5i":
		return twooffive.Encode(value, true)
	case "DataMatrix":
		return datamatrix.Encode(value)
	case "Aztec":
		return aztec.Encode([]byte(value), aztec.DEFAULT_EC_PERCENT, aztec.DEFAULT_LAYERS)
	case "QR-L":
		return qr.Encode(value, qr.L, qr.Auto)
	case "QR-M":
		return qr.Encode(value, qr.M, qr.Auto)
	case "QR-Q":
		return qr.Encode(value, qr.Q, qr.Auto)
	case "QR-H":
		return qr.Encode(value, qr.H, qr.Auto)
	}
	return nil, fmt.Errorf("unknown type")
}

const code39Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ-. $/+%"

func checkContent(kind, value string) error {
	fail := func(reason string) error {
		return fmt.Errorf("barcode %s: cannot encode %q: %s", kind, value, reason)
	}
	if value == "" {
		return fail("the value is empty")
	}
	switch kind {
	case "Code128":
		for _, char := range value {
			if char > 127 {
				return fail(fmt.Sprintf("%q is outside ASCII 0-127", char))
			}
		}
	case "Code39":
		for _, char := range value {
			if !strings.ContainsRune(code39Alphabet, char) {
				return fail(fmt.Sprintf("%q is not one of the digits, A-Z upper case, space, or - . $ / + %%", char))
			}
		}
	case "Code93":
		for _, char := range value {
			if char > 127 {
				return fail(fmt.Sprintf("%q is outside the ASCII range", char))
			}
		}
	case "2of5i":
		for _, char := range value {
			if char < '0' || char > '9' {
				return fail(fmt.Sprintf("%q is not a digit", char))
			}
		}
		if len(value)%2 != 0 {
			return fail(fmt.Sprintf("interleaved 2 of 5 encodes digits in pairs, and %d is an odd number of them; use format to fix the width", len(value)))
		}
	}
	return nil
}

// Metrics is a symbol placed at a module width.
type Metrics struct {
	// Length is the extent along the coding direction, in points.
	Length float64
	// Cross is the extent across it, in points.
	Cross float64
	// Module is the narrow-element width in points, after any grow adjustment.
	Module float64
}

// Measure sizes a symbol.
//
// The box always grows along the coding direction to at least the symbol's
// minimum size. grow expands the symbol to use the available box: for a
// two-dimensional type that recomputes the module, and for a one-dimensional
// one it grows the bar height.
func Measure(sym *Symbol, module float64, grow bool, boxLength, boxCross float64) Metrics {
	metrics := Metrics{Module: module}
	if sym.TwoD {
		if grow {
			fit := boxLength
			if boxCross < fit {
				fit = boxCross
			}
			if modules := float64(sym.Modules); modules > 0 && fit/modules > module {
				metrics.Module = fit / modules
			}
		}
		metrics.Length = float64(sym.Modules) * metrics.Module
		metrics.Cross = float64(sym.CrossModules) * metrics.Module
		return metrics
	}

	metrics.Length = float64(sym.Modules) * metrics.Module
	minHeight := metrics.Length * MinHeightRatio
	if minHeight < MinHeightPt {
		minHeight = MinHeightPt
	}
	metrics.Cross = minHeight
	if grow && boxCross > metrics.Cross {
		metrics.Cross = boxCross
	}
	return metrics
}
