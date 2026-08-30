package sr

import (
	"path/filepath"
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
