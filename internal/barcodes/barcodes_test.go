package barcodes

import (
	"strconv"
	"strings"
	"testing"
)

func TestEveryTypeEncodes(test *testing.T) {
	cases := []struct {
		kind  string
		value string
		twoD  bool
	}{
		{"Code128", "Code 128", false},
		{"Code39", "CODE 39", false},
		{"Code93", "Code 93", false},
		{"2of5i", "9999", false},
		{"DataMatrix", "Data Matrix", true},
		{"Aztec", "Aztec Code 2D :)", true},
		{"QR-L", "Ver1", true},
		{"QR-M", "Version 2", true},
		{"QR-Q", "Version 2 (25x25)", true},
		{"QR-H", "high", true},
	}
	for _, testCase := range cases {
		test.Run(testCase.kind, func(test *testing.T) {
			sym, err := Encode(testCase.kind, testCase.value)
			if err != nil {
				test.Fatal(err)
			}
			if sym.TwoD != testCase.twoD {
				test.Errorf("TwoD = %v, want %v", sym.TwoD, testCase.twoD)
			}
			if sym.Value != testCase.value {
				test.Errorf("Value = %q", sym.Value)
			}
			if testCase.twoD {
				if len(sym.Rows) == 0 || sym.CrossModules != len(sym.Rows) {
					test.Errorf("rows = %d, cross = %d", len(sym.Rows), sym.CrossModules)
				}
				for index, row := range sym.Rows {
					total := 0
					for _, run := range row {
						total += run
					}
					if total != sym.Modules {
						test.Errorf("row %d runs sum to %d, want %d", index, total, sym.Modules)
					}
				}
				checkTwoDQuietZone(test, testCase.kind, sym)
				return
			}
			if len(sym.Stripes) == 0 {
				test.Fatal("no stripes")
			}
			total := 0
			for _, stripe := range sym.Stripes {
				total += stripe
			}
			if total != sym.Modules {
				test.Errorf("stripes sum to %d, want Modules = %d", total, sym.Modules)
			}
			// Runs start light, so a one-dimensional symbol opens
			// with its quiet zone and needs nothing in front of it.
			if sym.Stripes[0] != QuietModules {
				test.Errorf("leading quiet zone = %d", sym.Stripes[0])
			}
			if sym.Stripes[len(sym.Stripes)-1] != QuietModules {
				test.Errorf("trailing quiet zone = %d", sym.Stripes[len(sym.Stripes)-1])
			}
		})
	}
}

func TestContentErrors(test *testing.T) {
	cases := []struct {
		kind, value, want string
	}{
		{"2of5i", "999", "odd number"},
		{"2of5i", "12a4", "not a digit"},
		{"Code39", "lower", "A-Z upper case"},
		{"Code128", "café", "outside ASCII"},
		{"Code128", "", "empty"},
	}
	for _, testCase := range cases {
		_, err := Encode(testCase.kind, testCase.value)
		if err == nil {
			test.Errorf("%s %q: want an error", testCase.kind, testCase.value)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, testCase.want) {
			test.Errorf("%s %q: %v; want a mention of %q",
				testCase.kind, testCase.value, err, testCase.want)
		}
		// Every diagnostic names the type and the value.
		if !strings.Contains(msg, testCase.kind) ||
			(testCase.value != "" && !strings.Contains(msg, testCase.value)) {
			test.Errorf("%s %q: the diagnostic must name the type and the value: %v",
				testCase.kind, testCase.value, err)
		}
	}
}

func TestRunsFromBits(test *testing.T) {
	cases := []struct {
		name  string
		bits  []bool
		quiet int
		want  []int
	}{
		// Index 0 is light, so a pattern that opens dark spends a
		// zero-length light run saying so, and one that opens light
		// does not have to.
		{"opens dark", []bool{true, true, false, true}, 0, []int{0, 2, 1, 1}},
		{"opens light", []bool{false, true}, 0, []int{1, 1}},
		{"with a quiet zone", []bool{true, false, true}, 10, []int{10, 1, 1, 1, 10}},
		{"ending light with a quiet zone", []bool{true, false}, 10, []int{10, 1, 11}},
	}
	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			got := runsFromBits(testCase.bits, testCase.quiet)
			if len(got) != len(testCase.want) {
				test.Fatalf("got %v, want %v", got, testCase.want)
			}
			for index := range got {
				if got[index] != testCase.want[index] {
					test.Fatalf("got %v, want %v", got, testCase.want)
				}
			}
		})
	}
}

func TestMeasure(test *testing.T) {
	oneD, err := Encode("Code128", "Code 128")
	if err != nil {
		test.Fatal(err)
	}
	const module = 0.72 // 10 mil

	// Without grow, the symbol takes its minimum bar height.
	metrics := Measure(oneD, module, false, 1000, 1000)
	if want := float64(oneD.Modules) * module; metrics.Length != want {
		test.Errorf("Length = %v, want %v", metrics.Length, want)
	}
	wantCross := metrics.Length * MinHeightRatio
	if wantCross < MinHeightPt {
		wantCross = MinHeightPt
	}
	if metrics.Cross != wantCross {
		test.Errorf("Cross = %v, want %v", metrics.Cross, wantCross)
	}
	// grow expands the bar height to the box, and leaves the module alone.
	metrics = Measure(oneD, module, true, 1000, 500)
	if metrics.Cross != 500 || metrics.Module != module {
		test.Errorf("grown 1-D: cross = %v, module = %v", metrics.Cross, metrics.Module)
	}

	twoD, err := Encode("QR-Q", "Version 2 (25x25)")
	if err != nil {
		test.Fatal(err)
	}
	metrics = Measure(twoD, module, false, 1000, 1000)
	if metrics.Module != module || metrics.Length != float64(twoD.Modules)*module {
		test.Errorf("ungrown 2-D: %+v", metrics)
	}
	// grow recomputes the module for a 2-D type, keeping it square.
	metrics = Measure(twoD, module, true, 64.8, 64.8)
	if want := 64.8 / float64(twoD.Modules); metrics.Module != want {
		test.Errorf("grown 2-D module = %v, want %v", metrics.Module, want)
	}
	if metrics.Length != metrics.Cross {
		test.Errorf("a 2-D symbol is square: %v by %v", metrics.Length, metrics.Cross)
	}
	// grow never shrinks below the declared module.
	metrics = Measure(twoD, module, true, 1, 1)
	if metrics.Module != module {
		test.Errorf("grow must not shrink the module: %v", metrics.Module)
	}
}

