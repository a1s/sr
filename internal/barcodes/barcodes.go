// Package barcodes encodes a value into the stripe geometry a printout carries.
//
// See doc/printout.md#barcode.
// A renderer does not need to implement encoding of its own.
package barcodes

import (
	"fmt"
	"image/color"
	"math"
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
const QuietModules = 10

// quietZone is the margin a symbology requires on every side of the symbol,
// in modules.
//
// The encoder this package wraps returns the bare symbol and adds no
// margin of its own, so every one of these has to be applied here.
// The two-dimensional values are what each standard asks for: four modules
// for QR (ISO/IEC 18004), one for Data Matrix (ISO/IEC 16022), and none for
// Aztec (ISO/IEC 24778), whose bullseye finder needs no margin to be found.
func quietZone(kind string) int {
	switch kind {
	case "QR-L", "QR-M", "QR-Q", "QR-H":
		return 4
	case "DataMatrix":
		return 1
	case "Aztec":
		return 0
	}
	return QuietModules
}

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
	// Stripes for a one-dimensional symbol: alternating space and bar widths,
	// starting with a space, which is the leading quiet zone.
	Stripes []int
	// Rows for a two-dimensional symbol: per row, alternating light and dark
	// run lengths, starting with light. A row that opens dark -- which only
	// a symbology with no quiet zone can do -- opens with a zero-length
	// light run.
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
	quiet := quietZone(kind)

	if bounds.Dy() <= 1 {
		bits := make([]bool, 0, bounds.Dx())
		for col := bounds.Min.X; col < bounds.Max.X; col++ {
			bits = append(bits, isDark(code.At(col, bounds.Min.Y)))
		}
		sym.Stripes = runsFromBits(bits, quiet)
		for _, stripe := range sym.Stripes {
			sym.Modules += stripe
		}
		sym.CrossModules = 0
		return sym, nil
	}

	sym.TwoD = true
	sym.Modules = bounds.Dx() + 2*quiet
	sym.CrossModules = bounds.Dy() + 2*quiet
	// The quiet zone runs round all four sides, so it is a band of wholly
	// light rows above and below as well as a margin within every data row.
	for count := 0; count < quiet; count++ {
		sym.Rows = append(sym.Rows, []int{sym.Modules})
	}
	for row := bounds.Min.Y; row < bounds.Max.Y; row++ {
		bits := make([]bool, 0, bounds.Dx())
		for col := bounds.Min.X; col < bounds.Max.X; col++ {
			bits = append(bits, isDark(code.At(col, row)))
		}
		sym.Rows = append(sym.Rows, runsFromBits(bits, quiet))
	}
	for count := 0; count < quiet; count++ {
		sym.Rows = append(sym.Rows, []int{sym.Modules})
	}
	return sym, nil
}

// runsFromBits converts a bit pattern into alternating run lengths starting
// with a light run, adding a quiet zone at each end when one is asked for.
//
// Polarity is positional: index 0 is light, index 1 dark, and so on,
// so nothing has to record where the alternation starts. A quiet zone
// is light and so is the first run, which is why the two fold together
// and why the common case costs no extra element. A pattern that opens
// dark -- possible only where the symbology asks for no quiet zone --
// opens with a zero-length light run, which keeps the alternation unambiguous
// while letting the runs still sum to the whole extent.
func runsFromBits(bits []bool, quiet int) []int {
	runs := []int{0}
	// dark is the polarity of the run being accumulated, the last in runs.
	dark := false
	for _, bit := range bits {
		if bit == dark {
			runs[len(runs)-1]++
			continue
		}
		runs = append(runs, 1)
		dark = !dark
	}
	runs[0] += quiet
	switch {
	case !dark:
		// The pattern ended light, so the trailing quiet zone folds into
		// the run already there.
		runs[len(runs)-1] += quiet
	case quiet > 0:
		runs = append(runs, quiet)
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

// Scan contrast thresholds, as fractions of full reflectance.
//
// A barcode is read in red light -- around 660 nm for a laser scanner and
// for most linear imagers, and 670 nm is where ISO/IEC 15416 measures the
// contrast it grades. So what decides whether a symbol scans is how little
// red it reflects, not how dark a colour looks. Blue, green, brown and
// purple bars absorb red and read as bars; red, orange and yellow ones
// reflect it and are very nearly invisible. The same fact in reverse
// makes yellow, orange and pink perfectly good backgrounds, and blue
// or green ones unusable.
const (
	// MinSymbolContrast is the reflectance difference between paper
	// and ink that a symbol needs. ISO/IEC 15416 grades this parameter,
	// and forty per cent is its grade C, the usual floor for retail.
	MinSymbolContrast = 0.40
	// MaxInkFraction is the most of the paper's own reflectance that the
	// ink may reflect, which is the standard's minimum-reflectance rule.
	MaxInkFraction = 0.5
)

// CheckContrast reports whether bars of one colour on paper of another
// can be read, judging both by the red light a scanner uses. Only the
// red component of each colour is consulted, for the reason above.
//
// The judgement is deliberately coarse. It works from the colours a
// template names and cannot know what the ink, the substrate, or the
// printer will do to them, so it catches the combinations that cannot work
// in principle -- yellow bars, a navy background -- and nothing subtler.
// A symbol going into production still wants verifying off a printed label.
func CheckContrast(inkRed, paperRed uint8) error {
	ink, paper := redReflectance(inkRed), redReflectance(paperRed)
	if paper-ink < MinSymbolContrast {
		return fmt.Errorf(
			"the bars reflect %.0f%% of red light and the background %.0f%%, "+
				"a difference of %.0f%% where a scanner needs %.0f%%",
			ink*100, paper*100, (paper-ink)*100, MinSymbolContrast*100)
	}
	if ink > paper*MaxInkFraction {
		return fmt.Errorf(
			"the bars reflect %.0f%% of red light against the background's %.0f%%, "+
				"and a scanner needs them under %.0f%%",
			ink*100, paper*100, paper*MaxInkFraction*100)
	}
	return nil
}

// redReflectance is how much red light a colour returns, from its sRGB
// red component. Reflectance is a linear quantity and sRGB is not, so
// the component is linearised first by the sRGB transfer function.
func redReflectance(component uint8) float64 {
	value := float64(component) / 255
	if value <= 0.04045 {
		return value / 12.92
	}
	return math.Pow((value+0.055)/1.055, 2.4)
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
