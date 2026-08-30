package sr

import (
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a1s/sr/printout"
)

// barcodeTemplate wraps one barcode declaration in a report
// that is otherwise the smallest thing that builds.
func barcodeTemplate(declaration string) string {
	return `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=300 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    title height=120 {
      ` + declaration + `
    }
    detail height=10 { field expr="n" format="row %d" left=0 width=100 height=10 }
  }
}`
}

// onlyBarcode returns the single barcode mark of a built printout.
func onlyBarcode(test *testing.T, out *Printout) *printout.Barcode {
	test.Helper()
	var found *printout.Barcode
	for _, page := range out.Pages {
		for _, mark := range page.Marks {
			if barcode, ok := mark.(*printout.Barcode); ok {
				if found != nil {
					test.Fatal("more than one barcode mark")
				}
				found = barcode
			}
		}
	}
	if found == nil {
		test.Fatal("no barcode mark")
	}
	return found
}

// leadingQuietRows counts the wholly light rows a matrix opens with.
// A light row is one run, because runs start light and alternate.
func leadingQuietRows(rows [][]int) int {
	count := 0
	for _, row := range rows {
		if len(row) != 1 {
			break
		}
		count++
	}
	return count
}

// The margin each two-dimensional symbology requires reaches the printout.
// The encoder returns the bare symbol, so nothing but the engine puts these
// modules there, and a symbol drawn flush to its neighbours does not scan.
func TestTwoDQuietZonesReachThePrintout(test *testing.T) {
	cases := []struct {
		kind  string
		quiet int
	}{
		{"QR-L", 4},
		{"QR-H", 4},
		{"DataMatrix", 1},
		{"Aztec", 0},
	}
	for _, testCase := range cases {
		test.Run(testCase.kind, func(test *testing.T) {
			out := buildString(test, barcodeTemplate(
				`barcode type="`+testCase.kind+`" text="Ver1" left=0`), rowsOf(1))
			mark := onlyBarcode(test, out)
			if len(mark.Rows) == 0 {
				test.Fatal("a two-dimensional symbol must carry rows")
			}
			if got := leadingQuietRows(mark.Rows); got != testCase.quiet {
				test.Errorf("leading light rows = %d, want a quiet zone of %d",
					got, testCase.quiet)
			}
			// The margin is in the geometry too, not only in the runs.
			if got := len(mark.Rows); float64(got)*mark.Module-mark.Box.Height > 0.001 {
				test.Errorf("%d rows at %g do not fill the %g box",
					got, mark.Module, mark.Box.Height)
			}
		})
	}
}

// A one-dimensional symbol opens with its quiet zone directly:
// runs start light, so nothing precedes it.
func TestStripesOpenWithTheQuietZone(test *testing.T) {
	out := buildString(test, barcodeTemplate(
		`barcode type="Code128" text="Code 128" left=0`), rowsOf(1))
	mark := onlyBarcode(test, out)
	if len(mark.Stripes) == 0 {
		test.Fatal("no stripes")
	}
	if mark.Stripes[0] != 10 {
		test.Errorf("stripes open %v, want a light quiet zone of 10", mark.Stripes[:2])
	}
}

// Ink and paper reach the printout as canonical colours.
func TestBarcodeColoursReachThePrintout(test *testing.T) {
	out := buildString(test, barcodeTemplate(
		`barcode type="Code128" text="Code 128" left=0 ink="navy" paper="yellow"`),
		rowsOf(1))
	mark := onlyBarcode(test, out)
	if mark.Ink != "#000080" {
		test.Errorf("ink = %q, want navy", mark.Ink)
	}
	if mark.Paper == nil || *mark.Paper != "#FFFF00" {
		test.Errorf("paper = %v, want yellow", mark.Paper)
	}
}

// Saying nothing about colour gives black bars and no background, which
// is what every template written before the properties existed means.
func TestBarcodeDefaultsToBlackOnNothing(test *testing.T) {
	out := buildString(test, barcodeTemplate(
		`barcode type="Code128" text="Code 128" left=0`), rowsOf(1))
	mark := onlyBarcode(test, out)
	if mark.Ink != "#000000" {
		test.Errorf("ink = %q, want black", mark.Ink)
	}
	if mark.Paper != nil {
		test.Errorf("paper = %v, want none", *mark.Paper)
	}
}

// Both serialisations carry the colours and the quiet zone.
// A printout is meant to be archived and rendered later, so
// what survives the file is what the format actually promises.
func TestBarcodeSurvivesBothEncodings(test *testing.T) {
	out := buildString(test, barcodeTemplate(
		`barcode type="QR-H" text="Ver1" left=0 ink="navy" paper="yellow"`), rowsOf(1))
	before := onlyBarcode(test, out)
	dir := test.TempDir()
	for _, name := range []string{"report.srp.cbor", "report.srp.jsonl"} {
		path := filepath.Join(dir, name)
		if err := out.WriteFile(path); err != nil {
			test.Fatal(err)
		}
		back, err := printout.ReadFile(path)
		if err != nil {
			test.Fatal(err)
		}
		mark := onlyBarcode(test, back)
		if mark.Ink != "#000080" {
			test.Errorf("%s: ink = %q, want navy", name, mark.Ink)
		}
		if mark.Paper == nil || *mark.Paper != "#FFFF00" {
			test.Errorf("%s: paper = %v, want yellow", name, mark.Paper)
		}
		if len(mark.Rows) != len(before.Rows) {
			test.Errorf("%s: %d rows, want %d", name, len(mark.Rows), len(before.Rows))
		}
	}
}

