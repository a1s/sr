package pdf

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/a1s/sr/internal/fontres"
	"github.com/a1s/sr/internal/pdfscan"
	"github.com/a1s/sr/printout"
)

// The committed fonts, so that no test outcome depends on
// what is installed on the machine.
const (
	goRegular = "../example/fonts/Go-Regular.ttf"
	goBold    = "../example/fonts/Go-Bold.ttf"
)

// tolerance is the slack a comparison against a rendered coordinate allows:
// the printout rounds to three decimals and so does the file,
// so anything beyond that is a real disagreement.
const tolerance = 0.0015

// document builds a one-page printout with one font at ten points.
func document(marks ...printout.Mark) *printout.Printout {
	return &printout.Printout{
		Header: printout.Header{
			SR:          printout.Version,
			Kind:        "header",
			Built:       "2026-08-04T09:12:44Z",
			Engine:      "sr test",
			StrictFonts: true,
			Page: printout.PageGeometry{
				Width: 300, Height: 200,
				LeftMargin: 10, RightMargin: 10, TopMargin: 10, BottomMargin: 10,
			},
			Fonts: []printout.FontEntry{{
				Name: "body", Size: 10,
				ResolvedFile: goRegular, ResolvedFace: "Go", ResolvedBy: "explicit",
			}},
			Data: map[string]*printout.Blob{},
		},
		Pages: []*printout.Page{{Kind: "page", Number: 1, Marks: marks}},
	}
}

// text builds a text mark with the defaults the engine would emit.
func text(box printout.Box, align string, lines ...string) *printout.Text {
	mark := printout.NewText()
	mark.Box = box
	mark.Font = "body"
	mark.Color = "#000000"
	mark.Align = align
	mark.Leading = 12
	mark.Lines = lines
	return mark
}

// render renders a printout and reads it back.
func render(test *testing.T, doc *printout.Printout, options ...Option) ([]*pdfscan.Page, *pdfscan.File) {
	test.Helper()
	raw := renderBytes(test, doc, options...)
	file, err := pdfscan.Read(raw)
	if err != nil {
		test.Fatalf("reading the rendered file: %v", err)
	}
	pages, err := file.ReadPages()
	if err != nil {
		test.Fatalf("replaying the content streams: %v", err)
	}
	return pages, file
}

func renderBytes(test *testing.T, doc *printout.Printout, options ...Option) []byte {
	test.Helper()
	var out bytes.Buffer
	if err := Write(doc, &out, options...); err != nil {
		test.Fatalf("rendering: %v", err)
	}
	return out.Bytes()
}

// face loads a font the way the renderer does,
// for computing what a measurement ought to be.
func face(test *testing.T, path string, size int) *fontres.Face {
	test.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		test.Fatal(err)
	}
	loaded, err := fontres.Load(raw, 0, size)
	if err != nil {
		test.Fatal(err)
	}
	return loaded
}

func near(test *testing.T, what string, got, want float64) {
	test.Helper()
	if math.Abs(got-want) > tolerance {
		test.Errorf("%s = %.4f, want %.4f", what, got, want)
	}
}

// A rendered file is a PDF: a version header, one page,
// and a trailer that leads to a catalog.
func TestFileStructure(test *testing.T) {
	doc := document(text(printout.Box{Left: 10, Top: 10, Width: 100, Height: 12},
		"left", "Hello"))
	raw := renderBytes(test, doc)

	if !bytes.HasPrefix(raw, []byte("%PDF-1.7\n")) {
		test.Errorf("the file begins %q", raw[:min(12, len(raw))])
	}
	if !bytes.HasSuffix(raw, []byte("%%EOF\n")) {
		test.Errorf("the file ends %q", raw[max(0, len(raw)-16):])
	}
	file, err := pdfscan.Read(raw)
	if err != nil {
		test.Fatal(err)
	}
	if file.Catalog() == nil {
		test.Fatal("the trailer leads to no catalog")
	}
	pages := file.Pages()
	if len(pages) != 1 {
		test.Fatalf("pages = %d, want 1", len(pages))
	}
	if got, ok := file.Number(pages[0], "MediaBox"); ok {
		test.Errorf("MediaBox is a number: %v", got)
	}
}