// The invariant a printout asserts: stripes sums, times module,
// equal the box extent along the coding direction.
func TestStripeSumMatchesTheExtent(test *testing.T) {
	sym, err := Encode("2of5i", "9999")
	if err != nil {
		test.Fatal(err)
	}
	const module = 0.54 // 7.5 mil, as sakila writes it
	metrics := Measure(sym, module, false, 0, 0)
	total := 0
	for _, stripe := range sym.Stripes {
		total += stripe
	}
	if got := float64(total) * metrics.Module; got != metrics.Length {
		test.Errorf("stripes x module = %v, extent = %v", got, metrics.Length)
	}
}

// checkTwoDQuietZone asserts the margin a two-dimensional symbology
// requires is actually there, on all four sides.
//
// The encoder this package wraps returns the bare symbol, so nothing
// but Encode puts these modules in and nothing but this notices if they
// stop arriving.
func checkTwoDQuietZone(test *testing.T, kind string, sym *Symbol) {
	test.Helper()
	quiet := quietZone(kind)
	if quiet == 0 {
		// Aztec asks for no margin, so its rows may legitimately begin
		// and end dark and there is nothing here to assert.
		return
	}
	if len(sym.Rows) < 2*quiet+1 {
		test.Fatalf("%d rows cannot hold a quiet zone of %d", len(sym.Rows), quiet)
	}
	// The top and bottom bands are wholly light, which one run says.
	for index := 0; index < quiet; index++ {
		for _, row := range [][]int{sym.Rows[index], sym.Rows[len(sym.Rows)-1-index]} {
			if len(row) != 1 || row[0] != sym.Modules {
				test.Errorf("quiet row %d = %v, want one light run of %d",
					index, row, sym.Modules)
			}
		}
	}
	// Every data row opens and closes light, by at least the margin.
	for index := quiet; index < len(sym.Rows)-quiet; index++ {
		row := sym.Rows[index]
		if row[0] < quiet {
			test.Errorf("row %d opens with %d light modules, want %d", index, row[0], quiet)
		}
		// A row ends light only when it has an even number of runs,
		// the last one being the odd-indexed dark run's light successor.
		last := len(row) - 1
		if last%2 != 0 || row[last] < quiet {
			test.Errorf("row %d ends %v, want a light run of at least %d", index, row, quiet)
		}
	}
}

func TestQuietZonesMatchTheStandards(test *testing.T) {
	// Four modules for QR, one for Data Matrix, none for Aztec.
	cases := map[string]int{
		"QR-L": 4, "QR-M": 4, "QR-Q": 4, "QR-H": 4,
		"DataMatrix": 1, "Aztec": 0,
		"Code128": QuietModules, "Code39": QuietModules,
	}
	for kind, want := range cases {
		if got := quietZone(kind); got != want {
			test.Errorf("quietZone(%q) = %d, want %d", kind, got, want)
		}
	}
}

// redOf reads the red component out of a "#RRGGBB" string,
// which is the only part of a colour the contrast check consults.
func redOf(test *testing.T, hex string) uint8 {
	test.Helper()
	value, err := strconv.ParseUint(hex[1:3], 16, 8)
	if err != nil {
		test.Fatalf("bad colour %q: %v", hex, err)
	}
	return uint8(value)
}

func TestCheckContrast(test *testing.T) {
	// Whole colours, so the table shows what it is judging. Several of these
	// share a red component and therefore a verdict -- black, navy and dark
	// green are all simply "no red" to a scanner -- which is the point the
	// function makes rather than a gap in the table.
	cases := []struct {
		ink, paper string
		wantOK     bool
	}{
		{"#000000", "#FFFFFF", true},  // black on white
		{"#000080", "#FFFFFF", true},  // navy bars
		{"#654321", "#FFFFFF", true},  // brown bars
		{"#800080", "#FFFFFF", true},  // purple bars
		{"#006400", "#FFFFFF", true},  // dark green bars
		{"#000000", "#FFFF00", true},  // yellow paper
		{"#000000", "#FFC0CB", true},  // pink paper
		{"#000000", "#FFA500", true},  // orange paper
		{"#000000", "#FF0000", true},  // red paper reflects red light fully
		{"#FFFF00", "#FFFFFF", false}, // yellow bars reflect it as fully
		{"#FF0000", "#FFFFFF", false}, // and so do red ones
		{"#D3D3D3", "#FFFFFF", false}, // light grey: too little difference
		{"#000000", "#000080", false}, // navy paper absorbs red
		{"#000000", "#006400", false}, // so does dark green
		{"#FFFFFF", "#000000", false}, // inverted
		{"#000080", "#654321", false}, // dark bars on a dark background
	}
	for _, testCase := range cases {
		err := CheckContrast(redOf(test, testCase.ink), redOf(test, testCase.paper))
		if (err == nil) != testCase.wantOK {
			test.Errorf("ink %s on paper %s: error = %v, want ok = %v",
				testCase.ink, testCase.paper, err, testCase.wantOK)
		}
	}
}