// A deferred 2-D barcode whose resolved value encodes to a smaller matrix
// than its placeholder must shrink on both axes. Only the coding direction
// used to be updated, which left the mark claiming a cross extent its rows
// no longer filled -- and, once `paper` existed, painting a background past
// the symbol.
func TestDeferredMatrixShrinksOnBothAxes(test *testing.T) {
	placeholder := strings.Repeat("9", 130)
	out := buildString(test, barcodeTemplate(
		`barcode type="QR-L" expr="FINAL.REPORT_COUNT" format="%04d" `+
			`text="`+placeholder+`" evaltime="report" `+
			`paper="white" left=0 width=150 height=150`), rowsOf(1, 2, 3))
	mark := onlyBarcode(test, out)
	if mark.Value != "0003" {
		test.Fatalf("value = %q, want the resolved 0003", mark.Value)
	}
	want := float64(len(mark.Rows)) * mark.Module
	if math.Abs(mark.Box.Height-want) > 0.001 {
		test.Errorf("box is %g across and %d rows at %g measure %g",
			mark.Box.Height, len(mark.Rows), mark.Module, want)
	}
	if math.Abs(mark.Box.Width-want) > 0.001 {
		test.Errorf("box is %g along and the symbol measures %g", mark.Box.Width, want)
	}
}

// fieldTemplate wraps one field declaration in a title band, ahead
// of a detail band that only exists because a layout needs one.
func fieldTemplate(declaration string) string {
	return `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=300 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    title height=120 {
      ` + declaration + `
    }
    detail height=10 { field expr="n" format="row %d" left=0 width=100 height=10 }
  }
}`
}

// A deferred barcode is aligned by the symbol it resolves to,
// not by the placeholder that reserved room for it. The same declaration
// without a deferral produces exactly that placeholder box, so the edge
// the alignment names has to be the same in both.
func TestDeferredBarcodeStaysAligned(test *testing.T) {
	placeholder := strings.Repeat("9", 130)
	const geometry = ` type="QR-L" left=0 width=150 top=0 height=100 `
	cases := []struct {
		name, align string
		// edge is the coordinate the alignment pins.
		edge func(box printout.Box) float64
	}{
		{"halign right", `halign="right"`,
			func(box printout.Box) float64 { return box.Left + box.Width }},
		{"halign center", `halign="center"`,
			func(box printout.Box) float64 { return box.Left + box.Width/2 }},
		{"valign bottom", `valign="bottom"`,
			func(box printout.Box) float64 { return box.Top + box.Height }},
		{"valign center", `valign="center"`,
			func(box printout.Box) float64 { return box.Top + box.Height/2 }},
	}
	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			reserved := onlyBarcode(test, buildString(test, barcodeTemplate(
				`barcode`+geometry+testCase.align+` text="`+placeholder+`"`),
				rowsOf(1, 2, 3))).Box
			resolved := onlyBarcode(test, buildString(test, barcodeTemplate(
				`barcode`+geometry+testCase.align+
					` expr="FINAL.REPORT_COUNT" format="%04d" evaltime="report"`+
					` text="`+placeholder+`"`), rowsOf(1, 2, 3))).Box
			if resolved.Width >= reserved.Width {
				test.Fatalf("the resolved symbol did not shrink: %g wide against %g",
					resolved.Width, reserved.Width)
			}
			got := testCase.edge(resolved)
			want := testCase.edge(reserved)
			if math.Abs(got-want) > 0.001 {
				test.Errorf("aligned edge = %g, want %g", got, want)
			}
		})
	}
}

// The same for a deferred field, whose box spans the slot horizontally --
// so only the vertical can drift, and `align` handles the rest at render time.
func TestDeferredFieldStaysAligned(test *testing.T) {
	const placeholder = "9999 9999 9999 9999 9999 9999 9999 9999"
	const geometry = ` left=0 width=60 top=0 height=100 `
	for _, align := range []string{`valign="bottom"`, `valign="center"`} {
		test.Run(align, func(test *testing.T) {
			reserved := texts(buildString(test, fieldTemplate(
				`field`+geometry+align+` text="`+placeholder+`"`),
				rowsOf(1, 2, 3)).Pages[0])[0]
			resolved := texts(buildString(test, fieldTemplate(
				`field`+geometry+align+
					` expr="FINAL.REPORT_COUNT" format="%d" evaltime="report"`+
					` text="`+placeholder+`"`), rowsOf(1, 2, 3)).Pages[0])[0]
			if len(resolved.Lines) >= len(reserved.Lines) {
				test.Fatalf("the resolved text did not shrink: %d lines against %d",
					len(resolved.Lines), len(reserved.Lines))
			}
			// Measured from the lines themselves, not from the box: the
			// box moving is the fix, and what a reader sees is where the
			// text ends up. Without it the box keeps the placeholder's
			// top and the shorter text simply rides up inside it.
			edge := func(mark *printout.Text) float64 {
				extent := float64(len(mark.Lines)) * mark.Leading
				if align == `valign="bottom"` {
					return mark.Box.Top + extent
				}
				return mark.Box.Top + extent/2
			}
			if got, want := edge(resolved), edge(reserved); math.Abs(got-want) > 0.001 {
				test.Errorf("aligned edge = %g, want %g", got, want)
			}
		})
	}
}