// Every alignment positions the line from the width the engine measured it
// with, so the drawn line falls exactly where the box says.
func TestAlignment(test *testing.T) {
	const line = "Alignment"
	box := printout.Box{Left: 20, Top: 30, Width: 120, Height: 12}
	metrics := face(test, goRegular, 10)
	width := metrics.Width(line)

	for _, row := range []struct {
		align string
		left  float64
	}{
		{"left", box.Left},
		{"center", box.Left + (box.Width-width)/2},
		{"right", box.Left + box.Width - width},
	} {
		pages, _ := render(test, document(text(box, row.align, line)))
		if len(pages[0].Runs) != 1 {
			test.Fatalf("%s: runs = %d, want 1", row.align, len(pages[0].Runs))
		}
		run := pages[0].Runs[0]
		if run.Text != line {
			test.Errorf("%s: text = %q, want %q", row.align, run.Text, line)
		}
		near(test, row.align+" left", run.Left, row.left)
		// The width the file states for the run is the width
		// the engine measured, to within the thousandths of an em
		// the font dictionary expresses widths in.
		near(test, row.align+" width", run.Width, width)
	}
}

// The baseline sits with the face's em extent centred in the leading the
// printout reserved, and every line after the first is one leading down.
func TestBaselinePlacement(test *testing.T) {
	box := printout.Box{Left: 10, Top: 20, Width: 200, Height: 36}
	pages, _ := render(test, document(text(box, "left", "one", "two", "three")))
	runs := pages[0].Runs
	if len(runs) != 3 {
		test.Fatalf("runs = %d, want 3", len(runs))
	}

	metrics := face(test, goRegular, 10)
	ascender, descender := metrics.VerticalMetrics()
	scale := 10 / metrics.Upem()
	ascent, descent := ascender*scale, descender*scale
	first := box.Top + (12-(ascent+descent))/2 + ascent

	for index, run := range runs {
		near(test, "baseline", run.Baseline, first+float64(index)*12)
	}
	if runs[1].Baseline-runs[0].Baseline != runs[2].Baseline-runs[1].Baseline {
		test.Errorf("the baselines are not evenly spaced: %v", runs)
	}
}

// A justified line spends its slack on the gaps between words,
// so the last word ends at the box's right edge and the spaces
// are still in the text.
//
// The line under test is the first of two, since the last line
// of a paragraph is not justified.
func TestJustification(test *testing.T) {
	box := printout.Box{Left: 20, Top: 20, Width: 150, Height: 24}
	mark := text(box, "justified", "one two three", "tail")
	pages, _ := render(test, document(mark))
	runs := pages[0].Runs
	if len(runs) != 4 {
		test.Fatalf("runs = %d, want one per word plus the last line: %v",
			len(runs), runs)
	}
	runs = runs[:3]
	if got := pages[0].Text(); got != "one two threetail" {
		test.Errorf("text = %q, want both lines with their spaces", got)
	}
	near(test, "first segment", runs[0].Left, box.Left)
	last := runs[2]
	near(test, "right edge", last.Left+last.Width, box.Left+box.Width)

	// The gaps are equal, which is what distributing the slack evenly
	// means.
	oneGap := runs[1].Left - (runs[0].Left + runs[0].Width)
	twoGap := runs[2].Left - (runs[1].Left + runs[1].Width)
	near(test, "even gaps", oneGap, twoGap)
	if oneGap <= 0 {
		test.Errorf("the gaps did not grow: %.3f", oneGap)
	}
}

// The last line of a justified paragraph is set flush left, unless the
// band was split and the paragraph continues on the next frame.
func TestJustifiedLastLine(test *testing.T) {
	box := printout.Box{Left: 20, Top: 20, Width: 150, Height: 24}
	metrics := face(test, goRegular, 10)

	mark := text(box, "justified", "one two three", "four five")
	pages, _ := render(test, document(mark))
	tail := pages[0].Runs[len(pages[0].Runs)-1]
	got := tail.Left + tail.Width
	want := box.Left + metrics.Width("four five")
	if math.Abs(got-want) > tolerance {
		test.Errorf("an unsplit last line was justified: ends at %.3f, want %.3f", got, want)
	}

	mark = text(box, "justified", "one two three", "four five")
	mark.LastLineJustified = true
	pages, _ = render(test, document(mark))
	tail = pages[0].Runs[len(pages[0].Runs)-1]
	near(test, "continued last line", tail.Left+tail.Width, box.Left+box.Width)
}

// A justified line with no room to grow, or with no gap to grow at,
// is left alone rather than stretched.
func TestJustificationDegenerate(test *testing.T) {
	metrics := face(test, goRegular, 10)
	word := "unbreakable"
	box := printout.Box{Left: 20, Top: 20, Width: 150, Height: 12}
	mark := text(box, "justified", word)
	mark.LastLineJustified = true
	pages, _ := render(test, document(mark))
	if len(pages[0].Runs) != 1 {
		test.Fatalf("runs = %d, want 1", len(pages[0].Runs))
	}
	run := pages[0].Runs[0]
	near(test, "one-word line", run.Left, box.Left)
	near(test, "one-word width", run.Width, metrics.Width(word))
}

// A line mark runs corner to corner of its box, the other diagonal when
// it is backslanted, and carries the stroke width and dash it declares.
func TestLines(test *testing.T) {
	rule := printout.NewLine()
	rule.Box = printout.Box{Left: 10, Top: 20, Width: 100, Height: 30}
	rule.Width = 1.5
	rule.Dash = "dash"
	rule.Color = "#FF0000"

	back := printout.NewLine()
	back.Box = rule.Box
	back.Dash = "solid"
	back.Color = "#000000"
	back.Backslant = true

	pages, _ := render(test, document(rule, back))
	if len(pages[0].Segments) != 2 {
		test.Fatalf("segments = %d, want 2", len(pages[0].Segments))
	}

	one := pages[0].Segments[0]
	near(test, "from left", one.FromLeft, 10)
	near(test, "from top", one.FromTop, 20)
	near(test, "to left", one.ToLeft, 110)
	near(test, "to top", one.ToTop, 50)
	near(test, "stroke width", one.LineWidth, 1.5)
	if one.Stroke != [3]float64{1, 0, 0} {
		test.Errorf("stroke = %v, want red", one.Stroke)
	}
	want := []float64{3, 2}
	if len(one.Dash) != 2 || one.Dash[0] != want[0] || one.Dash[1] != want[1] {
		test.Errorf("dash = %v, want %v", one.Dash, want)
	}

	two := pages[0].Segments[1]
	near(test, "backslant from top", two.FromTop, 50)
	near(test, "backslant to top", two.ToTop, 20)
	if len(two.Dash) != 0 {
		test.Errorf("a solid line carries a dash pattern: %v", two.Dash)
	}
	// A width of zero reaches the file as zero, which is PDF's
	// own name for the thinnest line the device draws.
	near(test, "hairline", two.LineWidth, 0)
}

// A rectangle strokes and fills independently, and an absent stroke
// draws no outline however wide it was declared.
func TestRectangles(test *testing.T) {
	both := printout.NewRectangle()
	both.Box = printout.Box{Left: 10, Top: 10, Width: 50, Height: 20}
	both.Width = 2
	both.Dash = "solid"
	stroke, fill := "#0000FF", "#00FF00"
	both.Stroke, both.Fill = &stroke, &fill

	fillOnly := printout.NewRectangle()
	fillOnly.Box = printout.Box{Left: 70, Top: 10, Width: 50, Height: 20}
	fillOnly.Width = 3
	fillOnly.Fill = &fill

	neither := printout.NewRectangle()
	neither.Box = printout.Box{Left: 130, Top: 10, Width: 50, Height: 20}
	neither.Width = 3

	pages, _ := render(test, document(both, fillOnly, neither))
	rects := pages[0].Rects
	if len(rects) != 2 {
		test.Fatalf("rectangles = %d, want 2: a rectangle with neither"+
			" a stroke nor a fill draws nothing", len(rects))
	}
	if rects[0].Painted != "fillstroke" {
		test.Errorf("painted = %q, want fillstroke", rects[0].Painted)
	}
	near(test, "left", rects[0].Left, 10)
	near(test, "top", rects[0].Top, 10)
	near(test, "width", rects[0].Width, 50)
	near(test, "height", rects[0].Height, 20)
	if rects[0].Stroke != [3]float64{0, 0, 1} {
		test.Errorf("stroke = %v, want blue", rects[0].Stroke)
	}
	if rects[1].Painted != "fill" {
		test.Errorf("painted = %q, want fill", rects[1].Painted)
	}
}

// A radius turns the corners into curves, so the path is no longer
// a rectangle operator.
func TestRoundedRectangle(test *testing.T) {
	rect := printout.NewRectangle()
	rect.Box = printout.Box{Left: 10, Top: 10, Width: 50, Height: 20}
	rect.Radius = 4
	stroke := "#000000"
	rect.Stroke = &stroke

	pages, _ := render(test, document(rect))
	if len(pages[0].Rects) != 0 {
		test.Errorf("a rounded rectangle was drawn with the rectangle operator")
	}
	if len(pages[0].Segments) != 1 {
		test.Fatalf("the rounded path was not stroked: %v", pages[0].Segments)
	}
}

// Bars come out of the printout's stripe list as filled rectangles,
// and they span the symbol exactly.
func TestBarcodeStripes(test *testing.T) {
	code := printout.NewBarcode()
	code.Type = "Code128"
	code.Value = "42"
	code.Module = 2
	// A quiet zone, then two bars with a space between:
	// 10 + 3 + 1 + 2 = 16 modules. Runs start light,
	// so the quiet zone is the first of them.
	code.Stripes = []int{10, 3, 1, 2}
	code.Box = printout.Box{Left: 20, Top: 20, Width: 32, Height: 18}

	pages, _ := render(test, document(code))
	rects := pages[0].Rects
	if len(rects) != 2 {
		test.Fatalf("bars = %d, want 2", len(rects))
	}
	near(test, "first bar left", rects[0].Left, 20+2*10)
	near(test, "first bar width", rects[0].Width, 6)
	near(test, "first bar height", rects[0].Height, 18)
	near(test, "second bar left", rects[1].Left, 20+2*14)
	near(test, "second bar width", rects[1].Width, 4)
	for _, rect := range rects {
		if rect.Fill != [3]float64{0, 0, 0} {
			test.Errorf("a bar is not black: %v", rect.Fill)
		}
	}
}

// Ink colours the bars, and paper is laid under the whole box first,
// so the quiet zone is the colour the template asked for rather than
// whatever the band left underneath.
func TestBarcodeInkAndPaper(test *testing.T) {
	code := printout.NewBarcode()
	code.Type = "Code128"
	code.Value = "42"
	code.Module = 2
	code.Stripes = []int{10, 3, 1, 2}
	code.Box = printout.Box{Left: 20, Top: 20, Width: 32, Height: 18}
	code.Ink = "#000080"
	paper := "#FFFF00"
	code.Paper = &paper

	pages, _ := render(test, document(code))
	rects := pages[0].Rects
	if len(rects) != 3 {
		test.Fatalf("rectangles = %d, want the paper and two bars", len(rects))
	}
	// The paper comes first and covers the box, quiet zone included.
	near(test, "paper left", rects[0].Left, 20)
	near(test, "paper width", rects[0].Width, 32)
	near(test, "paper height", rects[0].Height, 18)
	if rects[0].Fill != [3]float64{1, 1, 0} {
		test.Errorf("paper fill = %v, want yellow", rects[0].Fill)
	}
	// Navy: the PDF carries the component to three decimals.
	for index, rect := range rects[1:] {
		near(test, fmt.Sprintf("bar %d blue", index), rect.Fill[2], 128.0/255)
		if rect.Fill[0] != 0 || rect.Fill[1] != 0 {
			test.Errorf("bar %d is not navy: %v", index, rect.Fill)
		}
	}
}

// Without paper nothing is laid down behind the symbol, which is
// what a template that says nothing about the background means.
func TestBarcodeWithoutPaperDrawsNoBackground(test *testing.T) {
	code := printout.NewBarcode()
	code.Type = "Code128"
	code.Value = "42"
	code.Module = 2
	code.Stripes = []int{10, 3, 1, 2}
	code.Box = printout.Box{Left: 20, Top: 20, Width: 32, Height: 18}

	pages, _ := render(test, document(code))
	if got := len(pages[0].Rects); got != 2 {
		test.Errorf("rectangles = %d, want the two bars alone", got)
	}
}

// A matrix symbol draws one row of runs per module of height,
// and a vertical one is the same symbol turned a quarter turn clockwise.
func TestBarcodeMatrix(test *testing.T) {
	code := printout.NewBarcode()
	code.Type = "QR-L"
	code.Value = "x"
	code.Module = 3
	// Runs start light: the first row is wholly dark, so it opens with
	// a zero-length light run, and the second is one light then one dark.
	code.Rows = [][]int{{0, 2}, {1, 1}}
	code.Box = printout.Box{Left: 10, Top: 10, Width: 6, Height: 6}

	pages, _ := render(test, document(code))
	rects := pages[0].Rects
	if len(rects) != 2 {
		test.Fatalf("dark runs = %d, want 2", len(rects))
	}
	near(test, "row 0 left", rects[0].Left, 10)
	near(test, "row 0 top", rects[0].Top, 10)
	near(test, "row 0 width", rects[0].Width, 6)
	near(test, "row 0 height", rects[0].Height, 3)
	near(test, "row 1 left", rects[1].Left, 13)
	near(test, "row 1 top", rects[1].Top, 13)

	code.Vertical = true
	pages, _ = render(test, document(code))
	rects = pages[0].Rects
	if len(rects) != 2 {
		test.Fatalf("dark runs = %d, want 2", len(rects))
	}
	// Turned clockwise: the coding direction runs down the page
	// and the rows advance leftward from the box's right edge.
	near(test, "vertical row 0 left", rects[0].Left, 13)
	near(test, "vertical row 0 top", rects[0].Top, 10)
	near(test, "vertical row 0 width", rects[0].Width, 3)
	near(test, "vertical row 0 height", rects[0].Height, 6)
}

// An underlined font draws a rule under each line, below the baseline.
func TestUnderline(test *testing.T) {
	doc := document(text(printout.Box{Left: 10, Top: 10, Width: 100, Height: 12},
		"left", "under"))
	doc.Header.Fonts[0].Underline = true

	pages, _ := render(test, doc)
	if len(pages[0].Rects) != 1 {
		test.Fatalf("rules = %d, want one under the line", len(pages[0].Rects))
	}
	rule := pages[0].Rects[0]
	run := pages[0].Runs[0]
	if rule.Top <= run.Baseline {
		test.Errorf("the underline is at %.3f, not below the baseline at %.3f",
			rule.Top, run.Baseline)
	}
	near(test, "underline width", rule.Width, run.Width)
	if rule.Painted != "fill" {
		test.Errorf("the underline is %q, want a fill", rule.Painted)
	}
}
